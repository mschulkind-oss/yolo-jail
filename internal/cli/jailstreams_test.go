package cli

// jailstreams_test.go pins that a build jail which stopped before its build line ran is relayed with
// what the JAIL said — its runtime client's and pid 1's lines, which a launch relays apart from its
// own (run.Options.JailStdout and JailStderr) — and not with its keeper's last lines, in the build
// act's own process and in the child a serving advance runs (docs/design/patched-extensions.md
// PPX-D39).

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// discardStreams is a capture jail's four writers, discarded.
var discardStreams = captureStreams{out: io.Discard, errw: io.Discard, jailOut: io.Discard, jailErr: io.Discard}

// runtimeRefusal is what the fixture's runtime says when it will not start the build jail.
const runtimeRefusal = "Error: simulated runtime refusal: crun: creating cgroup: Permission denied: OCI permission denied"

// keeperLines are the lines a launch prints around a runtime's refusal on its own stderr: its
// keeper's disclosure and start, then its end.
var keeperLines = []string{
	"keeper: yolo internal daemon jail-keeper will hold this jail's container until its last session leaves; " +
		"log: /fixture/jail-keeper.log",
	"keeper: started, pid 42",
}

// jailStderr is the writer a launch relays its jail's own stderr to: o.JailStderr, or the process's
// own stderr when it is nil, as run.Run fills it.
func jailStderr(o run.Options) io.Writer {
	if o.JailStderr != nil {
		return o.JailStderr
	}
	return os.Stderr
}

// runtimeRefusingBuildJail is a build jail the runtime would not start, as a launch prints it: the
// keeper's lines on the launch's stderr, the runtime's error on the jail's own, then `keeper: done`.
func runtimeRefusingBuildJail(o run.Options) int {
	for _, l := range keeperLines {
		fmt.Fprintln(o.Stderr, l)
	}
	fmt.Fprintln(jailStderr(o), runtimeRefusal)
	fmt.Fprintln(o.Stderr, "keeper: done")
	return 126
}

// assertRelaysTheRuntime fails unless text relays the runtime's refusal, and that alone.
func assertRelaysTheRuntime(t *testing.T, text string) {
	t.Helper()
	if !strings.Contains(text, "its build jail exited 126 before its build line ran") ||
		!strings.Contains(text, "\n    "+runtimeRefusal+"\n") {
		t.Errorf("the runtime's refusal is not what is relayed, on a line of its own:\n%s", text)
	}
	if strings.Contains(text, "    keeper:") || strings.Contains(text, " / ") {
		t.Errorf("the keeper's lines are relayed as the jail's account, or lines are joined:\n%s", text)
	}
}

// `yolo capture <pack>/<name>`, the reviewer's reproduction: a runtime that refuses the container
// is relayed with its own error. Red if the act stops handing the jail writers it tees, or the tail
// stops preferring them over the keeper's.
func TestCaptureOfATreeTheRuntimeRefusedRelaysTheRuntimesError(t *testing.T) {
	newTreeFixture(t, `"f.txt"`)
	withFakeCaptureJail(t, runtimeRefusingBuildJail)
	var out, errw bytes.Buffer
	if rc, handled := captureTree(treeKeyCLI, &out, &errw, false); !handled || rc == 0 {
		t.Fatalf("captureTree = %d, %v\n%s%s", rc, handled, out.String(), errw.String())
	}
	assertRelaysTheRuntime(t, errw.String())
}

