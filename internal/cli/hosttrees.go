package cli

// hosttrees.go is a PATCHED EXTENSION at the host notch (docs/design/patched-extensions.md §8.3,
// §11; PPX-D11, PPX-D18): the check and the advance before the render, the render's arm for a
// tree, and the stop a host launch of the owning agent gets when nothing serves.
//
// # The render: a versioned copy and an owned link
//
// The `files` render copies file by file and skips a contribution with no `from`
// (entrypoint.RenderHostFiles), so a patched extension gets an arm of its own. The good build this
// machine admitted is copied — reflink or copy, never a hardlink (capture.CopyTree) — into a
// VERSIONED DIRECTORY (coined here: one directory per store entry) under paths.HostTreesDir, where
// no jail mounts it; `~/<into>` is a symbolic link the render owns, recorded in the `files`
// ownership record and swapped by writing a link beside it and renaming it over. The version the
// link named before the swap is kept until the next move, so a host agent already running keeps
// reading the tree it loaded; anything older is removed.
//
// # Where the advance runs, and where it never does
//
//   - `yolo host apply` (the acting posture): every patched extension's check and advance, before
//     the render (advanceHostTrees). A dry run checks nothing.
//   - `yolo host -- <bin>` under `host_apply_on_launch`: only the extensions whose owning agent
//     pack declares `<bin>` (a fork of its program keeps the bin), before the gate's comparison and
//     outside it — the comparison is the apply in observe posture, bounded at a second as a
//     stuck-detector, and a check alone may wait a minute on the network. So `yolo host -- claude`
//     runs no check for a pi extension.
//   - Inside the comparison, and at every other bin's launch, the arm only reads which build the
//     link names against the good build the record names.
//
// macOS hosts build no tree (the build is the jail's Linux platform): the render says so once,
// naming a jail that has the extension, and so does `yolo host -- <bin>` of the owning agent,
// which starts without it (noteHostTreeLines).
//
// # A revert
//
// `yolo host apply --revert` removes every link the `files` ownership record names into the
// versioned copies, and the copies with it (revertHostTreeLinks, PPX-D31).

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostTreesBuild reports whether this host builds patched extensions: a Linux host, outside a
// jail. A var so a test can stand a macOS host in.
var hostTreesBuild = func() bool { return goruntime.GOOS == "linux" && !config.InJail() }

// hostTreeAdvance runs a patched extension's advance at the host: a var so a test can count it.
var hostTreeAdvance = advancePatchedFork

// advanceHostTrees runs the check and the advance of every patched extension the host's selection
// carries — only those bin's owning agent pack runs, when bin is not "" — before any render reads
// them (PPX-D11). Lines go to errw, as every host launch line does. act is the verb's act interrupt
// (PF-D57), which the patched forks' advances after these read too.
func advanceHostTrees(errw io.Writer, color bool, bin string, act *run.ActInterrupt) {
	if !hostTreesBuild() {
		return
	}
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		return
	}
	for _, f := range packload.PatchedTrees(sel.packs) {
		if !f.DeliveredAtHost() || (bin != "" && !ownerRuns(sel.packs, f, bin)) {
			continue // no host render links a tree whose list entry reaches jails alone (PPX-D35)
		}
		hostTreeAdvance(f, advanceOptions{platform: captureJailPlatform(), out: errw, errw: errw, color: color,
			launch: true, host: true, act: act})
	}
}

// ownerRuns reports whether f's owning agent pack declares a program named bin — its own, or the
// one a fork of it delivers, which keeps the bin.
func ownerRuns(packs []*packload.Pack, f packload.Fork, bin string) bool {
	if f.Owner == "" {
		return false
	}
	for _, p := range packs {
		if p == nil || p.Name != f.Owner || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindProgram && c.Bin == bin {
				return true
			}
		}
	}
	return false
}

