package cli

// forkbuildchild.go runs a fork's BUILD JAIL as a CHILD yolo process, `yolo internal
// fork-build-jail`, for every build a jail launch runs (buildreport.go) and for the one build a
// Ctrl-C must end without ending its launch, a patched advance's while a good build serves, at
// `yolo host` too (docs/design/patched-forks.md §7, PF-D25, PF-D79).
//
// WHY A CHILD. A build jail is a whole launch, run through the same pipeline (runCaptureJail), and
// a launch run in this process installs signal arms of its own whose exit ends the process
// (run's armstack.go): in-process, a Ctrl-C during the build tears the build jail down and ends the
// user's launch with it, which is the one thing PF-D25 rules out while a good build serves. A child
// takes the arms with it, but it must not share the launch's process group: otherwise one terminal
// Ctrl-C reaches the pool and every child independently, and the pool's later signal reaches only
// a child's pid, not compiler descendants holding its stdout/stderr pipes. The launch's act scope
// owns the interrupt; each build child gets its own process group, which the scope signals and then
// kills as a unit. The child retains its own launch signal arms for teardown, and its streams are
// pipes this process reads, so only its output can be kept off the launch's terminal (PF-D79).
//
// THE CHILD IS TOLD, NOT TRUSTED: a SIGINT that reached this process alone (`kill -INT`) is sent to
// the child's process group. If the child or a descendant has not exited after the five-second
// grace, the whole group is killed; the child then unwinds its build jail as its lifeline closes. A
// build past forkBuildWaitBound is ended the same way, and that one is a failed build (§8.1).
//
// THE JAIL'S OWN STREAMS CROSS APART. The child's launch prints its own lines on the child's stdout
// and stderr, and relays its jail's — the runtime client's and pid 1's — to the child's fds 3 and 4
// (--jail-streams), which this process copies to the act's jail writers, and says on fd 5 that the
// jail's boot is done. So the act keeps the two apart in a child as it does in its own process, and
// a jail that stopped before its build line is relayed with what the jail said rather than its
// keeper's last lines (jailTail, PPX-D39). And the launch reading them can tell the nested launch's
// own lines, its warnings and refusals among them, from the jail's (buildreport.go): until the
// ready, the child's stdout and stderr carry the launch's lines alone.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// forkBuildJailVerb is the hidden subcommand: `yolo internal fork-build-jail`.
const forkBuildJailVerb = "fork-build-jail"

// forkBuildChildGrace is how long a child told to stop has before its process group is killed.
var forkBuildChildGrace = 5 * time.Second

// forkBuildChildDrain bounds the drain of a child's stdout/stderr and jail streams after its root
// process exits; a detached descendant must not keep the startup launch waiting on an inherited fd.
var forkBuildChildDrain = 5 * time.Second

// The child's descriptors for its jail (--jail-streams), ExtraFiles' three after stdin, stdout and
// stderr: its own stdout and stderr, and the one byte that says its boot is done
// (run.Options.OnJailReady).
const childJailStdoutFD, childJailStderrFD, childJailReadyFD = 3, 4, 5

// childJailFDs are the three, in ExtraFiles order.
var childJailFDs = []int{childJailStdoutFD, childJailStderrFD, childJailReadyFD}

// forkBuildJailStreamsFlag tells the child that fds 3 to 5 carry its jail's (childJailFDs).
const forkBuildJailStreamsFlag = "--jail-streams"

// forkBuildChild runs b's build jail in a child process: a var so a test can stand in for the
// child.
var forkBuildChild = runForkBuildChild

// forkBuildChildCommand is the child's command for argv (the args after the binary): this very
// binary, exec'd as the keeper is (run.SelfExecPath), so a `just install` during a long launch never
// runs the build jail from another build of yolo. A test binary never self-execs: that would run
// the package's whole suite in the child. A var so a test can run a stand-in.
var forkBuildChildCommand = func(argv []string) (*exec.Cmd, error) {
	if flag.Lookup("test.v") != nil {
		return nil, errForkBuildChildFromTest
	}
	return forkBuildChildExec(argv), nil
}

// forkBuildChildExec is the child's command, unguarded: this very binary with argv.
func forkBuildChildExec(argv []string) *exec.Cmd { return exec.Command(run.SelfExecPath(), argv...) }

// errForkBuildChildFromTest refuses the one spawn a unit test must never make (forkBuildChildCommand).
var errForkBuildChildFromTest = errors.New("a test binary does not self-exec the fork build jail")

