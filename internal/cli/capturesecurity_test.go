//go:build linux

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

func TestProgramCaptureDriverReapsSuccessfulEscapedDescendants(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, escape := range []string{"group", "double-fork-new-session"} {
		t.Run(escape, func(t *testing.T) {
			home, out := t.TempDir(), filepath.Join(t.TempDir(), "out")
			t.Setenv(entrypoint.SealedBuildEnv, "1")
			script := `mkdir -p "$HOME/.local/bin"; printf program > "$HOME/.local/bin/probe"; sh -c 'echo $$ > "$HOME/survivor"; sleep 30 & echo $! > "$HOME/grandchild"; wait' & while [ ! -f "$HOME/grandchild" ]; do sleep .01; done`
			if escape != "group" {
				script = `mkdir -p "$HOME/.local/bin"; printf program > "$HOME/.local/bin/probe"; "$1" internal capture-test-escape`
			}
			if rc := runInternal([]string{"capture-run", "--home=" + home, "--out=" + out, "--artifact=.local/bin/probe", "--", "/bin/sh", "-c", script, "fixture", exe}); rc != 0 {
				t.Fatalf("exit %d", rc)
			}
			assertCaptureDescendantsGone(t, home)
		})
	}

}

func TestProgramCaptureDriverParentSwapCannotCaptureExcludedRegularFile(t *testing.T) {
	home, err := os.MkdirTemp("/dev/shm", "capture-parent-swap-")
	if err != nil {
		t.Skip("cross-mount fixture unavailable:", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	out := filepath.Join(t.TempDir(), "out")
	t.Setenv(entrypoint.SealedBuildEnv, "1")
	script := `mkdir -p "$HOME/.local/lib" "$HOME/.config"; printf secret > "$HOME/.config/z"; printf program > "$HOME/.local/lib/a"; printf program > "$HOME/.local/lib/z"; dd if=/dev/zero of="$HOME/.local/lib/aa" bs=1048576 count=32 2>/dev/null; dd if=/dev/zero of="$HOME/.config/aa" bs=1048576 count=32 2>/dev/null; (while [ -f "$HOME/.local/lib/a" ]; do :; done; mv "$HOME/.local/lib" "$HOME/old"; ln -s "$HOME/.config" "$HOME/.local/lib") >/dev/null 2>&1 &`
	started := time.Now()
	rc := runInternal([]string{"capture-run", "--home=" + home, "--out=" + out, "--artifact=.local/lib/a", "--artifact=.local/lib/aa", "--artifact=.local/lib/z", "--", "/bin/sh", "-c", script})
	if time.Since(started) > 5*time.Second {
		t.Fatal("driver cleanup unbounded")
	}
	if rc == 0 {
		data, err := os.ReadFile(filepath.Join(capture.TreeDir(out), ".local/lib/z"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) == "secret" {
			t.Fatal("excluded config captured as regular artifact")
		}
	}
}

// Fixture-only self-exec: immediately orphan a new-session process, which in turn
// spawns a child. No setsid/Python installation or ancestry timing is required.
func init() {
	if len(os.Args) > 2 && os.Args[1] == "internal" && os.Args[2] == "capture-test-escape" {
		exe, err := os.Executable()
		if err != nil {
			os.Exit(1)
		}
		if len(os.Args) == 3 {
			cmd := exec.Command(exe, "internal", "capture-test-escape", "survive")
			cmd.Env = os.Environ()
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				os.Exit(1)
			}
			for i := 0; i < 1000; i++ {
				if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "grandchild")); err == nil {
					os.Exit(0)
				}
				time.Sleep(time.Millisecond)
			}
			os.Exit(1)
		}
		home := os.Getenv("HOME")
		_ = os.WriteFile(filepath.Join(home, "survivor"), []byte(strconv.Itoa(os.Getpid())), 0644)
		child := exec.Command("/bin/sleep", "30")
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		_ = os.WriteFile(filepath.Join(home, "grandchild"), []byte(strconv.Itoa(child.Process.Pid)), 0644)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}

func TestProgramCaptureDriverOwnershipFailureDoesNotPublish(t *testing.T) {
	t.Setenv(entrypoint.SealedBuildEnv, "1")
	out := filepath.Join(t.TempDir(), "out")
	rc := runInternal([]string{"capture-run", "--home=" + t.TempDir(), "--out=" + out, "--artifact=.local/bin/probe", "--", "/bin/sh", "-c", `mkdir -p "$HOME/.local/bin"; echo program > "$HOME/.local/bin/probe"; kill -KILL $PPID`})
	if rc == 0 {
		t.Fatal("missing supervisor evidence accepted")
	}
	if _, err := os.Stat(capture.ManifestPath(out)); !os.IsNotExist(err) {
		t.Fatal("ownership failure published manifest")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(exe, "internal", capture.InstallerSupervisorVerb, "/bin/true").Run(); err == nil {
		t.Fatal("helper accepted absent ownership pipes")
	}
}

func TestProgramCaptureDriverCancellationReapsEscapedDescendants(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home, out := t.TempDir(), filepath.Join(t.TempDir(), "out")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := capture.Run(capture.Options{Context: ctx, Home: home, Out: out, Artifacts: []string{".local/bin/probe"}, Command: []string{"/bin/sh", "-c", `"$1" internal capture-test-escape; sleep 30`, "fixture", exe}})
		done <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "grandchild")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("escaped child never started")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation unbounded")
	}
	assertCaptureDescendantsGone(t, home)
	if _, err := os.Stat(capture.ManifestPath(out)); !os.IsNotExist(err) {
		t.Fatal("cancelled capture published manifest")
	}
}

func assertCaptureDescendantsGone(t *testing.T, home string) {
	t.Helper()
	for _, name := range []string{"survivor", "grandchild"} {
		data, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(pid, 0); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Errorf("owned %s %d survived", name, pid)
		}
	}
}

// The cancelled capture whose supervisor never answers is
// internal/capture's TestProgramCaptureDriverCancellationWithStoppedSupervisorFailsClosed, where its
// wait for the stopped supervisor can be shrunk.
