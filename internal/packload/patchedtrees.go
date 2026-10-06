package packload

// patchedtrees.go is the pack-set half of a PATCHED EXTENSION (docs/design/patched-extensions.md):
// which patched extensions a selection carries, the OWNING AGENT PACK of each, and the lint that
// says when no list entry names the tree.
//
// A PATCHED EXTENSION IS A Fork WITH AN Into. The patched-fork mode's check, replay, ratchet and
// explicit acts are keyed by an OWNER KEY (patched-forks.md PF-D22), `<pack>/<bin>` for a fork and
// `<pack>/<name>` for an extension, and one implementation serves both; so an extension reaches
// that implementation as the same value a patched fork does, with Bin its name (the last segment
// of `into`), Into its landing, and Owner in place of Base. Nothing that rewrites programs
// (ApplyForks, Forks) ever lists one, because it is not a `program`.
//
// THE OWNING AGENT PACK (PPX-D4) is the selected pack that declares the surface of the
// contributing pack's `config-list` or posture-list entry naming `~/<into>` or a path inside it
// (loadsTree, PPX-D36): the entry that makes the agent load the tree. Compared as a path, so core
// reads none of an agent's grammar. With no such entry there is none, and the agent never loads
// the tree, which LintPatchedTrees says.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// IsTree reports whether f is a PATCHED EXTENSION (a tree landing at Into), not a fork of a
// program.
func (f Fork) IsTree() bool { return f.Into != "" }

// Label is how a line names f: "fork <key>" for a fork, "extension <key>" for a patched extension.
// Every line of the shared implementation names its subject through here, so a fork's lines stay
// what they were and an extension's say what it is.
func (f Fork) Label() string {
	if f.IsTree() {
		return "extension " + f.Key()
	}
	return "fork " + f.Key()
}

// Thing is what a jail goes without when f has nothing to serve: the program's bin for a fork,
// "extension <key>" for a patched extension.
func (f Fork) Thing() string {
	if f.IsTree() {
		return "extension " + f.Key()
	}
	return f.Bin
}

// CaptureArg is what `yolo capture` takes to build f now: a fork's bin, an extension's key.
func (f Fork) CaptureArg() string {
	if f.IsTree() {
		return f.Key()
	}
	return f.Bin
}

// HoldPacks are the packs whose `agent_updates` holds f's upstream (PF-D19, PPX-D9): a fork's own
// pack and its base; a patched extension's contributing pack, its owning agent pack, and every
// fork of the owning agent's programs in the selection.
func (f Fork) HoldPacks() []string {
	if !f.IsTree() {
		return []string{f.Pack, f.Base}
	}
	out := []string{f.Pack}
	if f.Owner != "" {
		out = append(out, f.Owner)
	}
	return append(out, f.OwnerForks...)
}

// PatchedTrees lists every BUILT TREE the packs carry — each patched extension and each UNMODIFIED
// one (docs/design/pi-extension-store-builds.md §4.1, XB-D1), which is a patched extension with no
// series and goes through every reader of this list unchanged — in pack order and then declaration
// order, each with its owning agent pack and where the list entry naming it reaches.
//
// An unmodified extension's value carries what its declaration leaves to defaults, so no reader
// re-derives them: Follow is `head` for a git source that names none (XB-D2), and Build the line its
// tree is built with (packdecl.Contribution.TreeBuild: XB-D3's dependency install for a git source,
// npm's own install for an npm one).
func PatchedTrees(packs []*Pack) []Fork {
	var out []Fork
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if !c.IsBuiltTree() {
				continue
			}
			follow := c.Follow
			if c.IsUnmodifiedExtension() && follow == "" && !packsrc.IsNpmSource(c.Source) {
				follow = "head"
			}
			f := Fork{
				Pack: p.Name, Bin: c.ExtensionName(), Source: c.Source, Build: c.TreeBuild(),
				Produces: append([]string(nil), c.Produces...),
				Root:     p.Root, Patches: c.Patches, Follow: follow, Into: c.Into, Fallback: c.Fallback,
				PackBases: forkBaseClosure(packs, p),
			}
			f.Owner, f.ListedInJail, f.ListedAtHost = owningAgentPack(packs, p, c.TreeListEntry())
			if f.Owner != "" {
				for _, fk := range Forks(packs) {
					if fk.Base == f.Owner {
						f.OwnerForks = append(f.OwnerForks, fk.Pack)
					}
				}
			}
			out = append(out, f)
		}
	}
	return out
}

