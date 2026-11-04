package packsrc

// replaybudget_test.go pins how many git processes a replay starts. Starting git is most of what a
// replay costs where starting a process is slow (about 25 ms a start on a macOS runner, measured
// across both Apple's git and nix's), so the count is the replay's cost there: the series is applied
// at its base in ONE `git am` whatever its length, the entry's tree is read from the listing its
// files' check already made, and the replay's last pick is not committed. Only the picks themselves
// — one merge per member and a commit between two — grow with the series.

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// wideSeries replaces the fixture's series with one of n stacked members, member i changing line
// 2*i, exported from a branch of the upstream at its base as newPatchedUpstream's is.
func (u *patchedUpstream) wideSeries(t *testing.T, n int) {
	t.Helper()
	gitIn(t, u.repo, "checkout", "-q", "-b", "wide", u.base)
	edits := map[int]string{}
	for i := 1; i <= n; i++ {
		edits[2*i] = "member " + strings.Repeat("x", i)
		commitFile(t, u.repo, "f.txt", thirtyLines(edits))
	}
	u.pack = t.TempDir()
	gitIn(t, u.repo, "format-patch", "-q", "--base="+u.base, "-o", filepath.Join(u.pack, "patches"), u.base+"..wide")
	gitIn(t, u.repo, "checkout", "-q", "main")
	gitIn(t, u.repo, "branch", "-q", "-D", "wide")
}

// countingGit is a git that appends each run's arguments to a file, one run a line, and the
// counter that reads it back as the number of runs of each subcommand.
func countingGit(t *testing.T) (bin string, counts func() map[string]int) {
	t.Helper()
	return countingGitWith(t, "")
}

// countingGitWith is countingGit running body after it counts a run, before the real git.
func countingGitWith(t *testing.T, body string) (bin string, counts func() map[string]int) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "runs")
	bin = wrappedGit(t, `printf '%s\n' "$*" >> `+shquote.Quote(log)+"\n"+body)
	return bin, func() map[string]int {
		data, err := os.ReadFile(log)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		got := map[string]int{}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line != "" {
				got[gitLabel(strings.Fields(line))]++
			}
		}
		return got
	}
}

// A REPLAY'S GIT STARTS DO NOT GROW WITH ITS SERIES outside its picks: through WalkSeries, a series
// of 2 and one of 6 members each start one `git am`, read no tree with rev-parse or cat-file, and
// start the same number of every other git; the picks are one merge-tree per member and a
// commit-tree between two. The replay is still the hand rebase's tree.
func TestAReplaysGitStartsDoNotGrowWithItsSeries(t *testing.T) {
	var others []int
	for _, n := range []int{2, 6} {
		u := newPatchedUpstream(t)
		u.wideSeries(t, n)
		v11 := u.release(t, "v1.1.0", map[int]string{29: "twenty-nine"})
		series, list := u.checked(t)
		if series.Len() != n || len(list) == 0 || list[0].Commit != v11 {
			t.Fatalf("fixture: %d members, list %+v", series.Len(), list)
		}
		bin, counts := countingGit(t)
		u.store.Git = bin
		addr := mustAddr(t, u.source("main"))
		w := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{})
		if w.Err != nil || w.Base != nil || w.Fit != 0 || !w.Results[0].Clean {
			t.Fatalf("n=%d: walk = %+v", n, w)
		}
		if want := u.handRebaseTree(t, v11); w.Results[0].Tree != want {
			t.Errorf("n=%d: the replay's tree %s, the hand rebase's %s", n, w.Results[0].Tree, want)
		}
		got := counts()
		commitTrees := n - 1 // a git whose merge-tree takes trees commits no pick (treeMergeGit)
		if gitAtLeast(w.Git, treeMergeGit) {
			commitTrees = 0
		}
		if got["am"] != 1 || got["rev-parse"] != 0 || got["cat-file"] != 0 ||
			got["merge-tree"] != n || got["commit-tree"] != commitTrees {
			t.Errorf("n=%d: git starts by subcommand %v, want one am, no rev-parse or cat-file, %d merge-tree "+
				"and %d commit-tree", n, got, n, commitTrees)
		}
		other := 0
		for sub, c := range got {
			if sub != "merge-tree" && sub != "commit-tree" {
				other += c
			}
		}
		others = append(others, other)
	}
	// At most 11: `git version`, the scratch repository's init and checkout, the one am (its commits
	// read from the files it wrote, amCommits), the base's and the entry's one listing together
	// (probeComplete), which finds files missing in this blobless mirror, and then for the base and
	// the entry each a probe of the mirror, the fetch of what it lacks and the check after it (the
	// entry's listing is the pick's tree).
	if others[0] != others[1] || others[1] > 11 {
		t.Errorf("git starts outside the picks: %v for series of 2 and 6, want the same, at most 11", others)
	}
}

