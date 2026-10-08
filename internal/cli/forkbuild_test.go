package cli

// forkbuild_test.go drives the fork BUILD ACT (forkbuild.go; docs/design/forked-programs-as-packs.md
// FP-D1, FP-D8, FP-D9) with the run pipeline substituted, as capturehost_test.go drives `yolo
// capture`: every assertion below is downstream of the act composing the build jail's options and
// calling captureRunPipeline with them.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

const (
	forkTestSource = "git+file:///nonexistent/yolo-test/probetool-fork?ref=main" // forkManifest's
	forkTestCommit = "0123456789abcdef0123456789abcdef01234567"
)

// forkBuildHome is a temp HOME selecting a base pack declaring probetool and a fork of it, with
// the fork pinned, and a fixture tree standing in for the pack store's checkout. It returns the
// fork as the selection reads it.
func forkBuildHome(t *testing.T) packload.Fork {
	t.Helper()
	previousProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) { return false, true }
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })
	forkHostFixture(t, "probetool", captureFixtureInstaller)
	l := &packsrc.ForkLock{}
	l.Set(packsrc.ForkLockEntry{Key: "forkpack/probetool", Source: forkTestSource, Ref: "main", Commit: forkTestCommit})
	if err := l.Save(forkLockPath()); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "Makefile"), "install:\n\techo built\n")
	if err := os.Symlink("Makefile", filepath.Join(src, "GNUmakefile")); err != nil {
		t.Fatal(err)
	}
	orig := forkCheckout
	forkCheckout = func(_ *packsrc.Store, a packsrc.Addr, commit string) (string, error) {
		if a.Raw != forkTestSource || commit != forkTestCommit {
			t.Errorf("checked out %s at %s, want the pinned commit of the fork's source", a.Raw, commit)
		}
		return src, nil
	}
	t.Cleanup(func() { forkCheckout = orig })
	return packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "probetool", Source: forkTestSource,
		Build: "make install", Produces: []string{".local/bin/probetool"}}
}

// fakeBuildJail is fakeCaptureJail for a build: it also checks the checkout reached the workspace
// and writes the toolchain record the real jail's script writes.
func fakeBuildJail(t *testing.T, seen *run.Options, entries []capture.ManifestEntry) func(run.Options) int {
	t.Helper()
	inner := fakeCaptureJail(t, seen, entries)
	return func(o run.Options) int {
		if _, err := os.Stat(filepath.Join(o.Workspace, forkSourceLeaf, "Makefile")); err != nil {
			t.Errorf("the source checkout is not in the build workspace: %v", err)
		}
		if link, err := os.Readlink(filepath.Join(o.Workspace, forkSourceLeaf, "GNUmakefile")); err != nil || link != "Makefile" {
			t.Errorf("a symlink in the source was not copied as a link: %q %v", link, err)
		}
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image-identity-abc\n")
		return inner(o)
	}
}

