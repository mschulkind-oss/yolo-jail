package packsrc

// replaybudget_test.go pins how many git processes a replay starts. Starting git is most of what a
// replay costs where starting a process is slow (about 25 ms a start on a macOS runner, measured
// across both Apple's git and nix's), so the count is the replay's cost there: the series is applied
// at its base in ONE `git am` whatever its length, the entry's tree is read from the listing its
// files' check already made, and the replay's last pick is not committed. Only the picks themselves
// — one merge per member and a commit between two — grow with the series.

import (
	"context"
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
	log := filepath.Join(t.TempDir(), "runs")
	bin = wrappedGit(t, `printf '%s\n' "$*" >> `+shquote.Quote(log))
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
		if got["am"] != 1 || got["rev-parse"] != 0 || got["cat-file"] != 0 ||
			got["merge-tree"] != n || got["commit-tree"] != n-1 {
			t.Errorf("n=%d: git starts by subcommand %v, want one am, no rev-parse or cat-file, %d merge-tree "+
				"and %d commit-tree", n, got, n, n-1)
		}
		other := 0
		for sub, c := range got {
			if sub != "merge-tree" && sub != "commit-tree" {
				other += c
			}
		}
		others = append(others, other)
	}
	// At most 11: `git version`, the scratch repository's init and checkout, the one am and the
	// rev-list naming its commits, and for the base and the entry each a probe of the blobless
	// mirror, the fetch of what it lacks and the check after it (a probe that finds nothing missing
	// is its own check, and the entry's listing is the pick's tree).
	if others[0] != others[1] || others[1] > 11 {
		t.Errorf("git starts outside the picks: %v for series of 2 and 6, want the same, at most 11", others)
	}
}

// A PROBE THAT FINDS NOTHING MISSING IS ITS OWN CHECK: once the mirror holds the base's and the
// entry's files, a walk lists each tree once — the base, the entry — and the am's commits once,
// fetching nothing.
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
	if got := counts(); got["rev-list"] != 3 || got["fetch"] != 0 {
		t.Errorf("git starts by subcommand %v, want three rev-list (the base's and the entry's listings, the "+
			"am's commits) and no fetch", got)
	}
}

// AN ENTRY THAT IS THE SERIES' BASE (an upstream that has not moved) IS LISTED ONCE: the base's
// probe found nothing missing under the walk's lock, so the entry's would only repeat it. Two
// rev-list starts, the base's listing and the am's commits, where the entry's probe made three.
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
	if got := counts(); got["rev-list"] != 2 || got["fetch"] != 0 {
		t.Errorf("git starts by subcommand %v, want two rev-list (the base's listing, the am's commits) "+
			"and no fetch", got)
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
