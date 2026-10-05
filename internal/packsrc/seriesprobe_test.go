package packsrc

// seriesprobe_test.go pins the SERIES PROBE `yolo pack lint --online` asks of a scratch store
// (seriesprobe.go; docs/design/patched-forks.md PF-D61): the forced check's answer, a tagless
// branch told apart from what the check says of it, and the series replayed at its own base.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A SERIES THAT FITS: the check finds the newest version containing the base, and the series
// replays at that base.
func TestTheProbeFindsTheNewestVersionAndReplaysAtTheBase(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	s := u.series(t)
	p := u.store.ProbeSeries(u.want(t, "main", ""), s, nil)
	if p.Err != nil {
		t.Fatalf("probe: %v", p.Err)
	}
	if p.Found.Problem != "" || p.Found.FetchErr != "" || p.Found.RefKind != "branch" ||
		len(p.Found.List) == 0 || p.Found.List[0].Tag != "v1.1.0" {
		t.Errorf("the check's answer = %+v, want a branch whose newest version is v1.1.0", p.Found)
	}
	if p.Tagless {
		t.Error("a branch carrying v1.0.0 and v1.1.0 is reported tagless")
	}
	if !p.Replayed || p.Walk.Base != nil || p.Walk.Err != nil {
		t.Errorf("the base replay = replayed %v, %+v, want the series applied at its base", p.Replayed, p.Walk)
	}
}

// A TAGLESS BRANCH under a release rule is said by the probe itself, whatever the check makes of
// it, so lint names it under either reading of an untagged upstream (request 4 may move the
// check's).
func TestTheProbeSaysABranchCarriesNoVersionTag(t *testing.T) {
	u := newPatchedUpstream(t)
	gitIn(t, u.repo, "tag", "-d", "v1.0.0")
	u.release(t, "", map[int]string{14: "fourteen"})
	p := u.store.ProbeSeries(u.want(t, "main", ""), u.series(t), nil)
	if p.Err != nil {
		t.Fatalf("probe: %v", p.Err)
	}
	if !p.Tagless {
		t.Errorf("a branch with no tag at all is not reported tagless: %+v", p.Found)
	}
	if !p.Replayed || p.Walk.Base != nil {
		t.Errorf("the base replay = replayed %v, %+v: the base is on the branch and the series applies there",
			p.Replayed, p.Walk)
	}
	// A head rule follows the commits, so no tag is missing.
	if p := u.store.ProbeSeries(u.want(t, "main", "head"), u.series(t), nil); p.Tagless {
		t.Error("a head rule's branch is reported tagless")
	}
}

// A SERIES THAT DOES NOT APPLY AT ITS OWN BASE is the replay's base error: a malformed series.
func TestTheProbeSaysASeriesDoesNotApplyAtItsBase(t *testing.T) {
	u := newPatchedUpstream(t)
	moved := u.release(t, "v1.1.0", map[int]string{10: "TEN"})
	// The same members, claiming a base whose line 10 their context does not match.
	dir := filepath.Join(u.pack, "patches")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(data), u.base, moved)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := u.series(t)
	if s.Base != moved {
		t.Fatalf("the rewritten series' base = %s, want %s", s.Base, moved)
	}
	p := u.store.ProbeSeries(u.want(t, "main", ""), s, nil)
	if p.Err != nil || p.Found.Problem != "" {
		t.Fatalf("probe: %v, %+v", p.Err, p.Found)
	}
	if !p.Replayed || p.Walk.Base == nil || !strings.HasPrefix(p.Walk.Base.Member, "0001-") {
		t.Errorf("the base replay = replayed %v, %+v, want 0001 refused at the base", p.Replayed, p.Walk)
	}
}

// AN UPSTREAM THAT CANNOT BE FETCHED is the check's fetch error, and nothing is replayed.
func TestTheProbeOfAnUnreachableUpstreamReplaysNothing(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	w.Source = "git+file://" + filepath.Join(t.TempDir(), "gone") + "?ref=main"
	p := u.store.ProbeSeries(w, u.series(t), nil)
	if p.Err != nil {
		t.Fatalf("probe: %v", p.Err)
	}
	if p.Found.FetchErr == "" || p.Replayed {
		t.Errorf("an unreachable upstream = %+v, replayed %v, want the fetch error and no replay", p.Found, p.Replayed)
	}
}
