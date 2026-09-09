package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
)

// TestEveryConfigLsModeIsExplainedByABuiltinSkill pins the MODE column's vocabulary
// against the prose that teaches an agent what to do about it.
//
// `configuring-the-jail` is staged into EVERY jail and its "Some files in your home
// are GENERATED" section is the only place the MODE column is explained. It listed
// three of the four values — `capture`, `copy`, `unrendered` — and omitted `rmw`,
// which is the mode `~/.claude.json` is in: an agent reading `rmw` in
// `yolo config ls` had no guidance at all for the most consequential file claude
// owns, and the two adjacent values say opposite things about whether an edit
// survives, so guessing lands on one of them and is wrong either way.
//
// IT LIVES HERE, not beside the other two built-in-skill guards in
// internal/entrypoint, for the reason those two state about themselves: a guard goes
// where its AUTHORITY is. Theirs are `retiredGeneratedDirs` and `BootPath`; this
// one's is `surfaceMode`, the single function that decides what the column prints.
// internal/entrypoint cannot import internal/cli (the dependency only ever goes the
// other way), and asserting against a copy of the mapping would be asserting against
// a copy.
func TestEveryConfigLsModeIsExplainedByABuiltinSkill(t *testing.T) {
	// Every mode a surface can resolve to, put through the same display mapping
	// `yolo config ls` uses. The empty string is ModeStateful's spelling on a
	// surface that declares no mode, which is the common case.
	var displayed []string
	for _, m := range []string{
		"", manifest.ModeStateful, manifest.ModeComputed,
		manifest.ModeRMW, manifest.ModeUnrendered,
	} {
		got := surfaceMode(manifest.Surface{Mode: m})
		if !contains(displayed, got) {
			displayed = append(displayed, got)
		}
	}
	if len(displayed) < 4 {
		t.Fatalf("surfaceMode collapsed the mode set to %v — this test assumes the "+
			"column has a distinct name per mechanism", displayed)
	}

	skill, err := builtinskills.FS.ReadFile(
		filepath.Join("configuring-the-jail", "SKILL.md"))
	if err != nil {
		// FAILS rather than skips: the skill going missing is a bigger version of
		// the defect this guards, not a reason to stop checking.
		t.Fatalf("the configuring-the-jail skill is unreadable, so nothing in any "+
			"jail explains the MODE column: %v", err)
	}
	body := string(skill)
	for _, mode := range displayed {
		// The bulleted definition, not a passing mention: that is the form the
		// section uses and the form a reader looks for.
		if !strings.Contains(body, "- **"+mode+"** —") {
			t.Errorf("`yolo config ls` can print MODE %q and the configuring-the-jail "+
				"skill does not define it. Every jail stages that skill, and its MODE "+
				"list is the only place an agent learns whether a hand edit survives — "+
				"add a `- **%s** — …` bullet beside the others.", mode, mode)
		}
	}
}

// contains is a local two-liner: this file must not depend on a helper elsewhere in
// the package moving or changing meaning.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
