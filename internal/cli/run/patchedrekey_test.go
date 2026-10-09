package run

// patchedrekey_test.go pins the launch block's half of the RE-KEY (patchedrekey.go,
// docs/design/patched-forks.md PF-D62): a patched fork's line and a patched extension's line, which
// every launch prints before its advance, read a good build recorded under the series' legacy digest
// as the build of the series as it stands — never as the user's edit — and leave it re-keyed; and the
// re-key itself names the store entry under the new recipe, once, keeping the receipt's time.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// recordLegacyGood writes f's check record with a good build of s under its legacy digest and the
// recipe that digest gives, its store entry named entry.
func recordLegacyGood(t *testing.T, f packload.Fork, s *packsrc.Series, entry string) {
	t.Helper()
	if s.LegacyDigest == s.Digest {
		t.Fatal("the series' legacy digest is its digest, so there is nothing to re-key")
	}
	err := patchedPacksStore().WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		r.Good = &packsrc.GoodBuild{Commit: patchedBase, Tag: "v1.0.0", Version: "1.0.0", Series: s.LegacyDigest,
			Recipe: PatchedRecipe(f, s.LegacyDigest), Patches: 1, Entry: entry}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// legacyReceiptTime is when admitLegacyBuild's receipt says its build was recorded.
var legacyReceiptTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// admitLegacyBuild admits a build of f into the capture store with the receipt a yolo before PF-D61
// wrote for it, under s's legacy digest and recipe, and returns the entry and its receipts file.
func admitLegacyBuild(t *testing.T, f packload.Fork, s *packsrc.Series) (*capture.Entry, string) {
	t.Helper()
	store := &capture.Store{Dir: paths.CapturesDir()}
	staged, err := store.Stage("rekey-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staged, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, ".local", "bin", "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry, err := store.Admit(staged)
	if err != nil {
		t.Fatal(err)
	}
	old := entrypoint.BuildReceipt{Bin: "tool", Source: packsrc.BuildSource(patchedSource), Key: entry.Key,
		Platform: "linux/amd64", Revision: patchedBase, Recipe: PatchedRecipe(f, s.LegacyDigest), Fork: f.Key(),
		Series: s.LegacyDigest, Tag: "v1.0.0", Version: "1.0.0", Act: entrypoint.ReceiptActRecord, Time: legacyReceiptTime}
	receipts := capture.ReceiptsPath(entry.Root)
	if err := entrypoint.AppendReceiptLine(receipts, old.Line()); err != nil {
		t.Fatal(err)
	}
	return entry, receipts
}

// assertGoodReKeyed fails unless f's record names its good build under s's digest as it stands.
func assertGoodReKeyed(t *testing.T, f packload.Fork, s *packsrc.Series) {
	t.Helper()
	rec, err := patchedPacksStore().LoadCheckRecord(f.Key())
	if err != nil {
		t.Fatal(err)
	}
	if g := rec.Good; g == nil || g.Series != s.Digest || g.Recipe != PatchedRecipe(f, s.Digest) {
		t.Errorf("the good build after the line = %+v, want it under the digest %s as it stands", g, s.ShortDigest())
	}
}

// THE FORK BLOCK'S LINE names the legacy good build as what runs, with no edit, and re-keys it.
func TestTheForkLineReadsALegacyDigestGoodBuildAsTheSeries(t *testing.T) {
	forkDir := patchedLaunchHome(t)
	f := packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool", Source: patchedSource, Build: "make install",
		Produces: []string{".local/bin/tool"}, Root: forkDir, Patches: "patches"}
	s, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	recordLegacyGood(t, f, s, "k1")
	line, warn := patchedForkLine(packload.ForkPin{Fork: f}, "a fresh launch")
	if !strings.HasSuffix(line, ", at v1.0.0 (01234567)") || warn {
		t.Errorf("the line over a legacy-digest good build is %q (warn %v), want it named as what runs", line, warn)
	}
	assertGoodReKeyed(t, f, s)
}

// THE PATCHED EXTENSIONS' BLOCK LINE does the same for a tree.
func TestTheTreeLineReadsALegacyDigestGoodBuildAsTheSeries(t *testing.T) {
	forkDir := patchedLaunchHome(t)
	f := packload.Fork{Pack: "forkpack", Bin: "tool-ext", Into: ".tool/ext/tool-ext", Source: patchedSource,
		Build: "true", Root: forkDir, Patches: "patches"}
	s, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	recordLegacyGood(t, f, s, "k1")
	line, warn := patchedTreeLine(f)
	if !strings.HasSuffix(line, ", at v1.0.0 (01234567)") || warn {
		t.Errorf("the tree's line over a legacy-digest good build is %q (warn %v), want it named as what runs", line, warn)
	}
	assertGoodReKeyed(t, f, s)
}

// THE RE-KEY NAMES THE STORE ENTRY UNDER THE NEW RECIPE: a copy of its build receipt with the recipe
// and the digest as they stand and the original's time, appended once however often it is read, so the
// exact lookup under the recipe as it stands finds the entry; nothing for a good build of another
// recipe, which is an edit.
func TestTheReKeyNamesTheStoreEntryUnderTheNewRecipeOnce(t *testing.T) {
	forkDir := patchedLaunchHome(t)
	f := packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool", Source: patchedSource, Build: "make install",
		Produces: []string{".local/bin/tool"}, Root: forkDir, Patches: "patches"}
	s, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	entry, receipts := admitLegacyBuild(t, f, s)
	// Twice, the second time over a record put back as a crash between the receipt and the record's
	// write leaves it: the receipt is there already, so none is appended again.
	for range 2 {
		recordLegacyGood(t, f, s, entry.Key)
		if _, err := LoadPatchedRecord(patchedPacksStore(), f, s); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := entrypoint.ReadBuildReceipts(receipts)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("the entry carries %d build receipts, want the original and one re-keyed copy: %+v", len(recs), recs)
	}
	if got := recs[1]; got.Recipe != PatchedRecipe(f, s.Digest) || got.Series != s.Digest || got.Key != entry.Key ||
		got.Revision != patchedBase || got.Fork != f.Key() || !got.Time.Equal(legacyReceiptTime) || got.Tag != "v1.0.0" {
		t.Errorf("the re-keyed receipt = %+v, want the original's under the new recipe and digest", got)
	}
	assertGoodReKeyed(t, f, s)

	// AN EDIT IS LEFT AS IT IS: a legacy good build of f's recipe, read under another build line, and
	// the conflict recorded beside it, keep the legacy digest, and the entry gains no receipt.
	edited := f
	edited.Build = "make install-other"
	recordLegacyGood(t, f, s, entry.Key)
	if err := patchedPacksStore().WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		r.Outcomes = []packsrc.EntryOutcome{{Commit: strings.Repeat("b", 40), Kind: packsrc.OutcomeConflict, Series: s.LegacyDigest}}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := LoadPatchedRecord(patchedPacksStore(), edited, s)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Good.Series != s.LegacyDigest || rec.Outcomes[0].Series != s.LegacyDigest {
		t.Errorf("an edit's record was re-keyed (good %+v, outcomes %+v): an edited build line is an edit", rec.Good,
			rec.Outcomes)
	}
	if recs, _ := entrypoint.ReadBuildReceipts(receipts); len(recs) != 2 {
		t.Errorf("reading an edit appended a receipt: %+v", recs)
	}
}

