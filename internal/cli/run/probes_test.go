package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// fakeExec builds an Exec seam matching on the joined argv, with canned results;
// unmatched calls degrade as "not ran" (the missing-binary branch).
func fakeExec(cases map[string]ExecResult) func([]string, string, []string, time.Duration) ExecResult {
	return func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		key := strings.Join(argv, " ")
		if r, ok := cases[key]; ok {
			return r
		}
		return ExecResult{Ran: false}
	}
}

func TestResolveRepoRootEnvVar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "YOLO_REPO_ROOT" {
			return dir
		}
		return ""
	}
	root, ok := resolveRepoRoot(getenv, discardBuf(), false)
	if !ok {
		t.Fatal("expected ok=true for valid YOLO_REPO_ROOT")
	}
	// Resolve both through EvalSymlinks-agnostic abs for comparison.
	wantAbs, _ := filepath.Abs(dir)
	if root.Root != wantAbs {
		t.Errorf("root = %q, want %q", root.Root, wantAbs)
	}
	if root.Source != reporoot.FromEnv {
		t.Errorf("source = %q, want %q — the report line would name the wrong origin",
			root.Source, reporoot.FromEnv)
	}
}

func TestResolveRepoRootEnvVarEmptyDirRejected(t *testing.T) {
	// YOLO_REPO_ROOT set but the dir has neither flake.nix nor go.mod: the env
	// branch must NOT accept it. With no cwd flake.nix and no bundled dir /
	// user config, it falls through to the error.
	empty := t.TempDir()
	// Point HOME at an isolated dir so the staged-bundle branch can't hit a real
	// install.
	t.Setenv("HOME", t.TempDir())
	getenv := func(k string) string {
		if k == "YOLO_REPO_ROOT" {
			return empty
		}
		return os.Getenv(k)
	}
	// A bundle beside the test binary could still resolve, so this asserts only
	// that the env branch was skipped: the returned root, if ok, must NOT be the
	// empty dir.
	root, ok := resolveRepoRoot(getenv, discardBuf(), false)
	if ok {
		abs, _ := filepath.Abs(empty)
		if root.Root == abs {
			t.Errorf("empty YOLO_REPO_ROOT dir was wrongly accepted: %q", root.Root)
		}
	}
}

