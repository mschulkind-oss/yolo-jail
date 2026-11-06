package check

import "github.com/mschulkind-oss/yolo-jail/internal/packload"

// sectionPackAudiences is FS-D6's row in `yolo check` (docs/design/pack-files-for-agent-state.md
// §4.2 and §10): content a selected pack addresses with `agents` to an agent no selected pack
// provides is SKIPPED by the launch and by `yolo host apply`, which run regardless, so here it is
// a [SKIP] row — counted apart from passes, never a failure. Each row names the pack, the kind
// and the agent as written, so a misspelling shows here; the note beneath it is the `yolo pack
// add` that makes the content apply, present only when a pack yolo ships provides the agent.
//
// Over the selection sectionPacks resolved (o.selectedPacks), the set the launch stages, and in
// the launch's own words (packload.AgentAudienceSkips, which run's discloseAudienceSkips prints).
// Prints nothing when sectionPacks never resolved a selection, or when no selected pack
// addresses an audience at all: there is nothing to report on, and the Packs section above
// already says what was selected.
func (o *Options) sectionPackAudiences(r *reporter) {
	if !o.selectedPacksKnown || !addressesAnAudience(o.selectedPacks) {
		return
	}
	r.sectionHeader("Pack audiences")
	defer r.blank()
	skips := packload.AgentAudienceSkips(o.selectedPacks, packload.Embedded())
	if len(skips) == 0 {
		r.ok("every `agents` selector names an agent a selected pack provides")
		return
	}
	for _, s := range skips {
		r.skip(s.Line(), s.NextStep())
	}
}

// addressesAnAudience reports whether any pack in packs names an `agents` selector.
func addressesAnAudience(packs []*packload.Pack) bool {
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if len(c.Agents) > 0 {
				return true
			}
		}
	}
	return false
}
