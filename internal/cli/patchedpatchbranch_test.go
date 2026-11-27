package cli

// patchedpatchbranch_test.go drives a PATCH BRANCH (docs/reference/patched-forks.md §3.5, OQ-PFK6; packsrc/patchbranch.go)
// through the real launch advance, `yolo pack lint` and `yolo pack status`, against local
// repositories with the build jail substituted: a clean merge builds the merged tree and keys the
// good build on the patch branch's commit; a moved branch rebuilds; a conflict refuses naming the paths and
// the merge that repairs it; an old git is refused by name; and a patch branch repository that cannot be
// fetched is a failed fetch a launch with nothing to run retries at once (PF-D88).

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// patchBranchAdvanceFixture is the patched advance fixture with its series replaced by a patch branch: a
// clone of the upstream whose branch `mine`, started at v1.0.0, makes the series' two edits as commits.
type patchBranchAdvanceFixture struct {
	*patchedAdvanceFixture
	patchBranch string
}

func newPatchBranchAdvanceFixture(t *testing.T) *patchBranchAdvanceFixture {
	t.Helper()
	return withPatchBranch(t, newPatchedAdvanceFixture(t, ""))
}

// withPatchBranch replaces base's series by the patch branch: any patched fixture, the host floor's too.
func withPatchBranch(t *testing.T, base *patchedAdvanceFixture) *patchBranchAdvanceFixture {
	t.Helper()
	fx := &patchBranchAdvanceFixture{patchedAdvanceFixture: base}
	fx.patchBranch = filepath.Join(t.TempDir(), "branch")
	upstreamGit(t, filepath.Dir(fx.patchBranch), "clone", "-q", fx.repo, fx.patchBranch)
	upstreamGit(t, fx.patchBranch, "checkout", "-q", "-b", "mine", fx.base)
	fx.patchBranchCommit(t, map[int]string{10: "ten"})
	fx.patchBranchCommit(t, map[int]string{10: "ten", 12: "twelve"})
	fx.writePatchBranchManifest(t, "git+file://"+fx.patchBranch+"?ref=mine")
	if err := os.RemoveAll(filepath.Join(fx.forkDir, "patches")); err != nil { // a patch branch ships no series
		t.Fatal(err)
	}
	return fx
}

func (fx *patchBranchAdvanceFixture) writePatchBranchManifest(t *testing.T, patchBranch string) {
	t.Helper()
	writeFile(t, fx.manifest, `{"name":"forkpack","contributes":[{"kind":"program","bin":"tool","via":"source",`+
		`"fork_of":"basepack","source":"git+file://`+fx.repo+`?ref=main","patch_branch":"`+patchBranch+`",`+
		`"build":"sh build.sh","produces":[".local/bin/tool"]}]}`)
}

// patchBranchCommit commits f.txt with edits on the patch branch, returning the commit.
func (fx *patchBranchAdvanceFixture) patchBranchCommit(t *testing.T, edits map[int]string) string {
	t.Helper()
	writeFile(t, filepath.Join(fx.patchBranch, "f.txt"), lines30(edits))
	upstreamGit(t, fx.patchBranch, "add", "-A")
	upstreamGit(t, fx.patchBranch, "commit", "-qm", "branch")
	return upstreamGit(t, fx.patchBranch, "rev-parse", "HEAD")
}

func TestAPatchBranchLaunchBuildsTheMergedTreeKeyedOnThePatchBranchCommit(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	tip := upstreamGit(t, fx.patchBranch, "rev-parse", "mine")
	r, out, handed := fx.launch(t, "podman")
	if len(fx.builds) != 1 || fx.builds[0] != lines30(map[int]string{10: "ten", 12: "twelve", 14: "fourteen"}) {
		t.Fatalf("builds = %q, want one of v1.1.0 with the patch branch merged\n%s", fx.builds, out)
	}
	g := fx.record(t).Good
	label := "mine@" + shortSHA(tip)
	if g == nil || g.Commit != v11 || g.Series != packsrc.PatchBranchDigest(tip) || g.PatchBranch != label || g.Entry != r.delivery.Key {
		t.Fatalf("the good build = %+v, want v1.1.0 keyed on the patch branch's commit %s", g, tip)
	}
	if len(handed) != 1 || handed[0].Key != g.Entry {
		t.Errorf("handed %+v, want the good build", handed)
	}
	if !strings.Contains(out, "v1.1.0 ("+shortSHA(v11)+") + patch branch "+label) {
		t.Errorf("the launch does not name the patch branch it merged:\n%s", out)
	}
	entry, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(g.Entry)
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(entry.Root))
	if len(recs) != 1 || recs[0].Series != packsrc.PatchBranchDigest(tip) || recs[0].Revision != v11 {
		t.Errorf("the build receipt = %+v, want the upstream commit and the patch branch's digest", recs)
	}

	// `yolo pack status` describes the patch branch, its merge base and the good build's patch branch.
	rc, status, _ := packVerb(t, "status")
	for _, w := range []string{"patch branch: " + label + " of file://" + fx.patchBranch, "merge base with upstream " + shortSHA(fx.base),
		"+ patch branch " + label, "patch branch lag: the patch branch's merge base with upstream v1.0.0", "which takes the patch branch cleanly"} {
		if !strings.Contains(status, w) {
			t.Errorf("status (rc %d) lacks %q:\n%s", rc, w, status)
		}
	}
}

