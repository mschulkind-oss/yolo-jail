package cli

// patchedseriescheck.go is `yolo pack series check [<pack-dir>] [--onto <ref>] [--online]`
// (docs/reference/patched-forks.md PF-D65): the verdict a launch would reach on a local pack's
// patch series. Patch branches use a current durable check record by default; `--online` keeps the
// disposable scratch-mirror fetch available. Patch series retain their scratch-mirror check.
//
// The act reads the pack as a launch does. It either checks and replays in a disposable scratch
// mirror, or for a patch branch reads its current record and replays from existing mirrors without
// fetching. A missing or stale patch-branch record names `yolo pack update` or a launch; `--online`
// is a throwaway verdict and never populates the durable record. A patch branch merges its resolved
// commit into the newest candidate; a series is replayed as patches.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// scratchBuilds is what follows a clean replay of the newest entry in a scratch act's lines (the
// series check): with no check record to read, it cannot say whether this
// machine's good build already runs the series there, as an explicit act's patchedNotBuilt can.
const scratchBuilds = "a fresh launch builds it, unless its good build already runs this series there"

// patchedScratchStore is the store a scratch act checks and replays through, rooted at dir, the
// scratch mirror's directory: the user's own terminal, as patchedForkStore's, with a scratch act's
// network bound, since a scratch mirror starts empty and its first fetch is a whole blobless clone. A
// var so a test can hand it a git of its own.
var patchedScratchStore = func(dir string) *packsrc.Store {
	return &packsrc.Store{Dir: dir, Timeout: packsrc.ScratchTimeout}
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
		if note := res.Staged.SkippedNote(); note != "" {
			pr.Printf("[dim]%s[/dim]", richtext.Escape(note))
		}
	}
	return p, err
}

// hydrateSelectedWorkspacePackMappings carries only the selected pack tree's host-established relative
// patch-branch identity onto the explicitly named workspace pack. The manifest remains the one loaded
// from dir; a mapping is used only when its recorded workspace-relative source resolves to that exact dir.
func hydrateSelectedWorkspacePackMappings(p *packload.Pack, dir string) {
	if p == nil || p.Decl == nil {
		return
	}
	e := entrypoint.JailEnvFromOS()
	root := e.Getenv("YOLO_PACK_ROOT")
	if root == "" {
		return
	}
	entries, recorded, err := packload.ReadPackTreeRecord(root)
	if err != nil || !recorded {
		return
	}
	resolve := func(path string) (string, bool) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", false
		}
		abs, err := filepath.Abs(resolved)
		if err != nil {
			return "", false
		}
		return filepath.Clean(abs), true
	}
	namedDir, ok := resolve(dir)
	if !ok {
		return
	}
	mappings := make(map[string]packload.PatchBranchMapping)
	for _, entry := range entries {
		for owner, mapping := range entry.PatchBranches {
			stagedSlug, contribution, ok := strings.Cut(owner, "/")
			if !ok || stagedSlug != filepath.Base(filepath.FromSlash(entry.Dir)) || !mapping.Mapped {
				continue
			}
			mappedDir := filepath.Join(e.WorkspaceDir(), filepath.FromSlash(mapping.Directory))
			resolvedMapping, ok := resolve(mappedDir)
			if !ok || resolvedMapping != namedDir {
				continue
			}
			for _, declared := range p.Decl.Contributions() {
				if declared.ExtensionName() != contribution || !packsrc.IsRelativeLocator(declared.PatchBranch) {
					continue
				}
				mappings[p.StagedSlug()+"/"+contribution] = packload.PatchBranchMapping{
					Repository: mapping.Repository, Directory: mappedDir, Mapped: true,
				}
			}
		}
	}
	if len(mappings) != 0 {
		p.PatchBranchMappings = mappings
	}
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
// "." by default, --onto and --online. rc 2 says what is wrong with it.
func parseSeriesCheckArgs(args []string, errw io.Writer) (dir, onto string, online bool, rc int) {
	var dirs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--online":
			online = true
		case a == "--onto":
			if i+1 >= len(args) || args[i+1] == "" {
				fmt.Fprintf(errw, "yolo pack series check: --onto needs a value (see `yolo pack --help`)\n")
				return "", "", false, 2
			}
			i++
			onto = args[i]
		case strings.HasPrefix(a, "--onto="):
			if onto = strings.TrimPrefix(a, "--onto="); onto == "" {
				fmt.Fprintf(errw, "yolo pack series check: --onto needs a value (see `yolo pack --help`)\n")
				return "", "", false, 2
			}
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo pack series check: unknown flag %q — it takes a pack directory, --onto <ref> and --online "+
				"(see `yolo pack --help`)\n", a)
			return "", "", false, 2
		default:
			dirs = append(dirs, a)
		}
	}
	switch len(dirs) {
	case 0:
		return ".", onto, online, 0
	case 1:
		return dirs[0], onto, online, 0
	}
	fmt.Fprintf(errw, "yolo pack series check: unexpected argument %q — it checks one pack directory\n", dirs[1])
	return "", "", false, 2
}

