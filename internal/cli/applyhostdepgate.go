package cli

// applyhostdepgate.go is docs/design/report-tiers.md §4.9's DEPENDENCY GATE: on `--assert`, a
// declared dependency that is missing STOPS the run — at the prompt, with nothing written.
//
// WHAT IT REVERSES, and this is worth stating because a sibling doc rules the opposite for a
// sibling verb: env-manager plan OQ-9 ruled a declined install NON-FATAL, on the grounds that
// the manifest is only written and never run. §6 protects the reversal explicitly for THIS
// verb, and §4.9 gives the reason — an `--assert`'s promise is a ready environment, so a
// posture that returns 0 having left it unready has stated a result it did not achieve. The
// tree already agreed with the fatal half elsewhere: `yolo check-deps` exits non-zero over the
// same probe of the same declared hints (checkdeps.go), so one verb called it a line and the
// other called it a failure.
//
// FIVE PROPERTIES, each of which §4.9 decides outright rather than leaving to the implementer:
//
//   - FATAL AT THE PROMPT, NOT AT THE END. A later stage of the apply may come to rely on the
//     tool, so continuing past a NO is continuing into an environment already known to be
//     incomplete — and an end-of-run failure could not truthfully say *nothing was written*.
//     This runs before the first render, on the pre-flight's answer, which is the position
//     that makes both halves of that sentence true.
//   - ONE PROMPT, listing every missing dependency and the exact command each install would
//     run, answered once — the shape confirmHostLosses already uses. Phase 4.3's batched,
//     elevation-class-grouped confirm may refine how many prompts there are and how elevation
//     is disclosed; it never refines whether a decline is fatal.
//   - SILENCE IS NO. promptYesNo reads a nil or EOF stdin as NO, so an unattended --assert
//     refuses rather than installing. It deliberately does NOT test for a terminal — its
//     docstring makes that a contract — and §4.9 point 4 rules that NO TERMINAL GATE IS ADDED
//     HERE: a deliberately piped `y` installs, and what protects the user is that the commands
//     are the packs' own declared hints, printed above the prompt before it is answered.
//   - AN INSTALL THAT RUNS AND LEAVES THE BINARY MISSING IS A DECLINE. Re-probe after each
//     install; still-missing is the same fatal, named in the refusal.
//   - ONLY `program` IS OFFERED AN INSTALL (OQ-RO7). Both kinds are fatal — a missing
//     `requires` is the more clear-cut blocker, since `guardrails` removes `grep`/`find` in
//     favour of binaries that must be present — but offering to install one would contradict
//     the kind's own definition, so it refuses with the remedy named. isDepKind stays folded
//     for the PROBE (below the jail notch both kinds ask the host the same question); the
//     split is here, at the offer.
//
// NO HATCH FLAG. There is no --ignore-missing-deps: an escape hatch is for a user's broken
// configuration, not for a verdict they dislike. The way to say "not on this machine" is to
// drop the pack from `packs`, and the way to see the config half regardless is the dry run,
// which prints everything, prompts for nothing and writes nothing.

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/depcheck"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostDepBlocker is one missing declared dependency, merged across every pack that declares it
// — §4.4's remedy key for this class is the BINARY, across packs, so two packs declaring `rg`
// are one blocker with one install command.
type hostDepBlocker struct {
	// Bin is the binary, and the group key.
	Bin string
	// hostDepFinding is the probe's answer for it: the state, the remedy, its alternative,
	// and why there is none. Embedded rather than copied field by field so a finding that
	// grows a field reaches the gate and the report together.
	hostDepFinding
}

// installable reports whether yolo has an install to OFFER for this dependency — OQ-RO7's
// split, and the only place the two dep kinds are told apart.
//
// Two conditions, and both are the offer's rather than the blocker's: the kind must be
// `program` (a `requires` asserts a binary must ALREADY exist, so offering to install it
// contradicts its definition), and there must be a command to run (a program whose manifest
// carries neither a via nor a usable hint is just as fatal, with nothing to propose).
func (f hostDepFinding) installable() bool {
	return f.Kind == packdecl.KindProgram && f.Remedy != ""
}

// hostDepBlockers is every missing declared dependency this run found, in sorted order, read
// from the SURVEY — the one collector the pre-flight filled (applyhostdeps.go).
//
// Sorted by binary because both consumers print it: the gate names them in a prompt and a
// refusal, and the dry run's tier-3 groups name them again, and a report that prints the same
// set in two orders is a report a reader has to diff by hand.
func hostDepBlockers(s *hostApplySurvey) []hostDepBlocker {
	var out []hostDepBlocker
	for _, bin := range s.MissingDeps() {
		out = append(out, hostDepBlocker{Bin: bin, hostDepFinding: s.MissingDepFinding(bin)})
	}
	return out
}

