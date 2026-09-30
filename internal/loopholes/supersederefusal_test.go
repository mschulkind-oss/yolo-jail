package loopholes

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// TestSupersessionProblemsForReadsTheGivenPackSetAndPrintsNothing is the launch's gate
// (docs/design/reference-mismatch-diagnostics.md §7 step 4) at its callee: the problems of
// the pack set it is HANDED, not of whatever the process recorded, and not one line on the
// warning channel. The launch runs it over the packs it has just staged, before recording
// them, and refuses on the answer; a gate that read the records would judge the previous
// launch's packs, and one that warned would print the sentence twice.
func TestSupersessionProblemsForReadsTheGivenPackSetAndPrintsNothing(t *testing.T) {
	unsetJail(t)
	t.Setenv("HOME", t.TempDir())
	root := modsDir(t)
	servingLoophole(t, root, "broker-like", []string{"claude-oauth-refresh"})
	warnings := captureWarnings(t)
	// The process records name NOTHING: a gate reading them would answer "no problems".
	restoreMods, restoreClaims := SnapshotPackModules(), SnapshotPackSupersessions()
	t.Cleanup(func() { restoreMods(); restoreClaims() })
	SetPackModules(nil)
	SetPackSupersessions(nil)

	typo := PackSupersession{Pack: "claude-bedrock", Capability: "claude-oauth-refersh", Because: "Bedrock"}
	probs := SupersessionProblemsFor(moduleDirsUnder(root), []PackSupersession{typo, bedrock()})

	if len(probs) != 1 {
		t.Fatalf("SupersessionProblemsFor = %v, want the one unmatched claim", probs)
	}
	for _, want := range []string{"'claude-bedrock'", "'claude-oauth-refersh'", "did you mean", "keeps running"} {
		if !strings.Contains(probs[0], want) {
			t.Errorf("the gate's sentence lost %q, which is Discover's own sentence (§4.4: the "+
				"same sentence, as a refusal): %q", want, probs[0])
		}
	}
	if len(*warnings) != 0 {
		t.Errorf("the gate printed %q; a launch about to refuse must not first print the same "+
			"finding as a bare warning", *warnings)
	}
	// And it agrees with Discover about the same inputs: one gate, never a copy of it.
	if got := discoverWith(root, []PackSupersession{typo, bedrock()}).SupersessionProblems(); len(got) != 1 || got[0] != probs[0] {
		t.Errorf("the launch's gate and discovery disagree over one pack set:\n gate: %q\n discovery: %q", probs, got)
	}
}

// TestSupersessionProblemsForIsEmptyForAMatchedClaim: the gate must stay silent for a claim
// that did what its author meant, or it would refuse every launch that supersedes correctly.
func TestSupersessionProblemsForIsEmptyForAMatchedClaim(t *testing.T) {
	unsetJail(t)
	t.Setenv("HOME", t.TempDir())
	root := modsDir(t)
	servingLoophole(t, root, "broker-like", []string{"claude-oauth-refresh"})
	captureWarnings(t)

	if probs := SupersessionProblemsFor(moduleDirsUnder(root), []PackSupersession{bedrock()}); len(probs) != 0 {
		t.Errorf("a matched claim was reported: %v", probs)
	}
	if probs := SupersessionProblemsFor(moduleDirsUnder(root), nil); len(probs) != 0 {
		t.Errorf("no claims at all were reported: %v", probs)
	}
}

// TestConfigDeclaredLoopholesServeNothing pins the premise SupersessionProblemsFor rests on:
// the launch's gate reads only the pack modules, which is the complete served set only while a
// loophole declared in the `loopholes` config block cannot declare `serves`. If that changes,
// the gate must read the config block too, or it refuses a claim such a loophole matches.
func TestConfigDeclaredLoopholesServeNothing(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	spec := jsonx.NewOrderedMap()
	spec.Set("description", "a config-declared daemon")
	spec.Set("command", []any{"/bin/true"})
	spec.Set("serves", []any{"acme-oauth-refresh"})
	cfg.Set("acme-inline", spec)

	got := synthesizeConfigLoopholes(cfg)
	if len(got) != 1 {
		t.Fatalf("synthesizeConfigLoopholes = %d records, want the one entry", len(got))
	}
	if len(got[0].Serves) != 0 {
		t.Errorf("a config-declared loophole now serves %v, so the supersession gate the launch "+
			"refuses on (SupersessionProblemsFor, pack modules only) no longer sees the whole "+
			"served set; give it the config block", got[0].Serves)
	}
}

// TestUnmatchedSupersessionFixNamesOnlyProvenSkew is RM-D2 at its callee: the remedy always,
// and the skew clause only for a skew version.SourceSkew proved — both commits, the tree and
// `just install`. With no repo root there is nothing to compare, so no clause: a clause
// nothing proved would be a guess.
func TestUnmatchedSupersessionFixNamesOnlyProvenSkew(t *testing.T) {
	if got := UnmatchedSupersessionFix(""); len(got) != 1 || got[0] != UnmatchedSupersessionRemedy {
		t.Errorf("UnmatchedSupersessionFix(\"\") = %q, want the remedy alone", got)
	}
	skew := &version.Skew{
		BinaryCommit: "1111111111111111111111111111111111111111",
		TreeCommit:   "2222222222222222222222222222222222222222",
		Changed:      []string{"packs"},
	}
	clause := supersessionSkewClause(skew, "/src/yolo-jail")
	for _, want := range []string{"older than the source tree", "11111111", "22222222",
		"/src/yolo-jail", "just install"} {
		if !strings.Contains(clause, want) {
			t.Errorf("skew clause missing %q: %q", want, clause)
		}
	}
	if got := supersessionSkewClause(nil, "/src/yolo-jail"); got != "" {
		t.Errorf("no skew rendered a clause: %q", got)
	}
}
