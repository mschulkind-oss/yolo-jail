package cli

// patchedseriescheck.go is `yolo pack series check [<pack-dir>] [--onto <ref>]`
// (docs/design/patched-forks.md PF-D65): the verdict a launch would reach on the patch series of
// every patched fork and patched extension in a local pack's directory, reached in a SCRATCH MIRROR
// (a term coined here: a pack store rooted in a new private temporary directory, outside yolo's
// state directory and outside the pack, which the check fetches the upstream into and which is
// deleted when the act ends). It writes nothing in the pack, nothing in yolo's state directory, and
// reads no check record, so it answers the same in a jail, which has neither the host's mirror nor
// its record, as on the host.
//
// The act, for each patched fork or extension of the pack, in declaration order:
//
//  1. The pack is read as a launch reads a local pack (the one resolver,
//     config.ResolvePackForProcess), and the series as a launch reads it (packload.Fork.ReadSeries),
//     once.
//  2. THE CHECK, forced, into the scratch mirror (packsrc.Store.CheckPatched): the fetch, the ref
//     rule, the follow rule and the walk's list. With no good build to cut it at, the list is the
//     whole of it, as a machine's first advance reads it.
//  3. THE WALK (packsrc.Store.WalkSeries), `yolo pack update`'s replay: the series applied at its
//     base, then picked onto the list's entries newest first until one takes it; or onto --onto
//     alone; or, with nothing on the list and the base on the branch, onto the base, which a first
//     advance builds.
//  4. THE VERDICT: the newest entry takes the series, or the member that stops it and its paths and
//     the newest fit below it, each conflict naming the rebase that resolves it. Exit 0 when the
//     newest entry (or --onto, or the base) takes the series, 1 otherwise.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// scratchBuilds is what follows a clean replay of the newest entry in a scratch act's lines (the
// series check, the scratch rebase): with no check record to read, it cannot say whether this
// machine's good build already runs the series there, as an explicit act's patchedNotBuilt can.
const scratchBuilds = "a fresh launch builds it, unless its good build already runs this series there"

// patchedScratchStore is the store a scratch act checks and replays through, rooted at dir, the
// scratch mirror's directory: the user's own terminal, as patchedForkStore's, with the rebase clone's
// network bound, since a scratch mirror starts empty and its first fetch is a whole blobless clone. A
// var so a test can hand it a git of its own.
var patchedScratchStore = func(dir string) *packsrc.Store {
	return &packsrc.Store{Dir: dir, Timeout: packsrc.RebaseCloneTimeout}
}

// scratchPatchedStore makes a scratch mirror for a pack rooted at packRoot and the store over it,
// with remove, which deletes it. It is refused where it would be yolo's state directory or inside
// the pack, naming TMPDIR, which places it.
func scratchPatchedStore(packRoot string) (*packsrc.Store, func(), error) {
	tmp, err := os.MkdirTemp("", "yolo-series-scratch-")
	if err != nil {
		return nil, nil, fmt.Errorf("making a scratch copy of the upstream: %w — set TMPDIR to a writable directory "+
			"(or unset it to use /tmp)", err)
	}
	remove := func() { _ = os.RemoveAll(tmp) }
	resolved := resolveExistingPrefix(tmp)
	if b := paths.WritableSourceScopeBreach(resolved); b != nil {
		remove()
		return nil, nil, fmt.Errorf("%s — set TMPDIR to a directory outside it", b.WhatFor("the scratch copy of the upstream"))
	}
	if r := resolveExistingPrefix(packRoot); underOrEqual(resolved, r) {
		remove()
		return nil, nil, fmt.Errorf("the scratch copy of the upstream %s would be inside the pack's own directory %s — "+
			"set TMPDIR to a directory outside it", tmp, packRoot)
	}
	return patchedScratchStore(tmp), remove, nil
}

// loadPatchedPackDir reads the local pack in dir, named name, as a launch reads a local pack: the
// one resolver, its directory read in place in declaration mode (config.ResolvePackForProcess), with
// no fallback to a tree a launch delivered (YOLO_PACK_ROOT), since a scratch act reads the directory
// it was named and never a copy of it. The caller has made sure dir is a directory.
//
// It is a USE READ (packload.LoadDirForUse), as a launch's is, so a contribution this yolo cannot
// read is skipped, and each skip is said on errw as a launch says it (PF-D72): without the line, a
// patched fork skipped for a field this yolo does not know reads as a pack that declares none.
func loadPatchedPackDir(dir, name string, errw io.Writer, color bool) (*packload.Pack, error) {
	e := config.PackEntry{Source: "file://" + dir, Name: name}
	res, err := config.ResolvePackForProcess(e, config.ResolvePackSpec{ReadOnlyStore: true,
		Getenv: func(string) string { return "" }})
	if err != nil {
		return nil, err
	}
	p, err := resolvedOrProblems(e, res)
	if p != nil {
		pr := richtext.Printer{W: errw, Color: color}
		for _, note := range p.SkewNotes {
			pr.Printf("[yellow]%s[/yellow]", richtext.Escape("Warning: "+note))
		}
	}
	return p, err
}

