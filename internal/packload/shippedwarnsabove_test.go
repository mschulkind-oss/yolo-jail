package packload

// shippedwarnsabove_test.go pins which shipped packs declare `warns_above_chars`
// (docs/reference/agent-briefings.md BD-D9, BD-D10): the claude pack declares Claude Code's measured
// 40,000 on its ~/.claude/CLAUDE.md destination, and no other shipped pack declares any value,
// because no other agent's limit was measured.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestOnlyTheClaudePackDeclaresAMeasuredBriefingLimit(t *testing.T) {
	found := false
	for _, p := range materializedShippedPacks(t) {
		for _, c := range p.Decl.Contributions() {
			if c.WarnsAboveChars == nil {
				continue
			}
			if p.Name == "claude" && c.Kind == packdecl.KindBriefing && c.Into == ".claude/CLAUDE.md" {
				found = true
				if *c.WarnsAboveChars != 40000 {
					t.Errorf("the claude pack declares warns_above_chars = %d; Claude Code 2.1.323 was "+
						"measured to warn above 40,000 characters (docs/reference/agent-briefings.md#budgets-and-the-size-line)",
						*c.WarnsAboveChars)
				}
				continue
			}
			t.Errorf("shipped pack %s declares warns_above_chars = %d on %q; only a MEASURED limit is "+
				"declared (docs/reference/agent-briefings.md BD-D10). Record the measurement in that "+
				"document's table of what each agent does with a long file first, then allow it here", p.Name, *c.WarnsAboveChars, c.Into)
		}
	}
	if !found {
		t.Error("the claude pack's ~/.claude/CLAUDE.md destination declares no warns_above_chars; " +
			"add `warns_above_chars = 40000` to its [[briefing]] destination in packs/claude/pack.toml")
	}
}
