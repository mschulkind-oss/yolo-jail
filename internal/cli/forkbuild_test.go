package cli

// forkbuild_test.go drives the fork BUILD ACT (forkbuild.go; docs/design/forked-programs-as-packs.md
// FP-D1, FP-D8, FP-D9) with the run pipeline substituted, as capturehost_test.go drives `yolo
// capture`: every assertion below is downstream of the act composing the build jail's options and
// calling captureRunPipeline with them.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
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