var probetoolBuilt = []capture.ManifestEntry{
	{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
	{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
	{Path: ".local/bin/probetool", Kind: capture.KindFile, Mode: "0755", Size: 32},
}

// `yolo capture <forked bin>` is the explicit rebuild: one sealed build jail, narrowed to the fork
// and its base, and an entry admitted under a `build` receipt carrying the revision, the recipe
// and the toolchain.
func TestCaptureOfAForkBuildsItSealedAndRecordsTheBuild(t *testing.T) {
	f := forkBuildHome(t)
	var seen run.Options
	withFakeCaptureJail(t, fakeBuildJail(t, &seen, probetoolBuilt))
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	// THE SEAL, and the narrowed selection (FP-D9): the build act's options carry both.
	if !seen.Sealed || !slices.Equal(seen.OnlyPacks, []string{"forkpack", "basepack"}) {
		t.Errorf("the build jail ran with Sealed=%v OnlyPacks=%v, want the seal and the fork's two packs",
			seen.Sealed, seen.OnlyPacks)
	}
	if !slices.Equal(seen.Args, forkBuildJailArgv("make install")) {
		t.Errorf("build jail argv = %q", seen.Args)
	}
	if seen.CapturesDir() != "" || !seen.NeverAttach {
		t.Error("the build jail is not the capture jail (no store mount, never attach)")
	}
	// NO READINESS ACT: the base's program is rewritten to the fork's source launcher, which a
	// build jail has no build for yet, so the act refused the jail before the build could run.
	if !seen.NoProgramReadiness {
		t.Error("the build jail runs the readiness act, which refuses it for the very program it builds")
	}
	store := &capture.Store{Dir: paths.CapturesDir()}
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	entry, rec, err := resolveForkBuild(store, "probetool", "linux/arm64", forkTestSource, forkTestCommit, b.recipe())
	if err != nil {
		t.Fatalf("the build is not selectable: %v\n%s", err, errw.String())
	}
	if rec.Source != forkTestSource || rec.Revision != forkTestCommit {
		t.Errorf("record = %+v", rec)
	}
	builds, _ := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(entry.Root))
	if len(builds) != 1 || builds[0].Recipe != b.recipe() || builds[0].Toolchain != "image-identity-abc" ||
		builds[0].Revision != forkTestCommit {
		t.Errorf("build receipts = %+v", builds)
	}
	// AN INSTALLER QUERY NEVER SELECTS IT, and the older capture reader never sees it.
	if _, _, err := resolveCaptureFor(store, "probetool", "linux/arm64"); err == nil {
		t.Error("an installer query of probetool selected the fork's build")
	}
	if caps, _ := entrypoint.ReadCaptureReceipts(capture.ReceiptsPath(entry.Root)); len(caps) != 0 {
		t.Errorf("the capture receipt reader reads a build line: %+v", caps)
	}
}

func TestForkBuildRetryWaitsForDetachedKeeperEvenWhenContainerIsAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	staging, cname, marker := seedRetainedForkBuildWorkspace(t, b, "podman")
	holdForkBuildFileLock(t, filepath.Join(paths.GlobalStorage(), "locks", cname+".keeper"))

	prevProbe := probeForkBuildContainer
	probes := 0
	probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
		probes++
		if gotName != cname || gotRuntime != "podman" {
			t.Errorf("retry probed %q on %q; want %q on the retained podman backend", gotName, gotRuntime, cname)
		}
		return false, true
	}
	t.Cleanup(func() { probeForkBuildContainer = prevProbe })

	runs := 0
	_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
		runJail: func(string, forkBuild, captureStreams) int { runs++; return 0 }}, io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "keeper") || !strings.Contains(err.Error(), "retaining staging") {
		t.Fatalf("retry with a live keeper and absent container = %v; want a fail-closed keeper refusal", err)
	}
	if probes != 0 || runs != 0 {
		t.Errorf("container probes=%d, build launches=%d; live-keeper gate must precede both", probes, runs)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "retained" {
		t.Errorf("live keeper's staging was cleared: %q (%v)", got, err)
	}
	if _, err := os.Stat(staging); err != nil {
		t.Errorf("live keeper's workspace disappeared: %v", err)
	}
}

