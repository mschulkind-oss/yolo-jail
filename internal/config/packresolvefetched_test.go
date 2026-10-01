package config

// packresolvefetched_test.go pins what the pack resolver does with the symlinks of a FETCHED
// pack in a SUBDIRECTORY of its repository, now that the store checks out only that
// subdirectory (packsrc's literal-pathspec checkout). The resolver's guarantee is unchanged: a
// fetched pack's link must land inside the pack, or the pack is refused (packstage's no-escape
// rule; followsSymlinks is false for every fetched pack). What changed underneath is where a
// link out of the subdirectory points: its sibling is no longer checked out, so the link
// dangles in the store instead of reaching a real file, and the sibling's contents are never
// on disk at all.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// fetchedSubdirPack makes a real git repository with a pack at tools/pack (a manifest and one
// skill), a sibling directory tools/secret holding a file the pack must never reach, and one
// symlink at tools/pack/<link> pointing at target; fetches it into this HOME's pack store as
// `yolo pack install` does; and returns the pack's entry.
func fetchedSubdirPack(t *testing.T, link, target string) PackEntry {
	t.Helper()
	repo := gitPackRepo(t, map[string]string{
		"tools/pack/pack.json":           `{"name":"sub"}`,
		"tools/pack/skills/s/SKILL.md":   "---\nname: s\ndescription: d\n---\nthe pack's own skill\n",
		"tools/secret/SKILL.md":          "---\nname: leak\ndescription: d\n---\nSIBLING SECRET\n",
		"tools/secret/credentials.token": "SIBLING SECRET\n",
	}, map[string]string{"tools/pack/" + link: target})
	source := "git+file://" + repo + "//tools/pack?ref=main"
	syncFetchedPack(t, source)
	return PackEntry{Source: source, Name: "sub"}
}