// hostTreeGate is PPX-D18 at `yolo host -- <bin>`, under any `host_management` but `none`: the launch of an owning agent stops when a tree
// it loads at the host — its list entry reaches the host — is not there to load: `~/<into>` names
// no directory, because nothing has built it or no apply has rendered it. It names the extension,
// the cause and the next step, and returns false to stop the launch. Silent where the host builds
// no tree (macOS, a jail), which is exempt.
func hostTreeGate(errw io.Writer, bin, home string) bool {
	// Under `host_management: none` yolo writes nothing into the home, so no link is ever there:
	// that contract delivers no tree at the host, and stops nothing for one.
	if !hostTreesBuild() || config.HostManagementMode() == config.HostManagementNone {
		return true
	}
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		return true
	}
	ok := true
	for _, f := range packload.PatchedTrees(sel.packs) {
		if !f.ListedAtHost || !ownerRuns(sel.packs, f, bin) {
			continue
		}
		dest := filepath.Join(home, filepath.FromSlash(strings.TrimSuffix(f.Into, "/")))
		if isDir(dest) {
			continue
		}
		_, _, why := hostTreeServing(f)
		if why == "" {
			why = "~/" + strings.TrimSuffix(f.Into, "/") + " names no build — `yolo host apply --assert` renders it"
		}
		if ok {
			fmt.Fprintf(errw, "yolo host: refusing to launch %s — a patched extension it loads is not here:\n", bin)
		}
		ok = false
		fmt.Fprintf(errw, "  ✗ %s (~/%s): %s\n", f.Label(), strings.TrimSuffix(f.Into, "/"), why)
	}
	if !ok {
		fmt.Fprintf(errw, "  So %s does not start rather than start without it; or drop the list entry naming it "+
			"to run %s without it.\n", bin, bin)
	}
	return ok
}

// hostTreeServing is the good build that serves f, read with no git: its store entry and its
// inputs, or why there is none.
func hostTreeServing(f packload.Fork) (*capture.Entry, *packsrc.GoodBuild, string) {
	a, early := newAdvance(f, advanceOptions{platform: captureJailPlatform(), out: io.Discard, errw: io.Discard, host: true})
	if early != nil {
		return nil, nil, early.delivery.Reason
	}
	if a.serving == nil {
		why := f.Label() + " has no build on this machine yet — `yolo host apply --assert` builds it"
		if g := a.rec.Good; g != nil && g.Recipe != a.recipe {
			why = f.Label() + "'s series or build recipe changed and the edit has no build yet — `yolo host apply " +
				"--assert` builds it"
		}
		return nil, nil, why
	}
	good := *a.rec.Good
	return a.serving, &good, ""
}

// hostTreeVersionsDir is where f's versioned copies live, one directory per store entry.
func hostTreeVersionsDir(f packload.Fork) string {
	return filepath.Join(paths.HostTreesDir(), run.PatchedCopySlug(f.Key()))
}

// renderHostTrees is the render's arm for p's patched extensions (PPX-D11). observe reads only:
// whether `~/<into>` already names the good build's versioned copy. A tree whose list entry reaches
// jails alone is linked at no host (PPX-D35), and a link to it an earlier render left is retired.
func renderHostTrees(p *packload.Pack, packs []*packload.Pack, home string, man *hostskills.Manifest,
	observe bool) []entrypoint.HostRenderResult {
	var out []entrypoint.HostRenderResult
	for _, f := range packload.PatchedTrees(packs) {
		if f.Pack != p.Name {
			continue
		}
		if !f.DeliveredAtHost() {
			if res, ok := retireHostTreeLink(f, home, man, observe); ok {
				out = append(out, res)
			}
			continue
		}
		out = append(out, renderHostTree(f, home, man, observe))
	}
	return out
}

