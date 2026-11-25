package cli

// patchbranchcheckout_test.go drives a PATCH BRANCH GIVEN AS A LOCAL CHECKOUT (packsrc/patchbranchcopy.go,
// docs/reference/patched-forks.md §3.5, PF-D99) through the real launch, `yolo pack update`, `yolo pack series
// check` and `yolo pack status`: yolo fetches the checkout's remote into its own mirror at every fresh launch,
// builds the newest copy, and every act prints the same provenance line from the check record. The user's
// repository is never written.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// checkoutAdvance is the patch branch advance fixture with the pack in "machine", a clone of a bare "origin"
// whose branch mine a second clone, "pusher", pushes. machine holds mine only as refs/remotes/origin/mine.
type checkoutAdvance struct {
	*patchBranchAdvanceFixture
	origin, pusher, machine string
}

func newCheckoutAdvance(t *testing.T) *checkoutAdvance {
	t.Helper()
	fx := newPatchBranchAdvanceFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &checkoutAdvance{patchBranchAdvanceFixture: fx, origin: filepath.Join(root, "origin.git"),
		pusher: filepath.Join(root, "pusher"), machine: filepath.Join(root, "machine")}
	upstreamGit(t, root, "clone", "-q", "--bare", fx.patchBranch, c.origin)
	upstreamGit(t, root, "clone", "-q", c.origin, c.pusher)
	upstreamGit(t, root, "clone", "-q", c.origin, c.machine)
	upstreamGit(t, c.machine, "checkout", "-q", "-b", "work", fx.base)
	upstreamGit(t, c.machine, "branch", "-q", "-D", "mine")
	fx.patchBranch = c.machine
	inRepoPatchBranch(t, fx, "../..?ref=mine")
	return c
}

// push commits edits on the pusher's mine and pushes it, returning the new tip.
func (c *checkoutAdvance) push(t *testing.T, edits map[int]string) string {
	t.Helper()
	writeFile(t, filepath.Join(c.pusher, "f.txt"), lines30(edits))
	upstreamGit(t, c.pusher, "commit", "-qam", "pushed")
	upstreamGit(t, c.pusher, "push", "-q", "origin", "mine")
	return upstreamGit(t, c.pusher, "rev-parse", "HEAD")
}

// machineState is what no act may change in the user's repository: its refs and FETCH_HEAD.
func (c *checkoutAdvance) machineState(t *testing.T) string {
	t.Helper()
	refs := upstreamGit(t, c.machine, "for-each-ref", "--format=%(refname) %(objectname)")
	head, _ := os.ReadFile(filepath.Join(c.machine, ".git", "FETCH_HEAD"))
	mtime := ""
	if st, err := os.Stat(filepath.Join(c.machine, ".git", "FETCH_HEAD")); err == nil {
		mtime = st.ModTime().String()
	}
	return refs + "\nFETCH_HEAD " + mtime + "\n" + string(head)
}

func mustContain(t *testing.T, what, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, out)
		}
	}
}