// forkBuildChildArgv is the child's arguments for b's build in staging.
func forkBuildChildArgv(staging string, b forkBuild, color bool) []string {
	argv := []string{"internal", forkBuildJailVerb, "--workspace=" + staging, "--bin=" + b.Fork.Bin}
	if b.Fork.IsTree() {
		// A PATCHED EXTENSION's build jail: the tree's final copy, and the seal narrowed to the
		// contributing pack (PPX-D5).
		argv = append(argv, "--tree="+b.Fork.Bin)
	}
	for _, p := range sealPacks(b.Fork) {
		argv = append(argv, "--only="+p)
	}
	if color {
		argv = append(argv, "--color")
	}
	// runForkBuildChild hands every child its jail's descriptors, fds 3 to 5.
	argv = append(argv, forkBuildJailStreamsFlag)
	return append(argv, "--", b.buildLine())
}

// runForkBuildChild runs b's build jail as a child and returns its exit status, and whether it was
// stopped for running past bound. A cancelled ctx (the interrupt scope's Ctrl-C) stops it too, and
// the caller reads that off ctx.
//
// s's out and errw take the child's stdout and stderr, which carry its launch's own lines, and s's
// jail writers what the child relays its jail's own lines to, its fds 3 and 4, a nil one being this
// process's own stream; s.jailReady is called when the child says on fd 5 that the jail's boot is
// done (childJailPipes).
func runForkBuildChild(ctx context.Context, bound time.Duration, staging string, b forkBuild, s captureStreams,
	color bool) (int, bool) {
	errw := s.errw
	if errw == nil {
		errw = io.Discard
	}
	cmd, err := forkBuildChildCommand(forkBuildChildArgv(staging, b, color))
	if err != nil {
		fmt.Fprintf(errw, "yolo: could not start the build jail of %s: %v\n", b.Fork.Key(), err)
		return 1, false
	}
	jail, err := newChildJailPipes()
	if err != nil {
		fmt.Fprintf(errw, "yolo: could not start the build jail of %s: %v\n", b.Fork.Key(), err)
		return 1, false
	}
	output, err := newForkBuildChildOutput()
	if err != nil {
		jail.abandon()
		fmt.Fprintf(errw, "yolo: could not start the build jail of %s: %v\n", b.Fork.Key(), err)
		return 1, false
	}
	cmd.Stdout, cmd.Stderr, cmd.ExtraFiles = output.writers[0], output.writers[1], jail.child
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// A launch build child owns its process group. Ctrl-C is caught by the launch's one act
	// interrupt and forwarded to this group, not delivered independently by the terminal to
	// several nested yolo/runtime processes with competing teardown arms.
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		jail.abandon()
		output.abandon()
		fmt.Fprintf(errw, "yolo: could not start the build jail of %s: %v\n", b.Fork.Key(), err)
		return 1, false
	}
	output.copyTo(s.out, errw)
	jail.copyTo(s.jailOut, s.jailErr, s.jailReady)
	done := make(chan forkBuildChildResult, 1)
	go func() {
		err := cmd.Wait()
		var drains sync.WaitGroup
		jailDrained, outputDrained := true, true
		drains.Go(func() { jailDrained = jail.drain(forkBuildChildDrain) })
		drains.Go(func() { outputDrained = output.drain(forkBuildChildDrain) })
		drains.Wait()
		done <- forkBuildChildResult{err: err, streamsDrained: jailDrained && outputDrained}
	}()
	timer := time.NewTimer(bound)
	defer timer.Stop()
	timedOut := false
	status := func(result forkBuildChildResult) int {
		if ctx.Err() != nil || timedOut {
			// A child may handle SIGINT and exit 0 after leaving half-written staging output. An
			// interrupted or bound-stopped child is never a successful build, whichever status it chose.
			return 128 + int(syscall.SIGINT)
		}
		if !result.streamsDrained {
			return 1
		}
		return exitStatus(result.err)
	}
	select {
	case result := <-done:
		returned := forkBuildRunReturned(staging)
		if s.lifetimeReturned != nil {
			*s.lifetimeReturned = returned
		}
		_, runtimeErr := readForkBuildRuntime(staging)
		lifetimeUnknown := !returned && runtimeErr == nil
		if ctx.Err() != nil || !result.streamsDrained || childExitWasSignalled(result.err) || lifetimeUnknown {
			// An externally handled signal exits normally, but bypasses runCaptureJail's return witness.
			// A resolved backend plus no witness therefore retains the workspace and stops any same-group
			// helper without guessing from 129/130/143. Group exit still is not keeper completion.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			retainUnlessNothingDispatched(s, staging, waitForkBuildChildGroup(cmd.Process.Pid, time.Second))
		}
		return status(result), false
	case <-ctx.Done():
	case <-timer.C:
		timedOut = true
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	var result forkBuildChildResult
	select {
	case result = <-done:
	case <-time.After(forkBuildChildGrace):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		result = <-done
	}
	// done includes bounded pipe drainage. Even if an output-drain bound expired, no member of
	// this owned process group may keep compiling after the launch has stopped waiting.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	retainUnlessNothingDispatched(s, staging, waitForkBuildChildGroup(cmd.Process.Pid, time.Second))
	return status(result), timedOut
}

