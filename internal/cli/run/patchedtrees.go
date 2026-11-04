package run

// patchedtrees.go is the launch's half of a PATCHED EXTENSION (docs/reference/pi-extensions.md §6.1,
// §8.1, §9, §11): the tree arm beside the fork builds in their slot, the per-launch copy beside the
// pack tree, the read-only mount from that copy (on macos-user, the home overlay's copy of it),
// YOLO_PATCHED_TREES, the launch's lines and an attach's.
//
// THE TERMS are the designs' (patched-forks.md and pi-extensions.md §1): a PATCHED EXTENSION is
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
//   - ON macos-user it runs too, in the arm's own slot call (runForkBuildSlotAndRefusals), and builds
//     each tree for the sandbox's darwin platform (jailPlatform). The copy is made under a STABLE
//     root of the workspace's instead (macosUserTreeCopyRoot), keyed by its store entry, so an
//     unchanged relaunch copies nothing; and instead of a mount the home overlay copies it to `~/<into>`
//     (buildMacosHomeOverlayWith), where the Seatbelt profile write-protects it: this backend's `:ro`.
//     A dry run builds nothing and says what a launch would (dryRunTreeDeliveries).
//
// # The copy is beside the pack tree, never in it
//
// `<tree>.patched/<slug>`: never inside a pack's staged directory, whose content digest an attach
// compares (packtree.go), and never at the tree's top level, which the boot's fallback walk reads as
// packs. Its name carries a dot, which a tree's name never does, as the delivery record's does
// (forkhanded.go), and it goes with its tree (discardPackTree) or with the jail's whole AGENTS_DIR.
// On macos-user it is beside the pack trees instead, in paths.TreeCopyRoot, which outlives them.

import (
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
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
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
func PatchedCopySlug(key string) string { return paths.KeySlug(key) }

// TreeBuildRequest is what a launch hands its tree arm (Options.BuildTrees).
type TreeBuildRequest struct {
	// FreshUpdate opts patched keys into explicit check-after-registration mode.
	FreshUpdate bool
	// FreshBoundary and FreshBoundaryErr are keyed epoch/start boundaries captured before the pool starts.
	FreshBoundary    map[string]packsrc.CheckStart
	FreshBoundaryErr map[string]error
	FreshIdentity    map[string]string
	// Trees are the patched extensions to deliver (packload.PatchedTrees).
	Trees []packload.Fork
	// Platform is the jail's platform (jailPlatform: a container's linux, or the macos-user
	// sandbox's darwin), never the host's.
	Platform string
	// Runtime is the backend the jail runs on, for the lines that name what differs there.
	Runtime string
	// Workspace is the launch's workspace, whose launch.log a failed build's line names.
	Workspace string
	// Stdout and Stderr are the launch's own writers, teed into its launch.log, as the fork
	// builds' are (ForkBuildRequest.Stdout).
	Stdout, Stderr io.Writer
	// Progress is the rendering of this launch's stream, for each build's progress line.
	Progress progress.Config
	// Build is false where this launch may check and build nothing — below Apple Container's
	// read-only floor (BuildFloor says why) — and a good build already on this machine is delivered
	// all the same.
	Build      bool
	BuildFloor string
	// CopyRoot is the directory each tree's per-launch copy is made in, as <CopyRoot>/<PatchedCopySlug>.
	CopyRoot string
	// ReuseCopies makes CopyRoot a STABLE root rather than this launch's own: each copy is keyed by
	// the store entry and prefix it was made from, a later launch handed the same build is given
	// the copy as it is, and a copy no live launch holds is reaped (internal/cli's
	// reusableTreeCopy). Set on macos-user only, where the copy is read once, into the home
	// overlay, and never mounted (macosUserTreeCopyRoot).
	ReuseCopies bool
	// Interrupt is this launch's act interrupt, the fork builds' (ForkBuildRequest.Interrupt,
	// PF-D57): once a Ctrl-C has ended a wait in this launch's fork-build slot, each extension's
	// good build is copied with no check and no build.
	Interrupt *ActInterrupt
	// CachedGood are the extension keys YOLO_USE_CACHED_GOOD covers this launch (selectCachedGood):
	// each is handed the record's older good build, validated, as its per-launch copy, with no
	// advance.
	CachedGood []string
}

// TreeDelivery is the tree arm's answer for one patched extension this launch.
type TreeDelivery struct {
	// FreshFailure refuses explicit preparation even when a fallback or serving copy exists.
	FreshFailure string
	// FreshError retains typed host-only admission refusal evidence.
	FreshError error
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
	// Skipped says this launch skipped the extension (YOLO_SKIP_EXTENSIONS, skipextensions.go): no
	// advance, no copy, no mount, its entries left out of the owning agent's lists, and nothing is
	// missing — the missing-build refusal passes over it.
	Skipped bool
	// PatchFailure is host-only failure evidence carried beside the admitted directory.
	PatchFailure *packsrc.PatchFailure
	// Cause is the build's cause in plain words, when its act found one, which the launch's refusal
	// says once for every key that shares it (missingbuilds.go) and the jail's gate is handed.
	Cause *entrypoint.BuildCause
	// Unsaid says the build act said nothing of Reason, leaving it to the launch's refusal or warning
	// (missingbuilds.go), as entrypoint.ForkDelivery.Unsaid.
	Unsaid bool
	// Recovery is the hatch list the advance decided would get this launch running, as
	// entrypoint.ForkDelivery.Recovery.
	Recovery []patchrecovery.Hatch
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
	return WithPatches(goodLabelOf(d.Commit, d.Tag), d.Patches)
}

// notePatchedTrees is every launch's block for its patched extensions, an attach's included, above
// the dispatch as the fork block is (noteForkPins): each one's line, read offline from its series
// and its check record with no git (patchedTreeLine), and the one lint, said once per launch, for a
// tree no list entry names (PPX-D10). Disclosures, so none has a quiet switch (OQ-RO3). It returns
// the trees a jail is delivered (packload.Fork.DeliveredInJail, PPX-D35), for the tree arm: one whose
// list entry reaches the host alone is named in the block and built and mounted in no jail.
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
	return jailDeliveredTrees(trees)
}

