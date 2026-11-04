package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// macosuserpatchedfork_test.go is the macos-user cell for a PATCHED fork (docs/reference/patched-forks.md
// PF-D97): a macos-user launch runs the patched-fork advance — the upstream check, the series replay on
// the host, the sealed darwin build of docs/design/forked-programs-as-packs.md FP-D24, the recorded
// failures and the recovery hatches (§8.5) — and the sandbox session runs the good build its source
// launcher materializes from the store, as a container jail's does.
//
// THE FIXTURE is patchedfork_test.go's (patchFixtureUpstream, the one-member series patching line
// ten), with the base pack's installer pointed nowhere, as macosuserhostforkbuild_test.go's is: the
// fork supplies the program, so the base's installer must never run. HERMETIC: the upstream is a local
// git repository (git+file://) and the build writes a marker script naming the upstream version and
// the line the series patches. No agent starts.
//
// ⚠ Mac-only, behind requireMacosUser: the sandbox account, the run-as rule and nix. Every Linux
// runner, and a nested jail, cannot reach it. Each test removes the capture entries it added and its
// fork's check record.

// macosUserPatchFixture is one patched-fork fixture selected for macos-user launches.
type macosUserPatchFixture struct {
	up     patchFixtureUpstream
	owner  string
	store  string
	before map[string]bool
	ws     string
	v11    string
}

