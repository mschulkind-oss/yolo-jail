package packsrc

// patchbranchref_test.go pins how a PATCH BRANCH's ?ref= is resolved (patchbranch.go,
// resolvePatchBranchRef), against real repositories: the local branch, then a remote-tracking
// branch of the patch branch's repository (origin first, then the other remotes by name), then a
// tag, then a full commit; the ref that answered recorded beside its commit; no local branch ever
// created; and a ref nothing answers refused with the fetch that delivers it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// fetchedOnlyFixture is the reported machine (yolo-fix-requests §28): a publishing repository
// ("published") holding branch patch/x started at the upstream's v1.0.0, and a clone of it
// ("machine") that holds patch/x only as refs/remotes/origin/patch/x — a fetch delivered it, and no
// local branch was ever made for it.
type fetchedOnlyFixture struct {
	*patchedUpstream
	published, machine, tip string
}

func newFetchedOnlyFixture(t *testing.T) *fetchedOnlyFixture {
	t.Helper()
	u := newPatchedUpstream(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fetchedOnlyFixture{patchedUpstream: u, published: filepath.Join(root, "published"), machine: filepath.Join(root, "machine")}
	gitIn(t, root, "clone", "-q", u.repo, f.published)
	gitIn(t, f.published, "checkout", "-q", "-b", "patch/x", u.base)
	f.tip = commitFile(t, f.published, "f.txt", thirtyLines(map[int]string{10: "ten"}))
	gitIn(t, f.published, "checkout", "-q", "main")
	gitIn(t, root, "clone", "-q", f.published, f.machine)
	gitIn(t, f.machine, "fetch", "-q", "origin")
	if hasRef(t, f.machine, "refs/heads/patch/x") || !hasRef(t, f.machine, "refs/remotes/origin/patch/x") {
		t.Fatal("fixture: the machine clone should hold patch/x only as refs/remotes/origin/patch/x")
	}
	return f
}

func (f *fetchedOnlyFixture) want(ref string) PatchedWant {
	return PatchedWant{Owner: "forkpack/tool", Source: f.source("main"), PatchBranch: "git+file://" + f.machine + "?ref=" + ref}
}

func hasRef(t *testing.T, repo, ref string) bool {
	t.Helper()
	return exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", ref).Run() == nil
}

// resolvedRef runs a forced check and returns what the record says resolved the patch branch.
func (f *fetchedOnlyFixture) resolvedRef(t *testing.T, w PatchedWant) *PatchBranchResolved {
	t.Helper()
	res := f.check(t, w, true)
	if p := res.Record.Check.Problem; p != "" {
		t.Fatalf("check problem: %s", p)
	}
	if res.Record.PatchBranch == nil {
		t.Fatal("the check recorded no patch branch resolution")
	}
	return res.Record.PatchBranch
}

func TestAFetchedOnlyPatchBranchResolvesToTheRemoteTrackingRefWithNoLocalBranchMade(t *testing.T) {
	f := newFetchedOnlyFixture(t)
	// ORIGIN REACHABLE: yolo's own mirror of origin answers (patchbranchcopy.go).
	r := f.resolvedRef(t, f.want("patch/x"))
	if r.Commit != f.tip || r.Copy != "origin" || r.MirrorRepo != f.published {
		t.Fatalf("resolved = commit %s copy %q mirror %q, want origin's %s from yolo's mirror of it", r.Commit, r.Copy, r.MirrorRepo, f.tip)
	}
	if hasRef(t, f.machine, "refs/heads/patch/x") {
		t.Error("resolving the patch branch created a local branch in the declaring repository")
	}
	s, err := PatchBranchSeries(f.want("patch/x").PatchBranch, "", f.source("main"), &CheckRecord{PatchBranch: r})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.PatchBranch.Label(), "patch/x@"+shortCommit(f.tip)+" (origin)"; got != want {
		t.Errorf("the patch branch's label = %q, want %q: one line naming the copy that answered and its commit", got, want)
	}

	// ORIGIN UNREACHABLE AND NEVER FETCHED BY YOLO: the checkout's own refs/remotes/origin/patch/x answers.
	g := newFetchedOnlyFixture(t)
	if err := os.Rename(g.published, g.published+".gone"); err != nil {
		t.Fatal(err)
	}
	r = g.resolvedRef(t, g.want("patch/x"))
	if r.Commit != g.tip || r.Ref != "refs/remotes/origin/patch/x" || r.Copy != "origin" || r.MirrorRepo != "" || r.Verified {
		t.Fatalf("resolved = %+v, want %s from the checkout's refs/remotes/origin/patch/x, not verified", r, g.tip)
	}
	if hasRef(t, g.machine, "refs/heads/patch/x") {
		t.Error("resolving the patch branch created a local branch in the declaring repository")
	}
}

func TestALocalPatchBranchWinsOverTheRemoteTrackingRef(t *testing.T) {
	f := newFetchedOnlyFixture(t)
	gitIn(t, f.machine, "checkout", "-q", "-b", "patch/x", f.base)
	local := commitFile(t, f.machine, "f.txt", thirtyLines(map[int]string{12: "twelve"}))
	gitIn(t, f.machine, "checkout", "-q", "main")
	r := f.resolvedRef(t, f.want("patch/x"))
	if r.Commit != local || r.Ref != "refs/heads/patch/x" {
		t.Fatalf("resolved = commit %s ref %q, want the local branch's %s", r.Commit, r.Ref, local)
	}
	s, err := PatchBranchSeries(f.want("patch/x").PatchBranch, "", f.source("main"), &CheckRecord{PatchBranch: r})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.PatchBranch.Label(); got != "patch/x@"+shortCommit(local) {
		t.Errorf("a local branch's label = %q, want the plain <ref>@<commit>", got)
	}
}

// TWO REMOTES BOTH HOLDING THE REF: origin answers; with no origin, the remote first by name.
func TestOriginAnswersFirstAndOtherwiseTheRemoteFirstByName(t *testing.T) {
	f := newFetchedOnlyFixture(t)
	other := filepath.Join(filepath.Dir(f.published), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", f.published, other)
	gitIn(t, other, "checkout", "-q", "-b", "patch/x", f.base)
	otherTip := commitFile(t, other, "f.txt", thirtyLines(map[int]string{14: "fourteen"}))
	for _, name := range []string{"zeta", "alpha"} {
		gitIn(t, f.machine, "remote", "add", name, other)
		gitIn(t, f.machine, "fetch", "-q", name)
	}
	if r := f.resolvedRef(t, f.want("patch/x")); r.Commit != f.tip || r.Copy != "origin" || r.Remote != "origin" {
		t.Fatalf("with origin present resolved = %s %q, want origin's", r.Commit, r.Copy)
	}
	gitIn(t, f.machine, "remote", "remove", "origin")
	if r := f.resolvedRef(t, f.want("patch/x")); r.Commit != otherTip || r.Copy != "alpha" || r.Remote != "alpha" {
		t.Fatalf("with no origin resolved = %s %q, want alpha's (first by name)", r.Commit, r.Copy)
	}
}

func TestAPatchBranchRefFallsBackToATag(t *testing.T) {
	f := newFetchedOnlyFixture(t)
	gitIn(t, f.machine, "tag", "patched-1", "refs/remotes/origin/patch/x")
	r := f.resolvedRef(t, f.want("patched-1"))
	if r.Commit != f.tip || r.Ref != "refs/tags/patched-1" {
		t.Fatalf("resolved = %s %q, want the tag's commit from refs/tags/patched-1", r.Commit, r.Ref)
	}
}

func TestAPatchBranchRefNothingAnswersNamesTheFetchCommand(t *testing.T) {
	f := newFetchedOnlyFixture(t)
	res := f.check(t, f.want("patch/missing"), true)
	p := res.Record.Check.Problem
	if !strings.Contains(p, "`git -C "+shquote.Quote(f.machine)+" fetch -- origin patch/missing`") ||
		!strings.Contains(p, "names no local branch, remote-tracking branch, tag or commit") {
		t.Fatalf("the check's problem = %q, want the refusal naming the fetch that delivers the ref", p)
	}
}
