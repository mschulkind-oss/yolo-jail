package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// `pack init` must scaffold a pack that `pack lint` accepts. If the scaffold did not
// lint clean, every author's first action would produce an error — and the two would
// be free to drift apart.
func TestPackInitScaffoldLintsClean(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	if rc := packMain([]string{"init", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("init rc = %d: %s", rc, errw.String())
	}
	for _, want := range []string{"AGENTS.md", "SKILL.md", "README.md"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("init did not create %s:\n%s", want, out.String())
		}
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("scaffolded pack does not lint clean: rc %d\n%s%s", rc, out.String(), errw.String())
	}
}

// init must be safe to re-run: it reports skips rather than clobbering an author's
// edited files.
func TestPackInitDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	edited := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(edited, []byte("MY OWN PROSE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if rc := packMain([]string{"init", dir}, &out, &errw, false); rc != 0 {
		t.Fatal(errw.String())
	}
	if !strings.Contains(out.String(), "skip AGENTS.md") {
		t.Errorf("re-run should report a skip:\n%s", out.String())
	}
	if data, _ := os.ReadFile(edited); !strings.Contains(string(data), "MY OWN PROSE") {
		t.Error("init clobbered an edited file")
	}
}

// lint runs the REAL staging rules, so an author hits the exec-bit refusal before a
// consumer's jail does. A linter that disagreed with the stager would be worse than
// none.
func TestPackLintReportsStagingRefusals(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	if err := os.WriteFile(filepath.Join(dir, "hook.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatal("expected lint to fail on an executable file")
	}
	if !strings.Contains(errw.String(), "allow_exec") {
		t.Errorf("lint error should name the opt-in: %s", errw.String())
	}
}

// A skill dir with no SKILL.md is invisible to every agent and produces no error
// anywhere else — the single most likely authoring mistake.
func TestPackLintCatchesSkillDirWithoutManifest(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	broken := filepath.Join(dir, "skills", "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatal("expected lint to flag a skill dir with no SKILL.md")
	}
	if !strings.Contains(out.String(), "skills/broken") {
		t.Errorf("lint did not name the offending dir:\n%s", out.String())
	}
}

// A lint-clean pack whose files nothing reads is still a problem: it stages content
// no agent looks at, which the author almost certainly did not intend.
func TestPackLintFlagsPackNothingReads(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatal("expected lint to flag a pack with no skills/ and no AGENTS.md")
	}
	if !strings.Contains(out.String(), "nothing reads") {
		t.Errorf("lint message unclear:\n%s", out.String())
	}
}

func TestPackUnknownVerbIsAnError(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := packMain([]string{"frobnicate"}, &out, &errw, false); rc == 0 {
		t.Error("unknown verb should fail")
	}
	if !strings.Contains(errw.String(), "unknown verb") {
		t.Errorf("stderr = %s", errw.String())
	}
}

// `explain` is the answer to "why isn't my skill showing up?", so it must report
// the FILTERED files, not just the staged ones.
func TestPackExplainReportsFilteredFiles(t *testing.T) {
	home := t.TempDir()
	pack := t.TempDir()
	t.Setenv("HOME", home)

	var out, errw bytes.Buffer
	packMain([]string{"init", pack}, &out, &errw, false)

	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{"packs": [{"source": "file://`+pack+`", "name": "p", "only": ["skills/*"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"explain", "p"}, &out, &errw, false); rc != 0 {
		t.Fatalf("explain rc = %d: %s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "skills/example/SKILL.md") {
		t.Errorf("explain did not list the staged skill:\n%s", got)
	}
	// The whole point: the excluded files are named.
	if !strings.Contains(got, "AGENTS.md") || !strings.Contains(got, "filtered out") {
		t.Errorf("explain must report what the filters dropped:\n%s", got)
	}
}

func TestPackExplainUnknownNameIsAnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if rc := packMain([]string{"explain", "nope"}, &out, &errw, false); rc == 0 {
		t.Error("explain of an unconfigured pack should fail")
	}
	if !strings.Contains(errw.String(), "pack ls") {
		t.Errorf("error should point at `yolo pack ls`: %s", errw.String())
	}
}

// `pack ls` with nothing configured must explain where packs go rather than
// printing an empty table.
func TestPackLsEmptyExplainsWhereToConfigure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if rc := packMain([]string{"ls"}, &out, &errw, false); rc != 0 {
		t.Fatal(errw.String())
	}
	if !strings.Contains(out.String(), "user scope only") {
		t.Errorf("empty ls should say where packs are configured:\n%s", out.String())
	}
}

// gitPackRepo builds a real git repo containing a pack in a subdirectory, so the
// install path exercises actual git rather than a mock.
func gitPackRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "tools", "agent-pack", "skills", "gitskill")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "SKILL.md"),
		[]byte("---\nname: gitskill\ndescription: from git\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-qm", "pack")
	return dir
}

