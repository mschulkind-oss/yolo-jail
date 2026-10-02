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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
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
	// uncounted is the plan's Uncounted: a keeper that never ends the jail on the count (JL-P3).
	uncounted bool
}

// closeLifeline closes the lifeline once.
func (k *keeperProcess) closeLifeline() { k.closeMu.Do(func() { _ = k.lifeline.Close() }) }

// relay is relayKeeper over this keeper's progress pipe, which also notes the ready frame on k, for
// the teardown before ready that a signal can still run once the keeper is ready
// (keeperPreReadyTeardown).
func (k *keeperProcess) relay(out, errOut, jailOut, jailErr io.Writer, ev keeperEvents) bool {
	then := ev.ready
	ev.ready = func() {
		k.readyOnce.Do(func() { close(k.ready) })
		if then != nil {
			then()
		}
	}
	return relayKeeper(k.progress, out, errOut, jailOut, jailErr, ev)
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
	var lockFile *os.File
	if o.launchLock != nil && !o.launchLock.isClosed() {
		lockFile = o.launchLock.f
	}
	// THE JAIL'S RESERVED PORTS GO TO THE KEEPER (NC-D70; servedaddresses.go): it fronts the jail's
	// host services, each on a port-0 listener, before it starts the container whose daemons bind
	// them, so it holds them until then. This launch's own copies close once the keeper has its
	// own, or once it could not be spawned.
	reserved := o.reservedPortFiles()
	wait, err := defaultKeeperSpawner(o, planPath, progW, lifeR, lockFile, reserved)
	o.releaseReservedPorts()
	_ = progW.Close()
	_ = lifeR.Close()
	if err != nil {
		removeKeeperPlan(planPath)
		_ = progR.Close()
		_ = lifeW.Close()
		return nil, fmt.Errorf("could not start this jail's keeper: %w", err)
	}
	if lockFile != nil {
		o.launchLock.handOff()
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
		PackTree: staged.root, Packs: names, Services: services, Payload: payload,
		ApprovedScopes: o.approvedScopes, Forwards: forwards, ForwardDir: forwardDir,
		SocketsDir: socketsDir, RunCmd: runCmd, ImageRef: in.imageRef, Skeleton: in.homeSkeleton,
		ScratchVolumes: o.scratchVolumes, PerfRecording: o.timingRecording(), Sealed: o.Sealed,
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
