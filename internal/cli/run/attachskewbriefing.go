package run

// attachskewbriefing.go puts an ACKNOWLEDGED attach's skew into the briefing the attach refreshes
// (docs/design/attach-skew-and-contract-guardrails.md, OQ-SK4 decided as SK-D15).
//
// Under AllowAttachSkewEnv an attach goes ahead into a jail that cannot take what the entry
// delivers, withholds the entry's whole provider/profile channel, and says so on stderr
// (settleAttachSkew). That account scrolls away the moment the agent's screen takes the terminal,
// and the agent never sees it at all. The briefing is what the session this attach starts reads,
// and refreshJailBriefings already rewrites it on every attach, host-side and inode-preserving,
// into the running jail's staging — so a jail whose own binaries predate this change shows the
// section too, which is why the channel is the briefing and not the footer (an old jail's footer
// runs its own frozen binary). The section names the version the jail was launched with, the
// contract tags it lacks and what those withheld, and every other difference; its renderer is
// jailcontent's attachSkewSection.
//
// Each acknowledged attach writes it, and every other entry's refresh writes none, so it lasts
// until the next entry into the jail. A jail whose briefing this attach cannot reach gets only the
// stderr account, and the account says so (noteAttachSkewBriefing).

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
)

// attachSkewBriefing is the briefing's account of an acknowledged skew, or nil for none.
func (o *Options) attachSkewBriefing(s *attachSkew, baked, rt, cname string) *jailcontent.AttachSkew {
	if s == nil {
		return nil
	}
	host := o.yoloVersion("")
	if host == "unknown" {
		host = ""
	}
	b := &jailcontent.AttachSkew{
		JailVersion:     baked,
		LauncherVersion: host,
		Jail:            s.jail,
		Differences:     s.differences,
		// The restart the attach's own messages name, in the briefing's markup: the agent cannot
		// run it, since it is a host command, so it is named for the agent to pass on.
		Remedy: strings.ReplaceAll(stopRemedy(rt, cname), "'", "`") + " on the host",
	}
	for _, n := range s.missing {
		b.MissingTags = append(b.MissingTags, jailcontent.AttachSkewTag{Tag: n.tag, Lacks: n.lacks})
		b.Withheld = append(b.Withheld, n.withheld...)
	}
	return b
}

// noteAttachSkewBriefing is the acknowledgment's last stderr line, printed once the briefing is
// refreshed: that the briefing names the difference too, or why no briefing does and the stderr
// account is the only record (SK-D15).
func (o *Options) noteAttachSkewBriefing(rt string, view attachPackView) {
	err := o.pr(o.Stderr)
	switch {
	case view.unreadable:
		err.print("[yellow]No briefing records this difference: the jail's packs could not be read, " +
			"so its briefing is not refreshed, and the warning above is its only record.[/yellow]")
	case len(briefingDestinations(view.staged.packs)) == 0:
		err.print("[yellow]No briefing records this difference: the jail's packs declare no " +
			"briefing, so the warning above is its only record.[/yellow]")
	case rt == "container": // parity: Warned — this line: an Apple Container jail's briefing is the copy its launch made (assembleRunCmd's acMaterialize), which a re-entry does not reach
		err.print("[yellow]No briefing records this difference: an Apple Container jail keeps the " +
			"briefing its launch copied in, so the warning above is its only record.[/yellow]")
	default:
		err.print("[dim]The jail's briefing names this difference as well, for the agent this attach " +
			"starts.[/dim]")
	}
}
