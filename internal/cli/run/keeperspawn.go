package run

// keeperspawn.go is the FRESH LAUNCH'S SIDE of the keeper, and every session's quit
// (docs/design/jail-lifetime-last-session-wins.md §9.1, §9.3, §4.5; step 3 of its §7).
//
// A fresh container launch does everything that needs the terminal as it always did: the
// pre-flights, the approval, the build, the staging and every disclosure. Then it counts itself as
// the jail's first session, spawns the keeper with its plan, relays what the keeper and pid 1 print
// until the boot is done, and enters its first session by exec, exactly as an attach does. When that
// session ends, it is an ordinary session's quit: the launch lets its lock go and finds out what it
// left behind, a jail its other sessions keep, or the teardown its keeper runs, which it streams
// (JL-D11). Nothing in it waits on the container or a runtime client, so the prompt comes back as
// soon as the agent is gone.
//
// ITS SIGNALS. Before ready, a SIGINT, SIGHUP or SIGTERM ends the launch alone: it closes the lifeline,
// which is the keeper's signal to unwind what it started, relays the unwind for a moment, and exits.
// From ready on, it is the session's arm an attach runs under (sessionhangup.go): it hangs up the
// session's own processes in the jail, and never stops the jail (JL-D4, as OQ-JL8 ruled); on a SIGINT
// or a SIGTERM it then says, as a quit does, that the jail stays up for other sessions in it (JL-D76).
// One arm for the whole window, retargeted at ready, so no signal falls between two.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// keeperSpawner starts a keeper for launch with a plan file and its descriptors (the launch lock
// may be nil, and reserved, the jail's reserved ports, empty) and returns a wait for its exit
// status. The spawner's copies of the descriptors are the caller's to close once it returns.
type keeperSpawner func(launch *Options, planPath string, progress, lifeline, lock *os.File,
	reserved []*os.File) (wait func() int, err error)

// defaultKeeperSpawner is how every fresh launch spawns its keeper: realSpawnKeeper. A package's
// tests swap it for a keeper run in-process, since a test binary must never self-exec as the keeper
// (it would run the package's whole suite in a detached child).
var defaultKeeperSpawner keeperSpawner = realSpawnKeeper

// SelfExecPath is what a child that must be this very binary is exec'd from, as the keeper is: on
// Linux /proc/self/exe, which still names it after `just install` replaced its file (JL-D5), and
// elsewhere its path (execx.SelfExecArgv), or the bare "yolo" when that cannot be read. A patched
// fork's child build jail is the other caller (cli's forkbuildchild.go).
func SelfExecPath() string {
	if exe := keeperSelfExe(); exe != "" {
		return exe
	}
	return execx.SelfExecArgv([]string{"yolo"})[0]
}

// realSpawnKeeper is the production spawner: the running binary, in a session of its own, stdio on
// /dev/null, the three descriptors as fds 3 to 5 and the reserved ports after them
// (startDetached's shape, JL-D29). On Linux it is exec'd from /proc/self/exe, which still names
// this binary after `just install` replaced its file (JL-D5); elsewhere from its path, and a
// keeper of another build refuses the plan.
func realSpawnKeeper(_ *Options, planPath string, progress, lifeline, lock *os.File,
	reserved []*os.File) (func() int, error) {
	argv := keeperArgv(planPath, lock != nil, len(reserved))
	if exe := keeperSelfExe(); exe != "" {
		argv[0] = exe
	} else {
		argv = execx.SelfExecArgv(argv)
	}
	if flag.Lookup("test.v") != nil {
		return nil, errTestBinarySelfExec
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.ExtraFiles = []*os.File{progress, lifeline}
	if lock != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, lock)
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, reserved...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() int { return exitCodeOf(cmd.Wait()) }, nil
}

// keeperProcess is a spawned keeper, as the fresh launch holds it.
type keeperProcess struct {
	// lifeline is the write end only this launch holds: its close, by the launch or by the launch's
	// death, is the keeper's signal to unwind before ready.
	lifeline *os.File
	progress *os.File
	exited   chan struct{}
	exitCode int
	closeMu  sync.Once
	// ready is closed once this launch's relay has read the keeper's ready frame (relay): from then
	// on the keeper ends the jail on its session count, and no longer on the lifeline.
	ready     chan struct{}
	readyOnce sync.Once
	// logFrom is the keeper log's length the ready frame carried, and logFromKnown whether it
	// carried one: set before ready closes (sessionLogFrom).
	logFrom      int64
	logFromKnown bool
	// uncounted is the plan's Uncounted: a keeper that never ends the jail on the count (JL-P3).
	uncounted bool
}

// closeLifeline closes the lifeline once.
func (k *keeperProcess) closeLifeline() { k.closeMu.Do(func() { _ = k.lifeline.Close() }) }

// relay is relayKeeper over this keeper's progress pipe, which also notes the ready frame on k: for
// the teardown before ready that a signal can still run once the keeper is ready
// (keeperPreReadyTeardown), and the keeper log's length the frame carried, for the first session's
// quit (sessionLogFrom).
func (k *keeperProcess) relay(out, errOut, jailOut, jailErr io.Writer, ev keeperEvents) bool {
	then := ev.ready
	ev.ready = func(logFrom int64, known bool) {
		k.readyOnce.Do(func() {
			k.logFrom, k.logFromKnown = logFrom, known
			close(k.ready)
		})
		if then != nil {
			then(logFrom, known)
		}
	}
	return relayKeeper(k.progress, out, errOut, jailOut, jailErr, ev)
}

// sessionLogFrom is the offset in the keeper's log the first session's quit replays from: the log's
// length at the ready frame, which the keeper took under the lock its every line's writes take, so
// a line the relay did not print is past it (keeperSink.sayReady, JL-D78). A stat of the log here
// would be behind every line the keeper logged since the frame, which then reached neither the
// relay nor the quit. A frame that carried no length, from a keeper with no log it could measure,
// falls back to that stat. Called once the relay saw ready.
func (k *keeperProcess) sessionLogFrom(cname string) int64 {
	if k.logFromKnown {
		return k.logFrom
	}
	return keeperLogSize(cname)
}

// startKeeper writes the plan and spawns the keeper, handing it the launch lock this launch holds
// (JL-D31): the keeper's copy keeps the lock, and this one is closed without the unlock that would
// release every copy (workspaceLock.handOff).
//
// UNDER THE LAUNCH GUARD'S HOLD (launchguard.go, JL-D75): a signal whose teardown began first
// spawns nothing (errLaunchEnded), and one that lands during the spawn waits for it, so it ends the
// jail through the keeper rather than discarding what the keeper was just handed.
func (o *Options) startKeeper(plan *keeperPlan) (kp *keeperProcess, err error) {
	if !o.launchGuard.lockSpawn() {
		return nil, errLaunchEnded
	}
	defer func() { o.launchGuard.unlockSpawn(kp) }()
	planPath, err := writeKeeperPlan(plan)
	if err != nil {
		return nil, fmt.Errorf("could not write this jail's plan for its keeper: %w", err)
	}
	progR, progW, err := os.Pipe()
	if err != nil {
		removeKeeperPlan(planPath)
		return nil, err
	}
	lifeR, lifeW, err := os.Pipe()
	if err != nil {
		removeKeeperPlan(planPath)
		_ = progR.Close()
		_ = progW.Close()
		return nil, err
	}
	// THE LOCK THE KEEPER HOLDS UNTIL READY: the workspace launch lock at a container backend
	// (JL-D31), and the key's arrival lock at macos-user (keeperHandOff).
	handed, reserved, release := o.keeperHandOff(plan)
	var lockFile *os.File
	if handed != nil && !handed.isClosed() {
		lockFile = handed.f
	}
	// THE JAIL'S RESERVED PORTS GO TO THE KEEPER (NC-D70; servedaddresses.go): it fronts the jail's
	// host services, each on a port-0 listener, before it starts the container whose daemons bind
	// them, so it holds them until then. At macos-user they are the doorways' and launch-owned
	// services' own, which the keeper hands each one at its start. This launch's own copies close
	// once the keeper has its own, or once it could not be spawned.
	wait, err := defaultKeeperSpawner(o, planPath, progW, lifeR, lockFile, reserved)
	release()
	_ = progW.Close()
	_ = lifeR.Close()
	if err != nil {
		removeKeeperPlan(planPath)
		_ = progR.Close()
		_ = lifeW.Close()
		return nil, fmt.Errorf("could not start this jail's keeper: %w", err)
	}
	if lockFile != nil {
		handed.handOff()
	}
	kp = &keeperProcess{lifeline: lifeW, progress: progR, exited: make(chan struct{}),
		ready: make(chan struct{}), uncounted: plan.Uncounted}
	go func() {
		kp.exitCode = wait()
		close(kp.exited)
	}()
	return kp, nil
}

// keeperPlanFor is the plan a fresh launch hands its keeper: the values it computed and disclosed
// for the jail's host services and its container (JL-D20). in is the argv's assembly input, whose
// image ref and skeleton the argv names.
func (o *Options) keeperPlanFor(cfg *jsonx.OrderedMap, rt, cname string, staged stagedPacks,
	services []string, payload []loopholes.JailDaemonSpec, forwards []PortForward,
	forwardDir, socketsDir string, runCmd []string, in *assembleInput) (*keeperPlan, error) {
	raw, err := encodeConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("the config does not encode for this jail's keeper: %w", err)
	}
	names := make([]string, 0, len(staged.packs))
	for _, p := range staged.packs {
		names = append(names, p.Name)
	}
	if len(forwards) == 0 {
		forwardDir = ""
	}
	return &keeperPlan{
		Build: keeperBuildStamp(), Workspace: o.Workspace, Cname: cname, Runtime: rt,
		Network: o.Network, Color: o.streamColor(), Config: raw,
		PackTree: staged.root, Packs: names, Services: services,
		Settings: o.frozenLoopholeSettings(), SettingsPrepared: o.settingsPrepared,
		Payload:        payload,
		ApprovedScopes: o.approvedScopes, Forwards: forwards, ForwardDir: forwardDir,
		SocketsDir: socketsDir, RunCmd: runCmd, ImageRef: in.imageRef, Skeleton: in.homeSkeleton,
		ScratchVolumes: o.scratchVolumes, PerfRecording: o.timingRecording(), Sealed: o.Sealed,
		// THE JAIL'S GRANT (jailgrant.go): its NAMES, for the start record an attach reads. Its
		// values never ride the plan: the launch wrote them into the jail's grant file
		// (stageJailGrant), which the argv binds (ES-D37).
		Grant: o.jailGrant,
	}, nil
}

