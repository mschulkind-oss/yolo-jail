package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// configDrift compares the workspace config the jail was BUILT from against the
// workspace config on disk NOW, and prints the difference. It exists so an agent
// running inside a jail can tell whether the human has retuned yolo-jail.jsonc since
// the jail started — i.e. whether a restart is needed to pick up the change.
//
// Exit codes are the interface for an agent (no output parsing needed):
//
//	0  in sync — the workspace config on disk matches what started this jail
//	3  DRIFT — they differ (the unified diff is printed)
//	4  cannot determine — no boot baseline (a pre-feature jail, or run outside one)
//	1  an error occurred
//
// Drift compares the CANONICAL form of each config (sorted keys, stable formatting),
// so reordering keys or reformatting the source file is not drift; only a real
// value or structure change is.
//
// WORKSPACE-ONLY, AND SINCE OQ-LP9 THAT LIMIT HAS TO BE SAID OUT LOUD. The user half of the
// config is not comparable from inside a jail, and the reason is structural rather than
// missing work: what a jail sees as user scope is a GENERATED, FILTERED render of the host's
// effective config (internal/config/inherit.go), so there is no host file in here to diff
// against — and diffing the generated file against itself would answer a question nobody
// asked. Before OQ-LP9 the host's real config.jsonc was bind-mounted live, so a user-half
// edit was visible instantly and drift never came up; now it is LAUNCH-FROZEN like every
// other jail input (env, image, relay wiring).
//
// So this command reports what it can compare and NAMES what it cannot, rather than saying
// "in sync" and letting the reader conclude the user half was checked. That over-claim is
// exactly the shape of bug this batch already shipped a fix for elsewhere — a surface that
// answered confidently about a scope it had never looked at.
func configDrift(args []string, out, errw io.Writer, color bool) int {
	if len(args) > 0 {
		if isHelpToken(args[0]) {
			io.WriteString(out, configUsage+"\n")
			return 0
		}
		fmt.Fprintf(errw, "yolo config drift: unexpected argument %q\n", args[0])
		return 2
	}

	// Empty workspace → WorkspaceConfigDrift defaults to cwd, which is /workspace
	// in a container jail and the invoking dir on the host — the place the baseline
	// and the live workspace config both live.
	diffLines, hasDrift, ok, err := config.WorkspaceConfigDrift("")
	if err != nil {
		fmt.Fprintf(errw, "yolo config drift: %v\n", err)
		return 1
	}
	pr := richtext.Printer{W: out, Color: color}
	if !ok {
		pr.Printf("[yellow]No boot baseline found[/yellow] — this does not look like a jail " +
			"started by a yolo that writes one (or it was started before this feature). " +
			"Cannot determine drift.")
		return 4
	}
	if !hasDrift {
		pr.Printf("[green]In sync[/green] — the workspace config on disk matches the one that " +
			"started this jail. No restart needed for config reasons.")
		printUserScopeDriftLimit(pr)
		return 0
	}
	pr.Printf("[bold yellow]Workspace config has drifted since this jail started.[/bold yellow]")
	pr.Printf("[dim]Restart the jail to apply these changes.[/dim]")
	pr.Printf("")
	for _, line := range diffLines {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			pr.Printf("[dim]%s[/dim]", line)
		case strings.HasPrefix(line, "+"):
			pr.Printf("[green]%s[/green]", line)
		case strings.HasPrefix(line, "-"):
			pr.Printf("[red]%s[/red]", line)
		case strings.HasPrefix(line, "@@"):
			pr.Printf("[cyan]%s[/cyan]", line)
		default:
			pr.Print(line)
		}
	}
	printUserScopeDriftLimit(pr)
	return 3
}

// printUserScopeDriftLimit names the scope this command does NOT compare — in a jail only.
//
// OQ-LP9 R7: the inherited user scope is launch-frozen, so a host-side user-config edit is
// invisible until the next launch and this command cannot detect it (there is no host file in
// here to diff — the jail's user scope is a generated render). Saying so is the whole point:
// an agent that reads "In sync" and concludes nothing changed anywhere would go on to debug
// a stale `packs` list or a missing loophole as if it were a code problem.
//
// Printed on BOTH the in-sync and drifted paths, because the limit is a property of the
// command rather than of the result. Not printed on the host, where the user config is right
// there and live. Exit codes are untouched: the limit is not itself drift, and an agent keying
// on 0/3/4 must keep working.
func printUserScopeDriftLimit(pr richtext.Printer) {
	if os.Getenv("YOLO_VERSION") == "" {
		return
	}
	pr.Printf("")
	pr.Printf("[dim]Note: this compares the WORKSPACE config only. Your user-level config " +
		"(~/.config/yolo-jail/config.jsonc on the host) reaches this jail as a generated, " +
		"filtered snapshot taken at launch, so an edit to it is not visible in here and not " +
		"detectable as drift — it applies on the next launch. `yolo config dump` shows the " +
		"effective config this jail is actually running under.[/dim]")
}

// configDump prints the full COMPUTED config as canonical snapshot JSON — the same
// form the startup config-change diff validates against (sorted keys, ASCII-escaped,
// 2-space indent). This is the effective, merged config yolo actually assembled: the
// authoritative machine-readable answer to "what config is this jail running under".
//
// In a jail this is the boot snapshot read verbatim (LoadConfig prefers it); on the
// host it is the freshly assembled user+workspace merge. Either way it is the canonical
// serialization, so its bytes are stable and diffable.
func configDump(args []string, out, errw io.Writer) int {
	if len(args) > 0 {
		if isHelpToken(args[0]) {
			io.WriteString(out, configUsage+"\n")
			return 0
		}
		fmt.Fprintf(errw, "yolo config dump: unexpected argument %q\n", args[0])
		return 2
	}
	cfg, err := config.LoadConfig("", false, func(string) {})
	if err != nil {
		fmt.Fprintf(errw, "yolo config dump: %v\n", err)
		return 1
	}
	j, err := config.SnapshotJSON(cfg)
	if err != nil {
		fmt.Fprintf(errw, "yolo config dump: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, j)
	return 0
}