// C5 end to end: install FETCHES and records a commit; status reports it; a second
// install is a no-op that says "unchanged".
func TestPackInstallFetchesAndLocks(t *testing.T) {
	repo := gitPackRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "git+file://" + repo + "//tools/agent-pack?ref=main"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{"packs": [{"source": "`+src+`", "name": "gp"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("install rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "gp") {
		t.Errorf("install did not report the pack:\n%s", out.String())
	}

	// The lockfile records the COMMIT, not just the ref: "what you asked for" vs
	// "what you got" is the whole reason it exists.
	lock, err := packsrc.LoadLock(packsrc.LockPath(filepath.Join(cfgDir, "config.jsonc")))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := lock.Get("gp")
	if !ok {
		t.Fatalf("pack not locked: %+v", lock.Packs)
	}
	if len(e.Commit) != 40 || e.Ref != "main" {
		t.Errorf("lock entry = %+v, want a full SHA and ref=main", e)
	}

	// Re-install is idempotent and says so, rather than implying it re-fetched.
	out.Reset()
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("second install rc = %d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "unchanged") {
		t.Errorf("second install should report unchanged:\n%s", out.String())
	}

	// status reports the locked commit.
	out.Reset()
	if rc := packMain([]string{"status"}, &out, &errw, false); rc != 0 {
		t.Fatalf("status rc = %d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), e.Commit[:8]) {
		t.Errorf("status did not show the locked commit:\n%s", out.String())
	}
}

// DRIFT: editing the config address without re-installing must be REPORTED. Launch
// resolves from the store, so a silently-stale lock is the most confusing possible
// behavior — the user's edit appears to do nothing.
func TestPackStatusFlagsConfigDrift(t *testing.T) {
	repo := gitPackRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(cfgDir, "config.jsonc")
	write := func(ref string) {
		src := "git+file://" + repo + "//tools/agent-pack?ref=" + ref
		if err := os.WriteFile(cfg,
			[]byte(`{"packs": [{"source": "`+src+`", "name": "gp"}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out, errw bytes.Buffer
	write("main")
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("install rc = %d: %s", rc, errw.String())
	}

	// Edit the ref without re-installing.
	write("some-other-ref")
	out.Reset()
	rc := packMain([]string{"status"}, &out, &errw, false)
	if rc == 0 {
		t.Error("status should fail when config and lock disagree")
	}
	if !strings.Contains(out.String(), "config changed since install") {
		t.Errorf("status did not flag drift:\n%s", out.String())
	}
}

// A pack removed from config must be pruned from the lockfile, and the removal
// REPORTED: it means content is about to stop being delivered.
func TestPackInstallPrunesRemovedPacks(t *testing.T) {
	home := t.TempDir()
	pack := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(cfgDir, "config.jsonc")
	if err := os.WriteFile(cfg,
		[]byte(`{"packs": [{"source": "file://`+pack+`", "name": "gone"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	packMain([]string{"install"}, &out, &errw, false)

	if err := os.WriteFile(cfg, []byte(`{"packs": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	packMain([]string{"install"}, &out, &errw, false)

	lock, err := packsrc.LoadLock(packsrc.LockPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, still := lock.Get("gone"); still {
		t.Error("a pack removed from config should be pruned from the lockfile")
	}
}
