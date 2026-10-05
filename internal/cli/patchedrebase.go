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
//  1. HOST ONLY, keyed: a jail has neither the fork's mirror, its check record nor its pack, so in
//     a jail it names the command to run on the host, and the SCRATCH REBASE, `--pack <dir>`, which
//     rebases a local pack inside the jail's workspace there (patchedrebasescratch.go, PF-D61).
//  2. The clone's directory is settled before any network: `--into`, by default
//     ./<pack>-<bin>-rebase. It is refused where a workspace could not be (the home itself, or
//     inside yolo's config or state directory) and inside the fork pack's own directory, and when it
//     exists and is not this fork's clone; and while another run holds its rebase directory lock
//     (packsrc.Store.TryLockRebaseDir), which this run then holds to its end. On its own clone it
//     says so and prints that clone's next steps again — the continue while the rebase is stopped,
//     the export once it is finished (packsrc.Store.RebaseFinished), and for an aborted rebase, or a
//     clone a run left before its replay ended, that there is nothing to export — and names
//     `--restart`, on the command line the user typed, which removes it and starts over.
//  3. It FORCES THE CHECK (§4.1), as `yolo pack update` does, and picks the target: `--onto`, else
//     the newest entry the next fresh launch's advance would walk to — the held or pending
//     candidate, or the good build's own commit for an edited series with nothing newer.
//  4. The clone, the replay, and the clean or conflicting answer (packsrc.Store.RebaseClone).

import (
	"context"
	"errors"
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

// rebaseArgs is the verb's command line. pack is --pack's directory, the scratch rebase's.
type rebaseArgs struct {
	key, onto, into, pack string
	restart               bool
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
		case a == "--pack":
			ra.pack, ok = value(&i, a)
		case strings.HasPrefix(a, "--pack="):
			ra.pack = strings.TrimPrefix(a, "--pack=")
		case a == "--restart":
			ra.restart = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo pack rebase: unknown flag %q — it takes --onto <ref>, --into <dir>, --pack <dir> "+
				"and --restart (see `yolo pack --help`)\n", a)
			return ra, 2
		case ra.key != "":
			fmt.Fprintf(errw, "yolo pack rebase: unexpected argument %q — it rebases one patched fork or extension, "+
				"named as <pack>/<bin> or <pack>/<name>\n", a)
			return ra, 2
		default:
			ra.key = a
		}
		if !ok {
			return ra, 2
		}
	}
	if (ra.onto == "" && containsFlag(args, "--onto")) || (ra.into == "" && containsFlag(args, "--into")) ||
		(ra.pack == "" && containsFlag(args, "--pack")) {
		fmt.Fprintf(errw, "yolo pack rebase: --onto, --into and --pack each need a value (see `yolo pack --help`)\n")
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

// rebaseInterrupt is the context the verb's git runs under: ended by a Ctrl-C, a SIGTERM or a
// SIGHUP (the terminal or the ssh session closed), so an interrupted clone is removed rather than
// left half made. A var so a test can stand one in.
var rebaseInterrupt = func() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}

// rebaseCloneRun makes the rebase clone (packsrc.Store.RebaseClone); a var so a test can end the
// verb's interrupt context while it runs.
var rebaseCloneRun = func(s *packsrc.Store, o packsrc.RebaseOptions) packsrc.RebaseResult { return s.RebaseClone(o) }

// removeRebaseClone removes the verb's own clone for --restart (packsrc.RemoveRebaseClone); a var
// so a test can make the removal fail.
var removeRebaseClone = packsrc.RemoveRebaseClone

// rebaseSite is where a rebase runs, which changes what its lines say: the keyed verb on the host,
// reading the selected pack and this machine's check record; or the SCRATCH REBASE
// (patchedrebasescratch.go, which coins the term), which reads the pack in --pack's directory and
// checks its upstream in a scratch mirror with no check record, in a jail or on the host.
type rebaseSite struct {
	// scratch is the scratch rebase; jail is a run in a jail.
	scratch, jail bool
}

