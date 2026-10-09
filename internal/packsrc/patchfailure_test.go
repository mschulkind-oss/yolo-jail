package packsrc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func TestPatchFailureSnapshotDeepCopiesAuthority(t *testing.T) {
	in := CheckInputs{Repo: "r", Ref: "main", Follow: "release", Base: "base"}
	r := &CheckRecord{Owner: "forkpack/tool", Seq: 4, Read: in, Check: &CheckFound{Seq: 4},
		PatchFailure: &PatchFailure{Owner: "forkpack/tool", Inputs: in, Series: "s", Target: ListEntry{Commit: "c"},
			Kind: "conflict", Paths: []string{"one"}},
		ApplyErr: &ApplyError{Seq: 4, Error: "opaque", Legacy: &LegacyReplayAttempt{Seq: 4, State: "unavailable", Detail: "first"}}}
	snapshot, err := r.ReplaySnapshot(in, "s")
	if err != nil {
		t.Fatal(err)
	}
	r.PatchFailure.Paths[0] = "changed"
	r.ApplyErr.Legacy.Detail = "changed"
	if snapshot.Failure.Paths[0] != "one" || snapshot.ApplyErr.Legacy.Detail != "first" {
		t.Fatalf("snapshot aliased mutable record authority: %+v", snapshot)
	}
}

func TestPatchFailureWalkSnapshotRejectsMismatch(t *testing.T) {
	u, series, snap := patchFailureSnapshotFixture(t)
	addr := mustAddr(t, u.source("main"))
	walk := u.store.WalkSeries(addr.Repo, "wrong-subdir", series, nil, WalkOptions{Snapshot: &snap})
	if walk.Err == nil || walk.PatchFailure() != nil {
		t.Fatalf("mismatched bound walk = %+v, want ordinary API error only", walk)
	}
}

func TestPatchFailureSnapshotRejectsNewerCheck(t *testing.T) {
	u, _, snap := patchFailureSnapshotFixture(t)
	r, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	r.Seq++
	r.Check.Seq = r.Seq
	if err := u.store.saveCheckRecord(r); err != nil {
		t.Fatal(err)
	}
	walk := WalkResult{Fit: -1, snapshot: cloneReplaySnapshot(&snap), Results: []ReplayResult{{
		Entry: ListEntry{Commit: r.Check.List[0].Commit}, Conflict: &ReplayConflict{Member: "0001-x.patch"},
	}}}
	got := u.store.RecordReplay(snap, "yolo", walk, time.Unix(1_800_000_000, 0))
	if !errors.Is(got.Err, ErrStaleReplay) || got.Recorded {
		t.Fatalf("RecordReplay = %+v, want stale and unrecorded", got)
	}
}

func TestPatchFailureSnapshotRejectsNewerSameSequenceAuthority(t *testing.T) {
	u, _, snap := patchFailureSnapshotFixture(t)
	r, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	r.PatchFailure = &PatchFailure{Owner: snap.Owner, Inputs: snap.Inputs, Series: snap.Series,
		Target: r.Check.List[0], Kind: "conflict", Member: "newer.patch", Seq: snap.Seq}
	if err := u.store.saveCheckRecord(r); err != nil {
		t.Fatal(err)
	}
	walk := WalkResult{Fit: -1, snapshot: cloneReplaySnapshot(&snap), Results: []ReplayResult{{
		Entry: r.Check.List[0], Conflict: &ReplayConflict{Member: "older.patch"},
	}}}
	got := u.store.RecordReplay(snap, "yolo", walk, time.Unix(1_800_000_000, 0))
	if !errors.Is(got.Err, ErrStaleReplay) || got.Recorded {
		t.Fatalf("RecordReplay = %+v, want stale same-sequence authority and no update", got)
	}
}

func TestPatchFailureSnapshotRejectsNewerSameSequenceApplyError(t *testing.T) {
	u, _, snap := patchFailureSnapshotFixture(t)
	r, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	r.ApplyErr = &ApplyError{Seq: snap.Seq, Error: "newer operational authority"}
	if err := u.store.saveCheckRecord(r); err != nil {
		t.Fatal(err)
	}
	walk := WalkResult{Fit: -1, snapshot: cloneReplaySnapshot(&snap), Results: []ReplayResult{{
		Entry: r.Check.List[0], Conflict: &ReplayConflict{Member: "older.patch"},
	}}}
	got := u.store.RecordReplay(snap, "yolo", walk, time.Unix(1_800_000_000, 0))
	if !errors.Is(got.Err, ErrStaleReplay) || got.Recorded {
		t.Fatalf("RecordReplay = %+v, want stale newer ApplyErr authority and no update", got)
	}
}

