package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostfloorwiring_test.go pins the PRODUCTION floor's inputs at their call sites in internal/cli —
// each one a line of newHostFloor, or a guard the floor's callers keep, whose removal the floor
// package's own tests cannot see because they set the same input by hand:
//
//   - Capture: the capture act `yolo capture <bin>` (hostFloorCaptureAct);
//   - NodeFloor: the highest node_floor the selected packs declare;
//   - UpdatesAllowed: the user config's `agent_updates`;
//   - the in-jail guards: in a jail there is no host floor at all.

// admitRelocatableCapture puts a capture of bin into the machine store under the test HOME — the
// artifact `yolo capture` leaves: a manifest with the full reference scan, and a record receipt.
func admitRelocatableCapture(t *testing.T, bin, body string) *capture.Entry {
	t.Helper()
	store := &capture.Store{Dir: paths.CapturesDir()}
	staged, err := store.Stage(bin)
	if err != nil {
		t.Fatal(err)
	}
	tree := capture.TreeDir(staged)
	if err := os.MkdirAll(filepath.Join(tree, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".local", "bin", bin), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := capture.WriteManifest(staged, &capture.Manifest{
		Schema: capture.ManifestSchema, Home: "/home/agent", Platform: capture.Platform(),
		Surfaces: []string{".local"}, Excluded: []string{},
		Entries: []capture.ManifestEntry{
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin/" + bin, Kind: capture.KindFile, Mode: "0755", Size: int64(len(body))},
		},
		AbsoluteRefs: []capture.AbsoluteRef{}, RefScan: capture.RefScanFull, Relocatable: true,
	}); err != nil {
		t.Fatal(err)
	}
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	line := entrypoint.CaptureReceipt{
		Bin: bin, Declared: "https://example.invalid/" + bin + "/install.sh", Key: entry.Key,
		Digest: capture.DigestHash(entry.Digest), Bytes: int64(len(body)), Path: entry.Root,
		Platform: capture.Platform(), Act: entrypoint.ReceiptActRecord, Time: time.Now(),
	}.Line()
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), line); err != nil {
		t.Fatal(err)
	}
	return entry
}

// TestTheProductionFloorRunsTheCaptureActForAnInstallerAgentItHasNoCaptureOf drives newHostFloor's
// OWN Capture, ResolveCapture and CaptureUnavailable wiring: a machine whose store has no capture
// of nativecli, with a container runtime on PATH, runs the capture act once on the first launch —
// here a stand-in that files what `yolo capture` would — and then runs the materialized floor
// copy. Only the platform (Linux, where the floor holds installer agents) and the Node download
// are the test's. Replace the Capture wiring with a no-op and the launch fails: the store stays
// empty.
func TestTheProductionFloorRunsTheCaptureActForAnInstallerAgentItHasNoCaptureOf(t *testing.T) {
	nativeFloorFixture(t)
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := productionHostFloor(out, progs)
		f.GOOS = "linux"
		f.Node.BaseURL = "http://127.0.0.1:1/test-guard-no-node-download"
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	t.Setenv("YOLO_RUNTIME", "podman")
	stubBins(t, "podman")
	var captured []string
	origAct := hostFloorCaptureAct
	hostFloorCaptureAct = func(args []string, out, errw io.Writer, color bool) int {
		captured = append(captured, strings.Join(args, " "))
		admitRelocatableCapture(t, args[0], "#!/bin/sh\necho nativecli from the capture \"$@\"\n")
		return 0
	}
	t.Cleanup(func() { hostFloorCaptureAct = origAct })

	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"nativecli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d execed=%v\n%s", rc, got.execed, errw.String())
	}
	if strings.Join(captured, "|") != "nativecli" {
		t.Fatalf("the capture act ran %q, want once for nativecli\n%s", captured, errw.String())
	}
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "nativecli")
	if got.target != launcher {
		t.Fatalf("exec'd %s, want the floor's copy %s\n%s", got.target, launcher, errw.String())
	}
	cmd := exec.Command(launcher, "--version")
	cmd.Env = []string{}
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "nativecli from the capture --version\n" {
		t.Errorf("the floor's copy ran %q (%v), want the captured program", out, err)
	}
}