// retireHostTreeLink is the render's arm for a tree no host loads (PPX-D35): the link at `~/<into>`
// an earlier render left, when the files ownership record says it is f's pack's and it points into
// f's versioned copies, is removed with those copies and forgotten, as a revert removes it
// (PPX-D31). ok is false, and nothing is touched, when there is no such link.
func retireHostTreeLink(f packload.Fork, home string, man *hostskills.Manifest, observe bool) (
	entrypoint.HostRenderResult, bool) {
	dest := filepath.Join(home, filepath.FromSlash(strings.TrimSuffix(f.Into, "/")))
	target, err := os.Readlink(dest)
	if err != nil || man == nil || !man.OwnedBy(dest, f.Pack) || filepath.Dir(target) != hostTreeVersionsDir(f) {
		return entrypoint.HostRenderResult{}, false
	}
	why := "the list entry that loads " + f.Label() + " reaches jails alone"
	res := entrypoint.HostRenderResult{Surface: f.Pack + "/files", Path: dest, WouldChange: true}
	if observe {
		res.Action = "would remove (" + why + ")"
		return res, true
	}
	if err := os.Remove(dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
		res.Action, res.WouldChange = "refused: "+err.Error(), false
		return res, true
	}
	man.Forget(dest)
	_ = os.RemoveAll(hostTreeVersionsDir(f))
	res.Action = "removed (" + why + ")"
	return res, true
}

// renderHostTree is one patched extension's render.
func renderHostTree(f packload.Fork, home string, man *hostskills.Manifest, observe bool) entrypoint.HostRenderResult {
	dest := filepath.Join(home, filepath.FromSlash(strings.TrimSuffix(f.Into, "/")))
	res := entrypoint.HostRenderResult{Surface: f.Pack + "/files", Path: dest}
	if !hostTreesBuild() {
		res.Action = fmt.Sprintf("refused: %s is built for a Linux jail, and this host builds no tree — run its "+
			"agent in a jail that has it: YOLO_RUNTIME=podman yolo -- %s", f.Label(), ownerBinFor(f))
		return res
	}
	entry, _, why := hostTreeServing(f)
	if entry == nil {
		res.Action = "refused: " + why
		return res
	}
	version := filepath.Join(hostTreeVersionsDir(f), entry.Key)
	info, lerr := os.Lstat(dest)
	occupied := lerr == nil
	owned := man != nil && man.OwnedBy(dest, f.Pack)
	var previous string
	switch {
	case occupied && !owned:
		res.Action = "refused: exists and yolo has no record of writing it — left untouched"
		if man != nil {
			if owner, recorded := man.Owner(dest); recorded {
				res.Action = fmt.Sprintf("refused: belongs to pack %q — left untouched", owner)
			}
		}
		return res
	case occupied && info.Mode()&os.ModeSymlink == 0:
		res.Action = "refused: yolo's record names it, and it is not the link a patched extension renders — move it " +
			"aside and apply again"
		return res
	case occupied:
		previous, _ = os.Readlink(dest)
		if previous == version && isDir(version) {
			res.Action = "unchanged"
			return res
		}
	}
	res.WouldChange = true
	if observe {
		res.Action = "would render (→ " + version + ")"
		return res
	}
	if err := materializeHostTree(f, entry, version); err != nil {
		res.Action, res.WouldChange = "refused: "+err.Error(), false
		return res
	}
	if err := swapLink(dest, version); err != nil {
		res.Action, res.WouldChange = "refused: "+err.Error(), false
		return res
	}
	if man != nil {
		man.Record(dest, f.Pack)
	}
	pruneHostTreeVersions(hostTreeVersionsDir(f), version, previous)
	res.Action = "rendered (→ " + version + ")"
	return res
}

// ownerBinFor is the program a line names to run the owning agent with: its owning agent pack's
// first program, or "<agent>" with none.
func ownerBinFor(f packload.Fork) string {
	sel := selectConfiguredHostPacks()
	for _, p := range sel.packs {
		if p == nil || p.Name != f.Owner || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindProgram && c.Bin != "" {
				return c.Bin
			}
		}
	}
	return "<agent>"
}

