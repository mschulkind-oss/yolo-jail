package cli

// hosttrees.go is a PATCHED EXTENSION at the host notch (docs/reference/pi-extensions.md §8.3,
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
// ownership record and swapped by writing a link beside it and renaming it over. Older versions are
// retained: host agent sessions have no reliable process-to-version liveness record, so pruning by
// age or by the two most recent links could delete bytes a still-running session may read.
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
// A Linux host builds its trees in a capture jail; a Mac builds them for darwin through the
// macos-user fork-build act, as its host floor builds a patched fork (hostTreeRuntime,
// hostTreePlatform). A host that cannot build one now — a Mac before `yolo yolo-account create`, whose
// reason names that step (hostTreeBuildBlocked), or a host yolo builds none on — has the render say
// so once, with its step, and so does `yolo host -- <bin>` of the owning agent, which starts
// without it (noteHostTreeLines).
//
// # A revert
//
// `yolo host apply --revert` removes every recorded link but retains its versioned bytes during the
// ordinary retirement; only the explicit revert's confirmed cleanup deletes generations.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostTreesBuild reports whether this host builds patched extensions now, outside a jail: a Linux
// host, in a capture jail of its container runtime; and a Mac whose macos-user fork-build act can run
// here (hostTreeBuildBlocked), the build being the yolo account's, for darwin (PPX-D5, PF-D97). A
// var so a test can stand a host that builds none in.
var hostTreesBuild = func() bool {
	if config.InJail() {
		return false
	}
	switch hostTreeGOOS() {
	case "linux":
		return true
	case "darwin":
		return hostTreeBuildBlocked() == ""
	}
	return false
}

// hostTreeGOOS is the OS this host's tree builds are for, read off the host floor's platform
// (hostFloorCapturePlatform), the seam a test stands a Mac in through.
func hostTreeGOOS() string {
	goos, _, _ := strings.Cut(floorPatchedPlatform(), "/")
	return goos
}

// hostTreeRuntime is the runtime a host's tree advance builds under: on a Mac the macos-user
// fork-build act, as the Mac host floor's patched fork's (floorPatchedRuntime) — a container build
// there is a Linux build, which no Mac host agent loads; elsewhere "", the advance's capture jail
// resolving its own.
func hostTreeRuntime() string { return floorPatchedRuntime() }

// hostTreePlatform is the platform a host's tree advance builds and reads builds for: on a Mac this
// Mac's (floorPatchedPlatform, darwin), what the macos-user act makes; elsewhere a capture jail's
// (captureJailPlatform), so the host and a jail launch name one build.
func hostTreePlatform() string {
	if hostTreeRuntime() == "macos-user" {
		return floorPatchedPlatform()
	}
	return captureJailPlatform()
}

// hostTreeBuildBlocked is why this Mac's macos-user fork-build act cannot build a patched extension
// now, with the step that ends it (the act's own refusals, as the Mac host floor asks them before a
// fork's build: macActBlocked), or "" when it can, and on a host that is no Mac.
func hostTreeBuildBlocked() string {
	if hostTreeRuntime() != "macos-user" {
		return ""
	}
	return macActBlocked(hostFloorMacProbes(), macActWords{act: "a patched extension's build",
		confines: "a patched extension's build", runsAs: "a patched extension's build on a Mac runs as"},
		"builds it")
}

// hostNoTreeWhy is why this host builds no tree, with its step when there is one: a Mac's blocked
// act (hostTreeBuildBlocked), or a host yolo builds no tree on at all.
func hostNoTreeWhy() string {
	if why := hostTreeBuildBlocked(); why != "" {
		return "this Mac cannot build a tree now: " + why
	}
	return "this host builds no tree"
}

// hostTreeAdvance runs a patched extension's advance at the host: a var so a test can count it.
var hostTreeAdvance = advancePatchedFork

// hostTreeNewAdvance opens a patched extension's advance at the host for a read (newAdvance): a var
// so a test can read the options the host opens it with.
var hostTreeNewAdvance = newAdvance

