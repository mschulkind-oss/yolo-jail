package packdecl

import (
	"strings"
	"testing"
)

// decodeNeeds decodes a pack manifest body, returning the problems.
func decodeNeeds(t *testing.T, body string) (*Manifest, []string) {
	t.Helper()
	m, problems := Decode([]byte(body))
	return m, problems
}

// TestNeedsDecodes is the happy path, and it also pins that `needs` is a
// TOP-LEVEL key beside `name` (the wire-bridge design §3.1's spelling), that the
// condition round-trips, and that an absent when_bins decodes as absent — the
// unconditional form, which the design allows and nothing ships.
func TestNeedsDecodes(t *testing.T) {
	m, problems := decodeNeeds(t, `{
	  "name": "cerebras",
	  "needs": [
	    {"pack": "wire-bridge", "when_bins": ["claude"]},
	    {"pack": "other"}
	  ]
	}`)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	got := m.DeclaredNeeds()
	if len(got) != 2 {
		t.Fatalf("DeclaredNeeds() = %v, want two entries", got)
	}
	if got[0].Pack != "wire-bridge" {
		t.Errorf("Pack = %q, want %q", got[0].Pack, "wire-bridge")
	}
	if len(got[0].WhenBins) != 1 || got[0].WhenBins[0] != "claude" {
		t.Errorf("WhenBins = %v, want [claude]", got[0].WhenBins)
	}
	if got[1].Pack != "other" || got[1].WhenBins != nil {
		t.Errorf("unconditional entry = %+v, want pack only with no bins", got[1])
	}
}

// TestNeedsRefusesABadPackName: the pack name is required and may not carry "=".
// Both are refused by the STRICT decoder with a message that states the rule —
// an author who shipped either would otherwise get a resolution-time failure one
// rebuild away, for a typo the manifest could have caught on the spot.
func TestNeedsRefusesABadPackName(t *testing.T) {
	for _, tc := range []struct {
		needle string
		body   string
	}{
		{"empty pack", `{"name": "p", "needs": [{"when_bins": ["claude"]}]}`},
		{"selector spelling", `{"name": "p", "needs": [{"pack": "a=b"}]}`},
	} {
		_, problems := decodeNeeds(t, tc.body)
		if len(problems) == 0 {
			t.Errorf("%s: a bad pack name was accepted:\n%s", tc.needle, tc.body)
			continue
		}
		joined := strings.Join(problems, "\n")
		if !strings.Contains(joined, "needs[0]") {
			t.Errorf("%s: the problem must name the needs entry:\n%s", tc.needle, joined)
		}
	}
}

// TestNeedsRefusesAnEmptyBinEntry: an empty when_bins entry is refused, naming
// the entry the author's editor produced.
func TestNeedsRefusesAnEmptyBinEntry(t *testing.T) {
	_, problems := decodeNeeds(t,
		`{"name": "p", "needs": [{"pack": "q", "when_bins": ["claude", ""]}]}`)
	if len(problems) == 0 {
		t.Fatal("an empty when_bins entry was accepted")
	}
	if !strings.Contains(strings.Join(problems, "\n"), `when_bins[1]`) {
		t.Errorf("the problem must name the bin entry: %v", problems)
	}
}

// TestNeedsProblemsReportEveryEntryAtOnce: Decode's contract is every problem,
// not the first — two broken entries cost one edit-check cycle, not two.
func TestNeedsProblemsReportEveryEntryAtOnce(t *testing.T) {
	_, problems := decodeNeeds(t,
		`{"name": "p", "needs": [{}, {"pack": "a=b"}]}`)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "needs[0]") || !strings.Contains(joined, "needs[1]") {
		t.Errorf("both entries' problems must be reported:\n%s", joined)
	}
}

// TestNeedsValidationRunsOnTheTolerantPath: the rules validateNeeds enforces are
// version-INVARIANT — typos both ends of a version boundary agree about — so the
// tolerant decoder refuses them exactly as the strict one does. Letting them pass
// tolerantly would be the `tier` incident's mirror image: a manifest both builds
// can read, delivering a dependency neither will honor.
func TestNeedsValidationRunsOnTheTolerantPath(t *testing.T) {
	_, problems, _ := DecodeTolerant([]byte(`{"name": "p", "needs": [{"pack": ""}]}`))
	if len(problems) == 0 {
		t.Fatal("the tolerant path accepted an empty needs pack name")
	}
}

// TestNeedsTolerantPathSkipsWhatItDoesNotKnow: the tolerant path is for reading a
// manifest some OTHER build wrote. A key this build has never heard of is skipped
// exactly like every other unknown top-level key (plain json.Unmarshal — silently
// ignored at the top level; the skip-AND-report mechanism exists one level down,
// for unknown kinds), while `needs` itself — which this build knows — is still
// parsed and still validated. The strict path refuses the same manifest, because
// an unknown key is an authoring problem there.
func TestNeedsTolerantPathSkipsWhatItDoesNotKnow(t *testing.T) {
	body := []byte(`{"name": "p", "future_top_level_key": 1,
	  "needs": [{"pack": "wire-bridge", "when_bins": ["claude"]}]}`)
	m, problems, skipped := DecodeTolerant(body)
	if len(problems) > 0 || len(skipped) > 0 {
		t.Fatalf("tolerant decode refused or reported: %v / %v", problems, skipped)
	}
	if len(m.DeclaredNeeds()) != 1 || m.DeclaredNeeds()[0].Pack != "wire-bridge" {
		t.Errorf("the key this build KNOWS must still parse: %+v", m.DeclaredNeeds())
	}
	_, problems = Decode(body)
	if len(problems) == 0 {
		t.Error("the strict path must refuse an unknown top-level key")
	}
}