// patchedTreeLine is one built tree's line in the launch's block, and whether it is a warning:
// "extension <key>: ~/<into>, a patched extension of <source> + N patches (series S), at <good
// build>" — or "an unmodified extension of <source>" with no series — then the held suffix, or why
// nothing serves.
func patchedTreeLine(f packload.Fork) (string, bool) {
	what := ", a patched extension of "
	if f.Unmodified() {
		what = ", an unmodified extension of "
	}
	head := f.Label() + ": ~/" + strings.TrimSuffix(f.Into, "/") + what + f.Source
	series, err := f.ReadSeries()
	if err == nil && series.Unresolved() {
		return head + " + the patch branch " + f.PatchBranch + " — not fetched on this machine yet; the next fresh launch " +
			"fetches and builds it, or `yolo pack update` fetches it now", true
	}
	if err != nil {
		return head + " — its patch series cannot be read: " + err.Error(), true
	}
	if series.Replays() {
		head += " + " + series.Describe()
	}
	if !f.DeliveredInJail() {
		if !hostBuildsOwnTrees() {
			// A HOST THAT BUILDS NO TREE for its own render (hostBuildsOwnTrees: neither Linux nor a macOS
			// host's own render), so the guarded entry reaches no notch that has the tree (PPX-D38): a
			// warning, with the step that works.
			return head + " — " + packload.NotDeliveredAnywhereNote, true
		}
		return head + " — " + packload.NotDeliveredInJailNote, false
	}
	if config.InJail() {
		return head + " — checked and built on the host", false
	}
	rec, err := LoadPatchedRecord(patchedPacksStore(), f, series)
	if err != nil || rec.Good == nil {
		return head + " — no build of it on this machine yet", true
	}
	g := rec.Good
	line := head + ", at " + GoodBuildLabel(g)
	recipe := packdecl.TreeSourceRecipe(f.Source, f.Build, f.Produces, series.Digest)
	if g.Recipe != recipe {
		what := "series or build recipe"
		if f.Unmodified() {
			what = "build recipe"
		}
		return line + " with another " + what + " — a fresh launch builds the edited one", true
	}
	in, _, _, _ := f.CheckWant(series).Inputs()
	if why := HeldSuffix(f, rec, in, series.Digest, recipe); why != "" {
		return line + "; " + why, true
	}
	return line, false
}

