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
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The repo-root gate: a missing yolo-jail repo root is FATAL (exit 1), not a
// degraded launch on a stale cached image. This is the revert of D2 — running
// the wrong environment silently was deemed worse than failing.
//
// BOTH BACKENDS ARE GATED NOW, for different reasons, and the two arms are
// pinned together because they are one decision. A container backend needs the
// flake to build its IMAGE. macos-user needs it to build the non-container FLOOR
// — the core set every jail gets — which since 2026-09-12 is materialized on
// every launch rather than only when `packages:` is non-empty
// (docs/design/macos-user-provisioning.md, OQ-P1). --dry-run is the one thing
// still exempt on that arm: it materializes nothing.

// runFatalOptions builds an Options whose seams reach the repo-root gate
// deterministically: RepoRoot fails, storage/config are trivially OK (empty
// workspace → empty merged config), and the runtime resolves via an explicit
// YOLO_RUNTIME so no real podman is consulted.
func runFatalOptions(t *testing.T, workspace, ytoRuntime string, stdout, stderr *bytes.Buffer) *Options {
	t.Helper()
	o := &Options{
		Workspace:   workspace,
		Network:     "bridge",
		IsLinux:     true,
		Stdout:      stdout,
		Stderr:      stderr,
		Getenv:      func(k string) string { return "" },
		LookPath:    func(string) (string, bool) { return "", false },
		Exec:        func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} },
		PathExists:  func(string) bool { return false },
		Now:         func() time.Time { return time.Unix(0, 0) },
		Getpid:      func() int { return 1 },
		IsTTYStdout: func() bool { return false },
		IsTTYStdin:  func() bool { return false },
		// The whole point of these tests: repo root cannot be resolved.
		RepoRoot: func() (reporoot.Resolution, bool) { return reporoot.Resolution{}, false },
	}
	fillDefaults(o)
	// fillDefaults re-installs real seams; re-apply the deterministic stubs it
	// clobbered (mirrors goldenOptions).
	o.Stdout = stdout
	o.Stderr = stderr
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{}, false }
	// Explicit runtime selection so resolveRuntime never touches a real daemon.
	// YOLO_RUNTIME wins over config and (for container backends) still requires
	// LookPath+connectable, so wire those for the podman case; macos-user is a
	// native runtime that passes validateExplicitRuntime through untouched.
	if ytoRuntime == "podman" {
		o.LookPath = func(name string) (string, bool) {
			if name == "podman" {
				return "/usr/bin/podman", true
			}
			return "", false
		}
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			// `podman info` → connectable.
			if len(argv) >= 2 && argv[0] == "podman" && argv[1] == "info" {
				return ExecResult{Ran: true, RC: 0, Stdout: "host: {}"}
			}
			return ExecResult{Ran: false}
		}
	}
	o.Getenv = func(k string) string {
		if k == "YOLO_RUNTIME" {
			return ytoRuntime
		}
		return ""
	}
	return o
}

