package runcmd

import (
	"fmt"
	"io"
	"regexp"
)

// richTagRe strips rich console markup ([bold red]…[/bold red], [dim]…[/dim]).
// run_cmd's human chatter is NOT under the byte-parity contract (only the
// ordered argv, yolo-user-env.sh bytes, shlex-quoting, and frozen host-state
// contracts are); we reproduce the TEXT content and drop the markup, matching
// the Stage-15 output-contract precedent for check.
var richTagRe = regexp.MustCompile(`\[/?[a-zA-Z][^\]]*\]`)

// stripRich removes rich markup tags, leaving the plain text.
func stripRich(s string) string {
	return richTagRe.ReplaceAllString(s, "")
}

// printer writes run()'s console lines. Rich markup in the message is stripped
// (parity is on text content, not the markup bytes). Writes to w.
type printer struct {
	w io.Writer
}

// print renders one console.print line (rich markup stripped) + newline.
func (p printer) print(msg string) {
	fmt.Fprintln(p.w, stripRich(msg))
}

// printf is print with a format string.
func (p printer) printf(format string, args ...any) {
	fmt.Fprintln(p.w, stripRich(fmt.Sprintf(format, args...)))
}
