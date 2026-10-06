package cli

// patchedrebasescratch.go is the SCRATCH REBASE (a term coined here, docs/design/patched-forks.md
// PF-D66): `yolo pack rebase <pack>/<bin> --pack <dir> [--onto <ref>] [--into <dir>] [--restart]`,
// the rebase of the series in a local pack's directory with no pack store behind it. It reads the
// pack in <dir> as a launch reads a local pack, checks the upstream in a SCRATCH MIRROR
// (patchedseriescheck.go coins the term) and reads no check record, and then makes the rebase clone
// and prints the continue and the export as the keyed verb does: it writes nothing in the pack and
// nothing in yolo's state directory.
//
// It is how a jail rebases a series. A jail has neither the host's mirror, its check record nor a
// path to the pack the host's `packs` names, so the keyed verb refuses there and names this one. In
// a jail <dir> must be inside the jail's own workspace, the host's own copy of a pack there;
// anywhere else the refusal names the keyed verb on the host. On the host it reads any local pack
// directory, selected or not.
//
// With no --onto, the target is the scratch check's newest entry. That is the keyed verb's default
// unless this machine's good build already runs the newest commit (rebaseDefaultTarget), which no
// record here can say, so a clean replay there says so (scratchBuilds).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// scratchRebaseLocks is the store a scratch rebase takes its rebase directory lock in: a directory
// of this user's under the temporary directory, which every terminal of a jail, or of the host,
// shares, so a second scratch rebase into one clone directory is refused as the keyed verb's second
// run is, and nothing is written in yolo's state directory. A var so a test can place it.
var scratchRebaseLocks = func() (*packsrc.Store, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("yolo-rebase-locks-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &packsrc.Store{Dir: dir}, nil
}

// packRebaseScratch is `yolo pack rebase … --pack <dir>`. See the file doc.
func packRebaseScratch(ra rebaseArgs, args []string, out, errw io.Writer, color bool) int {
	packDir, rc := rebasePackDir(ra, errw)
	if rc != 0 {
		return rc
	}
	// THE PACK'S NAME is the key's: the key a conflict line names is what the user types, and a
	// `packs` entry may name its pack other than by its directory. With no key, the directory's.
	name, _, keyed := strings.Cut(ra.key, "/")
	if !keyed || name == "" {
		name = filepath.Base(packDir)
	}
	p, err := loadPatchedPackDir(packDir, name)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: %v — `yolo pack lint %s` names every problem\n", err,
			shquote.QuoteDisplay(packDir))
		return 1
	}
	f, rc := pickRebaseFork(packForks(p), ra.key, packDir, errw)
	if rc != 0 {
		return rc
	}
	pr := richtext.Printer{W: out, Color: color}
	origin := forkPackOrigin{local: packDir}
	site := rebaseSite{scratch: true, jail: config.InJail()}

	// 2. THE CLONE'S DIRECTORY, as the keyed verb settles it.
	dir, rc := rebaseDir(f, origin, ra.into, errw)
	if rc != 0 {
		return rc
	}
	locks, err := scratchRebaseLocks()
	if err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: the rebase directory lock: %v — set TMPDIR to a directory of yours\n", err)
		return 1
	}
	unlock, rc, proceed := claimRebaseDir(pr, errw, locks, f, origin, dir, ra, args, site)
	if !proceed {
		return rc
	}
	defer unlock()
	series, err := f.ReadSeries()
	if err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: %s: %v\n", f.Label(), err)
		return 1
	}

	// 3. THE CHECK, forced, into a scratch mirror, under the interrupt context from the start: no
	// lock of the pack store is waited on here, so a Ctrl-C ends the fetch's git and the scratch
	// mirror is removed.
	store, remove, err := scratchPatchedStore(packDir)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: %s: %v\n", f.Label(), err)
		return 1
	}
	defer remove()
	ctx, stop := rebaseInterrupt()
	defer stop()
	store.Ctx = ctx
	res := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: patchedNow,
		Begin: func() (func(string), func()) {
			pr.Printf("[dim]%s[/dim]", richtext.Escape("checking "+f.Label()+"'s upstream "+f.Source+" in a scratch copy"))
			return func(line string) { pr.Printf("[dim]%s[/dim]", richtext.Escape(line)) }, func() {}
		}})
	switch {
	case ctx.Err() != nil:
		fmt.Fprintf(errw, "yolo pack rebase: interrupted — nothing was cloned, and the scratch copy is removed\n")
		return 130
	case res.Err != nil:
		fmt.Fprintf(errw, "yolo pack rebase: %s: %v\n", f.Label(), res.Err)
		return 1
	}
	rec, found := res.Record, res.Record.Check
	if found.FetchErr != "" {
		// A SCRATCH MIRROR STARTS EMPTY, so a fetch that failed leaves nothing to rebase onto.
		fmt.Fprintf(errw, "yolo pack rebase: %s: could not fetch %s into a scratch copy (%s) — check the address "+
			"and this machine's network, then run `%s` again\n", f.Label(), f.Source, found.FetchErr,
			rebaseCommandLine(ra, args))
		return 1
	}
	if found.Problem != "" {
		if ra.onto == "" {
			fmt.Fprintf(errw, "yolo pack rebase: %s: %s\n", f.Label(), found.Problem)
			return 1
		}
		fmt.Fprintf(errw, "yolo pack rebase: %s: %s — rebasing onto --onto %s anyway\n", f.Label(),
			found.Problem, ra.onto)
	}
	list := rec.Candidates(res.Inputs)
	var target packsrc.ListEntry
	switch {
	case ra.onto != "":
		target, err = store.ResolveRebaseTarget(mustRepo(f.Source), ra.onto, nil)
		if err != nil {
			fmt.Fprintf(errw, "yolo pack rebase: %s: %v — name a branch, a tag or a commit of the upstream\n",
				f.Label(), err)
			return 1
		}
	case len(list) > 0:
		target = list[0]
	case found.BaseOnBranch:
		pr.Printf("%s", richtext.Escape(f.Label()+": no version of the branch is newer than the series' base "+
			shortSHA(series.Base)+", which a fresh launch with no good build builds — no rebase is needed; "+
			"`--onto <ref>` rebases it onto another upstream commit"))
		return 0
	default:
		fmt.Fprintf(errw, "yolo pack rebase: %s: %s names nothing this series can be rebased onto — "+
			"name a commit with --onto <ref>\n", f.Label(), f.Source)
		return 1
	}

	// 4. THE CLONE AND THE REPLAY, as the keyed verb makes them.
	return runRebaseClone(ctx, pr, errw, store, f, origin, dir, series, target, site, rebaseCommandLine(ra, args),
		func(rr packsrc.RebaseResult) []string {
			return rebaseCleanLines(f, rec, res.Inputs, list, series, target, rr, site)
		})
}

