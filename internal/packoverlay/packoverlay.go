// Package packoverlay collects `config-overlay` contributions ACROSS packs and resolves
// each onto the surface identity it targets — the piece that was missing while the kind
// sat inert (docs/design/pack-config-collaboration.md §6 Option 2).
//
// CROSS-PACK BY CONSTRUCTION, and that is the one structural fact worth stating: an
// overlay in pack B targets a surface pack A owns, so collection cannot be per-pack.
// Both render paths iterate packs one at a time, so each would otherwise see only its
// own pack's overlays — which for the only case the kind exists to serve (B overlays A)
// is exactly zero of them. So the collection runs over the WHOLE loaded set first and
// hands the render a lookup keyed by surface identity.
//
// The owner check is the other half. Per ruling R2, an overlay whose target has no owner
// among the loaded packs is INERT AND REPORTED — it must not create the file (an overlay
// owning a surface by accident destroys the very distinction the kind draws) and must not
// fail the launch (a pack the user did not select is not an error). Deciding that needs
// the owner set, which is the same whole-set view, so ownership and collection are one
// pass.
//
// ITS OWN PACKAGE, not a file in packload, and the reason is a hard constraint rather
// than taste: this is the JOIN of the pack layer (packload) and the engine layer
// (agentcfg), and agentcfg's own test suite imports packload to pin the shipped packs'
// surfaces — so a packload → agentcfg edge is an import cycle in test. The join has to
// live above both.
package packoverlay