func TestPatchFailureRecordErrorRetainsCurrentEvidence(t *testing.T) {
	u, _, snap := patchFailureSnapshotFixture(t)
	walk := WalkResult{Fit: -1, snapshot: cloneReplaySnapshot(&snap), Results: []ReplayResult{{
		Entry: ListEntry{Commit: "new-target"}, Conflict: &ReplayConflict{Member: "0001-x.patch", Paths: []string{"f.txt"}},
	}}}
	lock := filepath.Join(u.store.Dir, "locks", "check-"+mirrorSlug(snap.Owner)+".lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	got := u.store.RecordReplay(snap, "yolo", walk, time.Unix(1_800_000_000, 0))
	if got.Err == nil || got.Recorded || got.Failure == nil || got.Failure.Member != "0001-x.patch" ||
		len(got.Failure.Paths) != 1 || got.Failure.Paths[0] != "f.txt" {
		t.Fatalf("RecordReplay = %+v, want complete current evidence and persistence error", got)
	}
}

func TestPatchFailureSuccessfulCheckAloneRemovesTarget(t *testing.T) {
	in := CheckInputs{Repo: "r", Ref: "main", Follow: "release", Base: "base"}
	pf := &PatchFailure{Owner: "forkpack/tool", Inputs: in, Series: "s", Target: ListEntry{Commit: "c12"}, Kind: "conflict"}
	for _, tc := range []struct {
		name  string
		check *CheckFound
		read  CheckInputs
		want  bool
	}{{name: "failed-empty-list", check: &CheckFound{Seq: 7, FetchErr: "offline"}, read: in, want: true},
		{name: "problem-empty-list", check: &CheckFound{Seq: 7, Problem: "temporary"}, read: in, want: true},
		{name: "successful-empty-list", check: &CheckFound{Seq: 7}, read: in, want: false},
		{name: "successful-still-selected", check: &CheckFound{Seq: 7, List: []ListEntry{{Commit: "c12"}}}, read: in, want: true}} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &CheckRecord{Owner: "forkpack/tool", Seq: 7, Read: tc.read, Check: tc.check, PatchFailure: pf}
			if got := rec.CurrentPatchFailure(in, "s") != nil; got != tc.want {
				t.Fatalf("CurrentPatchFailure present=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestPatchFailureCheckClearsOnlyAfterSuccessfulTargetRemoval(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	candidate := u.release(t, "", map[int]string{14: "fourteen", 25: "old tip"})
	series := u.series(t)
	want := u.want(t, "main", "head")
	checked := u.check(t, want, true)
	record, err := u.store.LoadCheckRecord(want.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Check.List) == 0 || record.Check.List[0].Commit != candidate {
		t.Fatalf("initial head list = %#v", record.Check.List)
	}
	record.PatchFailure = &PatchFailure{Owner: want.Owner, Inputs: checked.Inputs, Series: series.Digest,
		Target: record.Check.List[0], Kind: "conflict", Member: "0001-x.patch", Seq: record.Seq}
	if err := u.store.saveCheckRecord(record); err != nil {
		t.Fatal(err)
	}
	u.release(t, "", map[int]string{14: "fourteen", 25: "new tip"})
	u.now = u.now.Add(2 * time.Hour)
	updated := u.check(t, want, true)
	if updated.Err != nil {
		t.Fatal(updated.Err)
	}
	current, err := u.store.LoadCheckRecord(want.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if listHasCommit(current.Check.List, candidate) {
		t.Fatalf("candidate remains in successful list: %#v", current.Check.List)
	}
	if current.PatchFailure != nil {
		t.Fatalf("successful check retaining no target failed to clear authority: %#v", current.PatchFailure)
	}
}

func TestPatchFailureCleanDifferentTargetDoesNotClear(t *testing.T) {
	u, snap, targets := patchFailureSnapshotWithAuthority(t)
	if len(targets) < 2 {
		t.Fatalf("fixture targets = %#v", targets)
	}
	walk := WalkResult{Fit: 0, snapshot: cloneReplaySnapshot(&snap), Results: []ReplayResult{{Entry: targets[1], Clean: true}}}
	got := u.store.RecordReplay(snap, "yolo", walk, time.Unix(1_800_000_000, 0))
	if got.Err != nil || !got.Recorded {
		t.Fatalf("RecordReplay = %+v", got)
	}
	r, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if pf := r.CurrentPatchFailure(snap.Inputs, snap.Series); pf == nil || pf.Target.Commit != targets[0].Commit {
		t.Fatalf("different-target clean replay left failure %#v, want %s", pf, targets[0].Commit)
	}
}

func TestPatchFailureCleanSameTargetClears(t *testing.T) {
	u, snap, targets := patchFailureSnapshotWithAuthority(t)
	walk := WalkResult{Fit: 0, snapshot: cloneReplaySnapshot(&snap), Results: []ReplayResult{{Entry: targets[0], Clean: true}}}
	got := u.store.RecordReplay(snap, "yolo", walk, time.Unix(1_800_000_000, 0))
	if got.Err != nil || !got.Recorded {
		t.Fatalf("RecordReplay = %+v", got)
	}
	r, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if pf := r.CurrentPatchFailure(snap.Inputs, snap.Series); pf != nil {
		t.Fatalf("same-target clean replay left failure %#v", pf)
	}
}

func TestPatchFailureSelectedFirstFailureWins(t *testing.T) {
	u, _, snap := patchFailureSnapshotFixture(t)
	r, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Check.List) < 2 {
		t.Fatalf("fixture list = %#v", r.Check.List)
	}
	walk := WalkResult{Fit: -1, snapshot: cloneReplaySnapshot(&snap), Results: []ReplayResult{
		{Entry: r.Check.List[0], Conflict: &ReplayConflict{Member: "newest.patch"}},
		{Entry: r.Check.List[1], Conflict: &ReplayConflict{Member: "older.patch"}},
	}}
	got := u.store.RecordReplay(snap, "yolo", walk, time.Unix(1_800_000_000, 0))
	if got.Err != nil || !got.Recorded {
		t.Fatalf("RecordReplay = %+v", got)
	}
	current, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	pf := current.CurrentPatchFailure(snap.Inputs, snap.Series)
	if pf == nil || pf.Target.Commit != r.Check.List[0].Commit || pf.Member != "newest.patch" {
		t.Fatalf("selected failure = %#v, want first full-list target %s", pf, r.Check.List[0].Commit)
	}
	fresh, err := current.ReplaySnapshot(snap.Inputs, snap.Series)
	if err != nil {
		t.Fatal(err)
	}
	olderWalk := WalkResult{Fit: -1, snapshot: cloneReplaySnapshot(&fresh), Results: []ReplayResult{{
		Entry: r.Check.List[1], Conflict: &ReplayConflict{Member: "still-older.patch"},
	}}}
	if result := u.store.RecordReplay(fresh, "yolo", olderWalk, time.Unix(1_800_000_100, 0)); result.Err != nil {
		t.Fatal(result.Err)
	}
	current, err = u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if pf := current.CurrentPatchFailure(snap.Inputs, snap.Series); pf == nil || pf.Target.Commit != r.Check.List[0].Commit {
		t.Fatalf("older-target replay replaced newer selected authority: %#v", pf)
	}
}

func TestPatchFailureLegacyNewerConflictMaterializesOverOlderReplay(t *testing.T) {
	in := CheckInputs{Repo: "example/repo", Ref: "main", Follow: "release", Base: "base"}
	series := "series1"
	targets := []ListEntry{{Commit: "c12", Tag: "v1.2.0", Version: "1.2.0"}, {Commit: "c11", Tag: "v1.1.0", Version: "1.1.0"}}
	record := &CheckRecord{Owner: "forkpack/tool", Seq: 7, Read: in,
		Check: &CheckFound{Seq: 7, List: targets}, Good: &GoodBuild{Commit: "c12", Version: "1.2.0"},
		Outcomes: []EntryOutcome{{Commit: "c12", Kind: OutcomeConflict, Series: series,
			Member: "newer-legacy.patch", Paths: []string{"newer.txt"}}}}
	store, snapshot := savedReplaySnapshot(t, record, series)
	if record.PatchFailure != nil {
		t.Fatalf("fixture unexpectedly has typed authority: %#v", record.PatchFailure)
	}
	walk := WalkResult{Fit: 0, snapshot: cloneReplaySnapshot(&snapshot), Results: []ReplayResult{{
		Entry: targets[1], Conflict: &ReplayConflict{Member: "older-current.patch", Paths: []string{"older.txt"}},
	}}}
	got := store.RecordReplay(snapshot, "yolo", walk, time.Unix(1_800_000_000, 0))
	if got.Err != nil || !got.Recorded || got.Failure == nil || got.Failure.Target.Commit != targets[1].Commit {
		t.Fatalf("RecordReplay = %+v, want current operation failure at older target c11", got)
	}
	current, err := store.LoadCheckRecord(snapshot.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if failure := current.PatchFailure; failure == nil || failure.Target.Commit != "c12" ||
		failure.Member != "newer-legacy.patch" || len(failure.Paths) != 1 || failure.Paths[0] != "newer.txt" {
		t.Fatalf("materialized authority = %#v, want selected newer legacy conflict at c12", failure)
	}
	if failure := current.CurrentPatchFailure(in, series); failure == nil || failure.Target.Commit != "c12" {
		t.Fatalf("selected failure = %#v, want c12 despite Good cutting Candidates", failure)
	}
}

func TestPatchFailureAllSurveyCleanNewestThenOlderConflictSelectsOlder(t *testing.T) {
	in := CheckInputs{Repo: "example/repo", Ref: "main", Follow: "release", Base: "base"}
	series := "series1"
	targets := []ListEntry{{Commit: "c12", Tag: "v1.2.0", Version: "1.2.0"}, {Commit: "c11", Tag: "v1.1.0", Version: "1.1.0"}}
	record := &CheckRecord{Owner: "forkpack/tool", Seq: 7, Read: in,
		Check: &CheckFound{Seq: 7, List: targets},
		PatchFailure: &PatchFailure{Owner: "forkpack/tool", Inputs: in, Series: series, Target: targets[0],
			Kind: "conflict", Member: "newest-before-survey.patch", Paths: []string{"newer.txt"}, Seq: 7},
		Outcomes: []EntryOutcome{{Commit: "c12", Kind: OutcomeConflict, Series: series,
			Member: "newest-before-survey.patch", Paths: []string{"newer.txt"}}}}
	store, snapshot := savedReplaySnapshot(t, record, series)
	walk := WalkResult{Fit: 0, snapshot: cloneReplaySnapshot(&snapshot), Results: []ReplayResult{
		{Entry: targets[0], Clean: true},
		{Entry: targets[1], Conflict: &ReplayConflict{Member: "older-current.patch", Paths: []string{"older.txt"}}},
	}}
	got := store.RecordReplay(snapshot, "yolo", walk, time.Unix(1_800_000_000, 0))
	if got.Err != nil || !got.Recorded || got.Failure == nil || got.Failure.Target.Commit != "c11" {
		t.Fatalf("RecordReplay = %+v, want current operation failure at older target c11", got)
	}
	current, err := store.LoadCheckRecord(snapshot.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if failure := current.CurrentPatchFailure(in, series); failure == nil || failure.Target.Commit != "c11" {
		t.Fatalf("selected authority = %#v, want remaining older conflict at c11", failure)
	}
	var cleanNewest bool
	for _, outcome := range current.Outcomes {
		if outcome.Commit == "c12" && outcome.Series == series && outcome.Kind == OutcomeApplies {
			cleanNewest = true
		}
	}
	if !cleanNewest {
		t.Fatalf("newest clean result did not replace its conflict outcome: %#v", current.Outcomes)
	}
}

func savedReplaySnapshot(t *testing.T, record *CheckRecord, series string) (*Store, ReplaySnapshot) {
	t.Helper()
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	if err := store.saveCheckRecord(record); err != nil {
		t.Fatal(err)
	}
	snapshot, err := record.ReplaySnapshot(record.Read, series)
	if err != nil {
		t.Fatal(err)
	}
	return store, snapshot
}

func TestPatchFailureRekeyKeepsEquivalentFailureAndAttempt(t *testing.T) {
	in := CheckInputs{Repo: "r", Ref: "main", Base: "base"}
	record := &CheckRecord{
		PatchFailure: &PatchFailure{Inputs: in, Series: "old", Kind: "conflict"},
		ApplyErr:     &ApplyError{Seq: 3, Error: "opaque", Legacy: &LegacyReplayAttempt{Seq: 3, Series: "old", State: "unavailable"}},
	}
	if !record.RekeySeries("old", "old-recipe", "new", "new-recipe") {
		t.Fatal("equivalent series re-key reported no changes")
	}
	if record.PatchFailure.Series != "new" || record.ApplyErr.Legacy.Series != "new" {
		t.Fatalf("re-keyed authority = %+v, want new semantic digest", record)
	}
}

func patchFailureSnapshotFixture(t *testing.T) (*patchedUpstream, *Series, ReplaySnapshot) {
	t.Helper()
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	series := u.series(t)
	want := u.want(t, "main", "")
	checked := u.check(t, want, true)
	snapshot, err := checked.Record.ReplaySnapshot(checked.Inputs, series.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return u, series, snapshot
}

func patchFailureSnapshotWithAuthority(t *testing.T) (*patchedUpstream, ReplaySnapshot, []ListEntry) {
	t.Helper()
	u, series, initial := patchFailureSnapshotFixture(t)
	r, err := u.store.LoadCheckRecord(initial.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Check.List) == 0 {
		t.Fatal("fixture check has no selected targets")
	}
	targets := append([]ListEntry(nil), r.Check.List...)
	r.PatchFailure = &PatchFailure{Owner: initial.Owner, Inputs: initial.Inputs, Series: initial.Series,
		Target: targets[0], Kind: "conflict", Member: "0001-failed.patch", Paths: []string{"f.txt"}, Seq: initial.Seq}
	if err := u.store.saveCheckRecord(r); err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.ReplaySnapshot(initial.Inputs, series.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return u, snapshot, targets
}
func TestPatchFailureLegacyLocalOnlyConflict(t *testing.T) {
	u, series, snap, target := legacyProbeFixture(t, true, true)
	tripwire := filepath.Join(t.TempDir(), "fetch-called")
	u.store.Git = probeTripwireGit(t, tripwire, "")
	got := u.store.ReclassifyLegacyApplyError(snap, series, target, "yolo-v1", LegacyReplayOptions{Now: u.now})
	if got.State != "failure" || got.Failure == nil || got.Failure.Target.Commit != target.Commit ||
		!got.Recorded || got.Err != nil {
		t.Fatalf("legacy conflict result = %+v", got)
	}
	if _, err := os.Stat(tripwire); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local-only probe invoked fetch: %v", err)
	}
	record, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if record.ApplyErrAtLastCheck() != nil || record.CurrentPatchFailure(snap.Inputs, snap.Series) == nil {
		t.Fatalf("legacy failure did not replace the opaque diagnosis: %+v", record)
	}
}

func TestPatchFailureLegacyLocalOnlyClean(t *testing.T) {
	u, series, snap, target := legacyProbeFixture(t, false, true)
	got := u.store.ReclassifyLegacyApplyError(snap, series, target, "yolo-v1", LegacyReplayOptions{Now: u.now})
	if got.State != "clean" || got.Failure != nil || !got.Recorded || got.Err != nil {
		t.Fatalf("legacy clean result = %+v", got)
	}
	record, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if record.ApplyErrAtLastCheck() != nil || record.CurrentPatchFailure(snap.Inputs, snap.Series) != nil {
		t.Fatalf("clean probe retained opaque or typed failure: %+v", record)
	}
}

func TestPatchFailureLegacyUnavailableIsNotFatal(t *testing.T) {
	u, series, snap, target := legacyProbeFixture(t, false, false)
	tripwire := filepath.Join(t.TempDir(), "fetch-called")
	u.store.Git = probeTripwireGit(t, tripwire, "")
	if err := os.Rename(u.repo, u.repo+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(u.repo+".gone", u.repo) })
	got := u.store.ReclassifyLegacyApplyError(snap, series, target, "yolo-v1", LegacyReplayOptions{Now: u.now})
	if got.State != "unavailable" || got.Failure != nil || !got.Attempted || got.Err != nil ||
		!strings.Contains(got.Diagnostic, "could not") {
		t.Fatalf("missing-object result = %+v, want nonfatal unavailable", got)
	}
	if _, err := os.Stat(tripwire); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unavailable local-only probe invoked fetch: %v", err)
	}
	record, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if record.ApplyErrAtLastCheck() == nil || record.ApplyErr.Legacy == nil || record.ApplyErr.Legacy.State != "unavailable" {
		t.Fatalf("unavailable probe did not retain opaque diagnosis and its retry identity: %+v", record.ApplyErr)
	}
}

func TestPatchFailureLegacyUnavailableAttemptDoesNotRepeatReplay(t *testing.T) {
	u, series, snap, target := legacyProbeFixture(t, false, true)
	counter := filepath.Join(t.TempDir(), "merge-count")
	body := `case " $* " in *" merge-tree "*) echo x >> ` + shquote.Quote(counter) + `;; esac
` +
		`case " $* " in *" commit-tree "*) exit 1;; esac`
	u.store.Git = wrappedGit(t, body)
	first := u.store.ReclassifyLegacyApplyError(snap, series, target, "yolo-v1", LegacyReplayOptions{Now: u.now})
	if first.State != "unavailable" || !first.Attempted || first.Failure != nil {
		t.Fatalf("first failed local replay = %+v", first)
	}
	afterFirst, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	fresh := freshReplaySnapshot(t, u.store, snap.Owner, snap.Inputs, snap.Series)
	second := u.store.ReclassifyLegacyApplyError(fresh, series, target, "yolo-v1", LegacyReplayOptions{Now: u.now})
	afterSecond, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if second.State != "unavailable" || second.Attempted || string(afterSecond) != string(afterFirst) {
		t.Fatalf("repeat attempt replayed again: first=%+v second=%+v counts=%q/%q", first, second, afterFirst, afterSecond)
	}
}

func TestPatchFailureLegacyNewIdentityRetries(t *testing.T) {
	u, series, snap, target := legacyProbeFixture(t, false, true)
	counter := filepath.Join(t.TempDir(), "merge-count")
	body := `case " $* " in *" merge-tree "*) echo x >> ` + shquote.Quote(counter) + `;; esac
` +
		`case " $* " in *" commit-tree "*) exit 1;; esac`
	u.store.Git = wrappedGit(t, body)
	first := u.store.ReclassifyLegacyApplyError(snap, series, target, "yolo-v1", LegacyReplayOptions{Now: u.now})
	if first.State != "unavailable" || !first.Attempted {
		t.Fatalf("first attempt = %+v", first)
	}
	fresh := freshReplaySnapshot(t, u.store, snap.Owner, snap.Inputs, snap.Series)
	second := u.store.ReclassifyLegacyApplyError(fresh, series, target, "yolo-v2", LegacyReplayOptions{Now: u.now})
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if second.State != "unavailable" || !second.Attempted || len(data) <= 1 {
		t.Fatalf("new yolo identity did not retry: second=%+v merge count=%q", second, data)
	}
}

func TestPatchFailureLegacyGitVersionRetries(t *testing.T) {
	u, series, snap, target := legacyProbeFixture(t, false, true)
	counter := filepath.Join(t.TempDir(), "merge-count")
	probe := func(version string) string {
		return wrappedGit(t, `if [ "$1" = version ]; then echo 'git version `+version+`'; exit 0; fi
`+
			`case " $* " in *" merge-tree "*) echo x >> `+shquote.Quote(counter)+`;; esac
`+
			`case " $* " in *" commit-tree "*) exit 1;; esac`)
	}
	u.store.Git = probe("2.55.0")
	first := u.store.ReclassifyLegacyApplyError(snap, series, target, "same-yolo", LegacyReplayOptions{Now: u.now})
	if first.State != "unavailable" || !first.Attempted {
		t.Fatalf("first version attempt = %+v", first)
	}
	fresh := freshReplaySnapshot(t, u.store, snap.Owner, snap.Inputs, snap.Series)
	u.store.Git = probe("2.56.0")
	second := u.store.ReclassifyLegacyApplyError(fresh, series, target, "same-yolo", LegacyReplayOptions{Now: u.now})
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if second.State != "unavailable" || !second.Attempted || len(data) <= 1 {
		t.Fatalf("new git version did not retry: second=%+v merge count=%q", second, data)
	}
}

func TestPatchFailureLegacyOldGitIsUnavailable(t *testing.T) {
	u, series, snap, target := legacyProbeFixture(t, false, false)
	u.store.Git = wrappedGit(t, `if [ "$1" = version ]; then echo 'git version 2.39.0'; exit 0; fi`)
	got := u.store.ReclassifyLegacyApplyError(snap, series, target, "yolo", LegacyReplayOptions{Now: u.now})
	if got.State != "unavailable" || got.Failure != nil || !got.Attempted || got.Err != nil {
		t.Fatalf("old git legacy attempt = %+v", got)
	}
}

func TestPatchFailureLegacyProbeIsBounded(t *testing.T) {
	u, series, snap, target := legacyProbeFixture(t, false, false)
	pidFile := filepath.Join(t.TempDir(), "version-child.pid")
	childScript := `trap 'kill "$sleeper" 2>/dev/null; wait "$sleeper"; exit 0' TERM INT
sleep 30 &
sleeper=$!
printf '%s\\n' "$$" > "$1"
wait "$sleeper"`
	u.store.Git = wrappedGit(t, `if [ "$1" = version ]; then
  sh -c `+shquote.Quote(childScript)+` version-child `+shquote.Quote(pidFile)+` &
  wait
fi`)

	type probeResult struct {
		result  LegacyReplayResult
		elapsed time.Duration
	}
	done := make(chan probeResult, 1)
	start := time.Now()
	go func() {
		result := u.store.ReclassifyLegacyApplyError(snap, series, target, "yolo-v1", LegacyReplayOptions{Timeout: 100 * time.Millisecond, Now: u.now})
		done <- probeResult{result: result, elapsed: time.Since(start)}
	}()

	var childPID int
	cleanupChild := func() {
		if childPID != 0 {
			pid := childPID
			childPID = 0
			stopVersionProbeChild(t, pid)
		}
	}
	t.Cleanup(cleanupChild)
	pidDeadline := time.Now().Add(time.Second)
	for childPID == 0 && time.Now().Before(pidDeadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if _, err := fmt.Sscan(strings.TrimSpace(string(data)), &childPID); err != nil {
				t.Fatalf("parse version child pid: %v", err)
			}
			break
		}
		select {
		case got := <-done:
			t.Fatalf("version probe returned before starting its descendant: %+v", got)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		select {
		case got := <-done:
			t.Fatalf("version probe never wrote its descendant pid: %+v", got)
		case <-time.After(2 * time.Second):
			t.Fatal("version probe descendant did not start")
		}
	}

	var got probeResult
	select {
	case got = <-done:
	case <-time.After(2 * time.Second):
		cleanupChild()
		got = <-done
		t.Fatalf("legacy probe exceeded bound with descendant retaining stdout: %v", got.elapsed)
	}
	cleanupChild()
	if got.elapsed > 2*time.Second {
		t.Fatalf("legacy probe exceeded bound with descendant retaining stdout: %v", got.elapsed)
	}
	if got.result.State != "unavailable" || got.result.Failure != nil || !got.result.Attempted || got.result.Recorded || got.result.Err != nil {
		t.Fatalf("stalled version probe = %+v", got.result)
	}
	record, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if record.ApplyErr.Legacy != nil {
		t.Fatalf("cancelled probe was permanently recorded as unavailable: %+v", record.ApplyErr.Legacy)
	}
}

func stopVersionProbeChild(t *testing.T, pid int) {
	t.Helper()
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find version child %d: %v", pid, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("stop version child %d: %v", pid, err)
	}
	deadline := time.Now().Add(testsupport.ReadinessBudget(t))
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); errors.Is(err, os.ErrProcessDone) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill version child %d: %v", pid, err)
	}
	t.Fatalf("version child %d did not exit after cleanup", pid)
}

func TestPatchFailureLegacyHeldMirrorLockRemainsRetryable(t *testing.T) {
	u, series, snap, target := legacyProbeFixture(t, false, false)
	lock := filepath.Join(u.store.Dir, "locks", mirrorSlug(snap.Inputs.Repo)+".lock")
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }()
	got := u.store.ReclassifyLegacyApplyError(snap, series, target, "yolo", LegacyReplayOptions{Now: u.now})
	if got.State != "unavailable" || got.Failure != nil || !got.Attempted || got.Recorded || got.Err != nil {
		t.Fatalf("held mirror result = %+v", got)
	}
	record, err := u.store.LoadCheckRecord(snap.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if record.ApplyErr.Legacy != nil {
		t.Fatalf("held-lock attempt settled as unavailable: %+v", record.ApplyErr.Legacy)
	}
}

func TestPatchFailureClassifierNegativeClasses(t *testing.T) {
	cases := []struct {
		name string
		walk WalkResult
	}{{name: "walk-error", walk: WalkResult{Err: errors.New("prefetch failed")}},
		{name: "missing-blobs", walk: WalkResult{Results: []ReplayResult{{Entry: ListEntry{Commit: "c"}, Err: errors.New("missing blob")}}}},
		{name: "old-git", walk: WalkResult{Err: &GitTooOldError{Have: "2.39.0"}}},
		{name: "start-error", walk: WalkResult{Results: []ReplayResult{{Entry: ListEntry{Commit: "c"}, Err: errors.New("fork/exec git: no such file")}}}},
		{name: "cancel", walk: WalkResult{Err: context.Canceled}},
		{name: "budget", walk: WalkResult{Err: errors.New("series replay ran out of its 60s")}},
		{name: "export", walk: WalkResult{Results: []ReplayResult{{Entry: ListEntry{Commit: "c"}, Err: errors.New("copying patched tree")}}}},
		{name: "build-outcome", walk: WalkResult{Results: []ReplayResult{{Entry: ListEntry{Commit: "c"}, Clean: true}}}},
		{name: "unmodified", walk: WalkResult{Fit: 0, Results: []ReplayResult{{Entry: ListEntry{Commit: "c"}, Clean: true}}}}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.walk.PatchFailure(); got != nil {
				t.Fatalf("PatchFailure = %#v, want no fatal evidence", got)
			}
		})
	}
}