// A launch's tree arm, the maintainer's scenario: the warning and the reason the jail is handed
// relay the runtime's error.
func TestATreeBuildJailTheRuntimeRefusedIsRelayedWithTheRuntimesError(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	withFakeCaptureJail(t, runtimeRefusingBuildJail)
	d, out := fx.deliver(t, true)
	if d.Dir != "" {
		t.Fatalf("a jail the runtime refused delivered %+v\n%s", d, out)
	}
	// A jail launch's act shows the build's result, and the cause goes to the launch, which says it
	// once (run's missingbuilds.go): its lines, each its own.
	if !strings.Contains(out, "Building extension "+treeKeyCLI+": its build jail exited 126 before its build line ran") {
		t.Errorf("the build's result does not say its jail stopped:\n%s", out)
	}
	if d.Cause == nil || !slices.Equal(d.Cause.Lines, []string{runtimeRefusal}) || d.Cause.YoloBug {
		t.Errorf("the cause the launch is handed is %+v, want the runtime's line alone, not yolo's fault", d.Cause)
	}
}

// `yolo capture <fork bin>`: a plain fork's build is relayed the same way.
func TestCaptureOfAForkTheRuntimeRefusedRelaysTheRuntimesError(t *testing.T) {
	forkBuildHome(t)
	withFakeCaptureJail(t, runtimeRefusingBuildJail)
	rc, stderr := runCaptureFor(t, "probetool")
	if rc == 0 {
		t.Fatalf("a refused build jail succeeded:\n%s", stderr)
	}
	assertRelaysTheRuntime(t, stderr)
}

// A JAIL THAT STOPPED AFTER ITS BOOT WAS DONE is relayed with nothing: the lines pid 1 printed on a
// boot that succeeded are not why it stopped, nor is its keeper's teardown, and the step points at
// the output above, where its session printed why. Red if the act stops handing the launch its
// ready, or the tail stops forgetting at it.
func TestABuildJailThatStoppedAfterItsBootRelaysNothing(t *testing.T) {
	forkBuildHome(t)
	withFakeCaptureJail(t, func(o run.Options) int {
		fmt.Fprintln(jailStderr(o), "  cgroup delegate: not available (no host daemon socket)")
		if o.OnJailReady != nil {
			o.OnJailReady()
		}
		fmt.Fprintln(o.Stderr, "keeper: the last session of yolo-fork-x left; ending the jail")
		fmt.Fprintln(o.Stderr, "keeper: done")
		return 1
	})
	rc, stderr := runCaptureFor(t, "probetool")
	if rc == 0 {
		t.Fatalf("a build jail that stopped succeeded:\n%s", stderr)
	}
	if strings.Contains(stderr, "    cgroup delegate") || strings.Contains(stderr, "    keeper:") {
		t.Errorf("a jail that stopped after its boot was relayed with lines that are not why:\n%s", stderr)
	}
	const want = "  Its output above says why: fix what it names, then run `yolo capture probetool` again."
	if got := stepAfter(t, stderr, "yolo capture: its build jail exited 1 before its build line ran"); got != want {
		t.Errorf("the step is\n%q\nwant\n%q", got, want)
	}
}

// THE CHILD'S HALF, as the parent sees it: the child's argv says its jail's descriptors are fds 3
// to 5, what the child writes on 3 and 4 reaches the act's jail writers, apart from what it writes
// on its stdout and stderr, and a byte on 5 is the jail's ready. Red if the runner stops handing
// the child the three pipes, or the argv stops saying so.
func TestTheChildBuildJailsOwnStreamsCrossApart(t *testing.T) {
	for _, booted := range []bool{false, true} {
		t.Run(fmt.Sprintf("booted=%v", booted), func(t *testing.T) {
			prev := forkBuildChildCommand
			var argv []string
			script := "echo launch-out; echo launch-err >&2; echo jail-out >&3; echo jail-err >&4; "
			if booted {
				script += "printf R >&5; "
			}
			forkBuildChildCommand = func(a []string) (*exec.Cmd, error) {
				argv = a
				return exec.Command("sh", "-c", script+"exit 125"), nil
			}
			t.Cleanup(func() { forkBuildChildCommand = prev })
			var out, errw, jailOut, jailErr bytes.Buffer
			readies := 0
			rc, bound := runForkBuildChild(context.Background(), time.Hour, "/s", forkBuild{},
				captureStreams{out: &out, errw: &errw, jailOut: &jailOut, jailErr: &jailErr,
					jailReady: func() { readies++ }}, false)
			if rc != 125 || bound {
				t.Fatalf("the child = %d (bound %v)\nstderr: %s", rc, bound, errw.String())
			}
			if !slices.Contains(argv, forkBuildJailStreamsFlag) {
				t.Errorf("the child's argv %q does not say its jail's descriptors are fds 3 to 5", argv)
			}
			if want := map[bool]int{false: 0, true: 1}[booted]; readies != want {
				t.Errorf("the jail's ready was seen %d times, want %d", readies, want)
			}
			assertCrossedApart(t, &out, &errw, &jailOut, &jailErr)
		})
	}
}

