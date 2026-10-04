package cli

// patchedrebase.go is `yolo pack rebase <pack>/<bin> [--onto <ref>] [--into <dir>] [--restart]`
// (docs/design/patched-forks.md §8.4, PF-D13, PF-D26, PF-D47): the next step every conflict of a
// PATCHED FORK names. It reproduces the replay where the user can work on it: a REBASE CLONE of the
// upstream (packsrc/rebase.go, which coins the term), outside yolo's state directory, with the
// series replayed onto the target and, when a member conflicts, left mid-rebase with the markers in
// its work tree. It prints the continue command and the export that replaces the series, with the
// commits filled in, and runs neither: IT WRITES NOTHING IN THE PACK (§5.1's IMPORTANT note), whose
// patch directory is the user's source of truth.
//
// The act, in order:
//
//  1. HOST ONLY: a jail has neither the fork's mirror nor its pack, so in a jail it names the
//     command to run on the host.
//  2. The clone's directory is settled before any network: `--into`, by default
//     ./<pack>-<bin>-rebase. It is refused where a workspace could not be (the home itself, or
//     inside yolo's config or state directory) and inside the fork pack's own directory, and when it
//     exists and is not this fork's clone. On its own clone it says so, prints that clone's next
//     steps again, and names `--restart`, which removes it and starts over.
//  3. It FORCES THE CHECK (§4.1), as `yolo pack update` does, and picks the target: `--onto`, else
//     the newest entry the next fresh launch's advance would walk to — the held or pending
//     candidate, or the good build's own commit for an edited series with nothing newer.
//  4. The clone, the replay, and the clean or conflicting answer (packsrc.Store.RebaseClone).

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// rebaseArgs is the verb's command line.
type rebaseArgs struct {
	key, onto, into string
	restart         bool
}

// parseRebaseArgs reads the verb's command line, or says what is wrong with it (exit 2).
func parseRebaseArgs(args []string, errw io.Writer) (rebaseArgs, int) {
	var ra rebaseArgs
	value := func(i *int, flag string) (string, bool) {
		if *i+1 >= len(args) {
			fmt.Fprintf(errw, "yolo pack rebase: %s needs a value (see `yolo pack --help`)\n", flag)
			return "", false
		}
		*i++
		return args[*i], true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		ok := true
		switch {
		case a == "--onto":
			ra.onto, ok = value(&i, a)
		case strings.HasPrefix(a, "--onto="):
			ra.onto = strings.TrimPrefix(a, "--onto=")
		case a == "--into":
			ra.into, ok = value(&i, a)
		case strings.HasPrefix(a, "--into="):
			ra.into = strings.TrimPrefix(a, "--into=")
		case a == "--restart":
			ra.restart = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo pack rebase: unknown flag %q — it takes --onto <ref>, --into <dir> and "+
				"--restart (see `yolo pack --help`)\n", a)
			return ra, 2
		case ra.key != "":
			fmt.Fprintf(errw, "yolo pack rebase: unexpected argument %q — it rebases one patched fork, "+
				"named as <pack>/<bin>\n", a)
			return ra, 2
		default:
			ra.key = a
		}
		if !ok {
			return ra, 2
		}
	}
	if (ra.onto == "" && containsFlag(args, "--onto")) || (ra.into == "" && containsFlag(args, "--into")) {
		fmt.Fprintf(errw, "yolo pack rebase: --onto and --into each need a value (see `yolo pack --help`)\n")
		return ra, 2
	}
	return ra, 0
}

// containsFlag reports whether args spell flag, either way.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// rebaseInterrupt is the context the verb's git runs under: ended by a Ctrl-C or a SIGTERM, so
// an interrupted clone is removed rather than left half made. A var so a test can stand one in.
var rebaseInterrupt = func() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// rebaseCloneRun makes the rebase clone (packsrc.Store.RebaseClone); a var so a test can end the
// verb's interrupt context while it runs.
var rebaseCloneRun = func(s *packsrc.Store, o packsrc.RebaseOptions) packsrc.RebaseResult { return s.RebaseClone(o) }

