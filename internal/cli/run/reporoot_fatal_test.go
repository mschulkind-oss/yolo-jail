package run

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The repo-root gate: for container backends a missing yolo-jail repo root is
// FATAL (exit 1), not a degraded launch on a stale cached image. This is the
// revert of D2 — running the wrong environment silently was deemed worse than
// failing. macos-user with no `packages:` needs no repo, so it must NOT be
// gated. Both are pinned here because they are one decision with two arms.

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
		RepoRoot: func() (string, bool) { return "", false },
	}
	fillDefaults(o)
	// fillDefaults re-installs real seams; re-apply the deterministic stubs it
	// clobbered (mirrors goldenOptions).
	o.Stdout = stdout
	o.Stderr = stderr
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	o.RepoRoot = func() (string, bool) { return "", false }
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

// TestRunMacosUserNotGatedOnMissingRepoRoot: the macos-user backend needs no
// repo when `packages:` is empty, so a missing repo root must NOT gate it — Run
// reaches the MacosUserRun handler rather than exiting at the repo-root check.
func TestRunMacosUserNotGatedOnMissingRepoRoot(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	o := runFatalOptions(t, ws, "macos-user", &stdout, &stderr)

	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, repoRoot string, _ bool) int {
		reached = true
		// The empty-packages backend gets the empty repoRoot and does not need it.
		if repoRoot != "" {
			t.Errorf("MacosUserRun got repoRoot %q, want empty (unresolved)", repoRoot)
		}
		return 0
	}

	rc := Run(*o)

	if !reached {
		t.Fatalf("Run() exited before MacosUserRun — the repo-root gate wrongly caught macos-user\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
	if rc != 0 {
		t.Errorf("Run() = %d, want 0 (MacosUserRun stub returned 0)", rc)
	}
	if strings.Contains(stderr.String(), "Cannot find yolo-jail repo root") {
		t.Errorf("macos-user launch printed the repo-root failure — it must not be gated:\n%s", stderr.String())
	}
}
