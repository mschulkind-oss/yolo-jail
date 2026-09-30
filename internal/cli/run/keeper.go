package run

// keeper.go is the KEEPER: one small background process per running container jail, spawned by the
// fresh launch before any host service or the container exists, that owns the jail's host services
// and the jail's life, and ends itself once the last session or the container is gone
// (docs/design/jail-lifetime-last-session-wins.md §9, step 3 of its §7). The term is the design's
// (§1.1). Its verb is `yolo internal daemon jail-keeper`, a hidden self-exec subcommand of yolo as
// every host daemon is; the CLI wires it (internal/cli/internal.go).
//
// # Its life
//
//  1. Its first acts: the descriptors it inherited are marked close-on-exec, it moves into a systemd
//     scope of its own where there is one, it takes the jail's LIVENESS LOCK, and it writes the
//     owner-PID file naming itself and its START RECORD (keeperstate.go).
//  2. It refuses a plan it cannot run as disclosed: another build's, a pack set it resolves
//     differently, or one that would start a daemon the launch did not name (checkPlan).
//  3. It starts what the launch disclosed, today's code in today's order: the port forwards, the
//     loophole services and the launch check, the credential view, then the container's main
//     process, whose output it relays to the launch until pid 1's boot is done (keeperframe.go).
//     It releases the launch lock the launch handed it once the container is seen running.
//  4. It ends on one of three observations, never on a timer (JL-D17): before ready, the launch's
//     lifeline closing or a start that failed; its exclusive take of the SESSION LOCK, which is zero
//     sessions; or the container ending some other way. A SIGTERM or SIGINT ends the jail in order,
//     as the third does (JL-D24). Each runs today's teardown chain (teardownAfterExit) after the stop.
//
// # What it is not
//
// Not a supervisor: it restarts nothing, and nothing restarts it (JL-D18). Not a housekeeper: it
// cleans up only what its own jail made (JL-D23); the housekeeping slot stays in the launch
// (JL-D10). And no terminal: its stdio is /dev/null, its output crosses the progress pipe until
// ready and is its log's after, and a SIGHUP, which only a `kill` can send a process with no
// controlling terminal, is ignored.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// KeeperVerb is the keeper's member name in the `yolo internal daemon` group.
const KeeperVerb = "jail-keeper"

// KeeperSeams are what the CLI wires into a keeper, as it wires them into a launch: the E3 config
// capture lives in internal/cli, which imports this package. Its warnings go to warn, which is the
// keeper's log: a keeper's stderr is /dev/null.
type KeeperSeams struct {
	CaptureOnTerminate func(workspace, runtime string, warn func(string))
}

// The keeper's inherited descriptors: exec.Cmd.ExtraFiles puts the first at 3.
const (
	keeperProgressFD = 3
	keeperLifelineFD = 4
	keeperLockFD     = 5
)

// keeperArgv is the keeper's command line; "yolo" is replaced by the running binary at the spawn.
// withLock says the spawn hands it the launch lock as fd 5.
func keeperArgv(planPath string, withLock bool) []string {
	argv := []string{"yolo", "internal", "daemon", KeeperVerb, "--plan", planPath,
		"--progress-fd", strconv.Itoa(keeperProgressFD), "--lifeline-fd", strconv.Itoa(keeperLifelineFD)}
	if withLock {
		argv = append(argv, "--lock-fd", strconv.Itoa(keeperLockFD))
	}
	return argv
}