// gitPackRepo makes a real git repository with files (path -> contents) and the symlinks links
// (path -> target), all committed on main, and returns its directory.
func gitPackRepo(t *testing.T, files, links map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	for rel, body := range files {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for rel, target := range links {
		lp := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(lp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, lp); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-qm", "initial")
	return repo
}

// syncFetchedPack fetches source into this HOME's pack store, as `yolo pack install` does.
func syncFetchedPack(t *testing.T, source string) packsrc.Addr {
	t.Helper()
	addr, err := packsrc.Parse(source)
	if err != nil {
		t.Skipf("the pack grammar does not accept a local git transport: %v", err)
	}
	if _, err := (&packsrc.Store{Dir: paths.PacksDir()}).Sync(addr); err != nil {
		t.Fatalf("fetching the pack into the store: %v", err)
	}
	return addr
}

// noPackRoot is the host's environment for the store: no YOLO_PACK_ROOT, so the staged-tree
// fallback cannot answer for the pack (this repo is developed inside a jail, where it is set).
func noPackRoot(string) string { return "" }

// storeHolds reports whether any file under the pack store's trees contains needle.
func storeHolds(t *testing.T, needle string) bool {
	t.Helper()
	found := false
	_ = filepath.Walk(filepath.Join(paths.PacksDir(), "trees"), func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		if b, _ := os.ReadFile(p); strings.Contains(string(b), needle) {
			found = true
		}
		return nil
	})
	return found
}

// A FETCHED SUBDIRECTORY PACK'S LINK OUT OF THE PACK IS REFUSED, whether it names a sibling in
// the same repository or an absolute host path, in both resolver modes; the sibling's
// contents never reach the store; and a link that stays inside the pack is still delivered,
// as a plain file, so the refusals are not a resolver that refuses every link.
func TestResolvePackRefusesAFetchedSubdirectorysEscapingLinks(t *testing.T) {
	hostFile := filepath.Join(t.TempDir(), "host-secret.txt")
	if err := os.WriteFile(hostFile, []byte("HOST SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, link, target string
		refused            bool
	}{
		{"a sibling of the subdirectory", "skills/leak/SKILL.md", "../../../secret/SKILL.md", true},
		{"a sibling, by a link at the pack root", "credentials.token", "../secret/credentials.token", true},
		{"an absolute host path", "skills/abs/SKILL.md", hostFile, true},
		{"a file inside the pack", "skills/alias/SKILL.md", "../s/SKILL.md", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useProfileKeysHome(t)
			entry := fetchedSubdirPack(t, tc.link, tc.target)
			for _, dest := range []string{"", filepath.Join(t.TempDir(), "sub")} {
				res, err := ResolvePack(entry, ResolvePackSpec{Dest: dest, Getenv: noPackRoot})
				if tc.refused {
					if err == nil || !strings.Contains(err.Error(), "symlink pointing outside the pack") {
						t.Errorf("dest %q: err = %v, want the link refused as leaving the pack", dest, err)
					}
				} else if err != nil || res.Pack == nil {
					t.Errorf("dest %q: an in-pack link = %v, want the pack resolved", dest, err)
				}
				if dest == "" {
					continue
				}
				staged, _ := os.ReadFile(filepath.Join(dest, filepath.FromSlash(tc.link)))
				if strings.Contains(string(staged), "SECRET") {
					t.Errorf("dest %q: the link delivered %q", dest, staged)
				}
				if !tc.refused {
					fi, err := os.Lstat(filepath.Join(dest, filepath.FromSlash(tc.link)))
					if err != nil || !fi.Mode().IsRegular() || !strings.Contains(string(staged), "the pack's own skill") {
						t.Errorf("an in-pack link was staged as %v (%v): %q, want a plain copy of its target", fi, err, staged)
					}
				}
			}
			if storeHolds(t, "SIBLING SECRET") {
				t.Error("the subdirectory's sibling was checked out into the pack store")
			}
		})
	}
}

// AN ADDRESS THROUGH A LINK IN ITS REPOSITORY NEVER READS THE DIRECTORY THE LINK NAMES. A
// repository holding `packs -> <a directory on this machine>` and the address `//packs/mypack`
// made 0.11.0 stage that directory's `mypack` into the jail: its whole-commit checkout wrote
// the link into the store, and resolving the pack root followed it. The address is refused now,
// naming the link, in each mode the resolver has (the declaration read, the read-only store
// read that `yolo check` and validation use, and a launch's stage), and nothing of the host
// directory reaches a staged tree. The second round runs with the whole-commit checkout that
// earlier yolo left in the store, link and all, because a read-only resolution may answer from
// such a tree.
func TestResolvePackRefusesAnAddressThroughALinkToAHostDirectory(t *testing.T) {
	useProfileKeysHome(t)
	host := t.TempDir()
	for rel, body := range map[string]string{
		"mypack/pack.json":      `{"name":"sub"}`,
		"mypack/HOSTSECRET.txt": "HOST SECRET\n",
	} {
		p := filepath.Join(host, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repo := gitPackRepo(t, map[string]string{"README.md": "a repository\n"}, map[string]string{"packs": host})
	source := "git+file://" + repo + "//packs/mypack?ref=main"
	syncFetchedPack(t, source)
	entry := PackEntry{Source: source, Name: "sub"}

	for _, round := range []string{"no earlier checkout", "an earlier whole-commit checkout"} {
		if round != "no earlier checkout" {
			// The tree an earlier yolo left: the whole commit at trees/<commit>. A
			// repository-root checkout of the same commit is exactly that tree.
			whole, err := (&packsrc.Store{Dir: paths.PacksDir(), Getenv: noPackRoot}).Resolve(
				syncFetchedPack(t, "git+file://"+repo+"?ref=main"), "whole")
			if err != nil {
				t.Fatal(err)
			}
			if fi, err := os.Lstat(filepath.Join(whole.Root, "packs")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("fixture: the whole-commit tree has no link at packs (%v, %v)", fi, err)
			}
		}
		for _, mode := range []struct {
			name string
			spec ResolvePackSpec
		}{
			{"declaration", ResolvePackSpec{Getenv: noPackRoot}},
			{"read-only", ResolvePackSpec{Getenv: noPackRoot, ReadOnlyStore: true}},
			{"stage", ResolvePackSpec{Getenv: noPackRoot, Dest: filepath.Join(t.TempDir(), "sub")}},
		} {
			res, err := ResolvePack(entry, mode.spec)
			if err == nil || !strings.Contains(err.Error(), `"packs/mypack" passes through a symlink (packs)`) {
				t.Errorf("%s, %s: ResolvePack = %+v, %v; want the address refused, naming the link",
					round, mode.name, res, err)
			}
			if mode.spec.Dest == "" {
				continue
			}
			_ = filepath.Walk(mode.spec.Dest, func(p string, fi os.FileInfo, err error) error {
				if err == nil && fi.Mode().IsRegular() {
					if b, _ := os.ReadFile(p); strings.Contains(string(b), "HOST SECRET") {
						t.Errorf("%s: the host directory's file was staged at %s", round, p)
					}
				}
				return nil
			})
		}
	}
}