// streamColor is whether this launch renders color on its stream, which is what the printer asks
// (Options.pr): the keeper's lines reach this terminal through the relay, so it renders them the same.
func (o *Options) streamColor() bool {
	return o.Color && tty.Color(o.Getenv, true, o.IsTTYStdout())
}

// unwindUnspawned is a fresh launch that got as far as its records and no further: no keeper
// started, so no container ever held its skeleton or pack tree, and it takes them back itself, as
// the runtime-not-found branch did before there was a keeper. After the launch lock is released,
// which forgetGoneContainer takes non-blocking.
func (o *Options) unwindUnspawned(cname, rt, packTree, skeleton string) {
	discardUnheldSkeleton(cname, skeleton)
	forgetLivePackTree(cname, packTree)
	discardPackTree(cname, packTree)
	o.packTreeHeld = false
	o.releaseLaunchLock()
	o.forgetGoneContainer(cname, rt, skeleton)
}

// awaitPreviousKeeper is an arrival that found a keeper still ending this workspace's previous jail
// (JL-D28): it releases the launch lock, says why it waits, and waits for the keeper to be gone,
// within a bound, after which it is refused with what it waited on (JL-D34). The caller takes the
// launch lock again and decides from the top.
func (o *Options) awaitPreviousKeeper(cname string) bool {
	o.releaseLaunchLock()
	o.keeperDrainSeen = false
	who := "its keeper"
	if rec, ok := readKeeperRecord(cname); ok {
		who = fmt.Sprintf("its keeper (pid %d)", rec.PID)
	}
	o.pr(o.Stdout).printf("[bold cyan]The previous jail of this workspace (%s) is still shutting "+
		"down; waiting for %s to finish...[/bold cyan]", cname, who)
	sp := o.Perf.Span("launch.await_previous_keeper")
	gone := waitForKeeper(cname, 0, keeperTeardownWait, nil, nil)
	sp.End()
	if !gone {
		o.noteKeeperStillRunning(cname, keeperTeardownWait)
		o.pr(o.Stderr).printf("[bold red]Refusing to launch: the previous jail's keeper is still " +
			"running.[/bold red]")
	}
	return gone
}

// keeperUnwindWait bounds how long a launch interrupted before ready relays its keeper's unwind
// before it exits. A var so a test need not wait it out.
var keeperUnwindWait = 20 * time.Second

// keeperPreReadyTeardown is the launch arm's teardown until the jail is ready: close the lifeline,
// which ends the keeper's jail, let the first session's count go, relay the unwind for a moment,
// release the herdr pane, and give the terminal back.
//
// THE COUNT GOES TOO (JL-D74). The arm runs this until the launch's own goroutine retargets it,
// which is after the keeper said ready, and the keeper stops reading the lifeline at ready: a signal
// in between found a keeper waiting for its last session's count, and that count was this
// launch's, held until the process exited. The wait below then ran its whole bound for a keeper
// that could not end the jail until it gave up. With the count let go here, the keeper drains as
// for any last session, and the wait ends when it does; before ready the keeper is not counting,
// and the lifeline ends the jail as it always did. Past ready this launch is a session that never
// began, so the wait is a session's quit (awaitKeeperUnwind).
func (o *Options) keeperPreReadyTeardown(kp *keeperProcess, cname, rt string) func() {
	return func() {
		o.Perf.Mark("terminate.signal")
		kp.closeLifeline()
		o.releaseSessionLock()
		o.awaitKeeperUnwind(kp, cname, rt)
		// The report prints HERE, because the arm os.Exit(128+n)s the moment this returns and no
		// statement after it will run; then the terminal, so the launch's last words land in the
		// tab that ran it.
		o.emitTimingReport(0, cname, rt)
		o.releaseHerdrAgent()
		o.restoreTerminal()
	}
}

// keeperUnwindPoll is how often awaitKeeperUnwind looks at the session count once the keeper is ready.
const keeperUnwindPoll = 50 * time.Millisecond

// awaitKeeperUnwind is the teardown's wait for its keeper's end, within keeperUnwindWait. Before
// ready the keeper ends the jail on the lifeline, and the wait relays that. Once the relay has read
// ready the keeper ends it on the count instead, so the wait is a session's quit (JL-D74): it goes
// on only while the keeper is ending the jail, and a jail it keeps up is said, as endSession says
// it, and not waited for. It keeps one up for another session still in it (a session's shared hold
// on the count), and for an uncounted first session (JL-P3), on which it never drains. On a SIGHUP
// the jail is not said to stay up, by either line: the pane that would read it is gone, as for the
// session's teardown (endingOnAHangup, JL-D76). The wait still ends there.
func (o *Options) awaitKeeperUnwind(kp *keeperProcess, cname, rt string) {
	bound := time.After(keeperUnwindWait)
	ready := kp.ready
	var poll <-chan time.Time
	for {
		select {
		case <-kp.exited:
			return
		case <-bound:
			return
		case <-ready:
			ready = nil
			if kp.uncounted {
				if !endingOnAHangup() {
					o.pr(terminalOnly(o.Stderr)).printf("[dim]Jail %s stays up: its keeper could not count this "+
						"session, so it does not end the jail when its sessions leave; %s ends it.[/dim]",
						cname, stopRemedy(rt, cname))
				}
				return
			}
			poll = time.After(0)
		case <-poll:
			if othersInJail(cname) {
				if !endingOnAHangup() {
					o.noteJailStaysUp(cname, rt)
				}
				return
			}
			poll = time.After(keeperUnwindPoll)
		}
	}
}

// othersInJail reports whether a session holds cname's session lock shared: one is still in the
// jail, which its keeper keeps up. Called once this launch has let its own hold go.
func othersInJail(cname string) bool {
	f, err := openSessionLock(cname)
	if err != nil {
		return false
	}
	defer f.Close()
	return readSessionLockHolder(f) == heldShared
}

// keeperLine is the keeper's disclosure (JL-D21): one line, before the spawn, naming what it will
// hold, that it ends with the last session, and where it logs. A launch has no quiet mode, and a
// process that outlives the terminal it was started from is exactly what one must name.
func (o *Options) keeperLine(cname string, services []string, forwards int, rt string) string {
	var held []string
	for _, s := range services {
		held = append(held, "the "+s+" service")
	}
	switch forwards {
	case 0:
	case 1:
		held = append(held, "1 port forward")
	default:
		held = append(held, fmt.Sprintf("%d port forwards", forwards))
	}
	what := "this jail's container"
	if len(held) > 0 {
		what = "this jail's host services (" + strings.Join(held, ", ") + ") and its container"
	}
	return fmt.Sprintf("keeper: yolo internal daemon %s will hold %s until its last session leaves; "+
		"log: %s", KeeperVerb, what, keeperLogPath(cname))
}

// terminalOnly is w without the launch.log tee: what a session streams from the keeper's log is
// already in launch.log, which the keeper mirrors its own lines into.
func terminalOnly(w io.Writer) io.Writer {
	if t, ok := w.(teeLog); ok {
		return t.w
	}
	return w
}

// endSession is every session's quit, the fresh launch's first session included: why its jail ended
// under it when it did, then its own session lock, then what it left behind (the design's §4.5):
//
//   - other sessions remain: one line, and the prompt at once;
//   - it was the last: the keeper is ending the jail, and the session streams that teardown until the
//     keeper is gone, within a bound (JL-D11, JL-D34);
//   - the keeper is dead (an UNKEPT jail): the last session to quit reaps the jail itself (JL-D30), and
//     one that is not the last says so.
//
// rc is the session's exec status and since the moment it began; logFrom is the keeper log's length
// then, so it prints only what the keeper recorded while it was in (JL-D19). first is the fresh
// launch's own session, whose status for a jail a recorded stop ended stays 143, as it was when the
// first session was the container's main process (JL-D50, JL-D59). The status returned is the
// launch's.
func (o *Options) endSession(cname, rt string, rc int, since time.Time, logFrom int64, first bool) int {
	sp := o.Perf.Span("attach.why_the_jail_ended")
	reason, ended := o.whyTheJailEnded(cname, rt, rc, since)
	sp.End()
	o.reportJailEnded(rt, reason, ended)
	o.releaseSessionLock()
	if ended {
		if reason != "" && first {
			rc = 128 + int(syscall.SIGTERM)
		}
		if reason == "" {
			o.maybeWarnAboutOOMKiller(rc, rt)
		}
		return rc
	}
	sp = o.Perf.Span("shutdown.oom_check")
	o.maybeWarnAboutOOMKiller(rc, rt)
	sp.End()

	sp = o.Perf.Span("session.after_quit")
	state, locks := o.probeAfterQuit(cname)
	sp.End()
	out := o.pr(terminalOnly(o.Stderr))
	switch state {
	case quitOthers:
		o.printKeeperRecords(cname, logFrom)
		o.noteJailStaysUp(cname, rt)
	case quitLast:
		o.streamKeeperTeardown(cname, logFrom)
	case quitUnkeptLast:
		o.reapUnkeptJail(cname, rt)
		locks.release()
	case quitUnkeptOthers:
		out.printf("[yellow]This jail's keeper is gone, so its host services have been down since it "+
			"died; the last of its other sessions to quit ends %s, or %s does now.[/yellow]",
			cname, stopRemedy(rt, cname))
	case quitUnknown:
		out.printf("[dim]Could not tell whether other sessions remain in %s; it may be ending.[/dim]", cname)
	}
	return rc
}