// A WALK OVER A FILLED MIRROR STARTS SEVEN GITS for a series of two members onto an entry that is
// not its base, and no more: `git version`, ONE listing of the base's and the entry's trees
// (probeComplete), the scratch repository's init and checkout, the one `git am`, and one merge-tree
// per member — no rev-list naming the am's commits (amCommits reads them), no commit-tree between
// two picks (a git whose merge-tree takes trees, treeMergeGit), no rev-parse. This is the count a
// launch's replay pays on a macOS host at about 25 ms a start, so a change that starts git again
// per commit or per tree fails here.
func TestAFilledWalkStartsSevenGits(t *testing.T) {
	u := newPatchedUpstream(t)
	v11 := u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	series, list := u.checked(t)
	addr := mustAddr(t, u.source("main"))
	if series.Len() != 2 || list[0].Commit != v11 || list[0].Commit == series.Base {
		t.Fatalf("fixture: %d members, list %+v, base %s", series.Len(), list, series.Base)
	}
	if w := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{}); w.Err != nil || w.Fit != 0 {
		t.Fatalf("filling walk = %+v", w)
	}
	bin, counts := countingGit(t)
	u.store.Git = bin
	w := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{})
	if w.Err != nil || w.Fit != 0 || !w.Results[0].Clean {
		t.Fatalf("walk = %+v", w)
	}
	if !gitAtLeast(w.Git, treeMergeGit) {
		t.Skipf("git %s commits each pick but the last; the count is pinned for git %d.%d and later", w.Git,
			treeMergeGit[0], treeMergeGit[1])
	}
	if want := u.handRebaseTree(t, v11); w.Results[0].Tree != want {
		t.Errorf("the replay's tree %s, the hand rebase's %s", w.Results[0].Tree, want)
	}
	got := counts()
	want := map[string]int{"version": 1, "rev-list": 1, "init": 1, "checkout": 1, "am": 1, "merge-tree": 2}
	if !maps.Equal(got, want) {
		t.Errorf("git starts by subcommand %v, want %v", got, want)
	}
}

// A PICK MERGED ONTO THE PREVIOUS PICK'S TREE IS THE PICK MERGED ONTO ITS COMMIT: a git older than
// treeMergeGit (here one that says it is 2.44.0) commits every pick but the last and merges onto the
// commit, a newer one merges onto the tree, and the two walks answer the same — the same patched
// tree, the same members already upstream or empty — for a series of six onto a moved upstream.
func TestAPickOntoATreeIsThePickOntoItsCommit(t *testing.T) {
	u := newPatchedUpstream(t)
	u.wideSeries(t, 6)
	v11 := u.release(t, "v1.1.0", map[int]string{29: "twenty-nine", 4: "member xx"})
	series, list := u.checked(t)
	addr := mustAddr(t, u.source("main"))
	if v, err := u.store.GitVersion(); err != nil || !gitAtLeast(v, treeMergeGit) {
		t.Skipf("git %q cannot merge trees: %v", v, err)
	}
	walk := func(body string) (WalkResult, map[string]int) {
		t.Helper()
		bin, counts := countingGitWith(t, body)
		u.store.Git = bin
		w := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{})
		if w.Err != nil || w.Fit != 0 || !w.Results[0].Clean || w.Results[0].Entry.Commit != v11 {
			t.Fatalf("walk = %+v", w)
		}
		return w, counts()
	}
	old, oldCounts := walk(`if [ "$1" = version ]; then echo 'git version 2.44.0'; exit 0; fi`)
	cur, curCounts := walk("")
	if oldCounts["commit-tree"] != series.Len()-1-len(old.Results[0].Upstream)-len(old.Results[0].Empty) ||
		curCounts["commit-tree"] != 0 {
		t.Errorf("commit-tree starts: %d at git 2.44.0, %d at %s; want each pick but the last committed, then none",
			oldCounts["commit-tree"], curCounts["commit-tree"], cur.Git)
	}
	o, c := old.Results[0], cur.Results[0]
	if len(c.Upstream) != 1 { // the release made member 2's change: a pick between two that changes nothing
		t.Errorf("members already upstream %v, want the second", c.Upstream)
	}
	if o.Tree != c.Tree || !slices.Equal(o.Upstream, c.Upstream) || !slices.Equal(o.Empty, c.Empty) {
		t.Errorf("onto commits: tree %s upstream %v empty %v; onto trees: tree %s upstream %v empty %v",
			o.Tree, o.Upstream, o.Empty, c.Tree, c.Upstream, c.Empty)
	}
	if want := u.handRebaseTree(t, v11); c.Tree != want {
		t.Errorf("the replay's tree %s, the hand rebase's %s", c.Tree, want)
	}
}

