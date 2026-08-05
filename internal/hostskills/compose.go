package hostskills

// compose.go is the host-target SKILLS composition: yolo owns each skills destination
// WHOLESALE, exactly as the jail does, and the user's own tree MOVES into the local pack.
//
// Maintainer ruling, 2026-08-04 (outstanding-work.md §6a-2): *"I want to force user-level skills
// migration so we can cleanly own them. Out-of-sync skills between agents is the bigger risk."*
//
// WHAT THIS REPLACED, AND WHY IT IS THE SIMPLIFICATION. Delivery used to be per-pack and
// per-entry, negotiating with the destination through the ownership manifest: an entry yolo could
// not prove it wrote was reported `skipped (yours)` and left. That rule is correct for a user's
// hand-written skill and WRONG for a second pack's entry — a later pack could not overwrite an
// earlier pack's recorded name at all (§6a-5), so the local pack, defined as the layer that
// outranks everything, LOST a flat-tier collision. Under composition "later wins" is the
// mechanism rather than something a record has to permit, so that defect is not fixed here, it is
// unrepresentable.
//
// The decisions this encodes:
//
//   - THE JAIL'S COMPOSITION IS THE SPECIFICATION (agents.PrepareSkills): packs in config order,
//     later winning a same-named skill, with the LOCAL PACK last so a personal skill outranks a
//     shared one. That ordering is already what config.LoadPacks produces, so the precedence is
//     inherited from the pack order rather than restated here.
//   - BUILT-INS ARE STILL NOT WRITTEN TO A REAL HOME. The jail's layer 1 is yolo's own
//     jail-oriented skills (jail-startup, diagnosing-the-jail); on the host they describe an
//     environment the user is not in. The difference is deliberate and predates this file.
//   - TIERS DO NOT COLLAPSE, and that is the answer to the question §6a-2 left open. What a tier
//     decides is now only how the destination TOOL invokes a skill (`/<pack>:<name>` under a
//     per-pack subtree vs `/<name>` in a flat dir) and therefore what shape yolo writes — not
//     what yolo may overwrite, which is the half composition dissolves. Removing the concept
//     would change every namespaced invocation name, which no ruling asked for.
//   - THE USER'S TREE MOVES, it is not archived away. A hand-written skill is migrated into
//     ~/.config/yolo-jail/local/skills/, where the local pack composes it back into EVERY
//     destination — so the migration is behavior-PRESERVING and fixes the divergence the ruling
//     names, rather than merely not destroying anything. Archiving is the FALLBACK.
//   - A FIRST APPLY THAT ADOPTS A DESTINATION IS CONFIRMED. This package reports what WOULD be
//     adopted (Adoptions) and never decides; the CLI owns the gate.
//   - IDENTICAL CONTENT UNDER ONE NAME IS NOT A COLLISION. Measured before designing: every name
//     shared across this jail's four agent skills dirs was byte-identical (§6a-2), so comparing
//     CONTENT rather than names resolves the common case silently and correctly. DIFFERING
//     content under one name is a real conflict, gets a suffix, keeps BOTH, and warns naming both
//     sources — losing one of two hand-written skills silently is the failure the whole ruling
//     exists to prevent.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
)

// THE COMPOSED RECORD holds path → LAST COMPOSING PACK, and both halves of that are decisions.
//
// A REAL PACK NAME, not a pseudo-owner — which is where this deliberately diverges from the sibling
// briefing kind, whose record uses one (entrypoint.hostBriefingOwner). There, a destination is one
// FILE many packs concatenate into, so no single pack owns it and naming one would make dropping
// that pack read as "the file is the user's". Here a destination is a DIRECTORY and each entry in it
// has exactly one composer, so the pack name is both true and the only thing that answers the
// question ruling R1 asks: "did a pack that LEFT `packs` put this in my home?" A pseudo-owner cannot
// answer it, and R1's confirmation would have been silently dropped for skills.
//
// MEMBERSHIP, not equality, is what the write gate reads. That distinction IS the §6a-5 fix: the old
// rule was `OwnedBy(dest, thisPack)`, so a later pack could not overwrite an earlier pack's recorded
// name whatever the order and the local pack — appended last precisely because it outranks
// everything — lost a flat-tier collision. Asking only "is this path yolo's?" lets the LAYER ORDER
// be the precedence, and a name legitimately changing composer between applies is then self-healing
// rather than a permanent refusal.
//
// It is nonetheless its OWN FILE, and that half of §6a-6 defect 1 still holds: this record is
// rewritten wholesale by a mechanism that owns whole directories, while the per-entry record
// (hostSkillsManifestPath) is shared with the `files` kind and predates composition. One file, two
// writers with different notions of what a path means, is how the briefing record got every
// composed destination retired as a dropped pack's output.

