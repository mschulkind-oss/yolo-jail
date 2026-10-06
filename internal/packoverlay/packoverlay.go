// Package packoverlay collects `config-overlay` contributions ACROSS packs and resolves
// each onto the surface identity it targets — the piece that was missing while the kind
// sat inert (docs/reference/pack-system.md §6 Option 2). `config-list` contributions
// (docs/reference/pack-system.md#adding-entries-to-an-array-config-list) are the same
// cross-pack join and are collected in the same pass against the same owner set
// (OverlaySet.ListsFor), and so are an autonomy posture's `lists` and its `config` entries on
// another pack's surface, each gated on the posture the caller's notch selects
// (docs/design/notch-scoped-config-contributions.md §4.1 and OQ-3): a posture's lists join the
// lists, and its overlays join the overlays (OverlaySet.For).
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
	"encoding/json"
	"fmt"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// OverlaySet is the resolved config-overlay picture for one set of loaded packs: which
// overlays land on which surface, which land nowhere, and what went wrong.
type OverlaySet struct {
	// byTarget maps a surface identity to the overlay layers folding onto it, in
	// pack-then-declaration order (later wins — the same "later pack wins" rule skills
	// and launch flags already use).
	byTarget map[manifest.SurfaceKey][]agentcfg.Overlay

	// listsByTarget maps a surface identity to the config-list contributions appending to
	// it, in the same pack-then-declaration order
	// (docs/reference/pack-system.md#config-list-order). Collected in the same two passes
	// and against the same owner set, because a list is the same cross-pack join an
	// overlay is: pack B appending to a list pack A owns. The posture lists the render's
	// posture selects are here too, indistinguishable from a config-list once placed: the
	// fold, the per-entry capture and the rmw insert record key on the pack, not on which
	// kind declared the entries.
	listsByTarget map[manifest.SurfaceKey][]agentcfg.ListContribution

	// postureListsPlaced names each pack at least one of whose POSTURE LISTS was placed —
	// selected by the bit and given an owner — which listsByTarget cannot answer, since a
	// placed posture list is an ordinary ListContribution there. PlacesPostureListFrom reads
	// it.
	postureListsPlaced map[string]bool

	// posturePlaced names each pack at least one of whose POSTURE OVERLAYS — an autonomy
	// posture's `config` entry on another pack's surface — was placed, the config half's
	// twin of postureListsPlaced: byTarget holds a placed one as an ordinary Overlay, so it
	// cannot say which kind declared it. PlacesPostureConfigFrom reads it.
	posturePlaced map[string]bool

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
	// Kind is the contribution kind that went nowhere — packdecl.KindConfigOverlay,
	// packdecl.KindConfigList, or packdecl.KindAutonomy for a posture list or a posture
	// overlay — so a report names the kind the author wrote. All three are inert and reported
	// by the same rule (R2). A posture's contribution is an orphan only at a notch that
	// selects its posture; at any other it was never placed at all.
	Kind packdecl.Kind
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

	// PostureConfig marks an autonomy-kind orphan that is a posture's CONFIG PATCH (a posture
	// overlay) rather than a posture list, so the one sentence that names the declaration
	// names the right one.
	PostureConfig bool
}

// Reason renders the orphan as the one-line report ruling R2 specifies.
func (o OrphanOverlay) Reason() string {
	switch {
	case o.Owner != "":
		return fmt.Sprintf("no effect — %s has no owner (the `%s` pack is not selected)",
			o.Target, o.Owner)
	case o.CoreOwned:
		what := o.kindName()
		if o.Kind == packdecl.KindAutonomy {
			// The kind leads the line already; the sentence is about the declaration inside
			// it — a posture list, or a posture's config patch on another pack's surface —
			// since "autonomy contributes to a surface a pack owns" would name the kind whose
			// launch flags and own-surface patches contribute to nothing of the sort.
			what = "a posture list"
			if o.PostureConfig {
				what = "a posture's config patch"
			}
		}
		return fmt.Sprintf("no effect — %s is one of yolo's OWN surfaces, not a pack's; "+
			"%s contributes to a surface a pack owns", o.Target, what)
	default:
		return fmt.Sprintf("no effect — %s has no owner among the selected packs "+
			"(no pack declares that surface — check the identity)", o.Target)
	}
}

