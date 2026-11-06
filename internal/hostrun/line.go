// Package hostrun is `yolo host-run`: a command a human runs on the host, whose stdout, stderr
// and exit status are delivered to the jail as files in the workspace's mailbox,
// <workspace>/.yolo/host-run/.
//
// The PASTE LINE, `yolo host-run [-C DIR] [--name LABEL] -- ARGV`, is the whole interface: an
// agent or a human writes it by hand, and running it on the host is the approval (HX-P1,
// HX-D35). The host form (host.go) is the only thing that runs anything; the jail forms (wait.go)
// only read. The host never reads a file beneath `.yolo` (HX-P3).
//
// Architecture and invariants: docs/reference/host-run-mailbox.md
package hostrun

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// Usage is the one-line grammar every usage refusal repeats.
const Usage = "yolo host-run [-C DIR] [--name LABEL] -- COMMAND [ARG...]"

// Line is one parsed paste line.
type Line struct {
	Dir   string   // -C, "" when absent
	Label string   // --name, "" when absent
	Argv  []string // after --, non-empty
}

// UsageError is a paste line that does not parse. Its message names the one fix.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string {
	return e.Msg + "; nothing ran. The line is: " + Usage
}

func usagef(format string, a ...any) error { return &UsageError{Msg: fmt.Sprintf(format, a...)} }

// ParseHostArgs parses the arguments after `host-run`: `-C` and `--name` at most once each,
// before a required `--`, then a non-empty argv that is not itself `yolo host-run …`.
// Everything after `--` is argv, whatever it looks like.
func ParseHostArgs(args []string) (Line, error) {
	var l Line
	seenC, seenName := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			l.Argv = append([]string(nil), args[i+1:]...)
			if len(l.Argv) == 0 {
				return Line{}, usagef("no command after `--`; put the command to run there")
			}
			if isRecursive(l.Argv) {
				return Line{}, usagef("the command is itself `yolo host-run`; write the inner command after `--` instead")
			}
			return l, nil
		case a == "-C" || strings.HasPrefix(a, "-C="):
			if seenC {
				return Line{}, usagef("`-C` is given twice; keep one")
			}
			seenC = true
			v, next, err := flagValue(args, i, "-C")
			if err != nil {
				return Line{}, err
			}
			if v == "" {
				return Line{}, usagef("`-C` is empty; name the workspace directory (in the jail, `echo $YOLO_HOST_DIR` prints it)")
			}
			l.Dir, i = v, next
		case a == "--name" || strings.HasPrefix(a, "--name="):
			if seenName {
				return Line{}, usagef("`--name` is given twice; keep one")
			}
			seenName = true
			v, next, err := flagValue(args, i, "--name")
			if err != nil {
				return Line{}, err
			}
			if err := CheckLabel(v); err != nil {
				return Line{}, err
			}
			l.Label, i = v, next
		default:
			if strings.HasPrefix(a, "-") {
				return Line{}, usagef("unknown flag %s; only -C and --name come before `--`", shquote.QuoteDisplay(a))
			}
			return Line{}, usagef("%s comes before `--`; put `--` before the command", shquote.QuoteDisplay(a))
		}
	}
	return Line{}, usagef("no `--`; put `--` before the command to run")
}

// flagValue reads the value of the flag at args[i], glued (`-C=dir`) or the next word.
func flagValue(args []string, i int, name string) (string, int, error) {
	if v, ok := strings.CutPrefix(args[i], name+"="); ok {
		return v, i, nil
	}
	if i+1 >= len(args) {
		return "", i, usagef("`%s` needs a value", name)
	}
	return args[i+1], i + 1, nil
}

// selfName is the base name this process was invoked as, so a launcher installed under
// another name (`just deploy yolo-stable`, `just deploy-dev yolo-dev`) still catches itself.
var selfName = func() string { return filepath.Base(os.Args[0]) }