// Layer is ONE pack's contribution to ONE destination, in composition order.
type Layer struct {
	// Pack is the contributing pack's name. At a namespaced destination it is also the subtree
	// name and the invocation namespace, so it is user-visible.
	Pack string
	// Description goes into the namespaced plugin manifest.
	Description string
	// Tier is the DECLARED tier of this contribution. Kept PER LAYER rather than promoted to the
	// destination, even though a tier is a fact about the destination tool: two packs naming one
	// dir with different tiers is a pack bug, but resolving it by picking one would silently move
	// a pack's skills from the flat namespace it declared into a subtree (or the reverse). Per
	// layer, a disagreement costs one oddly-shaped delivery that the report names, instead of a
	// declaration honored as its opposite.
	Tier Tier
	// Sources are the pack-relative skills dirs this contribution resolved to, empty when the
	// declared `from` could not be honored (Problem then says so).
	Sources []string
	// Plugins are the wrapped plugin trees this pack's origin permits, and Refusals the
	// per-component refusals its origin denied.
	Plugins  []*pluginpack.Plugin
	Refusals []string
	// Problem is a failure to report about this layer: an unresolvable source, an unknown tier.
	Problem string
	// Unresolved marks a layer whose DECLARED source could not be read, which is the sharper half
	// of Problem and the one with a consequence: the composition of this destination is incomplete,
	// so it must be left entirely alone rather than composed from the remaining layers. Composing
	// anyway would retire every other pack's skills there because one pack's `from` was misspelled.
	//
	// Distinct from Problem because an unknown TIER is also a problem and is NOT a reason to skip:
	// the layer still has content, it just gets the safe tier.
	Unresolved bool
}

// Destination is one skills dir and every layer composing into it, in pack order.
type Destination struct {
	// Dir is the absolute destination in the user's home.
	Dir string
	// Layers are the contributing packs' contributions, in composition order — so a LATER layer
	// wins a same-named skill at flat tier, which is what makes the local pack outrank a shared
	// pack's copy.
	Layers []Layer
}

// Packs names the contributing packs in composition order, for a report line.
func (d Destination) Packs() []string {
	out := make([]string, 0, len(d.Layers))
	for _, l := range d.Layers {
		out = append(out, l.Pack)
	}
	return out
}

// ComposeRequest carries what a host skills composition needs beyond the packs. Same shape and
// same reason as HostBriefingRequest: the caller owns path layout, this package stays pointed at
// whatever dirs it is handed so tests can use temps.
type ComposeRequest struct {
	// Composed is the record of what this mechanism wrote — its own file, keyed by absolute path,
	// every owner being ComposedOwner. Required for the adoption gate to mean anything: without
	// it every existing entry reads as the user's, which is safe (nothing is adopted unconfirmed)
	// but means yolo can never regenerate its own output unprompted.
	Composed *Manifest
	// Legacy is the PER-ENTRY, per-pack record the pre-composition delivery kept (shared with the
	// `files` kind). It is read and never written: its only remaining job is to stop a skill a
	// previous yolo delivered from being adopted as though the user had written it, which would
	// migrate yolo's own output into the user's local pack on the very first apply after upgrade.
	Legacy *Manifest
	// ArchiveRoot is where a retired entry, or content that cannot be MOVED, is archived.
	ArchiveRoot ArchiveRoot
	// Stamp names the archive generation, so one apply groups its moves together.
	Stamp string
	// LocalPackSkills is the absolute path of the local pack's own skills/ dir — where an adopted
	// entry MOVES to. Empty disables the move and falls back to archiving, which is what a caller
	// with no resolvable local-pack location should do rather than guess one.
	LocalPackSkills string
	// PackSetComplete asserts that every pack the config NAMES resolved this run. Only then may
	// EITHER retire pass conclude that content has no contributor left.
	//
	// It gates the RENDER's own retire as well as PruneHostSkills, and that is sharper than the
	// briefing analogue it is modelled on. There, an unresolvable pack could only produce an orphan
	// the PRUNE would find. Here the agent pack that NAMES the destination stays resolvable while
	// the content pack does not — so the destination is still composed, and a retire keyed only on
	// "no remaining layer ships this name" archives the unreachable pack's skills on every offline
	// apply while reporting the directory as successfully composed. Found by running the lifecycle.
	//
	// FALSE IS THE FAIL-CLOSED ZERO VALUE: archiving on a bad guess costs the user a trip to the
	// state dir (§6a-6 defect 3), so a caller that does not answer retires nothing.
	PackSetComplete bool
	// Configured is the set of pack names the config NAMES, resolvable or not. It is the boundary
	// between the retire this package performs SILENTLY and the one ruling R1 requires a
	// confirmation for — see retireComposed's gate comment for why those are different user actions.
	//
	// NIL IS THE FAIL-CLOSED ZERO VALUE and it means "retire nothing", which is why every retire
	// path consults it rather than only the prune: a still-live destination's record can name a pack
	// the user has since dropped, and the RENDER reaches those entries first. Found by running the
	// lifecycle — the first cut gated only the prune, so R1's [y/N] silently stopped firing for
	// `skills` while the gate itself was still there.
	Configured map[string]bool
}

