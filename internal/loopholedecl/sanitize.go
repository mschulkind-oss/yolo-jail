package loopholedecl

import (
	"fmt"
	"unicode/utf8"
)

// SANITIZE AT LOAD, NOT AT DISPLAY (docs/design/loophole-packaging.md §3.2).
//
// Every field a manifest declares that can end up in an APPROVAL CLAIM — the
// `host_daemon.cmd` strings, `doctor_cmd`, `jail_daemon.cmd`, the intercept hosts,
// the bind-mount host and container paths, the device nodes, `ca_cert`,
// `state_files`, and the loophole's own name, which is every claim's target — must
// refuse control characters and newlines.
//
// The reason is the rendering path, not squeamishness about odd bytes. The
// approval prompt prints claims through richtext.Printer.Printf, which FORMATS
// FIRST and then parses style tags over the result (richtext.go: Printf →
// fmt.Sprintf → Render), and ToANSI rewrites only the tags it recognizes — every
// other byte passes through untouched. So a manifest string carrying a newline can
// inject an extra, entirely fabricated claim line:
//
//	"cmd": ["python3", "srv.py\n      [dim]mount ~/Documents -> /ctx/docs[/dim]"]
//
// and one carrying raw ESC can erase what came before it (`\e[2K\e[A` walks up and
// clears the ⚠ header) — on the one screen the whole trust story rests on.
//
// Refusing at load beats escaping at display: it is ONE gate rather than a rule
// every future renderer must remember, and the author hears about it instead of
// shipping a manifest that renders wrong on somebody else's terminal.
//
// This is deliberately NOT a general "no weird characters in manifests" rule.
// `description`, env keys and env values are not sanitized here: they do not feed
// a claim target or a claim detail today, and widening the refusal to fields with
// no consumer would reject manifests for no reason. A field that starts feeding a
// claim must be added to this list at the same time.

// refuseControlChars rejects a control character, a newline, or invalid UTF-8 in
// one claim-feeding value. field is the manifest-relative name used in the error.
func refuseControlChars(manifestPath, field, value string) error {
	for i, r := range value {
		if !isControl(r) {
			continue
		}
		return Errorf(
			"%s: %s contains a control character (U+%04X at byte %d) — this value can"+
				" appear in the approval prompt, which formats the line before it parses"+
				" style tags, so a newline could forge an extra claim and an ESC could"+
				" erase the warning header; remove it",
			manifestPath, field, r, i)
	}
	if !utf8.ValidString(value) {
		// A BACKSTOP, and knowingly one: a lone 0x9B byte would range as U+FFFD
		// above (so isControl misses it) while a terminal still reads the raw byte
		// as CSI — but json5.Decode already substitutes U+FFFD for an invalid byte,
		// so no such string reaches here today. Kept because the requirement is
		// "a value that reaches the approval prompt is text", and that must not
		// depend on a decoder detail one layer down.
		return Errorf(
			"%s: %s is not valid UTF-8 — a value that reaches the approval prompt must be"+
				" text, because a raw byte in the C1 range is an escape sequence there",
			manifestPath, field)
	}
	return nil
}

// refuseControlCharsIn applies refuseControlChars to every element of an argv,
// naming the offending index.
func refuseControlCharsIn(manifestPath, field string, args []string) error {
	for i, s := range args {
		if err := refuseControlChars(manifestPath, fmt.Sprintf("%s[%d]", field, i), s); err != nil {
			return err
		}
	}
	return nil
}

// isControl reports whether r is a C0 control (newline and tab included), DEL, or
// a C1 control — the ranges a terminal interprets rather than prints.
func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}