func TestForkBuildRetryRequiresWorkspaceLaunchOwnership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	staging, cname, marker := seedRetainedForkBuildWorkspace(t, b, "podman")
	holdForkBuildFileLock(t, filepath.Join(paths.GlobalStorage(), "locks", cname+".lock"))
	prevProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) {
		t.Error("a busy workspace launch was followed by a runtime absence probe")
		return false, true
	}
	t.Cleanup(func() { probeForkBuildContainer = prevProbe })

	_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
		runJail: func(string, forkBuild, captureStreams) int {
			t.Error("build started under an active workspace launch")
			return 1
		}},
		io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "workspace launch ownership") {
		t.Fatalf("retry with a busy workspace lock = %v, want an ownership refusal", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "retained" {
		t.Errorf("busy workspace launch's staging was cleared: %q (%v)", got, err)
	}
	if _, err := os.Stat(staging); err != nil {
		t.Errorf("busy workspace launch's workspace disappeared: %v", err)
	}
}

func TestForkBuildRetryUsesRetainedBackendWhenRuntimeIsUnspecified(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_RUNTIME", "container")
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	_, cname, _ := seedRetainedForkBuildWorkspace(t, b, "podman")
	prevProbe := probeForkBuildContainer
	probes := 0
	var probeRuntimes []string
	probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
		probes++
		if gotName != cname {
			t.Errorf("retry probed %q; want %q", gotName, cname)
		}
		probeRuntimes = append(probeRuntimes, gotRuntime)
		return false, true
	}
	t.Cleanup(func() { probeForkBuildContainer = prevProbe })

	var seen run.Options
	fake := fakeBuildJail(t, &seen, probetoolBuilt)
	withFakeCaptureJail(t, func(o run.Options) int {
		if o.OnRuntimeResolved == nil {
			t.Fatal("capture pipeline did not receive the resolved-runtime recorder")
		}
		if err := o.OnRuntimeResolved("container"); err != nil {
			t.Fatalf("record resolved current runtime: %v", err)
		}
		o.Getenv = func(key string) string {
			if key == "YOLO_RUNTIME" {
				return "container"
			}
			return ""
		}
		got, err := readForkBuildRuntime(o.Workspace)
		if err != nil || got != "container" {
			t.Errorf("fresh capture runtime = %q (%v), want actual selected backend container after old podman was proven absent", got, err)
		}
		return fake(o)
	})
	entry, err := buildFork(b, buildMode{lock: pidlock.NoWait}, io.Discard, io.Discard, false)
	if err != nil || entry == nil {
		t.Fatalf("same-ID retry after old backend is absent: entry=%v err=%v", entry, err)
	}
	if probes != 2 || len(probeRuntimes) != 2 || probeRuntimes[0] != "podman" || probeRuntimes[1] != "container" {
		t.Errorf("runtime probes = %v; want original podman reuse probe then fresh container completion probe", probeRuntimes)
	}
}

func TestForkBuildRuntimeRecordMatchesChildResolution(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_RUNTIME", "podman")
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	var seen run.Options
	fake := fakeBuildJail(t, &seen, probetoolBuilt)
	withFakeCaptureJail(t, func(o run.Options) int {
		if o.OnRuntimeResolved == nil {
			t.Fatal("ordinary capture pipeline has no resolved-runtime callback")
		}
		if err := o.OnRuntimeResolved("podman"); err != nil {
			t.Fatalf("record actual child selection: %v", err)
		}
		got, err := readForkBuildRuntime(o.Workspace)
		if err != nil || got != "podman" {
			t.Errorf("recorded child runtime = %q (%v), want the pipeline-selected podman backend", got, err)
		}
		return fake(o)
	})
	entry, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "container",
		runJail: func(staging string, b forkBuild, streams captureStreams) int {
			// In the production child path the parent's project runtime is not forwarded; the child
			// ordinary pipeline resolves the staging workspace and invokes the recorder itself.
			return forkBuildRunJail(staging, b, streams, false)
		}}, io.Discard, io.Discard, false)
	if err != nil || entry == nil {
		t.Fatalf("child runtime record build: entry=%v err=%v", entry, err)
	}
}