// noteJailStaysUp is the line of a session's quit that leaves others in its jail: it stays up for
// them, how to re-enter it, and how to end it. The fresh launch's teardown before ready prints it
// too (awaitKeeperUnwind), and a session a SIGINT or a SIGTERM ends prints it without the count
// (noteJailStaysUpOnSignal).
func (o *Options) noteJailStaysUp(cname, rt string) {
	others := othersUncounted
	if n, ok := o.jailSessionCount(rt, cname); ok && n > 0 {
		others = fmt.Sprintf("%d other %s", n, plural(n, "session", "sessions"))
	}
	o.sayJailStaysUpFor(cname, rt, others)
}

// othersUncounted names a jail's other sessions without a number, for when nothing can count them.
const othersUncounted = "its other sessions"

// sayJailStaysUpFor prints noteJailStaysUp's line, naming the sessions the jail stays up for as
// others.
func (o *Options) sayJailStaysUpFor(cname, rt, others string) {
	o.pr(terminalOnly(o.Stderr)).printf("[dim]Jail %s stays up for %s; `yolo -- <agent>` re-enters it, and %s ends them all.[/dim]",
		cname, others, stopRemedy(rt, cname))
}

// printKeeperRecords prints what the keeper logged since logFrom, on the terminal: its log holds the
// jail's own events once it is ready (a service's death, why it is ending), and a session is shown
// those that happened while it was in.
func (o *Options) printKeeperRecords(cname string, logFrom int64) {
	f := &keeperLogFollower{path: keeperLogPath(cname), offset: logFrom}
	out := o.pr(terminalOnly(o.Stderr))
	for _, l := range f.next() {
		out.print("[dim]" + richtext.Escape(l) + "[/dim]")
	}
}

// streamKeeperTeardown is the last session's wait: the keeper is ending the jail, and the session
// shows its teardown as it happens, from the keeper's log, until the keeper is gone (JL-D11). A
// Ctrl-C stops the stream and leaves the keeper running; the bound says what it is waiting on
// (JL-D34).
func (o *Options) streamKeeperTeardown(cname string, logFrom int64) {
	out := o.pr(terminalOnly(o.Stderr))
	stop := make(chan struct{})
	var once sync.Once
	end := func() { once.Do(func() { close(stop) }) }
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, syscall.SIGINT)
	defer signal.Stop(interrupts)
	interrupted := make(chan struct{})
	go func() {
		select {
		case <-interrupts:
			close(interrupted)
			end()
		case <-stop:
		}
	}()
	sp := o.Perf.Span("session.keeper_teardown")
	gone := waitForKeeper(cname, logFrom, keeperTeardownWait, func(l string) {
		out.print("[dim]  " + richtext.Escape(l) + "[/dim]")
	}, stop)
	sp.End()
	end()
	select {
	case <-interrupted:
		if !gone {
			out.printf("[dim]Stopped following the teardown; %s's keeper goes on ending it.[/dim]", cname)
			return
		}
	default:
	}
	if !gone {
		o.noteKeeperStillRunning(cname, keeperTeardownWait)
	}
}

// noteKeeperStillRunning is what a bounded wait on a keeper says when the bound ran out: the keeper's
// pid, its log and its last line, and how to end it in order (JL-D34).
func (o *Options) noteKeeperStillRunning(cname string, waited time.Duration) {
	out := o.pr(o.Stderr)
	rec, _ := readKeeperRecord(cname)
	last := ""
	f := &keeperLogFollower{path: keeperLogPath(cname)}
	if lines := f.next(); len(lines) > 0 {
		last = lines[len(lines)-1]
	}
	pid := "unknown"
	if rec.PID > 0 {
		pid = fmt.Sprint(rec.PID)
	}
	out.printf("[yellow]The keeper of %s (pid %s) has not finished after %s. Its log: %s. Its last "+
		"line: %q. `kill %s` ends it in order.[/yellow]", cname, pid, waited, keeperLogPath(cname),
		last, pid)
}

// reapUnkeptJail is JL-D30's reap, run by the last session of a jail whose keeper died, holding both
// locks: the keeper's chain, in this process. The names only the keeper knew come from its start
// record; what the record does not name is left for `yolo prune`, as a reaped orphan's is.
func (o *Options) reapUnkeptJail(cname, rt string) {
	o.pr(o.Stderr).printf("[yellow]This jail's keeper is gone, and this was its last session: ending "+
		"%s, and the host-side state its keeper left.[/yellow]", cname)
	o.reapUnkept(cname, rt, true)
}

// reapUnkept is the chain of an unkept jail, stopping it first when stop is set: `yolo stop` has
// stopped it already, and its own record says why.
func (o *Options) reapUnkept(cname, rt string, stop bool) {
	rec, _ := o.keeperEra(cname)
	if stop {
		o.stopJail(cname, rt, unkeptReapReason(o.Getpid(), rec.PID))
	}
	o.awaitContainerGone(cname, rt)
	if rec.PackTree != "" {
		o.packTree, o.packTreeHeld = rec.PackTree, true
	} else {
		o.packTreeHeld = false
	}
	if len(rec.ScratchVolumes) > 0 && rt != "container" { // parity: NotApplicable — Apple Container's scratch dirs are always tmpfs (appleContainerBaseMounts), so it has no volumes to remove
		o.scratchVolumes = rec.ScratchVolumes
		o.scratchRemovalOnce = &sync.Once{}
	}
	sockets := rec.SocketsDir
	if sockets == "" {
		sockets = hostServiceSocketsDir(cname, o.IsMacOS)
	}
	fwd := rec.ForwardDir
	if fwd == "" && (rt == "container" || !o.IsMacOS) { // parity: HonoredBy — the fresh path's own rule for where host ports are forwarded through socket files; a macOS podman jail forwards none
		fwd = o.fwdSocketDir(cname)
	}
	o.teardownAfterExit(nil, fwd, nil, sockets, cname, rt, rec.Skeleton, 0)
	clearOwnerPIDIf(cname, rec.PID)
	if rec.Scope != "" {
		o.Exec([]string{"systemctl", "--user", "stop", rec.Scope}, "", nil, 10*time.Second)
	}
	removeKeeperRecord(cname, rec.PID)
}

// awaitContainerGone waits, bounded, for no container of cname to exist, removing a stopped leftover.
func (o *Options) awaitContainerGone(cname, rt string) {
	for i := 0; i < restartPollAttempts; i++ {
		if id, known := o.probeExistingContainer(cname, rt, trackingProbeTimeout); known && id == "" {
			return
		}
		time.Sleep(restartPollInterval)
	}
	if id, known := o.probeRunningContainer(cname, rt, trackingProbeTimeout); known && id == "" {
		_ = o.removeStaleContainer(cname, rt)
	}
}

// unkeptReapReason is why an unkept jail's last session, or `yolo stop`, ends it.
func unkeptReapReason(pid, keeper int) string {
	return fmt.Sprintf("its keeper (pid %d) was gone, and yolo (pid %d) ended it with its last session", keeper, pid)
}

// KeeperLogOffset is the length of the workspace's jail keeper's log now: `yolo stop` notes it
// before its stop, so FinishStop streams only the teardown that stop causes.
func KeeperLogOffset(workspace string) int64 {
	return keeperLogSize(yoloruntime.FromWorkspace(workspace))
}

// KeeperAlive reports whether a keeper holds the workspace's jail, or cannot be told not to.
func KeeperAlive(workspace string) bool {
	return probeKeeper(yoloruntime.FromWorkspace(workspace)) != keeperGone
}

// LaunchedRuntime is the runtime the workspace's jail was launched on, as its keeper's start record
// names it; ok is false when no record names one. The record stays while the keeper lives and after
// one that died, so it is there for every jail a keeper started that is not known gone. `yolo stop`
// asks this runtime ahead of the one the config and YOLO_RUNTIME resolve (JL-D79): a jail launched
// with YOLO_RUNTIME=container in a workspace whose default is podman runs where podman cannot see it.
func LaunchedRuntime(workspace string) (string, bool) {
	rec, ok := readKeeperRecord(yoloruntime.FromWorkspace(workspace))
	if !ok || rec.Runtime == "" {
		return "", false
	}
	return rec.Runtime, true
}

// FinishStop is `yolo stop`'s second half at a container backend, once the runtime has stopped the
// jail (docs/design/jail-lifetime-last-session-wins.md JL-D25, JL-D30, JL-D34):
//
//   - a keeper holds the jail: the stop returns only once its teardown is done, streaming it from the
//     keeper's log from logFrom, as a last session's quit does, within a bound; a Ctrl-C stops the
//     stream and leaves the keeper running;
//   - the jail's keeper is gone (an UNKEPT jail): nothing else will tear the host side down, so this
//     runs the keeper's chain itself, holding the liveness lock and, once the sessions its stop ended
//     have let theirs go, the session lock;
//   - a jail an older yolo started: its own launcher tears it down, as `yolo stop` always left it to.
//
// capture is the E3 config capture the CLI wires into a launch. The status is `yolo stop`'s.
func FinishStop(stdout, stderr io.Writer, workspace, rt string, logFrom int64, capture func(workspace, rt string)) int {
	o := NewDefaultOptions()
	o.Workspace = workspace
	o.Stdout, o.Stderr = stdout, stderr
	o.PerfLoggingConfig = func() bool { return false }
	o.CaptureOnTerminate = capture
	fillDefaults(&o)
	o.runtime = rt
	return o.finishStop(yoloruntime.FromWorkspace(workspace), rt, logFrom)
}