// A MOVED PATCH BRANCH is a new commit and a new recipe: the next check sees it and rebuilds.
func TestAMovedPatchBranchRebuilds(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if _, out, _ := fx.launch(t, "podman"); len(fx.builds) != 1 {
		t.Fatalf("the first launch built %d\n%s", len(fx.builds), out)
	}
	moved := fx.patchBranchCommit(t, map[int]string{10: "ten", 12: "twelve", 20: "twenty"})
	fx.later(2 * time.Hour)
	r, out, _ := fx.launch(t, "podman")
	if len(fx.builds) != 2 || fx.builds[1] != lines30(map[int]string{10: "ten", 12: "twelve", 14: "fourteen", 20: "twenty"}) {
		t.Fatalf("after the branch moved builds = %q, want v1.1.0 with the moved patch branch merged\n%s", fx.builds, out)
	}
	if g := fx.record(t).Good; g == nil || g.Series != packsrc.PatchBranchDigest(moved) || g.Entry != r.delivery.Key {
		t.Errorf("the good build = %+v, want it keyed on the moved patch branch %s", g, moved)
	}
}

// THE HOST FLOOR'S PREFLIGHT reads a patch branch's recorded failure as it reads a series': `yolo host
// apply --assert` stops before its first write. Red with hostPatchPreflight skipping a fork with no
// `patches`, as it did.
func TestAHostApplyStopsOnAPatchBranchsRecordedFailureBeforeItWrites(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := withPatchBranch(t, patchedFloorFixture(t))
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.writeUserConfig(t, `,"host_management":"own"`)
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("the first launch: rc=%d\n%s", rc, out)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	if rc, _, out := fx.hostLaunch(t); rc == 0 || !strings.Contains(out, "patch branch does not merge into upstream") {
		t.Fatalf("the launch that records the failure: rc=%d\n%s", rc, out)
	}
	var out, errw bytes.Buffer
	rc := hostApplyRefreshAndRender(&out, &errw, false, true, nil, "", "")
	if rc == 0 || strings.Contains(out.String(), "host apply — applying into") ||
		!strings.Contains(errw.String(), "ERROR: fork forkpack/tool: patch branch does not merge into upstream v1.2.0") {
		t.Errorf("an apply over a patch branch's recorded failure: rc=%d, want a refusal before its render\n%s\n%s",
			rc, out.String(), errw.String())
	}
}

