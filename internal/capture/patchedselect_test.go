package capture

// patchedselect_test.go pins how a PATCHED fork's build is selected and reaped (select.go, gc.go;
// docs/design/patched-forks.md §6.3, PF-D7): by its fork key, never its source, so no plain fork's
// query and no other fork's selects it, and `yolo prune` keeps the newest build per fork; and the
// move's own reap (ReapEntry), marker first.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAPatchedBuildIsSelectedByItsForkNeverItsSource(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	when := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	plain := admitFixture(t, s, "plain", "PLAIN\n")
	mine := admitFixture(t, s, "mine", "MINE\n")
	mineNewer := admitFixture(t, s, "mine-newer", "MINE NEWER\n")
	theirs := admitFixture(t, s, "theirs", "THEIRS\n")
	const src = "git+https://example.com/up"
	base := Record{Bin: "pi", Platform: "linux/amd64", Source: src, Revision: "c1", Recipe: "r"}
	patched := func(fork string) Record { r := base; r.Fork = fork; return r }
	table := fakeRecords{
		plain.Root:     {rec(base, when.Add(time.Hour))}, // the newest of all, and a plain fork's
		mine.Root:      {rec(patched("a/pi"), when)},
		mineNewer.Root: {rec(patched("a/pi"), when.Add(time.Minute))},
		theirs.Root:    {rec(patched("b/pi"), when.Add(2*time.Hour))},
	}
	sel, err := Select(s, table.read)
	must(t, err)
	if got := sel[Program{Bin: "pi", Platform: "linux/amd64", Source: src}].Key; got != plain.Key {
		t.Errorf("the plain fork's query selected %s, want its own build %s", got, plain.Key)
	}
	if got := sel[Program{Bin: "pi", Platform: "linux/amd64", Fork: "a/pi"}].Key; got != mineNewer.Key {
		t.Errorf("fork a/pi's selection is %s, want its newest build %s", got, mineNewer.Key)
	}
	if got := sel[Program{Bin: "pi", Platform: "linux/amd64", Fork: "b/pi"}].Key; got != theirs.Key {
		t.Errorf("fork b/pi's selection is %s, want its own build %s", got, theirs.Key)
	}
	// THE PRUNE IS THE COMPLEMENT: the newest per fork and the plain fork's survive.
	reap, err := PruneSupersededCaptures(s.Dir, table.read, true)
	must(t, err)
	if len(reap.Entries) != 1 || reap.Entries[0].Key != mine.Key {
		t.Fatalf("prune reaped %+v, want fork a/pi's older build alone", reap.Entries)
	}
	if got := reap.Entries[0].Reason(); got != "pi (linux/amd64, patched fork a/pi) superseded by "+mineNewer.Key {
		t.Errorf("the reason = %q", got)
	}
}

// SCAN lists every complete entry with its records, for the exact lookup.
func TestScanListsEveryEntryWithItsRecords(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	a := admitFixture(t, s, "a", "A\n")
	b := admitFixture(t, s, "b", "B\n")
	table := fakeRecords{a.Root: {rec(Record{Bin: "pi", Platform: "linux/amd64", Fork: "f/pi"}, time.Now())}}
	scan, err := Scan(s, table.read)
	must(t, err)
	if len(scan) != 2 {
		t.Fatalf("scan = %+v, want both entries", scan)
	}
	for _, e := range scan {
		switch e.Key {
		case a.Key:
			if len(e.Records) != 1 || e.Records[0].Fork != "f/pi" {
				t.Errorf("entry a's records = %+v", e.Records)
			}
		case b.Key:
			if len(e.Records) != 0 {
				t.Errorf("entry b has records %+v, and its log is absent", e.Records)
			}
		}
	}
}

// REAPENTRY takes the marker first and the tree, and keeps the metadata; a key that is not an
// entry name is refused.
func TestReapEntryReapsOneEntry(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	e := admitFixture(t, s, "x", "X\n")
	must(t, s.ReapEntry(e.Key))
	if _, err := s.Resolve(e.Key); err == nil {
		t.Error("a reaped entry still resolves")
	}
	if _, err := os.Stat(filepath.Join(s.EntryDir(e.Key), ManifestName)); err != nil {
		t.Errorf("the reap removed the manifest beside the tree: %v", err)
	}
	if err := s.ReapEntry("../x"); err == nil {
		t.Error("a key with a path in it was reaped")
	}
}