// packRebase is `yolo pack rebase`. See the file doc.
func packRebase(args []string, out, errw io.Writer, color bool) int {
	ra, rc := parseRebaseArgs(args, errw)
	if rc != 0 {
		return rc
	}
	key := ra.key
	if key == "" {
		key = "<pack>/<bin>"
	}
	if config.InJail() {
		fmt.Fprintf(errw, "yolo pack rebase: a patched fork's upstream mirror, its check record and its "+
			"pack live on the host, not in this jail — run `yolo pack rebase %s` in a terminal on the host\n", key)
		return 1
	}
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		fmt.Fprintf(errw, "yolo pack rebase: %v\n", sel.loadErr)
		return 1
	}
	forks := packload.Forks(sel.packs)
	f, rc := pickRebaseFork(forks, ra.key, errw)
	if rc != 0 {
		return rc
	}
	pr := richtext.Printer{W: out, Color: color}
	origin := forkPackOriginOf(f)

	// 2. THE CLONE'S DIRECTORY, settled before anything reaches the network.
	dir, rc := rebaseDir(f, origin, ra.into, errw)
	if rc != 0 {
		return rc
	}
	state, marker, err := packsrc.InspectRebaseDir(dir)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: reading %s: %v — name another directory with --into <dir>\n", dir, err)
		return 1
	}
	switch {
	case state == packsrc.RebaseDirClone && marker.Owner != f.Key():
		fmt.Fprintf(errw, "yolo pack rebase: %s is the rebase clone of fork %s, not of %s — name another "+
			"directory with --into <dir>\n", dir, marker.Owner, f.Key())
		return 1
	case state == packsrc.RebaseDirClone && !ra.restart:
		for _, line := range ownCloneLines(f, origin, dir, marker) {
			pr.Printf("%s", line)
		}
		return 1
	case state == packsrc.RebaseDirClone:
		if err := packsrc.RemoveRebaseClone(dir, f.Key()); err != nil {
			fmt.Fprintf(errw, "yolo pack rebase: removing the old rebase clone: %v\n", err)
			return 1
		}
		pr.Printf("[dim]%s[/dim]", richtext.Escape("removed the old rebase clone "+dir+", to start over"))
	case state == packsrc.RebaseDirOther:
		fmt.Fprintf(errw, "yolo pack rebase: %s exists and is not a rebase clone of fork %s, so it is left "+
			"alone — name another directory with --into <dir>, or remove it if an interrupted rebase left it\n",
			dir, f.Key())
		return 1
	}

	series, err := f.ReadSeries()
	if err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: fork %s: %v\n", f.Key(), err)
		return 1
	}
	store := patchedForkStore()

	// 3. THE CHECK, forced, and the target.
	res := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: patchedNow,
		Begin: func() (func(string), func()) {
			pr.Printf("[dim]%s[/dim]", richtext.Escape("checking fork "+f.Key()+"'s upstream "+f.Source))
			return func(line string) { pr.Printf("[dim]%s[/dim]", richtext.Escape(line)) }, func() {}
		}})
	if res.Err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: fork %s: %v\n", f.Key(), res.Err)
		return 1
	}
	rec, found := res.Record, res.Record.Check
	if found.FetchErr != "" {
		fmt.Fprintf(errw, "yolo pack rebase: fork %s: could not fetch %s (%s) — using this machine's copy\n",
			f.Key(), f.Source, found.FetchErr)
	}
	if found.Problem != "" {
		if ra.onto == "" {
			fmt.Fprintf(errw, "yolo pack rebase: fork %s: %s\n", f.Key(), found.Problem)
			return 1
		}
		fmt.Fprintf(errw, "yolo pack rebase: fork %s: %s — rebasing onto --onto %s anyway\n", f.Key(),
			found.Problem, ra.onto)
	}
	repo, subdir := mustRepo(f.Source), subdirOf(f.Source)
	list := rec.Candidates(res.Inputs)
	target, hasTarget := rebaseDefaultTarget(rec, res.Inputs, series.Digest)
	switch {
	case ra.onto != "":
		target, err = store.ResolveRebaseTarget(repo, ra.onto, func(line string) {
			pr.Printf("[dim]%s[/dim]", richtext.Escape(line))
		})
		if err != nil {
			fmt.Fprintf(errw, "yolo pack rebase: fork %s: %v — name a branch, a tag or a commit of the upstream\n",
				f.Key(), err)
			return 1
		}
	case hasTarget:
	case rec.Good != nil:
		pr.Printf("%s", richtext.Escape("fork "+f.Key()+": nothing upstream is newer than the good build "+
			goodLabel(rec.Good)+", which runs this series — no rebase is needed; `--onto <ref>` rebases it "+
			"onto another upstream commit"))
		return 0
	case found.BaseOnBranch:
		pr.Printf("%s", richtext.Escape("fork "+f.Key()+": no version of the branch is newer than the series' "+
			"base "+shortSHA(series.Base)+", which the next fresh launch builds — no rebase is needed; "+
			"`--onto <ref>` rebases it onto another upstream commit"))
		return 0
	default:
		fmt.Fprintf(errw, "yolo pack rebase: fork %s: %s names nothing this series can be rebased onto — "+
			"name a commit with --onto <ref>\n", f.Key(), f.Source)
		return 1
	}
	if hold := patchedForkHold(f); hold != "" {
		pr.Printf("[dim]%s[/dim]", richtext.Escape("  "+hold+": no launch checks its upstream, so a launch "+
			"builds the series at the good build's commit until the hold lifts"))
	}

	// 4. THE CLONE AND THE REPLAY, under an interrupt context from here on, so a Ctrl-C ends the
	// clone's git and the clone is removed rather than left half made. Not before: the check and the
	// --onto lookup wait on pack-store flocks, which no context ends, and a Ctrl-C there should end
	// the verb as it always has.
	ctx, stop := rebaseInterrupt()
	defer stop()
	store.Ctx = ctx
	pr.Printf("[dim]%s[/dim]", richtext.Escape("cloning "+repo+" into "+dir+" to rebase fork "+f.Key()+
		"'s series onto upstream "+target.Label()))
	rr := rebaseCloneRun(store, packsrc.RebaseOptions{Owner: f.Key(), Repo: repo, Subdir: subdir, Series: series,
		Target: target, Dir: dir, Now: patchedNow()})
	if ctx.Err() != nil && !rr.Kept {
		fmt.Fprintf(errw, "yolo pack rebase: interrupted — the rebase clone %s is removed\n", dir)
		return 130
	}
	switch {
	case rr.Base != nil:
		fmt.Fprintf(errw, "yolo pack rebase: fork %s: %s\n", f.Key(), rr.Base.Error())
		return 1
	case rr.Err != nil:
		fmt.Fprintf(errw, "yolo pack rebase: fork %s: could not rebase the series onto %s: %v — the clone is "+
			"removed, and `yolo pack rebase %s` tries again\n", f.Key(), target.Label(), rr.Err, f.Key())
		return 1
	case rr.Clean:
		for _, line := range rebaseCleanLines(f, rec, res.Inputs, list, series, target, rr) {
			pr.Printf("%s", line)
		}
		return 0
	}
	paths := strings.Join(rr.Conflict.Paths, ", ")
	if paths == "" {
		paths = "(no path named)"
	}
	pr.Printf("[yellow]%s[/yellow]", richtext.Escape("fork "+f.Key()+": upstream "+target.Label()+
		" does not take the patch series — the rebase stopped in "+dir))
	pr.Printf("%s", richtext.Escape("  "+rr.Conflict.Member+" conflicts in "+paths))
	for _, line := range rebaseNextSteps(f, origin, dir, target, true) {
		pr.Printf("%s", line)
	}
	return 1
}

