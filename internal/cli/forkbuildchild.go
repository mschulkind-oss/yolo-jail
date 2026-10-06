package cli

// forkbuildchild.go runs a fork's BUILD JAIL as a CHILD yolo process, `yolo internal
// fork-build-jail`, for every build a jail launch runs (buildreport.go) and for the one build a
// Ctrl-C must end without ending its launch, a patched advance's while a good build serves, at
// `yolo host` too (docs/design/patched-forks.md §7, PF-D25, PF-D77).
//
// WHY A CHILD. A build jail is a whole launch, run through the same pipeline (runCaptureJail), and
// a launch run in this process installs signal arms of its own whose exit ends the process
// (run's armstack.go): in-process, a Ctrl-C during the build tears the build jail down and ends the
// user's launch with it, which is the one thing PF-D25 rules out while a good build serves. A child
// takes the arms with it: the terminal's SIGINT reaches the child's arms, which tear its build jail
// down and exit, while this process's interrupt scope (run.InterruptScope) ends the advance, and
// the launch goes on with the good build. And a child's streams are pipes this process reads,
// where an in-process build jail's container writes this process's own stdout and stderr, so only
// a child's output can be kept off the launch's terminal (PF-D77).
//
// THREE STREAMS. The child's stdout and stderr are its build jail's — the boot and the build line's
// output — and, with --launch-fd, its own launch's lines go to a third, so the launch that reads
// them can tell the nested launch's warnings and refusals from the build's output (buildreport.go).
//
// THE CHILD IS TOLD, NOT TRUSTED: a SIGINT that reached this process alone (`kill -INT`) is sent on
// to the child, and a child that has not exited a grace period after it is killed — its keeper
// then unwinds the build jail as its lifeline closes. A build past forkBuildWaitBound is ended the
// same way, and that one is a failed build (§8.1).

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
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

// forkBuildLaunchFDs are the descriptors a child prints its own launch's stdout and stderr on:
// exec.Cmd's ExtraFiles, the first of which is descriptor 3.
const forkBuildLaunchFDs = "3,4"

// forkBuildChildArgv is the child's arguments for b's build in staging.
func forkBuildChildArgv(staging string, b forkBuild, color bool) []string {
	return forkBuildChildArgvWith(staging, b, color, false)
}

// forkBuildChildArgvWith is forkBuildChildArgv, with launchFDs naming the streams the child prints
// its own launch's lines on (forkBuildLaunchFDs).
func forkBuildChildArgvWith(staging string, b forkBuild, color, launchFDs bool) []string {
	argv := []string{"internal", forkBuildJailVerb, "--workspace=" + staging, "--bin=" + b.Fork.Bin}
	if launchFDs {
		argv = append(argv, "--launch-fds="+forkBuildLaunchFDs)
	}
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
	return append(argv, "--", b.Fork.Build)
}

// forkBuildLaunchDrain bounds the wait for the child's launch streams to end once the child has
// exited: a descriptor a stray descendant still holds must not hold the launch.
const forkBuildLaunchDrain = 5 * time.Second

// launchPipes are the two pipes a child prints its own launch's stdout and stderr on.
type launchPipes struct{ r, w [2]*os.File }

// openLaunchPipes opens them, nil on any failure, which leaves the child's launch on its own stdout
// and stderr.
func openLaunchPipes() *launchPipes {
	p := &launchPipes{}
	for i := range p.r {
		r, w, err := os.Pipe()
		if err != nil {
			p.close()
			return nil
		}
		p.r[i], p.w[i] = r, w
	}
	return p
}

// close closes every end still open.
func (p *launchPipes) close() {
	if p == nil {
		return
	}
	closeFiles(p.r[0], p.r[1], p.w[0], p.w[1])
}

