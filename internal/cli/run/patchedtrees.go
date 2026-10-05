package run

// patchedtrees.go is the launch's half of a PATCHED EXTENSION (docs/design/patched-extensions.md §6.1,
// §8.1, §9, §11): the tree arm beside the fork builds in their slot, the per-launch copy beside the
// pack tree, the read-only mount from that copy, YOLO_PATCHED_TREES, the launch's lines and an
// attach's.
//
// THE TERMS are the designs' (patched-forks.md and patched-extensions.md §1): a PATCHED EXTENSION is
// a `files` contribution with `source` and `patches`, keyed by its EXTENSION KEY `<pack>/<name>`; its
// BUILT TREE is what the sealed build leaves and the store admits; the GOOD BUILD is this machine's
// record of the last one it admitted; the OWNING AGENT PACK is the selected pack declaring the
// surface of the list entry that names the tree. A PER-LAUNCH COPY (coined here) is the copy of the
// good build's tree one fresh launch makes for its jail, beside its pack tree, and mounts read-only.
//
// # Where it runs, and what it never does
//
//   - At a FRESH LAUNCH, in the fork-build slot (run.go), below every attach site and under the launch
//     lock, through the injected act (Options.BuildTrees): the check, the advance, the copy.
//   - NEVER IN A CAPTURE OR BUILD JAIL (CapturesDir returning "", the fork slot's own switch), so a
//     tree's build jail, which selects the contributing pack, cannot start another.
//   - NEVER IN A JAIL: a nested launch delivers nothing, and its line names the host.
//   - BELOW APPLE CONTAINER'S READ-ONLY FLOOR it checks and builds nothing, as the fork slot does, and
//     still delivers a good build already on this machine: the mount is a per-launch copy, so a write
//     through an ignored `:ro` changes only that copy (§11).
//   - On macos-user it is never reached (the arm returns above the slot) and noteMacosUserTrees
//     says so.
//
// # The copy is beside the pack tree, never in it
//
// `<tree>.patched/<slug>`: never inside a pack's staged directory, whose content digest an attach
// compares (packtree.go), and never at the tree's top level, which the boot's fallback walk reads as
// packs. Its name carries a dot, which a tree's name never does, as the delivery record's does
// (forkhanded.go), and it goes with its tree (discardPackTree) or with the jail's whole AGENTS_DIR.

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// patchedCopiesSuffix names the directory beside a pack tree that holds its launch's per-launch copies.
const patchedCopiesSuffix = ".patched"

// patchedCopiesDir is tree's per-launch copies directory, "" for no tree.
func patchedCopiesDir(tree string) string {
	if tree == "" {
		return ""
	}
	return tree + patchedCopiesSuffix
}

// PatchedCopySlug is the directory name one extension key's per-launch copy takes under a copies
// directory: readable, and unique by a hash of the key, since a key holds a "/".
func PatchedCopySlug(key string) string {
	sum := sha256.Sum256([]byte(key))
	return strings.ReplaceAll(key, "/", "--") + "-" + hex.EncodeToString(sum[:])[:8]
}

// TreeBuildRequest is what a launch hands its tree arm (Options.BuildTrees).
type TreeBuildRequest struct {
	// Trees are the patched extensions to deliver (packload.PatchedTrees).
	Trees []packload.Fork
	// Platform is the jail's platform (containerJailPlatform), never the host's.
	Platform string
	// Runtime is the backend the jail runs on, for the lines that name what differs there.
	Runtime string
	// Workspace is the launch's workspace, whose launch.log a failed build's line names.
	Workspace string
	// Stdout and Stderr are the launch's own writers, teed into its launch.log.
	Stdout, Stderr io.Writer
	// Build is false where this launch may check and build nothing — below Apple Container's
	// read-only floor (BuildFloor says why) — and a good build already on this machine is delivered
	// all the same.
	Build      bool
	BuildFloor string
	// CopyRoot is the directory each tree's per-launch copy is made in, as <CopyRoot>/<PatchedCopySlug>.
	CopyRoot string
}

// TreeDelivery is the tree arm's answer for one patched extension this launch.
type TreeDelivery struct {
	// Dir is the per-launch copy to mount at `~/<into>`, "" when there is none.
	Dir string
	// Entry is the capture store entry the copy was made from.
	Entry string
	// Commit, Tag, Patches and Series are the handed build's inputs, as the lines name them.
	Commit, Tag string
	Patches     int
	Series      string
	// Reason is why there is no copy, naming what to do; "" when Dir is set.
	Reason string
}

// goodLabelOf is a build as lines name it: its tag and short commit, or the commit alone.
func goodLabelOf(commit, tag string) string {
	return packsrc.ListEntry{Commit: commit, Tag: tag}.Label()
}

// label names a delivered build as lines do: "<tag> (<commit>) + N patches".
func (d TreeDelivery) label() string {
	if d.Dir == "" {
		return ""
	}
	return goodLabelOf(d.Commit, d.Tag) + " + " + PatchCount(d.Patches)
}

