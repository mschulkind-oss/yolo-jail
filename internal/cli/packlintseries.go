package cli

// packlintseries.go is `yolo pack lint`'s read of a pack's PATCH SERIES, and its opt-in online
// check of each upstream (docs/design/patched-forks.md PF-D63, PF-D64).
//
// THE SERIES IS READ AS A LAUNCH READS IT (packsrc.ReadSeries), for every patched fork and patched
// extension the pack declares, and a series that does not read is a lint FAILURE carrying the
// read's own message, which names the file and the remedy. A launch treats the same series as the
// fork's or extension's reason, and so builds nothing for it. It is read in the PACK'S OWN
// DIRECTORY, never in lint's staged copy: every reader of a series reads the pack in place (an
// unfiltered local pack, or the store's checkout of a fetched one), and staging resolves an in-pack
// link into a plain file and drops an empty directory, which would pass a series a launch refuses
// and misname an empty folder as a missing one.
//
// `--online` ASKS EACH UPSTREAM (packsrc.Store.ProbeSeries), in a SCRATCH MIRROR: a pack store
// rooted in a temporary directory lint deletes on its way out, a Ctrl-C included, so it writes
// nothing into this machine's pack store, check records or mirrors, and it runs the same in a jail
// as on the host. Its verdict is the check's: what the check calls a problem fails lint, since a
// launch builds nothing for it, and what a launch builds anyway is a note or a warning.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// lintOnlineStore is the scratch store `--online` checks through, rooted at dir; a var so a test
// can hand it a git of its own.
var lintOnlineStore = func(dir string) *packsrc.Store { return &packsrc.Store{Dir: dir} }

// lintOnlineInterrupt is the context the online checks' git runs under, ended by a Ctrl-C, a SIGTERM
// or a SIGHUP, so an interrupted check still deletes its scratch mirror; a var so a test can end it.
var lintOnlineInterrupt = func() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}

// lintOnlineAgain is the step a failed online check names when nothing in the pack is at fault.
const lintOnlineAgain = "run `yolo pack lint --online` again"

// lintedSeries is one patched fork or extension of the linted pack whose series read.
type lintedSeries struct {
	fork   packload.Fork
	series *packsrc.Series
}

// patchedOfPack is every patched fork and patched extension pack declares, each reading its series
// from root, the pack's own directory.
func patchedOfPack(pack *packload.Pack, root string) []packload.Fork {
	one := []*packload.Pack{pack}
	var out []packload.Fork
	for _, f := range packload.Forks(one) {
		if f.Patched() {
			f.Root = root
			out = append(out, f)
		}
	}
	for _, f := range packload.PatchedTrees(one) {
		f.Root = root
		out = append(out, f)
	}
	return out
}

// lintSeries reads every series of pack in root: the ones that read, and a problem for each one that
// does not, naming the fork or extension and carrying the read's remedy.
func lintSeries(pack *packload.Pack, root string) ([]lintedSeries, []string) {
	var read []lintedSeries
	var problems []string
	for _, f := range patchedOfPack(pack, root) {
		if packdecl.PatchesDirProblem(f.Patches) != "" {
			continue // the manifest's own problem names the value, and the read would say it again
		}
		s, err := f.ReadSeries()
		if err != nil {
			problems = append(problems, richtext.Escape(f.Label()+": "+err.Error()))
			continue
		}
		read = append(read, lintedSeries{fork: f, series: s})
	}
	return read, problems
}

// lintOnline is `--online` over every series that read: the problems that fail lint, and the lines
// it prints either way (each check's notes and warnings, in order).
func lintOnline(pr richtext.Printer, read []lintedSeries) (problems, lines []string) {
	if len(read) == 0 {
		return nil, []string{"[dim]online: no patched fork or extension of this pack has a series that " +
			"reads, so no upstream was checked[/dim]"}
	}
	tmp, err := os.MkdirTemp("", "yolo-pack-lint-online-")
	if err != nil {
		return []string{richtext.Escape("online: no scratch mirror could be made (" + err.Error() + ") — " +
			"check $TMPDIR, then " + lintOnlineAgain)}, nil
	}
	defer os.RemoveAll(tmp)
	ctx, stop := lintOnlineInterrupt()
	defer stop()
	store := lintOnlineStore(filepath.Join(tmp, "packs"))
	store.Ctx = ctx
	waiting := func(line string) { pr.Printf("[dim]%s[/dim]", richtext.Escape(line)) }
	for _, r := range read {
		if ctx.Err() != nil {
			break
		}
		if _, _, _, err := r.fork.CheckWant(r.series).Inputs(); err != nil {
			continue // the manifest's own problem names the source or follow rule, and only an edit fixes it
		}
		pr.Printf("[dim]%s[/dim]", richtext.Escape("checking "+r.fork.Label()+"'s upstream "+r.fork.Source+
			" in a scratch mirror"))
		p := store.ProbeSeries(r.fork.CheckWant(r.series), r.series, waiting)
		if ctx.Err() != nil {
			break
		}
		ps, warnings, notes := onlineVerdict(r.fork, r.series, p)
		for _, n := range notes {
			lines = append(lines, "[green]✓[/green] "+richtext.Escape("online: "+r.fork.Label()+": "+n))
		}
		for _, w := range warnings {
			lines = append(lines, "[yellow]⚠[/yellow] "+richtext.Escape("online: "+r.fork.Label()+": "+w))
		}
		for _, p := range ps {
			problems = append(problems, richtext.Escape("online: "+r.fork.Label()+": "+p))
		}
	}
	if ctx.Err() != nil {
		problems = append(problems, "online: interrupted, and the scratch mirror is deleted — "+lintOnlineAgain)
	}
	return problems, lines
}