// AN UNREADABLE CHECK RECORD is said as one, never as a branch not fetched yet, with the act that
// starts the record over.
func TestAnUnreadableRecordIsNotAPatchBranchNotFetchedYet(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	good, out, _ := fx.launch(t, "podman")
	if good.delivery.Key == "" {
		t.Fatalf("the first launch handed nothing\n%s", out)
	}
	rec := (&packsrc.Store{Dir: paths.PacksDir()}).CheckRecordPath("forkpack/tool")
	if err := os.WriteFile(rec, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, status, _ := packVerb(t, "status")
	if strings.Contains(status, "not fetched on this machine yet") || !strings.Contains(status, "cannot be read") ||
		!strings.Contains(status, "`yolo pack update` checks it again") {
		t.Errorf("status does not say the record cannot be read, with its next step:\n%s", status)
	}
}

// freshUpdate runs one fresh-check advance as `yolo --update` does: a preparation request registered
// first, its identity and boundary handed to the advance, and the request's guard at admission.
func (fx *patchBranchAdvanceFixture) freshUpdate(t *testing.T) advanceResult {
	t.Helper()
	f := fx.fork(t)
	req, err := newPreparationRequest(context.Background(), []packload.Fork{f}, "podman", patchedTestPlatform)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r := advancePatchedFork(f, advanceOptions{freshCheck: true, freshIdentity: req.Keys[0].InputIdentity,
		freshBoundary: req.Boundaries[f.Key()], platform: patchedTestPlatform, runtime: "podman", launch: true,
		ctx: context.Background(), out: &out, errw: &out,
		admitGuard: func(commit func() error) error { return req.Guard(f.Key(), commit) }})
	if err := qualifyPreparationResult(req, f.Key(), r); err != nil {
		t.Fatalf("the fresh update did not qualify: %v\n%s", err, out.String())
	}
	return r
}

// A FRESH UPDATE (`yolo --update`) ADMITS A PATCH BRANCH it resolves for the first time and one that
// moved: the request's identity is fixed before the check records the branch's new commit.
func TestAFreshUpdateAdmitsAMovedPatchBranch(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if r := fx.freshUpdate(t); !r.built || len(fx.builds) != 1 {
		t.Fatalf("the first fresh update built %d (result %+v)", len(fx.builds), r)
	}
	moved := fx.patchBranchCommit(t, map[int]string{10: "ten", 12: "twelve", 20: "twenty"})
	r := fx.freshUpdate(t)
	if !r.built || len(fx.builds) != 2 {
		t.Fatalf("the fresh update after the branch moved built %d (result %+v)", len(fx.builds), r)
	}
	if g := fx.record(t).Good; g == nil || g.Series != packsrc.PatchBranchDigest(moved) {
		t.Errorf("the good build = %+v, want it keyed on the moved branch %s", g, moved)
	}
}

// A CONFLICT REFUSES, naming the conflicting paths and the merge that repairs the patch branch.
func TestAPatchBranchConflictRefusesNamingThePaths(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{11: "eleven"})
	r, out, _ := fx.launch(t, "podman")
	pf := r.patchFailure
	if len(fx.builds) != 0 || !r.failed || pf == nil || pf.Kind != "conflict" || pf.Target.Tag != "v1.1.0" ||
		len(pf.Paths) != 1 || pf.Paths[0] != "f.txt" || !strings.Contains(pf.Member, "mine@") {
		t.Fatalf("a conflicting patch branch built %d, result %+v\n%s", len(fx.builds), pf, out)
	}
	var block bytes.Buffer
	writePatchFailure(&block, pf, fx.fork(t), packsrc.PatchBypass{}, "")
	for _, w := range []string{"patch branch does not merge into upstream v1.1.0", "Conflict: f.txt",
		"\nRepair:\n", " checkout mine", " merge " + pf.Target.Commit, "pushing the branch to its remote is optional"} {
		if !strings.Contains(block.String(), w) {
			t.Errorf("the error block lacks %q:\n%s", w, block.String())
		}
	}
	// The fixture's patch branch is read from a local clone, so the repair runs in that clone.
	if strings.Contains(block.String(), "git clone") || strings.Contains(block.String(), "git push origin mine") {
		t.Errorf("the repair clones or pushes instead of repairing the local clone:\n%s", block.String())
	}
}

// AN OLD GIT is refused by name, with updating git as the next step, and nothing is merged otherwise.
func TestAnOldGitRefusesAPatchBranchLaunchByName(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	patchedGitWrapper(t, `if [ "$1" = version ]; then echo "git version 2.37.1"; exit 0; fi`)
	r, out, _ := fx.launch(t, "podman")
	if len(fx.builds) != 0 || r.delivery.Key != "" {
		t.Fatalf("under git 2.37.1 the launch built %d and handed %+v\n%s", len(fx.builds), r.delivery, out)
	}
	if !strings.Contains(out, "merging a patch branch needs git 2.40 or newer (`git merge-tree --write-tree`), and this host's git is 2.37.1") ||
		!strings.Contains(out, "update git") {
		t.Errorf("the launch does not refuse the old git by name:\n%s", out)
	}
}

// PF-D88 FOR THE PATCH BRANCH: a patch branch repository that cannot be fetched refuses a launch with nothing to
// run, and the next launch fetches again at once rather than an hour later.
func TestAPatchBranchThatCannotBeFetchedIsRetriedByTheNextLaunch(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	gone := filepath.Join(t.TempDir(), "gone")
	fx.writePatchBranchManifest(t, "git+file://"+gone+"?ref=mine")
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key != "" || !strings.Contains(r.delivery.Reason, "could not be fetched") {
		t.Fatalf("an unreachable patch branch handed %+v\n%s", r.delivery, out)
	}
	seq := fx.record(t).Seq
	if err := os.Rename(fx.patchBranch, gone); err != nil {
		t.Fatal(err)
	}
	fx.later(time.Minute)
	r, out, _ = fx.launch(t, "podman")
	if fx.record(t).Seq == seq || r.delivery.Key == "" || len(fx.builds) != 1 {
		t.Fatalf("a minute later the launch did not fetch the patch branch again: handed %+v, builds %d\n%s",
			r.delivery, len(fx.builds), out)
	}
}

// `yolo pack lint` accepts a pack whose fork names a patch branch, and refuses one naming both.
func TestPackLintAcceptsAPatchBranch(t *testing.T) {
	fx := newPatchBranchAdvanceFixture(t)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", fx.forkDir}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint of a patch branch fork = %d\n%s%s", rc, out.String(), errw.String())
	}
	writeFile(t, fx.manifest, strings.Replace(readFileT(t, fx.manifest), `"patch_branch":`, `"patches":"patches","patch_branch":`, 1))
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", fx.forkDir}, &out, &errw, false); rc == 0 ||
		!strings.Contains(out.String()+errw.String(), "forkpack/tool") {
		t.Errorf("lint of a fork naming both = %d, want a refusal naming the owner key\n%s%s", rc, out.String(), errw.String())
	}
}