// retainUnlessNothingDispatched marks a stopped build's workspace retained unless its whole process
// group is confirmed gone AND it never recorded a resolved runtime. run.Run records the runtime
// before it dispatches any backend, so a dead group with no record had started no capture jail or
// keeper, and its workspace is safe to clean; anything else keeps it fenced for the retry check.
func retainUnlessNothingDispatched(s captureStreams, staging string, groupGone bool) {
	if groupGone {
		if _, err := readForkBuildRuntime(staging); errors.Is(err, os.ErrNotExist) {
			return
		}
	}
	markBuildWorkspaceCleanupUnconfirmed(s)
}

func markBuildWorkspaceCleanupUnconfirmed(s captureStreams) {
	if s.cleanupUnconfirmed != nil {
		*s.cleanupUnconfirmed = true
	}
}

// forkBuildChildResult is the child status and whether both output paths drained before their
// bound. An incomplete drain is a build failure, never a success whose output was truncated.
type forkBuildChildResult struct {
	err            error
	streamsDrained bool
}

// forkBuildChildOutput gives the process tree explicit stdout/stderr pipes, which lets this parent
// close a reader after a bounded drain instead of os/exec.Wait waiting forever on an inherited fd.
type forkBuildChildOutput struct {
	readers [2]*os.File
	writers [2]*os.File
	copied  sync.WaitGroup
}

func newForkBuildChildOutput() (*forkBuildChildOutput, error) {
	p := &forkBuildChildOutput{}
	for i := range p.readers {
		r, w, err := os.Pipe()
		if err != nil {
			p.abandon()
			return nil, err
		}
		p.readers[i], p.writers[i] = r, w
	}
	return p, nil
}

// copyTo closes the parent's writer copies, then relays the child's stdout and stderr.
func (p *forkBuildChildOutput) copyTo(out, errw io.Writer) {
	if out == nil {
		out = io.Discard
	}
	if errw == nil {
		errw = io.Discard
	}
	for _, w := range p.writers {
		_ = w.Close()
	}
	for i, dst := range []io.Writer{out, errw} {
		r := p.readers[i]
		p.copied.Go(func() { _, _ = io.Copy(dst, r) })
	}
}

// drain returns false if descendants kept stdout/stderr open past the bound. Closing the readers
// ends those copies, and the caller treats their incomplete output as a failed build.
func (p *forkBuildChildOutput) drain(bound time.Duration) bool {
	done := make(chan struct{})
	go func() { p.copied.Wait(); close(done) }()
	complete := true
	select {
	case <-done:
	case <-time.After(bound):
		complete = false
	}
	for _, r := range p.readers {
		_ = r.Close()
	}
	<-done
	return complete
}

func (p *forkBuildChildOutput) abandon() {
	for _, f := range append(p.readers[:], p.writers[:]...) {
		if f != nil {
			_ = f.Close()
		}
	}
}