// KeeperMain is `yolo internal daemon jail-keeper --plan <file> --progress-fd 3 --lifeline-fd 4
// [--lock-fd 5]`. Its caller is a fresh container launch (keeperspawn.go), never a person.
func KeeperMain(args []string, seams KeeperSeams) int {
	planPath := ""
	fds := map[string]int{}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--plan" && i+1 < len(args):
			planPath = args[i+1]
			i++
		case (a == "--progress-fd" || a == "--lifeline-fd" || a == "--lock-fd") && i+1 < len(args):
			fd, err := strconv.Atoi(args[i+1])
			if err != nil || fd < 3 {
				fmt.Fprintf(os.Stderr, "yolo internal daemon %s: bad %s %q\n", KeeperVerb, a, args[i+1])
				return 2
			}
			fds[a] = fd
			i++
		default:
			fmt.Fprintf(os.Stderr, "yolo internal daemon %s: unexpected argument %q\n", KeeperVerb, a)
			return 2
		}
	}
	// FIRST: no child the keeper starts may hold what it inherited (JL-D29). A Go child receives
	// ExtraFiles without close-on-exec, by convention, so socat, the fronted daemons, the runtime
	// client and the scratch remover would otherwise each keep the pipe and the lock alive.
	for _, fd := range fds {
		syscall.CloseOnExec(fd)
	}
	file := func(flag, name string) *os.File {
		if fd, ok := fds[flag]; ok {
			return os.NewFile(uintptr(fd), name)
		}
		return nil
	}
	progress := file("--progress-fd", "keeper-progress")
	lifeline := file("--lifeline-fd", "keeper-lifeline")
	lock := file("--lock-fd", "keeper-launch-lock")
	if planPath == "" {
		fmt.Fprintf(os.Stderr, "usage: yolo internal daemon %s --plan <file> --progress-fd <n> --lifeline-fd <n> [--lock-fd <n>]\n", KeeperVerb)
		return 2
	}
	// SIGHUP and SIGPIPE are received and dropped, SIGINT and SIGTERM end the jail in order: through
	// signal.Notify and never signal.Ignore, whose SIG_IGN every child would inherit (JL-D24, JL-D29).
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGPIPE, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	plan, err := readKeeperPlan(planPath)
	if err != nil {
		if progress != nil {
			_ = writeFrame(progress, frameStderr, []byte("yolo: this jail's keeper could not start: "+err.Error()+"\n"))
		}
		return 2
	}
	rc := runKeeper(plan, seams, progress, lifeline, lock, signals, nil)
	packload.ReleaseEmbedded()
	return rc
}

// runKeeper runs one keeper to its end. tune, when non-nil, adjusts the keeper's Options once they
// are built: a unit test's fakes, never a production caller.
func runKeeper(plan *keeperPlan, seams KeeperSeams, progress, lifeline, lock *os.File,
	signals <-chan os.Signal, tune func(*Options)) int {
	k := newKeeper(plan, seams, progress, lifeline, lock, signals)
	if tune != nil {
		tune(k.o)
	}
	return k.run()
}

// keeper is one keeper's state.
type keeper struct {
	o    *Options
	plan *keeperPlan
	sink *keeperSink

	progress   *os.File
	launchLock *os.File
	lockOnce   sync.Once
	signals    <-chan os.Signal
	// lifelineGone closes when the launch's lifeline reads EOF: the launch is gone.
	lifelineGone chan struct{}

	pid      int
	scope    string
	liveness *os.File
	mu       sync.Mutex
	sessions *os.File // the session lock, held exclusively once the keeper drained

	socat   []*exec.Cmd
	handles []loopholeDaemon
	jm      *jailMain
	running chan struct{} // closed once the container was seen running, or the wait gave up
	// ending closes at the keeper's last act, so a watch still looking at the runtime stops.
	ending     chan struct{}
	endingOnce sync.Once
	// jailLeft is a container the keeper's stop did not end: the keeper leaves it UNKEPT, its
	// owner-PID file and start record in place, rather than a container nothing owns (endJail).
	jailLeft bool
}

