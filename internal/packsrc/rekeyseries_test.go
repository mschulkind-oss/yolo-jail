package packsrc

import "testing"

// THE RE-KEY MOVES WHAT ONE DIGEST NAMES TO ANOTHER (PF-D62): the good build when it is a build of the
// old series and recipe, and every outcome of the old series, a failed build's recipe with it; an
// outcome of another series and a good build of another recipe stay as they are.
func TestRekeySeriesMovesTheGoodBuildAndTheOutcomesOfTheOldDigest(t *testing.T) {
	r := &CheckRecord{
		Good: &GoodBuild{Commit: "c1", Series: "old", Recipe: "old-recipe", Entry: "k1"},
		Outcomes: []EntryOutcome{
			{Commit: "c2", Kind: OutcomeConflict, Series: "old", Member: "0001-x.patch"},
			{Commit: "c3", Kind: OutcomeBuildFailed, Series: "old", Recipe: "old-recipe", Count: 2},
			{Commit: "c4", Kind: OutcomeConflict, Series: "other"},
		},
	}
	if !r.RekeySeries("old", "old-recipe", "new", "new-recipe") {
		t.Fatal("the re-key reports nothing moved")
	}
	if g := r.Good; g.Series != "new" || g.Recipe != "new-recipe" || g.Commit != "c1" || g.Entry != "k1" {
		t.Errorf("good build = %+v, want the new digest and recipe and the rest kept", g)
	}
	want := []EntryOutcome{
		{Commit: "c2", Kind: OutcomeConflict, Series: "new", Member: "0001-x.patch"},
		{Commit: "c3", Kind: OutcomeBuildFailed, Series: "new", Recipe: "new-recipe", Count: 2},
		{Commit: "c4", Kind: OutcomeConflict, Series: "other"},
	}
	for i, o := range r.Outcomes {
		if o.Commit != want[i].Commit || o.Series != want[i].Series || o.Recipe != want[i].Recipe ||
			o.Member != want[i].Member || o.Count != want[i].Count {
			t.Errorf("outcome %d = %+v, want %+v", i, o, want[i])
		}
	}
	if r.RekeySeries("old", "old-recipe", "new", "new-recipe") {
		t.Error("a second re-key reports a move")
	}
	edited := &CheckRecord{Good: &GoodBuild{Commit: "c1", Series: "old", Recipe: "another-recipe"}}
	if edited.RekeySeries("old", "old-recipe", "new", "new-recipe") || edited.Good.Series != "old" {
		t.Errorf("a good build of another recipe (an edited build) was re-keyed: %+v", edited.Good)
	}
}