// rebaseDefaultTarget is what `yolo pack rebase` rebases onto with no --onto (§8.4 step 2): the
// newest entry the next fresh launch's advance would walk to — the newest candidate above the good
// build, held or pending — or, for a series edited since the good build with nothing newer, the
// good build's own commit, which that launch replays it at first (PF-D40). false when there is
// nothing to rebase onto: the good build runs this very series at the newest commit, or no version
// is newer than the series' base. Every conflict line's step names the verb bare for this entry
// and with --onto for any other (rebaseCommand).
func rebaseDefaultTarget(rec *packsrc.CheckRecord, in packsrc.CheckInputs, series string) (packsrc.ListEntry, bool) {
	if list := rec.Candidates(in); len(list) > 0 {
		return list[0], true
	}
	if rec != nil && rec.Good != nil && rec.Good.Series != series {
		return goodEntry(rec.Good), true
	}
	return packsrc.ListEntry{}, false
}

// isRebaseDefault reports whether entry is what `yolo pack rebase` takes with no --onto.
func isRebaseDefault(rec *packsrc.CheckRecord, in packsrc.CheckInputs, series string, entry packsrc.ListEntry) bool {
	t, ok := rebaseDefaultTarget(rec, in, series)
	return ok && t.Commit == entry.Commit
}

