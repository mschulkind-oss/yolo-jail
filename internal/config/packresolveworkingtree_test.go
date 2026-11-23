package config

// packresolveworkingtree_test.go pins the CALL SITE of the working-tree staging
// (docs/design/local-pack-clones.md §6.4-§6.5, LPC-D14, slice 6): config.ResolvePack, the one resolver every
// notch and verb stages through, reads a `local` pack's launch record and stages the clone's files. The record is
// left by a real packsrc.Store.Refresh; deleting ResolvePack's consultation of it, or the record, fails these.

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

// localClonePack makes an origin repository holding a pack, a clone of it with an untracked file, and the launch
// record a real Refresh leaves for an entry that names the clone; it returns the entry and the clone.
func localClonePack(t *testing.T, link, target string) (PackEntry, string) {
	t.Helper()
	files := map[string]string{"pack.json": `{"name":"loc"}`}
	links := map[string]string{}
	if link != "" {
		links[link] = target
	}
	origin := gitPackRepo(t, files, links)
	clone := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", "-q", origin, clone)
	cmd.Env = testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ()))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(clone, "note.md"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := "git+file://" + origin + "?ref=main"
	entry := PackEntry{Source: source, Name: "loc", Local: clone}
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: noPackRoot}
	outs, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: packsrc.LockPath(paths.UserConfigPath())})
	if err != nil || outs[0].Err != nil || outs[0].Local == nil || outs[0].Local.Copy != packsrc.CopyWorkingTree {
		t.Fatalf("refresh = %+v, %v: want a decision that runs the working tree", outs, err)
	}
	return entry, clone
}

func TestResolvePackStagesALocalClonesWorkingTree(t *testing.T) {
	useProfileKeysHome(t)
	entry, _ := localClonePack(t, "", "")
	dest := filepath.Join(t.TempDir(), "loc")
	res, err := ResolvePack(entry, ResolvePackSpec{Dest: dest, Getenv: noPackRoot})
	if err != nil {
		t.Fatal(err)
	}
	if res.WorkingTree == nil || res.WorkingTreeFallback != "" || res.Commit != "" {
		t.Errorf("WorkingTree %+v fallback %q commit %q, want the working tree, no fallback, no commit",
			res.WorkingTree, res.WorkingTreeFallback, res.Commit)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "note.md")); err != nil || string(b) != "uncommitted" {
		t.Errorf("the untracked file did not reach the staged tree: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		t.Errorf(".git was staged")
	}
	// Declaration mode and the read-only resolver agree.
	for _, spec := range []ResolvePackSpec{{Getenv: noPackRoot}, {Getenv: noPackRoot, ReadOnlyStore: true}} {
		r, err := ResolvePack(entry, spec)
		if err != nil || r.WorkingTree == nil {
			t.Errorf("spec %+v: %+v, %v, want the working tree too", spec, r.WorkingTree, err)
		}
	}
}

// Without a record the same entry stages origin's copy: the record is what the resolver reads.
func TestResolvePackWithoutARecordStagesOriginsCopy(t *testing.T) {
	useProfileKeysHome(t)
	entry, _ := localClonePack(t, "", "")
	recs, _ := filepath.Glob(filepath.Join(paths.PacksDir(), "checks", "local-*.json"))
	if len(recs) != 1 {
		t.Fatalf("records = %v, want one", recs)
	}
	if err := os.Remove(recs[0]); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "loc")
	res, err := ResolvePack(entry, ResolvePackSpec{Dest: dest, Getenv: noPackRoot})
	if err != nil {
		t.Fatal(err)
	}
	if res.WorkingTree != nil || res.Commit == "" {
		t.Errorf("WorkingTree %+v commit %q, want origin's copy", res.WorkingTree, res.Commit)
	}
	if _, err := os.Stat(filepath.Join(dest, "note.md")); err == nil {
		t.Errorf("the clone's untracked file reached a pack the record does not run")
	}
}

// A link that leaves the pack is not followed: origin's held copy runs and the resolution says why.
func TestResolvePackFallsToOriginForAnEscapingLink(t *testing.T) {
	useProfileKeysHome(t)
	entry, clone := localClonePack(t, "", "")
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("HOST SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(clone, "leak")); err != nil {
		t.Fatal(err)
	}
	// The record was taken before the link: this launch reads the clone as it is now, at staging.
	dest := filepath.Join(t.TempDir(), "loc")
	res, err := ResolvePack(entry, ResolvePackSpec{Dest: dest, Getenv: noPackRoot})
	if err != nil {
		t.Fatal(err)
	}
	if res.WorkingTree != nil || !strings.Contains(res.WorkingTreeFallback, "leak") {
		t.Errorf("WorkingTree %+v fallback %q, want origin's copy and a fallback naming the link", res.WorkingTree, res.WorkingTreeFallback)
	}
	if _, err := os.Lstat(filepath.Join(dest, "leak")); err == nil {
		t.Errorf("the outward link was staged")
	}
}