func TestACheckoutPatchBranchBuildsTheNewestCopyAndEveryActSaysWhich(t *testing.T) {
	c := newCheckoutAdvance(t)
	before := c.machineState(t)
	first := upstreamGit(t, c.origin, "rev-parse", "mine")
	line := "fork forkpack/tool: patch branch mine  origin " + shortSHA(first) + "  no local branch  fetched just now"

	_, out, _ := c.launch(t, "podman")
	if len(c.builds) != 1 || c.builds[0] != lines30(map[int]string{10: "ten", 12: "twelve", 14: "fourteen"}) {
		t.Fatalf("builds = %q, want v1.1.0 with origin's patch branch merged\n%s", c.builds, out)
	}
	mustContain(t, "the first launch", out, line, "+ patch branch mine@"+shortSHA(first)+" (origin)")

	// A PUSH FROM ANOTHER CLONE is picked up by the next fresh launch, inside the hourly throttle.
	pushed := c.push(t, map[int]string{10: "ten", 12: "twelve", 20: "twenty"})
	c.later(2 * time.Minute)
	_, out, _ = c.launch(t, "podman")
	if len(c.builds) != 2 || c.builds[1] != lines30(map[int]string{10: "ten", 12: "twelve", 14: "fourteen", 20: "twenty"}) {
		t.Fatalf("after a push builds = %q, want the pushed tip merged\n%s", c.builds, out)
	}
	line = "fork forkpack/tool: patch branch mine  origin " + shortSHA(pushed) + "  no local branch  fetched just now"
	mustContain(t, "the launch after a push", out, line)
	if r := c.record(t).PatchBranch; r == nil || r.Commit != pushed || r.Copy != "origin" || !r.Verified {
		t.Fatalf("the record's patch branch = %+v, want origin's %s, verified", r, pushed)
	}

	// THE SAME LINE from the record in `yolo pack series check` and `yolo pack status`.
	if rc, sc, errw := seriesVerb(t, "check", c.forkDir); rc != 0 {
		t.Errorf("series check: rc=%d\n%s\n%s", rc, sc, errw)
	} else {
		mustContain(t, "series check", sc, line)
	}
	_, status, _ := packVerb(t, "status")
	mustContain(t, "status", status, "patch branch mine  origin "+shortSHA(pushed)+"  no local branch  fetched just now")

	// A PUSH BETWEEN A LAUNCH AND `yolo pack update`: the update's own fetch picks it up, and it says so.
	pushed = c.push(t, map[int]string{10: "ten", 12: "twelve", 20: "twenty", 24: "twentyfour"})
	line = "fork forkpack/tool: patch branch mine  origin " + shortSHA(pushed) + "  no local branch  fetched just now"
	c.later(2 * time.Minute)
	rc, uo, ue := packVerb(t, "update")
	if rc != 0 {
		t.Fatalf("`yolo pack update`: rc=%d\n%s\n%s", rc, uo, ue)
	}
	mustContain(t, "pack update", uo+ue, line)
	if r := c.record(t).PatchBranch; r == nil || r.Commit != pushed || r.Copy != "origin" {
		t.Errorf("`yolo pack update` recorded %+v, want origin's %s", r, pushed)
	}

	if after := c.machineState(t); after != before {
		t.Errorf("an act changed the user's repository:\nbefore %s\nafter %s", before, after)
	}

	// DIVERGED: a local mine rebuilt off the first tip, with origin moved on, builds the local branch, loudly.
	upstreamGit(t, c.machine, "checkout", "-q", "-b", "mine", first)
	writeFile(t, filepath.Join(c.machine, "f.txt"), lines30(map[int]string{10: "ten", 12: "twelve", 22: "twentytwo"}))
	upstreamGit(t, c.machine, "commit", "-qam", "local")
	local := upstreamGit(t, c.machine, "rev-parse", "HEAD")
	upstreamGit(t, c.machine, "checkout", "-q", "work")
	c.later(2 * time.Minute)
	_, out, _ = c.launch(t, "podman")
	mustContain(t, "the diverged launch", out, "⚠ fork forkpack/tool: patch branch mine  local "+shortSHA(local)+
		"  diverged from origin: local "+shortSHA(local)+" has 1 commit origin lacks, origin "+shortSHA(pushed)+
		" has 2 the local branch lacks — `git -C "+shquote.Quote(c.machine)+" push --force-with-lease -- origin mine` (if you rebased)")

	// A FAILED FETCH never refuses: the local branch builds, not verified, with the fetch to run by hand.
	if err := os.Rename(c.origin, c.origin+".gone"); err != nil {
		t.Fatal(err)
	}
	c.later(2 * time.Minute)
	r, out, _ := c.launch(t, "podman")
	if r.delivery.Key == "" {
		t.Fatalf("a failed fetch handed nothing:\n%s", out)
	}
	mustContain(t, "the offline launch", out, "⚠ fork forkpack/tool: patch branch mine  local "+shortSHA(local),
		"not verified (fetch failed: ", "last fetched 2 minutes ago", "`git -C "+shquote.Quote(c.machine)+" fetch -- origin mine`")
	if exec.Command("git", "-C", c.machine, "show-ref", "--verify", "--quiet", "refs/remotes/origin/mine").Run() != nil {
		t.Error("the user's remote-tracking ref is gone")
	}
}
