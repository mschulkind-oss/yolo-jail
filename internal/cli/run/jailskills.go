package run

// jailskills.go is the run pipeline's half of OQ-NC11 (docs/plans/notch-convergence.md#OQ-NC11,
// ruled 2026-09-28 by parity): it fills the jail's skills sources with what the host's layer
// writer needs to deliver them as the host does, and runs the host's collision refusal as a
// launch pre-flight.

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
)

// jailSkillSources converts one pack's resolved skills sources into the jail's records, each
// carrying the pack's tier, description and source mapping from hostskills.PackLayer — the one
// constructor the host composition reads a pack through — and the wrapped plugins inside it.
//
// ONE CONVERSION FOR BOTH READERS: the launch's own staging (packSkillSourceDirs) and an attach
// adopting the running jail's packs (adoptPackRecords). Two copies of this conversion would be
// two answers to what a pack's skills are called, which is the divergence the ruling removes.
func jailSkillSources(p *packload.Pack, sources []packload.SkillsSource) []jailcontent.PackSkillSource {
	layer := hostskills.PackLayer(p)
	out := make([]jailcontent.PackSkillSource, 0, len(sources))
	// A plugin inside NO source is the wrap-in-place shape, the pack root itself
	// (pluginpack.DiscoverIn): it rides every source of the pack, as the host carries a pack's
	// plugins on every one of its layers. jailcontent.SkillPlan delivers each plugin once per
	// destination.
	var rooted []*pluginpack.Plugin
	for _, pl := range layer.Plugins {
		inSource := false
		for _, src := range sources {
			inSource = inSource || pluginpack.Contains(src.Dir, pl.Dir)
		}
		if !inSource {
			rooted = append(rooted, pl)
		}
	}
	for _, src := range sources {
		// A plugin inside a source belongs to it, so an addressed source's plugin reaches only
		// that source's audience — the same rule its skills follow.
		plugins := append([]*pluginpack.Plugin(nil), rooted...)
		for _, pl := range layer.Plugins {
			if pluginpack.Contains(src.Dir, pl.Dir) {
				plugins = append(plugins, pl)
			}
		}
		out = append(out, jailcontent.PackSkillSource{Dir: src.Dir, Agents: src.Agents, Pack: p.Name,
			Tier: layer.Tier, Description: layer.Description, Plugins: plugins, SourceOf: layer.SourceOf})
	}
	return out
}

// checkSkillCollisions is the launch pre-flight for a skill NAME two packs ship to one
// destination (S1, and OQ-NC11 for the jail): the host's refusal, over the jail's own plan — its
// destinations, its fan-out, every tier — so the message and its remedies are the host's.
//
// It runs where the pack set becomes complete, beside the agent-name pre-flight, which is before
// any container exists and on an attach too. It is not an A12 boot failure inside a running jail:
// the user re-runs a command, as at the host, rather than losing a session.
func checkSkillCollisions(sources []jailcontent.PackSkillSource, packs []*packload.Pack) error {
	if err := jailcontent.SkillCollisionError(sources, packSkillTargets(packs)); err != nil {
		return fmt.Errorf("packs: %w", err)
	}
	return nil
}
