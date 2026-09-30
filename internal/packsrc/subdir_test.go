package packsrc

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// subdir_test.go pins the SUBDIRECTORY CHECKOUT (store.go: treeDir, literalPathspec,
// checkoutTree, checkSubdir): a pack in a subdirectory of a repository materializes that
// subdirectory and nothing else, under a pathspec no glob character can widen, in a tree no
// earlier commit's file can reach, with symlinks judged as the pack loader judges them.
// Every repository here is a real one reached over git+file://, because a mocked git would
// pass while the real pathspec was wrong.

// refreshOne runs one Refresh of a single pack at now and returns its outcome.
func refreshOne(t *testing.T, s *Store, source string, now time.Time) Outcome {
	t.Helper()
	outs, err := s.Refresh([]RefreshPack{{Name: "p", Source: source}},
		RefreshOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return outs[0]
}

// treeFiles lists every path under the tree a resolution came from, slash-separated and
// relative to the tree, with a symlink shown as "path -> target". The completion marker is
// left out: it is the store's, not the pack's.
func treeFiles(t *testing.T, tree string) []string {
	t.Helper()
	var got []string
	err := filepath.Walk(tree, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(tree, p)
		if rel == "." || rel == treeCompleteMarker || fi.IsDir() {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(p)
			rel += " -> " + target
		}
		got = append(got, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	return got
}

// resolveTree refreshes source into a fresh store and returns the resolution and the tree
// directory it came from.
func resolveTree(t *testing.T, source string) (*Resolved, string) {
	t.Helper()
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	o := refreshOne(t, store, source, time.Unix(1_800_000_000, 0))
	if o.Err != nil {
		t.Fatalf("refreshing %s: %v", source, o.Err)
	}
	a := mustParse(t, source)
	res, err := store.Resolve(a, "p")
	if err != nil {
		t.Fatalf("resolving %s: %v", source, err)
	}
	return res, store.treeDir(a, res.Commit)
}

// A PACK IN A SUBDIRECTORY MATERIALIZES THAT SUBDIRECTORY ONLY. The siblings are the ones a
// careless match would take too: a directory whose name extends the pack's (`pack-other`), a
// file whose name does (`packfoo.txt`), the parent's other files, and the rest of the repo.
// With the old `checkout -- .` every one of them was in the tree.
func TestMaterializeChecksOutOnlyTheSubdirectory(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		"tools/pack/pack.json":         `{"name":"p"}`,
		"tools/pack/skills/x/SKILL.md": "skill\n",
		"tools/pack-other/pack.json":   `{"name":"other"}`,
		"tools/packfoo.txt":            "sibling file\n",
		"tools/README.md":              "parent\n",
		"big/blob.bin":                 "the rest of the repository\n",
	})
	res, tree := resolveTree(t, "git+file://"+repo+"//tools/pack?ref=main")
	want := []string{"tools/pack/pack.json", "tools/pack/skills/x/SKILL.md"}
	if got := treeFiles(t, tree); !slices.Equal(got, want) {
		t.Errorf("the tree of a subdirectory pack holds %v, want exactly %v", got, want)
	}
	if res.Root != filepath.Join(tree, "tools", "pack") {
		t.Errorf("Root = %s, want the subdirectory at its repository path inside %s", res.Root, tree)
	}
}

// THE REPOSITORY-ROOT CASE IS UNCHANGED: a pack with no subdirectory still gets the whole
// commit, at trees/<commit>, with the tree itself as its root.
func TestMaterializeRootPackKeepsTheWholeCommit(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		"pack.json":            `{"name":"p"}`,
		"skills/x/SKILL.md":    "skill\n",
		"elsewhere/README.md":  "still part of a root pack\n",
		"elsewhere/deep/f.txt": "and this\n",
	})
	res, tree := resolveTree(t, "git+file://"+repo+"?ref=main")
	if want := filepath.Join(filepath.Dir(tree), res.Commit); tree != want || res.Root != tree {
		t.Errorf("a root pack's tree = %s and root = %s, want both %s", tree, res.Root, want)
	}
	want := []string{"elsewhere/README.md", "elsewhere/deep/f.txt", "pack.json", "skills/x/SKILL.md"}
	if got := treeFiles(t, tree); !slices.Equal(got, want) {
		t.Errorf("a root pack's tree holds %v, want the whole commit %v", got, want)
	}
}

