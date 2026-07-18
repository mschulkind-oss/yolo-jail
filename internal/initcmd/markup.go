package initcmd

import "strings"

// ANSI codes for the closed tag set the init briefing uses.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

var markupANSI = strings.NewReplacer(
	"[bold cyan]", ansiBold+ansiCyan, "[/bold cyan]", ansiReset,
	"[bold green]", ansiBold+ansiGreen, "[/bold green]", ansiReset,
	"[bold yellow]", ansiBold+ansiYellow, "[/bold yellow]", ansiReset,
	"[bold]", ansiBold, "[/bold]", ansiReset,
)

var markupStrip = strings.NewReplacer(
	"[bold cyan]", "", "[/bold cyan]", "",
	"[bold green]", "", "[/bold green]", "",
	"[bold yellow]", "", "[/bold yellow]", "",
	"[bold]", "", "[/bold]", "",
)

// renderMarkup renders the briefing's rich tags to ANSI (color) or strips them
// (plain). Info-parity: same text, purposeful color — not rich's byte-exact
// terminal reflow.
func renderMarkup(s string, color bool) string {
	if color {
		return markupANSI.Replace(s)
	}
	return markupStrip.Replace(s)
}
