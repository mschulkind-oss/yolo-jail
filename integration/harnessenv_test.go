package integration

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// TestEveryLaunchTheSuiteMakesHasTheAutomaticReapersOff: TestMain turns yolo's automatic reapers
// off for every launch this suite makes (applySuiteEnv), and a test that exercises one turns them
// back on for its own launch (withAutoReapers). The launches are a stand-in yolo that prints what
// it was handed, run through runCommand, the helper every run* helper funnels through.
//
// The first assertion is vacuous where the calling shell already sets the variable, as it holds
// there anyway; CI's shell does not.
func TestEveryLaunchTheSuiteMakesHasTheAutomaticReapersOff(t *testing.T) {
	if got := os.Getenv(autoReapersOffEnv); got != "1" {
		t.Errorf("this test process has %s=%q, want 1: TestMain no longer turns the automatic "+
			"reapers off, so a launch here reaps images and store outputs through whatever "+
			"runtime and nix daemon the machine has, while other tests use them", autoReapersOffEnv, got)
	}
	dir := resolvedTempDir(t)
	fake := filepath.Join(dir, "fake-yolo")
	script := "#!/bin/sh\necho \"REAPERS_OFF=${" + autoReapersOffEnv + "-unset}.\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := yoloBin
	yoloBin = fake
	t.Cleanup(func() { yoloBin = saved })

	if r := runCommand(t, dir, []string{"run", "--", "true"}); !strings.Contains(r.stdout, "REAPERS_OFF=1.") {
		t.Errorf("a launch did not have the automatic reapers off: %q", r.stdout)
	}
	if r := runCommand(t, dir, []string{"run", "--", "true"}, withAutoReapers()); !strings.Contains(r.stdout, "REAPERS_OFF=.") {
		t.Errorf("withAutoReapers did not turn the automatic reapers back on: %q", r.stdout)
	}
}