func TestForkBuildRetryWithoutResolvedBackendEvidenceFailsClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	staging, cname, _ := seedRetainedForkBuildWorkspace(t, b, "podman")
	if err := os.Remove(forkBuildRuntimeRecordPath(staging)); err != nil {
		t.Fatal(err)
	}
	prevProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) {
		t.Error("runtime was probed without actual resolved-backend evidence")
		return false, true
	}
	t.Cleanup(func() { probeForkBuildContainer = prevProbe })
	marker := filepath.Join(staging, "retained")
	_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "podman",
		runJail: func(string, forkBuild, captureStreams) int {
			t.Error("build started without backend evidence")
			return 1
		}},
		io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "original capture runtime cannot be established") {
		t.Fatalf("retry without selected-backend evidence = %v; want fail-closed refusal", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "retained" {
		t.Errorf("retry without backend evidence removed staging: %q (%v)", got, err)
	}
	if _, err := os.Stat(filepath.Join(paths.AgentsDir(), cname)); err != nil {
		t.Errorf("retry without backend evidence removed jail state: %v", err)
	}
}

func TestForkBuildRetryDoesNotProbeTheNewBackendForOldOwnership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_RUNTIME", "container")
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	staging, cname, marker := seedRetainedForkBuildWorkspace(t, b, "podman")
	prevProbe := probeForkBuildContainer
	probes := 0
	probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
		probes++
		if gotName != cname || gotRuntime != "podman" {
			t.Errorf("retry probed %q on %q; must check old ownership on podman", gotName, gotRuntime)
		}
		return true, true
	}
	t.Cleanup(func() { probeForkBuildContainer = prevProbe })
	runs := 0
	_, err := buildFork(b, buildMode{lock: pidlock.NoWait,
		runJail: func(string, forkBuild, captureStreams) int { runs++; return 0 }}, io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "still present") || !strings.Contains(err.Error(), "podman") {
		t.Fatalf("retry after backend change while old jail remains = %v; want refusal naming old backend", err)
	}
	if probes != 1 || runs != 0 {
		t.Errorf("old-backend probes=%d, new builds=%d; want one old-backend probe and no new build", probes, runs)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "retained" {
		t.Errorf("live old backend's staging was cleared: %q (%v)", got, err)
	}
	if _, err := os.Stat(staging); err != nil {
		t.Errorf("live old backend's workspace disappeared: %v", err)
	}
}

func seedRetainedForkBuildWorkspace(t *testing.T, b forkBuild, rt string) (staging, cname, marker string) {
	t.Helper()
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging = store.StagingDir("fork-" + b.id())
	cname = runtime.FromWorkspace(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	marker = filepath.Join(staging, "retained")
	writeFile(t, marker, "retained")
	if err := writeForkBuildRuntime(staging, rt); err != nil {
		t.Fatal(err)
	}
	agentMarker := filepath.Join(paths.AgentsDir(), cname, "retained")
	if err := os.MkdirAll(filepath.Dir(agentMarker), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, agentMarker, "retained")
	return staging, cname, marker
}

func holdForkBuildFileLock(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatalf("hold %s: %v", path, err)
	}
	t.Cleanup(func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	})
}

func TestMacosUserForkBuildHasNoContainerCompletionWitness(t *testing.T) {
	present, known := probeForkBuildContainer("unused", "macos-user", time.Millisecond)
	if present || known {
		t.Fatalf("macos-user container probe = present %v, known %v; without a native completion witness it must stay unknown", present, known)
	}
}

// macos-user has no container listing or process-ownership witness for an interrupted native build.
// An absent container record is therefore unknown, not known-gone: retain the exact build state and
// refuse same-ID reuse until a host operator verifies this native capture has ended.
func TestMacosUserForkBuildUnknownLivenessRetainsAndFencesWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeForkBuildRuntime(staging, "macos-user"); err != nil {
		t.Fatal(err)
	}
	cname := runtime.FromWorkspace(staging)
	agentState := filepath.Join(paths.AgentsDir(), cname)
	marker := filepath.Join(agentState, "native-capture-still-unverified")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, marker, "retained")

	prevProbe := probeForkBuildContainer
	probes := 0
	probeForkBuildContainer = func(gotName, gotRuntime string, _ time.Duration) (bool, bool) {
		probes++
		if gotName != cname || gotRuntime != "macos-user" {
			t.Errorf("native retry probed %q on %q; want %q on macos-user", gotName, gotRuntime, cname)
		}
		return false, false
	}
	t.Cleanup(func() { probeForkBuildContainer = prevProbe })

	var runCalls int
	_, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "macos-user",
		runJail: func(string, forkBuild, captureStreams) int { runCalls++; return 0 }}, io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "macos-user") || !strings.Contains(err.Error(), b.id()) ||
		!strings.Contains(err.Error(), staging) || !strings.Contains(err.Error(), agentState) ||
		!strings.Contains(err.Error(), "verify this specific native capture") {
		t.Fatalf("unknown macos-user teardown refusal = %v", err)
	}
	if probes != 1 || runCalls != 0 {
		t.Errorf("probe calls=%d, build calls=%d; want one liveness check and no same-ID reuse", probes, runCalls)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "retained" {
		t.Errorf("unknown native teardown removed its retained state: %q (%v)", got, err)
	}
}

