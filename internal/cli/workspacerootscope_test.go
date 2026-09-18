package cli

// workspacerootscope_test.go pins the half of the stray-.yolo fix that protects machines
// which ALREADY have one. Nothing is allowed to create a `.yolo` in the home any more, but
// the ones already there keep hijacking the target resolution's upward walk: every
// `yolo config` verb run in any directory below the home that is not itself a workspace would
// resolve the HOME as its workspace — `ls` and `diff` reporting a workspace the user is not
// in, and `reset` deleting that directory's sidecars.
//
// ⚠ THE MARKER ALONE DOES NOT CLOSE THIS, which is why both halves exist. The ruled marker
// (docs/reference/config-target-resolution.md [OQ-CR2]) is an ARTIFACT rather than a directory,
// so it already rejects the bare generated-script anchor — but a machine that launched in its
// home BEFORE paths.WorkspaceScopeBreach refused to create the state dir carries a real
// `~/.yolo/config-boot.json`, and that IS a marker. The breach stop is the only thing that
// rejects it, so the fixture below seeds exactly that file: with a bare `.yolo` the test would
// pass because the marker declined, and would stop measuring the stop it exists for.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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
	// A PRE-GUARD LAUNCH ARTIFACT, at the depth it really sits at — and a real marker, so
	// the breach stop is what this measures rather than the marker test.
	writeFile(t, config.WorkspaceConfigBootPath(home), `{}`)
	// Somewhere under the home that is NOT a workspace — the ordinary case for a user
	// poking around their own files.
	sub := filepath.Join(home, "notes", "scratch")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	got, ok := resolveWorkspaceRoot()
	if ok {
		if resolved, _ := filepath.EvalSymlinks(got); resolved == home {
			t.Fatalf("resolveWorkspaceRoot() = %q, the HOME — a stray ~/.yolo/config-boot.json "+
				"hijacked the walk, so `config ls` reports the wrong workspace and `reset` "+
				"would delete its sidecars", got)
		}
		t.Fatalf("resolveWorkspaceRoot() = %q, want no workspace: nothing below the home is "+
			"marked, and the walk may not pass the home", got)
	}
	// AND THE ANSWER IS THE HOST TARGET, not an empty workspace answer
	// (docs/reference/config-target-resolution.md [OQ-CR2]). This is the half that replaced
	// "the cwd stands": a directory resolving no workspace used to become a workspace whose
	// store does not exist, and every verb then reported its silence as an answer.
	tgt, refusal := resolveConfigTarget("")
	if refusal != "" {
		t.Fatalf("a directory with no workspace must resolve the host target, not refuse: %s", refusal)
	}
	if tgt.notch.String() != "host" {
		t.Errorf("resolveConfigTarget notch = %s, want host", tgt.notch)
	}
}

// TestWorkspaceRootStillWalksUpUnderTheHome is the positive control: stopping at the home
// must not stop the walk one directory early. A project under the home is where every
// workspace on the machine lives, and its subdirectories must still resolve to it.
func TestWorkspaceRootStillWalksUpUnderTheHome(t *testing.T) {
	home := scopeWalkHome(t)
	ws := filepath.Join(home, "code", "project")
	// The ruled marker: a committed workspace config, which is what a freshly cloned repo
	// carries before its first launch.
	writeFile(t, filepath.Join(ws, config.WorkspaceConfigName), `{}`)
	sub := filepath.Join(ws, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	got, ok := resolveWorkspaceRoot()
	if !ok {
		t.Fatalf("resolveWorkspaceRoot() from %q found no workspace, want %q", sub, ws)
	}
	if got, _ = filepath.EvalSymlinks(got); got != ws {
		t.Errorf("resolveWorkspaceRoot() from %q = %q, want the workspace %q", sub, got, ws)
	}
}
