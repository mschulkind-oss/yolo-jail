package cli

import (
	"fmt"
	"io"
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
func configDrift(args []string, out, errw io.Writer, color bool) int {
	for _, a := range args {
		if isHelpToken(a) {
			io.WriteString(out, configUsage+"\n")
			return 0
		}
		fmt.Fprintf(errw, "yolo config drift: unexpected argument %q\n", a)
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
	return 3
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
	for _, a := range args {
		if isHelpToken(a) {
			io.WriteString(out, configUsage+"\n")
			return 0
		}
		fmt.Fprintf(errw, "yolo config dump: unexpected argument %q\n", a)
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
