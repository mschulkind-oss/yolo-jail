package packsrc

// localclones_test.go pins the LOCAL CLONE MAP (localclones.go, PF-D100): a patch branch a pack names by its
// published URL is resolved against a registered clone whose remote names the same repository, with the
// newest-copy rule of a local checkout, and the provenance line names the clone.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const publishedPatchBranch = "git+https://github.com/o/r?ref=mine"

// cloneWant is the fixture's want with its patch branch at the published URL.
func (f *checkoutFixture) cloneWant() PatchedWant {
	w := f.want()
	w.PatchBranch = publishedPatchBranch
	return w
}

// useClones installs clones as the map for the test.
func useClones(t *testing.T, clones ...LocalClone) {
	t.Helper()
	old := SetLocalCloneSource(func() ([]LocalClone, error) { return clones, nil })
	t.Cleanup(func() { SetLocalCloneSource(old) })
}

func (f *checkoutFixture) cloneLaunch(t *testing.T) CheckResult {
	t.Helper()
	res := f.store.CheckPatched(f.cloneWant(), CheckOptions{Now: func() time.Time { return f.now }})
	if res.Err != nil {
		t.Fatalf("CheckPatched: %v", res.Err)
	}
	if res.Record == nil || res.Record.PatchBranch == nil {
		t.Fatalf("no patch branch resolution; check: %+v", res.Record.Check)
	}
	return res
}

// An explicit local_clones entry, in a clone holding no pack, whose remote is written scp-style while the pack
// names the https URL: the clone's unpushed local branch builds, and the line names the clone.
func TestAPublishedURLBuildsTheRegisteredClonesNewerLocalBranch(t *testing.T) {
	f := newCheckoutFixture(t)
	f.publishedRemote(t)
	tip := f.localMine(t, gitIn(t, f.pusher, "rev-parse", "mine"), 1)
	useClones(t, LocalClone{Dir: f.machine, Explicit: true, From: "~/src/machine",
		Remotes: []CloneRemote{{Name: "origin", URL: "git@github.com:o/r.git"}}})
	before := userRefs(t, f.machine)
	res := f.cloneLaunch(t)
	pb := res.Record.PatchBranch
	if pb.Commit != tip || pb.Copy != "local" {
		t.Fatalf("built %s from %q, want the clone's local branch %s", pb.Commit, pb.Copy, tip)
	}
	if pb.Source != publishedPatchBranch {
		t.Errorf("the record keys the resolution by %q, want the locator as written", pb.Source)
	}
	wantLine(t, pb, f.now, "mine", "local", "1 unpushed", "local clone "+f.machine, "local_clones entry ~/src/machine")
	if after := userRefs(t, f.machine); after != before {
		t.Errorf("the check wrote to the user's clone:\nbefore %s\nafter  %s", before, after)
	}
	// The series every reader rebuilds from the record finds the commit in the clone's mirror.
	s, err := PatchBranchSeries(publishedPatchBranch, "", f.source("main"), res.Record)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.PatchBranch.mirrorRepo(); got != "file://"+f.machine {
		t.Errorf("the series reads the commit from %q, want the clone's mirror", got)
	}
}

// A clone whose local branch is behind the published tip builds the published tip.
func TestABehindRegisteredCloneBuildsThePublishedTip(t *testing.T) {
	f := newCheckoutFixture(t)
	f.publishedRemote(t)
	f.localMine(t, gitIn(t, f.pusher, "rev-parse", "mine"), 0)
	pushed := f.push(t, map[int]string{10: "ten", 20: "twenty"})
	useClones(t, LocalClone{Dir: f.machine, From: "other",
		Remotes: []CloneRemote{{Name: "origin", URL: "https://github.com/o/r.git"}}})
	res := f.cloneLaunch(t)
	pb := res.Record.PatchBranch
	if pb.Commit != pushed || pb.Copy != "origin" {
		t.Fatalf("built %s from %q, want the published tip %s", pb.Commit, pb.Copy, pushed)
	}
	wantLine(t, pb, f.now, "local branch is stale, 1 behind origin", "local clone "+f.machine, "the path of pack other")
}

// Two clones match the URL: an explicit entry wins over an implicit one listed first, and the line says which
// also matched.
func TestTwoMatchingClonesTheExplicitEntryWins(t *testing.T) {
	f := newCheckoutFixture(t)
	f.publishedRemote(t)
	tip := f.localMine(t, gitIn(t, f.pusher, "rev-parse", "mine"), 2)
	other := filepath.Join(filepath.Dir(f.machine), "other")
	gitIn(t, filepath.Dir(f.machine), "clone", "-q", f.origin, other)
	useClones(t,
		LocalClone{Dir: other, From: "pack-a", Remotes: []CloneRemote{{Name: "origin", URL: "ssh://git@github.com/o/r"}}},
		LocalClone{Dir: f.machine, Explicit: true, From: "~/m", Remotes: []CloneRemote{{Name: "origin", URL: "git@github.com:o/r"}}})
	pb := f.cloneLaunch(t).Record.PatchBranch
	if pb.Checkout != f.machine || pb.Commit != tip {
		t.Fatalf("resolved in %s at %s, want the explicit clone %s at %s", pb.Checkout, pb.Commit, f.machine, tip)
	}
	wantLine(t, pb, f.now, "local clone "+f.machine, "local_clones entry ~/m", "also matched "+other)
}

// With no matching clone the URL form is unchanged: the map is not consulted for another repository.
func TestAURLNoCloneMatchesIsNotRedirected(t *testing.T) {
	f := newCheckoutFixture(t)
	f.publishedRemote(t)
	useClones(t, LocalClone{Dir: f.machine, Explicit: true, From: "~/m",
		Remotes: []CloneRemote{{Name: "origin", URL: "git@github.com:someone/else.git"}}})
	c, err := ParseFrom(publishedPatchBranch, "")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := f.store.chooseClone(c)
	if err != nil || ch.match != nil || ch.skip != "" {
		t.Fatalf("an unmatched URL chose %+v (%v)", ch, err)
	}
	got, plan := f.store.patchBranchPlan(c, ch)
	if got.Repo != c.Repo || plan != nil {
		t.Fatalf("an unmatched URL was planned at %q (plan %v)", got.Repo, plan)
	}
}

func TestRepoIdentityMatchesSpellingsOfOneRepository(t *testing.T) {
	same := []string{"git@github.com:o/r.git", "ssh://git@github.com/o/r", "https://github.com/o/r.git",
		"git+https://GitHub.com/o/r/", "git+ssh://git@github.com/o/r.git"}
	for _, s := range same {
		if got := RepoIdentity(s); got != "github.com/o/r" {
			t.Errorf("RepoIdentity(%q) = %q, want github.com/o/r", s, got)
		}
	}
	if RepoIdentity("https://github.com/o/other") == RepoIdentity("https://github.com/o/r") {
		t.Error("two repositories share an identity")
	}
	if a, b := RepoIdentity("/srv/r.git"), RepoIdentity("file:///srv/r.git"); a != b || !strings.HasPrefix(a, "file:") {
		t.Errorf("a path and its file:// URL differ: %q vs %q", a, b)
	}
}