// waitForkBuildChildGroup waits briefly for SIGKILLed descendants to leave the process table and
// reports whether the group is confirmed gone (ESRCH); a lingering member or zombie reads as false.
func waitForkBuildChildGroup(pid int, bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for {
		if errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// childJailPipes are the three pipes a child build jail is handed for its jail (childJailFDs), and
// what this process reads from them.
type childJailPipes struct {
	child  []*os.File // the write ends, the child's ExtraFiles, closed here once it started
	read   []*os.File
	copied sync.WaitGroup
}

// newChildJailPipes makes the three pipes.
func newChildJailPipes() (*childJailPipes, error) {
	p := &childJailPipes{}
	for range childJailFDs {
		r, w, err := os.Pipe()
		if err != nil {
			p.abandon()
			return nil, err
		}
		p.read, p.child = append(p.read, r), append(p.child, w)
	}
	return p, nil
}

// copyTo closes this process's copies of the child's ends, so a read ends when the child's own end
// closes, and copies the child's jail stdout to out and its jail stderr to errw, this process's own
// streams when nil, and calls ready, when non-nil, when the child says the boot is done.
func (p *childJailPipes) copyTo(out, errw io.Writer, ready func()) {
	for _, f := range p.child {
		_ = f.Close()
	}
	if out == nil {
		out = os.Stdout
	}
	if errw == nil {
		errw = os.Stderr
	}
	for i, w := range []io.Writer{out, errw} {
		r := p.read[i]
		p.copied.Go(func() { _, _ = io.Copy(w, r) })
	}
	said := p.read[2]
	p.copied.Go(func() {
		var b [1]byte
		if n, _ := said.Read(b[:]); n == 1 && ready != nil {
			ready()
		}
	})
}

// drain waits, at most bound, for the copies to reach the end of the child's lines, then closes
// the read ends, which ends a copy something else still holds open.
func (p *childJailPipes) drain(bound time.Duration) bool {
	copied := make(chan struct{})
	go func() { p.copied.Wait(); close(copied) }()
	complete := true
	select {
	case <-copied:
	case <-time.After(bound):
		complete = false
	}
	for _, f := range p.read {
		_ = f.Close()
	}
	<-copied
	return complete
}

// abandon closes every end, for a child that never started.
func (p *childJailPipes) abandon() {
	for _, f := range append(p.child, p.read...) {
		_ = f.Close()
	}
}

// forkBuildJailStreams is the jail's descriptors a child build jail was handed (--jail-streams):
// its stdout, its stderr and its ready, and whether it was handed them. A var so a test, whose own
// fds 3 to 5 are not its to take, can stand in for them.
var forkBuildJailStreams = inheritedJailStreams

// inheritedJailStreams is fds 3 to 5, when all three are the pipes runForkBuildChild hands a child.
// They are not handed on: the keeper and the runtime client this child starts would hold the
// parent's reads open past the child's own exit.
func inheritedJailStreams() (out, errw, ready *os.File, ok bool) {
	for _, fd := range childJailFDs {
		var st syscall.Stat_t
		if syscall.Fstat(fd, &st) != nil || uint32(st.Mode)&syscall.S_IFMT != syscall.S_IFIFO {
			return nil, nil, nil, false
		}
	}
	for _, fd := range childJailFDs {
		syscall.CloseOnExec(fd)
	}
	return os.NewFile(childJailStdoutFD, "jail-stdout"), os.NewFile(childJailStderrFD, "jail-stderr"),
		os.NewFile(childJailReadyFD, "jail-ready"), true
}

// childExitWasSignalled distinguishes a child killed by an external signal from an ordinary
// compiler failure, whose workspace is safe to clean after the child and its streams have ended.
func childExitWasSignalled(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	ws, ok := exit.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled()
}

// exitStatus is a child's exit status, 128+N for a signal, 1 for anything else that is not 0.
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		if code := exit.ExitCode(); code > 0 {
			return code
		}
	}
	return 1
}

// runForkBuildJail is `yolo internal fork-build-jail --workspace=W --bin=B --only=P... [--color]
// [--jail-streams] -- <build>`: the build jail of one fork, in the staged workspace W, under the
// seal and narrowed to the packs named (forkBuildRunJail's jail, run here as a child of the launch
// that staged it), relaying its jail's own lines to fds 3 and 4 and its boot's end to fd 5 with
// --jail-streams. Hidden: its caller is the advance.
func runForkBuildJail(args []string, out, errw io.Writer) int {
	var workspace, bin, build, tree string
	var only []string
	color, jailStreams := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			build = strings.Join(args[i+1:], " ")
			i = len(args)
		case strings.HasPrefix(a, "--workspace="):
			workspace = strings.TrimPrefix(a, "--workspace=")
		case strings.HasPrefix(a, "--bin="):
			bin = strings.TrimPrefix(a, "--bin=")
		case strings.HasPrefix(a, "--tree="):
			tree = strings.TrimPrefix(a, "--tree=")
		case strings.HasPrefix(a, "--only="):
			only = append(only, strings.TrimPrefix(a, "--only="))
		case a == "--color":
			color = true
		case a == forkBuildJailStreamsFlag:
			jailStreams = true
		default:
			fmt.Fprintf(errw, "fork-build-jail: unexpected argument %q\n", a)
			return 2
		}
	}
	// A tree's build line is optional (PPX-D3): its jail still copies the checkout and is admitted.
	if workspace == "" || bin == "" || (build == "" && tree == "") || len(only) == 0 {
		fmt.Fprintln(errw, "usage: yolo internal fork-build-jail --workspace=DIR --bin=NAME [--tree=NAME] --only=PACK... [--color] -- BUILD")
		return 2
	}
	argv := forkBuildJailArgv(build)
	if tree != "" {
		argv = treeBuildJailArgv(build, tree)
	}
	s := captureStreams{out: out, errw: errw}
	if jailStreams {
		// Without them, the jail's lines go to this process's own streams, mixed with the launch's
		// as they cross to the parent, and the build still runs.
		if jo, je, jr, ok := forkBuildJailStreams(); ok {
			defer jo.Close()
			defer je.Close()
			defer jr.Close()
			s.jailOut, s.jailErr = jo, je
			s.jailReady = func() { _, _ = jr.Write([]byte{'R'}); _ = jr.Close() }
		}
	}
	return runCaptureJail(workspace, bin, argv, &captureSeal{only: only, tree: tree}, s, color)
}