// THE PATHSPEC IS LITERAL. A subdirectory whose name is a glob must check out that directory
// and no sibling the glob would match; without `literal` magic, `p*` also takes `pa`, `pb`,
// `pother` and `p[ab]`, and `p[ab]` takes `pa` and `pb`. A name that looks like pathspec magic
// is a name too: bare, `:(glob)pa` would select `pa`.
func TestMaterializeSubdirectoryPathspecIsLiteral(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		"p*/pack.json":        `{"name":"star"}`,
		"p[ab]/pack.json":     `{"name":"class"}`,
		"pa/secret.txt":       "a\n",
		"pb/secret.txt":       "b\n",
		"pother/secret.md":    "other\n",
		":(glob)pa/pack.json": `{"name":"magic"}`,
	})
	for _, sub := range []string{"p*", "p[ab]", ":(glob)pa"} {
		_, tree := resolveTree(t, "git+file://"+repo+"//"+sub+"?ref=main")
		if got, want := treeFiles(t, tree), []string{sub + "/pack.json"}; !slices.Equal(got, want) {
			t.Errorf("subdirectory %q checked out %v, want only %v", sub, got, want)
		}
	}
}

// A PATHSPEC DIAL IN THE ENVIRONMENT CANNOT WIDEN IT EITHER. git exports GIT_ICASE_PATHSPECS
// to its subprocesses under `git --icase-pathspecs`; inherited, it would match every
// case-variant of the subdirectory (and makes `ls-tree` refuse to run). The store strips it.
func TestMaterializeIgnoresInheritedPathspecDials(t *testing.T) {
	probe := t.TempDir()
	if err := os.WriteFile(filepath.Join(probe, "a"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(probe, "A")); err == nil {
		t.Skip("case-insensitive filesystem: a repository holding pack/ and PACK/ cannot be checked out here")
	}
	repo := gitRepo(t, map[string]string{
		"pack/pack.json": `{"name":"p"}`,
		"PACK/secret.md": "a case-variant sibling\n",
	})
	source := "git+file://" + repo + "//pack?ref=main"
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree,
		Env: append(os.Environ(), "GIT_ICASE_PATHSPECS=1", "GIT_GLOB_PATHSPECS=1")}
	if o := refreshOne(t, store, source, time.Unix(1_800_000_000, 0)); o.Err != nil {
		t.Fatalf("with pathspec dials inherited: %v", o.Err)
	}
	a := mustParse(t, source)
	res, err := store.Resolve(a, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := treeFiles(t, store.treeDir(a, res.Commit)), []string{"pack/pack.json"}; !slices.Equal(got, want) {
		t.Errorf("checked out %v, want only %v", got, want)
	}
}

// A REF CHANGE LEAVES NO STALE FILES. A file present at A and deleted at B must be absent once
// a branch pack moves from A to B through the launch's own refresh, for a subdirectory pack
// and a root pack alike; the file B added must be there; A's tree, which another launch may
// still be staging from, is left whole; and the mirror's shared index is never written, the
// checkout using an index of its own.
func TestRefreshMoveToANewCommitLeavesNoStaleFiles(t *testing.T) {
	for _, tc := range []struct{ name, sub string }{{"subdirectory", "sub"}, {"repository root", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := gitRepo(t, map[string]string{
				"sub/pack.json": `{"name":"p"}`,
				"sub/old.md":    "only at A\n",
			})
			source := "git+file://" + repo + "?ref=main"
			rel := func(name string) string { return name }
			if tc.sub != "" {
				source = "git+file://" + repo + "//" + tc.sub + "?ref=main"
			} else {
				rel = func(name string) string { return "sub/" + name }
			}
			store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
			now := time.Unix(1_800_000_000, 0)
			a := mustParse(t, source)

			oA := refreshOne(t, store, source, now)
			if oA.Err != nil {
				t.Fatal(oA.Err)
			}
			resA, err := store.Resolve(a, "p")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(resA.Root, rel("old.md"))); err != nil {
				t.Fatalf("fixture: A's file is not in A's tree: %v", err)
			}

			gitIn(t, repo, "rm", "-q", "sub/old.md")
			commitB := commitFile(t, repo, "sub/new.md", "only at B\n")

			oB := refreshOne(t, store, source, now.Add(2*BranchRefreshInterval))
			if oB.Err != nil || oB.Commit != commitB {
				t.Fatalf("the branch did not move to B (%s): %+v", commitB[:8], oB)
			}
			resB, err := store.Resolve(a, "p")
			if err != nil || resB.Commit != commitB {
				t.Fatalf("Resolve after the move = %+v, %v", resB, err)
			}
			if _, err := os.Lstat(filepath.Join(resB.Root, rel("old.md"))); !os.IsNotExist(err) {
				t.Errorf("the file B deleted is still in B's tree (lstat: %v)", err)
			}
			if _, err := os.Stat(filepath.Join(resB.Root, rel("new.md"))); err != nil {
				t.Errorf("the file B added is not in B's tree: %v", err)
			}
			if resB.Root == resA.Root {
				t.Errorf("B was staged from A's directory %s", resA.Root)
			}
			if _, err := os.Stat(filepath.Join(resA.Root, rel("old.md"))); err != nil {
				t.Errorf("moving to B changed A's tree: %v", err)
			}
			if _, err := os.Stat(filepath.Join(store.mirrorPath(a.Repo), "index")); !os.IsNotExist(err) {
				t.Errorf("a checkout wrote the mirror's shared index (stat: %v)", err)
			}
		})
	}
}