// ListEntry is the list entry f's agent loads its tree through (packdecl.TreeListEntry): `~/<into>`,
// or for an npm source the package inside the npm prefix the tree is. An entry loads the tree when
// it is this or a path inside it, cleaned (loadsTree, PPX-D36), so `~/<into>/` loads it too; the pi
// pack's subagents render still matches only the spelling with no slash, which the guide says.
func (f Fork) ListEntry() string { return packdecl.TreeListEntry(f.Source, f.Into) }

// owningAgentPack is PPX-D4: the selected pack declaring the surface of contributing's list entry
// naming the tree's entry (packdecl.TreeListEntry) or a path inside it (loadsTree) — a
// `config-list` (every notch) or a posture list (its posture's notches) — and whether an entry
// reaches a jail (a `config-list`, or the autonomous posture's) and the host (a `config-list`, or
// the guarded posture's). "" when no entry names the tree, or no selected pack declares the surface
// one names.
func owningAgentPack(packs []*Pack, contributing *Pack, want string) (owner string, inJail, atHost bool) {
	for _, l := range contributing.Decl.ListContributions() {
		if !listAdds(l.Add, want) {
			continue
		}
		key, err := manifest.ParseSurfaceID(l.Surface)
		if err != nil {
			continue
		}
		pack := surfaceOwner(packs, key)
		if pack == "" {
			continue
		}
		if owner == "" {
			owner = pack
		}
		if pack != owner {
			continue
		}
		switch l.Posture {
		case "":
			inJail, atHost = true, true
		case packdecl.PostureAutonomous:
			inJail = true
		case packdecl.PostureGuarded:
			atHost = true
		}
	}
	return owner, inJail, atHost
}

// listAdds reports whether a list body's `add` array holds a string that loads the tree whose own
// entry is want (loadsTree).
func listAdds(add json.RawMessage, want string) bool {
	var entries []any
	if err := json.Unmarshal(add, &entries); err != nil {
		return false
	}
	for _, e := range entries {
		if s, ok := e.(string); ok && loadsTree(s, want) {
			return true
		}
	}
	return false
}

// surfaceOwner is the selected pack that declares the surface key, "" when none does.
func surfaceOwner(packs []*Pack, key manifest.SurfaceKey) string {
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		surfaces, _ := p.Surfaces()
		for _, s := range surfaces {
			if s.Agent == key.Agent && s.Name == key.Name {
				return p.Name
			}
		}
	}
	return ""
}

// LintPatchedTrees is the one lint a patched extension gets at `yolo pack lint` and once at launch
// (PPX-D10): a warning, naming the line to add, for every patched extension of p that no list entry
// in p names, so the tree is built and mounted and no agent loads it. An entry names it when it is
// `~/<into>` or a path inside it (loadsTree): core reads none of an agent's package grammar.
func LintPatchedTrees(p *Pack) []string {
	if p == nil || p.Decl == nil {
		return nil
	}
	var out []string
	for _, c := range p.Decl.Contributions() {
		if !c.IsBuiltTree() || c.Into == "" {
			continue
		}
		entry := c.TreeListEntry()
		if listedAnywhere(p, entry) {
			continue
		}
		drop := "drop the extension's own git: entry in the same edit so it does not load twice"
		if c.IsUnmodifiedExtension() {
			drop = "drop the extension's own raw entry in the same edit so it does not load twice — " +
				"or keep that string as the contribution's \"fallback\", which takes its place where no " +
				"tree is handed"
		}
		out = append(out, fmt.Sprintf("pack %s: extension %s/%s is built and mounted at ~/%s, and no "+
			"list entry in the pack names it, so no agent loads it — add %q to the agent's packages "+
			"list (for pi, a \"config-list\" on \"pi/settings\" at \"/packages\"), and %s",
			p.Name, p.Name, c.ExtensionName(), strings.TrimSuffix(c.Into, "/"), entry, drop))
	}
	return out
}