func (o *Options) finishStop(cname, rt string, logFrom int64) int {
	out := o.pr(o.Stdout)
	if probeKeeper(cname) != keeperGone {
		out.printf("Waiting for the jail's keeper to finish its teardown...")
		stop := make(chan struct{})
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, syscall.SIGINT)
		defer signal.Stop(interrupts)
		var once sync.Once
		var mu sync.Mutex
		interrupted := false
		end := func() { once.Do(func() { close(stop) }) }
		go func() {
			select {
			case <-interrupts:
				mu.Lock()
				interrupted = true
				mu.Unlock()
				end()
			case <-stop:
			}
		}()
		gone := waitForKeeper(cname, logFrom, keeperTeardownWait, func(l string) {
			out.print("[dim]  " + richtext.Escape(l) + "[/dim]")
		}, stop)
		end()
		mu.Lock()
		wasInterrupted := interrupted
		mu.Unlock()
		if wasInterrupted && !gone {
			out.printf("[dim]Stopped following the teardown; %s's keeper goes on ending it.[/dim]", cname)
			return 128 + int(syscall.SIGINT)
		}
		if !gone {
			o.noteKeeperStillRunning(cname, keeperTeardownWait)
			return 1
		}
		return 0
	}
	if _, era := o.keeperEra(cname); !era {
		return 0
	}
	live, err := holdLivenessLock(cname)
	if err != nil {
		return 0 // a keeper, or another reaper, took it in between: theirs to finish
	}
	defer releaseLock(live)
	var sessions *sessionLock
	deadline := time.Now().Add(keeperSessionsWait)
	for {
		if s, ok := tryExclusiveSessionLock(cname); ok {
			sessions = s
			break
		}
		if !time.Now().Before(deadline) {
			o.pr(o.Stderr).printf("[yellow]A session of %s still holds its session lock after %s, so its "+
				"host-side state stays for the next launch.[/yellow]", cname, keeperSessionsWait)
			return 1
		}
		time.Sleep(keeperPoll)
	}
	defer sessions.release()
	out.printf("[dim]The jail's keeper was gone; ending its host-side state too.[/dim]")
	o.reapUnkept(cname, rt, false)
	return 0
}

// WaitForKeeper blocks until no keeper holds the workspace's jail, or until a session is seen still
// in the jail, or timeout passes. A keeper outlives the launch that spawned it: when that launch's
// session was the last and was killed rather than quit, nothing waits for the teardown the keeper
// then runs, which writes into the workspace (the config capture, launch.log) and the machine's
// state. Its caller is the integration suite, which must not delete a workspace a keeper is still
// tearing down.
//
// A SESSION STILL IN THE JAIL ends the wait at once: the keeper is rightly keeping the jail up for
// it, and not ending it, and that session's own launch waits again once it has gone (the suite's
// cleanups run last-registered first, so a foreground entry's wait runs before the release of a
// background session started earlier).
func WaitForKeeper(workspace string, timeout time.Duration) error {
	cname := yoloruntime.FromWorkspace(workspace)
	deadline := time.Now().Add(timeout)
	for {
		if probeKeeper(cname) == keeperGone || sessionsRemain(cname) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the keeper of %s is still running after %s", cname, timeout)
		}
		time.Sleep(keeperPoll)
	}
}

// sessionsRemain reports whether some session holds cname's session lock shared, rather than the
// keeper holding it to drain the jail (readSessionLockHolder).
func sessionsRemain(cname string) bool {
	f, err := openSessionLock(cname)
	if err != nil {
		return false
	}
	defer f.Close()
	return readSessionLockHolder(f) == heldShared
}

// ─── A macos-user LAUNCH AT ITS KEY (docs/design/jail-lifetime-last-session-wins.md §9.9) ───
//
// A macos-user launch has no container to attach to, so a second terminal in a workspace is a
// second launch of it. What every launch of the workspace starts outside the sandbox (the fronts,
// the doorways, the launch-owned services) used to be started once per launch, on ports and behind
// tokens of that launch's own, while the per-agent env files and `.codex/auth.json` they are named
// in are the workspace's: so a later launch repointed an earlier session's agents at its own
// listeners, which died with it (§9.9.1). Now one KEEPER per key holds them for every session of
// the key, and every session composes against the one set (JL-D38, JL-D39):
//
//   - ARRIVAL (arriveMacosUser), before the channel is composed and under the key's arrival lock:
//     a live keeper is JOINED, its roster read and adopted (seedFromRoster), so the channel
//     composes the keeper's tokens and addresses; a key whose keeper died while sessions still run
//     is UNKEPT, and the arrival is refused naming them and `yolo stop` (JL-D44); a key with
//     neither is a FRESH launch, which keeps the arrival lock.
//   - A FRESH launch that plans anything outside the sandbox discloses it, counts itself, spawns
//     the keeper with the plan and the arrival lock, relays it until ready, and reads its endpoints
//     back from the roster (startMacosUserKeeper). One that plans nothing runs as before, with no
//     keeper and no count (JL-D42).
//   - A JOINER says which keeper it joined, starts nothing, and is refused when it needs a host
//     service the keeper does not run (joinMacosUserKeeper).
//   - Every session's QUIT (endMacosUserSession): one line while others remain, the keeper's
//     teardown streamed when it was the last, or the reap of an unkept key's records (JL-D40).
//
// What runs as the sandbox account or needs root stays each session's: every sudo step, the guest
// supervisor and the sandbox (JL-D38).

// macosUserKeying is one macos-user launch's place at its key.
type macosUserKeying struct {
	key string
	// arrival is the key's arrival lock while this launch holds it: from its arrival until it has
	// joined, until its keeper is ready, or until it finds it needs none.
	arrival *workspaceLock
	// joined is the roster this launch joined; nil for a fresh launch.
	joined *keeperRecord
	// record is this session's record (openKeyedSessionRecord), nil before it is written and after
	// its quit removed it.
	record *os.File
	// kp is the keeper a fresh launch spawned.
	kp *keeperProcess
	// logFrom is the keeper log's length this session's quit replays from.
	logFrom int64
	// seeded is how many of the launch's launch-owned services came from the roster
	// (seedFromRoster): any after them the joiner's own composition planned, which the keeper lacks.
	seeded int
	// handed are the reserved sockets a fresh launch hands its keeper, in keeperPlan.ReservedAddrs'
	// order.
	handed []*os.File
	// keeperless is set for a launch that plans nothing a keeper holds (JL-D42), whose record names
	// it for `yolo stop` and whose quit is only that record's removal.
	keeperless bool
	// waits is, on a dry run, the roster of a keeper that is ending its services, which the launch
	// would wait for before starting its own (peekMacosUserKey); nil otherwise.
	waits *keeperRecord
}

// macosUserKeeperNotch is the notch of this launch's key: the guest notch's own, or macos-user's.
func (o *Options) macosUserKeeperNotch() string {
	if o.atNotch == config.ConfinementGuest {
		return keeperNotchGuest
	}
	return keeperNotchMacosUser
}

// arriveMacosUser is a macos-user launch's arrival at its key, before its channel is composed
// (JL-D44; §9.9.5). It reports false when it refused the launch, having said why. A caller that
// hands Run no backend, and every other runtime, arrive nowhere; a dry run looks without taking a
// lock or counting itself (peekMacosUserKey).
func (o *Options) arriveMacosUser(rt, cname string) bool {
	if rt != "macos-user" || o.MacosUserRun == nil { // parity: NotApplicable — only a macos-user launch has a key of its own; a container launch's keeper is keyed by its jail, under the launch lock
		return true
	}
	m := &macosUserKeying{key: keeperKey(cname, o.macosUserKeeperNotch())}
	o.macosUserKey = m
	if o.DryRun {
		return o.peekMacosUserKey(rt, cname)
	}
	out := o.pr(o.Stderr)
	for {
		m.arrival = o.takeArrivalLock(m.key)
		switch probeKeeper(m.key) {
		case keeperUnknown:
			o.releaseArrivalLock()
			o.refuseUnaskableKey(m.key, rt, cname)
			return false
		case keeperGone:
			sessions, ok := tryExclusiveSessionLock(m.key)
			if !ok {
				o.refuseUnkeptKey(m.key, rt, cname)
				o.releaseArrivalLock()
				return false
			}
			// NO KEEPER AND NO SESSION: this is the fresh launch. A roster a dead keeper left, whose
			// last session died without reaping it, is removed first, as that reap would have.
			if rec, ok := readKeeperRecord(m.key); ok {
				if live, err := holdLivenessLock(m.key); err == nil {
					o.reapKeyRecords(m.key, cname, rec)
					releaseLock(live)
				}
			}
			sessions.release()
			return true
		}
		// A LIVE KEEPER, or a reaper holding a dead one's liveness lock. This launch COUNTS ITSELF
		// FIRST and reads the roster only then, so the roster it decides on was read after its count:
		// a keeper ending its services, and a reaper, mark the roster ending before they wait for the
		// count to go (endKey, markKeyEnding), and so a launch counted beside either one waits for it
		// instead of joining services that are being stopped, or gone. A keeper draining on the count
		// is never counted into (holdSessionLock's keeperDrainSeen).
		o.keeperDrainSeen = false
		o.holdSessionLock(m.key)
		rec, ok := readKeeperRecord(m.key)
		joinable := !o.keeperDrainSeen && ok && rec.Ready && !rec.Ending
		switch {
		case joinable && rec.Contract != keeperRosterContract:
			o.releaseSessionLock()
			o.releaseArrivalLock()
			o.refuseRosterContract(m.key, rt, cname, rec)
			return false
		case joinable:
			if o.sessionLock == nil {
				out.print("[yellow]This workspace's macos-user keeper cannot count this session, so it may " +
					"end the host services under it once the counted sessions leave.[/yellow]")
			}
			o.recordKeyedSession(m.key, true)
			// Before the roster's down list is printed, as an attach takes its offset (JL-D19).
			m.logFrom = keeperLogSize(m.key)
			m.joined = &rec
			o.releaseArrivalLock()
			o.seedFromRoster(rec)
			return true
		case !o.keeperDrainSeen && !ok && keeperRosterPresent(m.key):
			o.releaseSessionLock()
			o.releaseArrivalLock()
			o.refuseUnreadableRoster(m.key, rt, cname)
			return false
		}
		// ENDING, or a keeper whose roster is already gone, which a keeper finishing removes just
		// before it lets its liveness lock go: the arrival lock goes while this launch waits (JL-D28),
		// and the decision is made again from the top.
		o.releaseSessionLock()
		o.releaseArrivalLock()
		o.keeperDrainSeen = false
		if !o.awaitEndingKey(m.key, rec) {
			return false
		}
	}
}