// TWO SUBDIRECTORIES OF ONE COMMIT DO NOT SHARE A TREE. Each tree now holds one
// subdirectory, so a second pack of a monorepo at the same commit must get its own checkout
// rather than find the first one's tree complete and its own directory missing.
func TestMaterializeTwoSubdirectoriesOfOneCommit(t *testing.T) {
	repo := gitRepo(t, map[string]string{"one/pack.json": `{"name":"one"}`, "two/pack.json": `{"name":"two"}`})
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	outs, err := store.Refresh([]RefreshPack{
		{Name: "one", Source: "git+file://" + repo + "//one?ref=main"},
		{Name: "two", Source: "git+file://" + repo + "//two?ref=main"},
	}, RefreshOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, sub := range []string{"one", "two"} {
		if outs[i].Err != nil {
			t.Fatalf("pack %s: %v", sub, outs[i].Err)
		}
		a := mustParse(t, "git+file://"+repo+"//"+sub+"?ref=main")
		res, err := store.Resolve(a, sub)
		if err != nil {
			t.Fatalf("pack %s: %v", sub, err)
		}
		if got, want := treeFiles(t, store.treeDir(a, res.Commit)), []string{sub + "/pack.json"}; !slices.Equal(got, want) {
			t.Errorf("pack %s's tree holds %v, want %v", sub, got, want)
		}
	}
}

// THE FETCH BRINGS THE SUBDIRECTORY'S FILE CONTENTS AND NO OTHER. The mirror is a blobless
// partial clone, so file contents arrive when a checkout needs them; a checkout of `.` needed
// every one in the commit. The source repository has to allow the filter
// (uploadpack.allowFilter): without it git ignores --filter over file:// and sends everything,
// which is what every other test here gets.
func TestRefreshFetchesOnlyTheSubdirectorysContents(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		"sub/pack.json":   `{"name":"p"}`,
		"big/one.bin":     "a large sibling\n",
		"big/two.bin":     "another\n",
		"README.root.txt": "a root file\n",
	})
	gitIn(t, repo, "config", "uploadpack.allowFilter", "true")
	source := "git+file://" + repo + "//sub?ref=main"
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	if o := refreshOne(t, store, source, time.Unix(1_800_000_000, 0)); o.Err != nil {
		t.Fatal(o.Err)
	}
	missing := map[string]bool{}
	mirror := store.mirrorPath(mustParse(t, source).Repo)
	for _, line := range strings.Split(gitIn(t, mirror, "rev-list", "--objects", "--all", "--missing=print"), "\n") {
		if oid, ok := strings.CutPrefix(line, "?"); ok {
			missing[oid] = true
		}
	}
	for _, path := range []string{"big/one.bin", "big/two.bin", "README.root.txt"} {
		if oid := gitIn(t, repo, "rev-parse", "HEAD:"+path); !missing[oid] {
			t.Errorf("%s, outside the pack, was downloaded into the mirror", path)
		}
	}
	if oid := gitIn(t, repo, "rev-parse", "HEAD:sub/pack.json"); missing[oid] {
		t.Errorf("the pack's own file was never downloaded")
	}
}