// notePatchedTrees is every launch's block for its patched extensions, an attach's included, above
// the dispatch as the fork block is (noteForkPins): each one's line, read offline from its series
// and its check record with no git (patchedTreeLine), and the one lint, said once per launch, for a
// tree no list entry names (PPX-D10). Disclosures, so none has a quiet switch (OQ-RO3). It returns
// the trees, for the tree arm.
func (o *Options) notePatchedTrees(packs []*packload.Pack) []packload.Fork {
	if o.CapturesDir() == "" {
		return nil // a capture or build jail: the launch that started it said this block
	}
	trees := packload.PatchedTrees(packs)
	if len(trees) == 0 {
		return nil
	}
	out := o.pr(o.Stderr)
	out.print("[dim]Patched extensions this launch:[/dim]")
	for _, f := range trees {
		line, warn := patchedTreeLine(f)
		line = richtext.Escape(line)
		if warn {
			out.print("[yellow]  " + line + "[/yellow]")
		} else {
			out.print("[dim]  " + line + "[/dim]")
		}
	}
	for _, p := range packs {
		for _, w := range packload.LintPatchedTrees(p) {
			out.print("[yellow]Warning: " + richtext.Escape(w) + "[/yellow]")
		}
	}
	return trees
}

// patchedTreeLine is one patched extension's line in the launch's block, and whether it is a warning:
// "extension <key>: ~/<into>, a patched extension of <source> + N patches (series S), at <good
// build>", then the held suffix, or why nothing serves.
func patchedTreeLine(f packload.Fork) (string, bool) {
	head := f.Label() + ": ~/" + strings.TrimSuffix(f.Into, "/") + ", a patched extension of " + f.Source
	series, err := f.ReadSeries()
	if err != nil {
		return head + " — its patch series cannot be read: " + err.Error(), true
	}
	head += " + " + PatchCount(series.Len()) + " (series " + series.ShortDigest() + ")"
	if config.InJail() {
		return head + " — checked and built on the host", false
	}
	rec, err := patchedPacksStore().LoadCheckRecord(f.Key())
	if err != nil || rec.Good == nil {
		return head + " — no build of it on this machine yet", true
	}
	g := rec.Good
	line := head + ", at " + GoodBuildLabel(g)
	recipe := packdecl.TreeSourceRecipe(f.Source, f.Build, f.Produces, series.Digest)
	if g.Recipe != recipe {
		return line + " with another series or build recipe — a fresh launch builds the edited one", true
	}
	in, _, _, _ := f.CheckWant(series).Inputs()
	if why := HeldSuffix(f, rec, in, series.Digest, recipe); why != "" {
		return line + "; " + why, true
	}
	return line, false
}

// treeDeliveriesFor is THE TREE ARM, in the fork-build slot beside the fork builds: for every
// patched extension this launch carries, the per-launch copy its jail mounts, or why there is none.
// Nothing here can fail the launch (§9: "The jail launch itself is never refused").
func (o *Options) treeDeliveriesFor(rt string) map[string]TreeDelivery {
	if len(o.patchedTrees) == 0 || o.CapturesDir() == "" {
		return nil
	}
	out := map[string]TreeDelivery{}
	defer o.recordHandedTrees(out)
	if config.InJail() {
		for _, f := range o.patchedTrees {
			out[f.Key()] = TreeDelivery{Reason: f.Label() + " is a patched extension, whose upstream is checked and " +
				"whose series is replayed and built on the host — a launch from the host delivers it"}
		}
		return out
	}
	floor := o.roBindsUnsupported(rt) // parity: Honored — below Apple Container's read-only floor the tree arm checks and builds nothing and still copies a good build already on this machine (patched-extensions.md §11)
	if o.BuildTrees == nil {
		for _, f := range o.patchedTrees {
			out[f.Key()] = TreeDelivery{Reason: "this launch builds no patched extension"}
		}
		return out
	}
	req := TreeBuildRequest{Trees: o.patchedTrees, Platform: containerJailPlatform(), Runtime: rt,
		Workspace: o.Workspace, Stdout: o.Stdout, Stderr: o.Stderr, Build: floor == "", BuildFloor: floor,
		CopyRoot: patchedCopiesDir(o.packTree)}
	for key, d := range o.BuildTrees(req) {
		out[key] = d
	}
	for _, f := range o.patchedTrees {
		if _, ok := out[f.Key()]; !ok {
			out[f.Key()] = TreeDelivery{Reason: "the tree arm returned no answer for " + f.Label()}
		}
	}
	return out
}

// patchedTreeDirs is what packFilesTargets mounts for each patched extension: its per-launch copy,
// by extension key, for the trees this launch delivered one of.
func (o *Options) patchedTreeDirs() map[string]string {
	dirs := map[string]string{}
	for key, d := range o.treeDelivered {
		if d.Dir != "" {
			dirs[key] = d.Dir
		}
	}
	return dirs
}

// treesBuildHere reports whether a launch on rt builds patched extensions at all: the notches
// PPX-D18 does not exempt. A launch inside a jail builds none, and Apple Container below its
// read-only floor builds none (patched-extensions.md §9, §11).
func (o *Options) treesBuildHere(rt string) bool {
	return !config.InJail() && o.roBindsUnsupported(rt) == ""
}