// inJailTreeReason is why a nested launch hands f no tree: its build is the host's. An unmodified
// extension has no series to replay, so its reason names none (XB-D49).
func inJailTreeReason(f packload.Fork) string {
	if f.Unmodified() {
		return f.Label() + " is an unmodified extension, whose upstream is checked and built on the host — " +
			"a launch from the host delivers it"
	}
	return f.Label() + " is a patched extension, whose upstream is checked and whose series is replayed " +
		"and built on the host — a launch from the host delivers it"
}

// treeDeliveriesFor is THE TREE ARM, in the fork-build slot beside the fork builds: for every
// patched extension this launch carries, the per-launch copy its jail mounts, or why there is none.
// Nothing here fails the launch; with nothing to serve a needed tree, the launch refuses right
// after this slot (missingbuilds.go, pi-extensions.md PPX-D40).
func (o *Options) treeDeliveriesFor(rt string) map[string]TreeDelivery {
	out, req := o.treeBuildPlan(rt)
	if out == nil {
		return nil
	}
	defer o.recordHandedTrees(out)
	if req == nil {
		return out
	}
	if o.BuildTrees == nil {
		_, answers := o.BuildSlot(BuildSlotRequest{Trees: req, MaxBuilds: SlotBuildJails(rt)})
		return o.finishTreeDeliveries(out, answers)
	}
	return o.finishTreeDeliveries(out, o.BuildTrees(*req))
}

// treeBuildPlan is the tree arm's first half (treeDeliveriesFor): every extension's reason it has
// no copy to wait for, in out, and the request for the act, nil for none. out is nil when this
// launch decides nothing for any extension (none, or a capture or build jail).
func (o *Options) treeBuildPlan(rt string) (map[string]TreeDelivery, *TreeBuildRequest) {
	if len(o.patchedTrees) == 0 || o.CapturesDir() == "" {
		return nil, nil
	}
	out := map[string]TreeDelivery{}
	if config.InJail() {
		o.noteSkipExtensionsHasNoEffect("in a jail")
		for _, f := range o.patchedTrees {
			out[f.Key()] = TreeDelivery{Reason: inJailTreeReason(f)}
		}
		return out, nil
	}
	// SKIPPED EXTENSIONS (YOLO_SKIP_EXTENSIONS): answered here, with no advance, no copy and no mount.
	trees := o.skipExtensions(out)
	floor := o.roBindsUnsupported(rt) // parity: Honored — below Apple Container's read-only floor the tree arm checks and builds nothing and still copies a good build already on this machine (pi-extensions.md §11)
	if o.BuildTrees == nil && o.BuildSlot == nil {
		for _, f := range trees {
			out[f.Key()] = TreeDelivery{Reason: "this launch builds no extension"}
		}
		return out, nil
	}
	if len(trees) == 0 {
		return out, nil
	}
	copyRoot, reuse := patchedCopiesDir(o.packTree), false
	if rt == "macos-user" { // parity: Honored — only macos-user reads a copy once, into its home overlay, rather than mounting it for the jail's life
		copyRoot, reuse = macosUserTreeCopyRoot(o.packTree), true
	}
	return out, &TreeBuildRequest{FreshUpdate: o.Update && !o.DryRun, Trees: trees, Platform: jailPlatform(rt), Runtime: rt,
		Workspace: o.Workspace, Stdout: o.Stdout, Stderr: o.Stderr, Progress: o.progressConfig(),
		Build: floor == "", BuildFloor: floor,
		CopyRoot: copyRoot, ReuseCopies: reuse, Interrupt: o.actInterrupt(), CachedGood: o.cachedGoodTrees}
}