// packForks is every fork and patched extension the pack p declares, as the keyed verbs read the
// selected packs': its forks, plain and patched, then its patched extensions.
func packForks(p *packload.Pack) []packload.Fork {
	packs := []*packload.Pack{p}
	return append(packload.Forks(packs), packload.PatchedTrees(packs)...)
}

// packSeries is `yolo pack series <verb>`; `check` is its one verb.
func packSeries(args []string, out, errw io.Writer, color bool) int {
	const usage = "`yolo pack series check [<pack-dir>] [--onto <ref>]` checks a pack's patch series (see `yolo pack --help`)"
	switch {
	case len(args) == 0:
		fmt.Fprintf(errw, "yolo pack series: name what to do — %s\n", usage)
		return 2
	case args[0] == "check":
		return packSeriesCheck(args[1:], out, errw, color)
	}
	fmt.Fprintf(errw, "yolo pack series: unknown verb %q — %s\n", args[0], usage)
	return 2
}

// parseSeriesCheckArgs reads `yolo pack series check`'s command line: at most one pack directory,
// "." by default, and --onto. rc 2 says what is wrong with it.
func parseSeriesCheckArgs(args []string, errw io.Writer) (dir, onto string, rc int) {
	var dirs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--onto":
			if i+1 >= len(args) || args[i+1] == "" {
				fmt.Fprintf(errw, "yolo pack series check: --onto needs a value (see `yolo pack --help`)\n")
				return "", "", 2
			}
			i++
			onto = args[i]
		case strings.HasPrefix(a, "--onto="):
			if onto = strings.TrimPrefix(a, "--onto="); onto == "" {
				fmt.Fprintf(errw, "yolo pack series check: --onto needs a value (see `yolo pack --help`)\n")
				return "", "", 2
			}
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo pack series check: unknown flag %q — it takes a pack directory and --onto <ref> "+
				"(see `yolo pack --help`)\n", a)
			return "", "", 2
		default:
			dirs = append(dirs, a)
		}
	}
	switch len(dirs) {
	case 0:
		return ".", onto, 0
	case 1:
		return dirs[0], onto, 0
	}
	fmt.Fprintf(errw, "yolo pack series check: unexpected argument %q — it checks one pack directory\n", dirs[1])
	return "", "", 2
}

// packSeriesCheck is `yolo pack series check`. See the file doc.
func packSeriesCheck(args []string, out, errw io.Writer, color bool) int {
	dir, onto, rc := parseSeriesCheckArgs(args, errw)
	if rc != 0 {
		return rc
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack series check: %v\n", err)
		return 1
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		fmt.Fprintf(errw, "yolo pack series check: %s is not a directory — name the directory holding the pack's "+
			"pack.json\n", abs)
		return 1
	}
	// Named as a `packs` entry for the directory names it (its last segment), as `yolo pack lint` does.
	p, err := loadPatchedPackDir(abs, filepath.Base(abs), errw, color)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack series check: %v — `yolo pack lint %s` names every problem\n", err,
			shquote.QuoteDisplay(abs))
		return 1
	}
	var forks []packload.Fork
	for _, f := range packForks(p) {
		if f.Patched() {
			forks = append(forks, f)
		}
	}
	if len(forks) == 0 {
		fmt.Fprintf(errw, "yolo pack series check: the pack in %s declares no patch series — a `program` with "+
			"`via: \"source\"`, or a `files` contribution with a `source`, names one with `patches`\n", abs)
		return 1
	}
	pr := richtext.Printer{W: out, Color: color}
	ctx, stop := rebaseInterrupt()
	defer stop()
	rc = 0
	for _, f := range forks {
		n := seriesCheckOne(ctx, pr, errw, f, abs, onto)
		if ctx.Err() != nil {
			fmt.Fprintf(errw, "yolo pack series check: interrupted — the scratch copy of the upstream is removed\n")
			return 130
		}
		rc = max(rc, n)
	}
	return rc
}