// An ordinary completed macos-user build has no old staging path to probe and keeps its existing
// cleanup behavior; the new fail-closed branch applies only when teardown is uncertain.
func TestMacosUserForkBuildSuccessStillCleansItsWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := forkBuildHome(t)
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	store := &capture.Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("fork-" + b.id())
	cname := runtime.FromWorkspace(staging)
	agentState := filepath.Join(paths.AgentsDir(), cname)
	marker := filepath.Join(agentState, "ordinary-build")
	prevProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) {
		t.Error("ordinary first build unexpectedly probed a previous jail")
		return false, false
	}
	t.Cleanup(func() { probeForkBuildContainer = prevProbe })
	fake := fakeBuildJail(t, new(run.Options), probetoolBuilt)
	entry, err := buildFork(b, buildMode{lock: pidlock.NoWait, runtime: "macos-user",
		runJail: func(workspace string, _ forkBuild, _ captureStreams) int {
			if err := os.MkdirAll(agentState, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, marker, "completed")
			rc := fake(run.Options{Workspace: workspace})
			if err := writeForkBuildRuntime(workspace, "macos-user"); err != nil {
				t.Fatal(err)
			}
			if err := writeForkBuildRunReturned(workspace); err != nil {
				t.Fatal(err)
			}
			return rc
		}}, io.Discard, io.Discard, false)
	if err != nil || entry == nil {
		t.Fatalf("ordinary macos-user build: entry=%v err=%v", entry, err)
	}
	for _, path := range []string{staging, agentState, forkBuildRuntimeRecordPath(staging)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("ordinary successful build retained %s: %v", path, err)
		}
	}
}