// macosUserTreeCopyRoot is where a macos-user launch's patched-extension copies live: a STABLE
// directory of the workspace's (AGENTS_DIR/<cname>/tree-copies, paths.TreeCopyRoot), beside the pack
// trees rather than inside this launch's, so the next launch handed the same build finds its copy
// and copies nothing (TreeBuildRequest.ReuseCopies). A container mounts its copy for the jail's
// whole life, so it keeps one per launch, removed with the tree; macos-user reads it once, into the
// home overlay, whose own reuse keys on the copy's store entry rather than its path. "" when this
// launch staged no pack tree.
func macosUserTreeCopyRoot(packTree string) string {
	if packTree == "" {
		return ""
	}
	return paths.TreeCopyRootFor(packTree)
}

// finishTreeDeliveries is the tree arm's second half: the act's answers merged into out, and a
// reason for any extension it did not answer.
func (o *Options) finishTreeDeliveries(out, answers map[string]TreeDelivery) map[string]TreeDelivery {
	for key, d := range answers {
		out[key] = d
	}
	for _, f := range o.patchedTrees {
		if _, ok := out[f.Key()]; !ok {
			out[f.Key()] = TreeDelivery{Reason: "the tree arm returned no answer for " + f.Label()}
		}
	}
	return out
}

// patchedTreeDirs is what packFilesTargets delivers for each patched extension: its copy, by
// extension key, for the trees this launch delivered one of — a per-launch copy mounted on a
// container, a per-build one copied into the home overlay on macos-user (macosUserTreeCopyRoot). A tree with no copy (skipped, or nothing serves) has no key, and
// so no destination.
func (o *Options) patchedTreeDirs() map[string]string {
	dirs := map[string]string{}
	for key, d := range o.treeDelivered {
		if d.Dir != "" {
			dirs[key] = d.Dir
		}
	}
	return dirs
}

// patchedTreeIdentities names, by extension key, the content of each copy
// patchedTreeDirs delivers: the content-addressed capture store entry it was copied from and the
// prefix copied out of it (internal/cli's copyTreeForLaunch), which together decide every byte of the copy. The
// macos-user home overlay's digest reads this instead of the copy's bytes
// (buildMacosHomeOverlayDelivering). A delivery with no entry is left out, and its copy is hashed.
func (o *Options) patchedTreeIdentities() map[string]string {
	ids := map[string]string{}
	for _, f := range o.patchedTrees {
		d := o.treeDelivered[f.Key()]
		if d.Dir != "" && d.Entry != "" {
			ids[f.Key()] = "capture-entry:" + d.Entry + ";prefix:" + packdecl.TreeReservedDir(f.Bin)
		}
	}
	return ids
}

// treesBuildHere reports whether a launch on rt builds patched extensions at all: the notches
// PPX-D18 does not exempt. A launch inside a jail builds none, and Apple Container below its
// read-only floor builds none (pi-extensions.md §9, §11).
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
		switch {
		case d.Skipped:
			// SKIPPED (YOLO_SKIP_EXTENSIONS): the boot leaves its entries out of the lists and stops
			// nothing for it.
			w.Reason, w.Skipped = d.Reason, true
		case d.Dir != "":
			w.Build, w.Label = d.Entry, d.label()
		default:
			w.Reason, w.Cause = d.Reason, d.Cause
			// A FALLBACK NEVER STOPS the agent (XB-D7): the jail's boot puts its raw entry in the
			// list in the tree's place (entrypoint.withTreeFallbacks), and the agent installs it.
			w.Stop = builds && f.Owner != "" && f.ListedInJail && f.Fallback == ""
		}
		out[f.Key()] = w
	}
	return out
}