func TestResolveRepoRootIgnoresUserConfigRepoPath(t *testing.T) {
	// The user-config repo_path key was retired (2026-07-23). A stray repo_path
	// must NOT be resolved. HOME is isolated so the staged bundle misses and the
	// exe-relative bundle misses because the test binary has no share/yolo-jail
	// beside it — nothing left can return repo, so a pass proves the fallback is
	// gone (run + check agree via the single internal/reporoot.Resolve).
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{ "repo_path": "` + repo + `" }`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := resolveRepoRoot(func(string) string { return "" }, discardBuf(), false)
	wantAbs, _ := filepath.Abs(repo)
	if ok && got.Root == wantAbs {
		t.Fatalf("resolveRepoRoot honored the retired repo_path key: %q", got.Root)
	}
}

func TestResolveRepoRootError(t *testing.T) {
	// Force every branch to miss: no env var, no bundle beside the test binary,
	// HOME pointed at an empty dir so no staged bundle resolves. Assert the fix
	// hint renders IF ok=false (a dev machine may legitimately have a bundle
	// beside the test binary, which is not this test's business).
	home := t.TempDir()
	t.Setenv("HOME", home)
	var buf bytes.Buffer
	root, ok := resolveRepoRoot(func(string) string { return "" }, &buf, false)
	if !ok {
		if root.Root != "" {
			t.Errorf("root should be empty on failure, got %q", root.Root)
		}
		if !strings.Contains(buf.String(), "Cannot find yolo-jail repo root") {
			t.Errorf("missing error hint: %q", buf.String())
		}
	}
}

// TestResolveRepoRootIgnoresCwd pins the cwd removal at the RUN call site
// (2026-08-31), not just in internal/reporoot: standing in a directory must not
// change which flake a launch builds from. It subsumes the older audit §B2
// regression (a user's bare flake workspace must not be hijacked) — now nothing
// in cwd is looked at, hijackable or not.
//
// Both shapes are tried: a full yolo-jail-looking checkout (flake.nix + go.mod,
// what the retired walk required) and a bare user flake.
func TestResolveRepoRootIgnoresCwd(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
	}{
		{"full checkout", []string{"flake.nix", "go.mod"}},
		{"bare user flake", []string{"flake.nix"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("HOME", t.TempDir())
			t.Chdir(dir)
			abs, _ := filepath.Abs(dir)
			got, ok := resolveRepoRoot(func(string) string { return "" }, discardBuf(), false)
			if ok && got.Root == abs {
				t.Fatalf("resolved the cwd %q (source %q) — the cwd-walk is back", got.Root, got.Source)
			}
		})
	}
}

func TestResolveRuntimeEnvWins(t *testing.T) {
	o := Options{
		Getenv:   func(k string) string { return map[string]string{"YOLO_RUNTIME": "podman"}[k] },
		LookPath: func(string) (string, bool) { return "/usr/bin/podman", true },
		Exec:     fakeExec(map[string]ExecResult{"podman info": {Ran: true, RC: 0}}),
		Stdout:   discardBuf(),
	}
	fillDefaults(&o)
	o.Getenv = func(k string) string {
		if k == "YOLO_RUNTIME" {
			return "podman"
		}
		return ""
	}
	rt, ok := o.resolveRuntime(nil)
	if !ok || rt != "podman" {
		t.Errorf("resolveRuntime = %q,%v; want podman,true", rt, ok)
	}
}

// Podman installed but the machine VM not started: distinct from not-installed;
// the message must tell the user to START it, not to install it.
func TestResolveRuntimeExplicitNotStarted(t *testing.T) {
	var buf bytes.Buffer
	o := Options{
		Getenv:   func(k string) string { return map[string]string{"YOLO_RUNTIME": "podman"}[k] },
		LookPath: func(string) (string, bool) { return "/usr/bin/podman", true },
		Exec:     fakeExec(map[string]ExecResult{"podman info": {Ran: true, RC: 1}}),
		Stdout:   &buf,
	}
	fillDefaults(&o)
	o.Getenv = func(k string) string {
		if k == "YOLO_RUNTIME" {
			return "podman"
		}
		return ""
	}
	rt, ok := o.resolveRuntime(nil)
	if ok || rt != "" {
		t.Errorf("resolveRuntime = %q,%v; want '',false", rt, ok)
	}
	if !strings.Contains(buf.String(), "installed but not started") ||
		!strings.Contains(buf.String(), "podman machine start") {
		t.Errorf("missing not-started message: %q", buf.String())
	}
}

// An explicit YOLO_RUNTIME=podman with no podman on PATH must fail early with an
// actionable message, not sail into the image build. Regression for the report
// where a missing podman surfaced as an opaque nix/builder failure.
func TestResolveRuntimeExplicitNotInstalled(t *testing.T) {
	var buf bytes.Buffer
	o := Options{
		Getenv:   func(k string) string { return map[string]string{"YOLO_RUNTIME": "podman"}[k] },
		LookPath: func(string) (string, bool) { return "", false },
		Stdout:   &buf,
	}
	fillDefaults(&o)
	o.Getenv = func(k string) string {
		if k == "YOLO_RUNTIME" {
			return "podman"
		}
		return ""
	}
	rt, ok := o.resolveRuntime(nil)
	if ok || rt != "" {
		t.Errorf("resolveRuntime = %q,%v; want '',false", rt, ok)
	}
	if !strings.Contains(buf.String(), "is not installed") ||
		!strings.Contains(buf.String(), "YOLO_RUNTIME") {
		t.Errorf("missing not-installed message: %q", buf.String())
	}
}

// A native runtime (macos-user) is never on PATH — its availability is a
// downstream concern — so validation must let it pass through.
func TestResolveRuntimeNativePassesThrough(t *testing.T) {
	o := Options{
		Getenv:   func(k string) string { return map[string]string{"YOLO_RUNTIME": "macos-user"}[k] },
		LookPath: func(string) (string, bool) { return "", false },
		Stdout:   discardBuf(),
	}
	fillDefaults(&o)
	o.Getenv = func(k string) string {
		if k == "YOLO_RUNTIME" {
			return "macos-user"
		}
		return ""
	}
	rt, ok := o.resolveRuntime(nil)
	if !ok || rt != "macos-user" {
		t.Errorf("resolveRuntime = %q,%v; want macos-user,true", rt, ok)
	}
}

func TestResolveRuntimeNoneFound(t *testing.T) {
	var buf bytes.Buffer
	o := Options{
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, bool) { return "", false },
		Stdout:   &buf,
		IsMacOS:  false,
	}
	fillDefaults(&o)
	o.Stdout = &buf
	rt, ok := o.resolveRuntime(nil)
	if ok || rt != "" {
		t.Errorf("resolveRuntime = %q,%v; want '',false", rt, ok)
	}
	if !strings.Contains(buf.String(), "No container runtime found") {
		t.Errorf("missing no-runtime message: %q", buf.String())
	}
}

// Auto-detect (no explicit runtime) with podman installed but not started must
// say so — not the misleading "install podman" (it IS installed).
func TestResolveRuntimeAutoDetectNotStarted(t *testing.T) {
	var buf bytes.Buffer
	o := Options{
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, bool) { return "/usr/bin/podman", true },
		Exec:     fakeExec(map[string]ExecResult{"podman info": {Ran: true, RC: 1}}),
		Stdout:   &buf,
		IsMacOS:  false,
	}
	fillDefaults(&o)
	o.Stdout = &buf
	rt, ok := o.resolveRuntime(nil)
	if ok || rt != "" {
		t.Errorf("resolveRuntime = %q,%v; want '',false", rt, ok)
	}
	if !strings.Contains(buf.String(), "installed but not started") ||
		!strings.Contains(buf.String(), "podman machine start") {
		t.Errorf("missing not-started message: %q", buf.String())
	}
}

// discardBuf returns a throwaway writer.
func discardBuf() *bytes.Buffer { return &bytes.Buffer{} }