// TestTheProductionFloorsRefreshRecapturesAnInstallerAgentThroughTheCaptureAct drives newHostFloor's
// own Capture wiring from the EVERGREEN REFRESH (HP-D16): an installer agent provisioned from a
// capture, once its newest capture is older than the capture-refresh age (shortened here, with the
// hourly interval), is captured again through the capture act on its next launch, and the newer
// capture is the copy that runs. Delete the recapture from the refresh and the second launch runs the
// first copy, after one capture.
func TestTheProductionFloorsRefreshRecapturesAnInstallerAgentThroughTheCaptureAct(t *testing.T) {
	nativeFloorFixture(t)
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := productionHostFloor(out, progs)
		f.GOOS = "linux"
		f.Node.BaseURL = "http://127.0.0.1:1/test-guard-no-node-download"
		f.UpdateInterval, f.CaptureRefreshAge = time.Nanosecond, time.Nanosecond
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	t.Setenv("YOLO_RUNTIME", "podman")
	stubBins(t, "podman")
	var captured []string
	origAct := hostFloorCaptureAct
	hostFloorCaptureAct = func(args []string, out, errw io.Writer, color bool) int {
		captured = append(captured, strings.Join(args, " "))
		admitRelocatableCapture(t, args[0], fmt.Sprintf("#!/bin/sh\necho nativecli capture %d\n", len(captured)))
		return 0
	}
	t.Cleanup(func() { hostFloorCaptureAct = origAct })

	got := captureHostExec(t)
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "nativecli")
	for i, want := range []string{"nativecli capture 1\n", "nativecli capture 2\n"} {
		time.Sleep(time.Millisecond)
		var errw bytes.Buffer
		if rc := hostExec(nil, []string{"nativecli"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher {
			t.Fatalf("launch %d: rc=%d target=%s\n%s", i+1, rc, got.target, errw.String())
		}
		if len(captured) != i+1 {
			t.Fatalf("launch %d: the capture act ran %d times, want %d\n%s", i+1, len(captured), i+1, errw.String())
		}
		cmd := exec.Command(launcher)
		cmd.Env = []string{}
		if out, err := cmd.CombinedOutput(); err != nil || string(out) != want {
			t.Errorf("launch %d: the floor's copy ran %q (%v), want %q\n%s", i+1, out, err, want, errw.String())
		}
	}
}

// `yolo host -- <installer agent>` the store has no capture of, ON A MACHINE WITH NO CONTAINER
// RUNTIME on PATH, through newHostFloor's own CaptureUnavailable: the floor runs no capture, the
// no-copy line names the missing runtime and the step that ends it, and the launch runs the PATH
// copy (OQ-HE11). The step is true: with a runtime on PATH, the next launch runs the capture act
// once — a stand-in filing what `yolo capture` would — and execs the floor's copy, with nothing run
// by hand. Only the platform (Linux, where the floor holds installer agents) is the test's.
func TestHostLaunchOfAnInstallerAgentOnAMachineThatCannotCaptureNamesTheRuntimeStep(t *testing.T) {
	nativeFloorFixture(t)
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := productionHostFloor(out, progs)
		f.GOOS = "linux"
		f.Node.BaseURL = "http://127.0.0.1:1/test-guard-no-node-download"
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	var captured []string
	origAct := hostFloorCaptureAct
	hostFloorCaptureAct = func(args []string, out, errw io.Writer, color bool) int {
		captured = append(captured, strings.Join(args, " "))
		admitRelocatableCapture(t, args[0], "#!/bin/sh\necho nativecli from the capture \"$@\"\n")
		return 0
	}
	t.Cleanup(func() { hostFloorCaptureAct = origAct })
	stubDir := stubBins(t, "nativecli")
	withoutContainerRuntime(t, stubDir)

	got := captureHostExec(t)
	var errw bytes.Buffer
	stub := filepath.Join(stubDir, "nativecli")
	if rc := hostExec(nil, []string{"nativecli"}, io.Discard, &errw, nil); rc != 127 || got.execed {
		t.Fatalf("rc=%d target=%s, want 127 and no exec, never the PATH copy %s (HNR-D2)\n%s", rc, got.target,
			stub, errw.String())
	}
	want := "yolo host: yolo has no copy of nativecli on this machine (there is no capture of nativecli on " +
		"this machine, and no container runtime (podman) is on PATH to run `yolo capture` with — install " +
		"one (`yolo check` names how on this machine) and the next `yolo host` launch captures it)" +
		declaredNoCopyRefusal
	if !strings.Contains(errw.String(), want) {
		t.Errorf("stderr lacks the no-copy line with its step\n  %s\n%s", want, errw.String())
	}
	if len(captured) != 0 {
		t.Fatalf("a capture ran on a machine with no runtime: %q", captured)
	}

	// The step, taken: a runtime on PATH, and the next launch captures it and runs the floor's copy.
	if err := os.WriteFile(filepath.Join(stubDir, "podman"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	errw.Reset()
	if rc := hostExec(nil, []string{"nativecli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("with a runtime: rc=%d execed=%v\n%s", rc, got.execed, errw.String())
	}
	if strings.Join(captured, "|") != "nativecli" {
		t.Fatalf("with a runtime: the capture act ran %q, want once for nativecli\n%s", captured, errw.String())
	}
	if launcher := filepath.Join(paths.HostFloorDir(), "bin", "nativecli"); got.target != launcher {
		t.Errorf("with a runtime: exec'd %s, want the floor's copy %s\n%s", got.target, launcher, errw.String())
	}
}

// TestAPacksNodeFloorRaisesTheFloorsNode drives newHostFloor's NodeFloor: a selected pack whose
// npm program declares node_floor 99.1, above the release the floor ships, installs on Node
// 99.1.0 — the fake distribution serves it with published checksums — and its launcher starts
// that Node by path. With NodeFloor left unwired the install refuses ("below the pack's
// node_floor") and the launch exits 127.
func TestAPacksNodeFloorRaisesTheFloorsNode(t *testing.T) {
	floorHostFixtureWith(t,
		`{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg","node_floor":"99.1"}`, "")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d execed=%v\n%s", rc, got.execed, errw.String())
	}
	script, err := os.ReadFile(got.target)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(paths.HostFloorDir(), "node", "v99.1.0", "bin", "node"); !strings.Contains(string(script), want) {
		t.Errorf("the launcher does not start %s:\n%s", want, script)
	}
}

// TestAgentUpdatesReachesTheFloorsRefresh drives newHostFloor's UpdatesAllowed from the user
// config: once installed and past the update interval, a pack `agent_updates` freezes is never
// polled and keeps its version, while the same launch with updates allowed polls and updates.
func TestAgentUpdatesReachesTheFloorsRefresh(t *testing.T) {
	for _, c := range []struct {
		name, extra string
		polls       int
		version     string
	}{
		{"frozen for the pack", `,"agent_updates":{"floorpack":false}`, 0, "1.0.0"},
		{"frozen for every pack", `,"agent_updates":false`, 0, "1.0.0"},
		{"allowed", "", 1, "2.0.0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dist, _ := floorHostFixture(t, c.extra)
			orig := newHostFloor
			newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
				f := orig(out, progs)
				f.UpdateInterval = time.Nanosecond
				return f
			}
			t.Cleanup(func() { newHostFloor = orig })
			captureHostExec(t)
			var errw bytes.Buffer
			if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 {
				t.Fatalf("the installing launch: rc=%d\n%s", rc, errw.String())
			}
			dist.Publish("floorcli-pkg", "2.0.0", "bin=floorcli")
			time.Sleep(time.Millisecond)
			errw.Reset()
			if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 {
				t.Fatalf("the second launch: rc=%d\n%s", rc, errw.String())
			}
			if n := len(dist.NpmCalls("view")); n != c.polls {
				t.Errorf("%d polls, want %d\n%s", n, c.polls, errw.String())
			}
			rec := readFloorRecord(t, "floorcli")
			if rec.Version != c.version {
				t.Errorf("installed %s, want %s\n%s", rec.Version, c.version, errw.String())
			}
		})
	}
}

// readFloorRecord reads the floor's record of bin.
func readFloorRecord(t *testing.T, bin string) hostfloor.Record {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(paths.HostFloorDir(), "records", bin+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec hostfloor.Record
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestInAJailThereIsNoHostFloor: `yolo host --` is not refused in a jail, and there it runs the
// PATH copy (the jail's own launchers answer for its programs) with the environment unchanged —
// no floor bin/ on the child's PATH — installs nothing and creates no floor directory; and the
// floor's other callers, the dependency answer and `yolo host apply`'s stage, answer nothing.
func TestInAJailThereIsNoHostFloor(t *testing.T) {
	dist, packDir := floorHostFixture(t, "")
	handInstalled := filepath.Join(stubBins(t, "floorcli"), "floorcli")
	pack, problems := packload.LoadDir(packDir, "floorpack")
	if len(problems) > 0 {
		t.Fatalf("fixture pack: %v", problems)
	}
	packs := []*packload.Pack{pack}
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || got.target != handInstalled {
		t.Fatalf("rc=%d target=%s, want the PATH copy %s\n%s", rc, got.target, handInstalled, errw.String())
	}
	if childPath := envValue(got.env, "PATH"); childPath != os.Getenv("PATH") {
		t.Errorf("child PATH = %q, want the caller's unchanged %q", childPath, os.Getenv("PATH"))
	}
	if len(dist.NpmCalls("install")) != 0 {
		t.Error("an in-jail launch installed into a host floor")
	}
	if _, err := os.Stat(paths.HostFloorDir()); err == nil {
		t.Errorf("an in-jail launch created %s", paths.HostFloorDir())
	}
	if bins := floorDeliveredBins(packs); len(bins) != 0 {
		t.Errorf("in a jail the floor answers for %v", bins)
	}
	var out bytes.Buffer
	if rc := applyHostFloor(richtext.Printer{W: &out}, &out, packs, true, true, &hostApplySurvey{}); rc != 0 ||
		out.Len() != 0 {
		t.Errorf("in a jail the floor stage ran (rc=%d):\n%s", rc, out.String())
	}
	if _, err := os.Stat(paths.HostFloorDir()); err == nil {
		t.Errorf("an in-jail floor stage created %s", paths.HostFloorDir())
	}
}