// advanceHostTrees runs the check and the advance of every patched extension the host's selection
// carries — only those bin's owning agent pack runs, when bin is not "" — before any render reads
// them (PPX-D11). Lines go to errw, as every host launch line does. act is the verb's act interrupt
// (PF-D57), which the patched forks' advances after these read too. It returns false when a series
// does not apply and its bypass did not run an intact build in its place (PF-D81): the caller stops
// BEFORE the render, so a refused verb writes nothing; the advance has said the error block.
func advanceHostTrees(errw io.Writer, color bool, bin string, act *run.ActInterrupt, manual ...bool) bool {
	freshUpdate := len(manual) > 0 && manual[0]
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		if freshUpdate {
			fmt.Fprintf(errw, "yolo host: refusing --update: could not resolve the selected owner vector: %v — repair the selected pack/config and retry\n", sel.loadErr)
			return false
		}
		return true
	}
	command := "yolo host apply --assert"
	if bin != "" {
		command = "yolo host -- " + bin
	}
	var trees, declared []packload.Fork // declared: each tree as its manifest declares it
	for _, f := range packload.PatchedTrees(sel.packs) {
		if !f.DeliveredAtHost() || (bin != "" && !ownerRuns(sel.packs, f, bin)) {
			continue // no host render links a tree whose list entry reaches jails alone (PPX-D35)
		}
		if hostSkipped(f) {
			continue // YOLO_SKIP_EXTENSIONS: not advanced or built; the render and the launch say it
		}
		declared = append(declared, f)
		if !freshUpdate {
			// YOLO_SKIP_PATCHES (skippatches.go): its upstream build, advanced now whatever the timing;
			// --update prepares the series alone.
			f = upstreamBuildOf(f)
		} else {
			noteSkipPatchesFresh(errw, color, f)
		}
		trees = append(trees, f)
	}
	// A MAC PREPARES NOTHING BETWEEN LAUNCHES: macos-user runs no preparation (run.BackgroundChecksOn),
	// so a Mac registers none and builds a next-launch tree's newer build in this act, as the Mac host
	// floor does a patched fork's.
	prepares := hostTreeRuntime() != "macos-user"
	var queued []packload.Fork
	for _, f := range trees {
		if prepares && !f.Upstream && treeUpdateTiming(f) == timingNextLaunch {
			queued = append(queued, f)
		}
	}
	if len(queued) > 0 {
		// Host registrations are additive and limited to this command owner's tree vector.
		report := &buildReport{perf: hostActPerf}
		span := report.preparationSpan("preparation.registration")
		err := registerHostTreePreparation(queued, errw)
		report.preparationOutcome("preparation.registration", "selected host trees", err)
		span.End()
		if err != nil {
			fmt.Fprintf(errw, "⚠ next-launch host tree preparation could not be recorded (%v) — repair the scheduler state, then run `yolo pack prepare start`\n", err)
			return false
		}
	}
	if !hostTreesBuild() {
		if freshUpdate && len(trees) > 0 {
			if why := hostTreeBuildBlocked(); why != "" {
				fmt.Fprintf(errw, "yolo host: refusing --update: patched trees cannot be freshly prepared on this Mac now: %s\n", why)
				return false
			}
			fmt.Fprintln(errw, "yolo host: refusing --update: patched trees cannot be freshly prepared on this host; run a fresh supported Linux jail launch instead")
			return false
		}
		return true
	}
	// Capture each fresh request boundary inside the interrupt scope, under the same owner record
	// lock that records STARTED before transport. A request canceled while waiting never advances.
	boundaries := make(map[string]packsrc.CheckStart, len(trees))
	identities := make(map[string]string, len(trees))
	prepare := func(ctx context.Context) error {
		if !freshUpdate {
			return nil
		}
		store := patchedAdvanceStore(true)
		store.Ctx = ctx
		errs := make(map[string]error, len(trees))
		for _, f := range trees {
			if err := registerFreshPreparation(ctx, store, f, hostTreePlatform(), identities, boundaries, errs); err != nil {
				return fmt.Errorf("could not register a fresh check for %s: %w", f.Label(), err)
			}
		}
		return nil
	}
	// A PARALLEL ADVANCE, as a jail launch's tree arm runs one (treepool.go, XB-D10): a Linux host
	// builds in podman's own capture jails, side by side; a Mac in its one sandbox account, one build
	// at a time (run.SlotBuildJailsOn). An admitted next-launch build is read without a foreground
	// upstream refresh where preparation runs; only a cold/mismatched build or strict patch failure
	// advances here.
	results := make([]advanceResult, len(trees))
	err := runTreesInParallelPrepared(trees, hostTreeRuntime(), act, errw, errw, prepare,
		func(i int, f packload.Fork, lane treeLane) {
			if prepares && !freshUpdate && !f.Upstream && treeUpdateTiming(f) == timingNextLaunch && currentPatchFailure(f) == nil {
				if entry, _, _ := hostTreeServing(f, hostActPerf); entry != nil {
					richtext.Printer{W: errw, Color: color}.Printf("[dim]%s[/dim]", richtext.Escape(
						f.Label()+": using admitted build "+entry.Key+"; newer builds are prepared between host launches"))
					return
				}
				richtext.Printer{W: errw, Color: color}.Printf("[dim]%s[/dim]", richtext.Escape(
					f.Label()+": no admitted build serves yet; preparing the cold host build now"))
			}
			opts := lane.options(advanceOptions{platform: hostTreePlatform(), runtime: hostTreeRuntime(), color: color,
				launch: true, host: true, act: act, bypassCommand: command})
			if freshUpdate {
				opts.freshCheck = true
				opts.freshBoundary = boundaries[f.Key()]
				opts.freshIdentity = identities[f.Key()]
			}
			results[i] = hostTreeAdvance(f, opts)
		})
	if err != nil {
		fmt.Fprintf(errw, "yolo host: refusing --update before preparation began: %v — retry after the owner record is available\n", err)
		return false
	}
	var stopped []string
	series := false // one of them is a series that does not apply, not only a newer build that failed
	for i, r := range results {
		if trees[i].Upstream && bin == "" && r.delivery.Key != "" {
			// YOLO_SKIP_PATCHES at `yolo host apply --assert`: the disclosure line (a launch's is
			// noteHostTreeLines').
			sayUpstreamBuild(errw, color, "yolo host: ", "running", declared[i], r.good)
		}
		if freshUpdate && r.failed {
			fmt.Fprintf(errw, "yolo host: refusing --update: fresh preparation of %s failed: %s — repair the named problem and retry, or launch without --update for ordinary recovery\n", trees[i].Label(), r.freshFailure())
		}
		if r.patchFailure != nil && !r.bypassed || freshUpdate && r.failed {
			stopped = append(stopped, trees[i].Label())
			series = series || r.patchFailure != nil && r.patchFailure.Kind != packsrc.PatchFailureBuild
		}
	}
	if len(stopped) == 0 {
		return true
	}
	if freshUpdate {
		return false
	}
	if !series {
		fmt.Fprintf(errw, "yolo host: refusing: the newer build of %s failed (the ERROR above); nothing was written.\n",
			entrypoint.JoinAnd(stopped))
		return false
	}
	fmt.Fprintf(errw, "yolo host: refusing: the patch series of %s %s not apply (the ERROR above); nothing was written.\n",
		entrypoint.JoinAnd(stopped), plural(len(stopped), "does", "do"))
	return false
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
			if c.Provides() && c.Bin == bin {
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
// no tree now (a jail, a Mac before its setup — noteHostTreeLines says that one), which is exempt.
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
	for _, f := range hostLaunchTrees(sel.packs, bin) {
		dest := filepath.Join(home, filepath.FromSlash(strings.TrimSuffix(f.Into, "/")))
		if !hostTreePatchFailureAllows(errw, f, bin, dest) {
			return false
		}
		if isDir(dest) {
			continue
		}
		_, _, why := hostTreeServing(f)
		if why == "" {
			why = "~/" + strings.TrimSuffix(f.Into, "/") + " names no build — " + hostApplyStep("renders it")
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

// hostTreePatchFailureAllows is PF-D81 at the stop, for a launch whose advance did not run before the
// gate (host_apply_on_launch off, where advanceHostTrees said and decided it): a patched extension
// whose series as it stands has a recorded patch failure stops the launch, read from its check record
// with no git, its error block said here, even though the linked good build is there to load.
// paths.AllowPatchFailuresEnv, `1` or a name covering it (internal/patchrecovery), loads that linked
// build, said in one line. UNDER A HOLD (docs/reference/patched-forks.md §3.4) a failure at another
// version than the good build's has no bearing while a build is linked: the launch loads it, unsaid.
func hostTreePatchFailureAllows(errw io.Writer, f packload.Fork, bin, dest string) bool {
	if config.HostApplyOnLaunchEnabled() {
		return true
	}
	pf := currentPatchFailure(f)
	if pf == nil {
		return true
	}
	runs := ""
	if target, err := os.Readlink(dest); err == nil && filepath.Dir(target) == hostTreeVersionsDir(f) && isDir(dest) {
		runs = linkedTreeLabel(filepath.Base(target))
	}
	if runs != "" && holdPassesOver(f, recordedGood(f), pf) {
		return true
	}
	command := "yolo host -- " + bin
	if runs != "" && allowPatchFailures(f) {
		// The hatch is set: the block with no list, and one line under it.
		_, _ = io.WriteString(errw, pf.Render(f.Label(), f.RebaseRecipe(), packsrc.PatchBypass{Key: f.Key()},
			colorForWriter(errw)))
		fmt.Fprintf(errw, "Continuing with %s (%s)\n", runs, allowPatchFailuresEnv)
		return true
	}
	bypass := packsrc.PatchBypass{Command: command, Key: f.Key(), Hatches: hostTreeGateHatches(f, command, runs)}
	_, _ = io.WriteString(errw, pf.Render(f.Label(), f.RebaseRecipe(), bypass, colorForWriter(errw)))
	return false
}

// hostTreeGateHatches is the hatch list of hostTreePatchFailureAllows' refusal: the advance's own
// (advance.recoveryHatches, the one authority) as it stands at this launch, which runs no advance —
// so no walk-back, no cached-good build and no upstream build (YOLO_SKIP_PATCHES would load whatever
// is linked, not the upstream) — and whose patch-failure bypass loads the LINKED build, runs,
// offered only when one is linked.
func hostTreeGateHatches(f packload.Fork, command, runs string) []patchrecovery.Hatch {
	var from []patchrecovery.Hatch
	if a, early := hostTreeNewAdvance(f, advanceOptions{platform: hostTreePlatform(), runtime: hostTreeRuntime(), out: io.Discard,
		errw: io.Discard, launch: true, host: true, bypassCommand: command, readOnlyInitialization: true}); early == nil {
		from = a.recoveryHatches()
	}
	var out []patchrecovery.Hatch
	if runs != "" {
		out = append(out, patchrecovery.Hatch{Env: patchrecovery.AllowPatchFailuresEnv, Value: f.Key(),
			Command: command, Label: "run " + runs})
	}
	for _, h := range from {
		switch h.Env {
		case patchrecovery.AllowPatchFailuresEnv, patchrecovery.AllowOlderUpstreamEnv, patchrecovery.UseCachedGoodEnv,
			patchrecovery.SkipPatchesEnv:
			continue
		}
		out = append(out, h)
	}
	return out
}

// recordedGood is f's recorded good build, nil for none or an unreadable record.
func recordedGood(f packload.Fork) *packsrc.GoodBuild {
	series, err := f.ReadSeries()
	if err != nil {
		return nil
	}
	rec, err := run.LoadPatchedRecord(patchedForkStore(), f, series)
	if err != nil || rec == nil {
		return nil
	}
	return rec.Good
}

// hostLaunchTrees is every patched extension `yolo host -- <bin>` stops without: those whose owning
// agent pack runs bin and whose list entry reaches the host. A FALLBACK never stops the agent
// (XB-D7): with no tree here, its raw entry is what the render put in the agent's list, and the
// agent installs it itself. The one definition the stop (hostTreeGate) and the launch's link
// (linkHostLaunchTrees) both read, so the launch links exactly what the stop looks for.
func hostLaunchTrees(packs []*packload.Pack, bin string) []packload.Fork {
	var out []packload.Fork
	for _, f := range packload.PatchedTrees(packs) {
		if f.ListedAtHost && ownerRuns(packs, f, bin) && f.Fallback == "" && !hostSkipped(f) {
			out = append(out, upstreamBuildOf(f)) // YOLO_SKIP_PATCHES links its upstream build
		}
	}
	return out
}

// launchHasHostTrees reports whether `yolo host -- <bin>` loads a patched extension at this host,
// which its check and advance ran for before the render gate (advanceHostTrees).
func launchHasHostTrees(bin string) bool {
	if !hostTreesBuild() {
		return false
	}
	sel := selectConfiguredHostPacks()
	return sel.loadErr == nil && len(hostLaunchTrees(sel.packs, bin)) > 0
}

// linkHostLaunchTrees is PPX-D44: the launch's own render of the trees `yolo host -- <bin>` stops
// without (hostLaunchTrees), each linked to the good build that serves it, for a render gate that
// renders nothing else — its observe pass did not finish within its budget, or it named a decision
// the launch cannot ask. The check and advance before the gate built or moved these trees for this
// very launch, so their link is the delivery of that build, not a freshness check the gate may
// decline.
//
// It is renderHostTree's acting posture, so it takes nothing the render would not: a path the files
// ownership record does not name as the tree's pack's is left untouched, and a tree with no build
// that serves is left for the stop to name. A record that cannot be read proves nothing is yolo's,
// so nothing is linked and the stop names `yolo host apply --assert`, which reports the record.
// The caller holds the host apply lock. One line per link written goes to errw.
func linkHostLaunchTrees(errw io.Writer, bin, home string) {
	if !hostTreesBuild() || config.HostManagementMode() == config.HostManagementNone {
		return
	}
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		return
	}
	trees := hostLaunchTrees(sel.packs, bin)
	if len(trees) == 0 {
		return
	}
	manPath := hostSkillsManifestPath()
	man, err := hostskills.LoadManifest(manPath)
	if err != nil {
		return
	}
	wrote := false
	for _, f := range trees {
		res := renderHostTree(f, home, man, false)
		if !res.WouldChange {
			continue
		}
		wrote = true
		fmt.Fprintf(errw, "yolo host: linked %s to the build of %s that %s loads\n", prettyHomePath(home, res.Path),
			f.Label(), bin)
	}
	if wrote {
		if err := man.Save(manPath); err != nil {
			fmt.Fprintf(errw, "yolo host: could not save the files ownership record %s (%v) — the next "+
				"`yolo host apply --assert` treats the link as yours and leaves it alone; remove it and apply "+
				"again to have yolo own it\n", manPath, err)
		}
	}
}

// hostTreeFallbacks is packs with every UNMODIFIED EXTENSION's fallback taken that the host renders
// no tree for (packload.ApplyTreeFallbacks; docs/design/pi-extension-store-builds.md XB-D7): every
// one on a host that builds none now (hostTreesBuild), and on a host that builds them each whose
// good build does not serve.
// What the host apply's list contributions are collected from, so the agent installs each such
// extension itself, from its raw entry, as it would with no yolo in between; the trees taken come
// back for the apply's lines.
//
// A SKIPPED extension (YOLO_SKIP_EXTENSIONS, hostSkipped) takes no fallback: every entry that loads
// it is left out of its pack's lists (packload.ApplyTreeLists), so the agent starts without it.
func hostTreeFallbacks(packs []*packload.Pack) ([]*packload.Pack, []packload.Fork) {
	out, taken, _ := packload.ApplyTreeLists(packs, func(f packload.Fork) bool {
		if !hostTreesBuild() {
			return false
		}
		entry, _, _ := hostTreeServing(f)
		return entry != nil
	}, hostSkipped)
	return out, taken
}

// hostSkipScope is this host act's YOLO_SKIP_EXTENSIONS (docs/reference/pi-extensions.md §9.1),
// read from this process's environment.
func hostSkipScope() patchrecovery.Scope {
	return patchrecovery.Read(nil, patchrecovery.SkipExtensionsEnv)
}

// hostSkipped reports whether this host act skips f: no advance, no link, its entries left out of
// the lists, and no stop for it.
func hostSkipped(f packload.Fork) bool {
	s := hostSkipScope()
	return s.Set() && s.Covers(run.TreeSubject(f))
}

// hostSkipUnmatchedLine is the warning for YOLO_SKIP_EXTENSIONS names that cover none of packs'
// built extensions, "" when there are none.
func hostSkipUnmatchedLine(packs []*packload.Pack) string {
	s := hostSkipScope()
	if !s.Set() {
		return ""
	}
	var subs []patchrecovery.Subject
	for _, f := range packload.PatchedTrees(packs) {
		subs = append(subs, run.TreeSubject(f))
	}
	return patchrecovery.UnmatchedLine(s.Env, s.Unmatched(subs), subs)
}

// hostSkippedAction opens the render's action for a tree YOLO_SKIP_EXTENSIONS skips.
const hostSkippedAction = "skipped: "

// hostFallbackAction opens the render's action for a tree whose fallback the apply took.
const hostFallbackAction = "not rendered: "

// hostFallbackLine is the line a host act prints for a fallback it took: what the agent installs in
// the tree's place, and why there is no tree.
func hostFallbackLine(f packload.Fork) string {
	why := hostNoTreeWhy()
	if hostTreesBuild() {
		if _, _, reason := hostTreeServing(f); reason != "" {
			why = reason
		}
	}
	return fmt.Sprintf("%s: no tree at the host — %s; the agent installs %s itself, from its raw entry", f.Label(),
		why, f.Fallback)
}

// hostTreeServing is the good build that serves f, read with no git: its store entry and its
// inputs, or why there is none.
func hostTreeServing(f packload.Fork, logs ...*perf.Log) (*capture.Entry, *packsrc.GoodBuild, string) {
	if len(logs) > 0 {
		defer logs[0].Span("preparation.admitted_read").End()
	}
	a, early := hostTreeNewAdvance(f, advanceOptions{platform: hostTreePlatform(), runtime: hostTreeRuntime(), out: io.Discard,
		errw: io.Discard, host: true})
	if early != nil {
		return nil, nil, early.delivery.Reason
	}
	if a.serving == nil {
		why := f.Label() + " has no build on this machine yet — " + hostApplyStep("builds it")
		if g := a.rec.Good; g != nil && g.Recipe != a.recipe {
			why = f.Label() + "'s series or build recipe changed and the edit has no build yet — " +
				hostApplyStep("builds it")
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
		if f.DeliveredAtHost() && hostSkipped(f) {
			// SKIPPED: no link is written or moved, and its list entries were left out
			// (hostTreeFallbacks). The next apply without the variable renders it again.
			out = append(out, entrypoint.HostRenderResult{Surface: f.Pack + "/files",
				Path:   filepath.Join(home, filepath.FromSlash(strings.TrimSuffix(f.Into, "/"))),
				Action: hostSkippedAction + run.SkippedLine(f)})
			continue
		}
		if !f.DeliveredAtHost() {
			if res, ok := retireHostTreeLink(f, home, man, observe); ok {
				out = append(out, res)
			}
			continue
		}
		out = append(out, renderHostTree(upstreamBuildOf(f), home, man, observe)) // YOLO_SKIP_PATCHES: its upstream build
	}
	return out
}

// retireHostTreeLink is the render's arm for a tree no host loads (PPX-D35): the link at `~/<into>`
// an earlier render left, when the files ownership record says it is f's pack's and it points into
// f's versioned copies, is removed and forgotten; the copied bytes are retained because an
// untracked running host session may still read them. ok is false, and nothing is touched, when
// there is no such link.
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
	res.Action = "removed (" + why + "); retained its host copies because a running host session may still use them"
	return res, true
}

// renderHostTree is one patched extension's render.
func renderHostTree(f packload.Fork, home string, man *hostskills.Manifest, observe bool) entrypoint.HostRenderResult {
	dest := filepath.Join(home, filepath.FromSlash(strings.TrimSuffix(f.Into, "/")))
	res := entrypoint.HostRenderResult{Surface: f.Pack + "/files", Path: dest}
	if !hostTreesBuild() {
		if f.Fallback != "" {
			res.Action = hostFallbackAction + hostFallbackLine(f)
			return res
		}
		res.Action = "refused: " + f.Label() + " is not built here — " + noHostTreeStep(f, ownerBinFor(f))
		return res
	}
	entry, _, why := hostTreeServing(f)
	if entry == nil {
		if f.Fallback != "" {
			res.Action = hostFallbackAction + hostFallbackLine(f)
			return res
		}
		res.Action = "refused: " + why
		return res
	}
	version := filepath.Join(hostTreeVersionsDir(f), entry.Key)
	info, lerr := os.Lstat(dest)
	occupied := lerr == nil
	owned := man != nil && man.OwnedBy(dest, f.Pack)
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
		previous, _ := os.Readlink(dest)
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
			if c.Provides() && c.Bin != "" {
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
	defer advanceSpan(f, "materialize").End()
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

// noteHostTreeLines prints a line for each patched extension the launch's agent loads at the host
// (its list entry reaches the host) — a disclosure (OQ-RO3), as a jail launch's block is (PPX-D26).
// It names the build `~/<into>` links to, which is what the agent loads, and, when this machine's
// good build has moved past it with no render since, that `yolo host apply --assert` renders the good
// one. On a host that builds no tree now it says the agent starts without it and names the step: a
// Mac's setup, or a jail that has it (§9, §11). Silent under `host_management: none`, which writes no link, and in a jail.
func noteHostTreeLines(errw io.Writer, color bool, bin, home string) {
	if config.InJail() || config.HostManagementMode() == config.HostManagementNone {
		return
	}
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		return
	}
	pr := richtext.Printer{W: errw, Color: color}
	if line := hostSkipUnmatchedLine(sel.packs); line != "" {
		pr.Printf("[yellow]%s[/yellow]", richtext.Escape("yolo host: "+line))
	}
	for _, f := range packload.PatchedTrees(sel.packs) {
		if !f.ListedAtHost || !ownerRuns(sel.packs, f, bin) {
			continue
		}
		if hostSkipped(f) {
			pr.Printf("[yellow]%s[/yellow]", richtext.Escape("yolo host: "+run.SkippedLine(f)))
			continue
		}
		if !hostTreesBuild() {
			if f.Fallback != "" {
				pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("yolo host: %s is not built on this host — "+
					"%s; %s installs %s itself, in this home, from its raw entry", f.Label(), hostNoTreeWhy(), bin,
					f.Fallback)))
				continue
			}
			pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("yolo host: %s is not delivered on this host, "+
				"so %s starts without it — %s", f.Label(), bin, noHostTreeStep(f, bin))))
			continue
		}
		if f.Fallback != "" {
			if entry, _, _ := hostTreeServing(f); entry == nil {
				pr.Printf("[dim]%s[/dim]", richtext.Escape("yolo host: "+hostFallbackLine(f)))
				continue
			}
		}
		if u := upstreamBuildOf(f); u.Upstream {
			// YOLO_SKIP_PATCHES: the disclosure line of the upstream build the link names.
			if hostTreeLine(u, home) != "" {
				sayUpstreamBuild(errw, color, "yolo host: ", "running", f, nil)
			}
			continue
		}
		if line := hostTreeLine(f, home); line != "" {
			pr.Printf("[dim]%s[/dim]", richtext.Escape("yolo host: "+line))
		}
	}
}