// assertCrossedApart fails unless each of the child fixture's four lines reached its own writer.
func assertCrossedApart(t *testing.T, out, errw, jailOut, jailErr *bytes.Buffer) {
	t.Helper()
	for _, c := range []struct {
		name string
		got  *bytes.Buffer
		want string
	}{{"stdout", out, "launch-out\n"}, {"stderr", errw, "launch-err\n"},
		{"jail stdout", jailOut, "jail-out\n"}, {"jail stderr", jailErr, "jail-err\n"}} {
		if c.got.String() != c.want {
			t.Errorf("the child's %s reached %q, want %q", c.name, c.got.String(), c.want)
		}
	}
}

// THE CHILD'S HALF, in the child: with the flag, the launch it runs relays the jail's own lines to
// the two descriptors it was handed and says its boot is done on the third; without it, it relays
// them to its own streams as any launch does. Red if the child stops reading the flag or stops
// handing what it read to the launch.
func TestTheChildBuildJailRelaysItsJailToTheDescriptorsItWasHanded(t *testing.T) {
	jo, je := pipeEnd(t), pipeEnd(t)
	readyR, jr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readyR.Close(); _ = jr.Close() })
	prev := forkBuildJailStreams
	forkBuildJailStreams = func() (*os.File, *os.File, *os.File, bool) { return jo, je, jr, true }
	t.Cleanup(func() { forkBuildJailStreams = prev })
	var seen run.Options
	withFakeCaptureJail(t, func(o run.Options) int {
		seen = o
		if o.OnJailReady != nil {
			o.OnJailReady()
		}
		return 0
	})
	argv := forkBuildChildArgv("/staging", forkBuild{Fork: forkFixture()}, false)
	if rc := runForkBuildJail(argv[2:], io.Discard, io.Discard); rc != 0 {
		t.Fatalf("the child refused its argv: rc %d", rc)
	}
	if seen.JailStdout != io.Writer(jo) || seen.JailStderr != io.Writer(je) {
		t.Errorf("the child's launch relays its jail to %v and %v, want the descriptors it was handed",
			seen.JailStdout, seen.JailStderr)
	}
	if b, _ := io.ReadAll(readyR); string(b) != "R" {
		t.Errorf("the child's launch said %q on the ready descriptor at its jail's ready, want one byte", b)
	}
	seen = run.Options{}
	argv = slices.DeleteFunc(argv, func(a string) bool { return a == forkBuildJailStreamsFlag })
	if rc := runForkBuildJail(argv[2:], io.Discard, io.Discard); rc != 0 {
		t.Fatalf("the child refused its argv without the flag: rc %d", rc)
	}
	if seen.JailStdout != nil || seen.JailStderr != nil || seen.OnJailReady != nil {
		t.Errorf("without the flag the child's launch relays its jail to %v and %v, want its own streams",
			seen.JailStdout, seen.JailStderr)
	}
}

// forkFixture is a plain fork, for an argv.
func forkFixture() packload.Fork {
	return packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool", Build: "make"}
}

// pipeEnd is the write end of a pipe the test closes.
func pipeEnd(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return w
}
