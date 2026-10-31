package configmigrate

// identities_test.go drives ReconcilePackRecords (docs/design/pack-naming.md §3.2, PN-D10, PN-D15)
// over records a launch left under one handle.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// identityFixture is a fake user config dir holding a pack lock entry, a fork pin and a check
// record for the handle acme/x, and the identity of the directory that pack is read from.
func identityFixture(t *testing.T) (dir, identity string) {
	t.Helper()
	home := env(t)
	dir = filepath.Join(home, ".config", "yolo-jail")
	packDir := filepath.Join(home, "src", "x")
	write(t, filepath.Join(packDir, "pack.toml"), "name = \"x\"\n")
	userConfig := filepath.Join(dir, paths.UserConfigTOMLName)
	store := paths.PacksDir()
	if err := packsrc.WithLock(store, packsrc.LockPath(userConfig), nil, func(l *packsrc.Lock) (bool, error) {
		l.Set(packsrc.LockEntry{Name: "acme/x", Source: "file://" + packDir})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := packsrc.WithForkLock(store, packsrc.ForkLockPath(userConfig), nil, func(l *packsrc.ForkLock) (bool, error) {
		l.Set(packsrc.ForkLockEntry{Key: "acme/x/pi", Source: "git+https://example.invalid/pi?ref=main", Ref: "main", Commit: pinnedCommit})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := (&packsrc.Store{Dir: store}).WithCheckRecord("acme/x/pi-patched", nil,
		func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
			r.Good = &packsrc.GoodBuild{Commit: pinnedCommit, Series: "s", Recipe: "r", Entry: "e"}
			return true, nil
		}); err != nil {
		t.Fatal(err)
	}
	return dir, packsrc.PackIdentity("", "", "file://"+packDir)
}

func recordsUnder(t *testing.T, dir, handle string) (lock, pin, check bool) {
	t.Helper()
	userConfig := filepath.Join(dir, paths.UserConfigTOMLName)
	l, err := packsrc.LoadLock(packsrc.LockPath(userConfig))
	if err != nil {
		t.Fatal(err)
	}
	_, lock = l.Get(handle)
	f, err := packsrc.LoadForkLock(packsrc.ForkLockPath(userConfig))
	if err != nil {
		t.Fatal(err)
	}
	e, pin := f.Forks[handle+"/pi"]
	pin = pin && e.Commit == pinnedCommit
	r, err := (&packsrc.Store{Dir: paths.PacksDir()}).LoadCheckRecord(handle + "/pi-patched")
	check = err == nil && r.Good != nil && r.Good.Commit == pinnedCommit
	return lock, pin, check
}

// The first read after the index exists adopts the records already under each handle (the
// migration of handle-keyed records) and moves nothing; a second read changes nothing either.
func TestTheFirstReadAdoptsHandleKeyedRecordsAndIsIdempotent(t *testing.T) {
	dir, id := identityFixture(t)
	if id == "" || !strings.HasPrefix(id, "dir:") {
		t.Fatalf("identity of a local pack = %q, want dir:<path>", id)
	}
	for i := 0; i < 2; i++ {
		lines, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "acme/x", Identity: id}})
		if err != nil || len(lines) != 0 {
			t.Fatalf("read %d: lines=%v err=%v; want nothing moved", i, lines, err)
		}
	}
	if lock, pin, check := recordsUnder(t, dir, "acme/x"); !lock || !pin || !check {
		t.Errorf("the adopted records moved: lock=%v pin=%v check=%v", lock, pin, check)
	}
	idx, err := packsrc.LoadIdentityIndex(filepath.Join(dir, packsrc.IdentityIndexName))
	if err != nil || idx.Packs[id] != "acme/x" {
		t.Errorf("the index does not record acme/x for %s: %+v %v", id, idx, err)
	}
}

