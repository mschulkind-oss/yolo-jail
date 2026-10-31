package configmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const pinnedCommit = "0123456789abcdef0123456789abcdef01234567"

// renamedPackFixture is a user config selecting a path pack whose readable handle renames it
// (`~/code/yolo-packs/pi-fork` is the pack `pi-fork`; `yolo-packs/pi-fork` is the pack
// `yolo-packs/pi-fork`), with the state a launch leaves under the old name: the pack's
// packs.lock.json entry, its fork's pin in forks.lock.json, and its patched fork's check record
// holding a good build.
func renamedPackFixture(t *testing.T) (dir, ws string) {
	t.Helper()
	home := env(t)
	dir = filepath.Join(home, ".config", "yolo-jail")
	packDir := filepath.Join(home, "code", "yolo-packs", "pi-fork")
	write(t, filepath.Join(packDir, "pack.toml"), "name = \"pi-fork\"\n")
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["~/code/yolo-packs/pi-fork"]}`+"\n")
	ws = realDir(t)
	userConfig := filepath.Join(dir, "config.jsonc")
	store := paths.PacksDir()
	if err := packsrc.WithLock(store, packsrc.LockPath(userConfig), nil, func(l *packsrc.Lock) (bool, error) {
		l.Set(packsrc.LockEntry{Name: "pi-fork", Source: "file://" + packDir})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := packsrc.WithForkLock(store, packsrc.ForkLockPath(userConfig), nil, func(l *packsrc.ForkLock) (bool, error) {
		l.Set(packsrc.ForkLockEntry{Key: "pi-fork/pi", Source: "git+https://example.invalid/pi?ref=main", Ref: "main", Commit: pinnedCommit})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := (&packsrc.Store{Dir: store}).WithCheckRecord("pi-fork/pi-patched", nil,
		func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
			r.Good = &packsrc.GoodBuild{Commit: pinnedCommit, Series: "s", Recipe: "r", Entry: "e"}
			return true, nil
		}); err != nil {
		t.Fatal(err)
	}
	return dir, ws
}

// assertStateFollowsTheNewName reads the state the way the next launch does, under the name the
// converted config now gives the pack.
func assertStateFollowsTheNewName(t *testing.T, dir string) {
	t.Helper()
	entries, err := config.LoadPacks(func(string) {})
	if err != nil || len(entries) != 1 {
		t.Fatalf("LoadPacks after the migration: %v %v", entries, err)
	}
	name := entries[0].Name
	if name != "yolo-packs/pi-fork" {
		t.Fatalf("the pack is named %q after the migration, want yolo-packs/pi-fork", name)
	}
	userConfig := filepath.Join(dir, paths.UserConfigTOMLName)
	lock, err := packsrc.LoadLock(packsrc.LockPath(userConfig))
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := lock.Get(name); !ok || e.Name != name {
		t.Errorf("packs.lock.json has no entry for %s after the migration: %+v", name, lock.Packs)
	}
	if _, ok := lock.Get("pi-fork"); ok {
		t.Errorf("packs.lock.json still holds the old name: %+v", lock.Packs)
	}
	fork := packload.Fork{Pack: name, Bin: "pi", Source: "git+https://example.invalid/pi?ref=main"}
	pins := packload.LoadForkPins([]packload.Fork{fork}, packsrc.ForkLockPath(userConfig))
	if len(pins) != 1 || pins[0].Commit != pinnedCommit {
		t.Errorf("fork %s lost its pin across the rename (the next launch re-pins it): %+v", fork.Key(), pins)
	}
	r, err := (&packsrc.Store{Dir: paths.PacksDir()}).LoadCheckRecord(name + "/pi-patched")
	if err != nil || r.Good == nil || r.Good.Commit != pinnedCommit {
		t.Errorf("the patched fork's check record did not follow the rename (the next launch re-checks and rebuilds): %v %+v", err, r)
	}
}

// MAJOR 1 of the review of 825892281: converting a path pack to its readable handle renames the
// pack, and every record keyed by the old name read as never made. The migration moves them.
func TestARenamedPackKeepsItsPinsAndCheckRecord(t *testing.T) {
	dir, ws := renamedPackFixture(t)
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if len(p.Renames) != 1 || p.Renames[0].Old != "pi-fork" || p.Renames[0].New != "yolo-packs/pi-fork" {
		t.Fatalf("the plan's renames = %+v, want pi-fork → yolo-packs/pi-fork", p.Renames)
	}
	if !strings.Contains(p.Transcript(), "pi-fork → yolo-packs/pi-fork") {
		t.Errorf("the transcript does not name the rename:\n%s", p.Transcript())
	}
	res, err := Apply(p, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PackRecords) != 3 {
		t.Errorf("moved records = %v, want the pack lock entry, the fork pin and the check record", res.PackRecords)
	}
	assertStateFollowsTheNewName(t, dir)
}

// MINOR 6: a migration killed part way finishes the records too when it is resumed, because the
// journal carries the record plan.
func TestAResumedMigrationMovesTheRenamedPacksRecords(t *testing.T) {
	dir, ws := renamedPackFixture(t)
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if _, err := Apply(p, ApplyOptions{StopAfter: func(int, string) bool { return true }}); err == nil {
		t.Fatal("the seam did not stop the migration")
	}
	journal := Interrupted(Request{UserConfigDir: dir, Workspace: ws})
	if journal == "" {
		t.Fatal("no journal after the stop")
	}
	res, err := Resume(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PackRecords) != 3 {
		t.Errorf("Resume moved %v, want the pack lock entry, the fork pin and the check record", res.PackRecords)
	}
	if _, err := os.Lstat(journal); err == nil {
		t.Error("the journal outlived the resumed migration")
	}
	assertStateFollowsTheNewName(t, dir)
}
