package cli

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// macosusercaptures_test.go pins internal/cli's half of install-capture.md hand-off H4: the HOST
// pick of what a macos-user launch stages from the store (macosUserCaptures, the
// run.Options.MacosUserCaptures seam), its wiring into `yolo run`, and the capture act a darwin
// auto-capture runs. The pipeline's own calls are pinned from Run in
// internal/cli/run/autocapture_test.go; the staging in internal/macosuser/capturestore_test.go.

// darwinPlatform is the sandbox's platform on this machine's architecture, as the run pipeline
// asks (run's macosUserJailPlatform).
var darwinPlatform = "darwin/" + goruntime.GOARCH

// admitForkBuildEntry admits a one-file entry whose `record` receipt is a FORK's build of bin
// (entrypoint.BuildReceipt, carrying a source), the kind no installer query may select.
func admitForkBuildEntry(t *testing.T, store *capture.Store, id, bin, platform string, when time.Time) *capture.Entry {
	t.Helper()
	staged, err := store.Stage(id)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(capture.TreeDir(staged), ".local", "bin", bin)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho fork\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := capture.WriteManifest(staged, &capture.Manifest{Schema: capture.ManifestSchema,
		Home: "/home/agent", Platform: platform, Surfaces: []string{".local"}, Excluded: []string{},
		Entries: []capture.ManifestEntry{
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin/" + bin, Kind: capture.KindFile, Mode: "0755", Size: 20},
		}, AbsoluteRefs: []capture.AbsoluteRef{}}); err != nil {
		t.Fatal(err)
	}
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	line := entrypoint.BuildReceipt{Bin: bin, Source: "git+https://example.invalid/fork.git", Key: entry.Key,
		Path: entry.Root, Platform: platform, Revision: "abc123", Recipe: "r",
		Act: entrypoint.ReceiptActRecord, Time: when}.Line()
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), line); err != nil {
		t.Fatal(err)
	}
	return entry
}

// The pick is the materialize path's own answer at DARWIN: for a bin with entries at three
// platforms-and-kinds, the darwin INSTALLER capture is staged — never the newer linux one a
// container jail would materialize, never the newest of all, a fork's build of the same bin — and
// every other installer program's darwin entry is kept, so another workspace's staged copy stays.
func TestMacosUserCapturesPickTheDarwinInstallerEntry(t *testing.T) {
	store := &capture.Store{Dir: t.TempDir()}
	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	want := admitCaptureEntry(t, store, "darwin", "probetool", darwinPlatform, "/Users/Shared/yolo-captures/probetool/home",
		"darwin bytes", t0)
	admitCaptureEntry(t, store, "linux", "probetool", "linux/"+goruntime.GOARCH, "/home/agent", "linux bytes",
		t0.Add(time.Hour))
	admitForkBuildEntry(t, store, "fork", "probetool", darwinPlatform, t0.Add(2*time.Hour))
	other := admitCaptureEntry(t, store, "other", "othertool", darwinPlatform, "/Users/Shared/yolo-captures/othertool/home",
		"other bytes", t0)
	admitCaptureEntry(t, store, "otherlinux", "othertool", "linux/"+goruntime.GOARCH, "/home/agent",
		"other linux bytes", t0)

	stage, kept := macosUserCaptures(store.Dir, []string{"probetool", "absenttool"}, darwinPlatform)
	if len(stage) != 1 {
		t.Fatalf("staged %+v, want the one darwin installer entry for probetool (absenttool has none)", stage)
	}
	if got := stage[0]; got.Bin != "probetool" || got.Key != want.Key || got.Source != want.Root {
		t.Errorf("staged %+v, want probetool's darwin installer capture %s at %s", got, want.Key, want.Root)
	}
	if len(kept) != 1 || kept[0] != other.Key {
		t.Errorf("kept %v, want only othertool's darwin installer capture %s: no linux entry, no fork "+
			"build, and not the staged key twice", kept, other.Key)
	}

	// No installer entry for any bin asked about: nothing staged, and nothing kept either, since a
	// launch that stages nothing runs no prune.
	if stage, kept := macosUserCaptures(store.Dir, []string{"absenttool"}, darwinPlatform); stage != nil || kept != nil {
		t.Errorf("a launch whose programs the store lacks was handed %+v / %v", stage, kept)
	}
	// An absent store is an empty one.
	if stage, _ := macosUserCaptures(filepath.Join(t.TempDir(), "none"), []string{"probetool"}, darwinPlatform); stage != nil {
		t.Errorf("an absent store staged %+v", stage)
	}
}