// peekMacosUserKey is a DRY RUN's arrival at its key (§9.9.7): what the launch would find there, read
// without taking the arrival lock or counting itself, so a plan render changes nothing another launch
// decides on. A key the launch would be refused at is refused here too, as a dry run refuses a
// context mount it cannot deliver: the plan would describe a launch that cannot happen. A live
// keeper whose roster this build reads is joined as the launch would join it (seedFromRoster), so
// the plan names the keeper's tokens and addresses rather than ones picked for nothing; a keeper
// that is ending is noted (macosUserKeying.waits). The count here is the kept session records, which
// a read never takes from their sessions, so a session that could not record itself is not seen.
func (o *Options) peekMacosUserKey(rt, cname string) bool {
	m := o.macosUserKey
	switch probeKeeper(m.key) {
	case keeperUnknown:
		o.refuseUnaskableKey(m.key, rt, cname)
		return false
	case keeperGone:
		if len(keptSessions(liveKeyedSessions(m.key))) > 0 {
			o.refuseUnkeptKey(m.key, rt, cname)
			return false
		}
		return true
	}
	rec, ok := readKeeperRecord(m.key)
	switch {
	case ok && rec.Ready && !rec.Ending && rec.Contract != keeperRosterContract:
		o.refuseRosterContract(m.key, rt, cname, rec)
		return false
	case ok && rec.Ready && !rec.Ending:
		m.joined = &rec
		o.seedFromRoster(rec)
	case !ok && keeperRosterPresent(m.key):
		o.refuseUnreadableRoster(m.key, rt, cname)
		return false
	default:
		m.waits = &rec
	}
	return true
}

// takeArrivalLock takes key's arrival lock, saying so when it has to wait for another launch of
// the workspace (acquireWorkspaceLock's notice). A lock that cannot be opened warns and is nil:
// the launch goes on unserialised, as a workspace lock's degraded mode does.
func (o *Options) takeArrivalLock(key string) *workspaceLock {
	out := o.pr(o.Stdout)
	path := arrivalLockPath(key)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	lock, err := acquireWorkspaceLock(path, o.Workspace, lockNotices{
		warn:    func(msg string) { out.printf("[dim]Warning: %s[/dim]", msg) },
		waiting: func(msg string) { out.printf("[bold cyan]%s[/bold cyan]", msg) },
	})
	if err != nil {
		out.printf("[dim]Warning: could not open this workspace's macos-user arrival lock (%s); "+
			"concurrent launches here are not serialised[/dim]", err.Error())
		return nil
	}
	return lock
}

// releaseArrivalLock lets this launch's hold on its key's arrival lock go. Idempotent, and a no-op
// for a launch that holds none or handed it to its keeper.
func (o *Options) releaseArrivalLock() {
	if m := o.macosUserKey; m != nil {
		m.arrival.Close()
	}
}

// awaitEndingKey is an arrival that found key's keeper ending its services: it waits for that
// keeper to be gone, within keeperTeardownWait, and is refused with what it waited on after it.
func (o *Options) awaitEndingKey(key string, rec keeperRecord) bool {
	who := "its keeper"
	if rec.PID > 0 {
		who = fmt.Sprintf("its keeper (pid %d)", rec.PID)
	}
	o.pr(o.Stdout).printf("[bold cyan]This workspace's macos-user host services are still shutting down; "+
		"waiting for %s to finish...[/bold cyan]", who)
	if waitForKeeper(key, 0, keeperTeardownWait, nil, nil) {
		return true
	}
	o.noteKeeperStillRunning(key, keeperTeardownWait)
	o.pr(o.Stderr).print("[bold red]Refusing to launch: this workspace's previous macos-user keeper is " +
		"still running.[/bold red]")
	return false
}

// refuseUnaskableKey is an arrival that could not ask whether a keeper holds its key: "could not ask"
// is never "gone" (JL-P3), so the launch is refused, naming the lock and its remedies.
func (o *Options) refuseUnaskableKey(key, rt, cname string) {
	out := o.pr(o.Stderr)
	out.printf("[bold red]Refusing to launch: could not ask whether a keeper holds this workspace's "+
		"macos-user host services (its lock %s cannot be taken).[/bold red]", livenessLockPath(key))
	out.printf("[dim]Check that file's directory is yours and writable, then launch again; %s ends "+
		"whatever holds it.[/dim]", stopRemedy(rt, cname))
}

// refuseUnreadableRoster is an arrival at a live keeper whose roster is there and cannot be read.
func (o *Options) refuseUnreadableRoster(key, rt, cname string) {
	out := o.pr(o.Stderr)
	out.printf("[bold red]Refusing to launch: a keeper holds this workspace's macos-user host "+
		"services, and its roster %s cannot be read.[/bold red]", keeperRecordPath(key))
	out.printf("[dim]%s ends it, and the next launch starts fresh.[/dim]", capitalize(stopRemedy(rt, cname)))
}

// refuseUnkeptKey is an arrival at an UNKEPT key: its keeper is gone and sessions still run without
// the host services it held. Refused as OQ-JL7 ruled for every notch (JL-D13, JL-D44): it names
// the sessions and the one remedy, and never spawns a replacement keeper, which would be that
// question's rejected repair.
func (o *Options) refuseUnkeptKey(key, rt, cname string) {
	who := "its keeper"
	if rec, ok := readKeeperRecord(key); ok && rec.PID > 0 {
		who = fmt.Sprintf("its keeper (pid %d)", rec.PID)
	}
	o.pr(o.Stderr).printf("[bold red]Refusing to launch: this workspace's macos-user host services are "+
		"gone, because %s is, and %s without them.[/bold red]", who,
		sessionsThat(keptSessions(liveKeyedSessions(key)), "still runs", "still run"))
	o.pr(o.Stderr).printf("[dim]%s ends them and removes what the keeper left; the next launch then "+
		"starts fresh.[/dim]", capitalize(stopRemedy(rt, cname)))
}

// refuseRosterContract is an arrival at a keeper whose roster this build cannot read (JL-D45):
// another build's, in a contract version this one does not know. The key's next fresh launch runs
// this build, so the remedy is to end the key.
func (o *Options) refuseRosterContract(key, rt, cname string, rec keeperRecord) {
	build := rec.Build
	if build == "" {
		build = "an unknown build"
	}
	o.pr(o.Stderr).printf("[bold red]Refusing to launch: this workspace's macos-user host services are held "+
		"by a keeper (pid %d) of yolo %s, whose roster (contract %d) this yolo (%s, contract %d) cannot "+
		"read; %s.[/bold red]", rec.PID, build, rec.Contract, keeperBuildStamp(), keeperRosterContract,
		sessionsThat(keptSessions(liveKeyedSessions(key)), "uses it", "use it"))
	o.pr(o.Stderr).printf("[dim]%s ends them and their keeper; the next launch then starts fresh on "+
		"this yolo.[/dim]", capitalize(stopRemedy(rt, cname)))
}

// recordKeyedSession names this launch's session in key's records (JL-D44), for a refused arrival
// to name and `yolo stop` to signal; kept says it is a session of the key's keeper. A record that
// cannot be written is said, and changes nothing else: the session lock is the count.
func (o *Options) recordKeyedSession(key string, kept bool) {
	f, err := openKeyedSessionRecord(key, o.Getpid(), o.Now(), kept)
	if err != nil {
		o.pr(o.Stderr).printf("[dim]Warning: could not record this macos-user session (%s), so `yolo stop` "+
			"cannot find it to end it.[/dim]", err.Error())
		return
	}
	o.macosUserKey.record = f
}

// otherKeptSessions is the kept sessions of this launch's key other than its own.
func (o *Options) otherKeptSessions() []keyedSession {
	m := o.macosUserKey
	own := ""
	if m.record != nil {
		own = strings.TrimSuffix(m.record.Name(), keyedSessionRecordPending) + ".json"
	}
	var out []keyedSession
	for _, s := range keptSessions(liveKeyedSessions(m.key)) {
		if s.path != own {
			out = append(out, s)
		}
	}
	return out
}

// recordKeeperlessSession is a launch that plans nothing a keeper holds (JL-D42): it runs with no
// keeper and no count, and still records itself, so `yolo stop` from the workspace ends it with the
// rest. A no-op for a launch with no key.
func (o *Options) recordKeeperlessSession() {
	m := o.macosUserKey
	if m == nil {
		return
	}
	m.keeperless = true
	o.recordKeyedSession(m.key, false)
}

// seedFromRoster makes the joined keeper's tokens and addresses this launch's, before its channel
// composes: every caller token it holds, the served address of each doorway it opened, and each
// launch-owned service's plan, so the composition plans none of them again (planMacosUserService
// and its neighbors keep a service already planned). The guest's own jail daemons are not the
// keeper's: they run in each session's sandbox, so their ports are this launch's to pick.
func (o *Options) seedFromRoster(rec keeperRecord) {
	o.adoptRunningCallerTokens(rec.CallerTokens)
	if len(rec.ServedAddresses) > 0 {
		if o.served.moved == nil {
			o.served.moved = map[string]string{}
		}
		for from, to := range rec.ServedAddresses {
			o.served.moved[from] = to
		}
	}
	for _, h := range rec.LaunchServices {
		o.launchServices = append(o.launchServices, h.plan())
	}
	o.macosUserKey.seeded = len(rec.LaunchServices)
}

// reapKeyRecords removes what a dead keeper of key left, holding key's liveness lock: its
// host-services dir, the pack tree its launch handed it, its grant record and its roster, each only
// while the roster still names that keeper (JL-D28 (4), JL-D44). A dir is removed only when it is a
// macos-user session dir of this workspace's container name.
func (o *Options) reapKeyRecords(key, cname string, rec keeperRecord) {
	now, ok := readKeeperRecord(key)
	if !ok || now.PID != rec.PID {
		return
	}
	if dir := rec.SocketsDir; dir != "" && filepath.Dir(dir) == paths.HostServicesBase(o.IsMacOS) &&
		strings.HasPrefix(filepath.Base(dir), paths.HostServicesSessionPrefix(cname)) {
		retireFrontSockets(frontShortHash(dir))
		if err := os.RemoveAll(dir); err != nil {
			o.pr(o.Stderr).printf("[yellow]Warning: could not remove %s, the host-services dir its dead keeper "+
				"left (%v).[/yellow]", dir, err)
		}
	}
	discardPackTree(cname, rec.PackTree)
	_ = os.Remove(keeperGrantedPath(key))
	removeKeeperRecord(key, rec.PID)
}