// seriesCheckOne is one patched fork's or extension's check, walk and verdict (the file doc's steps
// 2 to 4), in a scratch mirror of its own that is deleted when it returns. packDir is the pack's
// directory, onto --onto's ref or "".
func seriesCheckOne(ctx context.Context, pr richtext.Printer, errw io.Writer, f packload.Fork, packDir, onto string) int {
	again := seriesCheckLine(packDir, onto)
	series, err := f.ReadSeries()
	if err != nil {
		pr.Printf("[yellow]%s[/yellow]", richtext.Escape("⚠ "+f.Label()+": "+err.Error()))
		return 1
	}
	pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: %d %s in %s (series %s), base %s", f.Label(), series.Len(),
		plural(series.Len(), "patch", "patches"), series.Dir, series.ShortDigest(), shortSHA(series.Base))))
	store, remove, err := scratchPatchedStore(f.Root)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack series check: %s: %v\n", f.Label(), err)
		return 1
	}
	defer remove()
	store.Ctx = ctx
	res := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: patchedNow,
		Begin: func() (func(string), func()) {
			pr.Printf("[dim]%s[/dim]", richtext.Escape("checking "+f.Label()+"'s upstream "+f.Source+" in a scratch copy"))
			return func(line string) { pr.Printf("[dim]%s[/dim]", richtext.Escape(line)) }, func() {}
		}})
	switch {
	case ctx.Err() != nil:
		return 130
	case res.Err != nil:
		fmt.Fprintf(errw, "yolo pack series check: %s: %v\n", f.Label(), res.Err)
		return 1
	}
	found := res.Record.Check
	if found.FetchErr != "" {
		// A SCRATCH MIRROR STARTS EMPTY, so a fetch that failed leaves nothing to check against.
		fmt.Fprintf(errw, "yolo pack series check: %s: could not fetch %s into a scratch copy (%s) — check the "+
			"address and this machine's network, then run `%s` again\n", f.Label(), f.Source, found.FetchErr, again)
		return 1
	}
	if found.Problem != "" {
		if onto == "" {
			fmt.Fprintf(errw, "yolo pack series check: %s: %s\n", f.Label(), found.Problem)
			return 1
		}
		fmt.Fprintf(errw, "yolo pack series check: %s: %s — checking --onto %s anyway\n", f.Label(), found.Problem, onto)
	}
	repo, subdir := mustRepo(f.Source), subdirOf(f.Source)
	var list []packsrc.ListEntry
	atBase := false
	switch {
	case onto != "":
		target, err := store.ResolveRebaseTarget(repo, onto, nil)
		if err != nil {
			fmt.Fprintf(errw, "yolo pack series check: %s: %v — name a branch, a tag or a commit of the upstream\n",
				f.Label(), err)
			return 1
		}
		list = []packsrc.ListEntry{target}
	default:
		list = res.Record.Candidates(res.Inputs)
		if len(list) == 0 && found.BaseOnBranch {
			// NOTHING ON THE LIST: a first advance builds the series' base (§6.4), which it applies to by
			// construction once it applies at all.
			list, atBase = []packsrc.ListEntry{{Commit: series.Base}}, true
			if found.NoVersion != "" {
				// A RELEASE RULE OVER A BRANCH WITH NO VERSION TAG (PF-D60): why the list is empty, as
				// the launch that meets it says it.
				pr.Printf("[yellow]%s[/yellow]", richtext.Escape("⚠ "+f.Label()+": "+
					found.NoVersionLine(scratchStayAt(series))))
			}
		}
		if len(list) == 0 {
			fmt.Fprintf(errw, "yolo pack series check: %s: %s names nothing this series can be built at — the "+
				"branch does not contain the series' base %s; follow the branch the series was made on, or hold "+
				"at a tag or a commit\n", f.Label(), f.Source, shortSHA(series.Base))
			return 1
		}
	}
	w := store.WalkSeries(repo, subdir, series, list, packsrc.WalkOptions{Timeout: packsrc.RebaseCloneTimeout})
	if ctx.Err() != nil {
		return 130
	}
	lines, clean := seriesCheckReport(f, packDir, series, found.BaseOnBranch, w, atBase, onto)
	for _, line := range lines {
		pr.Printf("%s", line)
	}
	if clean {
		return 0
	}
	return 1
}

