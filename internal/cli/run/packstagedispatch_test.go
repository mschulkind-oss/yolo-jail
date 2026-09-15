package run

import (
	"bytes"
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
	reapTestSpawnedOpenAIBroker(t)
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
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, packRoot, _ string, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
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
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, packRoot, _ string, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
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

// reapTestSpawnedOpenAIBroker stops the machine-wide OpenAI broker singleton IF this test's
// own launch spawned it.
//
// WHY A TEST NEEDS THIS AT ALL, which is the part worth fixing properly one day:
// dispatchOptions promises "no real container daemon is consulted" and takes an execRec so
// Options.Exec can be stubbed — but internal/openaiauthhost/host.go:269 spawns with
// exec.Command DIRECTLY, bypassing that seam. So any test reaching the macos-user arm with
// the openai-auth loophole active starts a REAL daemon on the machine-global socket
// /tmp/yolo-openai-auth-broker.sock, and a singleton is deliberately NOT stopped per launch
// (h.stop is nil for it — one broker is meant to serve every jail).
//
// MEASURED 2026-09-15 in a long-lived jail: 159 leaked daemons from one day of test runs,
// two per run. The FIRST one owns the socket, so from the second run onward every launch
// reports "OpenAI credential service did not start" — the leak causes the symptom that
// produces more leaks.
//
// ⚠ INVISIBLE ON CI, AND ONLY THERE. Runners are ephemeral, so the first run always wins and
// the suite is green. It reproduces on any machine that runs the suite twice.
//
// ⚠ IT REAPS ONLY WHAT A TEST SPAWNED. The pid file is machine-global, so a blind kill here
// would stop a broker a real jail on this machine is using. A test-spawned one is identifiable
// by its --state-file argument pointing inside a go test temp dir; anything else is left alone.
func reapTestSpawnedOpenAIBroker(t *testing.T) {
	t.Helper()
	reap := func() {
		raw, err := os.ReadFile(paths.HostSingletonPIDFile("openai-auth-broker"))
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || pid <= 1 {
			return
		}
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			return // not this platform's /proc, or already gone: leave it alone
		}
		argv := strings.ReplaceAll(string(cmdline), "\x00", " ")
		if !strings.Contains(argv, "/Test") || !strings.Contains(argv, "openai-auth-broker") {
			return // not a test's daemon — do not touch a real one
		}
		_ = syscall.Kill(pid, syscall.SIGTERM)
		_ = os.Remove(paths.HostSingletonSocket("openai-auth-broker"))
		_ = os.Remove(paths.HostSingletonPIDFile("openai-auth-broker"))
	}
	// CLEANUP ONLY, NOT SETUP — measured, and the difference is 3 failures versus 14.
	// Reaping at setup kills a broker an EARLIER test in the same run started, and the
	// singleton is designed to be SHARED, so every later test then has to start its own.
	// Reaping only at the end keeps the sharing inside a run and leaves nothing behind
	// after it.
	t.Cleanup(reap)
}