// ComposeHostSkills resolves every skills destination the given packs name into its layers, in
// pack order.
//
// A PURE function of the pack set — no writes, no ownership questions — because four passes need
// the same answer for different reasons: the adoption scan reads the destinations, the migration
// reads them, the render writes them, and the prune needs to know which still have a contributor.
// Computing it four times from four loops is how the two notches came to disagree about `from` in
// the first place (skillssource.go's opening comment).
func ComposeHostSkills(packs []*packload.Pack, homeDir string) []Destination {
	var order []string
	byDir := map[string]*Destination{}
	for _, p := range packs {
		if p == nil {
			continue
		}
		// Per PACK, not per contribution: a wrapped plugin is carried BY the pack's skills
		// contributions, so a pack declaring two destinations delivers its plugin to both — the
		// behavior the per-pack delivery had.
		plugins, refused := p.HonoredPlugins()
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindSkills || c.Into == "" {
				continue
			}
			dir := filepath.Join(homeDir, filepath.FromSlash(c.Into))
			d, seen := byDir[dir]
			if !seen {
				d = &Destination{Dir: dir}
				byDir[dir] = d
				order = append(order, dir)
			}
			l := Layer{Pack: p.Name, Description: p.Decl.Description, Plugins: plugins,
				Refusals: refused}
			tier, ok := ParseTier(c.Tier)
			l.Tier = tier
			if !ok {
				// Reachable only for a manifest that bypassed validation (an older pack, a
				// hand-edited staged tree). Say so rather than silently choosing.
				l.Problem = fmt.Sprintf("pack %s: unknown skills tier %q — using flat (the safe "+
					"tier)", p.Name, c.Tier)
			}
			src, prob := p.SkillsSourceDir(c)
			if prob != "" {
				l.Problem, l.Unresolved = prob, true
			}
			if src != "" {
				l.Sources = []string{src}
			}
			d.Layers = append(d.Layers, l)
		}
	}
	out := make([]Destination, 0, len(order))
	for _, dir := range order {
		out = append(out, *byDir[dir])
	}
	return out
}

// Adoption is one entry at a composed destination that yolo cannot prove it wrote — the user's
// own skill, about to be taken over. It is what the CLI's confirmation gate names and what
// MigrateHostSkills acts on once confirmed.
type Adoption struct {
	// Dir is the destination the entry sits in.
	Dir string
	// Name is the entry's directory name — the identity the tool invokes.
	Name string
	// Path is the absolute path of the entry.
	Path string
}

