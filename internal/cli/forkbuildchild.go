package cli

// forkbuildchild.go runs a fork's BUILD JAIL as a CHILD yolo process, `yolo internal
// fork-build-jail`, for the one build a Ctrl-C must end without ending its launch: a patched
// fork's advance while a good build serves (docs/design/patched-forks.md §7, PF-D25).
//
// WHY A CHILD. A build jail is a whole launch, run through the same pipeline (runCaptureJail), and
// a launch run in this process installs signal arms of its own whose exit ends the process
// (run's armstack.go): in-process, a Ctrl-C during the build tears the build jail down and ends the
// user's launch with it, which is right for a plain fork's build and a first advance, and is the
// one thing PF-D25 rules out here. A child takes the arms with it: the terminal's SIGINT reaches
// the child's arms, which tear its build jail down and exit, while this process's interrupt scope
// (run.InterruptScope) ends the advance, and the launch goes on with the good build.
//
// THE CHILD IS TOLD, NOT TRUSTED: a SIGINT that reached this process alone (`kill -INT`) is sent on
// to the child, and a child that has not exited a grace period after it is killed — its keeper
// then unwinds the build jail as its lifeline closes. A build past forkBuildWaitBound is ended the
// same way, and that one is a failed build (§8.1).
//
// THE JAIL'S OWN STREAMS CROSS APART. The child's launch prints its own lines on the child's stdout
// and stderr, and relays its jail's — the runtime client's and pid 1's — to the child's fds 3 and 4
// (--jail-streams), which this process copies to the act's jail writers, and says on fd 5 that the
// jail's boot is done. So the act keeps the two apart in a child as it does in its own process, and
// a jail that stopped before its build line is relayed with what the jail said rather than its
// keeper's last lines (jailTail, PPX-D39).

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

// forkBuildChildGrace is how long a child told to stop has before it is killed.
const forkBuildChildGrace = 60 * time.Second

// forkBuildChildDrain bounds the wait, once the child has exited, for the last of its jail's lines
// to be copied (childJailPipes.drain): a process the child handed its descriptors to by mistake
// would otherwise hold the copy open for as long as it lives.
const forkBuildChildDrain = 5 * time.Second

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
	return append(argv, "--", b.Fork.Build)
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
	cmd.Stdout, cmd.Stderr, cmd.ExtraFiles = s.out, errw, jail.child
	if err := cmd.Start(); err != nil {
		jail.abandon()
		fmt.Fprintf(errw, "yolo: could not start the build jail of %s: %v\n", b.Fork.Key(), err)
		return 1, false
	}
	jail.copyTo(s.jailOut, s.jailErr, s.jailReady)
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		// Every line the jail printed is copied before the child's status is read.
		jail.drain(forkBuildChildDrain)
		done <- err
	}()
	timer := time.NewTimer(bound)
	defer timer.Stop()
	timedOut := false
	select {
	case err := <-done:
		return exitStatus(err), false
	case <-ctx.Done():
	case <-timer.C:
		timedOut = true
	}
	_ = cmd.Process.Signal(syscall.SIGINT)
	select {
	case err := <-done:
		return exitStatus(err), timedOut
	case <-time.After(forkBuildChildGrace):
	}
	_ = cmd.Process.Kill()
	return exitStatus(<-done), timedOut
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
func (p *childJailPipes) drain(bound time.Duration) {
	copied := make(chan struct{})
	go func() { p.copied.Wait(); close(copied) }()
	select {
	case <-copied:
	case <-time.After(bound):
	}
	for _, f := range p.read {
		_ = f.Close()
	}
	<-copied
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