// macosUserKeeps reports whether this launch plans anything a keeper at its notch holds (JL-D42): a
// front or a fronted daemon (the host services its loopholes start), a doorway, or a launch-owned
// service. One that plans nothing runs as before, with no keeper and no session lock.
func (o *Options) macosUserKeeps(rt string, cfg *jsonx.OrderedMap, doorways []*launchservice.Plan) bool {
	return len(o.plannedLoopholeNames(rt, cfg)) > 0 || len(doorways) > 0 || len(o.launchServices) > 0
}

// macosUserKeeperPlanFor is the plan a fresh macos-user launch hands its keeper: the values it
// computed and disclosed for what the keeper holds (JL-D20), in notch mode (no container), and the
// reserved sockets of each doorway's and service's served addresses, which the spawn hands over.
func (o *Options) macosUserKeeperPlanFor(cfg *jsonx.OrderedMap, rt, cname string, staged stagedPacks,
	services []string, payload []loopholes.JailDaemonSpec, doorways []*launchservice.Plan,
	channel *packChannel) (*keeperPlan, error) {
	raw, err := encodeConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("the config does not encode for this workspace's keeper: %w", err)
	}
	names := make([]string, 0, len(staged.packs))
	for _, p := range staged.packs {
		names = append(names, p.Name)
	}
	plan := &keeperPlan{
		Build: keeperBuildStamp(), Workspace: o.Workspace, Cname: cname, Runtime: rt, Network: o.Network,
		Color: o.streamColor(), Config: raw, PackTree: staged.root, Packs: names, Services: services,
		Payload: payload, ApprovedScopes: o.approvedScopes, PerfRecording: o.timingRecording(),
		Settings: o.frozenLoopholeSettings(), SettingsPrepared: o.settingsPrepared,
		Sealed: o.Sealed, Notch: o.macosUserKeeperNotch(), Command: o.macosUserCommandName(),
		CallerTokens: o.callerTokens,
	}
	m := o.macosUserKey
	m.handed = nil
	hand := func(p *launchservice.Plan) {
		reserved := p.Reserved()
		addrs := make([]string, 0, len(reserved))
		for a := range reserved {
			addrs = append(addrs, a)
		}
		sort.Strings(addrs)
		for _, a := range addrs {
			if f := reserved[a].File(); f != nil {
				plan.ReservedAddrs = append(plan.ReservedAddrs, a)
				m.handed = append(m.handed, f)
			}
		}
	}
	listens := map[string]bool{}
	for _, d := range doorways {
		plan.Doorways = append(plan.Doorways, heldFrom(d))
		hand(d)
		for _, a := range d.Addresses() {
			listens[a] = true
		}
	}
	for _, st := range o.macosUserServiceStarts(channel) {
		h := heldFrom(st.plan)
		h.Input, h.PointedAt, h.Worker = st.input, st.pointedAt, st.worker
		plan.LaunchServices = append(plan.LaunchServices, h)
		hand(st.plan)
	}
	// The doorways' declared-to-served addresses, for a joiner to compose its clients at: a guest's
	// jail daemon is each session's own, so its port is not the keeper's to name.
	for from, to := range o.served.moved {
		if listens[to] {
			if plan.ServedAddresses == nil {
				plan.ServedAddresses = map[string]string{}
			}
			plan.ServedAddresses[from] = to
		}
	}
	return plan, nil
}

// releaseHandedReservations lets go of this launch's copies of the sockets it handed its keeper:
// every doorway's and launch-owned service's plan, and nothing a guest's jail daemon was reserved,
// which stays held until the sandbox starts (releaseReservedPorts).
func (o *Options) releaseHandedReservations() {
	for _, p := range o.launchDoorways {
		p.Release()
	}
	for _, p := range o.launchServices {
		p.Release()
	}
	if m := o.macosUserKey; m != nil {
		m.handed = nil
	}
}

// keeperHandOff is what a spawn hands plan's keeper beside its plan: the lock it holds until ready
// (the workspace launch lock at a container backend, the key's arrival lock at macos-user, nil for
// none), the reserved sockets in the order the plan names them, and the release of this launch's
// own copies once the keeper has its own.
func (o *Options) keeperHandOff(plan *keeperPlan) (*workspaceLock, []*os.File, func()) {
	if plan.Notch == "" {
		return o.launchLock, o.reservedPortFiles(), o.releaseReservedPorts
	}
	m := o.macosUserKey
	if m == nil {
		return nil, nil, func() {}
	}
	return m.arrival, m.handed, o.releaseHandedReservations
}

// macosUserKeyServices names what a keeper of this launch's key holds, naming the key's notch
// (JL-D41): the jail notch's are "this workspace's macos-user host services", and the guest notch's,
// a key of their own (JL-D37), "this workspace's guest-notch macos-user host services".
func (o *Options) macosUserKeyServices() string {
	if o.macosUserKeeperNotch() == keeperNotchGuest {
		return "this workspace's guest-notch macos-user host services"
	}
	return "this workspace's macos-user host services"
}

// macosUserKeySession names one session of this launch's key, its notch named as
// macosUserKeyServices names it.
func (o *Options) macosUserKeySession() string {
	if o.macosUserKeeperNotch() == keeperNotchGuest {
		return "guest-notch macos-user session"
	}
	return "macos-user session"
}

// macosUserKeeperLine is the keeper's disclosure at macos-user (JL-D21, JL-D41): what it will hold,
// at which notch, that it ends with the key's last session, and where it logs.
func (o *Options) macosUserKeeperLine(key string, plan *keeperPlan) string {
	var held []string
	for _, s := range plan.Services {
		held = append(held, "the "+s+" service")
	}
	for _, d := range plan.Doorways {
		held = append(held, "the "+d.Service+" doorway")
	}
	for _, s := range plan.LaunchServices {
		held = append(held, "the "+s.Service+" service's host half")
	}
	return fmt.Sprintf("keeper: yolo internal daemon %s will hold %s (%s) until its last %s leaves; log: %s",
		KeeperVerb, o.macosUserKeyServices(), strings.Join(held, ", "), o.macosUserKeySession(), keeperLogPath(key))
}

// startMacosUserKeeper is a fresh macos-user launch's spawn of its keeper (§9.9.5): the disclosure
// of what it will run, the count of this session, the spawn with the plan and the arrival lock, and
// the relay of the keeper's lines until it is ready, after which the roster it wrote names the
// endpoints this session's sandbox is told. It reports false when the launch ends here, with
// status the launch's.
func (o *Options) startMacosUserKeeper(arm *MacosUserArm, cfg *jsonx.OrderedMap, rt, cname string,
	staged stagedPacks, payload []loopholes.JailDaemonSpec, doorways []*launchservice.Plan,
	channel *packChannel) (rec keeperRecord, status int, ok bool) {
	m, out := o.macosUserKey, o.pr(o.Stderr)
	// THE HOST-EXECUTION DISCLOSURE, in this terminal and BEFORE THE SPAWN (JL-D6): the keeper runs
	// exactly the plan this was printed from, and refuses a daemon it does not name (checkPlan). A
	// local pack's doorway or service is named here too, argv and all, for the keeper names none.
	o.discloseLoopholes(rt, cfg, staged.packs, payload)
	for _, d := range doorways {
		o.noteMacosUserLocalHostCode(d, fmt.Sprintf("the %q doorway's host argv", d.Service))
	}
	for _, s := range o.launchServices {
		o.noteMacosUserLocalHostCode(s, fmt.Sprintf("the %q service's host half", s.Service))
	}
	plan, err := o.macosUserKeeperPlanFor(cfg, rt, cname, staged, o.plannedLoopholeNames(rt, cfg),
		payload, doorways, channel)
	if err != nil {
		out.printf("[bold red]Refusing to launch: %s[/bold red]", err.Error())
		return rec, 1, false
	}
	out.print("[dim]" + richtext.Escape(o.macosUserKeeperLine(m.key, plan)) + "[/dim]")
	// THIS LAUNCH IS A SESSION OF THE KEY IT STARTS, counted before the keeper exists and under the
	// arrival lock, so the keeper never sees zero sessions before the first has begun (JL-D16).
	o.holdSessionLock(m.key)
	plan.Uncounted = o.sessionLock == nil
	if plan.Uncounted {
		out.printf("[yellow]This workspace's macos-user keeper cannot count this session, so it will not end "+
			"the host services when its sessions leave; %s ends them.[/yellow]", stopRemedy(rt, cname))
	}
	o.recordKeyedSession(m.key, true)
	sp := o.Perf.Span("launch.start_keeper")
	kp, err := o.startKeeper(plan)
	if err != nil {
		sp.End()
		out.printf("[bold red]%s[/bold red]", err.Error())
		return rec, 1, false
	}
	m.kp = kp
	// THE PACK TREE CHANGES HANDS (JL-D35): the keeper's daemons run from it for every session of the
	// key, and it removes it at its end, so this launch's return leaves it.
	o.packTreeHeld = true
	// A SIGNAL BEFORE READY ends the keeper's start, through its lifeline (the arm's setup phase).
	arm.onEnding(kp.closeLifeline)
	started := false
	ready := kp.relay(o.Stdout, o.Stderr, os.Stdout, os.Stderr, keeperEvents{
		started: func(pid int) {
			started = true
			out.printf("[dim]keeper: started, pid %d[/dim]", pid)
		},
	})
	_ = kp.progress.Close()
	kp.closeLifeline()
	sp.End()
	if !ready {
		<-kp.exited
		if st, ending := arm.Ending(); ending {
			return rec, st, false
		}
		if !started && kp.exitCode != 0 {
			out.printf("[bold red]This workspace's macos-user keeper ended (status %d) before it started; "+
				"its log: %s[/bold red]", kp.exitCode, keeperLogPath(m.key))
			o.packTreeHeld = false
		}
		st := kp.exitCode
		if st == 0 {
			st = 1
		}
		return rec, st, false
	}
	m.logFrom = kp.sessionLogFrom(m.key)
	// A SIGNAL THAT LANDED AROUND READY, which the keeper may not have seen through its lifeline (it
	// reads it on a goroutine of its own, and stops at ready): this launch is a session that never
	// began, so its quit lets the count go, and the keeper ends as for a last session, which the quit
	// streams (JL-D74's rule at a key).
	if st, ending := arm.Ending(); ending {
		return rec, o.endMacosUserSession(rt, st), false
	}
	rec, have := readKeeperRecord(m.key)
	if !have || !rec.Ready {
		out.printf("[bold red]Refusing the macos-user launch: its keeper said ready, and its roster %s "+
			"cannot be read.[/bold red]", keeperRecordPath(m.key))
		out.printf("[dim]Launch again: this launch's quit ends the keeper it started when no other session "+
			"joined it; if one still holds this workspace's macos-user host services, %s ends it.[/dim]",
			stopRemedy(rt, cname))
		return rec, 1, false
	}
	return rec, 0, true
}