// pickRebaseFork is the patched fork key names among forks, or why there is none (with the exit
// status): a key is required, since each conflict line names it, and a wrong one is answered with
// the keys there are.
func pickRebaseFork(forks []packload.Fork, key string, errw io.Writer) (packload.Fork, int) {
	var patched []string
	for _, f := range forks {
		if f.Patched() {
			patched = append(patched, f.Key())
		}
	}
	there := "no patched fork is selected (a fork pack's program that declares `patches`)"
	if len(patched) > 0 {
		there = "the selected patched " + plural(len(patched), "fork is ", "forks are ") + strings.Join(patched, ", ")
	}
	if key == "" {
		fmt.Fprintf(errw, "yolo pack rebase: name the patched fork to rebase, as <pack>/<bin> — %s\n", there)
		return packload.Fork{}, 2
	}
	for _, f := range forks {
		if f.Key() != key {
			continue
		}
		if !f.Patched() {
			fmt.Fprintf(errw, "yolo pack rebase: fork %s is a plain fork, which builds its own fork repository "+
				"at the commit its pin names — rebase that repository, and `yolo pack update` moves the pin\n", key)
			return packload.Fork{}, 1
		}
		return f, 0
	}
	fmt.Fprintf(errw, "yolo pack rebase: no selected fork is %s — %s\n", key, there)
	return packload.Fork{}, 1
}

// forkPackOrigin is where a fork pack's own files come from, which decides where a rebased series
// is exported to: a LOCAL pack's patch directory is the user's own, named in place; a FETCHED
// pack's copy on this machine is the pack store's, which no act writes, so the export goes into a
// clone of the pack's repository and is pushed.
type forkPackOrigin struct {
	// local is a local pack's directory, "" otherwise.
	local string
	// fetched is a fetched pack's address, nil otherwise.
	fetched *packsrc.Addr
}

// forkPackOriginOf reads the fork pack's `packs` entry. A pack no entry names (one yolo ships,
// joined through the closure) is neither.
func forkPackOriginOf(f packload.Fork) forkPackOrigin {
	entries, _, err := config.LoadPackEntries()
	if err != nil {
		return forkPackOrigin{}
	}
	for _, e := range entries {
		if e.Name != f.Pack || e.Embedded() {
			continue
		}
		a, err := packsrc.Parse(e.Source)
		if err != nil {
			return forkPackOrigin{}
		}
		if a.IsLocal() {
			return forkPackOrigin{local: a.Path}
		}
		return forkPackOrigin{fetched: &a}
	}
	return forkPackOrigin{}
}

// patches is the series directory a local pack's export replaces, "" for any other pack.
func (o forkPackOrigin) patches(f packload.Fork) string {
	if o.local == "" {
		return ""
	}
	return filepath.Join(o.local, filepath.FromSlash(f.Patches))
}

