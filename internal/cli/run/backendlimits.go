package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// backendlimits.go answers, for the AGENT, the question the launch warnings answer
// for the human: what does this backend not do for me?
//
// WHY THE AGENT NEEDS ITS OWN COPY. Every fact below is printed at launch — to
// stderr, where the human reads it and the agent never does. The agent then reasons
// from a jail it believes is like every other one: that its config file reflects the
// user's, that its home is its own, that a skill it edits stays edited. Each of those
// is false here, silently, and none has a moment of use to attach the correction to
// (docs/design/information-at-the-point-of-need.md: no moment → the briefing).
//
// NOT EVERYTHING THE HUMAN IS TOLD BELONGS HERE, and the filter is that same
// principle. `resources` and `cache_relocations` are read-and-ignored on this backend
// and warned about, and they condition nothing an agent does — it never asked for a
// memory cap. `mcp_presets` are absent, and the absence shows up as a server that is
// simply not in its config. Those stay human-only.
//
// ONE SOURCE, TWO RENDERINGS. The facts come from the same predicates the note*
// printers use, so a limit cannot be reported to one audience and not the other. The
// PROSE differs on purpose — the human gets an explanation at launch, the agent gets
// a standing constraint — but the conditions do not.

// backendLimits returns the standing constraints of `rt` for the agent's briefing,
// or nil when the backend imposes none (every container backend today).
func backendLimits(rt string, packs []*packload.Pack, cfg *jsonx.OrderedMap) []string {
	if rt != "macos-user" {
		return nil
	}
	var out []string

	// The home is machine-wide. This is the one that conditions the most: an agent
	// that believes its home is its own will write state there expecting it to be
	// private to this project, and it is visible to every workspace on the machine.
	if dirs := packload.WritableDirs(packs); len(dirs) > 0 {
		out = append(out, "Your home is SHARED by every workspace on this machine, not "+
			"scoped to this project — "+strings.Join(dirs, ", ")+" are the same directories "+
			"another workspace's session reads and writes. Treat anything you put there as "+
			"visible outside this project, and expect to see history that is not yours.")
	}

	// Agent config surfaces rendered from DEFAULTS rather than the user's bytes. An
	// agent reading its own settings.json will otherwise take it for the human's
	// preferences and act on them.
	var ungranted []string
	for _, p := range packs {
		if p == nil {
			continue
		}
		granted, _ := p.HonoredHostFiles()
		for _, hf := range granted {
			ungranted = append(ungranted, "~/"+hf.From)
		}
	}
	if len(ungranted) > 0 {
		out = append(out, "Your agent config files were rendered from DEFAULTS, not from the "+
			"human's own — "+strings.Join(ungranted, ", ")+" did not cross into this "+
			"environment. Do not read them as a statement of their preferences, and do not "+
			"reason from settings you find there as though they chose them.")
	}

	// Content is a writable copy rather than a read-only mount.
	if len(packs) > 0 {
		out = append(out, "Your skills and this briefing are a writable COPY, not the "+
			"read-only mount every other backend gives. You can edit them; the next launch "+
			"overwrites them without warning. Edit the pack they came from instead.")
	}

	// No loophole host services, so the jail-side clients for them are inert.
	if backendInertReason(rt) != "" {
		out = append(out, "No loophole host services are running, so their in-jail clients "+
			"(`yolo-ps`, `yolo-journalctl`, `yolo-cglimit`) have nothing to talk to here.")
	}
	return out
}
