package run

// briefingsizeline.go is the launch's size line (docs/reference/agent-briefings.md BD-D9): the hook
// refreshJailBriefingsMode calls with each destination's FINAL composed file, just before it is
// written. A file longer than its agent's declared warns_above_chars gets one warning line,
// which jailcontent.BriefingSizeWarning words. The launch is never refused and the file is
// never trimmed; a launch has no quiet mode, so the line is always printed.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// noteBriefingSize prints the size line for destination d, whose composed file is content, and
// nothing when d declared no limit or the file is within it.
func (o *Options) noteBriefingSize(d briefingDest, content string, packs []jailcontent.PackBriefing) {
	line := jailcontent.BriefingSizeWarning(jailcontent.BriefingSize{
		Into: d.Into, Agent: d.Agent, Content: content, Limit: d.WarnsAbove, Packs: packs,
	})
	if line == "" {
		return
	}
	o.pr(o.Stderr).print("[yellow]Warning: " + richtext.Escape(line) + "[/yellow]")
}
