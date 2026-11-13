package packdecl

// warnsabovechars.go validates the `warns_above_chars` field of a briefing destination. The
// field's semantics are in Contribution.WarnsAboveChars (docs/design/briefing-diet.md BD-D9).

import "fmt"

// warnsAboveCharsProblems validates one contribution's `warns_above_chars`. Each refusal names
// the fix, since a pack author reads it in `yolo pack lint` with nothing else to go on.
func warnsAboveCharsProblems(label string, c Contribution) []string {
	if c.WarnsAboveChars == nil {
		return nil
	}
	switch {
	case c.Kind != KindBriefing:
		return []string{fmt.Sprintf(
			"%s: kind %q does not take \"warns_above_chars\" — it is the length at which an agent "+
				"warns about its BRIEFING file, so only a \"briefing\" destination has one; remove it here",
			label, c.Kind)}
	case c.Into == "":
		return []string{fmt.Sprintf(
			"%s: \"warns_above_chars\" belongs on a briefing DESTINATION (an entry with \"into\") — it "+
				"states when the agent reading THAT file warns, which content a pack ships has no file "+
				"to be true of; move it to the destination's entry", label)}
	case *c.WarnsAboveChars <= 0:
		return []string{fmt.Sprintf(
			"%s: \"warns_above_chars\" is %d; write a positive character count, the length the agent "+
				"was measured to warn above, or remove the field if no limit was measured",
			label, *c.WarnsAboveChars)}
	}
	return nil
}