// runForkBuildChild runs b's build jail as a child and returns its exit status, and whether it was
// stopped for running past bound. A cancelled ctx (the interrupt scope's Ctrl-C) stops it too, and
// the caller reads that off ctx. s.launchOut and s.launchErr, when set, take the child's own
// launch's lines, on pipes of their own (--launch-fds); unset leaves them on s.out and s.errw.
func runForkBuildChild(ctx context.Context, bound time.Duration, staging string, b forkBuild, s jailStreams,
	color bool) (int, bool) {
	var pipes *launchPipes
	if s.launch() {
		if pipes = openLaunchPipes(); pipes == nil {
			fmt.Fprintf(s.errw, "yolo: could not open the launch streams of %s's build jail; its launch's lines "+
				"are on its own stdout and stderr\n", b.Fork.Key())
		}
	}
	cmd, err := forkBuildChildCommand(forkBuildChildArgvWith(staging, b, color, pipes != nil))
	if err != nil {
		pipes.close()
		fmt.Fprintf(s.errw, "yolo: could not start the build jail of %s: %v\n", b.Fork.Key(), err)
		return 1, false
	}
	cmd.Stdout, cmd.Stderr = s.out, s.errw
	if pipes != nil {
		cmd.ExtraFiles = []*os.File{pipes.w[0], pipes.w[1]} // the child's descriptors 3 and 4
	}
	if err := cmd.Start(); err != nil {
		pipes.close()
		fmt.Fprintf(s.errw, "yolo: could not start the build jail of %s: %v\n", b.Fork.Key(), err)
		return 1, false
	}
	var drained sync.WaitGroup
	if pipes != nil {
		for i, w := range []io.Writer{s.launchOut, s.launchErr} {
			_ = pipes.w[i].Close() // the child's copy is the one that ends the stream
			pipes.w[i] = nil
			drained.Add(1)
			go func(r *os.File, w io.Writer) {
				defer drained.Done()
				_, _ = io.Copy(w, r)
			}(pipes.r[i], w)
		}
	}
	// THE LAUNCH STREAMS END WITH THE CHILD: read to their end, bounded, once the child has exited.
	defer func() {
		ended := make(chan struct{})
		go func() { drained.Wait(); close(ended) }()
		select {
		case <-ended:
		case <-time.After(forkBuildLaunchDrain):
		}
		pipes.close()
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
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

// closeFiles closes each file that is not nil.
func closeFiles(fs ...*os.File) {
	for _, f := range fs {
		if f != nil {
			_ = f.Close()
		}
	}
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

// runForkBuildJail is `yolo internal fork-build-jail --workspace=W --bin=B [--launch-fds=O,E]
// --only=P... [--color] -- <build>`: the build jail of one fork, in the staged workspace W, under the
// seal and narrowed to the packs named (forkBuildRunJail's jail, run here as a child of the launch
// that staged it). With --launch-fds its launch's own stdout and stderr go to descriptors O and E,
// which no process it starts inherits, and the build jail's to out and errw. Hidden: its caller is
// the build act.
func runForkBuildJail(args []string, out, errw io.Writer) int {
	var workspace, bin, build, tree string
	var only []string
	var launchOut, launchErr io.Writer
	color := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			build = strings.Join(args[i+1:], " ")
			i = len(args)
		case strings.HasPrefix(a, "--launch-fds="):
			o, e, ok := launchFDs(strings.TrimPrefix(a, "--launch-fds="))
			if !ok {
				fmt.Fprintf(errw, "fork-build-jail: --launch-fds must name two descriptors past stderr, not %q\n", a)
				return 2
			}
			launchOut, launchErr = o, e
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
		default:
			fmt.Fprintf(errw, "fork-build-jail: unexpected argument %q\n", a)
			return 2
		}
	}
	// A tree's build line is optional (PPX-D3): its jail still copies the checkout and is admitted.
	if workspace == "" || bin == "" || (build == "" && tree == "") || len(only) == 0 {
		fmt.Fprintln(errw, "usage: yolo internal fork-build-jail --workspace=DIR --bin=NAME [--tree=NAME] [--launch-fd=N] "+
			"--only=PACK... [--color] -- BUILD")
		return 2
	}
	argv := forkBuildJailArgv(build)
	if tree != "" {
		argv = treeBuildJailArgv(build, tree)
	}
	if launchOut != nil {
		out, errw = launchOut, launchErr
	}
	return runCaptureJail(workspace, bin, argv, &captureSeal{only: only, tree: tree}, out, errw, color)
}

// launchFDs reads --launch-fds' "O,E" into the two files, each past stderr and not inherited by any
// process this one starts: the keeper, podman and nix the build jail's launch starts would otherwise
// hold the streams open past this process, and the launch reading them would wait for them.
func launchFDs(spec string) (io.Writer, io.Writer, bool) {
	o, e, ok := strings.Cut(spec, ",")
	if !ok {
		return nil, nil, false
	}
	var files [2]io.Writer
	for i, s := range []string{o, e} {
		fd, err := strconv.Atoi(s)
		if err != nil || fd < 3 {
			return nil, nil, false
		}
		syscall.CloseOnExec(fd)
		files[i] = os.NewFile(uintptr(fd), "launch")
	}
	return files[0], files[1], true
}