// noHostTreeStep is why f is not built on a host that builds no tree now, and the next step for it.
// On a Mac whose macos-user act cannot run yet, that act's own reason and step (hostNoTreeWhy): the
// Mac builds the tree once it can, whatever lists the entry. Elsewhere (PPX-D38): a jail that has it,
// running bin, when its list entry reaches a jail; otherwise no notch has it, since the entry is in a
// guarded posture list, which reaches the host alone, so the step is moving the entry.
func noHostTreeStep(f packload.Fork, bin string) string {
	why := hostNoTreeWhy()
	if hostTreeBuildBlocked() != "" {
		return why
	}
	if f.DeliveredInJail() {
		return why + "; YOLO_RUNTIME=podman yolo -- " + bin + " runs it in a jail that has it"
	}
	return why + ", and its list entry is in a guarded posture list, which reaches the host alone, so no jail " +
		"has it either — " + packload.GuardedOnlyStep
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
		return f.Label() + " at " + run.WithGood(run.GoodBuildLabel(g), g)
	case entry != nil:
		return f.Label() + " at " + linkedTreeLabel(linked) + "; " + run.WithGood(run.GoodBuildLabel(g), g) +
			" is built, and " + hostApplyStep("renders it")
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

// noteRetainedHostTreeVersions says what historical generations remain after an apply. Host sessions
// have no process-to-version liveness record, so ordinary drop/apply retires links but never deletes
// an unlinked generation; only the explicit revert is a cleanup authority.
func noteRetainedHostTreeVersions(pr richtext.Printer, write bool) {
	if !write {
		return
	}
	man, err := hostskills.LoadManifest(hostSkillsManifestPath())
	if err != nil {
		return // an unreadable ownership record cannot prove which generations remain in use
	}
	linked := map[string]bool{}
	for dest := range man.Entries {
		target, err := os.Readlink(dest)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(paths.HostTreesDir(), target)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			linked[filepath.Clean(target)] = true
		}
	}
	var retained int
	slugs, err := os.ReadDir(paths.HostTreesDir())
	if err != nil {
		return
	}
	for _, slug := range slugs {
		if !slug.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(paths.HostTreesDir(), slug.Name()))
		if err != nil {
			continue
		}
		for _, version := range versions {
			if version.IsDir() && !linked[filepath.Clean(filepath.Join(paths.HostTreesDir(), slug.Name(), version.Name()))] {
				retained++
			}
		}
	}
	if retained > 0 {
		pr.Printf("[dim]retained %d historical patched-extension host generation(s): a running host session may still read these bytes; stop all host agent sessions, then `yolo host apply --revert --assert` deletes them[/dim]", retained)
	}
}

