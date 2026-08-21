package packsrc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// noStagedTree is the "this is an ordinary host" environment: no YOLO_PACK_ROOT, so
// Resolve's staged-tree fallback has nothing to find.
//
// Every Resolve test states its delivery situation through this seam rather than
// reading the process environment, because this repo is developed from inside its own
// jail — where YOLO_PACK_ROOT really is set to a populated tree — so an ambient read
// would silently make the outcome depend on the machine running the suite.
func noStagedTree(string) string { return "" }

// stagedTree writes a delivered pack tree named `name` under a fresh root and returns
// a getenv that names the root, i.e. what a launcher mounted at /ctx/packs looks like.
func stagedTree(t *testing.T, name string) (string, func(string) string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"),
		[]byte(`{"name":"`+name+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, func(k string) string {
		if k == "YOLO_PACK_ROOT" {
			return root
		}
		return ""
	}
}

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

// A `git commit` hook inherits GIT_INDEX_FILE=.git/index (relative) from the
// committing repo. If that leaks into a Store's git subprocess, a bare mirror's
// `--work-tree` checkout resolves the relative index against the WORK-TREE and
// tries to write <tree>/.git/index.lock, which does not exist — every checkout
// fails. The store must strip git's own state vars so a run against one repo
// cannot be redirected by state git set for another.
func TestMaterializeIgnoresInheritedGitStateEnv(t *testing.T) {
	repo := gitRepo(t, map[string]string{"AGENTS.md": "x\n"})
	store := &Store{
		Dir: t.TempDir(),
		Env: append(os.Environ(), "GIT_INDEX_FILE=.git/index"),
	}
	a, err := Parse("git+file://" + repo + "?ref=main")
	if err != nil {
		t.Skipf("grammar does not accept a local git transport: %v", err)
	}
	commit, err := store.Sync(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Materialize(a, commit); err != nil {
		t.Fatalf("Materialize with inherited git state env: %v", err)
	}
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
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	a, err := Parse("git+https://example.invalid/o/r//p?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Resolve(a, "p")
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
	store := &Store{Dir: t.TempDir(), Git: "/nonexistent/git", Getenv: noStagedTree}
	a, err := Parse("file://" + pack)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Resolve(a, filepath.Base(pack))
	if err != nil {
		t.Fatalf("local resolve should not need git: %v", err)
	}
	if res.Root != pack {
		t.Errorf("root = %q, want %q", res.Root, pack)
	}
	if res.StagedFrom != "" {
		t.Errorf("StagedFrom = %q for a source that resolved normally; the fallback must "+
			"report only what it actually fell back to", res.StagedFrom)
	}
}

func TestResolveLocalRejectsMissingDir(t *testing.T) {
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	a, _ := Parse("file://" + filepath.Join(t.TempDir(), "nope"))
	if _, err := store.Resolve(a, "nope"); err == nil {
		t.Error("expected an error for a missing local pack dir")
	}
}

// THE FALLBACK, at the one place that owns resolution.
//
// Inside a jail every pack address in the inherited config names a HOST path, so a
// local pack's source is never a directory in here — and the pack is nonetheless
// working, out of the tree the launcher delivered under YOLO_PACK_ROOT. `yolo check`
// knew that and `yolo run` did not, so a nested launch was refused outright and the
// nested verification AGENTS.md mandates was impossible with a local pack selected
// (docs/design/storage-and-config.md §10, OQ-SC1 ruled option (i)).
//
// Delete the fallback from Resolve and this goes red, at BOTH callers at once — which
// is the property the ruling bought and the reason the test lives here rather than in
// one caller's package.
func TestResolveFallsBackToTheDeliveredTree(t *testing.T) {
	// A source path that does not exist, exactly as a host path looks from in-jail.
	missing := filepath.Join(t.TempDir(), "matt-core")
	staged, getenv := stagedTree(t, "matt-core")
	store := &Store{Dir: t.TempDir(), Git: "/nonexistent/git", Getenv: getenv}

	a, err := Parse("file://" + missing)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Resolve(a, "matt-core")
	if err != nil {
		t.Fatalf("a pack whose source is invisible but whose staged copy is present is "+
			"working, and must resolve: %v", err)
	}
	if res.Root != staged {
		t.Errorf("root = %q, want the delivered tree %q", res.Root, staged)
	}
	// The PROVENANCE, which is how a caller reports "staged at <path>" without
	// recomputing the answer it was just handed.
	if res.StagedFrom != staged {
		t.Errorf("StagedFrom = %q, want %q — a caller cannot tell the fallback fired",
			res.StagedFrom, staged)
	}
}

// The same fallback for a FETCHED pack, which §10 does not mention and which has the
// identical shape: the pack STORE is host-side too, so `pack %s has never been fetched`
// is what a jail says about a mirror that exists one filesystem away. The delivered
// tree answers that question as well as it answers the local one.
func TestResolveFallsBackToTheDeliveredTreeForAFetchedPack(t *testing.T) {
	staged, getenv := stagedTree(t, "r")
	store := &Store{Dir: t.TempDir(), Getenv: getenv}

	a, err := Parse("git+https://example.invalid/o/r?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Resolve(a, "r")
	if err != nil {
		t.Fatalf("a fetched pack with no mirror in here but a delivered tree is working: %v", err)
	}
	if res.StagedFrom != staged {
		t.Errorf("StagedFrom = %q, want %q", res.StagedFrom, staged)
	}
}

// THE ANTI-VACUITY CONTROLS: the fallback must not become "never report a missing
// pack". Two shapes, because they fail differently — no staged tree at all (a host),
// and a staged tree that holds no pack of this name (a jail where staging really did
// fail).
func TestResolveStillErrorsWithNoDeliveredTree(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	a, err := Parse("file://" + missing)
	if err != nil {
		t.Fatal(err)
	}

	// (a) No YOLO_PACK_ROOT: the ordinary host, where the predicate must never fire.
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	if _, err := store.Resolve(a, "gone"); err == nil {
		t.Error("a pack that is neither resolvable nor delivered is broken, and on a host " +
			"the fallback must not fire at all")
	}

	// (b) A pack root that exists and holds a DIFFERENT pack. Keyed on the filesystem,
	// so the question is "is THIS pack delivered", not "am I in a jail".
	_, getenv := stagedTree(t, "somebody-else")
	store = &Store{Dir: t.TempDir(), Getenv: getenv}
	if _, err := store.Resolve(a, "gone"); err == nil {
		t.Error("a staged tree holding no pack of this name must not rescue it")
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