// materializeHostTree copies entry's tree into version, whole or not at all: into a temporary
// directory beside it, checked against the entry's completion marker, then renamed into place.
func materializeHostTree(f packload.Fork, entry *capture.Entry, version string) error {
	if isDir(version) {
		return nil
	}
	if err := os.MkdirAll(paths.HostTreesDir(), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(paths.HostTreesDir(), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(version), 0o700); err != nil {
		return err
	}
	tmp := version + ".tmp-" + fmt.Sprint(os.Getpid())
	_ = os.RemoveAll(tmp)
	store := &capture.Store{Dir: paths.CapturesDir()}
	_, err := capture.CopyTree(capture.CopyTreeOptions{Entry: entry, Prefix: packdecl.TreeReservedDir(f.Bin), Dest: tmp})
	treeCopied(entry.Key) // the seam a jail launch's copy has, where a test reaps the entry as a move would
	if _, rerr := store.Resolve(entry.Key); rerr != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("%s's build %s was reaped while it was copied — apply again", f.Label(), entry.Key)
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("copying %s's build: %w", f.Label(), err)
	}
	if err := os.Rename(tmp, version); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

// swapLink points dest at target by writing a link beside it and renaming it over, so a reader
// sees the old link or the new one and never neither.
func swapLink(dest, target string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".yolo-link-" + fmt.Sprint(os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// pruneHostTreeVersions removes every versioned copy under dir but keep and previous: the version
// the link names now, and the one it named before the move, which a host agent already running
// may still be reading.
func pruneHostTreeVersions(dir, keep, previous string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if p == keep || p == previous {
			continue
		}
		_ = os.RemoveAll(p)
	}
}

// noteHostTreeLines prints a line for each patched extension the launch's agent loads at the host
// (its list entry reaches the host) — a disclosure (OQ-RO3), as a jail launch's block is (PPX-D26).
// It names the build `~/<into>` links to, which is what the agent loads, and, when this machine's
// good build has moved past it with no render since, that `yolo host apply --assert` renders the good
// one. On a host that builds no tree (macOS) it says the agent starts without it and names a jail that
// has it (§9, §11). Silent under `host_management: none`, which writes no link, and in a jail.
func noteHostTreeLines(errw io.Writer, color bool, bin, home string) {
	if config.InJail() || config.HostManagementMode() == config.HostManagementNone {
		return
	}
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		return
	}
	pr := richtext.Printer{W: errw, Color: color}
	for _, f := range packload.PatchedTrees(sel.packs) {
		if !f.ListedAtHost || !ownerRuns(sel.packs, f, bin) {
			continue
		}
		if !hostTreesBuild() {
			pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("yolo host: %s is not delivered on this host — "+
				"its tree is built for a Linux jail, and a macOS host builds none; %s starts without it. "+
				"YOLO_RUNTIME=podman yolo -- %s runs it in a jail that has it", f.Label(), bin, bin)))
			continue
		}
		if line := hostTreeLine(f, home); line != "" {
			pr.Printf("[dim]%s[/dim]", richtext.Escape("yolo host: "+line))
		}
	}
}

// hostTreeLine is f's line at a host launch, read from the link the render owns: "" when
// `~/<into>` is not a link into f's versioned copies (the stop has already said so when nothing is
// there, and a path the user owns is the user's).
func hostTreeLine(f packload.Fork, home string) string {
	dest := filepath.Join(home, filepath.FromSlash(strings.TrimSuffix(f.Into, "/")))
	target, err := os.Readlink(dest)
	if err != nil || filepath.Dir(target) != hostTreeVersionsDir(f) {
		return ""
	}
	linked := filepath.Base(target)
	entry, g, why := hostTreeServing(f)
	switch {
	case entry != nil && entry.Key == linked:
		return f.Label() + " at " + run.GoodBuildLabel(g) + " + " + run.PatchCount(g.Patches)
	case entry != nil:
		return f.Label() + " at " + linkedTreeLabel(linked) + "; " + run.GoodBuildLabel(g) + " + " +
			run.PatchCount(g.Patches) + " is built, and `yolo host apply --assert` renders it"
	default:
		return f.Label() + " at " + linkedTreeLabel(linked) + "; " + why
	}
}