// joinMacosUserKeeper is a joining launch's host-services step (JL-D39, JL-D41): one line naming
// the keeper it joined, the disclosures of what that keeper runs for it, and a refusal when this
// launch needs a host service the keeper does not run, unless AllowAttachSkewEnv acknowledges it.
// It starts nothing. It reports false when the launch is refused.
func (o *Options) joinMacosUserKeeper(rt, cname string, cfg *jsonx.OrderedMap, packs []*packload.Pack,
	payload []loopholes.JailDaemonSpec, doorways []*launchservice.Plan) bool {
	m := o.macosUserKey
	rec := *m.joined
	out := o.pr(o.Stderr)
	out.print("[dim]" + richtext.Escape(o.joinedKeeperLine(m.key, rec)) + "[/dim]")
	o.discloseLoopholes(rt, cfg, packs, payload)
	o.noteHeldByKeeper(rec)
	// What the keeper recorded down while this launch was not in (JL-D19).
	o.noteServicesDown(m.key, rt)
	return o.judgeKeeperLacks(rt, cname, rec, o.macosUserKeeperLacks(rt, cfg, doorways, rec), "Joining")
}

// noteHeldByKeeper is one line per doorway and launch-owned service the joined keeper holds, marked
// as held by it (JL-D41), on the addresses its roster names.
func (o *Options) noteHeldByKeeper(rec keeperRecord) {
	out := o.pr(o.Stderr)
	for _, d := range rec.Doorways {
		out.printf("[dim]Held by the keeper (pid %d): the %q doorway (pack %q) on %s, outside the sandbox.[/dim]",
			rec.PID, d.Service, d.Pack, strings.Join(heldAddresses(d), ", "))
	}
	for _, s := range rec.LaunchServices {
		out.printf("[dim]Held by the keeper (pid %d): the %q service (pack %q) on %s, outside the sandbox.[/dim]",
			rec.PID, s.Service, s.Pack, strings.Join(heldAddresses(s), ", "))
	}
}

// macosUserKeeperLacks is what this joining launch needs outside the sandbox that the keeper whose
// roster is rec does not run (JL-D39): a front, a doorway, or a launch-owned service its own
// composition planned after those it adopted from the roster (macosUserKeying.seeded).
func (o *Options) macosUserKeeperLacks(rt string, cfg *jsonx.OrderedMap, doorways []*launchservice.Plan,
	rec keeperRecord) []string {
	var lacks []string
	for _, s := range o.plannedLoopholeNames(rt, cfg) {
		if !slices.Contains(rec.Services, s) {
			lacks = append(lacks, "the "+s+" service")
		}
	}
	for _, d := range doorways {
		if !slices.ContainsFunc(rec.Doorways, func(h keeperHeld) bool { return h.Service == d.Service }) {
			lacks = append(lacks, "the "+d.Service+" doorway")
		}
	}
	for i, s := range o.launchServices {
		if i >= o.macosUserKey.seeded {
			lacks = append(lacks, "the "+s.Service+" service's host half")
		}
	}
	return lacks
}

// judgeKeeperLacks decides a join that lacks what lacks names: the refusal, naming the sessions and
// `yolo stop`, or under AllowAttachSkewEnv a join without them, which lead ("Joining", or a dry run's
// "Would join") says. Nothing lacking joins. It reports false when the launch is refused.
func (o *Options) judgeKeeperLacks(rt, cname string, rec keeperRecord, lacks []string, lead string) bool {
	if len(lacks) == 0 {
		return true
	}
	out := o.pr(o.Stderr)
	if o.Getenv(AllowAttachSkewEnv) != "" {
		out.printf("[bold yellow]%s this workspace's macos-user keeper (pid %d) without %s, which it "+
			"does not run (%s=1): what in this sandbox uses it fails.[/bold yellow]", lead, rec.PID,
			strings.Join(lacks, ", "), AllowAttachSkewEnv)
		o.releaseHandedReservations()
		return true
	}
	out.printf("[bold red]Refusing to launch: this launch needs %s, which this workspace's macos-user "+
		"keeper (pid %d) does not run, and %s.[/bold red]", strings.Join(lacks, ", "), rec.PID,
		sessionsThat(o.otherKeptSessions(), "uses it", "use it"))
	out.printf("[dim]%s ends them and their keeper, and the next launch starts fresh with what this one "+
		"needs; or %s=1 joins without it.[/dim]", capitalize(stopRemedy(rt, cname)), AllowAttachSkewEnv)
	return false
}