// patchedTreesWire is YOLO_PATCHED_TREES (entrypoint.PatchedTreesEnv, PPX-D8): per extension key,
// where it is mounted and the build handed, or the reason there is none, its owning agent pack, and
// whether that pack's launchers stop (PPX-D18): nothing serves, at a notch that builds trees, and the
// owner's list entry reaches a jail.
func (o *Options) patchedTreesWire(rt string) map[string]entrypoint.TreeDelivery {
	if len(o.patchedTrees) == 0 || o.treeDelivered == nil {
		return nil
	}
	builds := o.treesBuildHere(rt)
	out := map[string]entrypoint.TreeDelivery{}
	for _, f := range o.patchedTrees {
		d := o.treeDelivered[f.Key()]
		w := entrypoint.TreeDelivery{Into: f.Into, Owner: f.Owner}
		if d.Dir != "" {
			w.Build, w.Label = d.Entry, d.label()
		} else {
			w.Reason = d.Reason
			w.Stop = builds && f.Owner != "" && f.ListedInJail
		}
		out[f.Key()] = w
	}
	return out
}

// noteTreeDeliveries is the fresh launch's line for each patched extension it mounts nothing for at
// a notch that builds none, said once (§9: "start pi with a line said once, in FP-D3's shape, naming
// YOLO_RUNTIME=podman"); at every other notch the advance's own lines, and the owner's launcher, say it.
func (o *Options) noteTreeDeliveries(rt string) {
	if o.treesBuildHere(rt) {
		return
	}
	keys := make([]string, 0, len(o.treeDelivered))
	for k := range o.treeDelivered {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	next := " A podman jail builds it: YOLO_RUNTIME=podman."
	if config.InJail() {
		next = "" // the reason names the host, which is the next step from inside a jail
	}
	for _, k := range keys {
		if d := o.treeDelivered[k]; d.Dir == "" {
			o.pr(o.Stderr).print("[yellow]Warning: extension " + richtext.Escape(k) + " is not mounted in this jail[/yellow] — " +
				richtext.Escape(d.Reason) + "; the agent starts without it." + next)
		}
	}
}

// noteMacosUserTrees is the macos-user launch's line for each patched extension (§11, FP-D3's
// shape): no sealed build runs on this backend, so the agent starts without it.
func (o *Options) noteMacosUserTrees() {
	for _, f := range o.patchedTrees {
		o.pr(o.Stderr).print("[yellow]Warning: " + richtext.Escape(f.Label()) + " is not delivered on macos-user[/yellow] — " +
			"its tree is built from source in a capture jail, which this backend has none of; the agent starts " +
			"without it. Run it on a container backend (YOLO_RUNTIME=podman).")
	}
}

// removeTreeCopies removes a pack tree's per-launch copies, which live and die with it.
func removeTreeCopies(tree string) {
	if dir := patchedCopiesDir(tree); dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// patchedTreeCopyDir is where the copy of extension key goes under copyRoot.
func patchedTreeCopyDir(copyRoot, key string) string {
	return filepath.Join(copyRoot, PatchedCopySlug(key))
}

// PatchedTreeCopyDir is patchedTreeCopyDir for the tree act (internal/cli).
func PatchedTreeCopyDir(copyRoot, key string) string { return patchedTreeCopyDir(copyRoot, key) }

// noteAttachTreeBuilds is an attach's line for each patched extension its jail was handed
// (docs/design/patched-extensions.md §8.1 step 4), read from the running jail's delivery record:
// the build it mounts, and, when this machine's good build has moved since the jail booted, that the
// next fresh launch, once this jail stops, mounts that one. Silent for a jail whose tree this attach
// could not find, and for one launched before patched extensions.
func (o *Options) noteAttachTreeBuilds(view attachPackView) {
	if view.unfound != "" || view.unreadable || view.staged.root == "" {
		return
	}
	handed, err := readHandedTrees(view.staged.root)
	if err != nil {
		o.pr(o.Stderr).printf("[yellow]Warning: could not read what this jail was handed for its patched extensions (%v)[/yellow]", err)
		return
	}
	keys := make([]string, 0, len(handed))
	for k := range handed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := o.pr(o.Stderr)
	for _, k := range keys {
		h := handed[k]
		if h.Entry == "" {
			out.printf("[yellow]this jail mounts no build of extension %s: %s[/yellow]", k, richtext.Escape(h.Reason))
			continue
		}
		line := "this jail mounts extension " + k + " at " + goodLabelOf(h.Commit, h.Tag) + " + " + PatchCount(h.Patches)
		if rec, err := patchedPacksStore().LoadCheckRecord(k); err == nil && rec.Good != nil && rec.Good.Entry != h.Entry {
			line += "; " + GoodBuildLabel(rec.Good) + " + " + PatchCount(rec.Good.Patches) + " is built, and the " +
				"next fresh launch, once this jail stops, mounts it"
		}
		out.printf("[dim]%s[/dim]", richtext.Escape(line))
	}
}
