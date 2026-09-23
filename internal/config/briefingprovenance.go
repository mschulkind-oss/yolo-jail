package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// briefingprovenance.go is the `briefing_provenance` opt-in: whether a composed briefing labels
// each pack's prose with a `<!-- from pack: NAME -->` header.
//
// # Why the default is OFF
//
// The header was added so an agent meeting a surprising rule could find out where it came from.
// In practice it does the opposite of what pack prose is for. A pack's briefing is the user's own
// rules for EVERY repository, and labelling it "from pack: X" invites the agent to read it as
// someone else's, scoped to something other than the repository in front of it — and to discount
// it. It also does nothing at all for Claude: Claude Code strips HTML comments out of CLAUDE.md
// before the model sees the file (measured 2026-09-22, 2.1.280), so the one agent the attribution
// was most wanted for never received it.
//
// So a composed briefing is plain concatenation by default, and the header is a debugging aid a
// user turns on.
//
// # Scope
//
// Read from the EFFECTIVE config at the jail notch (workspace scope may set it there — the header
// changes nothing but the text of this jail's own briefing), and from the USER config at the host
// notch, like every other key that shapes a render into the real home. Set it in the user config
// to change both.

// briefingProvenanceKey is the top-level opt-in.
const briefingProvenanceKey = "briefing_provenance"

// BriefingProvenance reports whether cfg turns the per-pack provenance header on. Absent, null,
// or anything that is not the boolean true means OFF — the validator reports a wrong shape, and a
// reader that guessed would be a second place the shape rule lives.
func BriefingProvenance(cfg *jsonx.OrderedMap) bool {
	if cfg == nil {
		return false
	}
	v, _ := cfg.Get(briefingProvenanceKey)
	on, _ := v.(bool)
	return on
}

// BriefingProvenanceUser is BriefingProvenance over the user-scope config — the host notch's
// reading. An unreadable user config is an empty one, so the default holds.
func BriefingProvenanceUser() bool {
	return BriefingProvenance(UserScopeConfigOrEmpty())
}

func validateBriefingProvenance(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get(briefingProvenanceKey)
	if !present || v == nil {
		return
	}
	if _, ok := v.(bool); !ok {
		add(errs, "config."+briefingProvenanceKey+": expected a boolean (got "+pyReprValue(v)+")")
	}
}
