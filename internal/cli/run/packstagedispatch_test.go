package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE ORDERING INVARIANT: pack staging happens BEFORE the backend dispatch.
//
// This is B-0's regression gate. Staging used to live inside runContainer, several
// calls below the `rt == "macos-user"` branch that returns before ever reaching it —
// so the native backend was dispatched with no YOLO_PACK_ROOT and rendered zero pack
// surfaces on every launch. Nothing failed: RunDarwinBootstrap's LoadJailPacks /
// ConfigurePackSurfaces / RunPackHooks loops simply iterated an empty list, and the
// backend reported a successful bootstrap.
//
// The defect was therefore invisible to every existing test, because each one asked
// either "does staging work?" (yes, on the container path) or "does the darwin
// bootstrap render packs?" (yes, when handed some). The question nobody asked was
// whether the two ever met. These tests ask it from the run pipeline's own entry
// point, which is the only place the answer lives.

// dispatchOptions builds an Options whose seams reach the backend dispatch
// deterministically: a resolvable repo root, trivially-OK storage/config, and an
// explicit YOLO_RUNTIME so no real container daemon is consulted.
func dispatchOptions(t *testing.T, workspace, ytoRuntime string, stdout, stderr *bytes.Buffer, execRec *[][]string) *Options {
	t.Helper()
	repoRoot := t.TempDir()
	o := &Options{
		Workspace: workspace,
		Network:   "bridge",
		IsLinux:   true,
		Stdout:    stdout,
		Stderr:    stderr,
	}
	fillDefaults(o)
	// fillDefaults installs the real seams; re-apply the deterministic stubs
	// (mirrors runFatalOptions).
	o.Stdout = stdout
	o.Stderr = stderr
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	o.Now = func() time.Time { return time.Unix(0, 0) }
	o.Getpid = func() int { return 1 }
	o.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: repoRoot, Source: reporoot.FromEnv}, true
	}
	o.LookPath = func(name string) (string, bool) {
		if name == ytoRuntime {
			return "/usr/bin/" + name, true
		}
		return "", false
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if execRec != nil {
			*execRec = append(*execRec, argv)
		}
		// `<rt> info` → connectable, so resolveRuntime accepts the explicit choice.
		if len(argv) >= 2 && argv[1] == "info" {
			return ExecResult{Ran: true, RC: 0, Stdout: "host: {}"}
		}
		return ExecResult{Ran: false}
	}
	o.Getenv = func(k string) string {
		if k == "YOLO_RUNTIME" {
			return ytoRuntime
		}
		return ""
	}
	return o
}

// TestPacksAreStagedBeforeBackendDispatch is the invariant itself, asserted at the
// macos-user arm because that is the arm the ordering bug lived in: the handler must
// receive a pack root that ALREADY EXISTS ON DISK with this launch's packs in it.
//
// It checks the staged TREE, not merely a non-empty string. A path argument is easy to
// thread and easy to thread wrongly (a root computed but never staged is exactly as
// empty to the bootstrap as no root at all), so the assertion is the one the backend
// actually depends on: claude's manifest is readable under the directory the handler
// was handed, at the moment it was handed it.
func TestPacksAreStagedBeforeBackendDispatch(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)

	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, packRoot, _ string, _ bool, _ *jsonx.OrderedMap) int {
		reached = true
		if packRoot == "" {
			t.Error("macos-user was dispatched with an empty pack root — " +
				"LoadJailPacks would find nothing and every pack loop would run over an empty list")
			return 0
		}
		manifest := filepath.Join(packRoot, "_official", "claude", "pack.json")
		if _, err := os.Stat(manifest); err != nil {
			staged, _ := os.ReadDir(packRoot)
			var names []string
			for _, e := range staged {
				names = append(names, e.Name())
			}
			t.Errorf("pack root %s does not hold the staged claude pack (%v); "+
				"the backend was dispatched before staging ran", packRoot, names)
		}
		return 0
	}

	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !reached {
		t.Fatalf("Run() never reached the macos-user handler\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
}

// TestPackRootIsEmptyWhenNoPacksAreConfigured: the other half of the contract. A launch
// with no packs must hand the backend an empty root rather than a directory that exists
// and holds nothing, so "this jail renders no pack surfaces" is stated by ABSENCE at the
// one place a reader looks — the plan's YOLO_PACK_ROOT — instead of being inferred from
// an empty tree. (The macos-user plan builder keys its whole pack block on this being
// empty; see TestRunPlanWithoutPacksNamesNoPackRoot.)
func TestPackRootIsEmptyWhenNoPacksAreConfigured(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)

	var got string
	gotSet := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, packRoot, _ string, _ bool, _ *jsonx.OrderedMap) int {
		got, gotSet = packRoot, true
		return 0
	}
	Run(*o)
	if !gotSet {
		t.Fatal("Run() never reached the macos-user handler")
	}
	// stagePacks still creates its root (the container path binds it unconditionally),
	// so what is asserted here is that the ROOT IS EMPTY of packs — the backend gets a
	// tree with nothing in it, and the plan builder is what turns that into "none".
	entries, err := os.ReadDir(got)
	if err != nil {
		t.Fatalf("staged root %s: %v", got, err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("no packs configured but the staged root holds %v", names)
	}
}

// TestStagingFailureStopsBeforeAnyContainerWork is the container arm of the same
// ordering claim, phrased as the consequence that is observable without a daemon: a
// pack that cannot be staged ends the launch BEFORE the run pipeline touches a
// container at all.
//
// Fail-closed is stagePacks' long-standing contract (A12); what this pins is that the
// contract now applies at the pipeline level rather than partway down one backend, so
// no backend can be dispatched with a half-resolved pack set.
func TestStagingFailureStopsBeforeAnyContainerWork(t *testing.T) {
	home := packHome(t)
	// A fetched pack that was never installed: launch is strictly offline, so this
	// resolves against the empty local store and fails without a network call.
	writeUserPacks(t, home,
		`[{"name": "ghost", "source": "git+https://example.invalid/ghost.git"}]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	var execRec [][]string
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, &execRec)

	rc := Run(*o)

	if rc != 1 {
		t.Fatalf("Run() = %d, want 1 (an unstageable pack is fail-closed)\nstdout:\n%s", rc, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ghost") {
		t.Errorf("the failure does not name the pack that could not be staged:\n%s", stdout.String())
	}
	for _, argv := range execRec {
		if len(argv) < 2 {
			continue
		}
		switch argv[1] {
		case "ps", "inspect", "rm", "exec", "run":
			t.Errorf("run reached container work (%v) despite the pack staging failure; "+
				"staging must gate the dispatch, not follow it", argv)
		}
	}
}
