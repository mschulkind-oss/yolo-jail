package wirebridged

// diag.go is this package's ONE diagnostic sink, and it exists because the
// bridge's worst bug to date was invisible rather than hard.
//
// THE MEASUREMENT THAT EXISTED NOWHERE. A provider-table aliasing bug leaked a
// pack's jail-loopback address into the user-provider table, so the entrypoint's
// port forwarder bound 127.0.0.1:8214 with socat four lines before the
// supervisor started this daemon. The bridge then could not bind — and nothing
// anywhere said what address it tried, what errno it got, or what already held
// the port. The host-side `ss -ltnp` was empty (the host half of that forward is
// a UNIX socket), so the decisive fact was recorded in no log on either side and
// it took four wrong hypotheses to find. Everything in this file exists so that
// the next one is a single `grep` in the file below.
//
// THE SINK, established rather than assumed: this daemon is a supervised child
// of `yolo-jaild supervise`, which sets BOTH cmd.Stdout and cmd.Stderr to
// <supervisor.LogDir()>/wire-bridge.log — i.e.
// ~/.local/state/yolo-jail-daemons/wire-bridge.log, rotated at 5 MB
// (internal/supervisor's openLog/child.start). So a plain write to os.Stderr
// LANDS IN A FILE a human can open, and there is no other channel out of this
// process: the supervisor's own stderr is /dev/null in a real jail. That file's
// other writer is the supervisor itself (its prefixed `logf` lines — restarts,
// backoff, exit codes), and the entrypoint prints its path when it waits on this
// daemon's readiness ("Daemon diagnostics: …", entrypoint.startJailDaemonSupervisor).
//
// ⚠ NOTHING HERE IS GATED, AND NOTHING MAY BECOME GATED. There is no verbosity
// flag, no env dial and no level: every line this package writes is written on
// every boot. A launch may compress progress but may never suppress a disclosure
// (OQ-RO3, docs/reference/report-tiers.md), and a diagnostic that is off by
// default is a diagnostic that is absent exactly when it is needed — which is
// the defect this file is fixing, not a thing to reintroduce behind a switch.
// The only dedup mechanism is logOnce, which is about a 200ms POLL LOOP writing
// the same sentence forever, not about volume policy.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// diagSink is where every line goes. It is a variable for exactly one reason —
// a test must be able to assert on the body the daemon actually wrote, rather
// than on a sentence a comment promises — and it is never rebound in production.
var (
	diagMu   sync.Mutex
	diagSink io.Writer = os.Stderr
	diagSeen           = map[string]bool{}
)

// logf writes one "wire-bridge: …" line to the sink. Every diagnostic in this
// package goes through here so the prefix, the newline and the serialization are
// in one place: the log file is shared with the supervisor's own lines and with
// this daemon's concurrent request goroutines, so an interleaved half-line would
// be a torn record in the one file the 8214 hunt needed.
func logf(format string, args ...any) {
	line := "wire-bridge: " + strings.TrimRight(fmt.Sprintf(format, args...), "\n") + "\n"
	diagMu.Lock()
	defer diagMu.Unlock()
	_, _ = io.WriteString(diagSink, line)
	// The write error is genuinely discarded, and this is the one place in the
	// package where that is unconditionally right: the sink IS the reporting
	// channel, so a failure to write has nowhere left to be reported. Returning
	// it would only invite callers to log it, here.
}

// logOnce writes a line at most once per process, keyed by key. It exists for
// the malformed-input reports reached from resolveRoute, which the idle path
// re-evaluates every entryChannelPollInterval (200ms) for the daemon's whole
// lifetime: the same sentence five times a second would bury the launch's other
// lines and rotate the file away. Once is not "less loud" — the fact is
// unchanged for as long as the channel is unchanged.
func logOnce(key, format string, args ...any) {
	diagMu.Lock()
	seen := diagSeen[key]
	diagSeen[key] = true
	diagMu.Unlock()
	if seen {
		return
	}
	logf(format, args...)
}

// logWriter adapts the sink to the io.Writer some collaborators take for their
// own diagnostics, prefixing each line so its origin is unambiguous in a shared
// file. It is what replaces the io.Discard that used to swallow the OpenAI
// credential service's stderr frames on the Codex route.
func logWriter(prefix string) io.Writer {
	return &lineWriter{prefix: prefix}
}

type lineWriter struct {
	prefix string
	buf    bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// A partial line: put it back and wait for the rest, so a frame
			// split mid-sentence is not logged as two records.
			w.buf.Reset()
			w.buf.WriteString(line)
			break
		}
		if trimmed := strings.TrimRight(line, "\n"); trimmed != "" {
			logf("%s%s", w.prefix, trimmed)
		}
	}
	return len(p), nil
}

// oneLine collapses a message to a single line. The readiness protocol the
// entrypoint reads is LINE-based and field-joined (entrypoint's readiness
// scanner rejects any line whose first field is not ready/failed), so a
// multi-line detail — an errno string is single-line, but a port holder's argv
// is arbitrary process input — would be read as a second readiness record and
// turn a precise failure into "jail daemon reported unexpected readiness".
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
