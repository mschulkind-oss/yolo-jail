package packsrc

// checkgitstarts_test.go pins how many git processes a refresh's and a check's ref reading starts,
// through Refresh and CheckPatched: starting git is most of what these cost where starting a
// process is slow (about 25 ms a start on a macOS runner), and a pack check runs them at every
// launch. countingGit (replaybudget_test.go) counts the starts by subcommand.

import (
	"testing"
)

// A REF'S CLASSIFICATION IS ONE GIT START, whatever it is: a bare branch name, which is looked
// up as a tag first, and a name nothing resolves, which is looked up as a tag, a branch and as
// written, each in one `cat-file --batch-check` (classifyRef), where one rev-parse a candidate
// made two and three. A refresh inside a branch's interval reads its ref once and fetches nothing.
func TestARefreshClassifiesItsRefInOneGitStart(t *testing.T) {
	f := newRefreshFixture(t)
	c1 := f.head(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	if o := f.refresh(t, false, pack)[0]; o.Err != nil || o.Commit != c1 {
		t.Fatalf("first refresh = %+v", o)
	}
	bin, counts := countingGit(t)
	f.store.Git = bin
	o := f.refresh(t, false, pack)[0]
	if o.Err != nil || o.FetchErr != nil || o.Fetched || o.Commit != c1 {
		t.Fatalf("refresh inside the interval = %+v, want %s unfetched", o, c1[:8])
	}
	if got := counts(); got["cat-file"] != 1 || got["rev-parse"] != 0 || got["fetch"] != 0 {
		t.Errorf("git starts by subcommand %v, want the branch classified in one cat-file, no rev-parse, "+
			"no fetch", got)
	}

	mirror := f.store.mirrorPath(mustParse(t, f.source("main")).Repo)
	for _, ref := range []string{"main", "nope"} {
		bin, counts := countingGit(t)
		f.store.Git = bin
		if _, _, _, err := f.store.classifyRef(mirror, ref); err != nil {
			t.Fatalf("classifyRef(%q): %v", ref, err)
		}
		if got := counts(); got["cat-file"] != 1 || len(got) != 1 {
			t.Errorf("classifyRef(%q): git starts by subcommand %v, want one cat-file", ref, got)
		}
	}
}

// A CHECK READS ITS REF AND THE SERIES' BASE IN ONE GIT START after its fetch (classifyRefAnd),
// and asks whether the branch contains the base in the for-each-ref that lists the versions
// containing it (versionsContainingOnBranch): no rev-parse for the base, no merge-base. A forced
// check of a branch is then two classifications (before and after the fetch) and three
// for-each-ref (the tag snapshot before and after the fetch, and the list).
func TestACheckReadsItsRefAndBaseInOneGitStart(t *testing.T) {
	u := newPatchedUpstream(t)
	v11 := u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	w := u.want(t, "main", "")
	u.check(t, w, true)
	bin, counts := countingGit(t)
	u.store.Git = bin
	res := u.check(t, w, true)
	f := res.Record.Check
	if f == nil || f.Problem != "" || f.FetchErr != "" || !f.BaseOnBranch || f.RefKind != "branch" ||
		listLabels(f.List) != "v1.1.0 v1.0.0" || f.List[0].Commit != v11 {
		t.Fatalf("check = %+v", f)
	}
	got := counts()
	if got["rev-parse"] != 0 || got["cat-file"] != 2 {
		t.Errorf("git starts by subcommand %v, want the ref and the base read in two cat-file (before "+
			"and after the fetch) and no rev-parse", got)
	}
	if got["merge-base"] != 0 || got["for-each-ref"] != 3 {
		t.Errorf("git starts by subcommand %v, want no merge-base and three for-each-ref (the tag "+
			"snapshot before and after the fetch, and the versions with the branch)", got)
	}
}

// A BRANCH THAT DOES NOT CONTAIN THE SERIES' BASE says so from the one for-each-ref: the base is a
// commit of the upstream (a version tag holds it) and the followed branch shares no history with
// it, so the branch is not listed beside the tags, and nothing on it is a version containing the
// base.
func TestACheckOfABranchWithoutTheBaseSaysSo(t *testing.T) {
	u := newPatchedUpstream(t)
	gitIn(t, u.repo, "checkout", "-q", "--orphan", "elsewhere")
	commitFile(t, u.repo, "other.txt", "other\n")
	gitIn(t, u.repo, "tag", "v2.0.0")
	gitIn(t, u.repo, "checkout", "-q", "main")
	res := u.check(t, u.want(t, "elsewhere", "head"), true)
	f := res.Record.Check
	if f == nil || f.Problem != "" || f.RefKind != "branch" || f.BaseOnBranch {
		t.Fatalf("check = %+v, want the branch followed and its base not on it", f)
	}
	if got := listLabels(f.List); len(f.List) != 1 || !f.List[0].Tip || f.List[0].Tag != "" {
		t.Errorf("the walk's list = %q, want the tip alone (v2.0.0 does not contain the base)", got)
	}
}
