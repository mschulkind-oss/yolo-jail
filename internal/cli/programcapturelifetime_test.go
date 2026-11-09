package cli

import (
	"context"
	"errors"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// selectedNPMWaitFile waits for the producer to reach the barrier file at path. The wait is on
// that event, under testHangGuard only; a fixed few seconds failed a healthy producer at a
// load average of ~40. A requester in done that returns first fails the wait at once with its
// error, since its producer can then never reach the barrier.
func selectedNPMWaitFile(t *testing.T, path string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(testHangGuard(t))
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("the requester returned before its producer reached the barrier %s: %v", path, err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("private producer did not reach its controlled barrier", path)
}

// selectedNPMWaitLeases waits for the shared demand count to settle at want, under
// testHangGuard only.
func selectedNPMWaitLeases(t *testing.T, dir string, want int) {
	t.Helper()
	deadline := time.Now().Add(testHangGuard(t))
	for time.Now().Before(deadline) {
		var records []livePreparationDemand
		err := withDemandState(context.Background(), dir, false, func() error { var err error; records, err = livePreparationDemands(dir); return err })
		if err == nil && len(records) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shared ordinary demand count did not settle", want)
}

func TestSelectedNPMSharedSecondDemandKeepsOriginalOwnedProducer(t *testing.T) {
	f := newSelectedNPMFixture(t)
	req := selectedNPMUser(t, f, false)
	workers := selectedNPMSharedWorker(t)
	writeFile(t, filepath.Join(f.root, "resolve-wait"), "1")
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	wait := func(ctx context.Context) error {
		_, err := waitOrdinaryPreparation(ctx, f.in, req.Fingerprint, req.Runtime, req.Platform, false, io.Discard, foregroundOrdinaryGuard(ctx, req, f.in))
		return err
	}
	go func() { firstDone <- wait(first) }()
	selectedNPMWaitFile(t, filepath.Join(f.root, "resolve-started"), firstDone)
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	go func() { secondDone <- wait(second) }()
	dir := sharedDemandDir(ordinaryProgramKey(f.in), req.Runtime, req.Platform)
	selectedNPMWaitLeases(t, dir, 2)
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first requester: %v", err)
	}
	selectedNPMWaitLeases(t, dir, 1)
	time.Sleep(200 * time.Millisecond)
	if f.resolves.Load() != 1 {
		t.Fatal("second demand restarted the original producer")
	}
	if err := os.Remove(filepath.Join(f.root, "resolve-wait")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testHangGuard(t)):
		t.Fatal("remaining requester did not complete")
	}
	workers.Wait()
	if f.resolves.Load() != 1 || f.installs.Load() != 1 {
		t.Fatalf("coalesced cold counts %d/%d", f.resolves.Load(), f.installs.Load())
	}
	record, err := readNPMCaptureRecord(f.store, f.in)
	if err != nil || record.Entry == "" {
		t.Fatalf("second demand did not admit: %+v %v", record, err)
	}
}

func TestSelectedNPMSharedZeroDemandAndRevocationDoNotAdmit(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "zero-demand", true: "selection-revoked"}[revoke], func(t *testing.T) {
			f := newSelectedNPMFixture(t)
			req := selectedNPMUser(t, f, false)
			workers := selectedNPMSharedWorker(t)
			writeFile(t, filepath.Join(f.root, "resolve-wait"), "1")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := waitOrdinaryPreparation(ctx, f.in, req.Fingerprint, req.Runtime, req.Platform, true, io.Discard, foregroundOrdinaryGuard(ctx, req, f.in))
				done <- err
			}()
			selectedNPMWaitFile(t, filepath.Join(f.root, "resolve-started"), done)
			if revoke {
				writeFile(t, paths.UserConfigPath(), `{"packs":[]}`)
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("revoked/interrupted ordinary request succeeded")
				}
			case <-time.After(testHangGuard(t)):
				t.Fatal("owned producer cancellation did not settle")
			}
			workers.Wait()
			record, err := readNPMCaptureRecord(f.store, f.in)
			if err != nil {
				t.Fatal(err)
			}
			if record.Entry != "" || record.Completed != 0 || f.installs.Load() != 0 {
				t.Fatalf("late/cancelled work admitted bytes: %+v", record)
			}
		})
	}
}