// printLines prints lines already in markup, each escaped where it was made.
func printLines(pr richtext.Printer, lines []string) {
	for _, l := range lines {
		pr.Printf("%s", l)
	}
}

// onlineVerdict is what lint says of one series probe: the problems that fail lint, the warnings and
// the notes, each without the fork's label. See the file doc for whose verdict it is.
func onlineVerdict(f packload.Fork, s *packsrc.Series, p packsrc.SeriesProbe) (problems, warnings, notes []string) {
	found := p.Found
	switch {
	case p.Err != nil:
		return []string{"could not check " + f.Source + ": " + p.Err.Error() + " — " + lintOnlineAgain}, nil, nil
	case found.FetchErr != "":
		return []string{"could not fetch " + f.Source + " (" + found.FetchErr + ") — check the address and " +
			"this machine's network, then " + lintOnlineAgain}, nil, nil
	case found.Problem != "":
		problems = append(problems, found.Problem)
	default:
		problems, warnings, notes = refVerdict(f, s, p)
	}
	if !p.Replayed {
		return problems, warnings, notes
	}
	var old *packsrc.GitTooOldError
	switch w := p.Walk; {
	case w.Base != nil:
		problems = append(problems, w.Base.Error())
	case errors.As(w.Err, &old):
		problems = append(problems, old.Need()+", and this machine's git is "+old.Have+" — update git, then "+
			lintOnlineAgain)
	case w.Err != nil:
		problems = append(problems, "could not replay the series at its base: "+w.Err.Error()+" — "+lintOnlineAgain)
	default:
		notes = append(notes, fmt.Sprintf("the series (%d %s, series %s) replays at its base %s", s.Len(),
			plural(s.Len(), "patch", "patches"), s.ShortDigest(), shortSHA(s.Base)))
	}
	return problems, warnings, notes
}

// refVerdict is what the ref and the follow rule found, for a check that recorded no problem.
func refVerdict(f packload.Fork, s *packsrc.Series, p packsrc.SeriesProbe) (problems, warnings, notes []string) {
	found := p.Found
	in, _, follow, err := f.CheckWant(s).Inputs()
	if err != nil {
		return []string{err.Error()}, nil, nil
	}
	ref := "?ref=" + in.Ref
	rule := "`follow: \"" + follow.String() + "\"`"
	switch {
	case found.RefKind == "tag" && len(found.List) > 0:
		return nil, nil, []string{ref + " is a tag, which holds the series at " + found.List[0].Label()}
	case found.RefKind == "commit" && len(found.List) > 0:
		return nil, nil, []string{ref + " is a commit, which holds the series at " + found.List[0].Label()}
	case p.Tagless:
		// A TAGLESS BRANCH THE CHECK BUILDS AT ITS BASE: legal, and likely a forgotten rule.
		return nil, []string{ref + " of " + in.Repo + " carries no version tag that " + rule + " reads, so the " +
			"series stays at its base " + shortSHA(s.Base) + " until one appears — `follow: \"head\"` follows the " +
			"branch's commits"}, nil
	case len(found.List) > 0 && found.List[0].Tip:
		return nil, nil, []string{ref + " is a branch, and " + rule + " follows its head, " + found.List[0].Label()}
	case len(found.List) > 0:
		return nil, nil, []string{ref + " is a branch, and " + rule + " finds " + found.List[0].Label() +
			", the newest version containing the series' base"}
	case found.BaseOnBranch:
		return nil, nil, []string{ref + " is a branch, and no version " + rule + " reads contains the series' base " +
			shortSHA(s.Base) + ", so a launch builds the series' base until a release after it"}
	}
	return []string{ref + " names nothing this series can be built at: the branch does not contain the series' " +
		"base " + shortSHA(s.Base) + " — follow the branch the series was made on, or hold at a tag or a commit"}, nil, nil
}
