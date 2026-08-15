package run

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestPackSupersessionsFlattensLoadedPacks pins the seam that makes supersession real.
//
// The capability design shipped complete except for two calls in this package, and until they
// existed the whole feature was INERT: a pack could declare `supersedes`, the loophole could
// declare `serves`, every unit test could pass, and no launch would ever withhold anything —
// because the process-wide record stayed empty. That is the third time in this batch a value
// was computed correctly and then not consumed, so it gets a test at the consuming end rather
// than only at the producing one.
func TestPackSupersessionsFlattensLoadedPacks(t *testing.T) {
	packs := []*packload.Pack{
		{Name: "claude-bedrock", Decl: &packdecl.Manifest{
			Supersedes: []packdecl.Supersession{{
				Capability: "claude-oauth-refresh",
				Because:    "Bedrock overrides the OAuth path; no token is ever refreshed",
			}},
		}},
		{Name: "plain-pack", Decl: &packdecl.Manifest{}},
	}

	got := packSupersessions(packs)
	if len(got) != 1 {
		t.Fatalf("want exactly the one claim the one pack made, got %d: %+v", len(got), got)
	}
	if got[0].Pack != "claude-bedrock" {
		t.Errorf("the claim must carry the pack that made it, or the user cannot be told who "+
			"turned their loophole off; got %q", got[0].Pack)
	}
	if got[0].Capability != "claude-oauth-refresh" {
		t.Errorf("capability = %q", got[0].Capability)
	}
	if !strings.Contains(got[0].Because, "Bedrock overrides") {
		t.Errorf("the mandatory reason must survive the flattening — it is what the user reads "+
			"when the loophole reports itself superseded; got %q", got[0].Because)
	}
}

// A pack that declares nothing contributes nothing — the empty case has to stay empty, or
// silence would start meaning something. loopholes.Superseded() keys on a non-empty record.
func TestPackSupersessionsIsEmptyForPacksThatClaimNothing(t *testing.T) {
	if got := packSupersessions([]*packload.Pack{{Name: "a", Decl: &packdecl.Manifest{}}}); len(got) != 0 {
		t.Errorf("a pack claiming nothing produced %d supersession(s): %+v", len(got), got)
	}
	if got := packSupersessions(nil); len(got) != 0 {
		t.Errorf("no packs produced %d supersession(s)", len(got))
	}
}

// And the record actually reaches internal/loopholes. Without this the two helpers above
// could be perfect and still never called — which is precisely the state this wiring fixed.
func TestSetPackSupersessionsIsReachedFromTheStagedSet(t *testing.T) {
	t.Cleanup(loopholes.ResetPackSupersessions)

	loopholes.SetPackSupersessions(packSupersessions([]*packload.Pack{
		{Name: "claude-bedrock", Decl: &packdecl.Manifest{
			Supersedes: []packdecl.Supersession{{
				Capability: "claude-oauth-refresh", Because: "Bedrock overrides the OAuth path",
			}},
		}},
	}))

	got := loopholes.PackSupersessions()
	if len(got) != 1 {
		t.Fatalf("the staged claims did not reach internal/loopholes — the record holds %d "+
			"entries, so supersession is inert no matter how correct the rest is", len(got))
	}
	if got[0].Pack != "claude-bedrock" || got[0].Capability != "claude-oauth-refresh" {
		t.Errorf("the record arrived garbled: %+v", got[0])
	}
	if !strings.Contains(got[0].Line(), "Bedrock overrides") {
		t.Errorf("the reason must survive into the line the user reads: %q", got[0].Line())
	}
}

// And stageRunPacks ACTUALLY CALLS it. Asserted over the source, because the two helpers
// being correct while nothing invokes them is precisely the state this wiring fixed — and a
// behavioural test cannot reach it: stageRunPacks stages a real tree, so the seam that would
// prove the call is the one thing a unit test cannot stand up.
//
// MEASURED: with the call deleted, every other test in this package still passed. That is the
// same shape as the relay's spare branch, which could be removed wholesale with the suite
// green, and it is why a source assertion is the honest instrument here rather than a
// fallback. It costs nothing and it fails loudly on the one edit that silently disarms the
// feature.
func TestStageRunPacksRecordsSupersessions(t *testing.T) {
	body, err := os.ReadFile("packs.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	for _, want := range []string{
		"loopholes.SetPackSupersessions(packSupersessions(loaded))",
		"loopholes.SetPackSupersessionResolver(resolvePackSupersessions)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("packs.go no longer contains %q.\n\nWithout it the supersession record is "+
				"never populated, so every `supersedes` claim is silently ignored: a pack says a "+
				"job need not be done, the loophole keeps doing it, and nothing reports a "+
				"disagreement. The feature is inert and every unit test still passes.", want)
		}
	}
}