// newKeeper builds a keeper and its Options from a plan.
func newKeeper(plan *keeperPlan, seams KeeperSeams, progress, lifeline, lock *os.File,
	signals <-chan os.Signal) *keeper {
	k := &keeper{plan: plan, progress: progress, launchLock: lock, signals: signals,
		lifelineGone: make(chan struct{}), running: make(chan struct{}), ending: make(chan struct{})}
	k.sink = &keeperSink{}
	if progress != nil {
		k.sink.pipe = progress
	}
	if lifeline != nil {
		go func() {
			_, _ = io.Copy(io.Discard, lifeline)
			_ = lifeline.Close()
			close(k.lifelineGone)
		}()
	}
	o := NewDefaultOptions()
	o.Workspace = plan.Workspace
	o.Network = plan.Network
	o.Color = plan.Color
	color := plan.Color
	o.IsTTYStdout = func() bool { return color }
	o.IsTTYStderr = func() bool { return false }
	recording := plan.PerfRecording
	o.PerfLoggingConfig = func() bool { return recording }
	if capture := seams.CaptureOnTerminate; capture != nil {
		o.CaptureOnTerminate = func(workspace, rt string) {
			capture(workspace, rt, func(msg string) { k.sink.logf("Warning: %s", msg) })
		}
	}
	o.Stdout = keeperStream{k.sink, frameStdout}
	o.Stderr = keeperStream{k.sink, frameStderr}
	fillDefaults(&o)
	o.runtime = plan.Runtime
	o.approvedScopes = plan.ApprovedScopes
	o.packTree, o.packTreeHeld = plan.PackTree, plan.PackTree != ""
	if len(plan.ScratchVolumes) > 0 {
		o.scratchVolumes = plan.ScratchVolumes
		o.scratchRemovalOnce = &sync.Once{}
	}
	o.keeperMode = true
	k.o = &o
	return k
}

// releaseLaunchLock lets the launch lock the launch handed over go: once the container is seen
// running, as the launch released it before there was a keeper, or before any unwind, whose
// guarded cleanups take it non-blocking (JL-D31).
func (k *keeper) releaseLaunchLock() {
	k.lockOnce.Do(func() { releaseLock(k.launchLock) })
}

