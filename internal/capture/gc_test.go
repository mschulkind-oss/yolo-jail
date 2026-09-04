package capture

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// gc_test.go pins the reap rule AS THE COMPLEMENT OF THE SELECTION, not as a rule of its own.
//
// The distinction is the whole point of OQ-PD17 and it is testable: the flagship case below
// asserts a PARTITION — every complete entry in the store is either the one Select returns for
// its program or one PruneSupersededCaptures takes, never both and never neither. Change
// selectFrom (invert the tie-break, keep the oldest, drop the platform from the key) and that
// test fails from the reap side as well as the resolve side, which is what "the two cannot
// drift" has to mean if it means anything.
//
// The receipt SCHEMA is not exercised here: the store is handed records through the Records
// seam (see select.go for why it does not parse them itself), so these tests supply a table.
// The real adapter over the real receipt log is pinned in internal/cli, where it lives.

// fakeRecords is the Records seam as a table: entry dir → what its receipt log says. A dir
// mapped to errUnreadable stands for a log that exists and cannot be read; an unmapped dir
// stands for no log at all. Both must read as "attributed to no program".
type fakeRecords map[string][]Record

var errUnreadable = errors.New("receipt log unreadable")

func (f fakeRecords) read(dir string) ([]Record, error) {
	recs, ok := f[dir]
	if !ok {
		return nil, nil
	}
	if recs == nil {
		return nil, errUnreadable
	}
	return recs, nil
}

