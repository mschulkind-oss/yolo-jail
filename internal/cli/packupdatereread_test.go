package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// packUpdateRereadFixture is a clean fork with one newer release; change simulates another launch of
// the same fork rechecking it, which moves the check authority `yolo pack update` bound its replay to.
func packUpdateRereadFixture(t *testing.T) (change func()) {
	t.Helper()
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, _ := patchedActorLaunch(t)
	fx.commit(t, "v1.2.0", cleanRelease)
	fx.later(2 * time.Hour)
	f := fx.fork(t)
	series, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	return func() {
		fx.later(time.Minute)
		before, _ := store.LoadCheckRecord(f.Key())
		r := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: fx.clock})
		if r.Err != nil || r.Record == nil || (before != nil && r.Record.Seq == before.Seq) {
			t.Fatalf("the other launch's check did not move the authority: %+v", r)
		}
	}
}

func TestPackUpdateRereadsAuthorityAnotherLaunchChanged(t *testing.T) {
	change := packUpdateRereadFixture(t)
	calls := 0
	replayRecordHook = func() {
		calls++
		if calls == 1 {
			change()
		}
	}
	t.Cleanup(func() { replayRecordHook = nil })
	rc, out, errw := packVerb(t, "update")
	if rc != 0 || strings.Contains(errw, "run `yolo pack update` again") {
		t.Fatalf("one concurrent change stopped pack update: rc=%d\n%s\n%s", rc, out, errw)
	}
	if calls != 2 {
		t.Errorf("update recorded its replay %d times, want 2 (one stale, one reread)", calls)
	}
	if n := strings.Count(out, "applies — "); n != 1 {
		t.Errorf("the replay was reported %d times, want once:\n%s", n, out)
	}
}

func TestPackUpdateStopsWhenAuthorityKeepsChanging(t *testing.T) {
	change := packUpdateRereadFixture(t)
	calls := 0
	replayRecordHook = func() { calls++; change() }
	t.Cleanup(func() { replayRecordHook = nil })
	rc, out, errw := packVerb(t, "update")
	if rc == 0 || !strings.Contains(errw, "replay authority changed during its forced check; run `yolo pack update` again") {
		t.Fatalf("a never-settling authority must stop with the next step: rc=%d\n%s\n%s", rc, out, errw)
	}
	if calls != 1+maxReplayRereads {
		t.Errorf("update attempted %d times, want %d", calls, 1+maxReplayRereads)
	}
	if strings.Contains(out, "applies — ") {
		t.Errorf("a stopped update reported a replay:\n%s", out)
	}
}
