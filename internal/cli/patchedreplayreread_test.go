package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// The newer release the fixtures check: one the series conflicts with, and one it applies to.
var (
	conflictingRelease = map[int]string{14: "fourteen", 11: "eleven"}
	cleanRelease       = map[int]string{14: "fourteen", 20: "twenty"}
)

// replayRereadFixture is a clean fork with one newer release, its check recorded, and an advance
// bound to it; change simulates another yolo process rechecking the same fork.
func replayRereadFixture(t *testing.T, newer map[int]string) (a *advance, list []packsrc.ListEntry, change func()) {
	t.Helper()
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, _ := patchedActorLaunch(t)
	fx.commit(t, "v1.2.0", newer)
	fx.later(2 * time.Hour)
	f := fx.fork(t)
	store := patchedAdvanceStore(true)
	series, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	check := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: fx.clock})
	if check.Err != nil || check.Record == nil || check.Record.Check == nil {
		t.Fatalf("prepare the check: %+v", check)
	}
	var out bytes.Buffer
	a, early := newAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		out: &out, errw: &out})
	if early != nil {
		t.Fatal("could not initialize the actor")
	}
	a.seq = check.Record.Seq
	change = func() {
		fx.later(time.Minute)
		r := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: fx.clock})
		if r.Err != nil || r.Record == nil || r.Record.Seq == check.Record.Seq {
			t.Fatalf("the other process's check did not move the authority: %+v", r)
		}
	}
	return a, check.Record.Check.List, change
}

func TestWalkRereadsAuthorityAnotherProcessChanged(t *testing.T) {
	a, list, change := replayRereadFixture(t, conflictingRelease)
	calls := 0
	replayRecordHook = func() {
		calls++
		if calls == 1 {
			change()
		}
	}
	t.Cleanup(func() { replayRecordHook = nil })
	w := a.walk(list, true)
	if errors.Is(w.Err, packsrc.ErrStaleReplay) {
		t.Fatalf("one concurrent change stopped the launch: %v", w.Err)
	}
	if calls != 2 {
		t.Errorf("walk recorded %d times, want 2 (one stale, one reread)", calls)
	}
}

func TestWalkStopsWhenAuthorityKeepsChanging(t *testing.T) {
	a, list, change := replayRereadFixture(t, conflictingRelease)
	calls := 0
	replayRecordHook = func() { calls++; change() }
	t.Cleanup(func() { replayRecordHook = nil })
	w := a.walk(list, true)
	if !errors.Is(w.Err, packsrc.ErrStaleReplay) {
		t.Fatalf("a never-settling authority must stop with the stale error, got %v", w.Err)
	}
	if calls != 1+maxReplayRereads {
		t.Errorf("walk attempted %d times, want %d", calls, 1+maxReplayRereads)
	}
}

// sourceRereadFixture walks the fixture's fork once, as a launch does before it builds, and returns
// the build that walk selected.
func sourceRereadFixture(t *testing.T) (a *advance, b forkBuild, change func()) {
	t.Helper()
	a, list, change := replayRereadFixture(t, cleanRelease)
	w := a.walk(list, true)
	if w.Fit < 0 || a.replayBinding == nil {
		t.Fatalf("the first walk found no fit: %+v", w)
	}
	entry := w.Results[w.Fit].Entry
	return a, forkBuild{Fork: a.f, Commit: entry.Commit, Platform: patchedTestPlatform, Series: a.series, Entry: entry}, change
}

func TestSourceReplayRereadsAuthorityAnotherProcessChanged(t *testing.T) {
	a, b, change := sourceRereadFixture(t)
	calls := 0
	replayRecordHook = func() {
		calls++
		if calls == 1 {
			change()
		}
	}
	t.Cleanup(func() { replayRecordHook = nil })
	dst := filepath.Join(t.TempDir(), "source")
	if _, err := replayIntoSource(a.packs, b, dst, 0, a.sourceReplay(b)); err != nil {
		t.Fatalf("one concurrent change stopped the build's source: %v", err)
	}
	if calls != 2 {
		t.Errorf("source replay recorded %d times, want 2 (one stale, one reread)", calls)
	}
}

func TestSourceReplayStopsWhenAuthorityKeepsChanging(t *testing.T) {
	a, b, change := sourceRereadFixture(t)
	calls := 0
	replayRecordHook = func() { calls++; change() }
	t.Cleanup(func() { replayRecordHook = nil })
	dst := filepath.Join(t.TempDir(), "source")
	_, err := replayIntoSource(a.packs, b, dst, 0, a.sourceReplay(b))
	if !errors.Is(err, packsrc.ErrStaleReplay) {
		t.Fatalf("a never-settling authority must stop with the stale error, got %v", err)
	}
	if calls != 1+maxReplayRereads {
		t.Errorf("source replay attempted %d times, want %d", calls, 1+maxReplayRereads)
	}
}
