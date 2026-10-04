package prune

// nixstop_test.go pins what a signal sent to `yolo prune` alone does to the nix prune has running
// — the bounded `nix store gc`, which an applied prune lets run for up to half an hour, and each
// `nix store delete`: it is stopped, and prune ends 128+N. Before, prune had no signal teardown at
// all, so the signal's default action ended it at once and its nix ran on with no parent. A
// Ctrl-C at a terminal never showed it, because the terminal signals nix too.

import (
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
)

// standInNix puts a `nix` on PATH that runs until interrupted, marking its start and the interrupt
// in the directory it returns. It gives up after 30 seconds, so a red run whose nix nothing stops
// does not leave it looping after the test binary exits.
func standInNix(t *testing.T) (marks string) {
	t.Helper()
	bin, marks := t.TempDir(), t.TempDir()
	script := "#!/bin/sh\ntrap 'touch " + filepath.Join(marks, "interrupted") + "; exit 130' INT\n" +
		"touch " + filepath.Join(marks, "started") + "\n" +
		"i=0; while [ $i -lt 600 ]; do sleep 0.05; i=$((i+1)); done; exit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marks
}

// awaitTracked waits for s to hold n running children.
func awaitTracked(t *testing.T, s *nixchildren.Set, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		got := s.Running()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d nix children tracked, want %d", got, n)
		}
	}
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

// interrupted reports whether the stand-in nix of marks was interrupted.
func interrupted(marks string) bool {
	_, err := os.Stat(filepath.Join(marks, "interrupted"))
	return err == nil
}

// catchSignal keeps sig from ending the test binary when no arm is installed to catch it, which is
// the defect's own shape: without this a red run would kill the whole package's run.
func catchSignal(t *testing.T, sig os.Signal) {
	t.Helper()
	catch := make(chan os.Signal, 4)
	signal.Notify(catch, sig)
	t.Cleanup(func() { signal.Stop(catch) })
}

// TestPrunesNixIsTracked: each nix prune runs through its own exec — the store GC and a store
// delete, as the defaults wire them — is one a stop reaches.
func TestPrunesNixIsTracked(t *testing.T) {
	var o Options
	fillDefaults(&o)
	for _, tc := range []struct {
		name string
		run  func() bool // reports whether the nix's work was done
	}{
		{"the store GC", func() bool { return o.NixStoreGC(1<<30, true).Ran }},
		{"a store delete", func() bool {
			return len(DeleteSupersededStoreOutputs([]string{"/nix/store/aaaa-yolo-jail-install-prefix"}, true, o.Exec)) > 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := nixchildren.Isolate(t)
			marks := standInNix(t)
			done := make(chan bool, 1)
			go func() { done <- tc.run() }()
			awaitTracked(t, s, 1)
			awaitStarted(t, marks)
			nixchildren.Stop(nil)
			select {
			case did := <-done:
				if did {
					t.Error("a stopped nix reported its work done")
				}
				if !interrupted(marks) {
					t.Error("the stop did not interrupt the nix")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the nix's caller did not return after its nix was stopped")
			}
		})
	}
}

// TestASignalToPruneStopsItsNix: a SIGTERM sent to prune's process alone, while its store GC (a
// stand-in that runs until interrupted) is running, interrupts that nix and ends prune 143.
func TestASignalToPruneStopsItsNix(t *testing.T) {
	s := nixchildren.Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	marks := standInNix(t)
	o, _ := baseOpts(t)
	o.NixGC = true
	o.InJail = func() bool { return false }
	o.Exec = stubExec(map[string]string{
		k("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}"): "\n",
	}, nil)
	o.NixStoreGC = nil // the real one, so the nix is the one `yolo prune --nix-gc` runs
	o.Out = io.Discard
	returned := make(chan int, 1)
	go func() { returned <- Run(o) }()
	awaitTracked(t, s, 1)
	awaitStarted(t, marks)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-s.Exited():
		if code != 128+int(syscall.SIGTERM) {
			t.Errorf("the signaled prune ended %d, want %d", code, 128+int(syscall.SIGTERM))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a SIGTERM sent to prune alone did not end it")
	}
	if !interrupted(marks) {
		t.Error("prune ended without interrupting the nix it had running")
	}
	select {
	case <-returned:
	case <-time.After(30 * time.Second):
		t.Fatal("Run never returned after its faked exit")
	}
}
