package darwinpkg

// nixchildren_test.go pins this package's two nix call sites — the skip-list eval and the package
// build — to the one set internal/nixchildren keeps, so the teardown that stops a launch's nix
// stops these too: a macos-user launch's while it builds the sandbox's tools, and a container
// launch's store-delivered package build (MaterializeAt). Each test fails if its call site starts
// its nix without the set.

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// standInNix puts a `nix` on PATH whose eval runs evalScript and whose build runs buildScript.
func standInNix(t *testing.T, evalScript, buildScript string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \" $* \" in\n*\" eval \"*)\n" + evalScript + "\n;;\n*)\n" + buildScript + "\n;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// runsUntilInterrupted is a stand-in step that runs until interrupted (testsupport.UntilInterrupted)
// and marks start and interrupt in dir under name.
func runsUntilInterrupted(dir, name string) string {
	started, interrupted := filepath.Join(dir, name+"-started"), filepath.Join(dir, name+"-interrupted")
	return testsupport.UntilInterrupted("touch "+shquote.Quote(interrupted), started)
}

// awaitFile waits for path to exist.
func awaitFile(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
	}
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

// stopMidStep runs MaterializeAt with the stand-in, waits for step's nix to be running and
// tracked, stops the set, and returns MaterializeAt's error once it has returned.
func stopMidStep(t *testing.T, s *nixchildren.Set, dir, step string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := MaterializeAt(t.TempDir(), nil, "x86_64-linux", "", io.Discard)
		done <- err
	}()
	awaitFile(t, filepath.Join(dir, step+"-started"))
	awaitTracked(t, s, 1)
	nixchildren.Stop(nil)
	select {
	case err := <-done:
		if _, serr := os.Stat(filepath.Join(dir, step+"-interrupted")); serr != nil {
			t.Errorf("the %s's nix was not interrupted", step)
		}
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("MaterializeAt did not return after its %s's nix was stopped", step)
		return nil
	}
}

// TestTheSkipListEvalIsTracked: the skip-list eval is a nix a stop reaches, and the build after
// the eval the stop cut short does not start.
func TestTheSkipListEvalIsTracked(t *testing.T) {
	s := nixchildren.Isolate(t)
	dir := t.TempDir()
	built := filepath.Join(dir, "built")
	standInNix(t, runsUntilInterrupted(dir, "eval"), "touch "+shquote.Quote(built)+"; echo /nix/store/fake-profile")
	if err := stopMidStep(t, s, dir, "eval"); err == nil {
		t.Error("a materialize whose eval was stopped reported success")
	}
	if _, err := os.Stat(built); err == nil {
		t.Error("the package build started after the stop")
	}
}

// TestThePackageBuildIsTracked: the package build is a nix a stop reaches.
func TestThePackageBuildIsTracked(t *testing.T) {
	s := nixchildren.Isolate(t)
	dir := t.TempDir()
	standInNix(t, "echo '[]'", runsUntilInterrupted(dir, "build"))
	if err := stopMidStep(t, s, dir, "build"); err == nil {
		t.Error("a materialize whose build was stopped reported success")
	}
}
