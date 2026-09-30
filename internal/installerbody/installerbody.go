// Package installerbody says what a downloaded vendor installer IS, before anything runs it: a
// script, a web page, a binary, or bytes that are neither. It is the host's copy of the check the
// jail's native launcher makes (_installer_body_kind in internal/entrypoint/shims.go), used by
// `yolo internal installer-check`, which a `via: installer` remedy at the host runs between its
// download and its `sh` (docs/design/provisioner-sets.md PS-D4).
//
// TWO IMPLEMENTATIONS, ON PURPOSE (PS-D6): the launcher's is shell, because it must work with no
// yolo on PATH and under macOS's stock bash 3.2, and this one is Go, because the host remedy runs
// wherever the user's shell is. TestTheGoAndShellChecksAgree (internal/entrypoint) runs every
// launcher fixture through both, so a rule changed in one and not the other fails there.
//
// The rule, over the body's FIRST KiB (1024 bytes), in this order:
//
//   - Binary: a NUL byte in it, whatever else the body says;
//   - Markup: the first line, after leading blanks, opens `<!doctype`, `<html` or `<?xml`, in
//     any case: a moved endpoint answering 200 with a web page;
//   - Script: the first line starts with `#!`;
//   - NonText: a byte file(1)'s text table marks as never appearing in text (0x01-0x06,
//     0x0E-0x19, 0x1C-0x1F, 0x7F; bytes 0x80 and up are text, so UTF-8 passes);
//   - Script otherwise: a shebang-less script is unusual but valid.
package installerbody

import (
	"bytes"
	"io"
)

// Kind is the verdict on a body.
type Kind string

const (
	// Script is a body that may be run.
	Script Kind = "script"
	// Markup is a web page (the first line opens like HTML or XML).
	Markup Kind = "markup"
	// Binary is a body with a NUL byte in its first KiB.
	Binary Kind = "binary"
	// NonText is a body with no `#!` line and a byte that never appears in text.
	NonText Kind = "nontext"
)

// window is how much of the body the byte checks read.
const window = 1024

// firstLineLimit bounds the first line the markup and shebang checks read. Both need only its
// opening bytes, and past the blanks a page opens within a few; a first line of blanks longer
// than this is not a page anyone serves.
const firstLineLimit = 64 * 1024

// Classify reads r and returns its verdict. It reads at most firstLineLimit bytes.
func Classify(r io.Reader) (Kind, error) {
	buf, err := io.ReadAll(io.LimitReader(r, firstLineLimit))
	if err != nil {
		return "", err
	}
	head := buf
	if len(head) > window {
		head = head[:window]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return Binary, nil
	}
	first := buf
	if i := bytes.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	if isMarkup(first) {
		return Markup, nil
	}
	if bytes.HasPrefix(first, []byte("#!")) {
		return Script, nil
	}
	for _, c := range head {
		if neverText(c) {
			return NonText, nil
		}
	}
	return Script, nil
}

// isMarkup is the launcher's `^[[:space:]]*<(!doctype|html|?xml)`, case-insensitive, over a first
// line with its newline removed. [[:space:]] there is a space, tab, vertical tab, form feed or
// carriage return.
func isMarkup(line []byte) bool {
	rest := bytes.TrimLeft(line, " \t\v\f\r")
	lower := bytes.ToLower(rest)
	for _, open := range []string{"<!doctype", "<html", "<?xml"} {
		if bytes.HasPrefix(lower, []byte(open)) {
			return true
		}
	}
	return false
}

// neverText is file(1)'s text table's never-in-text set, less the NUL Classify tests first.
func neverText(c byte) bool {
	return (c >= 0x01 && c <= 0x06) || (c >= 0x0e && c <= 0x19) || (c >= 0x1c && c <= 0x1f) || c == 0x7f
}

// Why is the refusal's clause for a kind that must not run, in the launcher's words, and "" for
// Script.
func Why(k Kind) string {
	switch k {
	case Markup:
		return "it served a web page."
	case Binary:
		return "it served binary data (a NUL byte in its first KiB)."
	case NonText:
		return "it served bytes that are neither a #! script nor text."
	}
	return ""
}

// Advice is what to do about a refused kind, in the launcher's words.
func Advice(k Kind) string {
	if k == Markup {
		return "The pack's installer URL is probably stale; check the tool's docs for its " +
			"current install command."
	}
	return "Nothing was run. A vendor CDN serving the wrong file has been transient before, so " +
		"retry; if it persists, the pack's installer URL no longer names an install script."
}