// linkedTreeLabel names the build a host link names by its store entry key: its tag and commit from
// the entry's receipt while the store still holds it, else "the build <key>" — the host keeps its
// own copy of a build a move has reaped from the store.
func linkedTreeLabel(key string) string {
	if e, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(key); err == nil {
		if recs, err := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(e.Root)); err == nil && len(recs) > 0 &&
			recs[0].Revision != "" {
			return packsrc.ListEntry{Commit: recs[0].Revision, Tag: recs[0].Tag}.Label()
		}
	}
	return "the build " + key
}

// sweepDroppedHostTrees removes the versioned copies of every patched extension the selection no
// longer carries, once nothing the files ownership record names links into them: the dropped pack's
// link is retired by the apply's prune of a dropped pack's output, which runs before this, and a
// link still in place keeps its tree. Best-effort: a copy left behind costs disk, never a render.
func sweepDroppedHostTrees(packs []*packload.Pack) {
	live := map[string]bool{}
	for _, f := range packload.PatchedTrees(packs) {
		live[run.PatchedCopySlug(f.Key())] = true
	}
	man, err := hostskills.LoadManifest(hostSkillsManifestPath())
	if err != nil {
		return // an unreadable record proves nothing about which links remain, so nothing goes
	}
	sweepUnlinkedHostTrees(live, man)
}

// sweepUnlinkedHostTrees removes every extension's versioned copies under paths.HostTreesDir that
// neither live names (by slug) nor any link man records points into. Best-effort.
func sweepUnlinkedHostTrees(live map[string]bool, man *hostskills.Manifest) {
	entries, err := os.ReadDir(paths.HostTreesDir())
	if err != nil {
		return
	}
	linked := map[string]bool{}
	for dest := range man.Entries {
		if slug := hostTreeLinkSlug(dest); slug != "" {
			linked[slug] = true
		}
	}
	for _, e := range entries {
		if !e.IsDir() || live[e.Name()] || linked[e.Name()] {
			continue
		}
		_ = os.RemoveAll(filepath.Join(paths.HostTreesDir(), e.Name()))
	}
}

// hostTreeLinkSlug is the extension slug a link at dest points into under paths.HostTreesDir, ""
// when dest is no such link.
func hostTreeLinkSlug(dest string) string {
	target, err := os.Readlink(dest)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(paths.HostTreesDir(), target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
}

// revertHostTreeLinks is the revert's arm for PATCHED EXTENSIONS (docs/design/patched-extensions.md
// §8.3, PPX-D31): every link the `files` ownership record names that points into the host's
// versioned copies is removed and forgotten, and then every versioned copy no recorded link names,
// whatever the selection carries — a revert is the user taking yolo out of the home. observe reports
// the links it would remove and writes nothing. The links come back sorted. A plain `files` tree's
// output is not this arm's: the revert withdraws keys, and leaves those as it always has.
//
// A record that cannot be read proves nothing is yolo's, so nothing is removed, and warn says so,
// naming the record and what to do; it fails no revert, whose keys are another record's.
func revertHostTreeLinks(observe bool) (links []string, warn string, err error) {
	manPath := hostSkillsManifestPath()
	man, lerr := hostskills.LoadManifest(manPath)
	if lerr != nil {
		return nil, fmt.Sprintf("the files ownership record %s cannot be read (%v), so no patched extension's "+
			"link is removed — repair or remove that file and re-run this, or remove a link at ~/<into> by hand", manPath, lerr), nil
	}
	for dest := range man.Entries {
		if hostTreeLinkSlug(dest) != "" {
			links = append(links, dest)
		}
	}
	sort.Strings(links)
	if observe || len(links) == 0 {
		return links, "", nil
	}
	for _, dest := range links {
		if err := os.Remove(dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return links, "", err
		}
		man.Forget(dest)
	}
	if err := man.Save(manPath); err != nil {
		return links, "", err
	}
	sweepUnlinkedHostTrees(nil, man)
	return links, "", nil
}