// A build that exits 0 and leaves nothing, or leaves something but not its program, stores
// nothing (§9: a build with no expected output is a failed build).
func TestAForkBuildWithoutItsOutputsStoresNothing(t *testing.T) {
	for name, entries := range map[string][]capture.ManifestEntry{
		"an empty delta": nil,
		"no program": {{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/share", Kind: capture.KindDir, Mode: "0755"}},
		// `npm install -g .`'s shape: the program is there, as a link into the checkout the build
		// ran in, which is deleted when the build ends.
		"a link into the build's workspace": {
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin/probetool", Kind: capture.KindSymlink, Target: "/workspace/src/bin/probetool"},
		},
		// The same link written relative, as npm writes it: resolved from the jail's home.
		"a relative link into the build's workspace": {
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin/probetool", Kind: capture.KindSymlink, Target: "../../../../workspace/src/bin/probetool"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			forkBuildHome(t)
			var seen run.Options
			withFakeCaptureJail(t, fakeBuildJail(t, &seen, entries))
			var out, errw bytes.Buffer
			if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc == 0 {
				t.Fatalf("a build without its outputs succeeded\n%s", out.String())
			}
			if !strings.Contains(errw.String(), "nothing was stored") {
				t.Errorf("the refusal does not say nothing was stored:\n%s", errw.String())
			}
			if keys, _ := (&capture.Store{Dir: paths.CapturesDir()}).EntryKeys(); len(keys) != 0 {
				t.Errorf("entries were admitted: %v", keys)
			}
		})
	}
}

// `yolo capture <forked bin>` REFUSES on contention: a human can re-run it (FP-D1).
func TestCaptureOfAForkRefusesWhileAnotherBuildRuns(t *testing.T) {
	f := forkBuildHome(t)
	withFakeCaptureJail(t, func(run.Options) int { t.Error("a build ran under a held lock"); return 1 })
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	holder, err := pidlock.Acquire(b.lockPath(), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc == 0 {
		t.Fatal("capture built under another build's lock")
	}
	if !strings.Contains(errw.String(), "another build of this fork is running") {
		t.Errorf("the refusal does not say why:\n%s", errw.String())
	}
}

// A LAUNCH'S build waits for the winner, bounded, and then uses the winner's entry instead of
// building again (FP-D1); a wait past its bound is that launch's failed build.
func TestALaunchBuildWaitsForTheWinnerThenUsesItsEntry(t *testing.T) {
	f := forkBuildHome(t)
	built := 0
	var seen run.Options
	fake := fakeBuildJail(t, &seen, probetoolBuilt)
	withFakeCaptureJail(t, func(o run.Options) int { built++; return fake(o) })
	// The platform fakeCaptureJail's manifest reports, so the winner's entry answers the loser.
	b := forkBuild{Fork: f, Commit: forkTestCommit, Platform: "linux/arm64"}
	holder, err := pidlock.Acquire(b.lockPath(), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The winner: builds while holding the lock, then releases it.
	go func() {
		time.Sleep(150 * time.Millisecond)
		var out, errw bytes.Buffer
		if _, err := buildForkLocked(b, &out, &errw); err != nil {
			t.Errorf("the winner's build: %v\n%s", err, errw.String())
		}
		holder.Release()
	}()
	var out, errw bytes.Buffer
	entry, err := buildFork(b, buildMode{lock: pidlock.Mode{Wait: true, Bound: 10 * time.Second}}, &out, &errw, false)
	if err != nil {
		t.Fatalf("the waiting launch's build: %v\n%s", err, errw.String())
	}
	if built != 1 || entry == nil {
		t.Errorf("builds = %d, entry = %v; want the winner's one build, used by the loser", built, entry)
	}
	if !strings.Contains(out.String(), "waiting for pid") {
		t.Errorf("the wait is not said:\n%s", out.String())
	}

	// A bound that runs out is a failed build, not a hang.
	held, err := pidlock.Acquire(b.lockPath(), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	_, err = buildFork(b, buildMode{force: true, lock: pidlock.Mode{Wait: true, Bound: 200 * time.Millisecond}}, &out, &errw, false)
	if err == nil || !strings.Contains(err.Error(), "has not finished") {
		t.Errorf("a wait past its bound returned %v", err)
	}
}

// buildForkLocked is buildFork for a caller that already holds b's lock — the winner above, which
// took it first to make the other one wait. It runs the same act under a lock of its own (a key
// differing only in the platform the lock is named for), and records what the fake jail's manifest
// reports, which is b's platform.
func buildForkLocked(b forkBuild, out, errw *bytes.Buffer) (*capture.Entry, error) {
	other := b
	other.Platform = b.Platform + "-winner"
	return buildFork(other, buildMode{force: true, lock: pidlock.NoWait}, out, errw, false)
}

// Prune keeps the newest fork entry per source and reaps the older: the reap is the reader's
// complement, and the reader reads both receipt kinds.
func TestPruneKeepsTheNewestForkBuildPerSource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := &capture.Store{Dir: paths.CapturesDir()}
	admit := func(name, source, revision string, at time.Time) string {
		staged, err := store.Stage(name)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(capture.TreeDir(staged), ".local", "bin", "probetool"), name)
		e, err := store.AdmitEntry(staged)
		if err != nil {
			t.Fatal(err)
		}
		line := entrypoint.BuildReceipt{Bin: "probetool", Source: source, Key: e.Key, Path: e.Root,
			Platform: "linux/amd64", Revision: revision, Recipe: "r", Act: entrypoint.ReceiptActRecord, Time: at}.Line()
		if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(e.Root), line); err != nil {
			t.Fatal(err)
		}
		return e.Key
	}
	t0 := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	old := admit("a", forkTestSource, "c1", t0)
	newer := admit("b", forkTestSource, "c2", t0.Add(time.Minute))
	other := admit("c", "git+https://example.invalid/other?ref=main", "c1", t0)
	reap, err := capture.PruneSupersededCaptures(store.Dir, captureRecords, false)
	if err != nil {
		t.Fatal(err)
	}
	var reaped []string
	for _, e := range reap.Entries {
		reaped = append(reaped, e.Key)
	}
	if !slices.Equal(reaped, []string{old}) || reap.Kept != 2 {
		t.Errorf("reaped %v (kept %d), want only the older build %s; newer %s and the other source's %s kept",
			reaped, reap.Kept, old, newer, other)
	}
}

func TestBuildReceiptRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.jsonl")
	at := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	want := entrypoint.BuildReceipt{Bin: "pi", Source: forkTestSource, Key: "k", Digest: "d", Bytes: 7, Path: "/p",
		Platform: "linux/amd64", Revision: forkTestCommit, Recipe: "r", Toolchain: "t",
		Act: entrypoint.ReceiptActRecord, Time: at}
	if err := entrypoint.AppendReceiptLine(path, want.Line()); err != nil {
		t.Fatal(err)
	}
	got, err := entrypoint.ReadBuildReceipts(path)
	if err != nil || len(got) != 1 || got[0] != want {
		t.Fatalf("round trip: %+v (%v), want %+v", got, err, want)
	}
}

