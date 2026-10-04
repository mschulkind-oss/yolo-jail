package cli

// forkdelivery_test.go pins the launch's half of the fork route in this package
// (docs/design/forked-programs-as-packs.md OQ-FP4, FP-D8): `yolo run` wires the build trigger, the
// trigger builds on a miss and not on a hit, and `capture-materialize --key` puts only a build of
// the asking bin and platform in place.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestALaunchWiresTheForkBuildTrigger pins the one line in runRun that hands the pipeline its
// BuildForks closure, by INVOKING the closure it wired (TestALaunchWiresTheAutoCaptureTrigger's
// reason: a refusal closure is non-nil too). Red with `opts.BuildForks = …` deleted.
func TestALaunchWiresTheForkBuildTrigger(t *testing.T) {
	f := forkBuildHome(t)
	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	if rc := runRun([]string{"run", "--", "true"}); rc != 0 {
		t.Fatalf("runRun = %d with the pipeline stubbed", rc)
	}
	if seen.BuildForks == nil {
		t.Fatal("`yolo run` did not wire Options.BuildForks: the fork build trigger is dead")
	}
	builds := 0
	var jailSeen run.Options
	fake := fakeBuildJail(t, &jailSeen, probetoolBuilt)
	withFakeCaptureJail(t, func(o run.Options) int { builds++; return fake(o) })
	pin := packload.ForkPin{Fork: f, Commit: forkTestCommit, Ref: "main"}
	quiet(t, func() {
		got := seen.BuildForks(run.ForkBuildRequest{Pins: []packload.ForkPin{pin}, Platform: "linux/arm64"})
		if d := got["probetool"]; d.Key == "" || builds != 1 {
			t.Errorf("the wired trigger answered %+v after %d builds, want a key from one build", got, builds)
		}
		// A HIT BUILDS NOTHING (§9: never rebuild on every launch).
		again := seen.BuildForks(run.ForkBuildRequest{Pins: []packload.ForkPin{pin}, Platform: "linux/arm64"})
		if again["probetool"].Key != got["probetool"].Key || builds != 1 {
			t.Errorf("the second launch built again (builds %d) or answered another key %+v", builds, again)
		}
	})
}

// A failed build is the fork's reason, and the launch continues (§9).
func TestAFailedLaunchBuildIsTheForksReason(t *testing.T) {
	f := forkBuildHome(t)
	withFakeCaptureJail(t, func(run.Options) int { return 5 })
	var got map[string]entrypoint.ForkDelivery
	quiet(t, func() {
		got = buildForksForLaunch(run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: f, Commit: forkTestCommit}},
			Platform: "linux/arm64"},
			&bytes.Buffer{}, &bytes.Buffer{}, false)
	})
	if d := got["probetool"]; d.Key != "" || !strings.Contains(d.Reason, "failed on the host") {
		t.Errorf("a failed build answered %+v", d)
	}
}

// quiet runs fn with the process's stdout and stderr sent to /dev/null, for a closure that
// writes to them directly.
func quiet(t *testing.T, fn func()) {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	fn()
}

// `capture-materialize --key` puts a fork's build in place only when the entry records a build of
// the asking bin for this platform, and records the materialize in the workspace's log.
func TestCaptureMaterializeKeyChecksTheEntrysBuildRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := &capture.Store{Dir: paths.CapturesDir()}
	staged, err := store.Stage("fork-x")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(capture.TreeDir(staged), ".local", "bin", "pi"), "#!/bin/sh\n")
	home := filepath.Join(t.TempDir(), "home")
	// Captured under the home it is materialized into, as a jail build is under /home/agent.
	m := &capture.Manifest{Schema: capture.ManifestSchema, Home: home, Platform: capture.Platform(),
		Entries: []capture.ManifestEntry{
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin/pi", Kind: capture.KindFile, Mode: "0755", Size: 10},
		}}
	if err := capture.WriteManifest(staged, m); err != nil {
		t.Fatal(err)
	}
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	line := entrypoint.BuildReceipt{Bin: "pi", Source: forkTestSource, Key: entry.Key, Path: entry.Root,
		Platform: capture.Platform(), Revision: forkTestCommit, Recipe: "r", Act: entrypoint.ReceiptActRecord,
		Time: time.Now()}.Line()
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), line); err != nil {
		t.Fatal(err)
	}
	receipts := filepath.Join(t.TempDir(), "receipts.jsonl")

	var errw bytes.Buffer
	if rc := materializeCapture(materializeArgs{store: store.Dir, bin: "omp", home: home, key: entry.Key}, &errw); rc == 0 {
		t.Error("a key whose record names another bin was materialized")
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "pi")); err == nil {
		t.Error("a refused key still put files in the home")
	}
	errw.Reset()
	if rc := materializeCapture(materializeArgs{store: store.Dir, bin: "pi", home: home, key: entry.Key,
		declared: forkTestSource, receipts: receipts}, &errw); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errw.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "pi")); err != nil {
		t.Errorf("the build is not in the home: %v", err)
	}
	got, _ := entrypoint.ReadBuildReceipts(receipts)
	if len(got) != 1 || got[0].Act != entrypoint.ReceiptActMaterialize || got[0].Revision != forkTestCommit {
		t.Errorf("materialize receipts = %+v", got)
	}
}
