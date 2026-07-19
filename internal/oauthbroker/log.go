package oauthbroker

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Operational logging — the incident-derived forensics contract ported from
// src/oauth_broker.py. The shared-identity bug on 2026-04-23 (host Claude
// rotated the shared refresh token out from under the broker) and the
// 2026-05-12 logout loop (silent proxy-mirror failures) were both invisible
// because the broker logged too little. These lines let a soak reconstruct
// what the broker saw: refresh attempts, token FINGERPRINTS (never the tokens
// themselves — see TokenFP), upstream status codes, cache hits/misses, and the
// proxy-mirror decision path.
//
// Sink + level mechanics mirror the established Go daemon convention from
// commit ec888c6 (the cgd delegate — now in-process in yolo run — and
// cmd/yolo-journald): a --log-file flag that
// defaults to stderr (captured by the loophole supervisor), set up via
// SetupLog. The Python broker's level + logger-name are preserved in each line
// so the incident greps (`grep INFO`, `grep bg_refresh`, ...) still work.
// loggerName logging.getLogger("oauth-broker-host").
const loggerName = "oauth-broker-host"

// logger is nil until SetupLog runs (matches the daemons' nil-guarded auditLog:
// no logging configured -> the log calls are no-ops, which unit tests rely on).
var (
	logger     *log.Logger
	logVerbose bool
)

// SetupLog configures the operational logger. An empty path logs to stderr
// (the default the loophole supervisor captures), matching the in-process cgd
// delegate and cmd/yolo-journald. verbose enables DEBUG lines (Python's `-v` ->
// level=DEBUG).
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

// logAt emits one line as "<LEVEL> <name>: <message>", carrying Python's
// levelname + logger name so the incident greps keep working. log.LstdFlags
// prepends the timestamp (the cgd/journald convention).
func logAt(level, format string, args ...any) {
	if logger == nil {
		return
	}
	logger.Printf("%s %s: %s", level, loggerName, fmt.Sprintf(format, args...))
}

func logInfo(format string, args ...any)  { logAt("INFO", format, args...) }
func logWarn(format string, args ...any)  { logAt("WARNING", format, args...) }
func logError(format string, args ...any) { logAt("ERROR", format, args...) }

// logDebug is gated on verbose (Python only emits DEBUG lines under `-v`).
func logDebug(format string, args ...any) {
	if !logVerbose {
		return
	}
	logAt("DEBUG", format, args...)
}

// LogStartup emits the daemon's startup snapshot of the shared creds file.
func LogStartup(credsPath string) {
	logInfo("startup: shared=%s", describeCreds(credsPath))
}

// describeCreds is a one-line summary of a creds file for logging: mtime, the
// access + refresh token FINGERPRINTS (never the tokens), and expiresAt.
// / malformed files (never raises).
func describeCreds(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path + ": <absent>"
		}
		return fmt.Sprintf("%s: stat_error=%s", path, err)
	}
	mtime := st.ModTime().Unix()
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		return fmt.Sprintf("%s: mtime=%d read_error=%s", path, mtime, rerr)
	}
	decoded, jerr := jsonx.Decode(data)
	if jerr != nil {
		return fmt.Sprintf("%s: mtime=%d read_error=%s", path, mtime, jerr)
	}
	// oa = data.get("claudeAiOauth") or {}
	oa := jsonx.NewOrderedMap()
	if root, ok := decoded.(*jsonx.OrderedMap); ok {
		if v, ok := root.Get("claudeAiOauth"); ok {
			if m, ok := v.(*jsonx.OrderedMap); ok {
				oa = m
			}
		}
	}
	at, _ := stringField(oa, "accessToken")
	rt, _ := stringField(oa, "refreshToken")
	return fmt.Sprintf("%s: mtime=%d at=%s rt=%s exp=%s",
		path, mtime, TokenFP(at), TokenFP(rt), describeExpiresAt(oa))
}

// describeExpiresAt renders oa.get("expiresAt") the way Python's f-string would:
// the integer literal when present, "None" when absent (matching str(None)).
func describeExpiresAt(oa *jsonx.OrderedMap) string {
	v, ok := oa.Get("expiresAt")
	if !ok || v == nil {
		return "None"
	}
	if s, err := jsonx.DumpsCompact(v); err == nil {
		return s
	}
	return "None"
}

// fpOf fingerprints a token-shaped field for a log line (accessToken /
// refreshToken from an oauth object), returning "(none)" for a missing /
// non-string value — the TokenFP("") behavior.
func fpOf(m *jsonx.OrderedMap, key string) string {
	s, _ := stringField(m, key)
	return TokenFP(s)
}

// expiresAtStr renders an oauth object's expiresAt field for a log line (the
// "exp=%s" / "old_exp=%s" fields Python passes current.get("expiresAt") to).
func expiresAtStr(m *jsonx.OrderedMap) string { return describeExpiresAt(m) }