// depInstallRun runs one install command, behind a var so a test can observe what the gate
// would run against a real host without running it. The seam is the ONLY way a test reaches
// this code: an automated test must never execute a pack's install hint (AGENTS.md's
// no-agent-tests rule is the same rule one layer down), and the commands are `sudo apt install`
// and `curl … | sh`.
var depInstallRun = runDepInstallCommand

// runDepInstallCommand runs a declared install hint through a shell.
//
// A SHELL, deliberately: the hints are not argv, they are commands a user would type —
// `curl -fsSL https://… | sh` is the shape depcheck's selfInstallFlavor produces for an
// `installer` program — so splitting on spaces would mangle exactly the remedy the pack wrote
// down. They run as the invoking user with the invoking environment, which is what makes
// `sudo` in a hint behave the way the user's own shell would.
func runDepInstallCommand(cmd string, out io.Writer) error {
	c := exec.Command("sh", "-c", cmd)
	c.Stdout, c.Stderr = out, out
	return c.Run()
}

// gateHostDeps is the whole gate. It returns the exit code of a run that must not continue,
// and 0 to carry on — so the caller's one line reads as the refusal it is.
//
// It is called ONLY in the --assert posture. The dry run reports a missing dependency as a
// tier-3 blocker, names it in the verdict, exits 0 and never prompts (§4.9's posture table,
// OQ-RO5): its whole output IS the finding, and a posture that writes nothing has nothing to
// gate.
func gateHostDeps(pr richtext.Printer, out io.Writer, stdin io.Reader,
	deps hostDepPreflight, survey *hostApplySurvey) int {
	blockers := hostDepBlockers(survey)
	if len(blockers) == 0 {
		return 0
	}
	// The BLOCKER GROUPS FIRST, in the same shape the dry run prints them (hostapplyremedy.go):
	// what is missing, and the exact command each install would run. §4.9 point 4 rests on
	// this — the protection against a piped `y` is that the commands are visible above the
	// prompt, which means they are printed whether the answer is yes, no or nobody's.
	printRemedyGroups(pr, depBlockerGroups(blockers))

	var offer, unoffered []hostDepBlocker
	for _, b := range blockers {
		if b.installable() {
			offer = append(offer, b)
			continue
		}
		unoffered = append(unoffered, b)
	}
	if len(unoffered) > 0 {
		// OQ-RO7: fatal, and never offered. Refused for the WHOLE set rather than prompting
		// for the installable half first — the run cannot complete either way, so an install
		// prompt here would spend the user's `y` on a host this run has already refused.
		pr.Printf("[bold red]host apply: refused — %s, and yolo installs neither a `requires` "+
			"nor a dependency it has no command for. Nothing was written.[/bold red]",
			depBlockerPhrase(unoffered))
		return 1
	}

	pr.Printf("[dim]yolo will run the %s above, as you, and re-check each binary "+
		"afterwards.[/dim]", plural(len(offer), "command", "commands"))
	if !promptYesNo(out, stdin, fmt.Sprintf("  Install %s now? [y/N] ",
		plural(len(offer), "it", "them"))) {
		pr.Printf("[bold red]host apply: refused — %s and the install was declined. "+
			"Nothing was written.[/bold red]", depBlockerPhrase(offer))
		return 1
	}
	for _, b := range offer {
		pr.Printf("  [cyan]→ %s[/cyan]", b.Remedy)
		if err := depInstallRun(b.Remedy, out); err != nil {
			pr.Printf("  [red]%s: %v[/red]", b.Bin, err)
		}
		// RE-PROBE, and it is the command's answer rather than its exit code that decides
		// (§4.9 point 5): an installer that exits 0 and delivers nothing leaves the
		// environment exactly as unready as one that failed loudly.
		//
		// AND IT STOPS HERE, not after the rest of the set: "we cannot continue" is the same
		// sentence the decline gets, so nothing further runs — neither a render nor the next
		// install. The user consented to the commands as a set, not to having the remainder
		// run against a host this run has already refused.
		path, ok := depcheck.Present(b.Bin)
		if !ok {
			pr.Printf("[bold red]host apply: refused — installing `%s` did not produce it. "+
				"Nothing was written.[/bold red]", b.Bin)
			return 1
		}
		deps.markInstalled(b.Bin, path)
		survey.noteInstalled(b.Bin)
		pr.Printf("  [green]✓[/green] %s [dim]%s[/dim]", b.Bin, path)
	}
	return 0
}

// depBlockerPhrase names the blockers the way the refusal sentence needs them: `rg` is
// missing / `rg`, `fd` are missing. The NAMES, because §4.3 requires a blocker to contribute
// its name to the result — the reader's next action is about those binaries.
func depBlockerPhrase(blockers []hostDepBlocker) string {
	names := make([]string, 0, len(blockers))
	for _, b := range blockers {
		names = append(names, "`"+b.Bin+"`")
	}
	return fmt.Sprintf("%s %s missing", strings.Join(names, ", "),
		plural(len(names), "is", "are"))
}