// newMacosUserPatchFixture builds the upstream (v1.0.0, the series' base, then v1.1.0 with line ten
// set to ten11), the series exported against v1.0.0, the base and fork packs, the isolated user
// config selecting them and a workspace under the shared root. With ten11 "ten" the series fits
// v1.1.0; with anything else it conflicts there from the start.
func newMacosUserPatchFixture(t *testing.T, ten11 string) *macosUserPatchFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	up := patchFixtureUpstream{t: t, dir: resolvedTempDir(t)}
	up.git("init", "-q", "-b", "main")
	base := up.release("1.0.0", "ten", "v1.0.0")

	fork := resolvedTempDir(t)
	up.git("checkout", "-q", "-b", "series")
	body, err := os.ReadFile(filepath.Join(up.dir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(up.dir, "f.txt"), []byte(strings.Replace(string(body), "ten\n", "patched\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	up.git("commit", "-qam", "patch line ten")
	up.git("format-patch", "-q", "--base="+base, "-o", filepath.Join(fork, "patches"), "main..series")
	up.git("checkout", "-q", "main")
	up.git("branch", "-q", "-D", "series")
	v11 := up.release("1.1.0", ten11, "v1.1.0")

	basePack := resolvedTempDir(t)
	writeManifest := func(dir, body string) {
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest(basePack, `{"name":"`+patchFixtureBasePack+`","contributes":[{"kind":"program","bin":"`+
		patchFixtureBin+`","via":"installer","url":"file:///nonexistent/`+patchFixtureBin+`-install.sh"}]}`)
	build := `mkdir -p "$HOME/.local/bin" && printf '#!/bin/sh\necho ` + patchFixtureMarker + `_%s_%s\n' "$(cat ver.txt)" ` +
		`"$(head -n 10 f.txt | tail -n 1)" > "$HOME/.local/bin/` + patchFixtureBin + `" && chmod +x "$HOME/.local/bin/` +
		patchFixtureBin + `"`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	writeManifest(fork, `{"name":"`+patchFixtureForkPack+`","contributes":[{"kind":"program","bin":"`+patchFixtureBin+
		`","via":"source","fork_of":"`+patchFixtureBasePack+`","source":"git+file://`+up.dir+`?ref=main",`+
		`"patches":"patches","build":"`+buildJSON+`","produces":[".local/bin/`+patchFixtureBin+`"]}]}`)
	packHome(t, `{"packs": [{"source": "file://`+basePack+`", "name": "`+patchFixtureBasePack+`"}, `+
		`{"source": "file://`+fork+`", "name": "`+patchFixtureForkPack+`"}]}`)

	home := os.Getenv("HOME")
	f := &macosUserPatchFixture{up: up, owner: patchFixtureForkPack + "/" + patchFixtureBin,
		store: paths.CapturesDirUnder(home), v11: v11}
	f.before = captureEntryNames(t, f.store)
	// The record, and any record beside it under the same owner (the `#upstream` build's, a
	// per-platform one), is removed before and after: the pack store may be the machine's.
	record := (&packsrc.Store{Dir: filepath.Join(paths.GlobalStorageUnder(home), "packs")}).CheckRecordPath(f.owner)
	removeRecords := func() {
		matches, _ := filepath.Glob(strings.TrimSuffix(record, ".json") + "*")
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
	removeRecords()
	t.Cleanup(func() {
		removeNewCaptureEntries(t, f.store, f.before, patchFixtureBin)
		removeRecords()
	})
	f.ws = macosUserWorkspace(t, `{}`)
	return f
}

// launch runs the fork's program in a macos-user sandbox session of the fixture's workspace.
func (f *macosUserPatchFixture) launch(t *testing.T, opts ...runOption) result {
	t.Helper()
	return runMacosUser(t, f.ws, patchFixtureBin, append([]runOption{withHostSemantics()}, opts...)...)
}

// live is the fixture's complete store entries.
func (f *macosUserPatchFixture) live(t *testing.T) []string {
	t.Helper()
	return liveCaptureEntries(t, f.store, newCaptureEntries(t, f.store, f.before, patchFixtureBin))
}

// patchedRuns is the marker the fixture's build prints at version ver with the series applied.
func patchedRuns(ver string) string { return patchFixtureMarker + "_" + ver + "_patched" }

// moveToConflict releases v1.2.0 with line ten changed under the series, and has `yolo pack update`
// check it now (a launch checks hourly), which fails on the conflict, as the container cell's does.
func (f *macosUserPatchFixture) moveToConflict(t *testing.T) string {
	t.Helper()
	v12 := f.up.release("1.2.0", "upstream-ten", "v1.2.0")
	r := runCommand(t, f.ws, []string{"pack", "update"}, withHostSemantics(), macosUserRunEnv("account"))
	if r.rc == 0 || !hasPatchFailureBlock(r.combined(), f.owner, "v1.2.0", v12, "f.txt") {
		t.Fatalf("yolo pack update: rc %d, want it to fail on the series' conflict at v1.2.0 with the patch "+
			"failure block (ERROR, Patch, Conflict, Repair, the rebase onto %s, Start anyway)\nstdout:\n%s\nstderr:\n%s",
			r.rc, v12, r.stdout, r.stderr)
	}
	return v12
}

// TestMacosUserLaunchBuildsAndRunsAPatchedFork: the first macos-user launch checks the upstream,
// replays the series onto v1.1.0, builds it for darwin as the sandbox account under the sealed
// profile, and the session runs the patched program; the store holds one complete darwin build of
// the fork; a second launch builds nothing.
func TestMacosUserLaunchBuildsAndRunsAPatchedFork(t *testing.T) {
	requireMacosUser(t)
	f := newMacosUserPatchFixture(t, "ten")

	r := f.launch(t)
	if r.rc != 0 || !strings.Contains(r.stdout, patchedRuns("1.1.0")) {
		t.Fatalf("first macos-user launch: rc %d, want the session to print %q (v1.1.0 with the series "+
			"replayed, built for darwin and materialized in the sandbox)\nstdout:\n%s\nstderr:\n%s",
			r.rc, patchedRuns("1.1.0"), r.stdout, r.stderr)
	}
	// The darwin build itself is proved by the receipt's platform below; a launch's build report
	// names no backend, so only its result line is asserted here.
	for _, want := range []string{
		"built fork " + f.owner + ": v1.1.0 (" + f.v11[:8] + ") + 1 patch",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the first launch's stderr lacks %q:\n%s", want, r.stderr)
		}
	}
	live := f.live(t)
	if len(live) != 1 {
		t.Fatalf("the first launch added %d complete store entries of %s, want 1: %v\nstderr:\n%s",
			len(live), patchFixtureBin, live, r.stderr)
	}
	entry := filepath.Join(f.store, "entries", live[0])
	builds, err := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(entry))
	if err != nil || len(builds) != 1 {
		t.Fatalf("the entry %s's build receipts are %+v (%v), want exactly one", entry, builds, err)
	}
	b := builds[0]
	if want := "darwin/" + runtime.GOARCH; b.Platform != want || b.Fork != f.owner || b.Revision != f.v11 ||
		b.Version == "" || b.Series == "" {
		t.Errorf("the build receipt is %+v, want a %s build of fork %s at %s with its version and series digest",
			b, want, f.owner, f.v11)
	}
	if m, err := capture.ReadManifest(entry); err != nil || m.Platform != "darwin/"+runtime.GOARCH {
		t.Errorf("the entry's manifest is %+v (%v), want platform darwin/%s", m, err, runtime.GOARCH)
	}

	// SECOND LAUNCH: the good build serves, and nothing is built.
	r = f.launch(t)
	if r.rc != 0 || !strings.Contains(r.stdout, patchedRuns("1.1.0")) || strings.Contains(r.stderr, "built fork "+f.owner) {
		t.Fatalf("second macos-user launch: rc %d, want %q with no build\nstdout:\n%s\nstderr:\n%s",
			r.rc, patchedRuns("1.1.0"), r.stdout, r.stderr)
	}
	if again := f.live(t); len(again) != 1 || again[0] != live[0] {
		t.Errorf("the second launch changed the store's entries of %s: %v, want only %s", patchFixtureBin, again, live[0])
	}
}

// TestMacosUserLaunchRefusesAConflictingSeriesAndNamesTheHatches: (a) a FIRST launch whose series
// conflicts at the newest version, with no good build, refuses before the session starts and names a
// way out (the missing-program hatch, or a container backend); (b) with a good build here, a
// conflict at a newer version refuses the next launch with the container cell's patch failure block
// and its YOLO_ALLOW_PATCH_FAILURES hatch, and that hatch runs the good build, said.
func TestMacosUserLaunchRefusesAConflictingSeriesAndNamesTheHatches(t *testing.T) {
	requireMacosUser(t)

	// (a) Conflicting from the start: v1.1.0 already changes line ten under the series.
	first := newMacosUserPatchFixture(t, "upstream-ten")
	r := first.launch(t)
	out := r.combined()
	if r.rc == 0 || strings.Contains(r.stdout, patchFixtureMarker) {
		t.Fatalf("a first launch whose series conflicts at v1.1.0: rc %d, want a refusal before the session "+
			"runs anything\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	for _, w := range []string{
		"ERROR: fork " + first.owner + ": patch series does not apply to upstream v1.1.0 (" + first.v11[:8] + ")",
		"\nStart anyway:\n",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("the first launch's refusal lacks %q:\n%s", w, out)
		}
	}
	if !strings.Contains(out, "YOLO_ALLOW_MISSING_PROGRAMS=1") && !strings.Contains(out, "YOLO_SETUP=podman-self yolo") {
		t.Errorf("the first launch's refusal names neither YOLO_ALLOW_MISSING_PROGRAMS=1 nor "+
			"`YOLO_SETUP=podman-self yolo`, though nothing of the fork is here to run:\n%s", out)
	}
	// §8.7: YOLO_ALLOW_PATCH_FAILURES is listed only when a build of the series serves.
	if strings.Contains(out, "YOLO_ALLOW_PATCH_FAILURES=") {
		t.Errorf("the first launch's refusal offers YOLO_ALLOW_PATCH_FAILURES with no build to run:\n%s", out)
	}
	if live := first.live(t); len(live) != 0 {
		t.Errorf("the refused first launch left complete store entries %v; PF-D81 builds no older fit or base", live)
	}

	// (b) A good build here, then a conflict at v1.2.0.
	f := newMacosUserPatchFixture(t, "ten")
	if r := f.launch(t); r.rc != 0 || !strings.Contains(r.stdout, patchedRuns("1.1.0")) {
		t.Fatalf("the good build's launch: rc %d, want %q\nstdout:\n%s\nstderr:\n%s",
			r.rc, patchedRuns("1.1.0"), r.stdout, r.stderr)
	}
	v12 := f.moveToConflict(t)
	refused := f.launch(t)
	out = refused.combined()
	if refused.rc == 0 || strings.Contains(out, patchFixtureMarker) {
		t.Fatalf("the launch after the conflict at v1.2.0: rc %d, want a refusal before the session runs the "+
			"older build\nstdout:\n%s\nstderr:\n%s", refused.rc, refused.stdout, refused.stderr)
	}
	for _, w := range []string{"ERROR: fork " + f.owner + ": patch series does not apply to upstream v1.2.0 (" + v12[:8] + ")",
		"\nRepair:\n", "    git rebase " + v12 + "\n",
		"\nStart anyway:\n  YOLO_ALLOW_PATCH_FAILURES=" + f.owner + " yolo "} {
		if !strings.Contains(out, w) {
			t.Errorf("the refused launch lacks %q:\n%s", w, out)
		}
	}
	bypassed := f.launch(t, withEnv("YOLO_ALLOW_PATCH_FAILURES=1"))
	out = bypassed.combined()
	if bypassed.rc != 0 || !strings.Contains(bypassed.stdout, patchedRuns("1.1.0")) ||
		strings.Contains(out, patchFixtureMarker+"_1.2.0") ||
		!strings.Contains(out, "\nContinuing with v1.1.0 (") || !strings.Contains(out, " (YOLO_ALLOW_PATCH_FAILURES)\n") {
		t.Fatalf("YOLO_ALLOW_PATCH_FAILURES=1: rc %d, want the session to run %q, said in a Continuing line\n"+
			"stdout:\n%s\nstderr:\n%s", bypassed.rc, patchedRuns("1.1.0"), bypassed.stdout, bypassed.stderr)
	}
}

// TestMacosUserCachedGoodServesTheDarwinGoodBuild: with the series conflicting at v1.2.0 after a
// darwin good build of v1.1.0, YOLO_USE_CACHED_GOOD runs that good build in the session, with no
// check, replay or build.
func TestMacosUserCachedGoodServesTheDarwinGoodBuild(t *testing.T) {
	requireMacosUser(t)
	f := newMacosUserPatchFixture(t, "ten")
	if r := f.launch(t); r.rc != 0 || !strings.Contains(r.stdout, patchedRuns("1.1.0")) {
		t.Fatalf("the good build's launch: rc %d, want %q\nstdout:\n%s\nstderr:\n%s",
			r.rc, patchedRuns("1.1.0"), r.stdout, r.stderr)
	}
	good := f.live(t)
	f.moveToConflict(t)

	r := f.launch(t, withEnv("YOLO_USE_CACHED_GOOD=1"))
	if r.rc != 0 || !strings.Contains(r.stdout, patchedRuns("1.1.0")) || strings.Contains(r.combined(), patchFixtureMarker+"_1.2.0") {
		t.Fatalf("YOLO_USE_CACHED_GOOD=1: rc %d, want the session to run the darwin good build, %q\n"+
			"stdout:\n%s\nstderr:\n%s", r.rc, patchedRuns("1.1.0"), r.stdout, r.stderr)
	}
	if strings.Contains(r.stderr, "built fork "+f.owner) {
		t.Errorf("YOLO_USE_CACHED_GOOD=1 built the fork, want no build:\n%s", r.stderr)
	}
	if after := f.live(t); len(after) != len(good) || (len(after) == 1 && after[0] != good[0]) {
		t.Errorf("YOLO_USE_CACHED_GOOD=1 changed the store's entries of %s: %v, want %v", patchFixtureBin, after, good)
	}
}