// packRebase is `yolo pack rebase`. See the file doc.
func packRebase(args []string, out, errw io.Writer, color bool) int {
	ra, rc := parseRebaseArgs(args, errw)
	if rc != 0 {
		return rc
	}
	if ra.pack != "" {
		return packRebaseScratch(ra, args, out, errw, color)
	}
	if config.InJail() {
		fmt.Fprintf(errw, "yolo pack rebase: a patched fork's or extension's upstream mirror, its check record and its "+
			"pack live on the host, not in this jail — run `%s` in a terminal on the host; for a local pack inside "+
			"this jail's workspace, `%s` rebases it here, with a scratch copy of the upstream\n",
			rebaseCommandLine(ra, args), rebaseCommandLine(ra, args)+" --pack <the pack's directory>")
		return 1
	}
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		fmt.Fprintf(errw, "yolo pack rebase: %v\n  fix what it names in %s, then run `%s` again\n", sel.loadErr,
			paths.UserConfigPath(), rebaseCommandLine(ra, args))
		return 1
	}
	// A PATCHED EXTENSION is rebased as a patched fork is, addressed by its owner key <pack>/<name>
	// (patched-extensions.md §9): every conflict line of its check names this verb with that key.
	forks := append(packload.Forks(sel.packs), packload.PatchedTrees(sel.packs)...)
	f, rc := pickRebaseFork(forks, ra.key, "", errw)
	if rc != 0 {
		return rc
	}
	pr := richtext.Printer{W: out, Color: color}
	origin := forkPackOriginOf(f)
	site := rebaseSite{}

	// 2. THE CLONE'S DIRECTORY, settled before anything reaches the network, and held.
	dir, rc := rebaseDir(f, origin, ra.into, errw)
	if rc != 0 {
		return rc
	}
	store := patchedForkStore()
	unlock, rc, proceed := claimRebaseDir(pr, errw, store, f, origin, dir, ra, args, site)
	if !proceed {
		return rc
	}
	defer unlock()

	series, err := f.ReadSeries()
	if err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: %s: %v\n", f.Label(), err)
		return 1
	}

	// 3. THE CHECK, forced, and the target.
	res := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: patchedNow,
		Begin: func() (func(string), func()) {
			pr.Printf("[dim]%s[/dim]", richtext.Escape("checking "+f.Label()+"'s upstream "+f.Source))
			return func(line string) { pr.Printf("[dim]%s[/dim]", richtext.Escape(line)) }, func() {}
		}})
	if res.Err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: %s: %v\n", f.Label(), res.Err)
		return 1
	}
	rec, found := res.Record, res.Record.Check
	if found.FetchErr != "" {
		fmt.Fprintf(errw, "yolo pack rebase: %s: could not fetch %s (%s) — using this machine's copy\n",
			f.Label(), f.Source, found.FetchErr)
	}
	if found.Problem != "" {
		if ra.onto == "" {
			fmt.Fprintf(errw, "yolo pack rebase: %s: %s\n", f.Label(), found.Problem)
			return 1
		}
		fmt.Fprintf(errw, "yolo pack rebase: %s: %s — rebasing onto --onto %s anyway\n", f.Label(),
			found.Problem, ra.onto)
	}
	repo := mustRepo(f.Source)
	list := rec.Candidates(res.Inputs)
	target, hasTarget := rebaseDefaultTarget(rec, res.Inputs, series.Digest)
	switch {
	case ra.onto != "":
		target, err = store.ResolveRebaseTarget(repo, ra.onto, func(line string) {
			pr.Printf("[dim]%s[/dim]", richtext.Escape(line))
		})
		if err != nil {
			fmt.Fprintf(errw, "yolo pack rebase: %s: %v — name a branch, a tag or a commit of the upstream\n",
				f.Label(), err)
			return 1
		}
	case hasTarget:
	case rec.Good != nil:
		pr.Printf("%s", richtext.Escape(f.Label()+": nothing upstream is newer than the good build "+
			goodLabel(rec.Good)+", which runs this series — no rebase is needed; `--onto <ref>` rebases it "+
			"onto another upstream commit"))
		return 0
	case found.BaseOnBranch:
		pr.Printf("%s", richtext.Escape(f.Label()+": no version of the branch is newer than the series' "+
			"base "+shortSHA(series.Base)+", which the next fresh launch builds — no rebase is needed; "+
			"`--onto <ref>` rebases it onto another upstream commit"))
		return 0
	default:
		fmt.Fprintf(errw, "yolo pack rebase: %s: %s names nothing this series can be rebased onto — "+
			"name a commit with --onto <ref>\n", f.Label(), f.Source)
		return 1
	}
	// A HOLD holds a fork only with a good build to hold at: with none, a launch checks as ever.
	if hold := patchedForkHold(f); hold != "" && rec.Good != nil {
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
	return runRebaseClone(ctx, pr, errw, store, f, origin, dir, series, target, site, "yolo pack rebase "+f.Key(),
		func(rr packsrc.RebaseResult) []string {
			return rebaseCleanLines(f, rec, res.Inputs, list, series, target, rr, site)
		})
}

