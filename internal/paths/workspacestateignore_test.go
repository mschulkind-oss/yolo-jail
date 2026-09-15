package paths

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// workspacestateignore_test.go covers EnsureWorkspaceStateDir: the per-workspace state dir
// yolo writes into a user's project holds the jail home overlay, the adoption archive's
// verbatim copies of the user's own agent config files, and a launch.log that carries
// whatever the launcher printed — including a --dry-run's env-pair argv. None of it may
// reach a commit, and this repo's own hand-written .gitignore line is not a fact about
// anybody else's repo.

// gitEnv is the environment every git below runs under: a repo whose ONLY ignore rules are
// the ones in the working tree.
//
// IT IS NOT OPTIONAL, and the first draft of this file shipped without it and passed for the
// wrong reason. A yolo developer's own global ignore file carries `.yolo/` — this jail's does,
// at ~/.config/git/ignore line 41 — so a test that merely asks "is .yolo/launch.log ignored?"
// answers YES on that machine against a yolo that writes nothing at all. GIT_CONFIG_GLOBAL and
// GIT_CONFIG_SYSTEM kill the config files; HOME and XDG_CONFIG_HOME kill the ~/.config/git/ignore
// default, which git consults on its own when core.excludesFile is unset.
func gitEnv(t *testing.T) []string {
	t.Helper()
	empty := t.TempDir()
	var base []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		base = append(base, kv)
	}
	return append(base,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+empty,
		"XDG_CONFIG_HOME="+empty,
	)
}

// gitIgnores reports whether a real git, run inside repo, ignores rel.
//
// REAL GIT, deliberately: the property this feature has to have is "git does not see it", and
// a test that asserted the file's bytes instead would pass on a .gitignore that ignores
// nothing. `check-ignore` exits 0 when the path IS ignored and 1 when it is not.
func gitIgnores(t *testing.T, repo, rel string) bool {
	t.Helper()
	cmd := exec.Command("git", "check-ignore", "-q", "--no-index", rel)
	cmd.Dir = repo
	cmd.Env = gitEnv(t)
	err := cmd.Run()
	if err == nil {
		return true
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 1 {
		t.Fatalf("git check-ignore %s: %v", rel, err)
	}
	return false
}

// writeAt creates rel under repo, parents and all.
func writeAt(t *testing.T, repo, rel string) {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitRepo initialises an empty repo with NO .gitignore of its own — an arbitrary user's
// project, not this one — and PROVES the premise before returning: a file under .yolo is
// committable right now. Without that assertion this whole file can only report on the
// machine it runs on.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	cmd.Env = gitEnv(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeAt(t, repo, ".yolo/premise")
	if gitIgnores(t, repo, ".yolo/premise") {
		t.Fatalf("something OTHER than yolo already ignores .yolo/ in this repo, so nothing " +
			"below can distinguish a working feature from a missing one")
	}
	if err := os.Remove(filepath.Join(repo, ".yolo", "premise")); err != nil {
		t.Fatal(err)
	}
	return repo
}

// THE PROPERTY. A workspace yolo has launched in leaves nothing for git to offer up.
func TestTheWorkspaceStateDirIsUncommittable(t *testing.T) {
	repo := gitRepo(t)

	dir, err := EnsureWorkspaceStateDir(repo)
	if err != nil {
		t.Fatalf("EnsureWorkspaceStateDir: %v", err)
	}
	if want := WorkspaceStateDir(repo); dir != want {
		t.Errorf("returned %q, want the state dir %q", dir, want)
	}

	// One file per thing the directory is dangerous for, at the depths they really sit at.
	for _, rel := range []string{
		".yolo/launch.log", // the launcher's argv, secrets included
		".yolo/archive/config/claude-settings/settings.json", // the user's own pre-yolo config
		".yolo/home/claude/.credentials.json",                // the home overlay
		".yolo/handover.md",                                  // the one-time pointer, consumed by renaming
	} {
		writeAt(t, repo, rel)
		if !gitIgnores(t, repo, rel) {
			t.Errorf("git does not ignore %s — it would be offered up by the next `git add .`", rel)
		}
	}

	// And the ignore file ignores ITSELF, which is the entire reason the content is a bare
	// `*` and not a list. Checked WITHOUT writing over it, unlike the loop above: it is
	// already there, and clobbering it with a placeholder is how this test first failed.
	if !gitIgnores(t, repo, ".yolo/"+WorkspaceStateIgnoreName) {
		t.Errorf("the ignore file does not ignore itself, so .yolo/ still shows as untracked")
	}

	// The end-to-end statement of the same thing: git has nothing to report at all.
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repo
	cmd.Env = gitEnv(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("a launched workspace is not clean:\n%s", out)
	}
}

// A .yolo that already exists gets the file too. This is the case that matters most in
// practice — every workspace yolo has ever launched — and the one a "write it when I create
// the directory" implementation silently misses forever.
func TestAPreExistingStateDirStillGetsIgnored(t *testing.T) {
	repo := gitRepo(t)
	stale := filepath.Join(repo, ".yolo")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "launch.log"), []byte("AWS_SECRET_ACCESS_KEY=..."), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureWorkspaceStateDir(repo); err != nil {
		t.Fatalf("EnsureWorkspaceStateDir: %v", err)
	}
	if !gitIgnores(t, repo, ".yolo/launch.log") {
		t.Error("a state dir that predates this feature was left committable")
	}
}

// A .gitignore the user has edited is theirs. yolo writes once and never again — including
// when the user emptied it on purpose, which is how they opt back INTO committing.
func TestAUserEditedIgnoreFileIsNeverClobbered(t *testing.T) {
	ws := t.TempDir()
	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(ws, ".yolo", WorkspaceStateIgnoreName)

	for _, mine := range []string{"# mine\n*\n!handover.md\n", ""} {
		if err := os.WriteFile(p, []byte(mine), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := EnsureWorkspaceStateDir(ws); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != mine {
			t.Errorf("yolo rewrote a .gitignore the user owns\n got: %q\nwant: %q", got, mine)
		}
	}
}

// A dangling symlink is still a file the user put here: os.WriteFile would FOLLOW it and
// create whatever it points at, somewhere yolo was never asked to write.
func TestADanglingIgnoreSymlinkIsNotFollowed(t *testing.T) {
	ws := t.TempDir()
	dir := WorkspaceStateDir(ws)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(target, filepath.Join(dir, WorkspaceStateIgnoreName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err == nil {
		t.Errorf("the write followed the symlink and created %s", target)
	}
}

// A workspace yolo cannot write into must not cost anyone a launch: every caller refuses on
// MkdirAll alone, and the ignore file is best-effort behind it.
func TestEnsureRefusesOnlyWhenTheDirectoryCannotExist(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".yolo"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureWorkspaceStateDir(ws); err == nil {
		t.Error("a .yolo that is a FILE has to reach the caller — attachLaunchLog and " +
			"attachBootLog both bail on it")
	}
}