// packSeriesCheck is `yolo pack series check`. See the file doc.
func packSeriesCheck(args []string, out, errw io.Writer, color bool) int {
	dir, onto, online, rc := parseSeriesCheckArgs(args, errw)
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
	hydrateSelectedWorkspacePackMappings(p, abs)
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
	ctx, stop := seriesInterrupt()
	defer stop()
	rc = 0
	for _, f := range forks {
		n := seriesCheckOne(ctx, pr, errw, f, abs, onto, online)
		if ctx.Err() != nil {
			fmt.Fprintf(errw, "yolo pack series check: interrupted — the scratch copy of the upstream is removed\n")
			return 130
		}
		rc = max(rc, n)
	}
	return rc
}

const recordedBranchVerdict = "the recorded merge verdict; no git ran"

// recordedPatchBranchCheck is the offline `series check` path for a patch branch. It reports only a matching
// recorded merge outcome or an accepted good build; it never resolves refs, walks a series, or touches mirrors.
// A missing or stale verdict names the host-side act that can record it; scratch recomputation is `--online` only.
func recordedPatchBranchCheck(ctx context.Context, pr richtext.Printer, errw io.Writer, f packload.Fork,
	packDir, onto string) int {
	if ctx.Err() != nil {
		return 130
	}
	unavailable := func(state string) int {
		fmt.Fprintf(errw, "yolo pack series check: %s: %s — run `yolo pack update` on the host or start a launch to "+
			"record a current verdict; `yolo pack series check --online %s` checks a disposable scratch copy and "+
			"does not populate the record\n", f.Label(), state, shquote.QuoteDisplay(packDir))
		return 1
	}
	series, err := f.DeclaredSeries()
	if err != nil {
		fmt.Fprintf(errw, "yolo pack series check: %s: %v\n", f.Label(), err)
		return 1
	}
	in, _, _, err := f.CheckWant(series).Inputs()
	if err != nil {
		fmt.Fprintf(errw, "yolo pack series check: %s: %v\n", f.Label(), err)
		return 1
	}
	store := patchedForkStore()
	rec, err := store.LoadCheckRecord(f.Key())
	if err != nil {
		if errors.Is(err, packsrc.ErrNoCheckRecord) {
			return unavailable("the patch branch is not yet resolved on this machine")
		}
		return unavailable("the patch branch's check record cannot be read (" + err.Error() + ")")
	}
	if rec.Read != in {
		return unavailable("the recorded verdict is stale because this pack now resolves its patch branch differently")
	}
	if due, why := packsrc.CheckDue(rec, in, patchedNow(), 0); due {
		return unavailable("the recorded verdict is stale (" + why + ")")
	}
	if rec.Check == nil || rec.Check.FetchErr != "" || rec.Check.Problem != "" {
		return unavailable("the last check did not resolve a current patch-branch verdict")
	}
	series, err = packsrc.PatchBranchSeriesWithIdentity(f.PatchBranch, f.PatchBranchDir,
		f.PatchBranchRepoIdentity, f.Source, rec)
	if err != nil {
		return unavailable("the recorded patch-branch resolution is stale")
	}
	pr.Printf("[dim]%s[/dim]", richtext.Escape(f.Label()+": uses the recorded patch-branch check"))
	pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: patch branch %s, merge base with upstream %s",
		f.Label(), series.PatchBranch.Label(), shortSHA(series.Base))))
	printPatchBranchProvenance(pr, f.Label(), rec.PatchBranch)

	var target packsrc.ListEntry
	atBase := false
	if onto != "" {
		for _, entry := range rec.Check.List {
			if onto == entry.Commit || onto == entry.Tag || onto == entry.Version {
				target = entry
				break
			}
		}
		if target.Commit == "" {
			return unavailable("the recorded upstream check has no candidate matching --onto " + onto)
		}
	} else {
		list := rec.Candidates(in)
		if len(list) == 0 && rec.Good != nil && rec.Good.Series == series.Digest {
			good := packsrc.ListEntry{Commit: rec.Good.Commit}
			for _, entry := range rec.Check.List {
				if entry.Commit == rec.Good.Commit {
					good = entry
					break
				}
			}
			pr.Printf("[green]%s[/green]: the recorded good build at upstream %s already merges the patch branch %s cleanly; no newer candidate is pending",
				richtext.Escape(f.Label()), richtext.Escape(good.Label()), richtext.Escape(series.PatchBranch.Label()))
			return 0
		}
		if len(list) == 0 && rec.Check.BaseOnBranch {
			list, atBase = []packsrc.ListEntry{{Commit: series.Base}}, true
		}
		if len(list) == 0 {
			return unavailable("the recorded upstream check has no candidate this patch branch can be built at")
		}
		target = list[0]
	}
	outcome := recordedBranchOutcome(rec, target.Commit, series.Digest)
	if outcome == nil {
		return unavailable("the record has no historical merge verdict for upstream " + target.Label())
	}
	if outcome.Kind != packsrc.OutcomeApplies && outcome.Kind != packsrc.OutcomeConflict {
		return unavailable("the record's outcome for upstream " + target.Label() + " is not a merge verdict")
	}
	pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: reports %s for patch branch %s (recorded by yolo %s, git %s)",
		f.Label(), recordedBranchVerdict, series.PatchBranch.Label(), recordedVersion(outcome.Yolo), recordedVersion(outcome.Git))))
	if outcome.Kind == packsrc.OutcomeConflict {
		paths := strings.Join(outcome.Paths, ", ")
		if paths == "" {
			paths = "(no path named)"
		}
		pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("%s: upstream %s does not merge the patch branch —", f.Label(), target.Label())))
		pr.Printf("%s", richtext.Escape("  conflicts in "+paths))
		for _, line := range f.RebaseRecipe().Repair(target, packsrc.RecipeLead, "  ") {
			pr.Printf("[dim]%s[/dim]", richtext.Escape(line))
		}
		return 1
	}
	what := "upstream " + target.Label()
	if atBase {
		what = "the patch branch's merge base " + target.Label() + " (no version of the branch contains it)"
	}
	pr.Printf("[green]%s[/green]: %s merges the patch branch %s cleanly — %s",
		richtext.Escape(f.Label()), richtext.Escape(what), richtext.Escape(series.PatchBranch.Label()), recordedBranchVerdict)
	return 0
}

