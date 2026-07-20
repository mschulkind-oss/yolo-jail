// Main is the entry point for the `yolo-jaild oauth-terminator` subcommand: the
// in-jail TLS terminator for Claude OAuth. It terminates Claude Code's TLS to
// platform.claude.com (routed to 127.0.0.1 by --add-host), and forwards to the
// host broker: POST /v1/oauth/token with grant_type=refresh_token ->
// action=refresh; everything else -> action=proxy.
// Hazards:
//   - KEEP-ALIVE DISABLED: Claude Code expects per-request connections
//     (HTTP/1.0 style); Go's net/http keeps connections alive by default. We
//     SetKeepAlivesEnabled(false) + send Connection: close to preserve the
//     observable connection behavior.
//   - Content-Length is recomputed (we set it); the caller's is dropped.
//   - The 502/400 status mapping + layer-named error detail come from the
//     handler in this package.
package oauthterminator

import (
	"crypto/tls"
	"flag"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// Main runs the in-jail TLS terminator. argv is the args after the
// `oauth-terminator` subcommand (flags only).
func Main(argv []string) int {
	fs := flag.NewFlagSet("oauth-terminator", flag.ContinueOnError)
	host := fs.String("host", "127.0.0.1", "listen host")
	port := fs.Int("port", 443, "listen port")
	cert := fs.String("cert", "/var/lib/yolo-jail/loopholes/claude-oauth-broker/server.crt", "TLS cert")
	key := fs.String("key", "/var/lib/yolo-jail/loopholes/claude-oauth-broker/server.key", "TLS key")
	hostSocket := fs.String("host-socket", os.Getenv("YOLO_SERVICE_CLAUDE_OAUTH_BROKER_SOCKET"),
		"Unix socket for the host-side broker (default: from env)")
	logFile := fs.String("log-file", "", "append the operational log here (default: stderr)")
	verbose := fs.Bool("verbose", false, "verbose logging")
	verboseShort := fs.Bool("v", false, "verbose logging")
	if err := fs.Parse(argv); err != nil {
		return 2
	}

	// Empty --log-file -> stderr, which the in-jail daemon supervisor captures
	// into ~/.local/state/yolo-jail-daemons/claude-oauth-broker.log (the same
	// sink the Python terminator's basicConfig(stderr) landed in).
	SetupLog(*logFile, *verbose || *verboseShort)

	if *hostSocket == "" {
		// Python logs this via log.error (basicConfig -> stderr); the logger's
		// default sink is stderr too, so a single LogError matches the wire.
		LogError("no host socket path available — expected YOLO_SERVICE_CLAUDE_OAUTH_BROKER_SOCKET")
		return 2
	}
	if !isFile(*cert) || !isFile(*key) {
		LogError("missing %s or %s — did `just deploy` run --init-ca?", *cert, *key)
		return 2
	}

	srv := &http.Server{
		Addr:    *host + ":" + itoa(*port),
		Handler: makeHandler(*hostSocket),
		// Pin HTTP/1.1: an empty TLSNextProto disables ALPN 'h2' negotiation.
		// Python's ssl context offers no ALPN (HTTP/1.x only); auto-negotiated
		// HTTP/2 would strip Connection: close, force lowercase headers, and
		// multiplex — voiding the keep-alive-disabled parity below.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}
	// HTTP/1.0-style per-request close, matching BaseHTTPRequestHandler +
	// Claude Code's reconnect-each-time behavior.
	srv.SetKeepAlivesEnabled(false)

	LogInfo("listening on https://%s:%d (intercepting %s -> %s)",
		*host, *port, UpstreamHost, *hostSocket)

	if err := srv.ListenAndServeTLS(*cert, *key); err != nil && err != http.ErrServerClosed {
		LogError("yolo-jaild oauth-terminator: %s", err)
		return 1
	}
	return 0
}

func makeHandler(hostSocket string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		isToken := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/oauth/token")
		isRefresh := isToken && IsRefreshGrant(body)
		ua := r.Header.Get("User-Agent")
		if ua == "" {
			ua = "-"
		}
		// Per-request line. body_len/ua
		// only — never the body (it can carry a token on the /login path).
		LogInfo("request: %s %s body_len=%d is_refresh=%t ua=%s",
			r.Method, r.URL.RequestURI(), len(body), isRefresh, pyRepr(ua))

		var result ProxyResult
		if isRefresh {
			result = Refresh(hostSocket)
		} else {
			result = ProxyUpstream(hostSocket, r.Method, r.URL.RequestURI(), flattenHeaders(r.Header), body)
			LogInfo("proxy: %s %s -> %d body_len=%d",
				r.Method, r.URL.RequestURI(), result.Status, len(result.Body))
		}
		writeResult(w, result)
	})
}

// flattenHeaders collapses http.Header (multi-value) to a single-value map,
// mirroring Python's dict(self.headers) (last value wins per key).
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		if len(vals) > 0 {
			out[k] = vals[len(vals)-1]
		}
	}
	return out
}

func writeResult(w http.ResponseWriter, res ProxyResult) {
	hdr := w.Header()
	sentCT := false
	for k, v := range res.Headers {
		if strings.EqualFold(k, "content-length") {
			continue // recomputed by the writer
		}
		if strings.EqualFold(k, "content-type") {
			sentCT = true
		}
		// Write header names VERBATIM (the plan/module-map-frozen hazard):
		// net/http emits a key stored via direct map assignment without
		// CanonicalMIMEHeaderKey, so an upstream 'x-request-id' survives to the
		// client byte-for-byte, matching Python's send_header. w.Header().Set
		// would canonicalize it to 'X-Request-Id'.
		hdr[k] = []string{v}
	}
	if !sentCT {
		hdr["Content-Type"] = []string{"application/json"}
	}
	hdr["Connection"] = []string{"close"}
	w.WriteHeader(res.Status)
	_, _ = w.Write(res.Body)
}

// pyRepr renders the User-Agent the way Python's f-string "{ua!r}" does in the
// per-request log line.
func pyRepr(s string) string { return pytext.Repr(s) }

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}
