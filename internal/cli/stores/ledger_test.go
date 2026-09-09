package stores

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTheLedgerIsBoundedAtThirtySamples is OQ-BF9's bound, asserted as the
// ruling states it: "bounded to 30 samples per store", so the 31st sample
// EVICTS THE FIRST rather than growing the file.
//
// The bound is the whole reason the exception to "never mutates" was grantable —
// a handle on growth that itself grows without limit is the defect one level up
// — so this is the test that has to fail if the trim ever goes.
func TestTheLedgerIsBoundedAtThirtySamples(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range MaxSamples + 1 {
		s := Sample{At: base.Add(time.Duration(i) * 24 * time.Hour), Sizing: SizingMeasured, Bytes: int64(i)}
		if err := AppendSample(dir, "cache.pants", s); err != nil {
			t.Fatalf("AppendSample #%d: %v", i, err)
		}
	}
	got := ReadSamples(dir, "cache.pants")
	if len(got) != MaxSamples {
		t.Fatalf("ledger holds %d samples, want the bound %d", len(got), MaxSamples)
	}
	if got[0].Bytes != 1 {
		t.Errorf("oldest surviving sample = %d bytes, want 1 — the 31st sample must evict the 1st, "+
			"not append past the bound", got[0].Bytes)
	}
	if last := got[len(got)-1]; last.Bytes != MaxSamples {
		t.Errorf("newest sample = %d bytes, want %d — the newest sample must be last", last.Bytes, MaxSamples)
	}
}

// TestTheBoundHoldsForAnAlreadyOversizedLedger pins that the trim is on the
// WRITE, not on the growth: a file that somehow already holds more than the
// bound (hand-edited, or written by a build with a larger cap) is brought back
// under it by the next write rather than kept forever at its old size.
func TestTheBoundHoldsForAnAlreadyOversizedLedger(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 100 {
		b.WriteString(base.Add(time.Duration(i)*time.Hour).Format(time.RFC3339) + " measured 5 0\n")
	}
	if err := os.WriteFile(SampleFile(dir, "k"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendSample(dir, "k", Sample{At: base, Sizing: SizingMeasured, Bytes: 7}); err != nil {
		t.Fatal(err)
	}
	if got := len(ReadSamples(dir, "k")); got != MaxSamples {
		t.Errorf("after one write the ledger holds %d samples, want %d", got, MaxSamples)
	}
}

// TestReadSamplesSurvivesGarbage: a corrupted line must never take out the
// inventory that reads it. The growth column is a convenience; the report is not.
func TestReadSamplesSurvivesGarbage(t *testing.T) {
	dir := t.TempDir()
	body := "not a timestamp at all\n" +
		"2026-01-01T00:00:00Z measured 42 3\n" +
		"2026-01-02T00:00:00Z measured notanumber 0\n" +
		"\n"
	if err := os.WriteFile(SampleFile(dir, "k"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadSamples(dir, "k")
	if len(got) != 1 || got[0].Bytes != 42 || got[0].Count != 3 {
		t.Fatalf("ReadSamples = %+v, want exactly the one parseable line (42 bytes, 3 entries)", got)
	}
}

// TestSampleFileCannotEscapeTheLedgerDir: a store key reaches this code straight
// off the disk (a cache subdir's own name), so a name carrying a separator or a
// traversal must not decide where the ledger writes.
func TestSampleFileCannotEscapeTheLedgerDir(t *testing.T) {
	dir := "/tmp/ledger"
	for _, key := range []string{"../../etc/passwd", "a/b", "..", ".", "", "nor mal"} {
		got := SampleFile(dir, key)
		if filepath.Dir(got) != dir {
			t.Errorf("SampleFile(%q) = %q, which is not directly under %q", key, got, dir)
		}
	}
}

// TestGrowthNeedsTwoDatedSamples covers the rate's whole contract: no prior
// sample means no rate, a prior MEASURED sample far enough back means a rate,
// and neither a partial sample nor a pair minutes apart may produce one.
func TestGrowthNeedsTwoDatedSamples(t *testing.T) {
	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	cur := Store{Sizing: SizingMeasured, Bytes: 20 << 30}

	if g := growthFrom(nil, cur, now); g != nil {
		t.Errorf("a store with no samples reported a rate (%+v); it must report none", g)
	}

	tenDaysAgo := Sample{At: now.Add(-10 * 24 * time.Hour), Sizing: SizingMeasured, Bytes: 10 << 30}
	g := growthFrom([]Sample{tenDaysAgo}, cur, now)
	if g == nil {
		t.Fatal("two dated measured samples produced no rate")
	}
	if want := int64(1 << 30); g.BytesPerDay != want {
		t.Errorf("rate = %d B/d, want %d (10 GiB over 10 days)", g.BytesPerDay, want)
	}

	partial := Sample{At: now.Add(-10 * 24 * time.Hour), Sizing: SizingPartial, Bytes: 1}
	if g := growthFrom([]Sample{partial}, cur, now); g != nil {
		t.Error("a rate was computed against a PARTIAL sample — a lower bound divided by a " +
			"real interval is a number with no honest name")
	}
	if g := growthFrom([]Sample{tenDaysAgo}, Store{Sizing: SizingPartial, Bytes: 1}, now); g != nil {
		t.Error("a rate was computed for a store whose CURRENT figure is partial")
	}

	minutesAgo := Sample{At: now.Add(-2 * time.Minute), Sizing: SizingMeasured, Bytes: 1}
	if g := growthFrom([]Sample{minutesAgo}, cur, now); g != nil {
		t.Errorf("two samples %v apart produced a rate (%+v); dividing noise by a sliver of a day "+
			"manufactures a catastrophe", 2*time.Minute, g)
	}
}
