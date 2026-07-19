// Command yolo-oauth-terminator is the in-jail TLS terminator for Claude
// OAuth. It terminates Claude Code's TLS to platform.claude.com (routed to
// 127.0.0.1 by --add-host), and forwards to the host broker: POST
// /v1/oauth/token with grant_type=refresh_token -> action=refresh; everything
// else -> action=proxy.
//
// Hazards:
//   - KEEP-ALIVE DISABLED: Claude Code expects per-request connections
//     (HTTP/1.0 style); Go's net/http keeps connections alive by default. We
//     SetKeepAlivesEnabled(false) + send Connection: close to preserve the
//     observable connection behavior.
//   - Content-Length is recomputed (we set it); the caller's is dropped.
//   - The 502/400 status mapping + layer-named error detail come from
//     internal/oauthterminator.
package main

import (
	"crypto/tls"
	"flag"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/oauthterminator"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

func main() {
	os.Exit(run())
}

func run() int {
	host := flag.String("host", "127.0.0.1", "listen host")
	port := flag.Int("port", 443, "listen port")
	cert := flag.String("cert", "/var/lib/yolo-jail/loopholes/claude-oauth-broker/server.crt", "TLS cert")
	key := flag.String("key", "/var/lib/yolo-jail/loopholes/claude-oauth-broker/server.key", "TLS key")
	hostSocket := flag.String("host-socket", os.Getenv("YOLO_SERVICE_CLAUDE_OAUTH_BROKER_SOCKET"),
		"Unix socket for the host-side broker (default: from env)")
	logFile := flag.String("log-file", "", "append the operational log here (default: stderr)")
	verbose := flag.Bool("verbose", false, "verbose logging")
	verboseShort := flag.Bool("v", false, "verbose logging")
	flag.Parse()

	// Empty --log-file -> stderr, which the in-jail daemon supervisor captures
	// into ~/.local/state/yolo-jail-daemons/claude-oauth-broker.log (the same
	// sink the Python terminator's basicConfig(stderr) landed in).
	oauthterminator.SetupLog(*logFile, *verbose || *verboseShort)

	if *hostSocket == "" {
		// Python logs this via log.error (basicConfig -> stderr); the logger's
		// default sink is stderr too, so a single LogError matches the wire.
		oauthterminator.LogError("no host socket path available — expected YOLO_SERVICE_CLAUDE_OAUTH_BROKER_SOCKET")
		return 2
	}
	if !isFile(*cert) || !isFile(*key) {
		oauthterminator.LogError("missing %s or %s — did `just deploy` run --init-ca?", *cert, *key)
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

	// Mirrors main()'s startup log.info in oauth_broker_jail.py.
	oauthterminator.LogInfo("listening on https://%s:%d (intercepting %s -> %s)",
		*host, *port, oauthterminator.UpstreamHost, *hostSocket)

	if err := srv.ListenAndServeTLS(*cert, *key); err != nil && err != http.ErrServerClosed {
		oauthterminator.LogError("yolo-oauth-terminator: %s", err)
		return 1
	}
	return 0
}

func makeHandler(hostSocket string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		isToken := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/oauth/token")
		isRefresh := isToken && oauthterminator.IsRefreshGrant(body)
		ua := r.Header.Get("User-Agent")
		if ua == "" {
			ua = "-"
		}
		// Per-request line (mirrors _handle's opening log.info). body_len/ua
		// only — never the body (it can carry a token on the /login path).
		oauthterminator.LogInfo("request: %s %s body_len=%d is_refresh=%t ua=%s",
			r.Method, r.URL.RequestURI(), len(body), isRefresh, pyRepr(ua))

		var result oauthterminator.ProxyResult
		if isRefresh {
			result = oauthterminator.Refresh(hostSocket)
		} else {
			result = oauthterminator.ProxyUpstream(hostSocket, r.Method, r.URL.RequestURI(), flattenHeaders(r.Header), body)
			// Mirrors _handle's post-proxy log.info summary line.
			oauthterminator.LogInfo("proxy: %s %s -> %d body_len=%d",
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

func writeResult(w http.ResponseWriter, res oauthterminator.ProxyResult) {
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
