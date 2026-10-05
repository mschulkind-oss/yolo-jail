package macosuser

// nixstop_test.go pins what a signal sent to yolo alone does while RunMacosUser runs nix on the
// host — the sandbox's tool build (MaterializeDarwin) or its guest-binaries build (GuestBinaries)
// — for a caller that wires NO signal arm (Deps.Ending nil): the signal's default action used to
// end yolo there and leave that nix building with no parent, and RunMacosUser's own stop
// (nixchildren.StopOnSignal) now stops it and ends the process 128+N, covering the two builds and
// nothing after them. A launch wires its arm instead (internal/cli/run's macosuserarm.go), which
// stops the nix and ends the launch through its teardown; terminate_test.go pins that half.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// catchSignal keeps sig from ending the test binary when no arm is installed to catch it, which is
// the defect's own shape: without this a red run would kill the whole package's run.
func catchSignal(t *testing.T, sig os.Signal) {
	t.Helper()
	catch := make(chan os.Signal, 4)
	signal.Notify(catch, sig)
	t.Cleanup(func() { signal.Stop(catch) })
}

// trackedStandInNix runs, through the tracked set, a stand-in nix that runs until interrupted
// (testsupport.UntilInterrupted) and marks its start and the interrupt in marks.
func trackedStandInNix(marks string) error {
	return nixchildren.Run(exec.Command("sh", "-c", testsupport.UntilInterrupted(
		"touch "+shquote.Quote(filepath.Join(marks, "interrupted")), filepath.Join(marks, "started"))))
}

// awaitStarted waits for the stand-in nix of marks to mark its start, which it does once its
// interrupt trap is set. Being tracked is not enough: the set tracks the stand-in as soon as it is
// started, and an interrupt that reaches the shell before its trap line ends it by the signal's
// default action, which marks nothing.
func awaitStarted(t *testing.T, marks string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(marks, "started")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-in nix never marked its start")
		}
	}
}

// TestASignalWhileTheSandboxNixRunsStopsIt: a SIGTERM sent to the launch alone during either of
// its host nix builds interrupts that nix and ends the launch 143.
func TestASignalWhileTheSandboxNixRunsStopsIt(t *testing.T) {
	for _, step := range []string{"the tool build", "the guest-binaries build"} {
		t.Run(step, func(t *testing.T) {
			s := nixchildren.Isolate(t)
			catchSignal(t, syscall.SIGTERM)
			marks := t.TempDir()
			var rec []string
			d := mockDeps(&rec)
			var buf bytes.Buffer
			d.Out = &buf
			o := newOpts("/Users/Shared/yolo/proj")
			o.JailDaemons = openAIAdapterDaemons("")
			if step == "the tool build" {
				d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
					return nil, false, trackedStandInNix(marks)
				}
				d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
			} else {
				d.GuestBinaries = func(string) (string, error) {
					if err := trackedStandInNix(marks); err != nil {
						return "", err
					}
					return "", errors.New("the stand-in guest build was not interrupted")
				}
			}
			returned := make(chan int, 1)
			go func() { returned <- RunMacosUser(d, o) }()
			for deadline := time.Now().Add(10 * time.Second); s.Running() != 1; time.Sleep(5 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatalf("%s's nix never ran\n%s", step, buf.String())
				}
			}
			awaitStarted(t, marks)
			if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case code := <-s.Exited():
				if code != 128+int(syscall.SIGTERM) {
					t.Errorf("the signaled launch ended %d, want %d", code, 128+int(syscall.SIGTERM))
				}
			case <-time.After(15 * time.Second):
				t.Fatalf("a SIGTERM sent to the launch alone during %s did not end it", step)
			}
			if _, err := os.Stat(filepath.Join(marks, "interrupted")); err != nil {
				t.Errorf("the launch ended without interrupting %s's nix", step)
			}
			select {
			case <-returned:
			case <-time.After(15 * time.Second):
				t.Fatal("RunMacosUser never returned after its faked exit")
			}
			for _, r := range rec {
				if strings.HasPrefix(r, "proxy:") {
					t.Error("the agent ran after the signal ended the launch")
				}
			}
		})
	}
}

// TestTheSandboxNixArmIsGoneBeforeTheSession: the stop covers the host nix builds and nothing after
// them. From the privileged steps on, a signal is the session runner's to handle, and two handlers
// acting on one signal would race each other to the exit.
func TestTheSandboxNixArmIsGoneBeforeTheSession(t *testing.T) {
	s := nixchildren.Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	d.RunWithProxy = func([]string) int {
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Error(err)
		}
		select {
		case code := <-s.Exited():
			t.Errorf("the nix arm was still armed in the session and ended it %d", code)
		case <-time.After(300 * time.Millisecond):
		}
		return 42
	}
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d, want 42 (the mock proxy's exit code)\n%s", rc, buf.String())
	}
}