// A PROBE THAT FINDS NOTHING MISSING IS ITS OWN CHECK: once the mirror holds the base's and the
// entry's files, a walk lists the base's and the entry's trees in one rev-list, fetching nothing;
// the am's commits are read without git (amCommits).
func TestAWalkOverAFilledMirrorListsEachTreeOnce(t *testing.T) {
	u := newPatchedUpstream(t)
	v11 := u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	series, list := u.checked(t)
	addr := mustAddr(t, u.source("main"))
	if w := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{}); w.Err != nil || w.Fit != 0 {
		t.Fatalf("filling walk = %+v", w)
	}
	bin, counts := countingGit(t)
	u.store.Git = bin
	w := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{})
	if w.Err != nil || w.Fit != 0 || w.Results[0].Entry.Commit != v11 {
		t.Fatalf("walk = %+v", w)
	}
	if got := counts(); got["rev-list"] != 1 || got["fetch"] != 0 {
		t.Errorf("git starts by subcommand %v, want one rev-list (the base's and the entry's listing) and no "+
			"fetch", got)
	}
}

// AN ENTRY THAT IS THE SERIES' BASE (an upstream that has not moved) IS LISTED ONCE: the base's
// probe found nothing missing under the walk's lock, so the entry's would only repeat it. One
// rev-list start, the base's listing, where the entry's probe and the am's commits made three.
func TestAWalkOverItsOwnBaseListsTheBaseOnce(t *testing.T) {
	u := newPatchedUpstream(t)
	series, list := u.checked(t)
	var entry []ListEntry
	for _, e := range list {
		if e.Commit == series.Base {
			entry = []ListEntry{e}
		}
	}
	if entry == nil {
		t.Fatalf("fixture: no entry on %+v is the base %s", list, series.Base)
	}
	addr := mustAddr(t, u.source("main"))
	if w := u.store.WalkSeries(addr.Repo, addr.Path, series, entry, WalkOptions{}); w.Err != nil || w.Fit != 0 {
		t.Fatalf("filling walk = %+v", w)
	}
	bin, counts := countingGit(t)
	u.store.Git = bin
	w := u.store.WalkSeries(addr.Repo, addr.Path, series, entry, WalkOptions{})
	if w.Err != nil || w.Fit != 0 || !w.Results[0].Clean || w.Results[0].Tree == "" {
		t.Fatalf("walk = %+v", w)
	}
	if got := counts(); got["rev-list"] != 1 || got["fetch"] != 0 {
		t.Errorf("git starts by subcommand %v, want one rev-list (the base's listing; the am's commits are "+
			"read without git) and no fetch", got)
	}
}

// ONE `git am` OVER THE WHOLE SERIES MAKES THE COMMITS ONE RUN PER MEMBER MAKES, the same ids in the
// same order, which is what lets applyAtBase take it (wholeSeriesAppliable).
func TestTheWholeSeriesAmMakesTheMemberByMemberCommits(t *testing.T) {
	u := newPatchedUpstream(t)
	u.wideSeries(t, 4)
	series, list := u.checked(t)
	addr := mustAddr(t, u.source("main"))
	if w := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{}); w.Err != nil || w.Fit != 0 {
		t.Fatalf("walk = %+v", w) // and the base's files are in the mirror now
	}
	if !wholeSeriesAppliable(series) {
		t.Fatal("a format-patch series is not taken whole")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	apply := func(whole bool) []string {
		t.Helper()
		sc, err := newScratchOf(u.store.git(), budget{ctx: ctx, d: time.Minute}, u.store.mirrorPath(addr.Repo))
		if err != nil {
			t.Fatal(err)
		}
		defer sc.remove()
		if _, err := sc.git("", "checkout", "-q", "--detach", series.Base); err != nil {
			t.Fatal(err)
		}
		files := filepath.Join(sc.dir, "series")
		if err := os.MkdirAll(files, 0o700); err != nil {
			t.Fatal(err)
		}
		if whole {
			commits, err := sc.amWhole(series, files)
			if err != nil || commits == nil {
				t.Fatalf("amWhole = %v, %v", commits, err)
			}
			return commits
		}
		commits, baseErr, err := sc.amEach(series, files)
		if err != nil || baseErr != nil {
			t.Fatalf("amEach = %v, %v, %v", commits, baseErr, err)
		}
		return commits
	}
	whole, each := apply(true), apply(false)
	if len(whole) != series.Len() || !slices.Equal(whole, each) {
		t.Errorf("one am made %v, one am per member %v", whole, each)
	}
}

