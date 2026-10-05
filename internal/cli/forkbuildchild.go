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

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"
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
	return append(argv, "--", b.Fork.Build)
}

// runForkBuildChild runs b's build jail as a child and returns its exit status, and whether it was
// stopped for running past bound. A cancelled ctx (the interrupt scope's Ctrl-C) stops it too, and
// the caller reads that off ctx.
func runForkBuildChild(ctx context.Context, bound time.Duration, staging string, b forkBuild, out, errw io.Writer,
	color bool) (int, bool) {
	cmd, err := forkBuildChildCommand(forkBuildChildArgv(staging, b, color))
	if err != nil {
		fmt.Fprintf(errw, "yolo: could not start the build jail of %s: %v\n", b.Fork.Key(), err)
		return 1, false
	}
	cmd.Stdout, cmd.Stderr = out, errw
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(errw, "yolo: could not start the build jail of %s: %v\n", b.Fork.Key(), err)
		return 1, false
	}
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

// runForkBuildJail is `yolo internal fork-build-jail --workspace=W --bin=B --only=P... [--color] --
// <build>`: the build jail of one fork, in the staged workspace W, under the seal and narrowed to
// the packs named (forkBuildRunJail's jail, run here as a child of the launch that staged it).
// Hidden: its caller is the advance.
func runForkBuildJail(args []string, out, errw io.Writer) int {
	var workspace, bin, build, tree string
	var only []string
	color := false
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
	return runCaptureJail(workspace, bin, argv, &captureSeal{only: only, tree: tree}, out, errw, color)
}
