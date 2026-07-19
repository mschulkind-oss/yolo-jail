package oauthterminator

import (
	"fmt"
	"io"
	"log"
	"os"
)

// Operational logging — the (lighter) forensics contract ported from
// src/oauth_broker_jail.py. The in-jail terminator log is a first-class triage
// surface: docs/research/claude-token-logouts.md greps it for the per-request
// `is_refresh=` line ("if the log shows only is_refresh=False ... /login") and
// the layer-named `refresh failed` / `proxy failed` errors. Ported so an
// unattended soak keeps that forensics.
//
// Sink + level mechanics mirror the established Go daemon convention (commit
// ec888c6, cmd/yolo-cgd + cmd/yolo-journald): a --log-file flag defaulting to
// stderr. Here stderr is captured by the in-jail daemon supervisor into
// ~/.local/state/yolo-jail-daemons/claude-oauth-broker.log — exactly where the
// Python terminator's logging.basicConfig(stderr) output landed. Python's
// levelname + logger name ("oauth-broker-jail") are preserved per line.
//
// SECURITY: this daemon terminates Claude's TLS, so request bodies can carry
// tokens. Like the Python terminator, we log method/path/body_len and never
// bodies; the one spot Python renders a response dict (the malformed-proxy
// error) redacts body_b64 here — see Errorf call sites.

const loggerName = "oauth-broker-jail"

var (
	logger     *log.Logger
	logVerbose bool
)

// SetupLog configures the operational logger. Empty path -> stderr (the
// supervisor-captured default). verbose enables DEBUG (Python's `-v`).
func SetupLog(path string, verbose bool) {
	var out io.Writer = os.Stderr
	if path != "" {
		if f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644); err == nil {
			out = f
		}
	}
	setupLogWriter(out, verbose)
}

// setupLogWriter is the injectable core (tests point it at a bytes.Buffer).
func setupLogWriter(w io.Writer, verbose bool) {
	logger = log.New(w, "", log.LstdFlags)
	logVerbose = verbose
}

func logAt(level, format string, args ...any) {
	if logger == nil {
		return
	}
	logger.Printf("%s %s: %s", level, loggerName, fmt.Sprintf(format, args...))
}

// LogInfo / LogWarn / LogError are exported because the terminator's request
// handling lives partly in the cmd (main package), which mirrors Python's
// _handle / main() log sites.
func LogInfo(format string, args ...any)  { logAt("INFO", format, args...) }
func LogWarn(format string, args ...any)  { logAt("WARNING", format, args...) }
func LogError(format string, args ...any) { logAt("ERROR", format, args...) }

// logDebug is gated on verbose (Python only emits DEBUG under `-v`).
func logDebug(format string, args ...any) {
	if !logVerbose {
		return
	}
	logAt("DEBUG", format, args...)
}