func hostTreeGenerationCount() int {
	var count int
	slugs, err := os.ReadDir(paths.HostTreesDir())
	if err != nil {
		return 0
	}
	for _, slug := range slugs {
		if !slug.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(paths.HostTreesDir(), slug.Name()))
		if err != nil {
			continue
		}
		for _, version := range versions {
			if version.IsDir() {
				count++
			}
		}
	}
	return count
}

// removeUnlinkedHostTrees removes every unlinked extension generation under paths.HostTreesDir.
// It is called only by the explicit confirmed revert; ordinary apply cannot know whether an
// untracked host session still has one of these paths open.
func removeUnlinkedHostTrees(man *hostskills.Manifest) {
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
		if !e.IsDir() || linked[e.Name()] {
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

// revertHostTreeLinks is the revert's arm for PATCHED EXTENSIONS (docs/reference/pi-extensions.md
// §8.3, PPX-D31): every link the `files` ownership record names that points into the host's
// versioned copies are deleted only in this explicit confirmed cleanup. observe reports the links,
// retained generations and running-session warning without writing. The caller prints that warning
// before the confirmed path deletes them. A plain `files` tree's output is not this arm's: the revert
// withdraws keys, and leaves those as it always has.
//
// A record that cannot be read proves nothing is yolo's, so nothing is removed, and warn says so,
// naming the record and what to do; it fails no revert, whose keys are another record's.
func revertHostTreeLinks(observe bool) (links []string, generations int, warn string, err error) {
	manPath := hostSkillsManifestPath()
	man, lerr := hostskills.LoadManifest(manPath)
	if lerr != nil {
		return nil, 0, fmt.Sprintf("the files ownership record %s cannot be read (%v), so no patched extension's "+
			"link is removed — repair or remove that file and re-run this, or remove a link at ~/<into> by hand", manPath, lerr), nil
	}
	for dest := range man.Entries {
		if hostTreeLinkSlug(dest) != "" {
			links = append(links, dest)
		}
	}
	sort.Strings(links)
	generations = hostTreeGenerationCount()
	if len(links) > 0 || generations > 0 {
		warn = "the confirmed revert will delete retained patched-extension host generations; a running host session may still read these bytes — stop all host agent sessions before running with `--assert`"
	}
	if observe {
		return links, generations, warn, nil
	}
	for _, dest := range links {
		if err := os.Remove(dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return links, generations, warn, err
		}
		man.Forget(dest)
	}
	if len(links) > 0 {
		if err := man.Save(manPath); err != nil {
			return links, generations, warn, err
		}
	}
	removeUnlinkedHostTrees(man)
	return links, generations, warn, nil
}