// TestALaunchWiresTheMacosUserCaptures pins the WIRING — the line in runRun that hands the pipeline
// its MacosUserCaptures seam — by invoking what it wired against a real store, for
// TestALaunchWiresTheAutoCaptureTrigger's reason: a non-nil check passes for a closure that picks
// nothing. Delete `opts.MacosUserCaptures = …` and this goes red; the pipeline's own tests inject
// their own seam and cannot see it.
func TestALaunchWiresTheMacosUserCaptures(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	if rc := runRun([]string{"run", "--", "true"}); rc != 0 {
		t.Fatalf("runRun = %d, want 0 with the pipeline stubbed", rc)
	}
	if seen.MacosUserCaptures == nil {
		t.Fatal("`yolo run` did not wire Options.MacosUserCaptures: a macos-user launch stages no " +
			"capture, and every launcher there downloads")
	}
	store := &capture.Store{Dir: t.TempDir()}
	e := admitCaptureEntry(t, store, "probetool", "probetool", darwinPlatform, "/Users/Shared/yolo-captures/probetool/home",
		"bytes", time.Now())
	stage, _ := seen.MacosUserCaptures(store.Dir, []string{"probetool"}, darwinPlatform)
	if len(stage) != 1 || stage[0].Key != e.Key {
		t.Errorf("the wired seam picked %+v, want the store's entry %s", stage, e.Key)
	}
}

// A DARWIN auto-capture runs the macos-user capture act, named for that act alone, whatever the
// runtime a capture would otherwise resolve: a launch whose workspace config chose macos-user over
// a user config's podman would otherwise record a linux entry, miss it at every launch, and
// capture again. A container platform keeps the runtime a capture resolves.
func TestADarwinAutoCaptureRunsTheMacosUserCaptureAct(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		[]byte(`{"runtime": "podman", "packs": [{"source": "file://`+filepath.Join(home, "packs", "fixture")+
			`", "name": "fixture"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_RUNTIME", "")
	t.Setenv(NoAutoCaptureEnv, "")
	var seen run.Options
	jail := fakeCaptureJail(t, &seen, probetoolEntries())
	withFakeCaptureJail(t, jail)

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	autoCapture([]string{"probetool"}, darwinPlatform, devnull, devnull, false)
	if seen.Getenv == nil || seen.Getenv("YOLO_RUNTIME") != "macos-user" {
		got := "<unset>"
		if seen.Getenv != nil {
			got = seen.Getenv("YOLO_RUNTIME")
		}
		t.Errorf("a darwin auto-capture ran its capture jail under YOLO_RUNTIME=%s, want macos-user", got)
	}

	seen = run.Options{}
	// A fresh store, so the linux capture is a miss too.
	if err := os.RemoveAll(paths.CapturesDirUnder(home)); err != nil {
		t.Fatal(err)
	}
	autoCapture([]string{"probetool"}, "linux/"+goruntime.GOARCH, devnull, devnull, false)
	if seen.Workspace == "" {
		t.Fatal("the container platform's auto-capture ran no capture jail, so the half below is vacuous")
	}
	if seen.Getenv != nil && seen.Getenv("YOLO_RUNTIME") == "macos-user" {
		t.Errorf("a container platform's auto-capture was forced onto macos-user")
	}
}
