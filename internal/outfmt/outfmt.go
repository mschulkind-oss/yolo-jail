// Package outfmt holds the output-format vocabulary yolo's state-reporting
// commands share: the two format names, and the writer swap that produces a
// machine-readable document instead of a human report.
//
// It is a package of its own — three consts and one function — because the
// alternative is worse. The commands that need it are `internal/cli` (the flag
// front end), `internal/prune`, `internal/loopholes` and `internal/broker`, and
// the last three deliberately do NOT import internal/cli: they are the engines,
// the CLI is the front door, and the dependency only ever points one way. So the
// string "json" would otherwise be spelled four times in four packages, which is
// exactly the shape that drifts — one of them accepting `--format JSON` or
// `--format=json ` and nobody noticing until an agent's parse comes back empty.
//
// It holds no flag parsing. Which spellings a command accepts, and what it does
// with an unknown value, is the CLI's business and lives in
// internal/cli/outputformat.go beside the help text that documents it.
package outfmt

import "io"

// The two output formats. Text is what every one of these commands printed
// before `--format` existed and stays the default: a bare invocation is
// unchanged.
const (
	Text = "text"
	JSON = "json"
)

// Valid reports whether format is one this CLI emits. An empty string counts as
// Text — a zero-valued Options field means "nobody asked for anything", which is
// the human report.
func Valid(format string) bool {
	return format == "" || format == Text || format == JSON
}

// IsJSON reports whether format selects the machine-readable document.
func IsJSON(format string) bool { return format == JSON }

// Sink returns where a command's HUMAN report should go while a JSON document is
// being produced: nowhere.
//
// THE HUMAN OUTPUT IS DISCARDED, NEVER RESHAPED. These commands print as they
// probe — prune's report is ~650 lines of interleaved print-and-measure — and
// the human form is the contract existing readers already have, so bending it
// into something an encoder finds convenient would break the surface that works
// in order to add one that does not exist yet. Discarding it instead leaves the
// text path byte-identical (its goldens still pin it) and lets the same single
// pass fill a report struct on the side.
func Sink(out io.Writer, format string) io.Writer {
	if IsJSON(format) {
		return io.Discard
	}
	return out
}
