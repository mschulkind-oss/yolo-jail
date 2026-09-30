package loopholes

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// supersederefusal.go is the ENFORCING half of the supersession match
// (docs/design/reference-mismatch-diagnostics.md §7 steps 4 and 5): what the launch refuses
// on, and what `yolo check` grades [FAIL] as the prediction of that refusal (RM-D1). The
// sentence each finding carries is unmatchedSupersessions'; this file adds only what an
// enforcing surface says under it, so the two surfaces read alike by construction.

// SupersessionProblemsFor is Set.SupersessionProblems over one explicit pack set: its
// loophole modules and its `supersedes` claims, such as the packs a launch has just staged.
//
// It reads the construction `yolo check` reads (validateSetOf, which ValidateSet calls over
// the recorded packs), so the launch's refusal and the check's [FAIL] row come from one gate
// with two inputs, never from two gates. And it prints nothing: Discover is the only place
// that warns a claim, and a launch that is about to refuse must not first print the same
// sentence as a bare warning line.
//
// The served set is the pack modules' alone, which is complete: every loophole yolo ships is
// a pack's, and a config-declared loophole carries no `serves` (synthesizeConfigLoopholes;
// TestConfigDeclaredLoopholesServeNothing pins it).
func SupersessionProblemsFor(mods []PackModule, claims []PackSupersession) []string {
	_, set := validateSetOf(mods, claims)
	return set.SupersessionProblems()
}

// UnmatchedSupersessionRemedy is the fix an enforcing surface prints under the sentences.
// OQ-TP6's choices, less the approval its prompt no longer offers: fix the pack, or remove it.
const UnmatchedSupersessionRemedy = "Fix the pack: correct the capability in its `supersedes`, " +
	"or delete that claim; or remove the pack from `packs`."

// UnmatchedSupersessionFix is what an enforcing surface prints under the sentences: the
// remedy, and the skew clause when there is skew to name (RM-D2).
//
// CALL IT ONLY ONCE A CLAIM HAS FAILED TO MATCH. It runs git (version.SourceSkew), which is
// cheap and still not free, and the happy path must not pay for a sentence it never prints.
//
// The clause reads version.SourceSkew DIRECTLY, not the launch's source-skew gate. That gate
// refuses a container launch before staging, so a container launch reaches this with skew
// only under YOLO_ALLOW_SOURCE_SKEW=1; the macos-user backend runs no such gate at all, and
// `yolo check` runs none either. The cause is the same everywhere: the packs a host yolo
// ships are the ones compiled into it, so a yolo older than its source serves the older
// packs' capabilities. When SourceSkew cannot prove the skew there is no clause, because the
// clause would be a guess.
func UnmatchedSupersessionFix(repoRoot string) []string {
	lines := []string{UnmatchedSupersessionRemedy}
	if clause := supersessionSkewClause(version.SourceSkew(repoRoot), repoRoot); clause != "" {
		lines = append(lines, clause)
	}
	return lines
}

// supersessionSkewClause renders RM-D2's clause for a proven skew, or "" for none: both
// commits, the tree they were read from, and `just install`. For this cause, §4.6's
// "run `just load`" is `just install`, since the image no longer carries yolo.
func supersessionSkewClause(skew *version.Skew, repoRoot string) string {
	if skew == nil {
		return ""
	}
	return fmt.Sprintf("This yolo is older than the source tree it builds from (yolo: %s, "+
		"tree: %s, at %s): the packs it ships are its own commit's, so a capability only a "+
		"newer pack serves matches nothing here. Run `(cd %s && just install)`, then retry.",
		shortCommit(skew.BinaryCommit), shortCommit(skew.TreeCommit), repoRoot, repoRoot)
}

// shortCommit abbreviates a full sha for the clause, as the source-skew refusal does: a
// fixed 8, stable to assert on, with the tree named beside it.
func shortCommit(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
