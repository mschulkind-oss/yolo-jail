package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestScopedSameBranchAdvanceIsLockFirstAndPureReadsUseRecordedPin(t *testing.T) {
	home := readablePackTestHome(t)
	repo := t.TempDir()
	configPackGit(t, repo, "init", "-q", "--initial-branch=main")
	write(t, filepath.Join(repo, "corpus/skills/chosen/SKILL.md"), "first\n")
	configPackGit(t, repo, "add", "--all")
	configPackGit(t, repo, "commit", "-qm", "first")
	first := configPackGit(t, repo, "rev-parse", "HEAD")
	root := filepath.Join(home, ".config/yolo-jail")
	write(t, filepath.Join(root, "config.toml"), "packs=[{pack='demo/corpus',skills={only=['chosen']}}]\n[pack_sources.demo]\ngit='file://"+filepath.ToSlash(repo)+"'\nref='main'\n")
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) > 0 || len(entries) != 1 {
		t.Fatalf("producer: %+v %v %v", entries, problems, err)
	}
	entry := entries[0]
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	now := time.Unix(1700000000, 0)
	refresh := func() {
		t.Helper()
		out, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: packsrc.LockPath(paths.UserConfigPath()), Force: true, Now: func() time.Time { return now }})
		if err != nil || len(out) != 1 || out[0].Err != nil {
			t.Fatalf("refresh: %+v %v", out, err)
		}
	}
	refresh()
	lockPath := packsrc.LockPath(paths.UserConfigPath())
	lockBytes, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	beforeMissingPin := explicitEditTreeSnapshot(t, paths.PacksDir(), lockPath)
	if _, err := ResolvePack(entry, ResolvePackSpec{ReadOnlyStore: true, Getenv: func(string) string { return "" }}); !errors.Is(err, packsrc.ErrDefaultRefUnavailable) {
		t.Fatalf("cached branch without recorded pin floated pure selection: %v", err)
	}
	if after := explicitEditTreeSnapshot(t, paths.PacksDir(), lockPath); after != beforeMissingPin {
		t.Fatal("missing-pin inspection wrote store/lock")
	}
	if err := os.WriteFile(lockPath, lockBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repo, "corpus/skills/chosen"), filepath.Join(repo, "corpus/skills/changed")); err != nil {
		t.Fatal(err)
	}
	configPackGit(t, repo, "add", "--all")
	configPackGit(t, repo, "commit", "-qm", "advance same branch")
	second := configPackGit(t, repo, "rev-parse", "HEAD")
	// Updating the mirror without recording this handle must not float a pure scoped
	// read off its last recorded pin. An ordinary refresh then advances it lock-first.
	if out, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{Force: true, Now: func() time.Time { return now }}); err != nil || len(out) != 1 || out[0].Err != nil {
		t.Fatalf("mirror-only refresh: %+v %v", out, err)
	}
	before := explicitEditTreeSnapshot(t, paths.PacksDir(), filepath.Join(root, "packs.lock.json"))
	resolved, err := ResolvePack(entry, ResolvePackSpec{ReadOnlyStore: true, Getenv: func(string) string { return "" }})
	if err != nil || resolved.Pack.Projection.Commit != first {
		t.Fatalf("pure read floated before lock advance: %+v %v", resolved, err)
	}
	if after := explicitEditTreeSnapshot(t, paths.PacksDir(), filepath.Join(root, "packs.lock.json")); before != after {
		t.Fatal("pure scoped pin read wrote")
	}
	now = now.Add(2 * time.Hour)
	refresh()
	lock, err := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
	if err != nil {
		t.Fatal(err)
	}
	pin, ok := lock.Get(entry.Name)
	if !ok || pin.Commit != second {
		t.Fatalf("same branch pin did not advance: %+v", pin)
	}
	_, err = ResolvePack(entry, ResolvePackSpec{ReadOnlyStore: true, Getenv: func(string) string { return "" }})
	if err == nil || !strings.Contains(err.Error(), second) || !strings.Contains(err.Error(), "chosen") || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("advanced selector diagnostic: %v", err)
	}
}