// A renamed handle (a source key renamed by hand or by `yolo pack source rename`) finds its pins,
// fork pins and check records under the new handle at the next read. Fails if the move is deleted.
func TestARenamedHandleFindsItsRecords(t *testing.T) {
	dir, id := identityFixture(t)
	if _, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "acme/x", Identity: id}}); err != nil {
		t.Fatal(err)
	}
	lines, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "tools/x", Identity: id}})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "pack tools/x was acme/x") {
		t.Errorf("the read does not say the records followed the rename: %v", lines)
	}
	if lock, pin, check := recordsUnder(t, dir, "tools/x"); !lock || !pin || !check {
		t.Errorf("the records did not follow the rename: lock=%v pin=%v check=%v", lock, pin, check)
	}
	if lock, pin, check := recordsUnder(t, dir, "acme/x"); lock || pin || check {
		t.Errorf("records stayed under the old handle: lock=%v pin=%v check=%v", lock, pin, check)
	}
	again, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "tools/x", Identity: id}})
	if err != nil || len(again) != 0 {
		t.Errorf("a second read moved something: %v %v", again, err)
	}
}

// A source renamed by hand before any build kept the index: the first read finds the old handle by
// the address its pin was locked for, and moves its records. Fails if that migration is deleted.
func TestARenameBeforeTheIndexExistedIsFoundByTheLockedAddress(t *testing.T) {
	dir, id := identityFixture(t)
	lines, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "tools/x", Identity: id}})
	if err != nil {
		t.Fatal(err)
	}
	if lock, pin, check := recordsUnder(t, dir, "tools/x"); !lock || !pin || !check {
		t.Errorf("the hand-renamed pack's records were not migrated: lock=%v pin=%v check=%v (%v)", lock, pin, check, lines)
	}
}

// A handle that now reads from another place starts its pins over, says so, and the old place's
// records come back if it is selected again.
func TestAMovedPackStartsOverAndItsRecordsWaitForItsReturn(t *testing.T) {
	dir, id := identityFixture(t)
	if _, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "acme/x", Identity: id}}); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(filepath.Dir(dir), "elsewhere")
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatal(err)
	}
	newID := packsrc.PackIdentity("", moved, "")
	lines, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "acme/x", Identity: newID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "start over") {
		t.Errorf("the read does not say the moved pack starts over: %v", lines)
	}
	if lock, pin, check := recordsUnder(t, dir, "acme/x"); lock || pin || check {
		t.Errorf("the moved pack kept the old place's records: lock=%v pin=%v check=%v", lock, pin, check)
	}
	// The old place selected again, under a new handle: its records come back.
	lines, err = ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "acme/x", Identity: newID}, {Handle: "old/x", Identity: id}})
	if err != nil {
		t.Fatal(err)
	}
	if lock, pin, check := recordsUnder(t, dir, "old/x"); !lock || !pin || !check {
		t.Errorf("the returned pack's records did not come back: lock=%v pin=%v check=%v (%v)", lock, pin, check, lines)
	}
}

// Two packs swapping handles each keep their own records.
func TestTwoPacksSwappingHandlesKeepTheirOwnRecords(t *testing.T) {
	dir, id := identityFixture(t)
	other := filepath.Join(filepath.Dir(dir), "y")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	otherID := packsrc.PackIdentity("", other, "")
	if _, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "acme/x", Identity: id}, {Handle: "acme/y", Identity: otherID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "acme/y", Identity: id}, {Handle: "acme/x", Identity: otherID}}); err != nil {
		t.Fatal(err)
	}
	if lock, pin, check := recordsUnder(t, dir, "acme/y"); !lock || !pin || !check {
		t.Errorf("x's records did not follow it to acme/y: lock=%v pin=%v check=%v", lock, pin, check)
	}
	if lock, pin, check := recordsUnder(t, dir, "acme/x"); lock || pin || check {
		t.Errorf("acme/x, now y, holds x's records: lock=%v pin=%v check=%v", lock, pin, check)
	}
}

func TestPackIdentityIgnoresTheRefAndNormalizesTheRepository(t *testing.T) {
	a := packsrc.PackIdentity("", "", "git+https://github.com/acme/packs//tools?ref=main")
	b := packsrc.PackIdentity("", "", "git+ssh://git@github.com/acme/packs.git//tools")
	if a == "" || a != b || !strings.HasPrefix(a, "git:") || !strings.HasSuffix(a, "//tools") {
		t.Errorf("one repository's pack has two identities: %q and %q", a, b)
	}
	if got := packsrc.PackIdentity("claude", "", ""); got != "yolo:claude" {
		t.Errorf("a shipped pack's identity = %q, want yolo:claude", got)
	}
}

