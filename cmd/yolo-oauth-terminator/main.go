// Command yolo-oauth-terminator is the Go port of src/oauth_broker_jail.py —
// the in-jail TLS terminator for Claude OAuth (baked with the jail-side wave,
// Stage 11). It terminates Claude Code's TLS to platform.claude.com (routed to
// 127.0.0.1 by --add-host), and forwards to the host broker: POST
// /v1/oauth/token with grant_type=refresh_token -> action=refresh; everything
// else -> action=proxy.
//
// Frozen hazards handled here:
//   - KEEP-ALIVE DISABLED: Python's BaseHTTPRequestHandler closes per request
//     (HTTP/1.0) and Claude Code reconnects each time; Go's net/http keeps
//     connections alive by default. We SetKeepAlivesEnabled(false) + send
//     Connection: close to preserve the observable connection behavior.
//   - Content-Length is recomputed (we set it); the caller's is dropped.
//   - The 502/400 status mapping + layer-named error detail come from
//     internal/oauthterminator (byte-frozen against the Python handler).
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/oauthterminator"
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
	flag.Bool("verbose", false, "verbose logging")
	flag.Bool("v", false, "verbose logging")
	flag.Parse()

	if *hostSocket == "" {
		fmt.Fprintln(os.Stderr, "no host socket path available — expected YOLO_SERVICE_CLAUDE_OAUTH_BROKER_SOCKET")
		return 2
	}
	if !isFile(*cert) || !isFile(*key) {
		fmt.Fprintf(os.Stderr, "missing %s or %s — did `just deploy` run --init-ca?\n", *cert, *key)
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

	if err := srv.ListenAndServeTLS(*cert, *key); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "yolo-oauth-terminator:", err)
		return 1
	}
	return 0
}

func makeHandler(hostSocket string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		isToken := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/oauth/token")
		var result oauthterminator.ProxyResult
		if isToken && oauthterminator.IsRefreshGrant(body) {
			result = oauthterminator.Refresh(hostSocket)
		} else {
			result = oauthterminator.ProxyUpstream(hostSocket, r.Method, r.URL.RequestURI(), flattenHeaders(r.Header), body)
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