// claimRebaseDir is step 2, the clone's directory settled before anything reaches the network, and
// held: the rebase directory lock, taken in locks' store, keeps a second run on it (another
// terminal) from taking this run's clone, half made, for its own, or removing it. It says what dir
// holds and acts on it: proceed is false when the verb ends here, with rc its exit status (a
// directory it must not touch, or its own clone, whose next steps it prints again); otherwise the
// caller defers unlock.
func claimRebaseDir(pr richtext.Printer, errw io.Writer, locks *packsrc.Store, f packload.Fork, origin forkPackOrigin,
	dir string, ra rebaseArgs, args []string, site rebaseSite) (unlock func(), rc int, proceed bool) {
	unlock, held, err := locks.TryLockRebaseDir(resolveExistingPrefix(dir))
	switch {
	case err != nil:
		fmt.Fprintf(errw, "yolo pack rebase: %v — name another directory with --into <dir>\n", err)
		return nil, 1, false
	case held:
		fmt.Fprintf(errw, "yolo pack rebase: another `yolo pack rebase` is working in %s right now — run `%s` "+
			"again once it has finished, or name another directory with --into <dir>\n", dir, rebaseCommandLine(ra, args))
		return nil, 1, false
	}
	state, marker, err := packsrc.InspectRebaseDir(dir)
	if err != nil {
		unlock()
		fmt.Fprintf(errw, "yolo pack rebase: reading %s: %v — name another directory with --into <dir>\n", dir, err)
		return nil, 1, false
	}
	switch {
	case state == packsrc.RebaseDirClone && marker.Owner != f.Key():
		unlock()
		fmt.Fprintf(errw, "yolo pack rebase: %s is the rebase clone of %s, not of %s — name another "+
			"directory with --into <dir>\n", dir, marker.Owner, f.Key())
		return nil, 1, false
	case state == packsrc.RebaseDirClone && !ra.restart:
		for _, line := range ownCloneLines(locks, f, origin, dir, marker, rebaseRestartLine(ra, args), site) {
			pr.Printf("%s", line)
		}
		unlock()
		return nil, 1, false
	case state == packsrc.RebaseDirClone:
		if err := removeRebaseClone(dir, f.Key()); err != nil {
			unlock()
			fmt.Fprintf(errw, "yolo pack rebase: removing the old rebase clone: %v — remove %s yourself, or name "+
				"another directory with --into <dir>\n", err, dir)
			return nil, 1, false
		}
		pr.Printf("[dim]%s[/dim]", richtext.Escape("removed the old rebase clone "+dir+", to start over"))
	case state == packsrc.RebaseDirOther:
		unlock()
		fmt.Fprintf(errw, "yolo pack rebase: %s exists and is not a rebase clone of %s, so it is left "+
			"alone — name another directory with --into <dir>, or remove it if an interrupted rebase left it\n",
			dir, f.Label())
		return nil, 1, false
	}
	return unlock, 0, true
}