// admitFixture stages an entry-shaped capture whose tree holds one file of the given body,
// admits it, and returns the entry. Distinct bodies mean distinct keys (the key is the tree's
// content address), which is what lets a test hold several entries for one program.
func admitFixture(t *testing.T, s *Store, id, body string) *Entry {
	t.Helper()
	staged, err := s.Stage(id)
	must(t, err)
	tree := TreeDir(staged)
	must(t, os.MkdirAll(filepath.Join(tree, ".local", "bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(tree, ".local", "bin", "probetool"), []byte(body), 0o755))
	must(t, WriteManifest(staged, &Manifest{
		Schema: ManifestSchema, Home: "/home/agent", Platform: "linux/amd64",
		Surfaces: []string{".local"}, Excluded: []string{},
		Entries: []ManifestEntry{
			{Path: ".local", Kind: KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: KindDir, Mode: "0755"},
			{Path: ".local/bin/probetool", Kind: KindFile, Mode: "0755", Size: int64(len(body))},
		},
		AbsoluteRefs: []AbsoluteRef{},
	}))
	entry, err := s.AdmitEntry(staged)
	must(t, err)
	return entry
}

// THE PARTITION: after a reap, the store holds exactly what Select would return, and the
// entries that went are exactly the ones it would not.
//
// Five entries, four states: two captures of one program (newest wins), one for a second
// platform (its own program, so it also wins), one whose receipt log cannot be read, and one
// with no log at all. The last two are reapable on the SAME rule as the superseded one — an
// entry nothing attributes to a program is an entry the resolver can never return.
func TestReapIsExactlyTheComplementOfSelect(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	old := admitFixture(t, s, "old", "OLD\n")
	newest := admitFixture(t, s, "new", "NEW\n")
	foreign := admitFixture(t, s, "foreign", "FOREIGN\n")
	unreadable := admitFixture(t, s, "unreadable", "UNREADABLE\n")
	unattributed := admitFixture(t, s, "unattributed", "UNATTRIBUTED\n")

	linux := Record{Bin: "probetool", Platform: "linux/amd64", Digest: "d"}
	table := fakeRecords{
		old.Root:     {rec(linux, when.Add(-2*time.Hour))},
		newest.Root:  {rec(linux, when)},
		foreign.Root: {rec(Record{Bin: "probetool", Platform: "plan9/mips"}, when)},
		// nil is the reader ERRORING; unattributed is simply absent from the table.
		unreadable.Root: nil,
	}

	selected, err := Select(s, table.read)
	must(t, err)
	if got := len(selected); got != 2 {
		t.Fatalf("Select returned %d programs, want 2: %+v", got, selected)
	}
	if k := selected[Program{"probetool", "linux/amd64"}].Key; k != newest.Key {
		t.Errorf("Select chose %s for the linux program, want the newest %s", k, newest.Key)
	}

	reap, err := PruneSupersededCaptures(s.Dir, table.read, false)
	must(t, err)
	if reap.Kept != 2 {
		t.Errorf("Kept = %d, want the 2 entries Select returned", reap.Kept)
	}
	reaped := map[string]ReapedEntry{}
	for _, e := range reap.Entries {
		reaped[e.Key] = e
	}
	// The partition, asserted over the store rather than over a hand-written expectation:
	// every complete entry is selected or reaped, never both, never neither.
	keys, err := s.EntryKeys()
	must(t, err)
	if len(keys) != 5 {
		t.Fatalf("store holds %d entries, want 5", len(keys))
	}
	wins := map[string]bool{}
	for _, sel := range selected {
		wins[sel.Key] = true
	}
	for _, key := range keys {
		_, gone := reaped[key]
		if wins[key] == gone {
			t.Errorf("entry %s: selected=%v reaped=%v — the reap set must be the complement "+
				"of the selection, exactly", key, wins[key], gone)
		}
	}
	// And the reasons name the winner, so a report can say what went and why.
	if r := reaped[old.Key]; len(r.Lost) != 1 || r.Lost[0].Winner != newest.Key {
		t.Errorf("the superseded entry does not name its winner: %+v", r.Lost)
	}
	if r := reaped[unattributed.Key]; len(r.Lost) != 0 ||
		r.Reason() != "no record receipt — attributed to no program" {
		t.Errorf("an unattributed entry should say so, got %q", r.Reason())
	}
	if r := reaped[unreadable.Key]; len(r.Lost) != 0 {
		t.Errorf("an unreadable log is the same answer as an absent one, got %+v", r.Lost)
	}
	if reap.Bytes != int64(len("OLD\n")+len("UNREADABLE\n")+len("UNATTRIBUTED\n")) {
		t.Errorf("Bytes = %d, want the three reaped trees' file bytes", reap.Bytes)
	}
	// DRY RUN TOUCHED NOTHING.
	for _, e := range []*Entry{old, newest, foreign, unreadable, unattributed} {
		if _, err := s.Resolve(e.Key); err != nil {
			t.Errorf("a dry run removed %s: %v", e.Key, err)
		}
	}
}

// APPLY: the tree goes, the entry reads ABSENT, and the manifest stays.
//
// The manifest half is not decoration — it is the reason a reap is cheap to be wrong about.
// capture-manifest.json sits beside tree/, so drift comparison against a version no longer
// stored survives the reclaim for kilobytes.
func TestApplyDropsTheTreeAndKeepsTheManifest(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	old := admitFixture(t, s, "old", "OLD\n")
	newest := admitFixture(t, s, "new", "NEW\n")
	linux := Record{Bin: "probetool", Platform: "linux/amd64"}
	table := fakeRecords{
		old.Root:    {rec(linux, when.Add(-time.Hour))},
		newest.Root: {rec(linux, when)},
	}

	reap, err := PruneSupersededCaptures(s.Dir, table.read, true)
	must(t, err)
	if len(reap.Entries) != 1 || reap.Entries[0].Key != old.Key || reap.Entries[0].Err != nil {
		t.Fatalf("reap = %+v, want the superseded entry removed cleanly", reap.Entries)
	}
	// ADMIT FROZE THE TREE'S FILES READ-ONLY, and the reap still removes it: store.go keeps
	// the write bit on DIRECTORIES precisely so a GC can unlink an entry, and nothing else
	// tests that promise.
	if _, err := os.Stat(old.Tree); !os.IsNotExist(err) {
		t.Errorf("the reaped tree is still there: %v", err)
	}
	if _, err := s.Resolve(old.Key); !errors.Is(err, ErrNotCaptured) {
		t.Errorf("a reaped entry must read as absent, got %v", err)
	}
	if _, err := os.Stat(ManifestPath(old.Root)); err != nil {
		t.Errorf("the manifest must survive the reap: %v", err)
	}
	if _, err := ReadManifest(old.Root); err != nil {
		t.Errorf("the surviving manifest must still parse: %v", err)
	}
	// The winner is untouched and still materializable.
	if _, err := s.Resolve(newest.Key); err != nil {
		t.Errorf("the selected entry went too: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newest.Tree, ".local", "bin", "probetool")); err != nil {
		t.Errorf("the selected entry's tree was disturbed: %v", err)
	}

	// IDEMPOTENT: the husk is not a candidate for anything. EntryKeys skips it (no marker),
	// so a second pass neither re-reaps it nor counts it as kept.
	again, err := PruneSupersededCaptures(s.Dir, table.read, true)
	must(t, err)
	if len(again.Entries) != 0 || again.Bytes != 0 || again.Kept != 1 {
		t.Errorf("second pass = %+v, want nothing left to do", again)
	}
}

// The completion marker goes FIRST, so an interrupted reap leaves an entry that reads ABSENT
// with its bytes still on disk (a miss, then a download) rather than one that reads COMPLETE
// with no tree (a loud mid-materialize failure). The observable half of that ordering: the
// marker is cleared unconditionally, even when there is no tree left to remove.
func TestReapClearsTheMarkerEvenWithNoTree(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	entry := admitFixture(t, s, "half", "HALF\n")
	must(t, os.RemoveAll(entry.Tree))

	must(t, reapEntry(entry.Root))
	if _, err := s.Resolve(entry.Key); !errors.Is(err, ErrNotCaptured) {
		t.Errorf("the marker survived a reap with no tree: %v", err)
	}
}

// A nil reader is refused rather than defaulted. With no attribution every entry looks
// unattributed, and the complement of an empty selection is the whole store.
func TestPruneRefusesWithoutAReader(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	entry := admitFixture(t, s, "only", "ONLY\n")
	if _, err := PruneSupersededCaptures(s.Dir, nil, true); err == nil {
		t.Fatal("a nil reader must be refused")
	}
	if _, err := s.Resolve(entry.Key); err != nil {
		t.Errorf("the refusal removed something: %v", err)
	}
}

// An absent store is an empty one — the state of every machine that has never captured.
func TestPruneOnAnAbsentStoreIsNotAnError(t *testing.T) {
	reap, err := PruneSupersededCaptures(filepath.Join(t.TempDir(), "nope"), fakeRecords{}.read, true)
	if err != nil {
		t.Fatalf("absent store: %v", err)
	}
	if len(reap.Entries) != 0 || reap.Kept != 0 {
		t.Errorf("absent store yielded %+v", reap)
	}
}

// selectFrom's tie-break, without a filesystem: same stamp → the GREATER KEY wins, because the
// receipt stamp has one-second resolution and directory order must not decide a re-capture.
func TestSelectFromTieBreaksOnTheGreaterKey(t *testing.T) {
	when := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	p := Program{"probetool", "linux/amd64"}
	linux := Record{Bin: p.Bin, Platform: p.Platform}
	scan := []entryRecords{
		{Key: "aaaa", Records: []Record{rec(linux, when)}},
		{Key: "cccc", Records: []Record{rec(linux, when)}},
		{Key: "bbbb", Records: []Record{rec(linux, when)}},
	}
	if got := selectFrom(scan)[p].Key; got != "cccc" {
		t.Errorf("tie went to %s, want the greater key cccc", got)
	}
	// A later stamp beats a greater key: time is the rule, the key is only the tie-break.
	scan = append(scan, entryRecords{Key: "aaab", Records: []Record{rec(linux, when.Add(time.Second))}})
	if got := selectFrom(scan)[p].Key; got != "aaab" {
		t.Errorf("newest lost to a greater key: got %s", got)
	}
}

// A record naming no bin or no platform is not a candidate: a query never carries an empty one,
// so nothing could ever match it. It is therefore reapable like any other unattributed entry —
// the same rule, not a second one.
func TestARecordWithNoProgramIsNotACandidate(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	entry := admitFixture(t, s, "nameless", "NAMELESS\n")
	table := fakeRecords{entry.Root: {
		rec(Record{Bin: "", Platform: "linux/amd64"}, time.Now()),
		rec(Record{Bin: "probetool", Platform: ""}, time.Now()),
	}}
	selected, err := Select(s, table.read)
	must(t, err)
	if len(selected) != 0 {
		t.Fatalf("an empty program was selected: %+v", selected)
	}
	reap, err := PruneSupersededCaptures(s.Dir, table.read, false)
	must(t, err)
	if len(reap.Entries) != 1 || len(reap.Entries[0].Lost) != 0 {
		t.Errorf("reap = %+v, want the entry reaped as unattributed", reap.Entries)
	}
}

// rec stamps a record — the two fields every fixture varies, spelled once.
func rec(r Record, at time.Time) Record {
	r.Time = at
	return r
}
