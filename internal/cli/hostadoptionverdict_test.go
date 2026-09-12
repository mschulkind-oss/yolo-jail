package cli

// hostadoptionverdict_test.go is OQ-CO7's D3: the VERDICT half of the zero-bytes adoption
// (docs/design/config-ownership-and-promotion.md §6.3.3, docs/design/report-tiers.md §4.3).
//
// hostadoptionarchivetier_test.go pinned the HEADING — an adopting render is a tier-3
// destination whether or not it reproduced the bytes, so the archive disclosure has a surface
// line of its own above it. That left the sentence the run ENDS on still saying the opposite,
// because a tier decides how a destination renders and only a count reaches the verdict.
// Measured on the built binary, 2026-09-12:
//
//	  claude/settings      rendered  …/.claude/settings.json
//	    archived your file as yolo found it: …/archive/config/claude-settings/settings.json
//	Nothing to apply — this home is up to date.
//
// A one-way, once-ever event, disclosed in the one line OQ-RO3 forbids hiding, under a verdict
// saying nothing happened. §4.3's rule is that every tier-3 class is represented in the verdict
// block; adoption was the class contributing nothing to it.

import (
	"os"
	"strings"
	"testing"
)

// applyVerdictLine is the sentence printHostApplyVerdict ends the report with, found
// STRUCTURALLY rather than by matching any of its wordings: it is the last unindented line
// above the posture footer, with the counts — indented — in between. Matching on the text
// would make this helper agree with whichever verdict the code happens to print, which is the
// one thing a test of the verdict must not do.
func applyVerdictLine(t *testing.T, report string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.HasPrefix(lines[i], "assert —") && !strings.HasPrefix(lines[i], "dry run —") {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if !strings.HasPrefix(lines[j], " ") {
				return lines[j]
			}
		}
	}
	t.Fatalf("the report has no posture footer, so it printed no verdict block at all:\n%s", report)
	return ""
}

// A RUN THAT ADOPTED A FILE DOES NOT CLOSE ON "NOTHING TO APPLY".
//
// The fixture is ownHomeSeeded's two-phase one for its reason (see the tier test): the bytes
// that make an adoption a no-op are whatever THIS pack's owned render emits, so phase 1
// measures them and phase 2 hands them to a fresh home. What is asserted here is only the
// verdict — the archive itself, and the surface line above it, are the other two tests'.
func TestHostApplyVerdictNamesAnAdoptionThatChangedNoBytes(t *testing.T) {
	_, primed := ownHomeSeeded(t, `{"apiKeyHelper":"/usr/local/bin/acme-key.sh"}`)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("priming apply rc=%d\n%s", rc, report)
	}
	canonical, err := os.ReadFile(primed)
	if err != nil {
		t.Fatal(err)
	}

	_, settings := ownHomeSeeded(t, string(canonical))
	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("`own` apply rc=%d\n%s", rc, report)
	}
	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	// THE FIXTURE GUARD, and it is what makes the assertions below about D3 rather than about
	// an ordinary changed file: the destination has to report WouldChange=false, or the
	// verdict names it through the `config file` count and this test passes for the wrong
	// reason.
	if string(after) != string(canonical) {
		t.Fatalf("the render moved the file, so this is not the zero-bytes case any more and "+
			"the verdict would name it as an ordinary change.\nbefore:\n%s\nafter:\n%s",
			canonical, after)
	}

	verdict := applyVerdictLine(t, report)
	if strings.Contains(verdict, "Nothing to apply") || strings.Contains(verdict, "Nothing to do") {
		t.Errorf("the run adopted a file and closed on:\n\t%s\n\nfull report:\n%s\n\n"+
			"An adoption is a one-way, once-ever event whose whole value is that the user "+
			"knows it fired, and the archive line naming the copy is unsuppressible (OQ-RO3). "+
			"A verdict saying nothing happened over a report line saying a file was archived "+
			"is the two-surfaces-disagree defect report-tiers.md exists to prevent: "+
			"hostApplyOutcome's nothing-to-do case must consult Adoptions(), not Changes() "+
			"alone — the canonical adoption reproduces the bytes and so leaves Changed empty.",
			verdict, report)
	}
	if !strings.Contains(verdict, "adopted") {
		t.Errorf("the verdict does not name the adoption:\n\t%s\n\nfull report:\n%s\n\n"+
			"§4.3: every tier-3 class is represented in the verdict block, a loss by its "+
			"count. Without the class in hostApplyWorkItems the outcome is `applied` over an "+
			"empty work list, which states the run did something and then declines to say "+
			"what.", verdict, report)
	}
}