import (
	"fmt"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// OverlaySet is the resolved config-overlay picture for one set of loaded packs: which
// overlays land on which surface, which land nowhere, and what went wrong.
type OverlaySet struct {
	// byTarget maps a surface identity to the overlay layers folding onto it, in
	// pack-then-declaration order (later wins — the same "later pack wins" rule skills
	// and launch flags already use).
	byTarget map[manifest.SurfaceKey][]agentcfg.Overlay

	// Orphans are the overlays whose target surface has no owner in this pack set —
	// ruling R2's "no effect, reported by name". Ordered deterministically.
	Orphans []OrphanOverlay

	// Problems are malformed overlays (an unparseable target, a body that redeclares
	// the surface, an empty body). Loud like every other manifest problem: an overlay
	// the author wrote wrong must not read as an overlay that simply lost a conflict.
	Problems []string
}

// OrphanOverlay is one overlay with no owner: enough to report it by name, including
// which pack would have had to be selected for it to work.
type OrphanOverlay struct {
	// Pack is the pack that declared the overlay.
	Pack string
	// Target is the surface identity it names, "agent/name".
	Target string
	// Owner names the pack that DOES own this surface among the packs yolo ships but
	// that is not in this set — the actionable half of the report ("add the `claude`
	// pack"). Empty when no shipped pack owns it either, in which case the target is
	// either core's own surface (see CoreOwned) or a typo.
	Owner string

	// CoreOwned marks a target that is one of CORE's surfaces (mise/config) rather than
	// any pack's. Still inert, but for a different reason than "not selected", so the
	// report says so instead of implying the identity is misspelled.
	CoreOwned bool
}

// Reason renders the orphan as the one-line report ruling R2 specifies.
func (o OrphanOverlay) Reason() string {
	switch {
	case o.Owner != "":
		return fmt.Sprintf("no effect — %s has no owner (the `%s` pack is not selected)",
			o.Target, o.Owner)
	case o.CoreOwned:
		return fmt.Sprintf("no effect — %s is one of yolo's OWN surfaces, not a pack's; "+
			"config-overlay contributes to a surface a pack owns", o.Target)
	default:
		return fmt.Sprintf("no effect — %s has no owner among the selected packs "+
			"(no pack declares that surface — check the identity)", o.Target)
	}
}

// For returns the overlay layers folding onto one surface, or nil for none. nil is the
// overwhelmingly common answer (no shipped pack declares an overlay), and Compose treats
// an empty Overlays slice as a no-op, so a surface with no overlay composes byte-identically
// to before this existed.
func (s *OverlaySet) For(agent, name string) []agentcfg.Overlay {
	if s == nil {
		return nil
	}
	return s.byTarget[manifest.SurfaceKey{Agent: agent, Name: name}]
}

// Collect resolves every loaded pack's config-overlay contributions against the surfaces
// those same packs own.
//
// autonomy is the §4.2 policy bit of the render target's confinement profile
// (render.Target.Profile().AgentAutonomy): it selects which posture the OWNER's surfaces are
// decoded under, so the owner set matches the render the overlays will fold into. It only
// affects which surfaces exist to be found, never the overlay bodies — an overlay carries no
// posture.
//
// Still a bool rather than a render.Profile, and by now that is a deliberate boundary rather
// than a leftover: every caller derives the bit from a Target's Profile (packsurfaces.go from
// the boot Target, apply.go from render.Host, configdiff.go from render.ProfileFor over the
// notch it is describing), so the literals plan §6c step 1 set out to remove are gone — C3
// closed the last one. Taking a Profile here would import the confinement model into a package
// whose whole job is resolving overlays against owners — it needs ONE bit, and receiving one
// bit is what keeps this package unable to disagree with the notch that computed it.
//
// AND ITS EFFECT ON THIS FUNCTION'S OUTPUT IS ZERO, which is worth stating because it looks
// like a gap. The posture fold (packload.foldPostureManaged) merges keys into the Managed
// layer of surfaces already declared, IGNORES a patch naming no base surface, and never adds
// or removes an identity — it only deep-merges into one that is there — so both postures
// yield the same surface-identity set — and identities are all this function reads.
// (The ignored patch is now REPORTED, by the render paths that run the fold; that this
// package reads surfaces through the same SurfacesFor and discards the notes is fine, since
// it is not the notch whose user wrote the patch.) Inverting the argument at every caller
// therefore leaves the suite green. That survival is a
// property, not missing coverage, and autonomyinert_test.go pins it so the two stay
// distinguishable: if a posture ever gains the power to add or remove an identity, the
// parameter starts deciding which overlays find an owner, and that test fails at the moment it
// does. Where the bit IS consequential — p.SurfacesFor at the render — it is pinned in both
// directions by internal/entrypoint/bootautonomy_test.go.
//
// The profile fold has the same shape and gets the same answer: SurfacesFor is called with
// NO profile table, because a variant patch merges into the managed layer of a surface the
// pack already declares and ignores a patch naming none, so no table can change which
// identities exist here. This package does not resolve variants — it resolves owners — and
// if a profile patch ever could add or remove an identity, that is the property that breaks
// and the same test class catches it.
//
// profiles is the ACTIVE profile table the CALLER's render resolved — packload.ProfileTable's
// lowering of YOLO_USE_PROFILES in the jail, of the config's use_profiles at the host —
// keyed by CLI name, and it gates the `profile` MODIFIER (profiles-as-pack-variants.md §7):
// an overlay declaring a profile contributes only while that name is the one active for the
// surface's OWNING agent, which is the target identity's agent segment (an "agent/name"
// identity's agent half IS a CLI name, the namespace the table keys on). Taking the table
// as a parameter rather than re-deriving it is what keeps the gate from answering the
// "which variant is selected" question differently than the render folding beside it — the
// caller already resolved the table for its own variant folds.
//
// An inactive profile is a CLEAN SKIP — no error, no orphan report, no applied notice —
// because selection is the optionality (§7.1, the same rule that makes an unselected owner
// a skip rather than a refusal) and profile VALUES are free-form (parent OQ-3): a name
// nothing selected is inert, and reporting it would be a launch that second-guesses the
// user's `-p`. A profile-gated overlay whose target has no owner while the profile IS
// active is still an orphan — R2's report fires for the reason that actually stopped the
// contribution.
func Collect(packs []*packload.Pack, autonomy bool, profiles map[string]string) *OverlaySet {
	set := &OverlaySet{byTarget: map[manifest.SurfaceKey][]agentcfg.Overlay{}}

	// Pass 1: who owns what. A surface's owner is the pack whose `config` contribution
	// declares it. Surface problems are NOT collected here — the render path reports
	// them against the owning pack already, and duplicating them would double every
	// message on a broken manifest.
	owners := map[manifest.SurfaceKey]string{}
	for _, p := range packs {
		surfaces, _ := p.SurfacesFor(autonomy, nil)
		for _, s := range surfaces {
			owners[s.Key()] = p.Name
		}
	}

	// Pass 2: place each overlay, in pack order then declaration order, so the fold
	// order is the config's `packs` order — the same precedence a reader already expects
	// from every other multi-pack kind.
	for _, p := range packs {
		for _, ov := range p.Decl.ConfigOverlayContributions() {
			key, err := manifest.ParseSurfaceID(ov.Surface)
			if err != nil {
				set.Problems = append(set.Problems, "pack "+p.Name+": config-overlay: "+err.Error())
				continue
			}
			data, probs := manifest.DecodeOverlay(ov.Surface, ov.Config)
			if len(probs) > 0 {
				for _, prob := range probs {
					set.Problems = append(set.Problems, "pack "+p.Name+": "+prob)
				}
				continue
			}
			// The PROFILE GATE, after the body decode and before the owner check, and both
			// positions are load-bearing. A malformed body is the AUTHOR's mistake and is
			// reported whatever the launch selected — an overlay that renders only under
			// profile X is still wrong when X is off, or the author would never hear it.
			// The owner check comes AFTER the gate so a profile-inactive overlay is not
			// reported as an orphan: the reason it contributed nothing is the selection,
			// and R2's report exists to name the remedy ("select that pack"), which here
			// would be a remedy the user deliberately declined.
			if ov.Profile != "" && profiles[key.Agent] != ov.Profile {
				continue
			}
			if _, owned := owners[key]; !owned {
				// R2: inert and reported. Named by target, with the shipped owner when
				// there is one, so the fix ("select that pack") is in the message.
				_, coreOwned := agentcfg.BuiltinManifest().Lookup(key.Agent, key.Name)
				set.Orphans = append(set.Orphans, OrphanOverlay{
					Pack: p.Name, Target: ov.Surface,
					Owner: shippedOwnerOf(key, autonomy), CoreOwned: coreOwned,
				})
				continue
			}
			set.byTarget[key] = append(set.byTarget[key], agentcfg.Overlay{Pack: p.Name, Data: data})
		}
	}

	sort.SliceStable(set.Orphans, func(i, j int) bool {
		if set.Orphans[i].Target != set.Orphans[j].Target {
			return set.Orphans[i].Target < set.Orphans[j].Target
		}
		return set.Orphans[i].Pack < set.Orphans[j].Pack
	})
	return set
}

// shippedOwnerOf names the EMBEDDED pack that owns a surface identity, or "" when none
// does.
//
// It reads the not-selection-gated embedded set on purpose (see the Embedded() note in
// AGENTS.md), and that is what makes the R2 message actionable rather than merely
// honest: "claude/settings has no owner" tells a user their overlay did nothing;
// "…(the `claude` pack is not selected)" tells them what to do about it. The distinction
// only exists if the lookup can see a pack the user did NOT select.
//
// Best-effort by design: an unresolvable embedded set degrades to the no-owner-anywhere
// message, which is still correct, just less helpful.
//
// autonomy is the caller's, threaded rather than fixed, so no posture literal survives in this
// package (plan §6c step 1). It cannot change the ANSWER: a posture only patches the managed
// layer of surfaces the pack already declares (foldPostureManaged ignores a patch naming no
// base surface), so both postures yield the same surface-identity set — which is all this
// lookup reads.
func shippedOwnerOf(key manifest.SurfaceKey, autonomy bool) string {
	for _, p := range packload.Embedded() {
		// No profile table, for the reason Collect states: identities are all this reads,
		// and no variant fold can add or remove one.
		surfaces, _ := p.SurfacesFor(autonomy, nil)
		for _, s := range surfaces {
			if s.Key() == key {
				return p.Name
			}
		}
	}
	return ""
}

// AppliedOverlay is one surface that carries overlays, with the contributing packs in
// fold order — what a caller reports so an override is legible at the moment it applies
// (ruling R3) rather than only afterwards from a sidecar.
type AppliedOverlay struct {
	// Target is the surface identity, "agent/name".
	Target string
	// Agent is the identity's owner segment, so a caller can name the `yolo config diff
	// <agent>` that explains the keys without re-splitting the identity.
	Agent string
	// Packs are the contributing packs, lowest precedence first (later wins).
	Packs []string
}

// Applied lists the surfaces carrying overlays, sorted by identity for a deterministic
// report. Empty for the common case of no overlays anywhere.
func (s *OverlaySet) Applied() []AppliedOverlay {
	if s == nil {
		return nil
	}
	out := make([]AppliedOverlay, 0, len(s.byTarget))
	for key, overlays := range s.byTarget {
		packs := make([]string, 0, len(overlays))
		for _, ov := range overlays {
			packs = append(packs, ov.Pack)
		}
		out = append(out, AppliedOverlay{Target: key.String(), Agent: key.Agent, Packs: packs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out
}