// seriesCheckReport is the verdict's lines for a walk, and whether the first entry it replayed —
// the newest, --onto's, or the base — took the series. baseOnBranch is the check's answer to
// whether the followed branch contains the series' base; atBase is a walk of the base alone; onto is
// --onto's ref, "" for none, whose walk of its one commit says nothing about what a launch builds.
func seriesCheckReport(f packload.Fork, packDir string, series *packsrc.Series, baseOnBranch bool, w packsrc.WalkResult,
	atBase bool, onto string) ([]string, bool) {
	again := "`" + seriesCheckLine(packDir, onto) + "`"
	warn := func(s string) []string {
		return []string{"[yellow]" + richtext.Escape("⚠ "+f.Label()+": "+s) + "[/yellow]"}
	}
	var old *packsrc.GitTooOldError
	switch {
	case w.Base != nil:
		return warn(w.Base.Error()), false
	case errors.As(w.Err, &old):
		return warn(old.Need() + ", and this git is " + old.Have + " — update git, then run " + again + " again"), false
	case w.Err != nil:
		return warn(fmt.Sprintf("could not replay the series: %v — %s tries again", w.Err, again)), false
	}
	var lines []string
	for i, r := range w.Results {
		switch {
		case r.Conflict != nil:
			paths := strings.Join(r.Conflict.Paths, ", ")
			if paths == "" {
				paths = "(no path named)"
			}
			lines = append(lines,
				"[yellow]"+richtext.Escape(f.Label()+": upstream "+r.Entry.Label()+" does not take the patch series —")+"[/yellow]",
				richtext.Escape("  "+r.Conflict.Member+" conflicts in "+paths),
				richtext.Escape("  "+scratchRebaseStep(f, packDir, r.Entry)))
		case r.Clean:
			for _, m := range r.Upstream {
				lines = append(lines, "[dim]"+richtext.Escape(fmt.Sprintf("  %s is already in upstream %s; drop it from %s",
					m, r.Entry.Label(), series.Dir))+"[/dim]")
			}
			what := "upstream " + r.Entry.Label()
			switch {
			case atBase:
				what = "the series' base " + r.Entry.Label() + " (no version of the branch contains it)"
			case i > 0:
				what = "the newest fit, " + what + ","
			}
			tail := ""
			if onto == "" {
				tail = " — " + scratchBuilds
			}
			lines = append(lines, "[green]"+richtext.Escape(f.Label())+"[/green]: "+richtext.Escape(fmt.Sprintf(
				"%s takes the series (%d %s, series %s): applies%s", what, series.Len(),
				plural(series.Len(), "patch", "patches"), series.ShortDigest(), tail)))
		case r.Err != nil:
			lines = append(lines, warn(fmt.Sprintf("could not replay the series onto %s: %v — %s tries again",
				r.Entry.Label(), r.Err, again))...)
		}
	}
	if onto == "" && w.Fit < 0 && len(w.Results) > 0 && w.Results[len(w.Results)-1].Err == nil {
		without := "with none, it has nothing to build"
		if baseOnBranch {
			without = "with none, it builds the series' base " + shortSHA(series.Base)
		}
		lines = append(lines, warn("no upstream version on the list takes the series — a launch keeps its good "+
			"build running; "+without)...)
	}
	return lines, len(w.Results) > 0 && w.Results[0].Clean
}

// scratchStayAt is where a fork whose release rule finds no version tag stays, as a scratch act
// names it (CheckFound.NoVersionLine): with no check record to read, it cannot say which good build
// this machine runs, so it names both.
func scratchStayAt(series *packsrc.Series) string {
	return "this machine's good build, or with none the series' base " + shortSHA(series.Base) + ","
}

// seriesCheckLine is the check's own command line, for a line that names running it again.
func seriesCheckLine(packDir, onto string) string {
	argv := []string{"yolo", "pack", "series", "check", packDir}
	if onto != "" {
		argv = append(argv, "--onto", onto)
	}
	return shquote.JoinDisplay(argv)
}

// scratchRebaseStep is a scratch act's conflict step: the scratch rebase of the pack in packDir onto
// e, named by its tag or commit so the rebase meets the commit this check did. In a jail, for a pack
// outside the jail's workspace, which the scratch rebase refuses there, the host's keyed verb.
func scratchRebaseStep(f packload.Fork, packDir string, e packsrc.ListEntry) string {
	ref := e.Tag
	if ref == "" {
		ref = e.Commit
	}
	if notTheHostsCopy(packDir) {
		return "rebase the series on the host: " + shquote.JoinDisplay([]string{"yolo", "pack", "rebase", f.Key(),
			"--onto", ref})
	}
	return "rebase the series: " + shquote.JoinDisplay([]string{"yolo", "pack", "rebase", f.Key(), "--pack", packDir,
		"--onto", ref})
}

// inJailWorkspace reports whether p is inside this jail's own workspace (config.JailWorkspace), both
// resolved, so a link to it, or a darwin temporary directory, compares as the directory it is.
func inJailWorkspace(p string) bool {
	return underOrEqual(resolveExistingPrefix(p), resolveExistingPrefix(config.JailWorkspace()))
}

// jailMountsItsOwnFiles reports whether a jail process sees a mount namespace of its own, where only
// the workspace is the host's own copy: true in a container, false in a macos-user sandbox (an
// in-jail process on darwin), which has no mount namespace and no /workspace, so every path it can
// read is the host's own file (PF-D74). A var so a test can be a macos-user sandbox.
var jailMountsItsOwnFiles = func() bool { return goruntime.GOOS != "darwin" }

// notTheHostsCopy reports whether p, read here, may not be the host's own copy of it, so a scratch
// act may not write the host's pack from it: in a container jail, any path outside its workspace.
func notTheHostsCopy(p string) bool {
	return config.InJail() && jailMountsItsOwnFiles() && !inJailWorkspace(p)
}