// lazyFetchesIn counts the fetches git started ON ITS OWN in a GIT_TRACE log: the one-blob
// fetches a checkout of a partial mirror spawns for each file it lacks. A fetch the store runs
// itself is a top-level command (`built-in:`), never a `run_command:` line.
func lazyFetchesIn(t *testing.T, trace string) int {
	t.Helper()
	data, err := os.ReadFile(trace)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "run_command:") && strings.Contains(line, " fetch origin ") {
			n++
		}
	}
	return n
}

// A CHECKOUT FETCHES THE FILES IT NEEDS IN ONE REQUEST. git's pathspec checkout of a partial
// mirror fetches each missing file by itself, one process and one connection apiece, which
// against a real remote costs about a quarter of a second a file (prefetchBlobs has the
// measurement). The store fetches them first, in one batch, so the checkout starts none of its
// own. The CONTROL runs the same checkout on a plain clone and must see one lazy fetch per
// file, so a zero below cannot come from a trace that stopped recording them. The
// subdirectory's name carries a space and a glob class, which the prefetch's path lookup must
// take literally, and its sibling must still not be downloaded.
func TestCheckoutFetchesTheFilesItNeedsInOneRequest(t *testing.T) {
	const sub = "pack [v2]"
	files := map[string]string{"elsewhere/big.bin": "not the pack's\n"}
	for _, name := range []string{"pack.json", "a.md", "b.md", "skills/s/SKILL.md", "skills/t/SKILL.md"} {
		files[sub+"/"+name] = name + " of the pack\n"
	}
	repo := gitRepo(t, files)
	gitIn(t, repo, "config", "uploadpack.allowFilter", "true")
	head := gitIn(t, repo, "rev-parse", "HEAD")

	control := filepath.Join(t.TempDir(), "control.git")
	gitIn(t, "", "clone", "-q", "--bare", "--filter=blob:none", "file://"+repo, control)
	controlTrace := filepath.Join(t.TempDir(), "control.trace")
	// The checkout checkoutTree runs, fsckArgs included: those are what allow a store run to
	// fetch on demand at all (gitCmd).
	cmd := (&Store{Env: append(os.Environ(), "GIT_TRACE="+controlTrace)}).gitCmd(t.Context(), control,
		withFsck("--work-tree="+t.TempDir(), "checkout", "--force", head, "--", literalPathspec(sub))...)
	cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+filepath.Join(t.TempDir(), "index"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("control checkout: %v\n%s", err, out)
	}
	if n := lazyFetchesIn(t, controlTrace); n != 5 {
		t.Fatalf("control: a lazy checkout of 5 files started %d fetches of its own, want 5 — the "+
			"trace no longer shows what this test counts", n)
	}

	for _, tc := range []struct{ name, suffix, want string }{
		{"a subdirectory pack", "//" + sub, sub + "/pack.json"},
		{"a repository-root pack", "", "elsewhere/big.bin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := filepath.Join(t.TempDir(), "store.trace")
			store := &Store{Dir: t.TempDir(), Getenv: noStagedTree,
				Env: append(os.Environ(), "GIT_TRACE="+trace)}
			source := "git+file://" + repo + tc.suffix + "?ref=main"
			if o := refreshOne(t, store, source, time.Unix(1_800_000_000, 0)); o.Err != nil {
				t.Fatal(o.Err)
			}
			if n := lazyFetchesIn(t, trace); n != 0 {
				t.Errorf("the checkout fetched %d files one at a time, want them all fetched first in one request", n)
			}
			a := mustParse(t, source)
			res, err := store.Resolve(a, "p")
			if err != nil {
				t.Fatal(err)
			}
			if got := treeFiles(t, store.treeDir(a, res.Commit)); !slices.Contains(got, tc.want) {
				t.Errorf("tree = %v, want it to hold %s", got, tc.want)
			}
			if tc.suffix == "" {
				return
			}
			mirror := store.mirrorPath(a.Repo)
			sibling := gitIn(t, repo, "rev-parse", "HEAD:elsewhere/big.bin")
			if !strings.Contains(gitIn(t, mirror, "rev-list", "--objects", "--all", "--missing=print"), "?"+sibling) {
				t.Error("the prefetch downloaded a file outside the pack's subdirectory")
			}
		})
	}
}