// run is the keeper's life. Its status matters only before ready, when the launch reads it: after
// ready nothing waits on the keeper's exit but the lock its death frees.
func (k *keeper) run() int {
	o, p := k.o, k.plan
	k.pid = o.Getpid()
	o.initPerf(p.Cname)
	scope, line := o.moveKeeperIntoScope(p.Cname)
	k.scope = scope

	live, err := awaitLivenessLock(p.Cname, keeperLivenessWait)
	if err != nil {
		o.pr(o.Stderr).printf("[bold red]Refusing to start this jail's keeper: %v. The keeper of the "+
			"previous jail named %s is still running; %s, then a launch, ends it.[/bold red]",
			err, p.Cname, stopRemedy(p.Runtime, p.Cname))
		return k.unwindUnstarted(1)
	}
	k.liveness = live
	defer releaseLock(live)
	// THE LOG, only now that this keeper holds the name: opening truncates it, and a keeper that
	// refused above could have met a live one, whose log its last session may be streaming.
	if f, err := openKeeperLog(p.Cname); err == nil {
		k.sink.setLog(f)
	}
	k.sink.logOnlyf("%s", line)
	o.writeOwnerPID(p.Cname)
	if err := writeKeeperRecord(p.Cname, keeperRecord{PID: k.pid, Started: time.Now(),
		Workspace: p.Workspace, Runtime: p.Runtime, Skeleton: p.Skeleton, PackTree: p.PackTree,
		ScratchVolumes: p.ScratchVolumes, ForwardDir: p.ForwardDir, SocketsDir: p.SocketsDir,
		Scope: k.scope, Log: keeperLogPath(p.Cname)}); err != nil {
		k.sink.logf("keeper: could not write its start record (%v); if it dies, its jail's last session cannot reap what only it knew the names of", err)
	}
	k.sink.event(frameStarted, strconv.Itoa(k.pid))

	cfg, err := k.checkPlan()
	if err != nil {
		o.pr(o.Stderr).printf("[bold red]Refusing this launch's plan: %v.[/bold red]", err)
		return k.unwindUnstarted(1)
	}

	// THE HOST SERVICES, before the container, in today's order (run.go's fresh path until now).
	if len(p.Forwards) > 0 && p.ForwardDir != "" {
		sp := o.Perf.Span("launch.start_port_forwarding")
		k.socat = o.startPortForwards(p.Forwards, p.Cname, p.ForwardDir)
		sp.End()
	}
	sp := o.Perf.Span("launch.start_loopholes")
	k.handles = o.startPlannedLoopholes(p.Cname, p.Runtime, cfg, p.Payload)
	sp.End()
	o.registerClaudeCredentialView(p.Runtime, p.Cname, cfg)

	// THE CONTAINER. The launch's argv, with the services' endpoint pairs inserted before the image,
	// exactly where the fresh path used to insert them.
	runCmd := insertHostServiceEnv(append([]string{}, p.RunCmd...), p.ImageRef, k.handles)
	jm, err := startJailMain(runCmd, keeperStream{k.sink, frameJailStdout},
		keeperStream{k.sink, frameJailStderr}, func() { o.Perf.Mark("jail_main.exited") })
	if err != nil {
		o.pr(o.Stdout).printf("[bold red]Configured runtime '%s' not found on PATH.[/bold red]", p.Runtime)
		o.pr(o.Stdout).print("[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]")
		return k.unwindUnstarted(1)
	}
	o.Perf.Mark("jail_main.spawned")
	k.jm = jm
	k.sink.event(frameSpawned, "")
	go k.awaitRunning()

	// BEFORE READY: the launch's death, a refused boot, or a signal.
	for {
		ready, rc, ended := k.beforeReady()
		if ended {
			return rc
		}
		if ready {
			break
		}
	}
	select {
	case <-k.running:
	case <-time.After(keeperRunningWait):
	}
	k.sink.logOnlyf("keeper: pid %d holds %s until its last session leaves", k.pid, p.Cname)
	k.sink.event(frameReady, "")
	mirror, _ := paths.OpenExistingWorkspaceStateFile(p.Workspace, LaunchLogName, os.O_WRONLY|os.O_APPEND, 0)
	var mirrorW io.Writer
	if mirror != nil {
		defer mirror.Close()
		mirrorW = mirror
	}
	k.sink.endRelay(mirrorW)

	// AFTER READY: the three ways in (§9.5).
	drained := k.watchSessions()
	ended := k.watchContainer()
	for {
		select {
		case <-drained:
			k.sink.logf("keeper: the last session of %s left; ending the jail", p.Cname)
			k.endJail(lastSessionLeftReason, true)
			return k.finish(0, nil)
		case <-ended:
			k.sink.logf("keeper: %s ended; tearing down its host services", p.Cname)
			k.endJail("", false)
			return k.finish(0, drained)
		case s := <-k.signals:
			if s == syscall.SIGHUP || s == syscall.SIGPIPE {
				continue
			}
			k.sink.logf("keeper: sent %v; ending %s in order", s, p.Cname)
			// The Window A probe's last word before anything here can be what hangs, as the fresh
			// launch's terminate arm took it while it held the main process's client.
			o.lingerFinalSample("final (keeper signalled)")
			k.endJail(keeperSignalledReason(k.pid), true)
			return k.finish(128+int(s.(syscall.Signal)), drained)
		}
	}
}

// keeperLivenessWait bounds a new keeper's wait for its liveness lock (awaitLivenessLock).
var keeperLivenessWait = 2 * time.Second

