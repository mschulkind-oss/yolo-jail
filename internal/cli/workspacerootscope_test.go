package cli

// workspacerootscope_test.go pins the half of the stray-.yolo fix that protects machines
// which ALREADY have one. Nothing is allowed to create a `.yolo` in the home any more, but
// the ones already there keep hijacking workspaceRoot()'s upward walk: every `yolo config`
// verb run in any directory below the home that is not itself a workspace would resolve the
// HOME as its workspace — `ls` and `diff` reporting a workspace the user is not in, and
// `reset` deleting that directory's sidecars.

import (
	"os"
	"path/filepath"
	"testing"
)

// scopeWalkHome mints a resolved HOME, points the process at it, and returns it.
func scopeWalkHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	t.Setenv("HOME", home)
	return home
}

func TestWorkspaceRootStopsAtABoundaryDirectory(t *testing.T) {
	home := scopeWalkHome(t)
	// The artifact a pre-guard launch left behind, at the depth it really sits at.
	if err := os.MkdirAll(filepath.Join(home, ".yolo", "prism"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Somewhere under the home that is NOT a workspace — the ordinary case for a user
	// poking around their own files.
	sub := filepath.Join(home, "notes", "scratch")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	got, _ := filepath.EvalSymlinks(workspaceRoot())
	if got == home {
		t.Fatalf("workspaceRoot() = %q, the HOME — a stray ~/.yolo hijacked the walk, so "+
			"`config ls` reports the wrong workspace and `reset` would delete its sidecars", got)
	}
	if got != sub {
		t.Errorf("workspaceRoot() = %q, want the cwd %q (the answer a machine with no stray "+
			"directory gives)", got, sub)
	}
}

// TestWorkspaceRootStillWalksUpUnderTheHome is the positive control: stopping at the home
// must not stop the walk one directory early. A project under the home is where every
// workspace on the machine lives, and its subdirectories must still resolve to it.
func TestWorkspaceRootStillWalksUpUnderTheHome(t *testing.T) {
	home := scopeWalkHome(t)
	ws := filepath.Join(home, "code", "project")
	if err := os.MkdirAll(filepath.Join(ws, ".yolo", "prism"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(ws, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	got, _ := filepath.EvalSymlinks(workspaceRoot())
	if got != ws {
		t.Errorf("workspaceRoot() from %q = %q, want the workspace %q", sub, got, ws)
	}
}
