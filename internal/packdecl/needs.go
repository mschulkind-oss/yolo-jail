package packdecl

// needs.go is the `needs` half of CONDITIONAL PACK DEPENDENCY
// (docs/design/wire-bridge.md §3.1): a pack's declaration that ANOTHER pack belongs
// in the launch when a condition on the selected set holds. The first instance is
// cerebras needing wire-bridge when claude is among the launch's agents, but the
// vocabulary is general and lands on its own — the design is explicit that it is
// vocabulary and not a bridge-shaped special case (§8 step 1).
//
// # Why it is a TOP-LEVEL key and not a 19th contribution kind
//
// The same argument supersedes.go makes, and it lands even harder here. Every kind
// is a CONTRIBUTION — something the pack DELIVERS at a target, with a `Combine`
// rule saying how two claims on that target resolve. A need delivers nothing and
// owns no target: its effect is to EXTEND SELECTION, and its conflict rule is
// "already present = no-op" (WB-D10's join) — a rule about the selected SET, which
// no per-target combine rule can state. It would be a 16th entry in the exclusions
// list supersedes.go already pays for. The shape it matches is `supersedes` and
// `skills_tier`: a per-pack fact about how the pack relates to its environment,
// declared once, beside `name`.
//
// # What is enforced HERE, and what is deliberately not
//
// validateNeeds refuses only what is version-INVARIANT structure: an empty pack
// name, a pack name carrying "=", an empty bin entry. Both ends of a version
// boundary agree those are typos, so checking them on the tolerant path too cannot
// become the `tier` incident a fourth time (validateSupersedes' argument, which
// this follows rather than relitigates).
//
// NOT checked here, and could not be: whether the named pack is in the EMBEDDED
// official set (WB-D9) and whether the needs graph is acyclic (WB-D10). Both are
// facts about the PACK UNIVERSE, which this package cannot know — it is
// dependency-free by design (see the package doc), and packload.ResolveNeeds holds
// the universe at resolution time, so that is where both live. What IS decided by
// ruling and recorded here: the named pack must be embedded (WB-D9 — a fetched
// pack needs-ing another fetched pack would make selection itself a supply-chain
// channel, and refusing keeps `packs:` the only place unreviewed code enters a
// launch), resolution is a transitive closure run at selection BEFORE staging
// because the mount is the filter (WB-D10), explicit user selection is joined and
// never overridden (WB-D10), the auto-inclusion prints on the launch banner and in
// `yolo check` and a silent join is the one forbidden behavior (WB-D12), and user
// config carries no `needs` key — manifests only (WB-D11).

import (
	"fmt"
	"strings"
)

// PackNeed is one conditional pack dependency: the named pack joins the launch
// when the condition holds. Several entries per manifest are fine and evaluate
// independently.
type PackNeed struct {
	// Pack is the name of the pack this one needs. It must name an EMBEDDED
	// official pack (WB-D9) — enforced where the universe is known, at resolution
	// (packload.ResolveNeeds), not here.
	//
	// No "=" in the name. The name is echoed into the launch's cause line and a
	// `packs:` config entry, and "=" is the one character with an existing meaning
	// in yolo's selection grammar (`-p <cli>=<profile>`), so a name carrying it
	// reads as a selector the moment it is quoted onto a command line.
	Pack string `json:"pack"`
	// WhenBins is the condition: the need is LIVE only when some selected pack —
	// including one a live need added — installs one of these bins, OR over the
	// list. Bins, not agent names: core speaks bins ("AGENTS ARE PACKS"), and the
	// profile-gating `profile:` modifier keys on bins the same way. `claude` here
	// means "a selected pack installs the claude CLI", which is exactly "claude is
	// in use".
	//
	// Absent (or empty) means UNCONDITIONAL — the named pack joins whenever this
	// one is selected. Allowed; nothing ships one yet (checked 2026-09-04).
	WhenBins []string `json:"when_bins,omitempty"`
}

// DeclaredNeeds returns the pack's conditional dependencies, in declaration
// order. THE accessor — no reader touches the field, matching how every other
// manifest fact is read through a method. The name is not `Needs` because the
// field has it (the `Supersedes`/`Supersessions` split, for the same reason).
func (m *Manifest) DeclaredNeeds() []PackNeed { return m.Needs }

// validateNeeds reports every structural problem with the `needs` list.
//
// It runs on the TOLERANT path as well as the strict one, and that is safe for the
// reason validateSupersedes' rules are: every rule here is version-invariant. An
// empty pack name, a "=", an empty bin — all typos both ends of a version boundary
// agree about, so refusing them cannot brick a jail the way an unknown FIELD would
// (DecodeTolerant's doc records that incident three times over).
//
// WHAT IS NOT CHECKED HERE is stated in the file comment above: embedded
// membership and acyclicity are resolution-time facts (WB-D9, WB-D10), and
// packload.ResolveNeeds owns both.
func (m *Manifest) validateNeeds() []string {
	var problems []string
	for i, n := range m.Needs {
		if n.Pack == "" {
			problems = append(problems, fmt.Sprintf(
				"needs[%d]: \"pack\" is required — a need that names no pack extends "+
					"selection by nothing, and an author reading the cause lines would "+
					"never learn it did nothing", i))
		} else if strings.Contains(n.Pack, "=") {
			problems = append(problems, fmt.Sprintf(
				"needs[%d].pack %q: must not contain \"=\" — that character is yolo's "+
					"profile-selector separator, and a pack name carrying it reads as a "+
					"selection wherever the name is quoted", i, n.Pack))
		}
		for j, bin := range n.WhenBins {
			if bin == "" {
				problems = append(problems, fmt.Sprintf(
					"needs[%d].when_bins[%d]: must be a non-empty bin name — an empty "+
						"entry is either a trailing comma in the author's editor or a bin "+
						"nobody can install, and both make the condition read as live to "+
						"nobody", i, j))
			}
		}
	}
	return problems
}