// awaitLivenessLock is a new keeper's take of its liveness lock, which waits out a hold of an
// instant, up to bound. Every probe of a keeper takes the lock exclusively for an instant
// (probeKeeper, the reaper's take), and several poll it: a last session streaming its teardown,
// `yolo stop`, an arrival waiting for a drain. The fresh launch that spawned this keeper found no
// keeper under the launch lock and handed that lock over, so no keeper can have started for the name
// since, and a hold met here is one of those instants. A take that failed at once refused the launch
// as though the previous jail's keeper were still running. A hold that outlasts the bound is still
// refused (errKeeperAlive): two launches with no launch lock between them (holdLaunchLock's
// warning) can each spawn one.
func awaitLivenessLock(cname string, bound time.Duration) (*os.File, error) {
	deadline := time.Now().Add(bound)
	for {
		f, err := holdLivenessLock(cname)
		if !errors.Is(err, errKeeperAlive) || !time.Now().Before(deadline) {
			return f, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// keeperRunningWait bounds the ready path's wait for awaitRunning, which is itself bounded.
var keeperRunningWait = 30 * time.Second

// beforeReady is one turn of the wait before ready: ready once pid 1's boot is done, or ended, with
// the status the launch reads, once the keeper has ended the jail. Neither is a turn that dropped a
// SIGHUP or a SIGPIPE.
func (k *keeper) beforeReady() (ready bool, rc int, ended bool) {
	o, p, jm := k.o, k.plan, k.jm
	select {
	case <-jm.ready:
		return true, 0, false
	case <-jm.exited:
		if jm.awaitReady() {
			return true, 0, false
		}
		// A main process whose client exits first never booted: a refusal, or a runtime that would
		// not start it. Its status is the launch's, as the container's always was.
		select {
		case <-k.running:
		case <-time.After(keeperRunningWait):
		}
		k.releaseLaunchLock()
		// And a container still running under a client that died (killed, or a Mac's remote client
		// that lost its machine) is the keeper's to stop, as it stops whatever else it started
		// (§9.5 item 1): its hold never ends by itself. A refused boot's container went with it, so
		// that ordinary case stops nothing; one the runtime cannot answer for is stopped anyway.
		id, known := o.probeRunningContainer(p.Cname, p.Runtime, trackingProbeTimeout)
		k.endJail(bootClientGoneReason(k.pid), !known || id != "")
		return false, k.finish(jm.exitCode, nil), true
	case <-k.lifelineGone:
		o.pr(o.Stderr).printf("keeper: the launch that started %s is gone before its jail was ready; ending the jail", p.Cname)
		k.releaseLaunchLock()
		k.endJail(launchGoneReason(k.pid), true)
		return false, k.finish(1, nil), true
	case s := <-k.signals:
		if s == syscall.SIGHUP || s == syscall.SIGPIPE {
			return false, 0, false
		}
		k.releaseLaunchLock()
		k.endJail(keeperSignalledReason(k.pid), true)
		return false, k.finish(128+int(s.(syscall.Signal)), nil), true
	}
}

// checkPlan refuses a plan this keeper cannot run as the launch disclosed it (JL-D20, JL-D35): one of
// another build, whose pack tree resolves to another pack set here, that would start a daemon the
// launch did not name, or that names one this keeper would not start. It resolves the packs itself, from the launch's staged tree, and points the
// process-wide pack records at them, which is where the loophole set the spawn walks reads.
func (k *keeper) checkPlan() (*jsonx.OrderedMap, error) {
	o, p := k.o, k.plan
	if p.Build != keeperBuildStamp() {
		return nil, fmt.Errorf("it was made by yolo %s and this keeper is yolo %s", p.Build, keeperBuildStamp())
	}
	cfg, err := p.config()
	if err != nil {
		return nil, err
	}
	if p.PackTree != "" {
		packs, err := loadPackTree(p.PackTree)
		if err != nil {
			return nil, fmt.Errorf("its pack tree %s does not load: %w", p.PackTree, err)
		}
		names := make([]string, 0, len(packs))
		for _, pk := range packs {
			names = append(names, pk.Name)
		}
		if !slices.Equal(names, p.Packs) {
			return nil, fmt.Errorf("its pack tree holds %s, and the launch disclosed %s",
				strings.Join(names, ", "), strings.Join(p.Packs, ", "))
		}
		adoptPackRecords(packs)
	}
	planned := o.plannedLoopholeNames(p.Runtime, cfg)
	for _, name := range planned {
		if !slices.Contains(p.Services, name) {
			return nil, fmt.Errorf("it would start the host service %q, which the launch did not disclose", name)
		}
	}
	// EXACTLY THAT PLAN, the other way round too: a service the launch disclosed and this keeper
	// would not start is a terminal told something runs that never does, and a keeper whose packs
	// did not resolve as the launch's did (adoptPackRecords above) would otherwise start a jail
	// short of every pack's services and say nothing.
	for _, name := range p.Services {
		if !slices.Contains(planned, name) {
			return nil, fmt.Errorf("the launch disclosed the host service %q, which this keeper would not start", name)
		}
	}
	return cfg, nil
}

// awaitRunning is the fresh path's onStarted, in the keeper: it waits (bounded) for the container to
// be seen running, releases the launch lock, tells the launch, and arms the Window A probe on the
// main process's client, which is this process's child now (JL-D62).
func (k *keeper) awaitRunning() {
	o, p := k.o, k.plan
	defer close(k.running)
	ctrID := ""
	for i := 0; i < lockReleasePollAttempts; i++ {
		if id, known := o.probeRunningContainer(p.Cname, p.Runtime, attachProbeTimeout); known && id != "" {
			ctrID = id
			break
		}
		select {
		case <-k.jm.exited:
			i = lockReleasePollAttempts
		case <-time.After(time.Duration(lockReleasePollIntervalSeconds * float64(time.Second))):
		}
	}
	k.releaseLaunchLock()
	k.sink.event(frameRunning, "")
	o.startLingerProbe(p.Runtime, p.Cname, ctrID, k.jm.cmd.Process)
}

// watchSessions blocks, on its own goroutine, on the session lock taken exclusively, and closes the
// returned channel once it holds it: zero sessions. A lock it cannot open is never zero (JL-D3,
// JL-P3): the channel is nil, which never fires, and the jail ends only when its container does.
func (k *keeper) watchSessions() <-chan struct{} {
	if k.plan.Uncounted {
		k.sink.logf("keeper: the launch that started %s could not count its first session, so the count cannot say when the last session leaves; this jail ends only when its container does, and %s ends it",
			k.plan.Cname, stopRemedy(k.plan.Runtime, k.plan.Cname))
		return nil
	}
	f, err := openSessionLock(k.plan.Cname)
	if err != nil {
		k.sink.logf("keeper: cannot open the session lock (%v), so this jail ends only when its container does; `yolo stop` ends it", err)
		return nil
	}
	drained := make(chan struct{})
	go func() {
		if err := flockSyscall(int(f.Fd()), syscall.LOCK_EX); err != nil {
			k.sink.logf("keeper: cannot take the session lock (%v), so this jail ends only when its container does", err)
			_ = f.Close()
			return
		}
		k.mu.Lock()
		k.sessions = f
		k.mu.Unlock()
		close(drained)
	}()
	return drained
}

// watchContainer closes the returned channel once the container has ended some other way: on podman
// `podman wait` answered, or the main process's client exited and the runtime then says no container
// of the name runs, whichever comes first (§9.5 item 3: "It confirms with the same probe").
//
// THE CLIENT'S EXIT IS NOT THE CONTAINER'S END. The container is conmon's, not the client's: a client
// that was killed, or a Mac's remote client that lost its podman machine, exits while the container
// runs on with sessions in it. Read as the end, it had the keeper take the jail's host services down
// under those sessions and then leave, with the jail's records gone, so the container, whose main
// process is a hold that never ends by itself, ran on with no owner and was entered by the next
// arrival with no word of why. So the exit is confirmed by a bounded probe, and a probe that finds
// the container still running, or cannot say ("could not ask" is never "gone", JL-P3), leaves the
// keeper holding: it logs that once and looks again every keeperClientGonePoll, since the one
// observation that needed no poll is gone with the client.
func (k *keeper) watchContainer() <-chan struct{} {
	o, p := k.o, k.plan
	ended := make(chan struct{})
	var once sync.Once
	done := func() { once.Do(func() { close(ended) }) }
	go func() {
		<-k.jm.exited
		k.awaitNotRunning(done)
	}()
	if p.Runtime == "podman" {
		go func() {
			res := o.Exec([]string{p.Runtime, "wait", p.Cname}, "", nil, 0)
			if res.Ran && !res.Timeout && res.RC == 0 && strings.TrimSpace(res.Stdout) != "" {
				done()
			}
		}()
	}
	return ended
}

// keeperClientGonePoll is how often a keeper whose main-process client exited under a running
// container asks the runtime again whether it still runs (watchContainer). A var so a test need not
// wait it out.
var keeperClientGonePoll = 5 * time.Second

// awaitNotRunning is the confirmation of a client's exit: done once a bounded probe answers that no
// container of the name runs, and never while it runs or the runtime cannot say. It returns at the
// keeper's end, whatever it found.
func (k *keeper) awaitNotRunning(done func()) {
	o, p := k.o, k.plan
	said := false
	for {
		id, known := o.probeRunningContainer(p.Cname, p.Runtime, trackingProbeTimeout)
		if known && id == "" {
			done()
			return
		}
		if !said {
			said = true
			if known {
				k.sink.logf("keeper: the main process's runtime client exited while %s still runs; keeping the jail and its host services until it ends", p.Cname)
			} else {
				k.sink.logf("keeper: the main process's runtime client exited, and %s could not say whether %s still runs; keeping the jail and its host services, and asking again", p.Runtime, p.Cname)
			}
		}
		select {
		case <-k.ending:
			return
		case <-time.After(keeperClientGonePoll):
		}
	}
}

// endJail stops the container when stop is set, recording reason first, confirms it is gone,
// removing a stopped leftover (JL-D9), and runs today's teardown chain.
//
// A CONTAINER ITS STOP DID NOT END is left UNKEPT, never unowned: the chain takes down what the
// keeper runs, and leaves what the container holds (the guarded cleanups back off a container that
// exists), and the keeper puts back the owner-PID file the chain removes and keeps its start record
// (finish). Those two are how every reader tells a jail whose keeper is gone: an arrival is refused
// and pointed at `yolo stop` (JL-D13), its last session or `yolo stop` reaps it (JL-D30), and the
// orphan sweep reaps it once no session is in it (JL-D7). Without them nothing could prove the
// jail's owner dead, and its hold would keep it running for good.
func (k *keeper) endJail(reason string, stop bool) {
	o, p := k.o, k.plan
	if stop && k.jm != nil {
		sp := o.Perf.Span("keeper.stop_jail")
		o.stopJail(p.Cname, p.Runtime, reason)
		sp.End()
	}
	k.jailLeft = !k.confirmGone()
	rc := 0
	if k.jm != nil {
		select {
		case <-k.jm.exited:
			rc = k.jm.exitCode
		default:
		}
	}
	o.teardownAfterExit(k.socat, p.ForwardDir, k.handles, p.SocketsDir, p.Cname, p.Runtime, p.Skeleton, rc)
	if k.jailLeft {
		o.writeOwnerPID(p.Cname)
	}
}

// keeperGoneAttempts bounds confirmGone's wait for the container to be gone, restartPollInterval
// apart. A var so a test need not wait it out.
var keeperGoneAttempts = restartPollAttempts

// confirmGone waits, bounded, for the tri-state existence probe to answer that no container of the
// name exists, and removes a stopped leftover it finds: a `--rm` removal fails while a headless exec
// was live at the stop (MEASURED in nested podman, §4.4). It never removes a running container. It
// reports whether the container is gone.
func (k *keeper) confirmGone() bool {
	o, p := k.o, k.plan
	for i := 0; i < keeperGoneAttempts; i++ {
		if id, known := o.probeExistingContainer(p.Cname, p.Runtime, trackingProbeTimeout); known && id == "" {
			return true
		}
		time.Sleep(restartPollInterval)
	}
	if id, known := o.probeRunningContainer(p.Cname, p.Runtime, trackingProbeTimeout); known && id == "" {
		if o.removeStaleContainer(p.Cname, p.Runtime) {
			k.sink.logf("keeper: removed the stopped container %s its --rm left behind", p.Cname)
			return true
		}
	}
	k.sink.logf("keeper: %s is still there after its stop, so the keeper leaves it unkept: its host-services dir and records stay, a new session is refused, and %s ends it", p.Cname, stopRemedy(p.Runtime, p.Cname))
	return false
}

// keeperSessionsWait bounds the keeper's wait, after the container ended, for the sessions whose
// exec ended to drop their locks. A session still holding on after it is logged and left: its stale
// lock can only keep the next jail up longer, never end it sooner (JL-P3).
var keeperSessionsWait = 30 * time.Second

// finish is the keeper's last act: it takes the session lock exclusively once its sessions have let
// it go, when drained is still pending (JL-D28 (2)), kills a main process client that outlived its
// container (the chain never waits on it, so Window A stays off every terminal), removes the start
// record it wrote, and lets the session lock go. The liveness lock goes last, with run's return.
func (k *keeper) finish(rc int, drained <-chan struct{}) int {
	p := k.plan
	if drained != nil {
		select {
		case <-drained:
		case <-time.After(keeperSessionsWait):
			k.sink.logf("keeper: a session of %s still holds the session lock after %s; leaving it", p.Cname, keeperSessionsWait)
		}
	}
	k.endingOnce.Do(func() { close(k.ending) })
	if k.jm != nil {
		select {
		case <-k.jm.exited:
		default:
			k.o.Perf.Mark("shutdown.window_a_cut.keeper")
			_ = k.jm.cmd.Process.Kill()
		}
	}
	if !k.jailLeft {
		removeKeeperRecord(p.Cname, k.pid)
	}
	k.sink.logf("keeper: done")
	k.mu.Lock()
	releaseLock(k.sessions)
	k.sessions = nil
	k.mu.Unlock()
	return rc
}

// unwindUnstarted is a keeper that ends before its container exists: it releases the launch lock,
// stops what it started and removes the records the launch handed it, as the fresh path's
// runtime-not-found branch did (JL-D31: release the lock, then clean up).
func (k *keeper) unwindUnstarted(rc int) int {
	o, p := k.o, k.plan
	k.releaseLaunchLock()
	cleanupPortForwarding(k.socat, p.ForwardDir)
	discardUnheldSkeleton(p.Cname, p.Skeleton)
	forgetLivePackTree(p.Cname, p.PackTree)
	discardPackTree(p.Cname, p.PackTree)
	o.stopLoopholes(k.handles, p.SocketsDir, p.Cname, p.Runtime)
	o.forgetGoneContainer(p.Cname, p.Runtime, p.Skeleton)
	if k.liveness != nil {
		clearOwnerPIDIf(p.Cname, k.pid)
		removeKeeperRecord(p.Cname, k.pid)
	}
	return rc
}

// plannedLoopholeNames are the host services a container launch starts, by name, sorted: every
// enabled loophole the backend allows whose host daemon the spawn would run, and the in-process
// cgroup delegate when its record turns it on. The launch discloses them in its keeper line and
// carries them in the plan; the keeper refuses to start one that is not among them (checkPlan). One
// function for both, over the same set the spawn walks (startLoopholesMatching), so they cannot
// disagree except when the two processes' packs do.
func (o *Options) plannedLoopholeNames(rt string, cfg *jsonx.OrderedMap) []string {
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	allow := o.loopholeAllow(rt, cfg)
	var names []string
	if allow(paths.BuiltinCgroupLoopholeName) && o.cgroupDelegateHonored(set) {
		names = append(names, paths.BuiltinCgroupLoopholeName)
	}
	var discovered []*loopholes.Loophole
	for _, lp := range set.Enabled() {
		if allow(lp.Name) {
			discovered = append(discovered, lp)
		}
	}
	order, _ := hostDaemonOrder(set, discovered, cfg, allow)
	names = append(names, order...)
	sort.Strings(names)
	return slices.Compact(names)
}

// Why the keeper stops a jail, for the sessions it ends (stopreason.go).
const lastSessionLeftReason = "its last session ended"

func keeperSignalledReason(pid int) string {
	return fmt.Sprintf("its keeper (pid %d) was sent a signal, and ended it in order", pid)
}

func bootClientGoneReason(pid int) string {
	return fmt.Sprintf("the runtime client that started it exited before its boot was done, so its keeper (pid %d) ended it", pid)
}

func launchGoneReason(pid int) string {
	return fmt.Sprintf("the launch that started it was gone before it was ready, so its keeper (pid %d) ended it", pid)
}
