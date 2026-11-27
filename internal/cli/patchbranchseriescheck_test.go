package cli

// patchbranchseriescheck_test.go pins `yolo pack series check` on a PATCH BRANCH: the default reports a matching
// recorded merge verdict, and `--online` answers the question against the newest upstream in a disposable scratch
// mirror without writing this machine's durable check record.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestSeriesCheckRequiresACurrentRecordedPatchBranchVerdictByDefault(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	rc, out, errw := seriesVerb(t, "check", fx.forkDir)
	if rc != 1 || !strings.Contains(errw, "not yet resolved on this machine") ||
		!strings.Contains(errw, "`yolo pack update`") || !strings.Contains(errw, "`yolo pack series check --online") {
		t.Fatalf("unrecorded patch branch: rc=%d\n%s\n%s", rc, out, errw)
	}
	noScratchLeft(t, tmp)
}

func TestSeriesCheckUsesTheRecordedPatchBranchVerdictAndNamesStaleRemedy(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if _, out, _ := fx.launch(t, "podman"); len(fx.builds) != 1 {
		t.Fatalf("launch did not record a patch branch check:\n%s", out)
	}
	tip := upstreamGit(t, fx.patchBranch, "rev-parse", "mine")
	store := patchedForkStore()
	mirrors := filepath.Join(store.Dir, "mirrors")
	if err := os.RemoveAll(mirrors); err != nil {
		t.Fatal(err)
	}
	gitLog := filepath.Join(t.TempDir(), "git-invocations")
	gitWrapper := filepath.Join(filepath.Dir(gitLog), "git")
	if err := os.WriteFile(gitWrapper, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+gitLog+"\nexit 97\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(gitWrapper)+string(os.PathListSeparator)+os.Getenv("PATH"))
	rc, out, errw := seriesVerb(t, "check", fx.forkDir)
	if rc != 0 || !strings.Contains(out, "recorded good build") || !strings.Contains(out, "merges the patch branch mine@"+shortSHA(tip)+" cleanly") {
		t.Fatalf("recorded patch branch verdict with no mirror objects: rc=%d\\n%s\\n%s", rc, out, errw)
	}
	if _, err := os.Stat(gitLog); !os.IsNotExist(err) {
		t.Fatalf("default series check invoked git despite the recorded verdict (log stat error %v)", err)
	}
	if _, err := os.Stat(mirrors); !os.IsNotExist(err) {
		t.Fatalf("default series check recreated its deleted mirrors: %v", err)
	}
	noScratchLeft(t, tmp)

	prevNow := patchedNow
	patchedNow = func() time.Time { return fx.now.Add(2 * time.Hour) }
	t.Cleanup(func() { patchedNow = prevNow })
	rc, out, errw = seriesVerb(t, "check", fx.forkDir)
	if rc != 1 || !strings.Contains(errw, "recorded verdict is stale") || !strings.Contains(errw, "`yolo pack update`") ||
		!strings.Contains(errw, "`yolo pack series check --online") {
		t.Fatalf("stale patch branch verdict: rc=%d\n%s\n%s", rc, out, errw)
	}
	noScratchLeft(t, tmp)
}

func TestSeriesCheckHydratesOnlyTheExactSelectedWorkspacePackIdentity(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	inRepoPatchBranch(t, fx, "../..?ref=mine")
	hostWorkspace, hostFork := fx.patchBranch, fx.forkDir
	hostIdentity, err := packsrc.RelativeRepositoryIdentity("../..?ref=mine", hostFork)
	if err != nil {
		t.Fatal(err)
	}
	if _, out, _ := fx.launch(t, "podman"); len(fx.builds) != 1 {
		t.Fatalf("host launch did not write the patch-branch record:\n%s", out)
	}

	guestWorkspace := filepath.Join(t.TempDir(), "guest")
	upstreamGit(t, filepath.Dir(guestWorkspace), "clone", "-q", hostWorkspace, guestWorkspace)
	guestFork := filepath.Join(guestWorkspace, "yolo-packs", "forkpack")
	if err := os.MkdirAll(guestFork, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(hostFork, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(guestFork, "pack.json"), string(manifest))
	guestIdentity, err := packsrc.RelativeRepositoryIdentity("../..?ref=mine", guestFork)
	if err != nil {
		t.Fatal(err)
	}
	if guestIdentity == hostIdentity || filepath.Clean(guestFork) == filepath.Clean(hostFork) {
		t.Fatalf("host/guest fixture did not distinguish repository identity and path: host=%q guest=%q paths=%q %q",
			hostIdentity, guestIdentity, hostFork, guestFork)
	}

	packRoot := t.TempDir()
	stagedPack := filepath.Join(packRoot, "forkpack")
	if err := os.MkdirAll(stagedPack, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stagedPack, "pack.json"), string(manifest))
	staged, problems := packload.LoadDir(stagedPack, "forkpack")
	if len(problems) != 0 {
		t.Fatalf("load selected pack for its tree record: %v", problems)
	}
	staged.SourceRoot = hostFork
	if err := packload.WritePackTreeRecordForWorkspace(packRoot, []*packload.Pack{staged}, hostWorkspace); err != nil {
		t.Fatal(err)
	}
	// A clone elsewhere in this workspace is not the selected pack's recorded source path.
	unrelatedWorkspace := filepath.Join(guestWorkspace, "unrelated-clone")
	upstreamGit(t, guestWorkspace, "clone", "-q", hostWorkspace, unrelatedWorkspace)
	unrelatedFork := filepath.Join(unrelatedWorkspace, "yolo-packs", "forkpack")
	if err := os.MkdirAll(unrelatedFork, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(unrelatedFork, "pack.json"), string(manifest))

	inAJail(t, guestWorkspace)
	t.Setenv("YOLO_PACK_ROOT", packRoot)
	store := patchedForkStore()
	if err := os.RemoveAll(filepath.Join(store.Dir, "mirrors")); err != nil {
		t.Fatal(err)
	}
	gitLog := filepath.Join(t.TempDir(), "git-invocations")
	gitWrapper := filepath.Join(filepath.Dir(gitLog), "git")
	if err := os.WriteFile(gitWrapper, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+gitLog+"\nexit 97\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(gitWrapper)+string(os.PathListSeparator)+os.Getenv("PATH"))

	rc, out, errw := seriesVerb(t, "check", guestFork)
	if rc != 0 || !strings.Contains(out, "recorded good build") || !strings.Contains(out, "merges the patch branch mine@") {
		t.Fatalf("selected guest pack did not use the host-recorded verdict: rc=%d\n%s\n%s", rc, out, errw)
	}
	if _, err := os.Stat(gitLog); !os.IsNotExist(err) {
		t.Fatalf("default check invoked a git wrapper that denies every command (stat error %v)", err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, "mirrors")); !os.IsNotExist(err) {
		t.Fatalf("default check recreated deleted mirrors: %v", err)
	}
	// The named directory's manifest remains authoritative; the selected tree contributes identity,
	// not a replacement manifest.
	changedManifest := strings.Replace(string(manifest), "../..?ref=mine", "../..?ref=other", 1)
	if changedManifest == string(manifest) {
		t.Fatal("fixture manifest did not contain its relative patch branch")
	}
	writeFile(t, filepath.Join(guestFork, "pack.json"), changedManifest)
	rc, _, errw = seriesVerb(t, "check", guestFork)
	if rc != 1 || !strings.Contains(errw, "recorded verdict is stale because this pack now resolves its patch branch differently") {
		t.Fatalf("series check replaced the explicitly named manifest with the selected tree's copy: rc=%d\n%s", rc, errw)
	}
	writeFile(t, filepath.Join(guestFork, "pack.json"), string(manifest))

	// It must derive its own identity rather than borrowing the host mapping.
	rc, out, errw = seriesVerb(t, "check", unrelatedFork)
	if rc != 1 || !strings.Contains(errw, "recorded verdict is stale because this pack now resolves its patch branch differently") {
		t.Fatalf("unrelated clone borrowed the selected pack identity: rc=%d\n%s\n%s", rc, out, errw)
	}
	if _, err := os.Stat(gitLog); !os.IsNotExist(err) {
		t.Fatalf("unrelated-clone rejection invoked git (stat error %v)", err)
	}
}

func TestSeriesCheckUsesRecordedConflictWithoutWalking(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{11: "eleven"})
	if _, out, _ := fx.launch(t, "podman"); len(fx.builds) != 0 {
		t.Fatalf("conflicting branch unexpectedly built:\n%s", out)
	}
	rc, out, errw := seriesVerb(t, "check", fx.forkDir)
	for _, want := range []string{"does not merge the patch branch", "conflicts in f.txt", " checkout mine", "the next launch reads that clone"} {
		if rc == 0 || !strings.Contains(out, want) {
			t.Errorf("recorded conflict omitted %q: rc=%d\n%s\n%s", want, rc, out, errw)
		}
	}
	// The fixture's patch branch is read from a local clone, so the recipe repairs that clone.
	if strings.Contains(out, "git clone") || strings.Contains(out, "git push origin mine") {
		t.Errorf("the recipe clones or pushes instead of repairing the local clone:\n%s", out)
	}
}

func TestSeriesCheckReportsUnavailableWhenNoRecordedMergeVerdictExists(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.launch(t, "podman")
	goodBefore := fx.record(t).Good
	fx.commit(t, "v1.2.0", map[int]string{15: "fifteen"})
	f := fx.fork(t)
	series, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	checked := patchedForkStore().CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true,
		Now: func() time.Time { return fx.now }})
	if checked.Err != nil || checked.Record == nil {
		t.Fatalf("refreshing the durable check without a merge walk: %+v", checked)
	}
	rc, out, errw := seriesVerb(t, "check", fx.forkDir)
	for _, want := range []string{"no historical merge verdict", "`yolo pack update`", "start a launch", "--online"} {
		if !strings.Contains(errw, want) {
			t.Errorf("unavailable recorded verdict omitted %q:\n%s%s", want, out, errw)
		}
	}
	if rc != 1 || strings.Contains(out, "merges the patch branch") {
		t.Fatalf("missing historical verdict was invented: rc=%d\n%s\n%s", rc, out, errw)
	}
	if got := fx.record(t).Good; got == nil || goodBefore == nil || got.Commit != goodBefore.Commit || got.Series != goodBefore.Series {
		t.Errorf("reporting an unavailable verdict changed the recorded good build: before=%+v after=%+v", goodBefore, got)
	}
}

