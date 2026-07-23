package integration

import (
	"os"
	"slices"
	"testing"
)

// TestChildRepoRootEnv locks the repo-root propagation that keeps the spawned
// CLI able to resolve the yolo-jail checkout.
//
// This runs under -short (no container) because it is a harness invariant, not
// a jail behavior: TestMain builds yolo into a temp dir and every test runs it
// with cmd.Dir set to a temp workspace, so the CLI's walk-up-from-cwd resolver
// finds nothing. Without the propagation, every test that spawns yolo fails
// with "Cannot find yolo-jail repo root" — an entire integration job's worth.
func TestChildRepoRootEnv(t *testing.T) {
	origRoot := repoRoot
	t.Cleanup(func() { repoRoot = origRoot })

	// -short leaves repoRoot empty (TestMain never resolves it): add nothing
	// rather than an empty YOLO_REPO_ROOT the CLI would have to ignore.
	repoRoot = ""
	t.Setenv("YOLO_REPO_ROOT", "")
	if got := childRepoRootEnv(); got != nil {
		t.Errorf("unresolved repo root should add no env, got %v", got)
	}

	// The normal case: hand the child the root TestMain derived via
	// runtime.Caller.
	repoRoot = "/some/checkout"
	if got := childRepoRootEnv(); !slices.Equal(got, []string{"YOLO_REPO_ROOT=/some/checkout"}) {
		t.Errorf("childRepoRootEnv() = %v, want [YOLO_REPO_ROOT=/some/checkout]", got)
	}

	// A real YOLO_REPO_ROOT wins — it is the CLI's own first-choice source and
	// may legitimately differ from this checkout (e.g. a CI override).
	t.Setenv("YOLO_REPO_ROOT", "/some/other/checkout")
	if got := childRepoRootEnv(); got != nil {
		t.Errorf("preexisting YOLO_REPO_ROOT must not be clobbered, got %v", got)
	}

	// os.Environ() carries the real value through to the child unchanged.
	if os.Getenv("YOLO_REPO_ROOT") != "/some/other/checkout" {
		t.Error("t.Setenv did not take effect")
	}
}
