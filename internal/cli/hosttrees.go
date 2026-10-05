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
// naming a jail that has the extension.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
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
// them (PPX-D11). Lines go to errw, as every host launch line does.
func advanceHostTrees(errw io.Writer, color bool, bin string) {
	if !hostTreesBuild() {
		return
	}
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		return
	}
	for _, f := range packload.PatchedTrees(sel.packs) {
		if bin != "" && !ownerRuns(sel.packs, f, bin) {
			continue
		}
		hostTreeAdvance(f, advanceOptions{platform: captureJailPlatform(), out: errw, errw: errw, color: color,
			launch: true, host: true})
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

// hostTreeGate is PPX-D18 at `yolo host -- <bin>`: the launch of an owning agent stops when a tree
// it loads at the host — its list entry reaches the host — is not there to load: `~/<into>` names
// no directory, because nothing has built it or no apply has rendered it. It names the extension,
// the cause and the next step, and returns false to stop the launch. Silent where the host builds
// no tree (macOS, a jail), which is exempt.
func hostTreeGate(errw io.Writer, bin, home string) bool {
	if !hostTreesBuild() {
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
// whether `~/<into>` already names the good build's versioned copy.
func renderHostTrees(p *packload.Pack, packs []*packload.Pack, home string, man *hostskills.Manifest,
	observe bool) []entrypoint.HostRenderResult {
	var out []entrypoint.HostRenderResult
	for _, f := range packload.PatchedTrees(packs) {
		if f.Pack != p.Name {
			continue
		}
		out = append(out, renderHostTree(f, home, man, observe))
	}
	return out
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

// noteHostTreeLines prints a line for each patched extension the launch's agent loads, naming its
// good build — a disclosure (OQ-RO3), as a jail launch's block is.
func noteHostTreeLines(errw io.Writer, color bool, bin string) {
	if !hostTreesBuild() {
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
		if _, g, why := hostTreeServing(f); why == "" {
			pr.Printf("[dim]yolo host: %s at %s + %s[/dim]", richtext.Escape(f.Label()), run.GoodBuildLabel(g),
				run.PatchCount(g.Patches))
		}
	}
}

// sweepDroppedHostTrees removes the versioned copies of every patched extension the selection no
// longer carries, once nothing the files ownership record names links into them: the dropped pack's
// link is retired by the apply's prune of a dropped pack's output, which runs before this, and a
// link still in place keeps its tree. Best-effort: a copy left behind costs disk, never a render.
func sweepDroppedHostTrees(packs []*packload.Pack) {
	entries, err := os.ReadDir(paths.HostTreesDir())
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, f := range packload.PatchedTrees(packs) {
		live[run.PatchedCopySlug(f.Key())] = true
	}
	man, err := hostskills.LoadManifest(hostSkillsManifestPath())
	if err != nil {
		return // an unreadable record proves nothing about which links remain, so nothing goes
	}
	linked := map[string]bool{}
	for dest := range man.Entries {
		if target, err := os.Readlink(dest); err == nil {
			if rel, err := filepath.Rel(paths.HostTreesDir(), target); err == nil && !strings.HasPrefix(rel, "..") {
				linked[strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]] = true
			}
		}
	}
	for _, e := range entries {
		if !e.IsDir() || live[e.Name()] || linked[e.Name()] {
			continue
		}
		_ = os.RemoveAll(filepath.Join(paths.HostTreesDir(), e.Name()))
	}
}
