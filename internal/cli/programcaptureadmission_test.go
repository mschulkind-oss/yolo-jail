package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
)

func programAdmissionFixture(t *testing.T) (*capture.Store, capture.ProgramInputs, []string, func(context.Context, string) error) {
	t.Helper()
	if goruntime.GOOS != "linux" {
		t.Skip("artifact ownership requires Linux")
	}
	t.Setenv("HOME", t.TempDir())
	store := &capture.Store{Dir: t.TempDir()}
	inputs := capture.ProgramInputs{Owner: "fixture", Bin: "probe", Declared: "fixture@1", Resolver: "npm", Release: "1", Recipe: "fixture-recipe", Platform: capture.Platform(), Runtime: "fixture-node", NodeABI: "127"}
	artifacts := []string{".local/bin/probe"}
	producer := func(ctx context.Context, staging string) error {
		_, err := capture.Run(capture.Options{Context: ctx, Home: t.TempDir(), Out: filepath.Join(staging, captureOutLeaf), Artifacts: artifacts, ScanContentRefs: true, Command: []string{"/bin/sh", "-c", `mkdir -p "$HOME/.local/bin"; printf program > "$HOME/.local/bin/probe"`}})
		return err
	}
	return store, inputs, artifacts, producer
}

func TestProgramCaptureAdmissionConcurrentWinnerRereadAndCancelledWaiter(t *testing.T) {
	store, inputs, artifacts, produce := programAdmissionFixture(t)
	started, finish := make(chan struct{}), make(chan struct{})
	var acts atomic.Int32
	producer := func(ctx context.Context, staging string) error {
		acts.Add(1)
		close(started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-finish:
		}
		return produce(ctx, staging)
	}
	guard := func(commit func() error) error { return commit() }
	type result struct {
		entry *capture.Entry
		err   error
	}
	done := make(chan result, 2)
	go func() {
		e, err := prepareProgramCapture(context.Background(), store, inputs, artifacts, producer, guard)
		done <- result{e, err}
	}()
	<-started
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepareProgramCapture(cancelled, store, inputs, artifacts, producer, guard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	go func() {
		e, err := prepareProgramCapture(context.Background(), store, inputs, artifacts, producer, guard)
		done <- result{e, err}
	}()
	close(finish)
	var key string
	for i := 0; i < 2; i++ {
		select {
		case r := <-done:
			if r.err != nil {
				t.Fatal(r.err)
			}
			if key != "" && key != r.entry.Key {
				t.Fatal("waiters received different entries")
			}
			key = r.entry.Key
		case <-time.After(5 * time.Second):
			t.Fatal("admission stalled")
		}
	}
	if acts.Load() != 1 {
		t.Fatalf("%d producers for one identity", acts.Load())
	}
	// A later workspace demand is an admitted read, not a third installer.
	if _, err := prepareProgramCapture(context.Background(), store, inputs, artifacts, func(context.Context, string) error { t.Fatal("warm reuse installed again"); return nil }, guard); err != nil {
		t.Fatal(err)
	}
}

func TestProgramCaptureAdmissionRejectsStaleInputBeforePublication(t *testing.T) {
	store, inputs, artifacts, produce := programAdmissionFixture(t)
	var mu sync.Mutex
	current := true
	stale := errors.New("selected inputs changed — retry preparation")
	guard := func(commit func() error) error {
		mu.Lock()
		defer mu.Unlock()
		if !current {
			return stale
		}
		return commit()
	}
	producer := func(ctx context.Context, staging string) error {
		if err := produce(ctx, staging); err != nil {
			return err
		}
		mu.Lock()
		current = false
		mu.Unlock()
		return nil
	}
	if _, err := prepareProgramCapture(context.Background(), store, inputs, artifacts, producer, guard); !errors.Is(err, stale) {
		t.Fatalf("stale output admitted: %v", err)
	}
	keys, err := store.EntryKeys()
	if err != nil || len(keys) != 0 {
		t.Fatalf("stale entries: %v %v", keys, err)
	}
}

func TestProgramCaptureAdmissionRetainsUnknownRuntimeOutput(t *testing.T) {
	store, inputs, artifacts, produce := programAdmissionFixture(t)
	oldProbe, oldWait := probeForkBuildContainer, forkBuildGoneWait
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) { return false, false }
	forkBuildGoneWait = 0
	t.Cleanup(func() { probeForkBuildContainer = oldProbe; forkBuildGoneWait = oldWait })
	producer := func(ctx context.Context, staging string) error {
		if err := writeForkBuildRuntime(staging, "podman", ""); err != nil {
			return err
		}
		return produce(ctx, staging)
	}
	guard := func(commit func() error) error { return commit() }
	if _, err := prepareProgramCapture(context.Background(), store, inputs, artifacts, producer, guard); !errors.Is(err, errForkBuildWorkspaceRetained) {
		t.Fatalf("unknown runtime output admitted: %v", err)
	}
	if _, err := prepareProgramCapture(context.Background(), store, inputs, artifacts, func(context.Context, string) error { t.Fatal("unknown runtime staging was replaced"); return nil }, guard); !errors.Is(err, errForkBuildWorkspaceRetained) {
		t.Fatalf("unknown retry: %v", err)
	}
	keys, err := store.EntryKeys()
	if err != nil || len(keys) != 0 {
		t.Fatalf("unknown runtime entries: %v %v", keys, err)
	}
}