func recordedVersion(v string) string {
	if v == "" {
		return "unknown version"
	}
	return v
}

func recordedBranchOutcome(rec *packsrc.CheckRecord, commit, series string) *packsrc.EntryOutcome {
	var found *packsrc.EntryOutcome
	for i := range rec.Outcomes {
		o := &rec.Outcomes[i]
		if o.Commit != commit || o.Series != series || (o.Kind != packsrc.OutcomeConflict && o.Kind != packsrc.OutcomeApplies) {
			continue
		}
		if found == nil || o.At >= found.At {
			found = o
		}
	}
	return found
}

// seriesCheckOne is one patched fork's or extension's check, walk and verdict. packDir is the pack's
// directory, onto --onto's ref or "".
func seriesCheckOne(ctx context.Context, pr richtext.Printer, errw io.Writer, f packload.Fork, packDir, onto string,
	online bool) int {
	if f.PatchBranch != "" && !online {
		return recordedPatchBranchCheck(ctx, pr, errw, f, packDir, onto)
	}
	again := seriesCheckLine(packDir, onto, online)
	var series *packsrc.Series
	var err error
	if f.PatchBranch != "" {
		// A PATCH BRANCH is the declared one here, never this machine's record of it: the scratch check below
		// fetches the branch and resolves its commit and merge base, which the walk then merges.
		series, err = f.DeclaredSeries()
	} else {
		series, err = f.ReadSeries()
	}
	if err != nil {
		pr.Printf("[yellow]%s[/yellow]", richtext.Escape("⚠ "+f.Label()+": "+err.Error()))
		return 1
	}
	if f.PatchBranch != "" {
		pr.Printf("[dim]%s[/dim]", richtext.Escape(f.Label()+": merges the patch branch "+f.PatchBranch))
	} else {
		pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: %d %s in %s (series %s), base %s", f.Label(), series.Len(),
			plural(series.Len(), "patch", "patches"), series.Dir, series.ShortDigest(), shortSHA(series.Base))))
	}
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
		what := f.Source + " "
		if f.PatchBranch != "" {
			what = "" // the error names which of the upstream and the patch branch it could not fetch
		}
		fmt.Fprintf(errw, "yolo pack series check: %s: could not fetch %sinto a scratch copy (%s) — check the "+
			"address and this machine's network, then run `%s` again\n", f.Label(), what, found.FetchErr, again)
		return 1
	}
	if found.Problem != "" {
		if onto == "" || f.PatchBranch != "" {
			// A PATCH BRANCH THE CHECK COULD NOT RESOLVE has no commit to merge, onto anything.
			fmt.Fprintf(errw, "yolo pack series check: %s: %s\n", f.Label(), found.Problem)
			return 1
		}
		fmt.Fprintf(errw, "yolo pack series check: %s: %s — checking --onto %s anyway\n", f.Label(), found.Problem, onto)
	}
	if f.PatchBranch != "" {
		// THE PATCH BRANCH AS THIS CHECK RESOLVED IT, in the scratch record: its commit is what the walk merges.
		resolved, err := packsrc.PatchBranchSeriesWithIdentity(f.PatchBranch, f.PatchBranchDir,
			f.PatchBranchRepoIdentity, f.Source, res.Record)
		if err != nil {
			fmt.Fprintf(errw, "yolo pack series check: %s: %v — run `%s` again\n", f.Label(), err, again)
			return 1
		}
		series = resolved
		pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: patch branch %s, merge base with upstream %s",
			f.Label(), series.PatchBranch.Label(), shortSHA(series.Base))))
		printPatchBranchProvenance(pr, f.Label(), res.Record.PatchBranch)
	}
	repo, subdir := mustRepo(f.Source), subdirOf(f.Source)
	var list []packsrc.ListEntry
	atBase := false
	switch {
	case onto != "":
		target, err := store.ResolveOnto(repo, onto, nil)
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
	// STOPS AT THE FIRST CONFLICT, as a launch's walk and `yolo pack update`'s do since PF-D81: a
	// series the newest version does not take is a failed operation there, and no older fit or base
	// is built, so naming one as what a launch builds would be a verdict no launch reaches.
	w := store.WalkSeries(repo, subdir, series, list, packsrc.WalkOptions{Timeout: packsrc.ScratchTimeout,
		StopOnPatchFailure: true})
	if ctx.Err() != nil {
		return 130
	}
	lines, clean := seriesCheckReport(f, packDir, series, w, atBase, onto, online, scratchBuilds)
	for _, line := range lines {
		pr.Printf("%s", line)
	}
	if clean {
		return 0
	}
	return 1
}

