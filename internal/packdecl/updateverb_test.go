package packdecl

import (
	"strings"
	"testing"
)

// updateverb_test.go covers `program`'s `update` verb (program-delivery.md OQ-PD14): the
// argv the installed program is run with to update ITSELF.
//
// The field exists because the vendors disagree — `claude install`, `codex update`,
// `agy update` — and core hardcoding one is how `yolo pack update` came to skip the
// installer class entirely. So the properties worth pinning are: the value SURVIVES the
// projection every consumer reads (a field the manifest accepts and the projection drops
// is a declaration that silently does nothing), it is accepted for BOTH vias, it is
// refused on kinds that could not run it, and an absent verb is not an error.

// TestUpdateVerbSurvivesDecodeAndProjection is the field's load-bearing cell: the
// projection is the ONLY way core reads a contribution, so a verb that does not reach
// Install.UpdateVerb never reaches a launcher no matter how well it decoded.
func TestUpdateVerbSurvivesDecodeAndProjection(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"claude","via":"installer","url":"https://claude.ai/install.sh",
	   "update":["install"]},
	  {"kind":"program","bin":"pi","via":"npm","package":"@earendil-works/pi-coding-agent",
	   "update":["update","--self"]},
	  {"kind":"program","bin":"copilot","via":"npm","package":"@github/copilot"}]}`))
	if len(probs) != 0 {
		t.Fatalf("an update verb should decode cleanly, got: %v", probs)
	}

	installs := m.InstallContributions()
	if len(installs) != 3 {
		t.Fatalf("InstallContributions = %d, want 3: %+v", len(installs), installs)
	}
	byBin := map[string]Install{}
	for _, in := range installs {
		byBin[in.Bin] = in
	}

	// An INSTALLER-delivered program's verb.
	if got := byBin["claude"].UpdateVerb; len(got) != 1 || got[0] != "install" {
		t.Errorf("claude's update verb did not survive the projection: %v", got)
	}
	// An NPM-delivered one's — the projection must not gate the verb on `via`, because
	// the verb describes what the PROGRAM does to itself, not how it arrived.
	if got := byBin["pi"].UpdateVerb; len(got) != 2 || got[0] != "update" || got[1] != "--self" {
		t.Errorf("an npm program's update verb must survive too, got %v", got)
	}
	// And absence stays absence: the launcher's fallback ("re-run the installer", or
	// `npm install -g`) is chosen by there being nothing here, so a defaulted verb would
	// silently take the wrong branch.
	if got := byBin["copilot"].UpdateVerb; len(got) != 0 {
		t.Errorf("a program declaring no verb must project none, got %v", got)
	}
}

// TestUpdateVerbIsRefusedOnKindsThatCannotRunIt: `requires` installs nothing and the
// content kinds have no program, so a verb on any of them is read by no consumer. A field
// accepted and ignored is the defect `requires does not take "via"` already refuses.
func TestUpdateVerbIsRefusedOnKindsThatCannotRunIt(t *testing.T) {
	for _, entry := range []string{
		`{"kind":"requires","bin":"fzf","update":["update"]}`,
		`{"kind":"skills","into":".claude/skills","update":["update"]}`,
	} {
		_, probs := Decode([]byte(`{"name":"x","contributes":[` + entry + `]}`))
		joined := strings.Join(probs, "\n")
		if !strings.Contains(joined, `does not take "update"`) {
			t.Errorf("%s should be refused by name, got: %v", entry, probs)
		}
	}
}

// TestUpdateVerbRefusesAnEmptyWord: the list reaches the vendor as argv, so an empty
// element is a zero-length argument the vendor reads as a malformed subcommand — from a
// manifest that otherwise looked fine.
func TestUpdateVerbRefusesAnEmptyWord(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"claude","via":"installer","url":"https://x/i.sh",
	   "update":["install","  "]}]}`))
	joined := strings.Join(probs, "\n")
	if !strings.Contains(joined, "update[1]") || !strings.Contains(joined, "empty word") {
		t.Errorf("an empty verb word should be refused, naming its index, got: %v", probs)
	}
}
