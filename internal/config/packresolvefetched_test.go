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
)

// fetchedSubdirPack makes a real git repository with a pack at tools/pack (a manifest and one
// skill), a sibling directory tools/secret holding a file the pack must never reach, and one
// symlink at tools/pack/<link> pointing at target; fetches it into this HOME's pack store as
// `yolo pack install` does; and returns the pack's entry.
func fetchedSubdirPack(t *testing.T, link, target string) PackEntry {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	for rel, body := range map[string]string{
		"tools/pack/pack.json":           `{"name":"sub"}`,
		"tools/pack/skills/s/SKILL.md":   "---\nname: s\ndescription: d\n---\nthe pack's own skill\n",
		"tools/secret/SKILL.md":          "---\nname: leak\ndescription: d\n---\nSIBLING SECRET\n",
		"tools/secret/credentials.token": "SIBLING SECRET\n",
	} {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lp := filepath.Join(repo, "tools", "pack", filepath.FromSlash(link))
	if err := os.MkdirAll(filepath.Dir(lp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, lp); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "initial")

	source := "git+file://" + repo + "//tools/pack?ref=main"
	addr, err := packsrc.Parse(source)
	if err != nil {
		t.Skipf("the pack grammar does not accept a local git transport: %v", err)
	}
	if _, err := (&packsrc.Store{Dir: paths.PacksDir()}).Sync(addr); err != nil {
		t.Fatalf("fetching the pack into the store: %v", err)
	}
	return PackEntry{Source: source, Name: "sub"}
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
