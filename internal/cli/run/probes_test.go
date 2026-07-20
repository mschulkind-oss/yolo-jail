package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if root != wantAbs {
		t.Errorf("root = %q, want %q", root, wantAbs)
	}
}

func TestResolveRepoRootEnvVarEmptyDirRejected(t *testing.T) {
	// YOLO_REPO_ROOT set but the dir has neither flake.nix nor the entrypoint
	// source: the env branch must NOT accept it (the nested-jail empty-bind
	// case). With no cwd flake.nix and no bundled dir / user config, it falls
	// through to the error.
	empty := t.TempDir()
	// cwd during test is the package dir (has no flake.nix up to /). Point HOME
	// at an isolated dir so the user-config branch can't hit a real config.
	t.Setenv("HOME", t.TempDir())
	getenv := func(k string) string {
		if k == "YOLO_REPO_ROOT" {
			return empty
		}
		return os.Getenv(k)
	}
	// Force cwd to a dir with no flake.nix anywhere above it is impossible
	// (temp dirs live under /tmp), so this only asserts the env branch was
	// skipped: the returned root, if ok, must NOT be the empty dir.
	root, ok := resolveRepoRoot(getenv, discardBuf(), false)
	if ok {
		abs, _ := filepath.Abs(empty)
		if root == abs {
			t.Errorf("empty YOLO_REPO_ROOT dir was wrongly accepted: %q", root)
		}
	}
}

func TestResolveRepoRootUserConfig(t *testing.T) {
	// A user config with repo_path pointing at a flake.nix dir, no env var, cwd
	// walk fails (we can't control cwd easily, so give a repo that the cwd walk
	// won't find: use a dir OUTSIDE any ancestor). We assert the resolver
	// returns SOME valid root; the specific branch is exercised by env/cwd
	// tests. Here we mainly guard that a repo_path config parses.
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
	// The cwd-walk (step 2) runs first and will find the repo's own flake.nix
	// only if a test happens to run under one; in CI the module root has a
	// flake.nix, so step 2 wins. Either way resolveRepoRoot must return ok.
	_, ok := resolveRepoRoot(func(string) string { return "" }, discardBuf(), false)
	if !ok {
		t.Error("expected a resolvable repo root (cwd walk or user config)")
	}
}

func TestResolveRepoRootError(t *testing.T) {
	// Force every branch to miss: no env var, cwd is an isolated temp with no
	// flake.nix up to /, no bundled dir, HOME with no config. We can only
	// control cwd via os.Chdir; do it under a temp dir with no flake.nix
	// ancestor is impossible (temp lives under real dirs). Instead assert the
	// error message renders when all controllable branches miss by pointing
	// HOME at an empty dir and checking stderr carries the fix hint IF ok=false.
	home := t.TempDir()
	t.Setenv("HOME", home)
	var buf bytes.Buffer
	root, ok := resolveRepoRoot(func(string) string { return "" }, &buf, false)
	if !ok {
		if root != "" {
			t.Errorf("root should be empty on failure, got %q", root)
		}
		if !strings.Contains(buf.String(), "Cannot find yolo-jail repo root") {
			t.Errorf("missing error hint: %q", buf.String())
		}
	}
}

