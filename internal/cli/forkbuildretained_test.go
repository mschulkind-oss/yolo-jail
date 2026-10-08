//go:build linux

package cli

// forkbuildretained_test.go pins how a fork build treats a capture jail that is not yet known gone
// (docs/design/pi-startup-cancellation.md §3): a finished build waits out late `--rm` removal before
// it gives up, and a build whose jail stays present is RETAINED, never a failed build, so a patched
// advance records nothing against the commit and the next launch builds it.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// withForkBuildGoneWait bounds the post-build wait for container removal for one test.
func withForkBuildGoneWait(t *testing.T, d time.Duration) {
	t.Helper()
	prev := forkBuildGoneWait
	forkBuildGoneWait = d
	t.Cleanup(func() { forkBuildGoneWait = prev })
}

// withForkBuildProbe replaces the original-backend presence probe for one test.
func withForkBuildProbe(t *testing.T, probe func(cname, rt string) (bool, bool)) {
	t.Helper()
	prev := probeForkBuildContainer
	probeForkBuildContainer = func(cname, rt string, _ time.Duration) (bool, bool) { return probe(cname, rt) }
	t.Cleanup(func() { probeForkBuildContainer = prev })
}

func directForkBuildFixture(t *testing.T) (forkBuild, *capture.Store, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	cname := runtime.FromWorkspace(staging)
	withFakeCaptureJail(t, func(o run.Options) int {
		if err := o.OnRuntimeResolved("podman"); err != nil {
			t.Fatalf("persist runtime: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(paths.AgentsDir(), cname), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkBuildFixtureOutput(t, o.Workspace)
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "fixture-image\n")
		return 0
	})
	return b, store, staging, cname
}

// A good build is admitted when its container leaves the runtime a little after the keeper exits.
func TestAFinishedForkBuildWaitsOutLateContainerRemoval(t *testing.T) {
	b, store, staging, _ := directForkBuildFixture(t)
	withForkBuildGoneWait(t, 10*time.Second)
	var mu sync.Mutex
	probes := 0
	withForkBuildProbe(t, func(string, string) (bool, bool) {
		mu.Lock()
		defer mu.Unlock()
		probes++
		return probes < 3, true // present twice, then gone
	})
	entry, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman"}, io.Discard, io.Discard, false)
	if err != nil || entry == nil {
		t.Fatalf("a build whose container left late was refused: entry=%v err=%v", entry, err)
	}
	if _, err := store.Resolve(entry.Key); err != nil {
		t.Errorf("the build was not admitted: %v", err)
	}
	if probes < 3 {
		t.Errorf("admission probed %d times; it must poll until the container is gone", probes)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Errorf("an admitted build left its staging: %v", err)
	}
}

// A build whose container never leaves within the bound is retained, and says so as retention.
func TestAFinishedForkBuildWhoseJailStaysIsRetainedNotFailed(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(map[bool]string{true: "present", false: "unknown"}[known], func(t *testing.T) {
			b, store, staging, _ := directForkBuildFixture(t)
			withForkBuildGoneWait(t, 0)
			withForkBuildProbe(t, func(string, string) (bool, bool) { return known, known })
			entry, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman"}, io.Discard, io.Discard, false)
			if entry != nil || !errors.Is(err, errForkBuildWorkspaceRetained) {
				t.Fatalf("entry=%v err=%v; want the retained-workspace error", entry, err)
			}
			if !strings.Contains(err.Error(), "not yet known gone") {
				t.Errorf("the retention error does not say why: %v", err)
			}
			if keys, _ := store.EntryKeys(); len(keys) != 0 {
				t.Errorf("output was admitted while the jail may still own it: %v", keys)
			}
			if _, err := os.Stat(staging); err != nil {
				t.Errorf("the retained staging is gone: %v", err)
			}
		})
	}
}

// A patched advance records nothing against a commit whose build was retained, and the next launch
// builds it once the jail is gone.
func TestARetainedPatchedBuildIsNoFailedBuild(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	withForkBuildGoneWait(t, 0)
	present := true
	withForkBuildProbe(t, func(string, string) (bool, bool) { return present, true })
	got, out, _ := fx.launch(t, "podman")
	if got.delivery.Key != r.delivery.Key || !strings.Contains(out, "not yet known gone") {
		t.Errorf("the retained build handed %+v and said:\n%s", got.delivery, out)
	}
	if strings.Contains(out, "failed") {
		t.Errorf("a retained build was reported as failed:\n%s", out)
	}
	for _, o := range fx.record(t).Outcomes {
		if o.Kind == packsrc.OutcomeBuildFailed {
			t.Fatalf("a retained build recorded a failed build:\n%s", out)
		}
	}
	present = false
	moved, out, _ := fx.launch(t, "podman")
	if moved.delivery.Key == r.delivery.Key {
		t.Errorf("the next launch did not build the candidate once its jail was gone:\n%s", out)
	}
}
