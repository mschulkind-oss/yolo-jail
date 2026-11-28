package packsrc

// patchbranchrewritten_test.go pins the ruling of 2026-11-27 (docs/reference/patched-forks.md, PF-D99): a local
// branch that DIVERGED from its remote but whose tip WAS ONCE the remote's has nothing unpushed. The remote was
// rewritten elsewhere, so the remote tip builds and the line names the safe reset, never the force-push.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// rewriteRemote force-pushes a REWRITE of mine from the pusher — a fresh history on the same base, as a rebase on
// another machine would — and returns the new tip.
func (f *checkoutFixture) rewriteRemote(t *testing.T) string {
	t.Helper()
	gitIn(t, f.pusher, "reset", "-q", "--hard", f.base)
	commitFile(t, f.pusher, "f.txt", thirtyLines(map[int]string{10: "ten", 20: "twenty"}))
	commitFile(t, f.pusher, "f.txt", thirtyLines(map[int]string{10: "ten", 20: "twenty", 22: "twenty-two"}))
	gitIn(t, f.pusher, "push", "-q", "--force", "origin", "mine")
	return gitIn(t, f.pusher, "rev-parse", "mine")
}

// wantRewrittenLine pins what a stale local branch's line says: origin answered, the local branch is stale
// because origin was rewritten, and the next step is the safe reset, never the force-push.
func wantRewrittenLine(t *testing.T, f *checkoutFixture, r *PatchBranchResolved, rewritten, local string) {
	t.Helper()
	if r.Commit != rewritten || r.Copy != "origin" || !r.Rewritten || r.Diverged() || !r.Loud() {
		t.Fatalf("resolved = %+v, want origin's rewritten %s, the local branch stale, loud", r, rewritten)
	}
	wantLine(t, r, f.now, "mine  origin "+shortCommit(rewritten), "local branch is stale",
		"origin was rewritten", "local "+shortCommit(local)+" was once origin's tip",
		"`git -C "+shquote.Quote(f.machine)+" branch -f -- mine origin/mine`")
	if line := r.Line(f.now); strings.Contains(line, "force-with-lease") {
		t.Errorf("the line offers the force-push that would destroy origin's rewrite: %q", line)
	}
}

func TestALocalBranchOnceOriginsTipIsStaleWhenOriginWasRewrittenByTheCheckoutsReflog(t *testing.T) {
	f := newCheckoutFixture(t)
	f.push(t, map[int]string{10: "ten", 12: "twelve"})
	gitIn(t, f.machine, "fetch", "-q", "origin") // the user's own fetch: the tracking ref's reflog now holds this tip
	local := f.localMine(t, "origin/mine", 0)    // exactly origin's tip
	rewritten := f.rewriteRemote(t)
	before := userRefs(t, f.machine)
	r := f.launch(t).Record.PatchBranch
	wantRewrittenLine(t, f, r, rewritten, local)
	if after := userRefs(t, f.machine); after != before {
		t.Errorf("a launch changed the user's repository:\nbefore %s\nafter %s", before, after)
	}
}

func TestALocalBranchOnceOriginsTipIsStaleWhenOriginWasRewrittenByYolosOwnRecord(t *testing.T) {
	f := newCheckoutFixture(t)
	local := f.localMine(t, "origin/mine", 0)
	if r := f.launch(t).Record.PatchBranch; r.Copy != "local" || r.Commit != local {
		t.Fatalf("resolved = %+v, want the equal local %s", r, local)
	}
	// The checkout keeps no reflog of its tracking ref (a pruned reflog, a fresh clone, reflogs off).
	if err := os.RemoveAll(filepath.Join(f.machine, ".git", "logs", "refs", "remotes")); err != nil {
		t.Fatal(err)
	}
	rewritten := f.rewriteRemote(t)
	f.now = f.now.Add(2 * time.Minute)
	wantRewrittenLine(t, f, f.launch(t).Record.PatchBranch, rewritten, local)
}

func TestARealUnpushedRebaseStillBuildsTheLocalBranchAndOffersThePush(t *testing.T) {
	f := newCheckoutFixture(t)
	local := f.localMine(t, "origin/mine", 1) // never origin's tip
	f.launch(t)                               // yolo records origin's tip
	f.push(t, map[int]string{10: "ten", 12: "twelve"})
	f.now = f.now.Add(2 * time.Minute)
	r := f.launch(t).Record.PatchBranch
	if r.Commit != local || r.Copy != "local" || r.Rewritten || !r.Diverged() {
		t.Fatalf("resolved = %+v, want the local %s, diverged and not rewritten", r, local)
	}
	wantLine(t, r, f.now, "diverged from origin", "push --force-with-lease -- origin mine` (if you rebased)")
}
