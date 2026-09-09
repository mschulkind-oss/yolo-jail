package cli

// outputformat.go is the ONE flag front end for `--format json` across yolo's
// state-reporting commands (`ps`, `prune`, `loopholes list`, `broker status`,
// `check`), and the one place the accepted spellings are decided.
//
// It exists because of who operates this CLI. Agents are the primary operators
// (docs/reference/self-documenting-cli.md, item 7 and its "Why this matters here
// specifically" §2): the jail is credential-isolated, so an in-jail agent cannot
// check the host by hand, and until this flag existed the only way it could read
// jail state, a reclaim plan or a loophole's health was to scrape ANSI-decorated
// prose written for a human eye. Every one of those commands already had the
// answer as a struct or a set of locals; none of them could hand it over.
//
// TWO SPELLINGS, and the second is not decoration. `--format json` is canonical
// — item 7 names it, and `yolo host env --format json` shipped it first — but
// `yolo stores --json` shipped the bare form before any of this, and an agent
// that learned `--json` from `stores` will type it at `ps`. A CLI where the flag
// for machine output depends on which command you are in fails the very standard
// this file implements, so both spellings work everywhere in the family and the
// help text names `--format json` as the canonical one.
//
// An unknown --format value is REFUSED (exit 2, stderr) rather than ignored.
// That is the whole reason to parse the value at all: silently printing human
// prose to something that asked for JSON is the failure this flag exists to
// prevent, and it is indistinguishable from a command that has no JSON at all.

import (
	"fmt"
	"io"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
)

// outputFormatUsage is the help block every command in the family prints,
// verbatim, so the flag reads identically wherever it is met. Commands splice it
// into their own `Flags:` list.
//
// It is a const rather than a per-command sentence for the reason the whole file
// exists: an operator who has read one of these help texts has read them all.
const outputFormatUsage = `  --format <fmt>           Output format: text (default) or json. JSON is stable,
                           ANSI-free and on stdout — the form to parse.
  --json                   Shorthand for --format json (the spelling
                           ` + "`yolo stores`" + ` shipped first; both work everywhere).`

// parseOutputFormat scans a state-reporting command's argv for the output-format
// flag family and returns the resolved format.
//
// ok=false means the caller must return 2 immediately: the reason is already on
// errw, and this is misuse (self-documenting-cli item 3 — misuse goes to stderr
// with a non-zero exit, and is distinct from help).
//
// It STOPS AT `--` for the same reason helpRequested does: everything after the
// separator belongs to an inner command. No command in this family takes one
// today, which is exactly why the guard is cheap to keep — it costs nothing now
// and removes an ordering dependency if one ever grows a `--`.
//
// Unrecognized tokens are left alone. Each command still owns its own flag
// parse; this reads only the format family out of the same argv.
func parseOutputFormat(sub string, args []string, errw io.Writer) (string, bool) {
	format := outfmt.Text
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return format, validOutputFormat(sub, format, errw)
		case a == "--json":
			format = outfmt.JSON
		case a == "--format":
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo %s: --format needs a value (%s|%s)\n",
					sub, outfmt.Text, outfmt.JSON)
				return format, false
			}
			i++
			format = args[i]
		case strings.HasPrefix(a, "--format="):
			format = a[len("--format="):]
		}
	}
	return format, validOutputFormat(sub, format, errw)
}

// validOutputFormat reports whether format is one this CLI emits, complaining to
// errw when it is not.
func validOutputFormat(sub, format string, errw io.Writer) bool {
	if outfmt.Valid(format) {
		return true
	}
	fmt.Fprintf(errw, "yolo %s: unknown --format %q (want %s or %s)\n",
		sub, format, outfmt.Text, outfmt.JSON)
	return false
}