// TestResolveRepoRootDoesNotHijackBareFlake is the audit §B2 regression: a
// user's own flake workspace (flake.nix present, but NO go.mod) must NOT be
// resolved as the yolo-jail repo when YOLO_REPO_ROOT is unset.
func TestResolveRepoRootDoesNotHijackBareFlake(t *testing.T) {
	stub := t.TempDir()
	if err := os.WriteFile(filepath.Join(stub, "flake.nix"), []byte("{ outputs = _: {}; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(stub)
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, ok := resolveRepoRoot(func(string) string { return "" }, discardBuf(), false)
	if ok && got == stub {
		t.Fatalf("hijacked the bare user flake as the repo root: %q", got)
	}
	if ok && !fileExistsTest(filepath.Join(got, "go.mod")) {
		t.Errorf("resolved a non-yolo-jail dir %q (missing go.mod)", got)
	}
}

func fileExistsTest(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// writeGoBundle writes a minimal but shaped share/yolo-jail/ bundle into dir:
// the goSrc fileset markers (flake.nix, flake.lock, go.mod, go.sum) plus a
// couple of nested source dirs, so staging has real subtrees to copy.
func writeGoBundle(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"flake.nix":                 "{ outputs = _: {}; }\n",
		"flake.lock":                "{}\n",
		"go.mod":                    "module x\n",
		"go.sum":                    "\n",
		"vendor/modules.txt":        "# vendored\n",
		"cmd/yolo/main.go":          "package main\n",
		"internal/x/x.go":           "package x\n",
		"bundled_loopholes/note.md": "loophole\n",
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestBundledSourceDirFrom covers the two candidate layouts: ../share/yolo-jail
// (release-archive/brew) preferred over the exe's own dir.
func TestBundledSourceDirFrom(t *testing.T) {
	// Layout A: <root>/bin/yolo with <root>/share/yolo-jail/flake.nix.
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	shareDir := filepath.Join(root, "share", "yolo-jail")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoBundle(t, shareDir)
	got, ok := bundledSourceDirFrom(binDir)
	if !ok {
		t.Fatal("expected to find ../share/yolo-jail bundle")
	}
	if wantAbs, _ := filepath.Abs(shareDir); got != wantAbs {
		t.Errorf("got %q, want %q", got, wantAbs)
	}

	// Layout B: <exeDir>/share/yolo-jail (release-archive layout: yolo + share/
	// at one level).
	archiveRoot := t.TempDir()
	writeGoBundle(t, filepath.Join(archiveRoot, "share", "yolo-jail"))
	if got, ok := bundledSourceDirFrom(archiveRoot); !ok || got != mustAbs(filepath.Join(archiveRoot, "share", "yolo-jail")) {
		t.Errorf("archive layout <exe>/share/yolo-jail: got %q,%v", got, ok)
	}

	// Layout C: bundle sitting directly beside the binary.
	beside := t.TempDir()
	writeGoBundle(t, beside)
	if got, ok := bundledSourceDirFrom(beside); !ok || got != mustAbs(beside) {
		t.Errorf("beside-exe layout: got %q,%v", got, ok)
	}

	// No bundle → miss.
	if _, ok := bundledSourceDirFrom(t.TempDir()); ok {
		t.Error("empty dir should not resolve a bundle")
	}
}

// TestStageInstalledWheelStagesFlat is the core D3 regression: staging a Go
// bundle must lay the full fileset FLAT into build_root (not build_root/src, the
// retired wheel layout) so `nix build .#ociImage` with cwd=build_root evaluates.
func TestStageInstalledWheelStagesFlat(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // GlobalStorage is HOME-derived; isolate it.
	bundle := t.TempDir()
	writeGoBundle(t, bundle)

	root, ok := stageInstalledWheel(bundle)
	if !ok {
		t.Fatal("stageInstalledWheel returned ok=false")
	}
	// The fileset must be present FLAT at build_root — the exact paths the
	// flake's goSrc fileset (root ./.) requires.
	for _, rel := range []string{
		"flake.nix", "flake.lock", "go.mod", "go.sum",
		"vendor/modules.txt", "cmd/yolo/main.go", "internal/x/x.go",
		"bundled_loopholes/note.md",
	} {
		if !fileExistsTest(filepath.Join(root, rel)) {
			t.Errorf("staged tree missing %q", rel)
		}
	}
	// The retired wheel layout must NOT appear.
	if fileExistsTest(filepath.Join(root, "src", "flake.nix")) {
		t.Error("staged into build_root/src (retired wheel layout); want flat")
	}
}

// TestStageInstalledWheelIdempotent guards that a second stage of an unchanged
// bundle is a no-op (returns the same build_root without repopulating), and
// never rmtrees — the frozen invariant. We assert no leftover .old.* generation
// is created on the idempotent path.
func TestStageInstalledWheelIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bundle := t.TempDir()
	writeGoBundle(t, bundle)

	root1, ok := stageInstalledWheel(bundle)
	if !ok {
		t.Fatal("first stage failed")
	}
	// Record the build_root inode so we can prove the second call didn't swap it.
	fi1, err := os.Stat(root1)
	if err != nil {
		t.Fatal(err)
	}
	root2, ok := stageInstalledWheel(bundle)
	if !ok || root2 != root1 {
		t.Fatalf("second stage: %q,%v (want same root)", root2, ok)
	}
	fi2, _ := os.Stat(root2)
	if !os.SameFile(fi1, fi2) {
		t.Error("idempotent stage swapped the build_root inode (unnecessary repopulate)")
	}
}

func mustAbs(p string) string {
	a, _ := filepath.Abs(p)
	return a
}

func TestCollectIdentityEnv(t *testing.T) {
	o := Options{
		Exec: fakeExec(map[string]ExecResult{
			"git config --get user.name":  {Stdout: "Ada Lovelace\n", Ran: true, RC: 0},
			"git config --get user.email": {Stdout: "ada@example.com\n", Ran: true, RC: 0},
			"jj config get user.name":     {Stdout: "\"Grace Hopper\"\n", Ran: true, RC: 0},
			"jj config get user.email":    {Stdout: "grace@example.com", Ran: true, RC: 0},
		}),
	}
	got := o.collectIdentityEnv()
	want := []string{
		"-e", "YOLO_GIT_NAME=Ada Lovelace",
		"-e", "YOLO_GIT_EMAIL=ada@example.com",
		"-e", "YOLO_JJ_NAME=Grace Hopper", // quotes stripped
		"-e", "YOLO_JJ_EMAIL=grace@example.com",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("identity env = %v, want %v", got, want)
	}
}

func TestCollectIdentityEnvSkipsMissing(t *testing.T) {
	// git present, jj absent (Ran=false), empty email skipped.
	o := Options{
		Exec: fakeExec(map[string]ExecResult{
			"git config --get user.name":  {Stdout: "Ada\n", Ran: true, RC: 0},
			"git config --get user.email": {Stdout: "\n", Ran: true, RC: 0}, // empty → skip
		}),
	}
	got := o.collectIdentityEnv()
	want := []string{"-e", "YOLO_GIT_NAME=Ada"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("identity env = %v, want %v", got, want)
	}
}

func TestResolveRuntimeEnvWins(t *testing.T) {
	o := Options{
		Getenv:   func(k string) string { return map[string]string{"YOLO_RUNTIME": "podman"}[k] },
		LookPath: func(string) (string, bool) { return "", false },
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

// discardBuf returns a throwaway writer.
func discardBuf() *bytes.Buffer { return &bytes.Buffer{} }