// A RECORD THAT CANNOT BE WRITTEN is re-keyed in what the reader gets all the same, and its entry is
// still named under the new recipe, so this reader neither reads the legacy digest as an edit nor
// misses the entry; the record on disk waits for the next reader's write.
func TestARecordThatCannotBeWrittenIsReKeyedInWhatIsRead(t *testing.T) {
	forkDir := patchedLaunchHome(t)
	f := packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool", Source: patchedSource, Build: "make install",
		Produces: []string{".local/bin/tool"}, Root: forkDir, Patches: "patches"}
	s, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	entry, receipts := admitLegacyBuild(t, f, s)
	recordLegacyGood(t, f, s, entry.Key)
	locks, _ := filepath.Glob(filepath.Join(paths.PacksDir(), "locks", "check-*.lock"))
	if len(locks) != 1 {
		t.Fatalf("the record's lock = %v, want the one its write took", locks)
	}
	// A directory where the lock file goes: the lock cannot be taken, so the record cannot be written.
	if err := os.Remove(locks[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(locks[0], 0o755); err != nil {
		t.Fatal(err)
	}
	rec, err := LoadPatchedRecord(patchedPacksStore(), f, s)
	if err != nil {
		t.Fatal(err)
	}
	if g := rec.Good; g.Series != s.Digest || g.Recipe != PatchedRecipe(f, s.Digest) {
		t.Errorf("what the reader got = %+v, want it re-keyed although the record cannot be written", g)
	}
	if recs, _ := entrypoint.ReadBuildReceipts(receipts); len(recs) != 2 || recs[1].Recipe != PatchedRecipe(f, s.Digest) {
		t.Errorf("the entry's receipts = %+v, want one naming it under the new recipe", recs)
	}
	if line, warn := patchedForkLine(packload.ForkPin{Fork: f}, "a fresh launch"); warn {
		t.Errorf("the fork's line over an unwritable record reads an edit: %q", line)
	}
	disk, err := patchedPacksStore().LoadCheckRecord(f.Key())
	if err != nil {
		t.Fatal(err)
	}
	if disk.Good.Series != s.LegacyDigest {
		t.Errorf("the record on disk = %+v, which no write could reach", disk.Good)
	}
}

// A busy legacy record under NoWait must defer both the migration and the receipt append.
func TestANoWaitLegacyReKeyLeavesABusyRecordAndReceipt(t *testing.T) {
	forkDir := patchedLaunchHome(t)
	f := packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool", Source: patchedSource, Build: "make install",
		Produces: []string{".local/bin/tool"}, Root: forkDir, Patches: "patches"}
	s, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	entry, receipts := admitLegacyBuild(t, f, s)
	recordLegacyGood(t, f, s, entry.Key)
	before, err := os.ReadFile(receipts)
	if err != nil {
		t.Fatal(err)
	}
	holding, release, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(released)
		_ = patchedPacksStore().WithCheckRecord(f.Key(), nil, func(*packsrc.CheckRecord, error, func() error) (bool, error) {
			close(holding)
			<-release
			return false, nil
		})
	}()
	<-holding
	bg := *patchedPacksStore()
	bg.NoWait = true
	done := make(chan *packsrc.CheckRecord, 1)
	go func() { rec, _ := LoadPatchedRecord(&bg, f, s); done <- rec }()
	var rec *packsrc.CheckRecord
	select {
	case rec = <-done:
	case <-time.After(time.Second):
		close(release)
		<-released
		t.Fatal("NoWait legacy initialization waited for its record lock")
	}
	close(release)
	<-released
	if rec == nil || rec.Good.Series != s.LegacyDigest {
		t.Fatalf("busy legacy record migrated: %+v", rec)
	}
	after, err := os.ReadFile(receipts)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("busy legacy record appended a receipt without taking its lock")
	}
}