// SYMLINKS IN A SUBDIRECTORY PACK. The checkout writes a link as a link, as it always did, and
// the pack loader judges it (packstage's no-escape rule; the config package's
// TestResolvePackRefusesAFetchedSubdirectorysEscapingLinks drives that end to end). What
// changes is the target of a link out of the subdirectory: its sibling is no longer
// materialized, so the link now dangles in the store instead of resolving to a sibling file.
// Either way it leaves the pack, and either way the loader refuses it.
func TestMaterializeWritesLinksAsLinksAndNotTheirTargets(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		"tools/pack/pack.json":   `{"name":"p"}`,
		"tools/pack/real.md":     "real\n",
		"tools/secret/SKILL.md":  "a sibling the pack links to\n",
		"tools/secret/other.md":  "and one it does not\n",
		"unrelated/whatever.txt": "x\n",
	})
	for link, target := range map[string]string{
		"tools/pack/alias.md":   "real.md",
		"tools/pack/sibling.md": "../secret/SKILL.md",
		"tools/pack/abs.md":     "/etc/hostname",
	} {
		if err := os.Symlink(target, filepath.Join(repo, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "links")

	res, tree := resolveTree(t, "git+file://"+repo+"//tools/pack?ref=main")
	want := []string{
		"tools/pack/abs.md -> /etc/hostname",
		"tools/pack/alias.md -> real.md",
		"tools/pack/pack.json",
		"tools/pack/real.md",
		"tools/pack/sibling.md -> ../secret/SKILL.md",
	}
	if got := treeFiles(t, tree); !slices.Equal(got, want) {
		t.Errorf("tree = %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(res.Root, "sibling.md")); !os.IsNotExist(err) {
		t.Errorf("the link out of the subdirectory resolves in the store (stat: %v); its target "+
			"must not have been checked out", err)
	}
}

// A SUBDIRECTORY THAT IS A SYMLINK, OR IS REACHED THROUGH ONE, IS NO PACK ROOT. Checked out,
// the link itself would be the root: `//linked` pointing at `../..` or an absolute path would
// make a pack of a directory outside the store. So the address is refused before anything is
// written, by what git's trees say, and the refusal names the link, including a link that is
// only ON the way: git has no entry below a link, so `//packs/current/mypack` with
// `packs/current -> v2` would otherwise read as a directory the commit lacks, although every
// full checkout of that commit has it. A submodule is named the same way, as the whole
// subdirectory and on the way to it. The read-only resolution a `yolo check` runs reads the
// same trees and says the same thing.
func TestMaterializeRefusesASymlinkedSubdirectory(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		"real/pack/pack.json":       `{"name":"p"}`,
		"packs/v2/mypack/pack.json": `{"name":"p"}`,
		"afile":                     "not a directory\n",
	})
	for link, target := range map[string]string{
		"linked":        "real/pack",
		"escape":        "/etc",
		"viaparent":     "real",
		"packs/current": "v2",
	} {
		if err := os.Symlink(target, filepath.Join(repo, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "add", "-A")
	// A gitlink: the tree entry a submodule is, mode 160000, naming a commit of another
	// repository (any commit id will do; nothing follows it).
	gitIn(t, repo, "update-index", "--add", "--cacheinfo",
		"160000,"+gitIn(t, repo, "rev-parse", "HEAD")+",gitlinked")
	gitIn(t, repo, "commit", "-qm", "links")
	if mode := strings.Fields(gitIn(t, repo, "ls-tree", "HEAD", "gitlinked"))[0]; mode != "160000" {
		t.Fatalf("fixture: gitlinked has mode %s, want a gitlink", mode)
	}

	for _, tc := range []struct{ sub, want string }{
		{"linked", `pack subdirectory "linked" is a symlink`},
		{"escape", `pack subdirectory "escape" is a symlink`},
		{"viaparent/pack", `pack subdirectory "viaparent/pack" passes through a symlink (viaparent)`},
		{"packs/current/mypack", `pack subdirectory "packs/current/mypack" passes through a symlink (packs/current)`},
		{"gitlinked", `pack subdirectory "gitlinked" is a submodule`},
		{"gitlinked/pack", `pack subdirectory "gitlinked/pack" passes through a submodule (gitlinked)`},
		{"afile", `pack subdirectory "afile" is a file`},
		{"afile/pack", `pack subdirectory "afile/pack" not found`},
		{"nosuch", `pack subdirectory "nosuch" not found`},
		{"packs/nosuch/deeper", `pack subdirectory "packs/nosuch/deeper" not found`},
	} {
		store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
		source := "git+file://" + repo + "//" + tc.sub + "?ref=main"
		o := refreshOne(t, store, source, time.Unix(1_800_000_000, 0))
		if o.Err == nil || !strings.Contains(o.Err.Error(), tc.want) {
			t.Errorf("//%s: err = %v, want %q", tc.sub, o.Err, tc.want)
		}
		if trees := treesIn(t, store); len(trees) != 0 {
			t.Errorf("//%s: a refused subdirectory left trees behind: %v", tc.sub, trees)
		}
		if _, err := store.ResolveExisting(mustParse(t, source), "p"); err == nil ||
			errors.Is(err, ErrNotCheckedOut) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("//%s: the read-only resolution says %v, want %q", tc.sub, err, tc.want)
		}
	}
}

// THE TREE ON DISK IS HELD TO THE SAME LINE. Whatever made a tree (an older yolo, a hand
// edit), a pack root reached through a symlink is refused rather than resolved to wherever
// the link points.
func TestTreeResolvedRefusesASymlinkOnThePath(t *testing.T) {
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	commit := strings.Repeat("a", 40)
	a := mustParse(t, "git+https://example.invalid/o/r//tools/pack?ref="+commit)
	tree := store.treeDir(a, commit)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tree, "tools")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, treeCompleteMarker), []byte(commit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := store.Materialize(a, commit)
	if err == nil || !strings.Contains(err.Error(), "passes through a symlink (tools)") {
		t.Errorf("Materialize through a linked component = %+v, %v; want it refused", res, err)
	}
}