// runRebaseClone is step 4: the clone and the replay, under ctx, the caller's interrupt context,
// which store.Ctx already carries; then the clean, conflicting or failed answer. clean is the lines
// a target the series takes as it stands gets, and retry the command that tries again after an apply
// error.
func runRebaseClone(ctx context.Context, pr richtext.Printer, errw io.Writer, store *packsrc.Store, f packload.Fork,
	origin forkPackOrigin, dir string, series *packsrc.Series, target packsrc.ListEntry, site rebaseSite, retry string,
	clean func(packsrc.RebaseResult) []string) int {
	repo, subdir := mustRepo(f.Source), subdirOf(f.Source)
	pr.Printf("[dim]%s[/dim]", richtext.Escape("cloning "+repo+" into "+dir+" to rebase "+f.Label()+
		"'s series onto upstream "+target.Label()))
	rr := rebaseCloneRun(store, packsrc.RebaseOptions{Owner: f.Key(), Repo: repo, Subdir: subdir, Series: series,
		Target: target, Dir: dir, Now: patchedNow()})
	if ctx.Err() != nil && !rr.Kept {
		fmt.Fprintf(errw, "yolo pack rebase: interrupted — the rebase clone %s is removed\n", dir)
		return 130
	}
	switch {
	case rr.Base != nil:
		fmt.Fprintf(errw, "yolo pack rebase: %s: %s\n", f.Label(), rr.Base.Error())
		return 1
	case rr.Err != nil:
		fmt.Fprintf(errw, "yolo pack rebase: %s: could not rebase the series onto %s: %v — the clone is "+
			"removed, and `%s` tries again\n", f.Label(), target.Label(), rr.Err, retry)
		return 1
	case rr.Clean:
		for _, line := range clean(rr) {
			pr.Printf("%s", line)
		}
		return 0
	}
	paths := strings.Join(rr.Conflict.Paths, ", ")
	if paths == "" {
		paths = "(no path named)"
	}
	pr.Printf("[yellow]%s[/yellow]", richtext.Escape(f.Label()+": upstream "+target.Label()+
		" does not take the patch series — the rebase stopped in "+dir))
	pr.Printf("%s", richtext.Escape("  "+rr.Conflict.Member+" conflicts in "+paths))
	for _, line := range rebaseNextSteps(f, origin, dir, target, rr.Applied, true, site) {
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

// pickRebaseFork is the patched fork or patched extension key names among forks, or why there is
// none (with the exit status): a key is required, since each conflict line names it, and a wrong one
// is answered with the keys there are. in is the pack directory the forks were read from, for the
// scratch rebase, and "" for the selected packs'.
func pickRebaseFork(forks []packload.Fork, key, in string, errw io.Writer) (packload.Fork, int) {
	var patched, trees []string
	for _, f := range forks {
		switch {
		case f.IsTree():
			trees = append(trees, f.Key())
		case f.Patched():
			patched = append(patched, f.Key())
		}
	}
	which, where := "the selected patched ", ""
	if in != "" {
		which, where = "the patched ", " in "+in
	}
	var there []string
	if len(patched) > 0 {
		there = append(there, which+plural(len(patched), "fork", "forks")+where+plural(len(patched), " is ", " are ")+
			strings.Join(patched, ", "))
	}
	if len(trees) > 0 {
		there = append(there, which+plural(len(trees), "extension", "extensions")+where+plural(len(trees), " is ", " are ")+
			strings.Join(trees, ", "))
	}
	switch {
	case len(there) > 0:
	case in != "":
		there = []string{"the pack in " + in + " declares no patched fork or extension (a fork pack's program, or a " +
			"`files` contribution, that declares `patches`)"}
	default:
		there = []string{"no patched fork or extension is selected (a fork pack's program, or a `files` " +
			"contribution, that declares `patches`)"}
	}
	thereAre := strings.Join(there, "; ")
	if key == "" {
		fmt.Fprintf(errw, "yolo pack rebase: name the patched fork or extension to rebase, as <pack>/<bin> or "+
			"<pack>/<name> — %s\n", thereAre)
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
	if in != "" {
		fmt.Fprintf(errw, "yolo pack rebase: no fork or extension in %s is %s — %s\n", in, key, thereAre)
		return packload.Fork{}, 1
	}
	fmt.Fprintf(errw, "yolo pack rebase: no selected fork or extension is %s — %s\n", key, thereAre)
	return packload.Fork{}, 1
}

// rebaseKind is what f is, as a line names it: "fork", or "extension" for a patched extension.
func rebaseKind(f packload.Fork) string {
	if f.IsTree() {
		return "extension"
	}
	return "fork"
}

// rebasePackNoun is what a line calls f's own pack: "fork pack", or "pack" for the pack contributing
// a patched extension.
func rebasePackNoun(f packload.Fork) string {
	if f.IsTree() {
		return "pack"
	}
	return "fork pack"
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
			fmt.Fprintf(errw, "yolo pack rebase: the rebase clone %s would be inside %s %s's own "+
				"directory %s, which yolo never writes — name a directory outside it with --into <dir>\n",
				abs, rebasePackNoun(f), f.Pack, root)
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
// Under an `agent_updates` hold with a good build, no launch checks the upstream, and a launch builds
// the series at the good build's commit alone until the hold lifts. A scratch rebase reads no check
// record, so for the newest entry it cannot say whether the good build already runs it (scratchBuilds).
func rebaseCleanLines(f packload.Fork, rec *packsrc.CheckRecord, in packsrc.CheckInputs, list []packsrc.ListEntry,
	series *packsrc.Series, target packsrc.ListEntry, rr packsrc.RebaseResult, site rebaseSite) []string {
	hold := ""
	if rec != nil && rec.Good != nil {
		hold = patchedForkHold(f)
	}
	next := "a " + rebaseKind(f) + " builds it only once its ?ref= and follow rule name it"
	switch {
	case rec != nil && rec.Good != nil && rec.Good.Commit == target.Commit && rec.Good.Series == series.Digest:
		next = "the good build already runs it"
	case hold != "" && rec.Good.Commit == target.Commit:
		next = patchedNotBuilt
	case hold != "":
		next = "a launch builds it once the hold lifts (" + hold + ")"
	case isRebaseDefault(rec, in, series.Digest, target) && site.scratch:
		next = scratchBuilds
	case isRebaseDefault(rec, in, series.Digest, target):
		next = patchedNotBuilt
	case onList(list, target.Commit):
		next = "the next fresh launch builds it unless a newer version on its list takes the series"
	}
	lines := []string{"[green]" + richtext.Escape(f.Label()) + "[/green]: " + richtext.Escape(fmt.Sprintf(
		"upstream %s takes the series as it stands (%d %s, series %s) — %s; nothing to rebase, so the "+
			"clone is removed", target.Label(), series.Len(), plural(series.Len(), "patch", "patches"),
		series.ShortDigest(), next))}
	for _, m := range rr.Upstream {
		lines = append(lines, "[dim]"+richtext.Escape(fmt.Sprintf("  %s is already in upstream %s; drop it from %s",
			m, target.Label(), series.Dir))+"[/dim]")
	}
	return lines
}

// rebaseCommandLine is the verb's command line as the user typed it, for a line that names it to
// run again or elsewhere: their --onto and --into kept, since the bare verb rebases onto another
// version and into another directory. With no key typed, the key's placeholder.
func rebaseCommandLine(ra rebaseArgs, args []string) string {
	line := shquote.JoinDisplay(append([]string{"yolo", "pack", "rebase"}, args...))
	if ra.key == "" {
		line = strings.TrimSpace("yolo pack rebase <pack>/<bin> " + shquote.JoinDisplay(args))
	}
	return line
}

// rebaseRestartLine is the command that starts the clone at the user's directory over: their own
// command line with --restart.
func rebaseRestartLine(ra rebaseArgs, args []string) string {
	if ra.restart {
		return rebaseCommandLine(ra, args)
	}
	ra.restart = true
	return rebaseCommandLine(ra, append(append([]string{}, args...), "--restart"))
}

// ownCloneLines are what a second `yolo pack rebase` says on its own clone: that it is there and
// what it was rebased onto; while git records the rebase in progress, its next steps again; once it
// is finished (packsrc.Store.RebaseFinished), the export; and otherwise — a rebase aborted, or a
// run that stopped before its replay ended — that it holds nothing to export. Each ends with
// restart, the command that starts it over.
func ownCloneLines(store *packsrc.Store, f packload.Fork, origin forkPackOrigin, dir string, m *packsrc.RebaseMarker,
	restart string, site rebaseSite) []string {
	made := patchedAge(patchedNow().Sub(time.Unix(m.At, 0)))
	lines := []string{"[yellow]" + richtext.Escape(f.Label()+": "+dir+" is its rebase clone, made "+made+
		" ago onto upstream "+m.Target.Label()) + "[/yellow]"}
	again := "[dim]" + richtext.Escape("  `"+restart+"` removes it and rebases the series again from the start") + "[/dim]"
	switch {
	case packsrc.RebaseInProgress(dir) && m.Applied != "":
		lines = append(lines, rebaseNextSteps(f, origin, dir, m.Target, m.Applied, true, site)...)
	case store.RebaseFinished(dir, m):
		lines = append(lines, rebaseNextSteps(f, origin, dir, m.Target, m.Applied, false, site)...)
	default:
		lines = append(lines, richtext.Escape("  it holds no finished rebase to export: the rebase there was "+
			"aborted, or a run stopped before its replay ended"))
		again = richtext.Escape("  `" + restart + "` removes it and rebases the series again from the start")
	}
	return append(lines, again)
}

// forkPackClone is where a fetched fork pack's publish steps clone the pack's repository: beside the
// rebase clone at dir, <pack>-pack, or the first of <pack>-pack-2, -3, … when that path holds
// anything but a clone of repo. exists is true when the path is a clone of repo already, which an
// earlier rebase's steps left: the steps then update it rather than clone over it.
func forkPackClone(store *packsrc.Store, dir, pack, repo string) (string, bool) {
	stem := filepath.Join(filepath.Dir(dir), strings.NewReplacer("/", "-").Replace(pack)+"-pack")
	for i := 1; ; i++ {
		p := stem
		if i > 1 {
			p = fmt.Sprintf("%s-%d", stem, i)
		}
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) || i >= 1000 {
			return p, false
		}
		if store.CloneOrigin(p) == repo {
			return p, true
		}
	}
}

// rebaseNextSteps are a stopped rebase's next steps, in order (§8.4 steps 6 and 7): the continue,
// to run after resolving; the export that replaces the series with no moment where it is empty or
// partial — into a local pack's own patch directory, or through a clone of a fetched pack's
// repository, committed and pushed; what then builds it; and that an agent can resolve it in a jail
// started there. applied is the series as applied at its base (packsrc.RebaseResult.Applied).
// Every path is quoted for the shell it is pasted into.
//
// THE EXPORT IS ONE COMMAND LINE, each step run only once the one before it succeeded, so a pasted
// block changes nothing until the rebase is finished: it starts with packsrc.RebaseExportGuard,
// which refuses a branch a stopped or aborted rebase left at the applied series, a skipped-through
// one, and a clone with no branch; it reads the rebased BRANCH, never HEAD, which a stopped rebase
// detaches at a partial replay; `mkdir` refuses a <patches>.new an earlier export left, whose files
// the series would take too; the format-patch writes `.patch` names with `a/` `b/` prefixes whatever
// the user's git config says (format.suffix, format.noprefix), since the series reads only `.patch`
// files and `git am` strips one path component, and with no `-- <git version>` signature, which
// changes with the git that exports and is no part of a patch (PF-D62); and only then the two
// renames, the one moment a reader finds no directory. A fetched pack's commit and push end the
// same line.
//
// A SCRATCH REBASE (site.scratch) exports into the --pack directory, which a launch builds only
// where `packs` selects it; in a jail (site.jail) the agent that resolves the conflict is already
// there, so no jail is offered.
func rebaseNextSteps(f packload.Fork, origin forkPackOrigin, dir string, target packsrc.ListEntry,
	applied string, inProgress bool, site rebaseSite) []string {
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
	guard := packsrc.RebaseExportGuard(dir, target.Commit, applied, q)
	formatPatch := func(out string) string {
		return "git -C " + q(dir) + " -c format.noprefix=false format-patch --no-signature --suffix=.patch --base=" +
			target.Commit + " -o " + out + " " + target.Commit + ".." + packsrc.RebaseBranchRef
	}
	export := func(patches, tail string) {
		if _, err := os.Lstat(patches + ".new"); err == nil {
			say("first remove " + q(patches+".new") + ", which an earlier export left — the export refuses while " +
				"it is there:")
		}
		if _, err := os.Lstat(patches + ".old"); err == nil {
			say("first remove " + q(patches+".old") + ", which an earlier export left:")
		}
		cmd(guard + " && mkdir " + q(patches+".new") + " && " + formatPatch(q(patches+".new")) +
			" && mv " + q(patches) + " " + q(patches+".old") + " && mv " + q(patches+".new") + " " + q(patches) +
			" && rm -r " + q(patches+".old") + tail)
	}
	switch {
	case origin.local != "" && site.scratch:
		say(then + "replace the series in " + q(origin.local) + " with the rebased one — the line changes nothing " +
			"until the rebase is finished:")
		export(origin.patches(f), "")
		say("the next fresh launch builds the new series wherever your `packs` selects this pack")
	case origin.local != "":
		say(then + "replace the series with the rebased one — the line changes nothing until the rebase is finished:")
		export(origin.patches(f), "")
		say("the next fresh launch builds the new series")
	case origin.fetched != nil:
		a := *origin.fetched
		store := patchedForkStore()
		clone, exists := forkPackClone(store, dir, f.Pack, a.Repo)
		rel := filepath.FromSlash(f.Patches)
		if a.Path != "" {
			rel = filepath.Join(filepath.FromSlash(a.Path), rel)
		}
		say(then + "publish the rebased series: " + rebasePackNoun(f) + " " + f.Pack + " is fetched from " + a.Repo + ", and " +
			"yolo never writes its copy on this machine, so export into a clone of that repository (or your own) " +
			"and push it — the second line changes nothing until the rebase is finished:")
		kind := store.RefKind(a)
		switch {
		case exists && kind == "branch":
			cmd("git -C " + q(clone) + " checkout -q " + q(a.Ref) + " && git -C " + q(clone) + " pull -q --ff-only")
		case exists:
			cmd("git -C " + q(clone) + " pull -q --ff-only")
		case kind == "branch":
			cmd("git clone -b " + q(a.Ref) + " " + q(a.Repo) + " " + q(clone))
		default:
			cmd("git clone " + q(a.Repo) + " " + q(clone))
		}
		export(filepath.Join(clone, rel), " && git -C "+q(clone)+" add -A "+q(rel)+" && git -C "+q(clone)+
			" commit -m "+q("Rebase the patch series onto "+target.Label())+" && git -C "+q(clone)+" push")
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
		say(then + "export it into a " + rebasePackNoun(f) + " of your own — " + rebasePackNoun(f) + " " + f.Pack + " is none of your `packs` " +
			"entries, so there is no patch directory of yours to replace:")
		cmd(guard + " && " + formatPatch("<your pack>/patches"))
	}
	if inProgress && !site.jail {
		say("an agent can resolve it in a jail started there: cd " + q(dir) + " && yolo")
	}
	return lines
}
