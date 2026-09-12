package run

// launchlog.go persists what the LAUNCHER says, because until now it said it exactly
// once, to a terminal, and nowhere else.
//
// # The gap it closes
//
// A launch prints from two processes and only one half was ever written down. The
// entrypoint's half is teed into `<workspace>/.yolo/boot.log` (bootlog.go); the host
// launcher's half — the flake source, the nix build, image delivery, the jail line,
// and every pack disclosure — went to the terminal and to nothing else. Checked
// 2026-09-10: no log sink held any of those phrases, and `host-perf.log` records the
// launch SPANS rather than the text. So the diagnoser's evidence for the first half of
// a launch was gone the moment it scrolled, which is worst exactly where it is needed —
// a launch that REFUSED, where there is no jail left to read anything from
// (docs/design/report-tiers.md §3.6, §4.7 *Persist the launcher's half*).
//
// # Where, and why here
//
// `<workspace>/.yolo/`, beside `boot.log`, so both halves of one launch sit in one
// directory — decided by precedent (perf-logging.md D2, which put `host-perf.log`
// there), not opened as a question. D2's caveat is inherited whole: the directory is
// inside the live workspace bind, so a jail can write the host's record. Nothing reads
// this file back to make a decision, which is what keeps that a disclosure rather than
// a trust hole; D2's named remediation (a host-global logs dir) is the fix for the day
// it stops being true.
//
// # Retention, and why it is not boot.log's
//
// boot.log rotates one generation aside (`boot.log.prev`) because a jail boots once per
// launch and the question is "did it work last time?". A HOST accumulates launches
// across every workspace, so the shape that fits is the perf log's: one appended run
// block per launch, trimmed to the newest perf.MaxRuns at open. Same directory, same
// bound, one implementation (perf.TrimRunsInFile).
//
// # Why it is never fatal, and never prints
//
// A logger that can stop a launch is a worse bug than the blindness it fixes, and one
// that ANNOUNCES its own failure adds a line to the launch stream P4 says must stay
// readable. Every failure — no workspace, a read-only mount, a full disk — degrades to
// the plain writers the launch already had, silently. The launch does not learn that
// the log failed, because there is nothing it could usefully do about it.
//
// # What it deliberately does not capture
//
// The version banner, which `internal/cli` prints BEFORE Run is called (the header
// records the version instead, which is the same fact in the one place a reader of this
// file will look for it). The live-overlay refusal, which is Run's first act and
// refuses before any side effect — including this one. And the jailed command's own
// output: the container is spawned with the process's real stdout/stderr (proxy_other.go,
// ttyproxy), never with Options.Stdout, so an interactive session cannot end up in here.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// LaunchLogName is the host launcher's half of one launch's record, under
// <workspace>/.yolo/ beside boot.log (the jail's half) and host-perf.log (the spans).
const LaunchLogName = "launch.log"

// launchRunPrefix delimits runs in the file. The trim splits on the same prefix, so the
// delimiter and the splitter are one spelling, here.
const launchRunPrefix = "=== yolo launch"

// launchLog is the open per-launch log. A nil *launchLog is valid and does nothing,
// which is what every failure path returns.
type launchLog struct{ f *os.File }

// attachLaunchLog opens the log and INSTALLS the tee on o.Stdout and o.Stderr.
//
// It mutates Options rather than returning writers for the caller to install, for the
// reason attachBootLog states: the returned-writer shape has a silent failure mode with
// no test that can catch it — a caller that opens the log and forgets to wire it
// produces a complete, correct-looking, permanently EMPTY log. Both writers are ALWAYS
// left usable: on any failure they stay exactly what they were and the returned
// *launchLog is nil, which every method accepts.
//
// BOTH streams, because the launch's own split is not a rule the reader can rely on:
// the notices go to stderr and the nix build, the image delivery, the config diff and
// its prompt go to stdout (§2.4). A log of one of them would be a log of half a launch,
// and which half would depend on the line.
func attachLaunchLog(o *Options) *launchLog {
	if o.Workspace == "" {
		return nil
	}
	dir := paths.WorkspaceStateDir(o.Workspace)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	path := filepath.Join(dir, LaunchLogName)
	// Trim at open, never at exit: a launch that hangs or is killed still leaves its
	// lines on disk, and the signal teardown's os.Exit would skip a rewrite anyway.
	perf.TrimRunsInFile(path, launchRunPrefix, perf.MaxRuns-1)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	l := &launchLog{f: f}
	l.writeHeader(o)
	o.Stdout = teeLog{w: o.Stdout, log: f}
	o.Stderr = teeLog{w: o.Stderr, log: f}
	return l
}