// rebasePackDir is --pack's directory, absolute, or why the scratch rebase may not read it (with the
// exit status): it must be a directory, and in a jail one inside the jail's own workspace.
func rebasePackDir(ra rebaseArgs, errw io.Writer) (string, int) {
	abs, err := filepath.Abs(ra.pack)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack rebase: --pack %s: %v\n", ra.pack, err)
		return "", 1
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		fmt.Fprintf(errw, "yolo pack rebase: --pack %s is not a directory — name the directory holding the "+
			"pack's pack.json\n", abs)
		return "", 1
	}
	if config.InJail() && !inJailWorkspace(abs) {
		fmt.Fprintf(errw, "yolo pack rebase: --pack %s is outside this jail's workspace %s, and in a jail it reads "+
			"only a pack inside the workspace, the host's own copy of it — run `%s` in a terminal on the host\n",
			abs, config.JailWorkspace(), rebaseHostLine(ra))
		return "", 1
	}
	return abs, 0
}

// rebaseHostLine is the keyed verb for the fork ra names, as a line names it to run on the host:
// its --onto and --into kept, --pack dropped, since the host reads the pack its `packs` selects.
func rebaseHostLine(ra rebaseArgs) string {
	key := ra.key
	if key == "" {
		key = "<pack>/<bin>"
	}
	argv := []string{"yolo", "pack", "rebase", key}
	if ra.onto != "" {
		argv = append(argv, "--onto", ra.onto)
	}
	if ra.into != "" {
		argv = append(argv, "--into", ra.into)
	}
	line := shquote.JoinDisplay(argv)
	if ra.key == "" {
		line = strings.Replace(line, shquote.QuoteDisplay(key), key, 1)
	}
	return line
}
