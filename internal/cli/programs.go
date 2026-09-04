package cli

// programs.go is the user-facing surface for the two halves of program-delivery.md §10 that
// only a boot could reach until now:
//
//	yolo programs ls      the OFFLINE report — the orphan catalog (step four) and the
//	                      record-vs-disk reconcile (step two), on demand instead of once per
//	                      launch, where they scroll past above an agent's first prompt
//	yolo programs remove  the EXPLICIT ACT OQ-PD4 rules — a dry run by default, and it
//	                      removes only what `ls` names
//
// BOTH ARE RENDERERS, not implementations. The catalog's candidate set and the reconcile's
// comparison live in internal/entrypoint beside the boot that also calls them
// (InstalledOrphans, ReconcileInstalled), because "what is an orphan" answered twice is the
// one disagreement that means one surface deletes what the other spares.
//
// IT IS A JAIL-SIDE COMMAND, and the discriminator is YOLO_PACK_ROOT rather than
// YOLO_VERSION — refreshNpmProgramsFromOS's rule, for its reason: the question is not "am I
// in a jail" but "can this process see the staged pack tree", which is the input the
// declared set is computed from. On the host there is no npm prefix, no ~/.local/bin under a
// per-workspace home and no staged tree, so every declaration would read as absent and every
// installed thing as an orphan.

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

const programsUsage = `Usage: yolo programs <subcommand>

What is installed in this jail's home, what nothing declares any more, and what the
install record gets wrong. Run it INSIDE a jail: the programs live in that jail's
per-workspace home, and the declarations come from its staged pack tree.

  yolo programs ls                  report, offline. Orphans (installed, and no selected
                                    pack, preset or LSP recipe declares them) with their
                                    sizes, plus every place the install receipts and the
                                    LSP sentinel disagree with what is on disk.
  yolo programs remove [NAME...]    remove orphans. WITHOUT --apply it is a DRY RUN: it
                                    prints every path it would unlink and exits.
  yolo programs remove --apply      actually remove them.
  yolo programs --help, -h          this text

  NAME is an orphan as 'ls' names it — an npm package name, or the base name of a
  ~/.local/bin or $GOPATH/bin entry. With no NAME, every orphan is a candidate.

Only an ORPHAN can be removed. A program a selected pack, an MCP preset or an LSP recipe
declares is never a candidate, so this command cannot uninstall a tool your config still
asks for — drop the declaration first, then run it.

Nothing is removed at boot unless you ask for it. Set "programs": {"autoprune": true} in
~/.config/yolo-jail/config.jsonc (USER scope only) to make every launch remove what it
catalogs. It is off by default and it is not reversible — read an 'ls' first.

  ~/.local/bin is also where YOU may have put things. yolo cannot tell a tool you
  installed by hand from one a dropped pack left behind: both are "installed and
  undeclared". That is what the dry run is for.`

// runPrograms is the registry entry point. args INCLUDES the subcommand name, like every
// other handler.
func runPrograms(args []string) int {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	return programsMain(rest, os.Stdout, os.Stderr, isTTYStdout())
}

// programsMain dispatches a subcommand. Bare `yolo programs` prints the usage rather than
// running `ls`, which is `pack`'s and `config`'s convention — and interrogating a tool must
// never do work (self-documenting-cli.md item 1).
func programsMain(args []string, out, errw io.Writer, color bool) int {
	if len(args) == 0 {
		fmt.Fprintln(out, programsUsage)
		return 0
	}
	switch args[0] {
	case "ls", "list":
		return programsLs(args[1:], out, errw, color)
	case "remove", "rm":
		return programsRemove(args[1:], out, errw, color)
	case "-h", "--help", "help":
		fmt.Fprintln(out, programsUsage)
		return 0
	default:
		fmt.Fprintf(errw, "yolo programs: unknown subcommand %q\n\n%s\n", args[0], programsUsage)
		return 2
	}
}

// programsEnv returns the jail environment this command reads, or nil (having said why) when
// it is not looking at a jail. See the file header for why YOLO_PACK_ROOT is the test.
func programsEnv(pr richtext.Printer) *entrypoint.Env {
	e := entrypoint.EnvFromOS()
	if e.Getenv("YOLO_PACK_ROOT") == "" {
		pr.Printf("[dim]No staged packs here — a program is installed INSIDE a jail, into " +
			"that jail's own home, so run `yolo programs` there.[/dim]")
		return nil
	}
	return e
}