// writeHeader records what the launch WAS, so a run block read later is self-describing.
//
// The version answers the first question anyone asks of a launch that misbehaved — "is
// this even the code I think it is?" — and it is recorded here because it is the one
// launch line the tee cannot see: `internal/cli` prints the banner before Run exists.
// version.Get("") is the ldflags stamp or "unknown"; it deliberately does not resolve a
// repo root, because the flake source gets its own line inside the block below.
func (l *launchLog) writeHeader(o *Options) {
	if l == nil {
		return
	}
	fmt.Fprintf(l.f, "%s %s ===\n", launchRunPrefix, time.Now().Format("2006-01-02T15:04:05-0700"))
	fmt.Fprintf(l.f, "  yolo=%s  workspace=%s\n", version.Get(""), o.Workspace)
	var absent []string
	for _, k := range launchLogFacts {
		if v := o.Getenv(k); v != "" {
			fmt.Fprintf(l.f, "  %s=%s\n", k, v)
			continue
		}
		absent = append(absent, k)
	}
	// Name the absent ones too, for bootlog.go's reason: an omitted line reads as "not
	// recorded" rather than "empty", and every variable below changes how a later line
	// in this same block should be read.
	if len(absent) > 0 {
		sort.Strings(absent)
		fmt.Fprintf(l.f, "  (unset: %s)\n", strings.Join(absent, " "))
	}
}

// launchLogFacts are the environment opt-ins and hatches that change what a launch DOES
// without appearing in any line it prints. YOLO_VERSION is here as the nested-launch
// marker: set means this launcher is itself running inside a jail, which is the fact
// that explains --net=host, --userns=host and every carve-out that follows from them.
var launchLogFacts = []string{
	"YOLO_VERSION",
	"YOLO_REPO_ROOT",
	"YOLO_RUNTIME",
	"YOLO_ALLOW_STALE_IMAGE",
	"YOLO_ALLOW_SOURCE_SKEW",
	"YOLO_NO_HOST_LOOPBACK",
	"YOLO_STORE_PACKAGES",
	"YOLO_ALLOW_UNREACHABLE_SERVICES",
}

// finish records how the launch ENDED. Without it the block's last line is ambiguous in
// the case that matters most: a refused launch and one killed mid-build both end with
// whatever happened to be printed last.
//
// A teardown that exits through a signal handler's os.Exit never reaches this, which is
// why nothing above waits for it — the header is written at open and every line lands as
// it happens.
func (l *launchLog) finish(rc int) {
	if l == nil {
		return
	}
	fmt.Fprintf(l.f, "=== launch done, rc=%d ===\n", rc)
	_ = l.f.Close()
}

// teeLog passes a launch line through to the terminal UNCHANGED and appends an
// ANSI-free copy to the log.
//
// THE TERMINAL COPY IS THE ORIGINAL BYTES, deliberately. The config-change prompt writes
// its `[y/N] ` through Options.Stdout without a newline and without the printer
// (preflight.go), so anything that reformatted, buffered or line-split what passes
// through here would change what a human is asked. The tee writes the terminal first,
// reports the terminal's own count, and treats the log as best-effort after it.
//
// THE LOG COPY IS STRIPPED, equally deliberately: color is resolved against the real
// terminal (Options.pr consults IsTTYStdout, not the writer), so on an interactive
// launch every styled line arrives here already rendered to escape sequences. A log full
// of them is a log nobody greps twice.
type teeLog struct {
	w   io.Writer
	log io.Writer
}

func (t teeLog) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		_, _ = t.log.Write(stripANSI(p[:n]))
	}
	return n, err
}

// ansiEscape matches a CSI sequence — the colors richtext.ToANSI emits, and any cursor
// motion a subprocess digest happens to carry through.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

func stripANSI(p []byte) []byte { return ansiEscape.ReplaceAll(p, nil) }