// THE AM'S COMMITS READ FROM ITS FILES ARE GIT'S `rev-list --reverse HEAD ^<base>`, or nothing:
// amCommits answers for the chain one `git am` leaves in a fresh scratch repository and declines
// every other shape — a count the chain does not have, a HEAD that is a symbolic ref, commits
// that are not loose — so amWhole asks git then, and still names the same commits.
func TestAmCommitsIsRevListOrNothing(t *testing.T) {
	u := newPatchedUpstream(t)
	u.wideSeries(t, 3)
	series, list := u.checked(t)
	addr := mustAddr(t, u.source("main"))
	if w := u.store.WalkSeries(addr.Repo, addr.Path, series, list[:1], WalkOptions{}); w.Err != nil || w.Fit != 0 {
		t.Fatalf("walk = %+v", w) // and the base's files are in the mirror now
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sc, err := newScratchOf(u.store.git(), budget{ctx: ctx, d: time.Minute}, u.store.mirrorPath(addr.Repo))
	if err != nil {
		t.Fatal(err)
	}
	defer sc.remove()
	if _, _, err := sc.applyAtBase(series); err != nil {
		t.Fatal(err)
	}
	out, err := sc.git("", "rev-list", "--reverse", "HEAD", "^"+series.Base)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(out)
	if got, ok := sc.amCommits(series.Base, series.Len()); !ok || !slices.Equal(got, want) {
		t.Fatalf("amCommits = %v, %v; rev-list names %v", got, ok, want)
	}
	for _, n := range []int{0, series.Len() - 1, series.Len() + 1} {
		if got, ok := sc.amCommits(series.Base, n); ok {
			t.Errorf("amCommits(%d) = %v for a chain of %d", n, got, series.Len())
		}
	}
	if _, ok := sc.amCommits(want[0], series.Len()); ok {
		t.Error("amCommits answered for a base the chain does not end at")
	}

	// Packed, the commits are not files amCommits reads, and the whole-series am asks git.
	if _, err := sc.git("", "repack", "-a", "-d", "-l", "-q"); err != nil {
		t.Fatal(err)
	}
	if got, ok := sc.amCommits(series.Base, series.Len()); ok {
		t.Errorf("amCommits read packed commits: %v", got)
	}
	head, ok := sc.detachedHead()
	if !ok || head != want[len(want)-1] {
		t.Errorf("detachedHead = %q, %v; want %s", head, ok, want[len(want)-1])
	}
	if _, err := sc.git("", "checkout", "-q", "-b", "named"); err != nil {
		t.Fatal(err)
	}
	if got, ok := sc.detachedHead(); ok {
		t.Errorf("detachedHead read a symbolic HEAD as %q", got)
	}
}

// A SERIES THAT IS NOT ONE MAIL A MEMBER is applied member by member: a member holding two mails,
// or one not led by a mail's "From " line, would make a different number of commits in one run.
func TestASeriesNotOneMailAMemberIsAppliedMemberByMember(t *testing.T) {
	mail := []byte("From 0123 Mon Sep 17 00:00:00 2001\nSubject: x\n\n---\n")
	for name, members := range map[string][][]byte{
		"one member":         {mail},
		"two mails in one":   {mail, append(append([]byte{}, mail...), mail...)},
		"no From line":       {mail, []byte("Subject: x\n\n---\n")},
		"From line not lead": {mail, append([]byte("\n"), mail...)},
	} {
		s := &Series{}
		for _, m := range members {
			s.Members = append(s.Members, SeriesMember{Name: "m.patch", Data: m})
		}
		if wholeSeriesAppliable(s) {
			t.Errorf("%s: taken whole", name)
		}
	}
	if !wholeSeriesAppliable(&Series{Members: []SeriesMember{{Data: mail}, {Data: mail}}}) {
		t.Error("two one-mail members are not taken whole")
	}
}

// THE TREE A CHECK'S LISTING STARTS WITH is the one the pick compares against; a listing led by
// anything else is no answer, and the pick asks git.
func TestListedRootTree(t *testing.T) {
	tree := strings.Repeat("a", 40)
	for in, want := range map[string]string{
		tree + " \n" + strings.Repeat("b", 40) + " f.txt\n": tree,
		"?" + tree + "\n": "",
		"":                "",
		"not an id\n":     "",
	} {
		if got := listedRootTree(in); got != want {
			t.Errorf("listedRootTree(%q) = %q, want %q", in, got, want)
		}
	}
}