func TestSeriesCheckOnlineRefreshesPatchBranchInScratch(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	tip := upstreamGit(t, fx.patchBranch, "rev-parse", "mine")
	rc, out, errw := seriesVerb(t, "check", "--online", fx.forkDir)
	if rc != 0 || !strings.Contains(out, "merges the patch branch mine@"+shortSHA(tip)+" cleanly") {
		t.Fatalf("online patch branch verdict: rc=%d\n%s\n%s", rc, out, errw)
	}
	noScratchLeft(t, tmp)
}

func TestSeriesCheckSaysWhetherTheNewestUpstreamMergesThePatchBranch(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	tip := upstreamGit(t, fx.patchBranch, "rev-parse", "mine")
	tmp := inAJail(t, filepath.Dir(fx.forkDir))
	rc, out, errw := seriesVerb(t, "check", "--online", fx.forkDir)
	want := "fork forkpack/tool: upstream v1.1.0"
	if rc != 0 || !strings.Contains(out, want) || !strings.Contains(out, "merges the patch branch mine@"+shortSHA(tip)+" cleanly") {
		t.Fatalf("series check of a clean patch branch: rc=%d, want %q merging cleanly\n%s%s", rc, want, out, errw)
	}
	if strings.Contains(out, "nothing here to check") {
		t.Errorf("series check still declines the patch branch:\n%s", out)
	}
	noScratchLeft(t, tmp)
	if _, err := os.Stat(filepath.Join(paths.PacksDir(), "checks")); err == nil {
		t.Errorf("series check wrote this machine's check records")
	}
}

func TestSeriesCheckNamesAPatchBranchConflictAndTheMergeThatRepairsIt(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{11: "eleven"})
	rc, out, errw := seriesVerb(t, "check", "--online", fx.forkDir)
	for _, w := range []string{"upstream v1.1.0", "does not merge the patch branch", "conflicts in f.txt",
		"git checkout mine", "git push origin mine"} {
		if rc == 0 || !strings.Contains(out, w) {
			t.Errorf("series check of a conflicting patch branch: rc=%d, want %q\n%s%s", rc, w, out, errw)
		}
	}
}

// A PATCH BRANCH THAT CANNOT BE FETCHED names the fetch, and the check to run again.
func TestSeriesCheckOfAPatchBranchItCannotFetchNamesTheNextStep(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.writePatchBranchManifest(t, "git+file://"+filepath.Join(t.TempDir(), "gone")+"?ref=mine")
	rc, out, errw := seriesVerb(t, "check", "--online", fx.forkDir)
	if rc == 0 || !strings.Contains(errw, "patch branch") || !strings.Contains(errw, "yolo pack series check") {
		t.Errorf("series check of an unfetchable patch branch: rc=%d\n%s%s", rc, out, errw)
	}
}