// kindName is the orphan's kind as written, defaulting to config-overlay for a value built
// before Kind existed.
func (o OrphanOverlay) kindName() string {
	if o.Kind == "" {
		return string(packdecl.KindConfigOverlay)
	}
	return string(o.Kind)
}

// KindName is the kind the report line leads with ("config-overlay" or "config-list").
func (o OrphanOverlay) KindName() string { return o.kindName() }

// ListsFor returns the config-list contributions appending to one surface, or nil for none
// — the universal answer for a pack set that declares no config-list, which composes
// byte-identically to before the kind existed.
func (s *OverlaySet) ListsFor(agent, name string) []agentcfg.ListContribution {
	if s == nil {
		return nil
	}
	return s.listsByTarget[manifest.SurfaceKey{Agent: agent, Name: name}]
}

// PlacesPostureListFrom reports whether this collection PLACED a posture list the named pack
// declares: one whose posture the collecting notch selects and whose surface has an owner in
// the set. A declared posture list is not enough — an unselected one is skipped and an
// ownerless one is an orphan, and neither lands in a surface — so this is the question
// `yolo host apply`'s notch line asks before it says the posture "folded into the config
// surfaces below" (notch-scoped-config-contributions.md NS-D12).
func (s *OverlaySet) PlacesPostureListFrom(pack string) bool {
	if s == nil {
		return false
	}
	return s.postureListsPlaced[pack]
}