// rebaseDir is the rebase clone's directory, absolute, or why it may not be (with the exit
// status): never where a workspace could not be, nor inside the fork pack's own directory.
func rebaseDir(f packload.Fork, origin forkPackOrigin, into string, errw io.Writer) (string, int) {
	if into == "" {
		into = strings.NewReplacer("/", "-", string(filepath.Separator), "-").Replace(f.Pack+"-"+f.Bin) + "-rebase"
	}
	abs, err := filepath.Abs(into)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: %v\n", err)
		return "", 1
	}
	resolved := resolveExistingPrefix(abs)
	// The rule a workspace's is, with no exemption: the capture store's is a capture's own staging
	// tree, and a rebase clone is no capture's (PF-D47). Asked of the path as written and as its
	// existing prefix resolves: the rule resolves a root only when it exists, so a state directory not
	// made yet under a home reached through a link is compared unresolved.
	for _, p := range []string{abs, resolved} {
		if b := paths.WritableSourceScopeBreach(p); b != nil {
			fmt.Fprintf(errw, "yolo pack rebase: %s — the rebase clone goes where a workspace could be: "+
				"name another directory with --into <dir>\n", b.WhatFor("the rebase clone"))
			return "", 1
		}
	}
	for _, root := range []string{origin.local, f.Root} {
		if root == "" {
			continue
		}
		if r := resolveExistingPrefix(root); underOrEqual(resolved, r) {
			fmt.Fprintf(errw, "yolo pack rebase: the rebase clone %s would be inside fork pack %s's own "+
				"directory %s, which yolo never writes — name a directory outside it with --into <dir>\n",
				abs, f.Pack, root)
			return "", 1
		}
	}
	return abs, 0
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

// rebaseCleanLines say that the series takes the target as it stands, what the next fresh launch
// does about it, and each member already upstream. in is what the check read, list its candidates.
func rebaseCleanLines(f packload.Fork, rec *packsrc.CheckRecord, in packsrc.CheckInputs, list []packsrc.ListEntry,
	series *packsrc.Series, target packsrc.ListEntry, rr packsrc.RebaseResult) []string {
	next := "a fork builds it only once its ?ref= and follow rule name it"
	switch {
	case isRebaseDefault(rec, in, series.Digest, target):
		next = patchedNotBuilt
	case onList(list, target.Commit):
		next = "the next fresh launch builds it unless a newer version on its list takes the series"
	case rec.Good != nil && rec.Good.Commit == target.Commit && rec.Good.Series == series.Digest:
		next = "the good build already runs it"
	}
	lines := []string{"[green]fork " + richtext.Escape(f.Key()) + "[/green]: " + richtext.Escape(fmt.Sprintf(
		"upstream %s takes the series as it stands (%d %s, series %s) — %s; nothing to rebase, so the "+
			"clone is removed", target.Label(), series.Len(), plural(series.Len(), "patch", "patches"),
		series.ShortDigest(), next))}
	for _, m := range rr.Upstream {
		lines = append(lines, "[dim]"+richtext.Escape(fmt.Sprintf("  %s is already in upstream %s; drop it from %s",
			m, target.Label(), series.Dir))+"[/dim]")
	}
	return lines
}

// ownCloneLines are what a second `yolo pack rebase` says on its own clone: that it is there and
// what it was rebased onto, its next steps again, and `--restart`.
func ownCloneLines(f packload.Fork, origin forkPackOrigin, dir string, m *packsrc.RebaseMarker) []string {
	made := patchedAge(patchedNow().Sub(time.Unix(m.At, 0)))
	lines := []string{"[yellow]" + richtext.Escape("fork "+f.Key()+": "+dir+" is its rebase clone, made "+made+
		" ago onto upstream "+m.Target.Label()) + "[/yellow]"}
	lines = append(lines, rebaseNextSteps(f, origin, dir, m.Target, packsrc.RebaseInProgress(dir))...)
	return append(lines, "[dim]"+richtext.Escape("  `yolo pack rebase "+f.Key()+" --restart` removes it and "+
		"rebases the series again from the start")+"[/dim]")
}

