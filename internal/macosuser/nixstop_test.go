package macosuser

// nixstop_test.go pins what a signal sent to a macos-user launch alone does while the launch runs
// nix on the host: the sandbox's tool build (MaterializeDarwin) or its guest-binaries build
// (GuestBinaries). This backend has no signal arm of its own before the TTY proxy's, so the
// signal's default action ended yolo there and left that nix building with no parent. Now it is
// stopped and the launch ends 128+N; from the privileged steps on, the signal is left to the
// arms that were always there.

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
)

// catchSignal keeps sig from ending the test binary when no arm is installed to catch it, which is
// the defect's own shape: without this a red run would kill the whole package's run.
func catchSignal(t *testing.T, sig os.Signal) {
	t.Helper()
	catch := make(chan os.Signal, 4)
	signal.Notify(catch, sig)
	t.Cleanup(func() { signal.Stop(catch) })
}

// trackedStandInNix runs, through the tracked set, a stand-in nix that runs until interrupted and
// marks the interrupt in marks. It gives up after 30 seconds, so a red run whose nix nothing
// stops does not leave it looping after the test binary exits.
func trackedStandInNix(marks string) error {
	return nixchildren.Run(exec.Command("sh", "-c",
		"trap 'touch "+filepath.Join(marks, "interrupted")+"; exit 130' INT; "+
			"i=0; while [ $i -lt 600 ]; do sleep 0.05; i=$((i+1)); done; exit 1"))
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

// TestTheSandboxNixArmIsGoneBeforeTheSession: the arm covers the host nix builds and nothing after
// them. From the privileged steps on, a signal is the TTY proxy's to handle, and two arms acting on
// one signal would race each other to the exit.
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