// isRecursive reports whether argv is `yolo host-run …`, by the program's base name: `yolo`,
// or whatever name this yolo was itself invoked as.
func isRecursive(argv []string) bool {
	if len(argv) < 2 || argv[1] != "host-run" {
		return false
	}
	base := filepath.Base(argv[0])
	return base == "yolo" || base == selfName()
}

var labelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// CheckLabel checks a --name against its grammar (HX-D46).
func CheckLabel(label string) error {
	if labelPattern.MatchString(label) {
		return nil
	}
	return usagef("the --name label must be 1 to 40 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit, such as merge-42-q7")
}

// RuneError is a word holding a rune the disclosure could not show truthfully (HX-D41). It
// names the word's position and the code point, never the word itself.
type RuneError struct {
	Index int  // 0-based position in the checked words
	Rune  rune // utf8.RuneError for a byte that is not UTF-8
	Byte  int  // that byte, or -1
	What  string
}

func (e *RuneError) Error() string {
	what := e.What
	if what == "" {
		what = fmt.Sprintf("word %d of the command", e.Index+1)
	}
	var cp string
	if e.Byte >= 0 {
		cp = fmt.Sprintf("byte 0x%02x, which is not UTF-8", e.Byte)
	} else {
		cp = fmt.Sprintf("U+%04X, %s", e.Rune, runeClass(e.Rune))
	}
	return what + " holds " + cp + ", which would make the printed command differ from what runs; " +
		"nothing ran. Retype that word, or put the text in a file in the workspace that the command reads"
}

func runeClass(r rune) string {
	switch {
	case unicode.IsControl(r):
		return "a control character"
	case unicode.Is(unicode.Bidi_Control, r):
		return "a bidi-format character"
	case unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
		return "a line or paragraph separator"
	default:
		return "an invisible format character"
	}
}

// badRune reports whether r may not appear in a word: what termsafe flags, and Unicode Cf, Zl
// and Zp, which termsafe passes.
func badRune(r rune) bool {
	return termsafe.Unsafe(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// CheckWords is the rune check over words, returning a *RuneError for the first bad rune.
func CheckWords(words []string) error {
	for i, w := range words {
		for j := 0; j < len(w); {
			r, n := utf8.DecodeRuneInString(w[j:])
			if r == utf8.RuneError && n == 1 {
				return &RuneError{Index: i, Rune: r, Byte: int(w[j])}
			}
			if badRune(r) {
				return &RuneError{Index: i, Rune: r, Byte: -1}
			}
			j += n
		}
	}
	return nil
}

// CheckLine runs the rune check over the argv, -C and the label, naming which one failed.
func CheckLine(l Line) error {
	if err := CheckWords(l.Argv); err != nil {
		return err
	}
	for _, f := range []struct{ v, what string }{{l.Dir, "the -C directory"}, {l.Label, "the --name label"}} {
		if err := CheckWords([]string{f.v}); err != nil {
			var re *RuneError
			if errors.As(err, &re) {
				re.What = f.what
			}
			return err
		}
	}
	return nil
}

// quoteWord is shquote.Quote, plus single quotes around a word beginning `=` (zsh expands it
// to a program path) or `%` (fish expands `%self`), which Quote leaves bare.
func quoteWord(w string) string {
	q := shquote.Quote(w)
	if q == w && (strings.HasPrefix(w, "=") || strings.HasPrefix(w, "%")) {
		return "'" + w + "'"
	}
	return q
}

// FormatLine writes l as a paste line.
func FormatLine(l Line) string {
	var b strings.Builder
	b.WriteString("yolo host-run")
	if l.Dir != "" {
		b.WriteString(" -C " + quoteWord(l.Dir))
	}
	if l.Label != "" {
		b.WriteString(" --name " + quoteWord(l.Label))
	}
	b.WriteString(" --")
	for _, w := range l.Argv {
		b.WriteString(" " + quoteWord(w))
	}
	return b.String()
}
