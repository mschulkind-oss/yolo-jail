package cli

// startupbanner.go owns the one line every `yolo` subcommand writes to stderr
// before it runs: `yolo-jail <version> | <platform> | host|in-jail`.
//
// # Where it hooks, and why there
//
// dispatchNative is the single choke point for the whole documented CLI surface
// — every one of the registry's commands arrives there, from both of Main's
// dispatch sites — so the banner is one insertion rather than one per command,
// and a command added to the registry tomorrow inherits it with no second edit.
// TestEveryRegisteredCommandGetsTheStartupBanner walks the registry to keep that
// true.
//
// # What deliberately does NOT get one
//
// Three surfaces answer inside Main, above dispatchNative, and are excluded by
// that position rather than by a list:
//
//   - `yolo internal …` — the hidden namespace. It holds the host daemons
//     (`internal daemon claude-oauth-broker`, which self-execs and runs for the
//     life of a jail), `internal config-dump` (the differential-testing oracle),
//     `internal bundle-dir` (prints one path and nothing else) and
//     `internal capture-run`. None is a command a human types into a bug report,
//     and two of them are read by other programs.
//   - `yolo --version` — it prints the version, on stdout, as its whole job.
//   - `yolo --help` / `-h` / `help`.
//
// # Why stderr, and why no isatty gate
//
// See internal/banner's package doc: several commands have machine-readable
// stdout (`config dump`'s canonical JSON, `config drift`'s agent-facing exit
// codes, `describe --json`, `config-ref`) that a banner on stdout would corrupt,
// and the piped case is precisely the one a bug report is pasted from, so a
// terminal gate would suppress the line exactly where it is wanted.

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/banner"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// emitStartupBanner writes the startup banner to w, unless banner.SuppressEnv is
// set. getenv is injected so both the hatch and the host/in-jail side are
// testable without mutating the process environment.
func emitStartupBanner(w io.Writer, getenv func(string) string) {
	if banner.Suppressed(getenv) {
		return
	}
	// Resolve the repo root the SAME way run/check/`--version` do (the shared
	// method), so an unstamped binary describes the yolo-jail repo and never the
	// version of whatever repo the cwd happens to sit in. A miss yields "", which
	// version.Get answers with the baked stamp or "unknown" — it never shells out
	// to git in the cwd.
	repoRes, _ := reporoot.Resolve(getenv)
	fmt.Fprintln(w, banner.Startup(version.Get(repoRes.Root), getenv))
}