func TestProgramCaptureAdmissionDifferentIdentitiesShareIdenticalCASBytes(t *testing.T) {
	store, inputs, artifacts, produce := programAdmissionFixture(t)
	other := inputs
	other.NodeABI = "128"
	var ready sync.WaitGroup
	ready.Add(2)
	producer := func(ctx context.Context, staging string) error {
		if err := produce(ctx, staging); err != nil {
			return err
		}
		ready.Done()
		ready.Wait()
		return nil
	}
	guard := func(commit func() error) error { return commit() }
	done := make(chan error, 2)
	for _, in := range []capture.ProgramInputs{inputs, other} {
		go func() {
			_, err := prepareProgramCapture(context.Background(), store, in, artifacts, producer, guard)
			done <- err
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("identical content admission stalled")
		}
	}
	selected, err := capture.Select(store, captureRecords)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 {
		t.Fatalf("CAS lost a receipt: %+v", selected)
	}
	keys, err := store.EntryKeys()
	if err != nil || len(keys) != 1 {
		t.Fatalf("duplicate bytes: %v %v", keys, err)
	}
}

func TestProgramCaptureAdmissionRejectsRetainedMetadataConflict(t *testing.T) {
	for _, conflict := range []string{"platform", "legacy-relocation", "artifacts", "relocation-refs"} {
		t.Run(conflict, func(t *testing.T) {
			store, inputs, artifacts, produce := programAdmissionFixture(t)
			staged, err := store.Stage("legacy")
			if err != nil {
				t.Fatal(err)
			}
			if err := produce(context.Background(), staged); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(staged, captureOutLeaf)
			m, err := capture.ReadManifest(out)
			if err != nil {
				t.Fatal(err)
			}
			switch conflict {
			case "platform":
				m.Platform = "darwin/arm64"
			case "legacy-relocation":
				m.RefScan = capture.RefScanSymlinks
				m.Relocatable = false
			case "relocation-refs":
				m.AbsoluteRefs = []capture.AbsoluteRef{{Path: ".local/bin/probe", Kind: capture.RefFileContent, Value: m.Home}}
			case "artifacts":
				m.Surfaces = []string{".local"}
			}
			if err := capture.WriteManifest(out, m); err != nil {
				t.Fatal(err)
			}
			existing, err := store.AdmitEntry(out)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(capture.ManifestPath(existing.Root))
			if err != nil {
				t.Fatal(err)
			}
			_, err = prepareProgramCapture(context.Background(), store, inputs, artifacts, produce, func(commit func() error) error { return commit() })
			if err == nil {
				t.Fatal("incompatible canonical metadata published new identity")
			}
			selected, err := capture.Select(store, captureRecords)
			if err != nil || len(selected) != 0 {
				t.Fatalf("invalid identity published: %v %v", selected, err)
			}
			after, err := os.ReadFile(capture.ManifestPath(existing.Root))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("legacy canonical metadata overwritten")
			}
		})
	}
}

func TestProgramCaptureAdmissionLinuxDarwinSameBytesRetainsOldReceipt(t *testing.T) {
	store, inputs, artifacts, produce := programAdmissionFixture(t)
	guard := func(commit func() error) error { return commit() }
	first, err := prepareProgramCapture(context.Background(), store, inputs, artifacts, produce, guard)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(capture.ReceiptsPath(first.Root))
	if err != nil {
		t.Fatal(err)
	}
	other := inputs
	other.Platform = "darwin/arm64"
	producer := func(ctx context.Context, staging string) error {
		if err := produce(ctx, staging); err != nil {
			return err
		}
		out := filepath.Join(staging, captureOutLeaf)
		m, err := capture.ReadManifest(out)
		if err != nil {
			return err
		}
		m.Platform = other.Platform
		return capture.WriteManifest(out, m)
	}
	if _, err := prepareProgramCapture(context.Background(), store, other, artifacts, producer, guard); err == nil {
		t.Fatal("same bytes admitted incompatible Darwin identity")
	}
	after, err := os.ReadFile(capture.ReceiptsPath(first.Root))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("old receipt changed")
	}
	if _, err := prepareProgramCapture(context.Background(), store, inputs, artifacts, func(context.Context, string) error { t.Fatal("old compatible winner reinstalled"); return nil }, guard); err != nil {
		t.Fatal(err)
	}
}