// programsLs is the report: the orphan catalog and the reconcile, in that order — the
// coarser finding first, which is the order boot.go runs them in and for the same reason
// (the catalog asks what has no owner at all, the reconcile what the record gets wrong about
// the things that do).
//
// EXIT 0 EVEN WITH FINDINGS. This is a report, and OQ-PD7 rules that reports come before
// gates; an exit code that turned an orphan into a failure would be the gate, granted
// without the measurement that is supposed to justify one.
func programsLs(args []string, out, errw io.Writer, color bool) int {
	if len(args) > 0 {
		if isHelpToken(args[0]) {
			fmt.Fprintln(out, programsUsage)
			return 0
		}
		fmt.Fprintf(errw, "yolo programs ls: unexpected argument %q\n", args[0])
		return 2
	}
	pr := richtext.Printer{W: out, Color: color}
	e := programsEnv(pr)
	if e == nil {
		return 0
	}

	orphans := entrypoint.InstalledOrphans(e)
	pr.Printf("[bold]Orphans[/bold] — installed, and nothing selected declares them")
	if len(orphans) == 0 {
		pr.Printf("  [dim]none[/dim]")
	} else {
		plan := entrypoint.PlanOrphanRemovals(e, orphans)
		for _, r := range plan {
			pr.Printf("  %s  [dim]%s (%s)[/dim]", r.Orphan.Display, r.Orphan.Path,
				entrypoint.RenderSize(r.Bytes))
		}
		pr.Printf("  [dim]%s, %s — `yolo programs remove` to see what removing them "+
			"would unlink[/dim]", countLabel(len(plan), "orphan"),
			entrypoint.RenderSize(planTotal(plan)))
	}

	rep := entrypoint.ReconcileInstalled(e)
	pr.Printf("")
	pr.Printf("[bold]Record[/bold] — where the install receipts and the LSP sentinel " +
		"disagree with the disk")
	switch {
	case len(rep.Findings) > 0:
		for _, f := range rep.Findings {
			pr.Printf("  %s", f)
		}
	case !rep.ReceiptsPresent:
		// The NORMAL state, and saying so beats an empty section: every install site
		// sits behind a cold branch, so a warm home writes no receipt and a jail
		// provisioned before receipts shipped has no file at all.
		pr.Printf("  [dim]no findings (no receipts log in this workspace yet — one is " +
			"written the first time a launcher installs something)[/dim]")
	default:
		pr.Printf("  [dim]no findings (%s read)[/dim]", countLabel(rep.Receipts, "receipt"))
	}
	for _, m := range rep.Malformed {
		pr.Printf("  [yellow]unparseable receipt line:[/yellow] %s", m)
	}
	return 0
}

// programsRemove is OQ-PD4's explicit act.
//
// THE DEFAULT IS THE DRY RUN, and --apply is the whole difference — `yolo prune`'s
// convention, which is the one destructive-act convention this CLI already has. The dry run
// is not a summary of the act: it prints the SAME plan the act executes, path by path, so
// what a user reads is what would go.
//
// THE CANDIDATES ARE ORPHANS AND NOTHING ELSE. A NAME argument filters that set; it cannot
// widen it. So this command structurally cannot uninstall a declared program, which is what
// makes `--apply` with no names an act a user can reason about rather than a wildcard
// pointed at their home.
func programsRemove(args []string, out, errw io.Writer, color bool) int {
	apply := false
	var names []string
	for _, a := range args {
		switch {
		case isHelpToken(a):
			fmt.Fprintln(out, programsUsage)
			return 0
		case a == "--apply":
			apply = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo programs remove: unknown flag %q\n\n%s\n", a, programsUsage)
			return 2
		default:
			names = append(names, a)
		}
	}

	pr := richtext.Printer{W: out, Color: color}
	e := programsEnv(pr)
	if e == nil {
		return 0
	}
	orphans := entrypoint.InstalledOrphans(e)
	selected, unknown := selectOrphans(orphans, names)
	if len(unknown) > 0 {
		// NOT a filter that silently matches nothing: a name this cannot find is either a
		// typo or a program that has an owner, and both are things the user must be told
		// rather than left to infer from an empty plan.
		fmt.Fprintf(errw, "yolo programs remove: not an orphan: %s\n"+
			"Only what `yolo programs ls` lists can be removed — a declared program is "+
			"not a candidate.\n", strings.Join(unknown, ", "))
		return 2
	}
	if len(selected) == 0 {
		pr.Printf("[dim]No orphans — nothing to remove.[/dim]")
		return 0
	}

	plan := entrypoint.PlanOrphanRemovals(e, selected)
	verb := "Would remove"
	if apply {
		verb = "Removing"
	}
	pr.Printf("[bold]%s %s (%s)[/bold]", verb, countLabel(len(plan), "orphan"),
		entrypoint.RenderSize(planTotal(plan)))
	for _, r := range plan {
		pr.Printf("  %s  [dim](%s)[/dim]", r.Orphan.Display, entrypoint.RenderSize(r.Bytes))
		for _, p := range r.Paths {
			pr.Printf("    [dim]%s[/dim]", p)
		}
	}
	if !apply {
		pr.Printf("")
		pr.Printf("[dim]Nothing was removed. Re-run with --apply.[/dim]")
		return 0
	}

	rc := 0
	for _, r := range entrypoint.ApplyOrphanRemovals(plan) {
		if r.Err != nil {
			fmt.Fprintf(errw, "yolo programs remove: %s: %v\n", r.Orphan.Display, r.Err)
			rc = 1
		}
	}
	return rc
}

// selectOrphans filters the catalog by name, and reports every name that matched nothing.
//
// A name matches an orphan's NAME (the npm package, or the file's base name) — what `ls`
// prints in its first column, and what a user would type. Matching the rendered path too
// would make `~/.local/bin/agy` and `agy` two spellings whose difference nobody could
// predict.
func selectOrphans(orphans []entrypoint.Orphan, names []string) (selected []entrypoint.Orphan, unknown []string) {
	if len(names) == 0 {
		return orphans, nil
	}
	for _, want := range names {
		found := false
		for _, o := range orphans {
			if o.Name == want || o.Display == want {
				selected = append(selected, o)
				found = true
			}
		}
		if !found {
			unknown = append(unknown, want)
		}
	}
	return selected, unknown
}

// planTotal sums a plan's reclaimable bytes.
func planTotal(plan []entrypoint.OrphanRemoval) int64 {
	var total int64
	for _, r := range plan {
		total += r.Bytes
	}
	return total
}

// countLabel renders "1 orphan" / "3 orphans" so a count reads as a sentence.
func countLabel(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