// TestRunFatalOnMissingRepoRootContainer: a podman launch with an unresolvable
// repo root exits 1 with an actionable message, and does NOT proceed into the
// image build / container launch on a stale image.
func TestRunFatalOnMissingRepoRootContainer(t *testing.T) {
	ws := t.TempDir() // no yolo-jail.jsonc → empty valid config
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	o := runFatalOptions(t, ws, "podman", &stdout, &stderr)

	rc := Run(*o)

	if rc != 1 {
		t.Fatalf("Run() = %d, want 1 (missing repo root is fatal on a container backend)\nstdout:\n%s\nstderr:\n%s",
			rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Cannot find yolo-jail repo root") {
		t.Errorf("missing the actionable repo-root failure on stderr:\n%s", stderr.String())
	}
	// The old D2 soft path printed a yellow "launching on the cached image
	// (no rebuild)" notice and continued. That specific notice must be gone —
	// match its distinctive phrase, not a substring the new fatal message
	// happens to share ("cached image").
	if strings.Contains(stderr.String(), "launching on the cached image") {
		t.Errorf("run still emitted the D2 degrade notice instead of failing:\nstderr:\n%s", stderr.String())
	}
}

// TestRunMacosUserGatedOnMissingRepoRootWithNoPackages: the exemption this test
// used to assert is GONE, and its inversion is the point.
//
// It read "the macos-user backend needs no repo when `packages:` is empty, so a
// missing repo root must NOT gate it" — correct while that backend's only nix
// work was the user's own declarations. The floor made an empty `packages:` the
// case that depends on nix MOST: every launch now builds git, node, mise and
// ripgrep from this flake, so a launch with no repo would produce a sandbox with
// none of them.
//
// Kept rather than deleted, because "was this launch gated?" is the question, and
// the answer flipping is exactly what a reader needs to find here.
func TestRunMacosUserGatedOnMissingRepoRootWithNoPackages(t *testing.T) {
	ws := t.TempDir() // no yolo-jail.jsonc → empty valid config, no `packages:`
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	o := runFatalOptions(t, ws, "macos-user", &stdout, &stderr)

	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		reached = true
		return 0
	}

	rc := Run(*o)

	if reached {
		t.Error("Run() reached MacosUserRun with no repo root — every macos-user launch " +
			"materializes the floor from this flake, so nix would have resolved one from " +
			"the caller's cwd")
	}
	if rc != 1 {
		t.Errorf("Run() = %d, want 1\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Cannot find yolo-jail repo root") {
		t.Errorf("missing the actionable repo-root failure on stderr:\n%s", stderr.String())
	}
	// The message must explain that the repo is needed even with nothing declared,
	// or a user who declares no packages reads it as the gate misfiring.
	if !strings.Contains(stderr.String(), "even when you declare nothing") {
		t.Errorf("the refusal does not say the repo is needed with an empty `packages:`:\n%s",
			stderr.String())
	}
}

// TestRunMacosUserGatedOnMissingRepoRootWithPackages: the declared-packages arm
// of the same gate. It is no longer the CONDITION — the cell above covers the
// empty case — but it is still the path that measured the original defect, and it
// must stay fatal HERE, where the message can name the fix, rather than three
// layers down where nix reports the user's own workspace as "not part of a
// flake" (the measured 2026-09-03 symptom).
//
// This pins the CALL SITE, not the callee: darwinpkg.Materialize has its own
// refusal for an empty root, and a test that only exercised that would still
// pass with this gate deleted. Deleting the `else if` in run.Run makes this test
// fail, which is the property AGENTS.md asks for.
func TestRunMacosUserGatedOnMissingRepoRootWithPackages(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"),
		[]byte(`{"packages": ["fzf"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	o := runFatalOptions(t, ws, "macos-user", &stdout, &stderr)

	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		reached = true
		return 0
	}

	rc := Run(*o)

	if reached {
		t.Errorf("Run() reached MacosUserRun with `packages:` and no repo root — nix would " +
			"have resolved a flake from the caller's cwd")
	}
	if rc != 1 {
		t.Errorf("Run() = %d, want 1\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Cannot find yolo-jail repo root") {
		t.Errorf("missing the actionable repo-root failure on stderr:\n%s", stderr.String())
	}
	// The message must name `packages:` among what it cannot build, or a user who
	// declared some reads the refusal as being about something else entirely.
	if !strings.Contains(stderr.String(), "packages:") {
		t.Errorf("the refusal never names `packages:`:\n%s", stderr.String())
	}
}

// TestRunMacosUserDryRunNotGatedWithPackages: --dry-run materializes nothing
// (RunMacosUser returns before the nix build), so the gate above must not catch
// it — refusing a plan render would hide the plan the user asked to inspect.
func TestRunMacosUserDryRunNotGatedWithPackages(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"),
		[]byte(`{"packages": ["fzf"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	o := runFatalOptions(t, ws, "macos-user", &stdout, &stderr)
	o.DryRun = true

	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string, dryRun bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		reached = true
		if !dryRun {
			t.Errorf("MacosUserRun got dryRun=false, want true")
		}
		return 0
	}

	if rc := Run(*o); rc != 0 {
		t.Errorf("Run() = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !reached {
		t.Errorf("--dry-run was gated on the missing repo root; it materializes nothing\nstderr:\n%s",
			stderr.String())
	}
}