// Adoptions returns every entry at these destinations that yolo did not write, sorted by path.
//
// OWNERSHIP IS PROVED FROM A RECORD OR A MARKER, never inferred from content. Four kinds of entry
// are excluded, and each exclusion is a decision:
//
//   - a path in the COMPOSED record, whichever pack it names: yolo's own output, its to regenerate.
//     Owner-agnostic for ownedHere's reason — the question is "is this yolo's?", never "is this
//     THIS pack's?" (§6a-5).
//   - a path in the LEGACY per-entry record: yolo's own output from before this mechanism. Without
//     this the first apply after an upgrade would migrate every skill yolo had ever delivered into
//     the user's local pack, which is the loudest possible way to get the migration wrong.
//   - a yolo-marked PLUGIN dir: the namespaced shape, proved by its own manifest (tier.go).
//   - a HAND-AUTHORED plugin dir: a plugin is not a skill. Moving one into the local pack's
//     skills/ would re-deliver it under a different namespace and break the component paths its
//     manifest declares, so it is left alone and reported. That is the one place the "yolo owns
//     the directory" claim yields, and it yields to content this kind does not model rather than
//     to content it merely did not write.
//
// A DANGLING SYMLINK is not an adoption either: it is absent content, cleared by the render
// (dangling.go), and archiving or moving a broken link would put a file in the local pack the user
// can find and cannot use. A loose FILE is not an adoption because it is not a skill to any of
// these tools; a dot-entry is not one because the composition never writes one at a destination's
// top level.
func Adoptions(dests []Destination, req ComposeRequest) (adoptions []Adoption, plugins []Result) {
	for _, d := range dests {
		entries, err := os.ReadDir(d.Dir)
		if err != nil {
			continue // a destination nothing has been written to yet
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			path := filepath.Join(d.Dir, name)
			// Stat, not the DirEntry: a symlink to a directory is a legitimate skill (the tools
			// follow them) and an Lstat-shaped IsDir would drop it.
			fi, serr := os.Stat(path)
			if serr != nil || !fi.IsDir() {
				continue
			}
			if req.Composed != nil {
				if _, ok := req.Composed.Owner(path); ok {
					continue
				}
			}
			if req.Legacy != nil {
				if _, ok := req.Legacy.Owner(path); ok {
					continue
				}
			}
			if IsYoloPluginDir(path) {
				continue
			}
			if _, isPlugin := pluginpack.ManifestPath(path); isPlugin {
				plugins = append(plugins, Result{
					Name: name, Path: path, Action: ActionSkippedUser,
					Detail: "a plugin you authored — yolo composes SKILLS here and does not " +
						"model a plugin's components, so it is left exactly as it is",
				})
				continue
			}
			adoptions = append(adoptions, Adoption{Dir: d.Dir, Name: name, Path: path})
		}
	}
	sort.Slice(adoptions, func(i, j int) bool { return adoptions[i].Path < adoptions[j].Path })
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Path < plugins[j].Path })
	return adoptions, plugins
}

// MigrateHostSkills moves each adopted entry into the local pack's skills/, so the user's skills
// keep reaching their agents — through the layer model instead of loose per-agent copies. Returns
// one Result per adoption.
//
// MOVE, NOT ARCHIVE (§6a-2). Archiving is safe but it is not a MIGRATION: the skill ends up in a
// timestamped directory nothing reads. Moving it into the pack yolo already composes means the
// same skill reaches EVERY agent on the very next render — which is also the fix for the risk the
// ruling names, since one copy cannot drift per agent. Archiving stays the FALLBACK for anything
// that cannot be moved, so nothing is ever deleted whichever path runs.
//
// THE UNION IS BY CONTENT. Two agents holding byte-identical copies of one name is the measured
// common case (§6a-2), and it is not a collision: the first copy moves, the second is dropped
// silently because it is already there. DIFFERING content under one name is a real conflict and
// gets a suffix, keeping both — the caller warns, naming both sources.
//
// observe computes and writes nothing, which is why the caller can preview a migration it has not
// confirmed. The union/suffix decision is taken against a PLANNED view of the local pack rather
// than the filesystem, so the dry run cannot resolve a name differently from the write: without
// it, observe would see every target absent (nothing having moved yet) and promise `mine` for two
// entries the write would split into `mine` and `mine-from-codex`.
func MigrateHostSkills(adoptions []Adoption, req ComposeRequest, observe bool) ([]Result, error) {
	planned := plannedLocalPack(req.LocalPackSkills)
	var out []Result
	for _, a := range adoptions {
		res := Result{Name: a.Name, Path: a.Path}
		if req.LocalPackSkills == "" {
			// No local pack location: fall back to the archive, which loses nothing but does not
			// preserve behavior. Named as the fallback so the difference is visible.
			res.Action, res.Detail = archivedAction(observe),
				skillsArchiveDetail(a.Path, req, observe, "no local pack to move it into")
			out = append(out, res)
			continue
		}
		digest, derr := treeDigest(a.Path)
		if derr != nil {
			// Unreadable content cannot be compared, so it cannot be unioned. Archiving it is the
			// fallback the ruling reserves for exactly this, and leaving it in place would let the
			// render compose over content nobody proved anything about.
			res.Action, res.Detail = archivedAction(observe),
				skillsArchiveDetail(a.Path, req, observe, "could not read it: "+derr.Error())
			out = append(out, res)
			continue
		}
		target, existing, renamed := planLocalPackTarget(planned, a.Name, digest, a.Dir)
		dest := filepath.Join(req.LocalPackSkills, target)
		switch {
		case existing:
			// Already in the local pack, byte-identical. The entry here is a redundant per-agent
			// copy — exactly the duplication the ruling is about — so it is simply removed, which
			// is what makes the union silent in the common case.
			res.Action = unionedAction(observe)
			res.Detail = "an identical copy is already in your local pack (" + dest + ")"
			if !observe {
				if err := os.RemoveAll(a.Path); err != nil {
					res.Action, res.Detail = ActionRefused, err.Error()
				}
			}
		case renamed != "":
			res.Action = renamedAction(observe)
			res.Detail = "kept BOTH: " + renamed + " already holds a DIFFERENT skill of this " +
				"name, so this one moves to " + dest
			if !observe {
				if err := moveTree(a.Path, dest); err != nil {
					res.Action, res.Detail = archivedAction(observe),
						skillsArchiveDetail(a.Path, req, observe,
							"could not move it into the local pack: "+err.Error())
				}
			}
		default:
			res.Action = movedAction(observe)
			res.Detail = "moved to " + dest + " — yolo composes it back into every destination"
			if !observe {
				if err := moveTree(a.Path, dest); err != nil {
					// A failed move must not silently become a wholesale overwrite on the render
					// that follows. Archive instead, and report both halves so the user knows
					// which one ran.
					res.Action, res.Detail = archivedAction(observe),
						skillsArchiveDetail(a.Path, req, observe,
							"could not move it into the local pack: "+err.Error())
				}
			}
		}
		planned[target] = digest
		out = append(out, res)
	}
	return out, nil
}