// A PATCHED FORK NEVER TAKES A PLAIN FORK'S RECIPE (PF-D31): a plain fork of the same upstream,
// build and produces is in the store, and the patched fork's build finds no recipe to ask for it
// with, so it is not served the unpatched upstream, and buildFork builds nothing under it.
func TestAPatchedForkIsNeverServedAPlainForksBuild(t *testing.T) {
	f := forkBuildHome(t)
	var seen run.Options
	withFakeCaptureJail(t, fakeBuildJail(t, &seen, probetoolBuilt))
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("the plain fork's build: rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	plain := forkBuild{Fork: f, Commit: forkTestCommit, Platform: captureJailPlatform()}
	patchedFork := f
	patchedFork.Patches = "patches"
	patched := forkBuild{Fork: patchedFork, Commit: forkTestCommit, Platform: captureJailPlatform()}
	if plain.recipe() == "" || patched.recipe() != "" {
		t.Fatalf("recipes: plain %q, patched %q; want the plain fork's hash and none for the patched", plain.recipe(), patched.recipe())
	}
	store := &capture.Store{Dir: paths.CapturesDir()}
	// The fixture jail's manifest is linux/arm64's (fakeCaptureJail), as the plain fork's own test reads.
	if _, _, err := resolveForkBuild(store, "probetool", "linux/arm64", forkTestSource, forkTestCommit, plain.recipe()); err != nil {
		t.Fatalf("the plain fork's build is not selectable, so the patched lookup below proves nothing: %v", err)
	}
	if _, _, err := resolveForkBuild(store, "probetool", "linux/arm64", forkTestSource, forkTestCommit, patched.recipe()); err == nil {
		t.Error("the patched fork's lookup selected the plain fork's build of the unpatched upstream")
	} else if !strings.Contains(err.Error(), "asked for by a recipe") {
		t.Errorf("an empty recipe reached the store's selection instead of matching nothing: %v", err)
	}
	withFakeCaptureJail(t, func(run.Options) int { t.Error("a build jail ran for a patched fork"); return 1 })
	if _, err := buildFork(patched, buildMode{force: true, lock: pidlock.NoWait}, &out, &errw, false); err == nil ||
		!strings.Contains(err.Error(), "is a patched fork") {
		t.Errorf("buildFork of a patched fork = %v, want it refused", err)
	}
}