// rebaseNextSteps are a stopped rebase's next steps, in order (§8.4 steps 6 and 7): the continue,
// to run after resolving; the export that replaces the series with no moment where it is empty or
// partial — into a local pack's own patch directory, or through a clone of a fetched pack's
// repository, committed and pushed; what then builds it; and that an agent can resolve it in a jail
// started there. Every path is quoted for the shell it is pasted into.
func rebaseNextSteps(f packload.Fork, origin forkPackOrigin, dir string, target packsrc.ListEntry,
	inProgress bool) []string {
	q := shquote.QuoteDisplay
	var lines []string
	cmd := func(s string) { lines = append(lines, "    "+richtext.Escape(s)) }
	say := func(s string) { lines = append(lines, richtext.Escape("  "+s)) }
	then := "then "
	if inProgress {
		say("resolve the conflict there, then continue — again for each member that stops:")
		cmd("git -C " + q(dir) + " rebase --continue")
	} else {
		say("the rebase there is done")
		then = ""
	}
	// export is the format-patch into <patches>.new and the two renames, the one moment a reader
	// finds no directory being between them (§8.4: never an empty or a partial series).
	export := func(patches string) {
		for _, left := range []string{patches + ".new", patches + ".old"} {
			if _, err := os.Lstat(left); err == nil {
				say("first remove " + q(left) + ", which an earlier export left, or the series takes its files too:")
			}
		}
		cmd("git -C " + q(dir) + " format-patch --base=" + target.Commit + " -o " + q(patches+".new") + " " +
			target.Commit + "..HEAD")
		cmd("mv " + q(patches) + " " + q(patches+".old") + " && mv " + q(patches+".new") + " " + q(patches) +
			" && rm -r " + q(patches+".old"))
	}
	switch {
	case origin.local != "":
		say(then + "replace the series with the rebased one:")
		export(origin.patches(f))
		say("the next fresh launch builds the new series")
	case origin.fetched != nil:
		a := *origin.fetched
		clone := filepath.Join(filepath.Dir(dir), strings.NewReplacer("/", "-").Replace(f.Pack)+"-pack")
		rel := filepath.FromSlash(f.Patches)
		if a.Path != "" {
			rel = filepath.Join(filepath.FromSlash(a.Path), rel)
		}
		say(then + "publish the rebased series: fork pack " + f.Pack + " is fetched from " + a.Repo + ", and " +
			"yolo never writes its copy on this machine, so export into a clone of that repository (or your own) " +
			"and push it:")
		kind := patchedForkStore().RefKind(a)
		branch := ""
		if kind == "branch" {
			branch = " -b " + q(a.Ref)
		}
		cmd("git clone" + branch + " " + q(a.Repo) + " " + q(clone))
		export(filepath.Join(clone, rel))
		cmd("git -C " + q(clone) + " add -A " + q(rel) + " && git -C " + q(clone) + " commit -m " +
			q("Rebase the patch series onto "+target.Label()) + " && git -C " + q(clone) + " push")
		switch kind {
		case "branch":
			say("the pack's refresh at a launch brings the pushed series within the hour, or `yolo pack update` " +
				"now; the next fresh launch then builds it")
		case "tag", "commit":
			say("your config holds pack " + f.Pack + " at ?ref=" + a.Ref + ": point its ?ref= at the pushed " +
				"commit, and the next fresh launch builds it")
		default:
			say("a branch ?ref= brings the pushed series within the hour, or `yolo pack update` now; a tag or " +
				"commit ?ref= takes it once pointed at the pushed commit")
		}
	default:
		say(then + "export it into a fork pack of your own — fork pack " + f.Pack + " is none of your `packs` " +
			"entries, so there is no patch directory of yours to replace:")
		cmd("git -C " + q(dir) + " format-patch --base=" + target.Commit + " -o <your pack>/patches " +
			target.Commit + "..HEAD")
	}
	if inProgress {
		say("an agent can resolve it in a jail started there: cd " + q(dir) + " && yolo")
	}
	return lines
}