// listedAnywhere reports whether any list body of p adds entry, whatever surface it names.
func listedAnywhere(p *Pack, entry string) bool {
	for _, l := range p.Decl.ListContributions() {
		if listAdds(l.Add, entry) {
			return true
		}
	}
	return false
}

// patchedTreeClaimDetailPrefix opens a PATCHED EXTENSION's footprint claim Detail, which
// Claim.DisclosureSentence keys its sentence on, in place of a plain tree's "read-only tree".
const patchedTreeClaimDetailPrefix = "patched extension build: "

// unmodifiedTreeClaimDetailPrefix opens an UNMODIFIED EXTENSION's claim Detail
// (docs/design/pi-extension-store-builds.md §4.1): an upstream's code built from source as a patched
// extension's is, with no series.
const unmodifiedTreeClaimDetailPrefix = "unmodified extension build: "

// IsPatchedExtension reports whether c is a PATCHED EXTENSION's claim (PPX-D15).
func (c Claim) IsPatchedExtension() bool {
	return c.Kind == packdecl.KindFiles && strings.HasPrefix(c.Detail, patchedTreeClaimDetailPrefix)
}

// IsBuiltTree reports whether c is a BUILT TREE's claim, patched or unmodified: the `files` claims
// that are review-worthy, which the launch's disclosure routes per claim, as it routes a wrapped
// plugin's code-running claim — an upstream's code, built from source, that the agent loading the
// tree runs.
func (c Claim) IsBuiltTree() bool {
	return c.IsPatchedExtension() ||
		c.Kind == packdecl.KindFiles && strings.HasPrefix(c.Detail, unmodifiedTreeClaimDetailPrefix)
}

// unmodifiedTreeClaimDetail is an unmodified extension's claim Detail: the source as written, the
// follow rule a git source takes, the build line its tree is built with, and the fallback, so two
// extensions that differ in any of them render as two lines.
func unmodifiedTreeClaimDetail(c packdecl.Contribution) string {
	detail := unmodifiedTreeClaimDetailPrefix + c.Source
	if !packsrc.IsNpmSource(c.Source) {
		rule := c.Follow
		if rule == "" {
			rule = "head"
		}
		detail += ", following " + rule
	}
	detail += ", built by `" + c.TreeBuild() + "`"
	if c.Fallback != "" {
		detail += ", falling back to " + c.Fallback + " where no tree is handed"
	}
	return detail
}

// patchedTreeClaimDetail is a patched extension's claim Detail (PPX-D15): the source as written
// (its ref included), the series — its directory, and its patch count and digest when root's series
// reads — the follow rule and the build line, so two extensions that differ in any of them render
// as two lines. The landing is the claim's Target.
func patchedTreeClaimDetail(root string, c packdecl.Contribution) string {
	rule := c.Follow
	if follow, err := packsrc.ParseFollow(c.Follow); err == nil {
		rule = follow.String()
	}
	series := "the series in " + c.Patches
	if s, err := packsrc.ReadSeries(root, c.Patches); err == nil {
		series = fmt.Sprintf("%d %s in %s (series %s)", s.Len(), plural(s.Len(), "patch", "patches"),
			c.Patches, s.ShortDigest())
	}
	build := "no build line"
	if strings.TrimSpace(c.Build) != "" {
		build = "built by `" + c.Build + "`"
	}
	return patchedTreeClaimDetailPrefix + c.Source + " + " + series + ", following " + rule + ", " + build
}
