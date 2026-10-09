package run

// patchedrekey.go is the RE-KEY of a patched build recorded under its series' LEGACY digest
// (docs/design/patched-forks.md PF-D62): the digest of the same series files before PF-D61 took the
// mbox first line and git's signature out of it. Such a good build is a build of the series as it
// stands, so it must keep serving: reading it as the user's own edit would rebuild every series on
// the machine at the first launch after an upgrade, and leave a program missing wherever that build
// failed (PF-D23).
//
// THE RE-KEY moves it, once, to the digest as it stands, where every reader then finds it. Under the
// record lock, a receipt appended to its capture store entry names the entry under the new recipe as
// well (the entry's bytes are the build of both, and its key is a digest of those bytes, so nothing
// else changes), and then the check record's good build and the outcomes of the old digest take the
// new one. Every reader of a check record that holds the series as it stands loads it through here
// (LoadPatchedRecord), so whichever reads first — a launch's fork block, the advance, `yolo pack
// status`, the host floor — re-keys it, and none reads the old digest as an edit. Only that first
// read takes the record lock: a record with nothing to re-key is read without it, as before.
//
// It matches the old digest and the old recipe exactly, computed from the very files on disk, so a
// good build of other bytes, or of another `build` or `produces`, is never re-keyed: that is an edit.

import (
	"errors"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// PatchedRecipe is the recipe hash a build of f's series carries when the series' digest is series:
// a patched extension's tree recipe (PPX-D6), or a patched fork's program recipe (PF-D7).
func PatchedRecipe(f packload.Fork, series string) string {
	if f.IsTree() {
		return packdecl.TreeSourceRecipe(f.Source, f.Build, f.Produces, series)
	}
	return packdecl.ForkSourcePatchedRecipe(f.Source, f.Build, f.Produces, series)
}

// LoadPatchedRecord is f's check record as a reader holding its series s reads it: the record, its
// good build re-keyed from s's legacy digest first (RekeyLegacyGood). Errors are LoadCheckRecord's.
func LoadPatchedRecord(packs *packsrc.Store, f packload.Fork, s *packsrc.Series) (*packsrc.CheckRecord, error) {
	rec, err := packs.LoadCheckRecord(f.Key())
	if err != nil {
		return rec, err
	}
	return RekeyLegacyGood(packs, f, s, rec), nil
}

// RekeyLegacyGood is rec with its good build re-keyed, when rec names it under s's legacy digest and
// the recipe that digest gives (PF-D62): the store entry's receipt first, then the record under its
// lock, which it returns as it now stands. rec itself, unchanged, when there is nothing to re-key. A
// record that cannot be written is re-keyed in what this returns all the same, so this reader does
// not read the old digest as an edit, and the next reader tries the write again.
func RekeyLegacyGood(packs *packsrc.Store, f packload.Fork, s *packsrc.Series, rec *packsrc.CheckRecord) *packsrc.CheckRecord {
	if rec == nil || rec.Good == nil || s == nil || s.LegacyDigest == "" || s.LegacyDigest == s.Digest {
		return rec
	}
	oldRecipe := PatchedRecipe(f, s.LegacyDigest)
	if rec.Good.Series != s.LegacyDigest || rec.Good.Recipe != oldRecipe {
		return rec
	}
	newRecipe := PatchedRecipe(f, s.Digest)
	var out *packsrc.CheckRecord
	err := packs.WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil {
			return false, readErr
		}
		out = r
		if g := r.Good; g != nil && g.Series == s.LegacyDigest && g.Recipe == oldRecipe {
			// The receipt first, under the record lock, so two readers append one: a record re-keyed
			// with no receipt would name an entry the exact lookup cannot find.
			rekeyReceipt(f.Key(), g, oldRecipe, newRecipe, s.Digest)
		}
		return r.RekeySeries(s.LegacyDigest, oldRecipe, s.Digest, newRecipe), nil
	})
	if errors.Is(err, packsrc.ErrLockHeld) {
		return rec // background initialization defers the migration; no receipt write without its lock
	}
	if err == nil && out != nil {
		return out
	}
	rekeyReceipt(f.Key(), rec.Good, oldRecipe, newRecipe, s.Digest)
	c := *rec
	g := *rec.Good
	c.Good = &g
	c.Outcomes = append([]packsrc.EntryOutcome(nil), rec.Outcomes...)
	c.RekeySeries(s.LegacyDigest, oldRecipe, s.Digest, newRecipe)
	return &c
}

// rekeyReceipt appends to good build g's capture store entry a copy of its build receipt naming it
// under newRecipe and series, so the exact lookup under the recipe as it stands finds the entry
// (§6.3). The copy keeps the receipt's time, so no selection by recency moves. Best effort, and
// once: an entry gone from the store, or with no receipt of this fork at g's commit under oldRecipe,
// gets none, and one already carrying the new recipe gets no second.
func rekeyReceipt(fork string, g *packsrc.GoodBuild, oldRecipe, newRecipe, series string) {
	if g.Entry == "" {
		return
	}
	entry, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(g.Entry)
	if err != nil {
		return
	}
	path := capture.ReceiptsPath(entry.Root)
	builds, err := entrypoint.ReadBuildReceipts(path)
	if err != nil {
		return
	}
	var from *entrypoint.BuildReceipt
	for i := range builds {
		b := &builds[i]
		if b.Act != entrypoint.ReceiptActRecord || b.Fork != fork || b.Revision != g.Commit {
			continue
		}
		switch b.Recipe {
		case newRecipe:
			return
		case oldRecipe:
			from = b
		}
	}
	if from == nil {
		return
	}
	r := *from
	r.Recipe, r.Series = newRecipe, series
	_ = entrypoint.AppendReceiptLine(path, r.Line())
}