// noteTreeDeliveries is the fresh launch's line for each built tree it mounts nothing for: at a notch
// that builds none, said once (§9: "start pi with a line said once, in FP-D3's shape, naming
// yolo --setup podman-self -- <command>"); and, at every notch, each FALLBACK taken, with its reason and what the agent
// installs in the tree's place (docs/design/pi-extension-store-builds.md XB-D7). Elsewhere the
// advance's own lines, and the owner's launcher, say it.
func (o *Options) noteTreeDeliveries(rt string) {
	keys := make([]string, 0, len(o.treeDelivered))
	for k := range o.treeDelivered {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fallbacks := map[string]string{}
	for _, f := range o.patchedTrees {
		fallbacks[f.Key()] = f.Fallback
	}
	builds := o.treesBuildHere(rt)
	next := " A podman jail builds it: yolo --setup podman-self -- <command>."
	if config.InJail() {
		next = "" // the reason names the host, which is the next step from inside a jail
	}
	for _, k := range keys {
		d := o.treeDelivered[k]
		if d.Dir != "" || d.Skipped {
			continue // a skipped one's line was said where it was skipped (skipExtensions)
		}
		if fb := fallbacks[k]; fb != "" {
			o.pr(o.Stderr).print("[dim]" + richtext.Escape(FallbackLine(k, d.Reason, fb)) + "[/dim]")
			if d.Cause != nil {
				// THE BUILD'S CAUSE, ONCE, under the one line that says what replaced it: the launch's
				// refusal of a missing build passes over a key with a fallback (missingbuilds.go).
				for _, l := range d.Cause.Lines {
					o.pr(o.Stderr).print("[dim]    " + richtext.Escape(l) + "[/dim]")
				}
			}
			continue
		}
		if !builds {
			o.pr(o.Stderr).print("[yellow]Warning: extension " + richtext.Escape(k) + " is not mounted in this jail[/yellow] — " +
				richtext.Escape(d.Reason) + "; the agent starts without it." + next)
		}
	}
}

// FallbackLine is the line a launch prints for the fallback it took for extension key: why no tree
// is mounted, and what the agent installs itself instead (XB-D7).
func FallbackLine(key, reason, fallback string) string {
	if reason == "" {
		reason = "this launch hands it no tree"
	}
	return "extension " + key + ": no tree is mounted in this jail — " + strings.TrimSuffix(reason, ".") +
		"; the agent installs " + fallback + " itself, in this workspace, from its raw entry"
}

// dryRunTreeDeliveries is a macos-user DRY RUN's tree decisions, which build nothing, as
// dryRunForkDeliveries is its forks': a skipped extension is skipped as a launch would skip it
// (skipExtensions says so), and every other is said to be built for macOS by a launch without
// --dry-run. The reasons are this launch's wire, so the plan shows what the sandbox would be handed.
func (o *Options) dryRunTreeDeliveries() {
	if len(o.patchedTrees) == 0 || o.CapturesDir() == "" || config.InJail() {
		return
	}
	out := map[string]TreeDelivery{}
	for _, f := range o.skipExtensions(out) {
		why := "a dry run builds no extension; a launch without --dry-run builds " + f.Label() + " for macOS"
		out[f.Key()] = TreeDelivery{Reason: why}
		o.pr(o.Stderr).print("[dim]" + richtext.Escape(why) + "[/dim]")
	}
	o.treeDelivered = out
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
// (docs/reference/pi-extensions.md §8.1 step 4), from the running jail's delivery record
// (noteAttachHandedBuilds reads it): the build it mounts, and, when this machine's good build has
// moved since the jail booted, that the next fresh launch, once this jail stops, mounts that one.
// Silent for a jail launched before patched extensions, whose record has no trees.
func (o *Options) noteAttachTreeBuilds(handed map[string]HandedTree) {
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
		line := "this jail mounts extension " + k + " at " + WithPatches(goodLabelOf(h.Commit, h.Tag), h.Patches)
		if rec, err := patchedPacksStore().LoadCheckRecord(k); err == nil && rec.Good != nil && rec.Good.Entry != h.Entry {
			line += "; " + WithGood(GoodBuildLabel(rec.Good), rec.Good) + " is built, and the " +
				"next fresh launch, once this jail stops, mounts it"
		}
		out.printf("[dim]%s[/dim]", richtext.Escape(line))
	}
}