// PlacesPostureConfigFrom is PlacesPostureListFrom for the config half: whether this
// collection PLACED a posture overlay the named pack declares — an autonomy posture's `config`
// entry on a surface another pack owns, whose posture the collecting notch selects and whose
// surface has an owner in the set. `yolo host apply`'s notch line asks it for the same reason
// (notch-scoped-config-contributions.md NS-D24): an ownerless one is an orphan and folds into
// nothing below.
func (s *OverlaySet) PlacesPostureConfigFrom(pack string) bool {
	if s == nil {
		return false
	}
	return s.posturePlaced[pack]
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

// Collect resolves every loaded pack's config-overlay and config-list contributions — and
// the POSTURE LISTS and POSTURE OVERLAYS its autonomy contributions declare — against the
// surfaces those same packs own.
//
// autonomy is the §4.2 policy bit of the render target's confinement profile
// (render.Target.Profile().AgentAutonomy). It does exactly two things here, and only the
// second reaches the output:
//
//   - It selects which posture the OWNER's surfaces are decoded under, so the owner set
//     matches the render the contributions will fold into.
//   - It selects which posture's cross-pack contributions are placed: its LISTS
//     (packdecl.AutonomyPosture.Lists, docs/design/notch-scoped-config-contributions.md §4.1)
//     and its OVERLAYS, the `config` entries naming a surface the declaring pack does not own
//     (packdecl.AutonomyPosture.Config, OQ-3's ruling). The guarded posture's while the bit is
//     off, the autonomous posture's while it is on. A config-overlay and a plain config-list
//     carry no posture and contribute at both.
//
// Still a bool rather than a render.Profile, and by now that is a deliberate boundary rather
// than a leftover: every caller derives the bit from a Target's Profile — the boot loop and
// `yolo check`'s probe from the boot Target (entrypoint.ConfigurePackSurfaces,
// entrypoint.ConfigurePackByName), `yolo host apply` from render.Host (applyHostSurveyed), and
// `yolo config render`, `config ls` and `config promote` from render.ProfileFor over the notch
// they describe (renderContributions, overlayContributionRows, loadPromoteFold) — so the
// literals plan §6c step 1 set out to remove are gone; C3 closed the last one. Taking a
// Profile here would import the confinement model into a package whose whole job is resolving
// contributions against owners — it needs ONE bit, and receiving one bit is what keeps this
// package unable to disagree with the notch that computed it. It also keeps notch NAMES out
// of here and out of every manifest (pack-system.md §6c): a posture's contributions are gated
// on the posture the notch's profile picks, never on a spelling of the notch.
//
// THE BIT NEVER CHANGES SURFACE IDENTITIES OR OWNERSHIP, which is worth stating because the
// first half above looks like it should. The posture fold (packload.foldPostureManaged)
// merges keys into the Managed layer of surfaces already declared, leaves a patch naming no
// surface of its pack to this function (a posture overlay), and never adds or removes an
// identity — so both postures yield the same surface-identity set, and identities are all the
// owner pass reads. autonomyinert_test.go pins that half as a property: if a posture ever
// gains the power to add or remove an identity, the bit starts deciding which contributions
// find an owner, and that test fails at the moment it does. What the bit DOES decide is which
// posture lists and posture overlays are placed, pinned in both directions by
// autonomyinert_test.go and postureoverlay_test.go. At a caller, inverting the argument is
// visible wherever the caller reads ListsFor or For, because a posture's contribution moves
// between the two answers, and each such caller has a test that reads the moved entry or key:
// the boot loop (posturelistrender_test.go, postureoverlayrender_test.go) and `yolo check`'s
// probe (configureonepack_test.go, lists only: the probe collects over the one pack it
// renders, so a posture overlay, on another pack's surface by definition, is never placed
// there), `yolo host apply`, `config render`
// and `config ls` (internal/cli's posturelist_test.go and postureoverlay_test.go), and
// `config promote`, which reads only overlays (For) and so reads a posture overlay too
// (internal/cli's TestPromoteIsOutrankedByAJailPostureOverlay). Where the bit is
// consequential for the surfaces themselves — p.SurfacesFor at the render — it is pinned in
// both directions by internal/entrypoint/bootautonomy_test.go.
//
// There is NO PROFILE FOLD for the same reason, and since OQ-PT8 that is a statement about
// the kind rather than about a missing parameter: the profile's config body used to be a
// variant patch merged inside SurfacesFor, and the shrink moved it HERE — a `config-overlay`
// gated by the `profile` modifier — so a profile contributes through this function's own
// gate, never by changing which identities exist. The owner pass still takes no table,
// because ownership is a property of the packs alone.
//
// profiles is the ACTIVE profile table the CALLER's render resolved — packload.ProfileTable's
// lowering of YOLO_USE_PROFILES in the jail, of the config's profile at the host —
// keyed by CLI name, and it gates the `profile` MODIFIER (docs/reference/providers.md#the-profile-modifier):
// an overlay declaring a profile contributes only while that name is the one active for the
// surface's OWNING agent, which is the target identity's agent segment (an "agent/name"
// identity's agent half IS a CLI name, the namespace the table keys on). Taking the table
// as a parameter rather than re-deriving it is what keeps the gate from answering the
// "which profile is selected" question differently than the other profile consumers in the
// same render — the caller already resolved it once for all of them.
//
// An inactive profile is a CLEAN SKIP — no error, no orphan report, no applied notice —
// because selection is the optionality (providers.md#the-profile-modifier, the same rule that makes an unselected owner
// a skip rather than a refusal) and profile VALUES were free-form (providers.md#pv-oq-3, since superseded by #oq-cs6): a name
// nothing selected is inert, and reporting it would be a launch that second-guesses the
// user's `-p`. A profile-gated overlay whose target has no owner while the profile IS
// active is still an orphan — R2's report fires for the reason that actually stopped the
// contribution.
func Collect(packs []*packload.Pack, autonomy bool, profiles map[string]string) *OverlaySet {
	set := &OverlaySet{
		byTarget:           map[manifest.SurfaceKey][]agentcfg.Overlay{},
		listsByTarget:      map[manifest.SurfaceKey][]agentcfg.ListContribution{},
		postureListsPlaced: map[string]bool{},
		posturePlaced:      map[string]bool{},
	}

	// Pass 1: who owns what. A surface's owner is the pack whose `config` contribution
	// declares it. Surface problems are NOT collected here — the render path reports
	// them against the owning pack already, and duplicating them would double every
	// message on a broken manifest.
	//
	// ownerSurface keeps the declaration beside the name, for the one check a POSTURE
	// OVERLAY owes its owner (its path and codec, below), and ownKeys each pack's own
	// identities, which is what tells a posture's config patch on its own surface (the
	// posture fold's) from one on another pack's (this pass's).
	owners := map[manifest.SurfaceKey]string{}
	ownerSurface := map[manifest.SurfaceKey]manifest.Surface{}
	ownKeys := map[*packload.Pack]map[manifest.SurfaceKey]bool{}
	for _, p := range packs {
		surfaces, _ := p.SurfacesFor(autonomy)
		ownKeys[p] = map[manifest.SurfaceKey]bool{}
		for _, s := range surfaces {
			owners[s.Key()] = p.Name
			ownerSurface[s.Key()] = s
			ownKeys[p][s.Key()] = true
		}
	}

	// Pass 2: place each overlay, in pack order then declaration order, so the fold
	// order is the config's `packs` order — the same precedence a reader already expects
	// from every other multi-pack kind. Two declarations take this pass: a `config-overlay`
	// contribution, and a POSTURE OVERLAY — an autonomy posture's `config` entry naming a
	// surface its own pack does not declare (OQ-3's ruling, "do it now. extension point.";
	// notch-scoped-config-contributions.md NS-D19), which stands at its autonomy
	// contribution's position, so "later wins" between the two is declaration order.
	selected := packdecl.PostureOf(autonomy)
	for _, p := range packs {
		for _, ov := range p.Decl.OverlayContributions() {
			kind := packdecl.KindConfigOverlay
			var key manifest.SurfaceKey
			var data map[string]any
			var patch manifest.Surface
			if ov.Posture == "" {
				var err error
				if key, err = manifest.ParseSurfaceID(ov.Surface); err != nil {
					set.Problems = append(set.Problems, "pack "+p.Name+": config-overlay: "+err.Error())
					continue
				}
				var probs []string
				if data, probs = manifest.DecodeOverlay(ov.Surface, ov.Config); len(probs) > 0 {
					for _, prob := range probs {
						set.Problems = append(set.Problems, "pack "+p.Name+": "+prob)
					}
					continue
				}
			} else {
				// A POSTURE OVERLAY CANDIDATE. Two dispositions come before the gate:
				//
				//   - on a surface this pack declares: the posture fold's to MERGE into that
				//     surface's managed layer, which is where a pack's own permission keys belong
				//     (above capture and host), so never an overlay;
				//   - malformed, or breaking config-overlay's rules (manifest.DecodePostureOverlay):
				//     a Problem at EVERY notch, whichever posture it sits under, as a malformed
				//     posture list is — the author of a host-only key hears it from a jail boot
				//     too. Led by `autonomy <posture>.config`, the kind as written, for
				//     collectProblemKind. The one exception is a malformed entry the posture fold
				//     ALSO decodes — the selected posture of a pack declaring a surface of its own
				//     (SurfacesForReport) — which that fold already names against the pack, and a
				//     second copy of one complaint is noise.
				kind = packdecl.KindAutonomy
				if own, err := manifest.ParseSurfaceID(ov.Surface); err == nil && ownKeys[p][own] {
					continue
				}
				label := fmt.Sprintf("%s %s.config %s", packdecl.KindAutonomy, ov.Posture, ov.Surface)
				var wellFormed bool
				var probs []string
				patch, data, wellFormed, probs = manifest.DecodePostureOverlay(label, ov.Config)
				if len(probs) > 0 {
					foldReports := !wellFormed && len(ownKeys[p]) > 0 && ov.Posture == selected
					for _, prob := range probs {
						if !foldReports {
							set.Problems = append(set.Problems, "pack "+p.Name+": "+prob)
						}
					}
					continue
				}
				key = patch.Key()
				// THE POSTURE GATE, at the posture list's position and for its reason (Pass 3): an
				// unselected posture's overlay is a clean skip — no problem, no orphan, no
				// applied row — because the reason it contributed nothing is the notch.
				if ov.Posture != selected {
					continue
				}
			}
			// The PROFILE GATE, after the body decode and before the owner check, and both
			// positions are load-bearing. A malformed body is the AUTHOR's mistake and is
			// reported whatever the launch selected — an overlay that renders only under
			// profile X is still wrong when X is off, or the author would never hear it.
			// The owner check comes AFTER the gate so a profile-inactive overlay is not
			// reported as an orphan: the reason it contributed nothing is the selection,
			// and R2's report exists to name the remedy ("select that pack"), which here
			// would be a remedy the user deliberately declined. (A posture overlay carries no
			// profile: its one gate is the posture's, above.)
			if ov.Profile != "" && profiles[key.Agent] != ov.Profile {
				continue
			}
			if _, owned := owners[key]; !owned {
				// R2: inert and reported. Named by target, with the shipped owner when
				// there is one, so the fix ("select that pack") is in the message. Led by the
				// kind the author wrote, so an ownerless posture overlay says `autonomy`.
				_, coreOwned := agentcfg.BuiltinManifest().Lookup(key.Agent, key.Name)
				set.Orphans = append(set.Orphans, OrphanOverlay{
					Kind: kind, Pack: p.Name, Target: ov.Surface,
					Owner: shippedOwnerOf(key, autonomy), CoreOwned: coreOwned,
					PostureConfig: ov.Posture != "",
				})
				continue
			}
			if ov.Posture != "" {
				// THE OWNER DECIDES WHERE THE FILE LANDS AND ITS FORMAT (NS-D21). A posture patch
				// spells `path` and `codec` because the surface schema requires them; one on another
				// pack's surface that names a different file or codec would read, to its author,
				// like keys written somewhere they are not. Only here is the owner known, so this
				// is the one overlay rule checked after the gate: at the notch that would place it.
				if owner := ownerSurface[key]; patch.Path != owner.Path || patch.Codec != owner.Codec {
					set.Problems = append(set.Problems, fmt.Sprintf("pack %s: %s %s.config %s: names "+
						"path %q and codec %q, but pack %s declares path %q and codec %q — a posture's "+
						"config patch on another pack's surface contributes keys, and the surface's OWNER "+
						"decides where the file lands and its format; spell the owner's",
						p.Name, packdecl.KindAutonomy, ov.Posture, ov.Surface, patch.Path, patch.Codec,
						owners[key], owner.Path, owner.Codec))
					continue
				}
				set.posturePlaced[p.Name] = true
			}
			set.byTarget[key] = append(set.byTarget[key], agentcfg.Overlay{Pack: p.Name, Data: data})
		}
	}

	// Pass 3: place each list the same way — pack order, then declaration order, which is
	// the order the entries append in (pack-system.md#config-list-order). Two declarations
	// take this pass: a `config-list` contribution, and a POSTURE LIST (an autonomy
	// posture's `lists`), which stands at its autonomy contribution's position. A malformed
	// one is a Problem, loud like a malformed overlay; an ownerless one is inert and
	// reported (R2), led by the kind the author wrote. A list on a KEYLESS surface is NOT
	// refused here — the owner's codec is the render's to judge, and the render refuses it
	// naming the surface and its mode (agentcfg.ListCaptureRefusal).
	//
	// No profile gate: config-list takes none (packdecl refuses `profile` on it). The one
	// gate is the POSTURE's, below.
	//
	// A REGISTERING SLOT's entries take this pass too, each placed ahead of its contributing
	// pack's own lists (registrationsOf).
	registrations := registrationsOf(packs, ownKeys, set)
	for _, p := range packs {
		for _, r := range registrations[p.Name] {
			set.listsByTarget[r.key] = append(set.listsByTarget[r.key], r.list)
		}
		for _, cl := range p.Decl.ListContributions() {
			kind, what := packdecl.KindConfigList, string(packdecl.KindConfigList)
			if cl.Posture != "" {
				// The problem text leads with the kind as written, for the reader that labels
				// a problem by it (apply.go's collectProblemKind): "config-list" there would
				// name a declaration the author never made.
				kind, what = packdecl.KindAutonomy, fmt.Sprintf("%s %s.lists",
					packdecl.KindAutonomy, cl.Posture)
			}
			key, err := manifest.ParseSurfaceID(cl.Surface)
			if err != nil {
				set.Problems = append(set.Problems, "pack "+p.Name+": "+what+": "+err.Error())
				continue
			}
			list, err := agentcfg.NewListContribution(p.Name, cl.Path, cl.Add)
			if err != nil {
				set.Problems = append(set.Problems, "pack "+p.Name+": "+what+" on "+
					cl.Surface+": "+err.Error())
				continue
			}
			// THE POSTURE GATE, after the decode and before the owner check — the profile
			// gate's two positions, for the profile gate's two reasons (Pass 2). A malformed
			// posture list is reported at EVERY notch, because the author must hear about a
			// host-only entry from a jail boot too. And a posture this notch does not select
			// is a CLEAN SKIP — no problem, no orphan, no applied row — because the reason it
			// contributed nothing is the notch, and R2's remedy ("select that pack") would be
			// the wrong one.
			if cl.Posture != "" && cl.Posture != selected {
				continue
			}
			if _, owned := owners[key]; !owned {
				_, coreOwned := agentcfg.BuiltinManifest().Lookup(key.Agent, key.Name)
				set.Orphans = append(set.Orphans, OrphanOverlay{
					Kind: kind, Pack: p.Name, Target: cl.Surface,
					Owner: shippedOwnerOf(key, autonomy), CoreOwned: coreOwned,
				})
				continue
			}
			set.listsByTarget[key] = append(set.listsByTarget[key], list)
			if cl.Posture != "" {
				set.postureListsPlaced[p.Name] = true
			}
		}
	}

	sort.SliceStable(set.Orphans, func(i, j int) bool {
		if set.Orphans[i].Target != set.Orphans[j].Target {
			return set.Orphans[i].Target < set.Orphans[j].Target
		}
		if set.Orphans[i].Pack != set.Orphans[j].Pack {
			return set.Orphans[i].Pack < set.Orphans[j].Pack
		}
		return set.Orphans[i].kindName() < set.Orphans[j].kindName()
	})
	return set
}

// placedRegistration is one registering slot's entry for one landed tree, ready to fold: the
// surface it lands in and the list contribution, attributed to the pack whose tree it is.
type placedRegistration struct {
	key  manifest.SurfaceKey
	list agentcfg.ListContribution
}

// registrationsOf is the list entries the REGISTERING `files` slots in `packs` ask for, keyed by
// the CONTRIBUTING pack (packload.Registrations; docs/design/pack-pi-resources.md §3.3), and it adds
// a Problem to `set` for each slot whose `register` names a surface its own pack does not declare.
//
// Each entry is an ordinary config-list entry of the pack whose tree landed, which is the whole
// design (PR-D2): the fold, the jail's per-entry capture, the host's inserted-entries record and the
// removal of a dropped pack's entries are config-list's, so a tree's entry leaves with its pack at
// both notches by the rule that already removes a dropped pack's own config-list.
//
// The slot's OWN pack's surface, because only that is the owner's to promise: an entry written into
// another pack's settings would make the slot work or not depending on a pack the slot cannot name.
// Checked per slot whether or not any tree addresses it, so `pack lint` of the owner alone says it.
func registrationsOf(packs []*packload.Pack, ownKeys map[*packload.Pack]map[manifest.SurfaceKey]bool,
	set *OverlaySet) map[string][]placedRegistration {
	owned := map[string]manifest.SurfaceKey{}
	for _, p := range packs {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindFiles || c.Agent == "" || c.Register == nil {
				continue
			}
			key, err := manifest.ParseSurfaceID(c.Register.Surface)
			switch {
			case err != nil:
				set.Problems = append(set.Problems, fmt.Sprintf("pack %s: files slot %s: register: %v",
					p.Name, c.Into, err))
			case !ownKeys[p][key]:
				set.Problems = append(set.Problems, fmt.Sprintf("pack %s: files slot %s: register "+
					"names surface %s, which this pack does not declare — a slot lists the trees "+
					"landing in it in a surface of its own pack, so declare that surface in a "+
					"`config` contribution of this pack, or name one it declares",
					p.Name, c.Into, c.Register.Surface))
			default:
				owned[p.Name+"\x00"+c.Into] = key
			}
		}
	}
	out := map[string][]placedRegistration{}
	for _, r := range packload.Registrations(packs) {
		key, ok := owned[r.Owner+"\x00"+r.Slot]
		if !ok {
			continue // its slot's Problem is above
		}
		add, err := json.Marshal([]string{r.Entry})
		if err != nil {
			continue
		}
		list, err := agentcfg.NewListContribution(r.Pack, r.Path, add)
		if err != nil {
			set.Problems = append(set.Problems, fmt.Sprintf("pack %s: files slot %s: register: %v",
				r.Owner, r.Slot, err))
			continue
		}
		out[r.Pack] = append(out[r.Pack], placedRegistration{key: key, list: list})
	}
	return out
}

