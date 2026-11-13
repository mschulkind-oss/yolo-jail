package cli

import (
	"bytes"
	"context"
	"errors"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

func TestPreparationRegistrationGenerationAndSnapshotGuard(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scheduler")
	s := preparationSubscription{Key: "owner/tree", Runtime: "podman", Platform: "linux-amd64", InputIdentity: "input-one"}
	first, err := registerPreparationSubscriptions(root, []preparationSubscription{s, s})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Generation != 1 {
		t.Fatalf("%+v", first)
	}
	again, err := registerPreparationSubscriptions(root, []preparationSubscription{s})
	if err != nil || again[0] != first[0] {
		t.Fatalf("%+v %v", again, err)
	}
	snap := preparationSnapshot{Key: s.Key, Runtime: s.Runtime, Platform: s.Platform, InputIdentity: s.InputIdentity, Generation: first[0].Generation, Phase: "checking", WorkerIdentity: "lease-one"}
	if err := publishPreparationSnapshot(root, snap); err != nil {
		t.Fatal(err)
	}
	s.InputIdentity = "input-two"
	second, err := registerPreparationSubscriptions(root, []preparationSubscription{s})
	if err != nil || second[0].Generation != 2 {
		t.Fatalf("%+v %v", second, err)
	}
	if err := publishPreparationSnapshot(root, snap); !errors.Is(err, errPreparationGeneration) {
		t.Fatalf("stale snapshot admitted: %v", err)
	}
	if retained, err := readPreparationSnapshotFor(root, second[0]); !errors.Is(err, errPreparationGeneration) || retained.Generation != 1 {
		t.Fatalf("stale snapshot misrepresented or erased: %+v %v", retained, err)
	}
	committed := false
	if err := withPreparationGeneration(root, first[0], func() error { committed = true; return nil }); !errors.Is(err, errPreparationGeneration) || committed {
		t.Fatalf("stale admission: %v %v", committed, err)
	}
	if err := withPreparationGeneration(root, second[0], func() error { committed = true; return nil }); err != nil || !committed {
		t.Fatalf("current admission: %v %v", committed, err)
	}
}
func TestPreparationGenerationGuardNeverWaits(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scheduler")
	s := preparationSubscription{Key: "a", Runtime: "podman", Platform: "linux-amd64", InputIdentity: "one"}
	got, err := registerPreparationSubscriptions(root, []preparationSubscription{s})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := pidlock.Acquire(filepath.Join(root, "scheduler-state.lock"), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := withPreparationGeneration(root, got[0], func() error { t.Fatal("busy guard committed"); return nil }); !errors.Is(err, pidlock.ErrHeld) {
		t.Fatalf("%v", err)
	}
}
func TestPreparationCorruptSubscriptionNeverResets(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scheduler")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "scheduler.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := registerPreparationSubscriptions(root, []preparationSubscription{{Key: "a", Runtime: "podman", Platform: "linux", InputIdentity: "one"}})
	if err == nil {
		t.Fatal("corrupt metadata reset")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "broken" {
		t.Fatal("corrupt state overwritten")
	}
}

func TestPreparationStateConcurrentRegistrationCoalesces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scheduler")
	s := preparationSubscription{Key: "a", Runtime: "podman", Platform: "linux-amd64", InputIdentity: "one"}
	done := make(chan error, 8)
	for range 8 {
		go func() {
			got, err := registerPreparationSubscriptions(root, []preparationSubscription{s})
			if err == nil && (len(got) != 1 || got[0].Generation != 1) {
				err = errors.New("duplicate registration advanced generation")
			}
			done <- err
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPreparationPublicRegistrationRetainsOldGenerationWithoutAdmission(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.launch(t, "podman")
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":"next-launch"}`)
	stubPreparationWorkerStart(t)
	f := fx.fork(t)
	req := run.ForkBuildRequest{Runtime: "podman", Platform: patchedTestPlatform}
	if err := registerProgramPreparation([]packload.ForkPin{{Fork: f}}, req, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := preparationUpdateRecord(f.Key(), req.Runtime, req.Platform, func(r *preparationRecord) {
		r.Phase, r.Reason, r.Log = "failed", "original failure", "original.log"
	}); err != nil {
		t.Fatal(err)
	}
	before, err := readPreparationSnapshot(f.Key())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fx.manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, fx.manifest, strings.Replace(string(data), "sh build.sh", "sh changed-build.sh", 1))
	f = fx.fork(t)
	if err := registerProgramPreparation([]packload.ForkPin{{Fork: f}}, req, io.Discard); err != nil {
		t.Fatal(err)
	}
	state, err := loadPreparationState()
	if err != nil {
		t.Fatal(err)
	}
	i := preparationRecordIndex(state, f.Key(), req.Runtime, req.Platform)
	if i < 0 {
		t.Fatal("registration lost selected declaration")
	}
	r := state.Subscriptions[i]
	if r.Generation != before.Generation+1 || r.Phase != "pending" || r.Reason != "" || r.Log != "" || r.Retained == nil || *r.Retained != before {
		t.Fatalf("registration relabelled or erased diagnostic evidence: before=%+v current=%+v", before, r)
	}
	committed := false
	if err := preparationAdmissionGuardUnder(context.Background(), f.Key(), req.Runtime, req.Platform, before.Generation)(func() error { committed = true; return nil }); !errors.Is(err, errPreparationAdmissionStale) || committed {
		t.Fatalf("old declaration admitted: %v committed=%v", err, committed)
	}
	var out bytes.Buffer
	statusLine := f.Key() + " [" + req.Runtime + "/" + req.Platform + "]"
	if rc := printPreparationStatus(&out, io.Discard, false, ""); rc != 0 || !strings.Contains(out.String(), statusLine) || !strings.Contains(out.String(), "retained diagnostic generation") || !strings.Contains(out.String(), "previous declaration, not current admission") || !strings.Contains(out.String(), "original failure") {
		t.Fatalf("public all-record status misrepresented fixture diagnostics %s: rc=%d %s", statusLine, rc, out.String())
	}
}

// The design's explicit-stop row: after `yolo pack prepare stop` the worker stays stopped until
// a launch or `yolo pack prepare start`, and status names the stop rather than reading as a
// crash or an idle exit (seen missing in the 2026-11-13 runtime acceptance).
func TestPreparationStatusNamesAnExplicitStop(t *testing.T) {
	withHomeAndCwd(t)
	stoppedAt := time.Date(2026, 11, 13, 11, 12, 24, 0, time.UTC)
	if err := withPreparationState(func(s *preparationState) error {
		s.Subscriptions = []preparationRecord{{Key: "owner/tree", Runtime: "podman", Platform: "linux/amd64", Generation: 1, Active: true, Phase: "admitted"}}
		s.Worker = preparationWorker{Identity: "stopped-incarnation", StopRequested: true, StoppedAt: stoppedAt}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if rc := printPreparationStatus(&out, io.Discard, false, ""); rc != 0 {
		t.Fatalf("status rc=%d: %s", rc, out.String())
	}
	want := "Tree preparation worker: not running; stopped by `yolo pack prepare stop` at " + stoppedAt.Local().Format(time.RFC3339) + "; run `yolo pack prepare start` to reauthorize and resume"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("status did not name the explicit stop; want %q in:\n%s", want, out.String())
	}

	// A worker that exited on its own (no stop asked) keeps the plain line.
	if err := withPreparationState(func(s *preparationState) error {
		s.Worker = preparationWorker{Identity: "idle-incarnation", StoppedAt: stoppedAt}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	_ = printPreparationStatus(&out, io.Discard, false, "")
	if !strings.Contains(out.String(), "Tree preparation worker: not running; run `yolo pack prepare start`") || strings.Contains(out.String(), "stopped by") {
		t.Fatalf("an idle exit read as an explicit stop:\n%s", out.String())
	}
}
