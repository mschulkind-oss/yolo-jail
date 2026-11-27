package run

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// noteToolSkills is the HOST half of the mise tool-skills disclosure (docs/reference/mise-tool-skills.md
// section 6.2): one line per skill the layer did not deliver, each naming its next step. It runs
// from refreshJailBriefings, so a launch and every attach say it, to stderr as noteWorkspaceSkills
// does (an attach's stdout belongs to its "Attaching" line and the command after).
//
// A DELIVERED SKILL HAS NO LINE, by the design's first row: "Delivered, nothing skipped: none: the
// skills are simply there". A nil report (no layer) prints nothing, and so does an empty one.
//
// Every string interpolated is the layer's sanitized text (control characters already stripped by
// toolskills.Sanitize) passed through richtext.Escape, because a tool and its skills are named by
// a third party and a bracket must not forge a style tag in a disclosure.
func (o *Options) noteToolSkills(r *jailcontent.ToolSkillsReport) {
	if r == nil || (len(r.Shadowed) == 0 && len(r.Skipped) == 0) {
		return
	}
	out := o.pr(o.Stderr)
	for _, s := range r.Shadowed {
		out.print(fmt.Sprintf("[yellow]yolo: mise skill \"%s\" (%s) not delivered: %s already provides a skill by "+
			"that name and wins. Rename that skill to use the tool's.[/yellow]",
			richtext.Escape(s.Name), richtext.Escape(s.Tool), richtext.Escape(s.By)))
	}
	for _, s := range r.Skipped {
		if s.Winner != "" {
			out.print(fmt.Sprintf("[yellow]yolo: mise skill \"%s\" from %s not delivered: %s comes first in your "+
				"install.mise and wins. Reorder install.mise (`yolo config unset install.mise.<tool> --user`, then "+
				"`yolo config set` it again) to change that.[/yellow]",
				richtext.Escape(s.Name), richtext.Escape(s.Tool), richtext.Escape(s.Winner)))
			continue
		}
		out.print(fmt.Sprintf("[yellow]yolo: mise skill \"%s\" (%s) not delivered: %s. Fix that skill in the tool, "+
			"or remove the tool from your install.mise, then relaunch yolo.[/yellow]",
			richtext.Escape(s.Name), richtext.Escape(s.Tool), richtext.Escape(s.Reason)))
	}
}

// noteToolSkillsOff is the line for the one launch-side case where the user named tools and the
// layer is off anyway: the workspace's snapshot token could not be minted or stored. Without it the
// jail is handed no allowlist at all and the tool skills silently never arrive.
func (o *Options) noteToolSkillsOff(why string) {
	o.pr(o.Stderr).print(fmt.Sprintf("[yellow]yolo: mise skills: tool skills are off for this launch: the workspace's "+
		"snapshot token could not be stored (%s). Make yolo's own storage directory writable (`yolo check` "+
		"names it), then relaunch yolo.[/yellow]", richtext.Escape(why)))
}