// A move whose new key already holds a record leaves both, says so, and keeps the identity's index
// entry, so the next read tries again and finishes once the newer record is gone. Fails if the
// index is updated past a skipped move.
func TestAMoveBlockedByARecordAlreadyThereIsReportedAndRetried(t *testing.T) {
	dir, id := identityFixture(t)
	if _, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "acme/x", Identity: id}}); err != nil {
		t.Fatal(err)
	}
	forkLock := packsrc.ForkLockPath(filepath.Join(dir, paths.UserConfigTOMLName))
	setPin := func(fn func(*packsrc.ForkLock)) {
		t.Helper()
		if err := packsrc.WithForkLock(paths.PacksDir(), forkLock, nil, func(l *packsrc.ForkLock) (bool, error) {
			fn(l)
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	setPin(func(l *packsrc.ForkLock) {
		l.Set(packsrc.ForkLockEntry{Key: "tools/x/pi", Source: "git+https://example.invalid/pi?ref=main", Ref: "main",
			Commit: "2222222222222222222222222222222222222222"})
	})
	selected := []IdentifiedPack{{Handle: "tools/x", Identity: id}}
	lines, err := ReconcilePackRecords(dir, selected)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "could not all move to tools/x") || !strings.Contains(joined, "acme/x/pi stays") {
		t.Errorf("the read does not report the blocked move:\n%s", joined)
	}
	if _, pin, _ := recordsUnder(t, dir, "acme/x"); !pin {
		t.Errorf("the blocked pin did not stay under acme/x")
	}
	idx, err := packsrc.LoadIdentityIndex(filepath.Join(dir, packsrc.IdentityIndexName))
	if err != nil || idx.Packs[id] != "acme/x" {
		t.Fatalf("the index moved past a blocked move: %+v %v", idx, err)
	}
	// The newer record gone, the next read finishes the move.
	setPin(func(l *packsrc.ForkLock) { delete(l.Forks, "tools/x/pi") })
	if _, err := ReconcilePackRecords(dir, selected); err != nil {
		t.Fatal(err)
	}
	if lock, pin, check := recordsUnder(t, dir, "tools/x"); !lock || !pin || !check {
		t.Errorf("the retried move did not finish: lock=%v pin=%v check=%v", lock, pin, check)
	}
	if idx, _ := packsrc.LoadIdentityIndex(filepath.Join(dir, packsrc.IdentityIndexName)); idx.Packs[id] != "tools/x" {
		t.Errorf("the index does not follow the finished move: %+v", idx)
	}
}

// A pack added and launched (its pin written, the index not yet keeping it), then re-pointed at
// another place and launched again, does not adopt the first place's records: they are set aside
// and come back when that place is selected again. Fails if the first sight adopts records locked
// for another place.
func TestARepointBeforeTheIndexKeptThePackSetsTheOldRecordsAside(t *testing.T) {
	dir, id := identityFixture(t) // add + launch: the records exist, the index does not
	moved := filepath.Join(filepath.Dir(dir), "elsewhere")
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatal(err)
	}
	newID := packsrc.PackIdentity("", moved, "")
	lines, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "acme/x", Identity: newID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "start over") {
		t.Errorf("the re-pointed pack does not say it starts over: %v", lines)
	}
	if lock, pin, check := recordsUnder(t, dir, "acme/x"); lock || pin || check {
		t.Errorf("the re-pointed pack adopted the old place's records: lock=%v pin=%v check=%v", lock, pin, check)
	}
	if lock, pin, check := recordsUnder(t, dir, packsrc.ParkedHandle(id)); !lock || !pin || !check {
		t.Errorf("the old place's records were not set aside: lock=%v pin=%v check=%v", lock, pin, check)
	}
	if _, err := ReconcilePackRecords(dir, []IdentifiedPack{{Handle: "acme/x", Identity: id}}); err != nil {
		t.Fatal(err)
	}
	if lock, pin, check := recordsUnder(t, dir, "acme/x"); !lock || !pin || !check {
		t.Errorf("pointing back did not bring the records back: lock=%v pin=%v check=%v", lock, pin, check)
	}
}