// shippedOwnerOf names the EMBEDDED pack that owns a surface identity, or "" when none
// does.
//
// It reads the embedded set, every pack yolo ships whether selected or not, on purpose,
// and that is what makes the R2 message actionable rather than merely
// honest: "claude/settings has no owner" tells a user their overlay did nothing;
// "…(the `claude` pack is not selected)" tells them what to do about it. The distinction
// only exists if the lookup can see a pack the user did NOT select.
//
// Best-effort by design: an unresolvable embedded set degrades to the no-owner-anywhere
// message, which is still correct, just less helpful.
//
// autonomy is the caller's, threaded rather than fixed, so no posture literal survives in this
// package (plan §6c step 1). It cannot change the ANSWER: the posture fold only patches the
// managed layer of surfaces the pack already declares (foldPostureManaged leaves a patch
// naming no surface of its pack to Collect, as a posture overlay), so both postures yield the
// same surface-identity set — which is all this lookup reads.
//
// (The two SurfacesFor calls above this comment carried a nil profile table before OQ-PT8
// shrank the kind; the parameter is gone rather than accepted-and-ignored, because the fold
// it fed no longer exists — see the paragraph above.)
func shippedOwnerOf(key manifest.SurfaceKey, autonomy bool) string {
	for _, p := range packload.Embedded() {
		// No profile table, for the reason Collect states: identities are all this reads,
		// and ownership is a property of the packs, not of a selection.
		surfaces, _ := p.SurfacesFor(autonomy)
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

// AppliedLists lists the surfaces carrying config-list contributions, with the contributing
// packs in fold order (a pack contributing twice is named once), sorted by identity — the
// list twin of Applied, so an assembled array is legible at the moment it applies (R3, and
// "Each boot, and each `yolo host apply`, names the packs that appended entries",
// pack-system.md#config-list-visibility) rather than only in a sidecar.
func (s *OverlaySet) AppliedLists() []AppliedOverlay {
	if s == nil {
		return nil
	}
	out := make([]AppliedOverlay, 0, len(s.listsByTarget))
	for key, lists := range s.listsByTarget {
		var packs []string
		seen := map[string]bool{}
		for _, l := range lists {
			if !seen[l.Pack] {
				seen[l.Pack] = true
				packs = append(packs, l.Pack)
			}
		}
		out = append(out, AppliedOverlay{Target: key.String(), Agent: key.Agent, Packs: packs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out
}
