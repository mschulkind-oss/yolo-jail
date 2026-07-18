package brokercmd

import "strings"

// ANSI codes for the closed tag set the broker command bodies use:
// [bold], [dim], [green], [red], [yellow], [cyan].
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

var markupANSI = strings.NewReplacer(
	"[bold]", ansiBold, "[/bold]", ansiReset,
	"[dim]", ansiDim, "[/dim]", ansiReset,
	"[red]", ansiRed, "[/red]", ansiReset,
	"[green]", ansiGreen, "[/green]", ansiReset,
	"[yellow]", ansiYellow, "[/yellow]", ansiReset,
	"[cyan]", ansiCyan, "[/cyan]", ansiReset,
)

var markupStrip = strings.NewReplacer(
	"[bold]", "", "[/bold]", "",
	"[dim]", "", "[/dim]", "",
	"[red]", "", "[/red]", "",
	"[green]", "", "[/green]", "",
	"[yellow]", "", "[/yellow]", "",
	"[cyan]", "", "[/cyan]", "",
)

// renderMarkup renders the closed rich-tag set to ANSI (color) or strips it
// (plain). Info-parity: same text, purposeful color — not rich's byte-exact
// terminal reflow.
func renderMarkup(s string, color bool) string {
	if color {
		return markupANSI.Replace(s)
	}
	return markupStrip.Replace(s)
}