func legacyProbeFixture(t *testing.T, conflict, prefill bool) (*patchedUpstream, *Series, ReplaySnapshot, ListEntry) {
	t.Helper()
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	if conflict {
		u.release(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	}
	series := u.series(t)
	want := u.want(t, "main", "")
	checked := u.check(t, want, true)
	if len(checked.Record.Check.List) == 0 {
		t.Fatal("legacy probe fixture has no selected target")
	}
	target := checked.Record.Check.List[0]
	if prefill {
		addr := mustAddr(t, u.source("main"))
		walk := u.store.WalkSeries(addr.Repo, addr.Path, series, checked.Record.Check.List, WalkOptions{})
		if walk.Err != nil || walk.Base != nil {
			t.Fatalf("fixture prefill walk = %+v", walk)
		}
	}
	record, err := u.store.LoadCheckRecord(want.Owner)
	if err != nil {
		t.Fatal(err)
	}
	record.ApplyErr = &ApplyError{Seq: record.Seq, Error: "opaque historical replay error"}
	if err := u.store.saveCheckRecord(record); err != nil {
		t.Fatal(err)
	}
	snapshot, err := record.ReplaySnapshot(checked.Inputs, series.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return u, series, snapshot, target
}

func probeTripwireGit(t *testing.T, tripwire, extra string) string {
	t.Helper()
	return wrappedGit(t, `case " $* " in *" fetch "*) echo fetch > `+shquote.Quote(tripwire)+`; exit 77;; esac
`+extra)
}

func freshReplaySnapshot(t *testing.T, s *Store, owner string, inputs CheckInputs, series string) ReplaySnapshot {
	t.Helper()
	r, err := s.LoadCheckRecord(owner)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.ReplaySnapshot(inputs, series)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestPatchFailureFailedCheckDoesNotRemoveAuthority(t *testing.T) {
	in := CheckInputs{Repo: "example/repo", Ref: "main", Follow: "release", Base: "base"}
	failure := &PatchFailure{
		Owner: "forkpack/tool", Inputs: in, Series: "series1",
		Target: ListEntry{Commit: "c12", Tag: "v1.2.0"}, Kind: "conflict", Member: "0001-x.patch",
	}
	for _, tc := range []struct {
		name  string
		check *CheckFound
	}{{
		name:  "fetch-error",
		check: &CheckFound{Seq: 7, FetchErr: "offline"},
	}, {
		name:  "problem",
		check: &CheckFound{Seq: 7, Problem: "temporary check problem"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &CheckRecord{Owner: "forkpack/tool", Seq: 7, Read: in, Check: tc.check, PatchFailure: failure}
			got := rec.CurrentPatchFailure(in, "series1")
			if got == nil || got.Target.Commit != "c12" {
				t.Fatalf("CurrentPatchFailure = %#v, want retained failure at c12 through an unsuccessful check", got)
			}
		})
	}
}

func TestPatchFailureLegacyConflictIsSelectedBeforeGoodCut(t *testing.T) {
	in := CheckInputs{Repo: "example/repo", Ref: "main", Follow: "release", Base: "base"}
	c12 := ListEntry{Commit: "c12", Tag: "v1.2.0", Version: "1.2.0"}
	c11 := ListEntry{Commit: "c11", Tag: "v1.1.0", Version: "1.1.0"}
	rec := &CheckRecord{
		Owner: "forkpack/tool", Seq: 7, Read: in,
		Check: &CheckFound{Seq: 7, List: []ListEntry{c12, c11}},
		Good:  &GoodBuild{Commit: "c12", Version: "1.2.0"},
		Outcomes: []EntryOutcome{
			{Commit: "c11", Kind: OutcomeConflict, Series: "another-series", Member: "wrong.patch"},
			{Commit: "c12", Kind: OutcomeConflict, Series: "series1", Member: "0001-c12.patch", Paths: []string{"c12.txt"}},
			{Commit: "c11", Kind: OutcomeConflict, Series: "series1", Member: "0001-c11.patch", Paths: []string{"c11.txt"}},
		},
	}
	got := rec.CurrentPatchFailure(in, "series1")
	if got == nil || got.Target.Commit != "c12" || got.Member != "0001-c12.patch" || len(got.Paths) != 1 || got.Paths[0] != "c12.txt" {
		t.Fatalf("CurrentPatchFailure = %#v, want concrete c12 conflict selected before Good cuts Candidates", got)
	}
	if rec.CurrentPatchFailure(in, "different-series") != nil {
		t.Fatal("legacy conflict from another series became current")
	}
	// Outcome storage order does not replace the newest-first check-list order.
	rec.Outcomes[1], rec.Outcomes[2] = rec.Outcomes[2], rec.Outcomes[1]
	got = rec.CurrentPatchFailure(in, "series1")
	if got == nil || got.Target.Commit != "c12" {
		t.Fatalf("reversed outcomes selected %#v, want first matching conflict in check-list order c12", got)
	}
}

func TestPatchFailureFailedCheckDoesNotInventLegacyConflictOrder(t *testing.T) {
	in := CheckInputs{Repo: "example/repo", Ref: "main", Follow: "release", Base: "base"}
	rec := &CheckRecord{Owner: "forkpack/tool", Seq: 7, Read: in,
		Check:    &CheckFound{Seq: 7, FetchErr: "offline"},
		Outcomes: []EntryOutcome{{Commit: "c12", Kind: OutcomeConflict, Series: "s", Member: "0001-x.patch"}}}
	if got := rec.CurrentPatchFailure(in, "s"); got != nil {
		t.Fatalf("incomplete failed-check list invented selected target: %#v", got)
	}
	if len(rec.Outcomes) != 1 {
		t.Fatalf("legacy conflict history was discarded: %#v", rec.Outcomes)
	}
}

func TestPatchFailureFailedFetchPreservesLegacyConflictHistory(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	want := u.want(t, "main", "")
	checked := u.check(t, want, true)
	record, err := u.store.LoadCheckRecord(want.Owner)
	if err != nil {
		t.Fatal(err)
	}
	orphan := strings.Repeat("a", 40)
	record.SetOutcome(EntryOutcome{Commit: orphan, Kind: OutcomeConflict, Series: "legacy-series", Yolo: "old"})
	if err := u.store.saveCheckRecord(record); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(u.repo, u.repo+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(u.repo+".gone", u.repo) })
	failed := u.store.CheckPatched(want, CheckOptions{Force: true, Now: func() time.Time { return u.now }})
	if failed.Err != nil {
		t.Fatal(failed.Err)
	}
	if failed.Record.Check.FetchErr == "" {
		t.Fatalf("forced check did not record unavailable fetch: %+v", failed.Record.Check)
	}
	for _, outcome := range failed.Record.Outcomes {
		if outcome.Commit == orphan {
			return
		}
	}
	t.Fatalf("failed fetch pruned legacy history from an incomplete list: %#v (initial seq %d)", failed.Record.Outcomes, checked.Record.Seq)
}

func TestPatchFailureUnboundWalkCannotAdoptLatestCheck(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir, Getenv: noStagedTree}
	in := CheckInputs{Repo: "example/repo", Ref: "main", Follow: "release", Base: "base"}
	before := &CheckRecord{
		Owner: "forkpack/tool", Seq: 8, Read: in,
		Check: &CheckFound{Seq: 8, List: []ListEntry{{Commit: "c12"}}},
		PatchFailure: &PatchFailure{Owner: "forkpack/tool", Inputs: in, Series: "series1",
			Target: ListEntry{Commit: "c12"}, Kind: "conflict", Member: "0001-newer.patch", Seq: 8},
	}
	if err := s.saveCheckRecord(before); err != nil {
		t.Fatal(err)
	}
	path := s.CheckRecordPath(before.Owner)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	walk := WalkResult{Fit: 0, Results: []ReplayResult{{Entry: ListEntry{Commit: "c12"}, Clean: true}}}
	err = s.RecordWalk(before.Owner, "series1", "yolo", walk, time.Unix(123, 0))
	if err == nil {
		t.Fatal("RecordWalk accepted an unbound older walk by adopting the latest on-disk check identity")
	}
	after, readErr := os.ReadFile(filepath.Clean(path))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(original) {
		t.Fatalf("unbound walk changed record bytes\nbefore: %s\nafter:  %s", original, after)
	}
}

// A PATCH FAILURE'S ONE-LINE REASON names its target's commit once: the label already carries it,
// so a launch's refusal that quotes the reason does not say it twice.
func TestAPatchFailureReasonSaysItsCommitOnce(t *testing.T) {
	commit := "c5c0f6bd0123456789abcdef0123456789abcdef"
	pf := &PatchFailure{Target: ListEntry{Commit: commit, Tag: "v1.2.0"}, Kind: "conflict", Member: "0001-ten.patch"}
	if got, want := pf.Error(), "patch application failed at v1.2.0 (c5c0f6bd): 0001-ten.patch"; got != want {
		t.Errorf("reason %q, want %q", got, want)
	}
	pf.Member = ""
	if got, want := pf.Error(), "patch application failed at v1.2.0 (c5c0f6bd)"; got != want {
		t.Errorf("reason with no member %q, want %q", got, want)
	}
}
