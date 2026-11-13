package cli

import (
	"context"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitOrdinaryTimerPhase(t *testing.T, key, phase string) preparationRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		records, err := preparationSubscriptions()
		if err == nil {
			for _, r := range records {
				if r.Key == key && r.Phase == phase {
					return r
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("production timer did not reach %s for %s", phase, key)
	return preparationRecord{}
}

func TestSelectedNPMTimerActualDispatchPreparesWithoutMovingRunningHome(t *testing.T) {
	f := newSelectedNPMFixture(t)
	req := selectedNPMUser(t, f, true)
	stubPreparationWorkerStart(t)
	workers := selectedNPMSharedWorker(t)
	deliveries, err := prepareOrdinaryForLaunch(req, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	old := deliveries[f.in.Bin]
	home := t.TempDir()
	if rc := materializeCapture(materializeArgs{store: f.store.Dir, home: home, bin: f.in.Bin, compatibility: old.Compatibility}, io.Discard); rc != 0 {
		t.Fatal(rc)
	}
	// Age only the synthetic check stamp; the real persistent scheduler and worker
	// must resolve/admit a next-launch generation, never execute the target or edit home.
	if err := withNPMCaptureRecord(context.Background(), f.store, f.in, func(r *npmCaptureRecord) error { r.CheckedAt = r.CheckedAt.Add(-2 * time.Hour); return nil }); err != nil {
		t.Fatal(err)
	}
	f.version(t, "2.0.0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan error, 1)
	go func() { done <- runPreparationScheduler(ctx, ticks, io.Discard) }()
	ticks <- time.Now()
	key := ordinaryProgramKey(f.in)
	snapshot := waitOrdinaryTimerPhase(t, key, "admitted")
	if snapshot.ServingEntry == "" || f.resolves.Load() != 2 || f.installs.Load() != 2 {
		t.Fatalf("real timer counts %d/%d %+v", f.resolves.Load(), f.installs.Load(), snapshot)
	}
	got, err := exec.Command(filepath.Join(home, ".npm-global/bin/probe")).Output()
	if err != nil || string(got) != "1.0.0\n" {
		t.Fatalf("timer moved running home: %q %v", got, err)
	}
	// Effective policy revocation removes the subscription through the same timer
	// loop, not a second watcher or a sleep-until-next-launch pseudo-task.
	configPath := paths.UserConfigPath()
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, configPath, strings.Replace(string(data), `"agent_updates":true`, `"agent_updates":false`, 1))
	ticks <- time.Now().Add(time.Second)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timer did not retire its revoked ordinary subscription")
	}
	workers.Wait()
	records, err := preparationSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Key == key && r.Active {
			t.Fatal("revoked ordinary timer demand stayed active")
		}
		// Status names the policy and its next steps, not a fresh launch that under the same
		// policy registers nothing (seen in the 2026-11-13 runtime acceptance).
		if r.Key == key && (r.Phase != "inactive" || !strings.Contains(r.Reason, "off for pack") || !strings.Contains(r.Reason, "agent_updates") || strings.Contains(r.Reason, "use a fresh launch")) {
			t.Fatalf("policy-off timer record misnamed: phase=%q reason=%q", r.Phase, r.Reason)
		}
	}
	if f.resolves.Load() != 2 || f.installs.Load() != 2 {
		t.Fatal("revoked timer performed another check/build")
	}
}