// A READ-ONLY RESOLUTION TELLS A MISSING SUBDIRECTORY FROM A TREE NOT CHECKED OUT YET. Each
// subdirectory has a tree of its own now, so "no tree" no longer implies the commit was never
// checked out for this pack; `yolo check` reports ErrNotCheckedOut as the next launch's to
// repair, and a subdirectory the commit lacks is not: that launch fails on it.
func TestResolveExistingNamesAMissingSubdirectory(t *testing.T) {
	repo := gitRepo(t, map[string]string{"sub/pack.json": `{"name":"p"}`})
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	present := mustParse(t, "git+file://"+repo+"//sub?ref=main")
	if _, err := store.Sync(present); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveExisting(present, "p"); !errors.Is(err, ErrNotCheckedOut) {
		t.Errorf("a subdirectory the commit has, not checked out: err = %v, want ErrNotCheckedOut", err)
	}
	absent := mustParse(t, "git+file://"+repo+"//nosuch?ref=main")
	_, err := store.ResolveExisting(absent, "p")
	if err == nil || errors.Is(err, ErrNotCheckedOut) || !strings.Contains(err.Error(), `"nosuch" not found`) {
		t.Errorf("a subdirectory the commit lacks: err = %v, want it named as not found", err)
	}
	if trees := treesIn(t, store); len(trees) != 0 {
		t.Errorf("ResolveExisting checked out %v", trees)
	}
}
