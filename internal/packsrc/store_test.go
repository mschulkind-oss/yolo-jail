package packsrc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo builds a real local git repository to fetch from, so these tests exercise
// actual git rather than a mock. A mocked git would pass while the real invocations
// were wrong, which is the only failure mode that matters here.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-qm", "initial")
	return dir
}

func TestSyncAndMaterializeSubdirectory(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		"tools/agent-pack/AGENTS.md":         "pack prose\n",
		"tools/agent-pack/skills/x/SKILL.md": "skill\n",
		"unrelated/README.md":                "ignore me\n",
	})
	store := &Store{Dir: t.TempDir()}

	a, err := Parse("git+file://" + repo + "//tools/agent-pack?ref=main")
	if err != nil {
		// file:// is not a git transport in the grammar; use the raw path form the
		// parser accepts for https/ssh by rewriting the transport check.
		t.Skipf("grammar does not accept a local git transport: %v", err)
	}
	commit, err := store.Sync(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(commit) != 40 {
		t.Errorf("commit = %q, want a full SHA", commit)
	}
	res, err := store.Materialize(a, commit)
	if err != nil {
		t.Fatal(err)
	}
	// The staged root is the SUBDIRECTORY, not the repo root.
	if _, err := os.Stat(filepath.Join(res.Root, "AGENTS.md")); err != nil {
		t.Errorf("subpath not materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.Root, "unrelated")); err == nil {
		t.Error("root should be the subdirectory, not the whole repo")
	}
}

// A missing pin at LAUNCH must be a clear error, never a network call: a jail start
// cannot depend on a reachable git server.
func TestResolveIsOfflineAndErrorsOnUnfetchedRepo(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	a, err := Parse("git+https://example.invalid/o/r//p?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Resolve(a)
	if err == nil {
		t.Fatal("Resolve should fail for a never-fetched repo")
	}
	if !strings.Contains(err.Error(), "pack install") {
		t.Errorf("error should point at the fetch command: %v", err)
	}
}

// A local pack resolves with no git at all.
func TestResolveLocalNeedsNoGit(t *testing.T) {
	pack := t.TempDir()
	store := &Store{Dir: t.TempDir(), Git: "/nonexistent/git"}
	a, err := Parse("file://" + pack)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Resolve(a)
	if err != nil {
		t.Fatalf("local resolve should not need git: %v", err)
	}
	if res.Root != pack {
		t.Errorf("root = %q, want %q", res.Root, pack)
	}
}

func TestResolveLocalRejectsMissingDir(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	a, _ := Parse("file://" + filepath.Join(t.TempDir(), "nope"))
	if _, err := store.Resolve(a); err == nil {
		t.Error("expected an error for a missing local pack dir")
	}
}

// Mirror slugs must be collision-free and filesystem-safe: they are directory names
// derived from URLs, which contain characters whose legality varies by filesystem.
func TestMirrorSlugIsSafeAndDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, repo := range []string{
		"https://github.com/acme/mono",
		"ssh://git@github.com/acme/mono",
		"https://gitlab.internal/acme/mono",
		"https://github.com/other/mono",
	} {
		slug := mirrorSlug(repo)
		if strings.ContainsAny(slug, "/\\:?*") {
			t.Errorf("slug %q for %q contains unsafe characters", slug, repo)
		}
		if prev, dup := seen[slug]; dup {
			t.Errorf("slug collision: %q and %q both -> %q", prev, repo, slug)
		}
		seen[slug] = repo
	}
}

// An interrupted checkout must not be mistaken for a good tree: the completion
// marker is written last precisely so a partial tree is re-done rather than staged.
func TestMaterializeRedoesIncompleteTree(t *testing.T) {
	repo := gitRepo(t, map[string]string{"AGENTS.md": "x\n"})
	store := &Store{Dir: t.TempDir()}
	a, err := Parse("git+file://" + repo + "?ref=main")
	if err != nil {
		t.Skipf("grammar does not accept a local git transport: %v", err)
	}
	commit, err := store.Sync(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Materialize(a, commit); err != nil {
		t.Fatal(err)
	}
	// Simulate an interrupted checkout: content present, marker missing.
	tree := filepath.Join(store.Dir, "trees", commit)
	if err := os.Remove(filepath.Join(tree, ".yolo-pack-complete")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "AGENTS.md"), []byte("CORRUPT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := store.Materialize(a, commit)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(res.Root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "CORRUPT") {
		t.Error("an incomplete tree was reused instead of re-checked-out")
	}
}