// heldAddresses is the served addresses a held doorway or service answers on, sorted.
func heldAddresses(h keeperHeld) []string {
	out := make([]string, 0, len(h.Moved))
	for _, to := range h.Moved {
		out = append(out, to)
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// joinedKeeperLine is a joining launch's keeper line (JL-D41): which keeper it joined, at which
// notch, for how many sessions, another build's when it is, and where it logs.
func (o *Options) joinedKeeperLine(key string, rec keeperRecord) string {
	n := len(keptSessions(liveKeyedSessions(key)))
	build := ""
	if rec.Build != "" && rec.Build != keeperBuildStamp() {
		build = fmt.Sprintf(", yolo %s, another build than this one", rec.Build)
	}
	return fmt.Sprintf("keeper: joined yolo internal daemon %s (pid %d%s), which holds %s for %d %s; log: %s",
		KeeperVerb, rec.PID, build, o.macosUserKeyServices(), n, plural(n, "session", "sessions"),
		keeperLogPath(key))
}

// setRosterEndpoints tells this session's sandbox each host service's endpoint file, as the roster
// names it: the same files for every session of the key.
func setRosterEndpoints(launchEnv *jsonx.OrderedMap, rec keeperRecord) {
	vars := make([]string, 0, len(rec.Endpoints))
	for v := range rec.Endpoints {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	for _, v := range vars {
		launchEnv.Set(v, rec.Endpoints[v])
	}
}

// endMacosUserSession is a macos-user session's quit, once its command returned (JL-D40, §9.9.6):
// its record and its count go, and then what it left behind: other sessions of the key, which one
// line names; the keeper's teardown, which the last session streams; or an UNKEPT key, whose last
// session removes what its dead keeper left. A session with no keeper only removes its record, and
// a launch that recorded and counted nothing returns as before.
func (o *Options) endMacosUserSession(rt string, rc int) int {
	m := o.macosUserKey
	if m == nil {
		return rc
	}
	if m.keeperless {
		closeKeyedSessionRecord(m.record)
		m.record = nil
		return rc
	}
	if m.record == nil && o.sessionLock == nil {
		return rc
	}
	closeKeyedSessionRecord(m.record)
	m.record = nil
	o.releaseSessionLock()
	cname := yoloruntime.FromWorkspace(o.Workspace)
	sp := o.Perf.Span("session.after_quit")
	state, locks := o.probeAfterQuit(m.key)
	sp.End()
	out := o.pr(terminalOnly(o.Stderr))
	switch state {
	case quitOthers:
		o.printKeeperRecords(m.key, m.logFrom)
		pid := "unknown"
		if rec, ok := readKeeperRecord(m.key); ok && rec.PID > 0 {
			pid = fmt.Sprint(rec.PID)
		}
		others := othersUncounted
		if n := len(keptSessions(liveKeyedSessions(m.key))); n > 0 {
			others = fmt.Sprintf("%d other %s", n, plural(n, "session", "sessions"))
		}
		out.printf("[dim]This workspace's macos-user host services stay up (keeper pid %s) for %s; %s ends "+
			"them all.[/dim]", pid, others, stopRemedy(rt, cname))
	case quitLast:
		o.streamKeeperTeardown(m.key, m.logFrom)
	case quitUnkeptLast:
		if rec, ok := readKeeperRecord(m.key); ok {
			o.pr(o.Stderr).printf("[yellow]This workspace's macos-user keeper (pid %d) is gone, and this was its "+
				"last session: removing the host-side state it left.[/yellow]", rec.PID)
			o.reapKeyRecords(m.key, cname, rec)
		}
		locks.release()
	case quitUnkeptOthers:
		out.printf("[yellow]This workspace's macos-user keeper is gone, so its host services have been down "+
			"since it died; the last of its other sessions to quit removes what it left, or %s does now.[/yellow]",
			stopRemedy(rt, cname))
	case quitUnknown:
		out.print("[dim]Could not tell whether other macos-user sessions of this workspace remain; its host " +
			"services may be ending.[/dim]")
	}
	return rc
}

// endMacosUserKeying is Run's deferred end of a macos-user launch's place at its key, for every
// return before its quit: the arrival lock it still holds, and, for a launch refused once it counted
// itself, its quit, so a keeper this launch spawned and was the only session of ends with its
// teardown streamed here rather than behind the prompt. After the quit it does nothing more.
func (o *Options) endMacosUserKeying() {
	m := o.macosUserKey
	if m == nil {
		return
	}
	m.arrival.Close()
	if m.record != nil || o.sessionLock != nil {
		o.endMacosUserSession(o.runtime, 0)
	}
	closeKeyedSessionRecord(m.record)
	m.record = nil
}

// noteMacosUserKeeperDryRun is what a dry run says of what it would run outside the sandbox
// (§9.9.7), as the keeper's, since a keeper would hold all of it: the keeper it would join
// (peekMacosUserKey) and each doorway and service that keeper holds, the refusal a join would meet
// when this launch needs one the keeper does not run, or, for a fresh launch, each service and
// doorway it would start and the keeper that would hold them. A launch that plans nothing a keeper
// holds says nothing here (JL-D42). It reports false when the launch would be refused.
func (o *Options) noteMacosUserKeeperDryRun(rt, cname string, cfg *jsonx.OrderedMap,
	doorways []*launchservice.Plan, channel *packChannel) bool {
	m := o.macosUserKey
	if m == nil {
		return true
	}
	out := o.pr(o.Stderr)
	if m.joined != nil {
		rec := *m.joined
		out.print("[dim]Would join: " + richtext.Escape(o.joinedKeeperLine(m.key, rec)) + "[/dim]")
		o.noteHeldByKeeper(rec)
		return o.judgeKeeperLacks(rt, cname, rec, o.macosUserKeeperLacks(rt, cfg, doorways, rec), "Would join")
	}
	if w := m.waits; w != nil {
		who := "its previous keeper"
		if w.PID > 0 {
			who = fmt.Sprintf("its previous keeper (pid %d)", w.PID)
		}
		out.printf("Would wait for %s to finish ending %s first.", who, o.macosUserKeyServices())
	}
	if !o.macosUserKeeps(rt, cfg, doorways) {
		return true
	}
	held := fmt.Sprintf("for this launch's keeper to hold, outside the sandbox, until this workspace's last %s leaves",
		o.macosUserKeySession())
	for _, plan := range o.launchServices {
		out.print(fmt.Sprintf("Would start the %q service (pack %q) on %v %s.", plan.Service, plan.Pack,
			o.servicePointedAt(plan, channel), held))
	}
	for _, plan := range doorways {
		out.print(fmt.Sprintf("Would open the %q doorway (pack %q) on %v %s: %s", plan.Service, plan.Pack,
			plan.Addresses(), held, strings.Join(plan.Cmd, " ")))
	}
	plan := &keeperPlan{Services: o.plannedLoopholeNames(rt, cfg)}
	for _, d := range doorways {
		plan.Doorways = append(plan.Doorways, heldFrom(d))
	}
	for _, s := range o.launchServices {
		plan.LaunchServices = append(plan.LaunchServices, heldFrom(s))
	}
	out.print("[dim]Would start: " + richtext.Escape(o.macosUserKeeperLine(m.key, plan)) + "[/dim]")
	return true
}

// macosUserGrants is what this launch tells its backend about the endpoint files of its key's
// keeper (§9.9.5): which ones a session of the key has granted the sandbox account already, so a
// joiner's stage does not grant them again, and the record of what its own stage granted. Only
// paths in the keeper's host-services dir, the one every session of the key is told, are ever
// skipped or recorded. Nil for a launch with no keeper.
func (o *Options) macosUserGrants(rec keeperRecord) (skip func(string) bool, staged func([]string)) {
	m := o.macosUserKey
	if m == nil || rec.SocketsDir == "" {
		return nil, nil
	}
	dir := filepath.Clean(rec.SocketsDir)
	inDir := func(p string) bool {
		p = filepath.Clean(p)
		return p == dir || filepath.Dir(p) == dir
	}
	granted := readKeeperGranted(m.key)
	skip = func(p string) bool { return inDir(p) && granted[filepath.Clean(p)] }
	staged = func(paths []string) {
		var mine []string
		for _, p := range paths {
			if inDir(p) {
				mine = append(mine, filepath.Clean(p))
			}
		}
		if len(mine) > 0 {
			recordKeeperGranted(m.key, mine)
		}
	}
	return skip, staged
}

// signalKeyProcess is how `yolo stop` signals a macos-user keeper or session; a var so a test can
// stand in for the kernel.
var signalKeyProcess = func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }

// StopMacosUser is `yolo stop` at macos-user (docs/design/jail-lifetime-last-session-wins.md JL-D44,
// §9.9.6): for each key of the workspace (its jail notch's and its guest notch's), it sends SIGTERM
// to the keeper, which ends its host services in order, and to each live session's launcher, which
// forwards it to its sudo and so to the sandboxed command (macosuserarm.go's session phase); then it
// streams the keeper's teardown, within keeperTeardownWait, or, for an UNKEPT key, removes what its
// dead keeper left once the sessions it signalled let the count go. The status is `yolo stop`'s.
func StopMacosUser(stdout, stderr io.Writer, workspace string) int {
	o := NewDefaultOptions()
	o.Workspace = workspace
	o.Stdout, o.Stderr = stdout, stderr
	o.PerfLoggingConfig = func() bool { return false }
	fillDefaults(&o)
	o.runtime = "macos-user"
	cname := yoloruntime.FromWorkspace(workspace)
	rc, stopped := 0, false
	for _, notch := range macosUserKeeperNotches {
		krc, did := o.stopMacosUserKey(cname, keeperKey(cname, notch))
		if krc != 0 {
			rc = krc
		}
		stopped = stopped || did
	}
	if !stopped {
		fmt.Fprintf(stdout, "Nothing to stop: no macos-user session of this workspace (%s) is running, and the "+
			"macos-user backend has no persistent jail — every invocation is a fresh sandbox.\n", cname)
	}
	return rc
}

// stopMacosUserKey is StopMacosUser for one key. did reports whether there was anything to end.
func (o *Options) stopMacosUserKey(cname, key string) (rc int, did bool) {
	out := o.pr(o.Stdout)
	alive := probeKeeper(key) != keeperGone
	rec, recorded := readKeeperRecord(key)
	sessions := liveKeyedSessions(key)
	if !alive && !recorded && len(sessions) == 0 {
		return 0, false
	}
	logFrom := keeperLogSize(key)
	var said []string
	signalled := map[string]bool{}
	signal := func(pid int, who string) {
		if pid <= 1 || pid == os.Getpid() {
			return
		}
		if err := signalKeyProcess(pid, syscall.SIGTERM); err == nil {
			said = append(said, fmt.Sprintf("%s (pid %d)", who, pid))
		}
	}
	if alive && recorded {
		signal(rec.PID, "the keeper")
	}
	for _, s := range sessions {
		signalled[s.path] = true
		signal(s.PID, "a session")
	}
	what := "this workspace's macos-user sessions"
	if len(said) > 0 {
		out.printf("Ending %s: sent SIGTERM to %s.", what, strings.Join(said, ", "))
	}
	stream := func(l string) { out.print("[dim]  " + richtext.Escape(l) + "[/dim]") }
	if alive {
		out.printf("Waiting for the keeper to finish its teardown...")
		if !waitForKeeper(key, logFrom, keeperTeardownWait, stream, nil) {
			o.noteKeeperStillRunning(key, keeperTeardownWait)
			return 1, true
		}
		if !o.awaitSignalledSessions(key, signalled) {
			return 1, true
		}
		out.printf("Stopped %s and their keeper. The next yolo launch starts fresh.", what)
		return 0, true
	}
	if recorded {
		if rc, done := o.reapStoppedKey(cname, key, rec, signalled); done {
			return rc, true
		}
	}
	if !o.awaitSignalledSessions(key, signalled) {
		return 1, true
	}
	out.printf("Stopped %s. The next yolo launch starts fresh.", what)
	return 0, true
}

// reapStoppedKey is `yolo stop`'s reap of an UNKEPT key, or of the records of a keeper that died with
// no session left (JL-D44): holding the key's liveness lock, it marks the dead keeper's roster ending
// (markKeyEnding), so an arrival meanwhile waits for the reap instead of joining services that are
// gone; once the sessions it signalled let the count go it removes what the keeper left. A session
// that counted itself meanwhile is signalled too, since the reap waits for it. done reports that the
// stop ends here, with rc: a refusal, or another reaper having taken the lock.
func (o *Options) reapStoppedKey(cname, key string, rec keeperRecord, signalled map[string]bool) (rc int, done bool) {
	out := o.pr(o.Stdout)
	live, err := holdLivenessLock(key)
	if err != nil {
		return 0, true // a keeper, or another reaper, took it in between: theirs to finish
	}
	defer releaseLock(live)
	markKeyEnding(key, rec.PID)
	deadline := time.Now().Add(keeperSessionsWait)
	var held *sessionLock
	for {
		if s, ok := tryExclusiveSessionLock(key); ok {
			held = s
			break
		}
		for _, s := range keptSessions(liveKeyedSessions(key)) {
			if !signalled[s.path] {
				signalled[s.path] = true
				if s.PID > 1 && s.PID != os.Getpid() && signalKeyProcess(s.PID, syscall.SIGTERM) == nil {
					out.printf("Sent SIGTERM to a session that arrived meanwhile (pid %d).", s.PID)
				}
			}
		}
		if !time.Now().Before(deadline) {
			o.pr(o.Stderr).printf("[yellow]A macos-user session of this workspace still holds its count after %s, "+
				"so what its dead keeper left stays for the last of them to remove; `yolo stop` again retries.[/yellow]",
				keeperSessionsWait)
			return 1, true
		}
		time.Sleep(keeperPoll)
	}
	defer held.release()
	out.printf("[dim]This workspace's macos-user keeper (pid %d) was gone; removing what it left.[/dim]", rec.PID)
	o.reapKeyRecords(key, cname, rec)
	return 0, false
}

// awaitSignalledSessions waits, within keeperSessionsWait, for every session `yolo stop` signalled
// (signalled, by record path) to end and remove its record, so the stop says stopped only once they
// have: a session with no keeper is nobody's teardown but its own. It reports false, naming what
// still runs, when the bound ran out.
func (o *Options) awaitSignalledSessions(key string, signalled map[string]bool) bool {
	deadline := time.Now().Add(keeperSessionsWait)
	for {
		var left []keyedSession
		for _, s := range liveKeyedSessions(key) {
			if signalled[s.path] {
				left = append(left, s)
			}
		}
		if len(left) == 0 {
			return true
		}
		if !time.Now().Before(deadline) {
			pids := make([]string, 0, len(left))
			for _, s := range left {
				pids = append(pids, strconv.Itoa(s.PID))
			}
			o.pr(o.Stderr).printf("[yellow]%s after %s; `kill %s` ends %s, or `yolo stop` again signals %s.[/yellow]",
				capitalize(sessionsThat(left, "is still running", "are still running")), keeperSessionsWait,
				strings.Join(pids, " "), plural(len(left), "it", "them"), plural(len(left), "it", "them"))
			return false
		}
		time.Sleep(keeperPoll)
	}
}