// plannedLocalPack is {skill name → content digest} for the local pack's skills dir, the view
// MigrateHostSkills resolves names against. An unreadable entry gets an empty digest, which
// compares unequal to everything and so is treated as a DIFFERENT skill — the direction that
// keeps both copies.
func plannedLocalPack(dir string) map[string]string {
	out := map[string]string{}
	if dir == "" {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if fi, serr := os.Stat(path); serr != nil || !fi.IsDir() {
			continue
		}
		digest, _ := treeDigest(path)
		out[e.Name()] = digest
	}
	return out
}

// planLocalPackTarget decides where one adopted entry lands in the local pack: the name itself
// when it is free, "already there" when an identical copy holds it, or a suffixed name when a
// DIFFERENT skill does. `renamed` is the occupied name a suffix was needed for, so the caller can
// warn naming both sources.
func planLocalPackTarget(planned map[string]string, name, digest, fromDir string) (
	target string, existing bool, renamed string) {
	have, occupied := planned[name]
	switch {
	case !occupied:
		return name, false, ""
	case have == digest && digest != "":
		return name, true, ""
	}
	// A real conflict: two hand-written skills sharing a name. Suffix by the destination the
	// loser came FROM, because that is the fact the user needs to tell the two apart — "which of
	// my agents did this one live in?" — and it is stable across applies, unlike a counter.
	base := name + "-from-" + destinationLabel(fromDir)
	for i, candidate := 2, base; ; i++ {
		if have, occupied := planned[candidate]; !occupied {
			return candidate, false, name
		} else if have == digest && digest != "" {
			return candidate, true, ""
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

// destinationLabel names a skills destination the way its user would: the agent whose home it is.
//
// Derived from the PATH rather than from the destination's contributing packs, and that is
// deliberate. The contributing pack list is in config order, so its first entry is whichever pack
// the user happened to list first — `packs: ["sflat","codex"]` would label a `.codex/skills`
// conflict "from-sflat", which is actively misleading. The path is the thing the user typed into
// no config and recognizes on sight.
//
// Scanned from the RIGHT — inward from the skills dir — because that is where the agent's name
// sits and where the path stops being about the agent: leftward lies `/home/<user>`, which every
// destination shares and which labels nothing. Structural segments on the way are dropped, so a
// nested layout still yields a tool name: `.pi/agent/skills` → `pi`, `.config/opencode/skills` →
// `opencode`, `.claude/skills` → `claude`.
func destinationLabel(dir string) string {
	skip := map[string]bool{"skills": true, "agent": true, "config": true, "": true}
	segs := strings.Split(filepath.ToSlash(dir), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		label := strings.TrimPrefix(segs[i], ".")
		if skip[label] {
			continue
		}
		if label != "" {
			return label
		}
	}
	return "your-agent"
}

// skillsArchiveDetail archives a path and returns the detail line, or a refusal.
func skillsArchiveDetail(path string, req ComposeRequest, observe bool, why string) string {
	if observe {
		return "would archive (" + why + ")"
	}
	at, err := Archive(req.ArchiveRoot, req.Stamp, "skills", path)
	if err != nil {
		return "refused: could not archive it: " + err.Error()
	}
	return "archived (" + why + ") → " + at
}

// RenderHostSkills composes every destination wholesale and returns one Result per entry
// considered.
//
// PER DESTINATION rather than per pack, and that is the structural consequence of the ruling: the
// directory's content is the union of every contributing pack's skills, so a per-pack pass would
// have to either leave an earlier pack's stale entry behind (which is what the per-entry retire
// did) or refuse to overwrite it (which is §6a-5).
//
// The order inside one destination is load-bearing:
//
//  1. CLEAR a dangling link standing where a directory has to be, before anything stats the tree.
//  2. WRITE each layer in composition order, so a later layer's same-named skill wins. Overwriting
//     an entry the composed record owns needs no negotiation — it is yolo's own output either way.
//  3. RETIRE what the composed record still names and this composition no longer ships.
//
// Retire LAST, not first: a name that moves from one pack to another between applies must not be
// archived and immediately rewritten, which is what a retire-then-write order produces (an archive
// entry per apply, forever, for a destination that never changed).
func RenderHostSkills(dests []Destination, req ComposeRequest, observe bool) ([]Result, error) {
	var out []Result
	for _, d := range dests {
		res, err := renderDestination(d, req, observe)
		out = append(out, res...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func renderDestination(d Destination, req ComposeRequest, observe bool) ([]Result, error) {
	out := clearDanglingDirs(d.Dir, d.Dir, observe)
	// The paths yolo owned here BEFORE this apply. Snapshotted because the write loop records as
	// it goes: without it, the second layer to touch a name would see the first layer's brand-new
	// record and archive a copy of content that was never in the user's home.
	preOwned := map[string]bool{}
	if req.Composed != nil {
		for _, p := range req.Composed.EntriesUnder(d.Dir) {
			preOwned[p] = true
		}
	}

	var problems []Result
	unresolved := false
	for _, l := range d.Layers {
		if l.Problem != "" {
			problems = append(problems, Result{Name: l.Pack, Path: d.Dir,
				Action: ActionRefused, Detail: l.Problem})
		}
		for _, msg := range l.Refusals {
			problems = append(problems, Result{Name: l.Pack, Path: d.Dir,
				Action: ActionRefused, Detail: msg})
		}
		unresolved = unresolved || l.Unresolved
	}
	out = append(out, problems...)
	// A destination with an UNRESOLVED layer is left entirely alone — not composed from the rest,
	// and not retired. The whole point of a wholesale render is that the destination's content is a
	// function of the pack set; if one member of that set could not be read, the function has no
	// value and writing a partial answer would archive every other pack's skills there because one
	// pack's `from` was misspelled. The refusal above is the report.
	//
	// This is where the cost of being wrong went UP relative to the per-pack delivery: there, a
	// pack with a bad `from` merely delivered nothing of its own (sourceList's reason).
	if unresolved {
		return out, nil
	}

	// The destination dir is NOT created here. Each layer's delivery creates it if it has content,
	// which is what keeps "a pack that ships no skills leaves NO trace" true — and it has to stay
	// true, because all six shipped agent packs declare a `skills` contribution purely to NAME the
	// destination other packs merge into. Creating it here would put an empty ~/.claude/skills in
	// every home that selects one.
	want := map[string]string{}
	for _, l := range d.Layers {
		res, err := writeLayer(d.Dir, l, req, preOwned, want, observe)
		out = append(out, res...)
		if err != nil {
			return out, err
		}
	}
	if !req.PackSetComplete {
		// A pack the config still names but that did not resolve contributes no layer, so its
		// skills look like content no pack composes any more — while the AGENT pack that names this
		// destination is still here, so the destination IS composed and the retire would run. See
		// ComposeRequest.PackSetComplete.
		return out, nil
	}
	return append(out, retireComposed(d.Dir, want, req, observe)...), nil
}

// writeLayer writes one pack's contribution into the destination, at the tier it declared, and
// records every path it claimed in `want` so the retire below can tell the composition from the
// residue of an older one.
func writeLayer(dir string, l Layer, req ComposeRequest, preOwned map[string]bool, want map[string]string,
	observe bool) ([]Result, error) {
	pluginDirs := make([]string, 0, len(l.Plugins))
	for _, pl := range l.Plugins {
		pluginDirs = append(pluginDirs, pl.Dir)
	}
	var out []Result
	for _, pl := range l.Plugins {
		res, err := DeliverPlugin(PluginRequest{
			Pack: l.Pack, Plugin: pl, SkillsDir: dir, Tier: l.Tier,
			Composed: req.Composed, Legacy: req.Legacy, PreOwned: preOwned, Claimed: want,
			ArchiveRoot: req.ArchiveRoot, Stamp: req.Stamp, Observe: observe,
		})
		out = append(out, res...)
		if err != nil {
			return out, err
		}
	}
	if len(l.Sources) == 0 {
		return out, nil
	}
	res, err := Deliver(Request{
		Pack: l.Pack, Description: l.Description, Sources: l.Sources, SkipSources: pluginDirs,
		SkillsDir: dir, Tier: l.Tier,
		Composed: req.Composed, Legacy: req.Legacy, PreOwned: preOwned, Claimed: want,
		ArchiveRoot: req.ArchiveRoot, Stamp: req.Stamp, Observe: observe,
	})
	return append(out, res...), err
}

// retireComposed archives every path the composed record still names under dir that this
// composition no longer ships — but ONLY where the recorded composer is a pack the user still has.
//
// ARCHIVED, never deleted, and UNCONFIRMED: every byte being moved is a byte yolo composed, so there
// is no user content to ask about (the same asymmetry PruneHostBriefings carries), and being wrong
// costs one `mv` back.
//
// THE `stillConfigured` GATE IS WHERE THIS STOPS AND RULING R1 STARTS, and the boundary is not an
// implementation detail — it is the difference between two user actions with two different answers:
//
//   - "the pack I still have stopped shipping this skill" → yolo's own upkeep of a directory it
//     composes. Nothing the user did, nothing to ask about, so it happens silently and is reported.
//   - "I removed a pack from `packs`" → R1: *the user's mental model is "I edited a config list" and
//     the consequence is "files left my real home"*, which is far enough apart that the action has
//     to be named at the moment it happens. That retire rides pruneDroppedPackOutput's confirmation.
//
// Found by running the lifecycle: without the gate this pass reached a dropped pack's skills FIRST
// and archived them unconfirmed, so R1's [y/N] never fired for `skills` at all — the gate was intact,
// the code had simply stopped arriving at it. It applies on BOTH retire paths, not only the prune,
// because a destination the render still visits can hold a dropped pack's entries too: the agent pack
// naming the dir stays while the content pack leaves.
func retireComposed(dir string, want map[string]string, req ComposeRequest, observe bool) []Result {
	if req.Composed == nil {
		return nil
	}
	var out []Result
	for _, path := range req.Composed.EntriesUnder(dir) {
		if _, composed := want[path]; composed {
			continue
		}
		if owner, _ := req.Composed.Owner(path); !req.Configured[owner] {
			continue // R1's, not ours — see above
		}
		if _, err := os.Lstat(path); err != nil {
			// The record outlived the path. Bookkeeping only: forget it so the next apply does not
			// report a phantom removal.
			if !observe {
				req.Composed.Forget(path)
			}
			continue
		}
		r := Result{Name: filepath.Base(path), Path: path, Action: archivedAction(observe),
			Detail: "no pack composes it here any more"}
		if target := danglingLink(path); target != "" {
			// A retiring entry that has become a dangling link is CLEARED, not archived: renaming
			// a broken link into the archive would report "moved to <path>" as though the content
			// were recoverable there, when there is none.
			r.Action = clearedAction(observe)
			r.Detail = "was → " + target + ", which no longer exists — no pack composes it here " +
				"any more and there is nothing to archive"
			if !observe {
				if err := clearLinks([]clearedLink{{Path: path, Target: target}}); err != nil {
					r.Action, r.Detail = ActionRefused, err.Error()
				} else {
					req.Composed.Forget(path)
				}
			}
			out = append(out, r)
			continue
		}
		if !observe {
			at, err := Archive(req.ArchiveRoot, req.Stamp, "skills", path)
			if err != nil {
				r.Action, r.Detail = ActionRefused, err.Error()
			} else {
				// Forget only AFTER the move: a record dropped for a path still in the home would
				// make the next apply read it as the user's own, which is the one state yolo can
				// never clean up from.
				req.Composed.Forget(path)
				r.Detail += " → " + at
			}
		} else {
			r.Detail += " (would move to the archive)"
		}
		out = append(out, r)
	}
	return out
}

// PruneHostSkills retires the composed output at a destination NO active pack contributes skills
// to any more — the orphan case: generated content left behind with nobody to regenerate it.
//
// It is a separate entry from RenderHostSkills for PruneHostBriefings' reason: a pack DROPPED from
// config never appears in the render loop, so the only way its destination is visited at all is a
// pass that knows the destinations independently of the active set. candidates supplies the packs
// whose destinations to look at (every pack yolo can resolve — embedded plus configured), active
// names the packs whose destinations are legitimate.
//
// A nil active set is REFUSED rather than read as "nothing is active": that reading would retire
// every composed destination on a caller bug, which is the one outcome this file exists to
// prevent. An empty non-nil map is the honest "no packs configured".
//
// req.PackSetComplete is the second guard, for the offline-remote case — see its field comment.
//
// req.Configured is the third: it retires only what a pack the user STILL HAS composed, leaving a
// DROPPED pack's content to ruling R1's confirmed retire (retireComposed's gate comment). The two
// cases do overlap — dropping the only pack that named a destination is both — and R1 wins the
// overlap, which is the direction that cannot surprise anyone.
func PruneHostSkills(candidates []*packload.Pack, active map[string]bool, homeDir string,
	req ComposeRequest, observe bool) ([]Result, error) {
	if active == nil {
		return nil, fmt.Errorf("host skills prune: refusing to prune with an unknown active pack set")
	}
	if !req.PackSetComplete {
		return nil, nil
	}
	var activePacks []*packload.Pack
	for _, p := range candidates {
		if p != nil && active[p.Name] {
			activePacks = append(activePacks, p)
		}
	}
	live := map[string]bool{}
	for _, d := range ComposeHostSkills(activePacks, homeDir) {
		live[d.Dir] = true
	}
	var out []Result
	for _, d := range ComposeHostSkills(candidates, homeDir) {
		if live[d.Dir] {
			continue // still visited by the render, which retires its own residue
		}
		out = append(out, retireComposed(d.Dir, nil, req, observe)...)
	}
	return out, nil
}

// treeDigest hashes a skill's whole subtree — relative paths, entry kinds, file bytes and SYMLINK
// TARGETS — so "is this the same skill?" is answered by content rather than by name.
//
// Symlink targets are part of the identity rather than followed, because a dotfile-manager
// deployment is a tree of links: two agents' copies of one skill that link to the SAME source are
// the same skill, and two that link to different sources are not. Following them would make every
// such pair compare equal to whatever they happen to point at today.
func treeDigest(root string) (string, error) {
	h := sha256.New()
	var walk func(rel string) error
	walk = func(rel string) error {
		path := filepath.Join(root, rel)
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, rerr := os.Readlink(path)
			if rerr != nil {
				return rerr
			}
			fmt.Fprintf(h, "l %s %s\n", rel, target)
			return nil
		case fi.IsDir():
			entries, rerr := os.ReadDir(path)
			if rerr != nil {
				return rerr
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			sort.Strings(names) // ReadDir already sorts; stated so the digest cannot drift with it
			fmt.Fprintf(h, "d %s\n", rel)
			for _, name := range names {
				if werr := walk(filepath.Join(rel, name)); werr != nil {
					return werr
				}
			}
			return nil
		}
		f, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer f.Close()
		// The exec bit is part of the identity: a skill that ships a script the user made
		// executable is not the same skill as one that did not.
		fmt.Fprintf(h, "f %s %o\n", rel, fi.Mode().Perm()&0o111)
		_, cerr := io.Copy(h, f)
		return cerr
	}
	if err := walk("."); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// moveTree moves src to dst, creating dst's parent. A rename first (cheap, atomic within a
// filesystem), then copy+remove for the cross-device case — the local pack lives under the config
// dir and a skills dir under the agent's home, which need not be the same filesystem.
//
// The copy happens BEFORE the removal, always: the reverse order loses the user's skill if the
// copy fails, which is the one outcome a MIGRATION may never produce.
func moveTree(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}