// seriesCheckReport is the verdict's lines and whether the first entry it replayed — the newest,
// --onto's, or the base — took the series. buildNote describes what the clean result means at this path.
func seriesCheckReport(f packload.Fork, packDir string, series *packsrc.Series, w packsrc.WalkResult,
	atBase bool, onto string, online bool, buildNote string) ([]string, bool) {
	again := "`" + seriesCheckLine(packDir, onto, online) + "`"
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
	// A PATCH BRANCH is merged, not replayed (packsrc/patchbranch.go), and its lines say so.
	branch := series.PatchBranch != nil
	var lines []string
	for i, r := range w.Results {
		switch {
		case r.Conflict != nil:
			paths := strings.Join(r.Conflict.Paths, ", ")
			if paths == "" {
				paths = "(no path named)"
			}
			takes := "take the patch series"
			if branch {
				takes = "merge the patch branch"
			}
			lines = append(lines,
				"[yellow]"+richtext.Escape(f.Label()+": upstream "+r.Entry.Label()+" does not "+takes+" —")+"[/yellow]",
				richtext.Escape("  "+r.Conflict.Member+" conflicts in "+paths))
			lines = append(lines, scratchRecipeLines(f, packDir, r.Entry)...)
		case r.Clean:
			for _, m := range r.Upstream {
				lines = append(lines, "[dim]"+richtext.Escape(fmt.Sprintf("  %s is already in upstream %s; drop it from %s",
					m, r.Entry.Label(), series.Dir))+"[/dim]")
			}
			for _, m := range r.Empty {
				lines = append(lines, "[dim]"+richtext.Escape(fmt.Sprintf("  %s was empty when written and makes no change of its own; drop it from %s",
					m, series.Dir))+"[/dim]")
			}
			what := "upstream " + r.Entry.Label()
			switch {
			case atBase && branch:
				what = "the patch branch's merge base " + r.Entry.Label() + " (no version of the branch contains it)"
			case atBase:
				what = "the series' base " + r.Entry.Label() + " (no version of the branch contains it)"
			case i > 0:
				what = "the newest fit, " + what + ","
			}
			tail := ""
			if onto == "" {
				tail = " — " + buildNote
			}
			if branch {
				lines = append(lines, "[green]"+richtext.Escape(f.Label())+"[/green]: "+richtext.Escape(fmt.Sprintf(
					"%s merges the patch branch %s cleanly%s", what, series.PatchBranch.Label(), tail)))
				continue
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
		// PF-D81: the conflict stops a launch; it neither keeps an older build as if nothing failed
		// nor builds an older version or the series' base in its place.
		repair := "the series' base until the series is rebased"
		if branch {
			repair = "the patch branch's merge base until the upstream is merged into the patch branch"
		}
		lines = append(lines, warn("a launch stops at this conflict and builds no older version or "+repair)...)
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
func seriesCheckLine(packDir, onto string, online bool) string {
	argv := []string{"yolo", "pack", "series", "check", packDir}
	if onto != "" {
		argv = append(argv, "--onto", onto)
	}
	if online {
		argv = append(argv, "--online")
	}
	return shquote.JoinDisplay(argv)
}

// scratchRecipeLines is the check's conflict step: the rebase recipe onto e (§8.4, PF-D85), the
// commit this check met, exporting into the series in packDir. In a container jail, for a pack
// outside the jail's workspace, that directory is not the host's own copy, so it says to export
// into the host's.
func scratchRecipeLines(f packload.Fork, packDir string, e packsrc.ListEntry) []string {
	var out []string
	if f.PatchBranch == "" && notTheHostsCopy(packDir) { // a patch branch is repaired in its own repository
		out = append(out, "[dim]"+richtext.Escape("  "+packDir+" is outside this jail's workspace, so it is not the "+
			"host's own copy of the pack: run the export below on the host, against the host's copy")+"[/dim]")
	}
	for _, l := range f.RebaseRecipe().Repair(e, packsrc.RecipeLead, "  ") {
		out = append(out, "[dim]"+richtext.Escape(l)+"[/dim]")
	}
	return out
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

// seriesInterrupt is the context the check's git runs under: ended by a Ctrl-C, a SIGTERM or a
// SIGHUP (the terminal or the ssh session closed), so the scratch copy of the upstream is removed
// rather than left behind. A var so a test can stand one in.
var seriesInterrupt = func() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}

// resolveExistingPrefix resolves the symlinks of p's longest existing prefix and joins the rest,
// so a directory not made yet compares against resolved roots as the one it will be (a darwin temp
// directory is a link, AGENTS.md's PATH-resolution class).
func resolveExistingPrefix(p string) string {
	rest := ""
	for cur := filepath.Clean(p); ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// underOrEqual reports whether child is base or inside it.
func underOrEqual(child, base string) bool {
	rel, err := filepath.Rel(base, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
