package wirebridged

// accesslog.go is the bridge's REQUEST LOG: one line per request on every listener, written
// when the request ends, plus a "still waiting" line every stillWaitingInterval while one is in
// flight (docs/reference/wire-bridge.md, "The daemon log").
//
// WHY. On 2026-10-11 a Claude Code session hung for 13 minutes, and this daemon's log held its
// startup lines and nothing else: no request, no upstream status, no transport error, no
// latency, no time of day. Whether the bridge was even asked could not be answered. Everything
// a reconstruction needs is on the one line now:
//
//	2026-10-11T12:17:03.412-04:00 wire-bridge: request #7 POST /v1/messages 200 listener=adapter
//	  peer=127.0.0.1:40122 caller=- auth=ok
//	  upstream=https://bedrock-runtime.us-east-1.amazonaws.com/anthropic/v1/messages
//	  upstream_status=200 upstream_headers_ms=812 upstream_bytes=48211 bytes_out=48211
//	  first_byte_ms=815 elapsed_ms=10234 streamed=yes end=clean (model … untranslated to …)
//
// (one line in the file; wrapped here). The line is built from a record (reqLog) that rides
// the request's context: the caller-token check writes the auth outcome into it, and every
// upstream request the handlers send goes through observedTransport, which writes the target,
// the status or the transport error and the response body's fate. So no handler has to
// remember to log, and a new route is logged by existing.
//
// NEVER A VALUE. The line carries names, numbers and error text yolo or the network stack
// composed: no header value, no credential, no body, no query string. The upstream target is
// scheme, host and path only. The TestTheRequestLogNeverCarriesTheCallerToken test drives
// every outcome with the real token and a wrong one and fails if either string reaches the log.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// stillWaitingInterval is how long a request may be in flight before the log says so, and how
// often it says so again.
const stillWaitingInterval = 30 * time.Second

// stillWaitingOverride, when nonzero, replaces stillWaitingInterval so a test need not wait. It is
// never set in production. Atomic because a handler goroutine from an earlier test's bridge may
// still be starting its ticker while the next test sets or restores it.
var stillWaitingOverride atomic.Int64

func stillWaitingEvery() time.Duration {
	if d := time.Duration(stillWaitingOverride.Load()); d > 0 {
		return d
	}
	return stillWaitingInterval
}

// requestSeq numbers requests for the life of the daemon, so a request's "still waiting" lines
// and its final line can be read together.
var requestSeq atomic.Uint64

// reqLogKey is the context key a request's record rides under.
type reqLogKey struct{}

// reqLog is one request's record. Every field is written under mu: the handler goroutine,
// the transport and the still-waiting ticker all touch it.
type reqLog struct {
	mu       sync.Mutex
	id       uint64
	start    time.Time
	listener string
	peer     string
	method   string
	path     string
	caller   string
	auth     string
	note     string
	attempts []*upstreamAttempt

	status      int
	wroteHeader bool
	streamed    bool
	bytesOut    int64
	firstByte   time.Duration
	lastWrite   time.Time
	writeErr    error
	aborted     bool
}

// upstreamAttempt is one request the bridge sent upstream on the agent's behalf. A retry (an
// expired credential refreshed, the Codex route's once-only 401 retry) is a second attempt.
type upstreamAttempt struct {
	target  string
	sent    time.Time
	headers time.Duration // 0 until the response headers arrive
	status  int
	err     string
	bytes   int64
	end     string // "" while reading, then "eof", "cut: <cause>" or "closed-early"
}

func reqLogFrom(ctx context.Context) *reqLog {
	rl, _ := ctx.Value(reqLogKey{}).(*reqLog)
	return rl
}

// withRequestLog wraps one listener's handler (the caller-token check included) in the
// request log. listener names it in each line: "adapter" or "via".
func withRequestLog(listener string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rl := &reqLog{id: requestSeq.Add(1), start: time.Now(), listener: listener,
			method: r.Method, path: r.URL.Path, peer: r.RemoteAddr, caller: callerFromPath(r.URL.Path), auth: "-"}
		lw := &loggedWriter{ResponseWriter: w, rl: rl}
		stop := rl.watch()
		defer func() {
			stop()
			p := recover()
			if p != nil {
				rl.mu.Lock()
				rl.aborted = true
				rl.mu.Unlock()
			}
			logf("%s", rl.finalLine())
			if p != nil {
				panic(p)
			}
		}()
		next.ServeHTTP(lw, r.WithContext(context.WithValue(r.Context(), reqLogKey{}, rl)))
	})
}

// callerFromPath is the agent a via path names (/agent/<name>/…), else "-": the adapter
// routes carry no caller in the request, and nothing in a header is read for one.
func callerFromPath(p string) string {
	rest, ok := strings.CutPrefix(p, "/agent/")
	if !ok {
		return "-"
	}
	agent, _, _ := strings.Cut(rest, "/")
	if agent == "" {
		return "-"
	}
	return agent
}

// setAuth records the caller-token outcome: "ok", "missing-token" or "wrong-token".
func (rl *reqLog) setAuth(outcome string) {
	if rl == nil {
		return
	}
	rl.mu.Lock()
	rl.auth = outcome
	rl.mu.Unlock()
}

// setNote records a handler's routing note (which upstream answered, a refusal's reason).
func (rl *reqLog) setNote(note string) {
	if rl == nil {
		return
	}
	rl.mu.Lock()
	rl.note = note
	rl.mu.Unlock()
}

// watch starts the still-waiting ticker and returns its stop.
func (rl *reqLog) watch() func() {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(stillWaitingEvery())
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				logf("%s", rl.waitingLine())
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// waitingLine says what an in-flight request is waiting on.
func (rl *reqLog) waitingLine() string {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	var phase string
	a := rl.lastAttempt()
	switch {
	case a == nil:
		phase = "no upstream request sent yet (the bridge is still preparing it: reading the " +
			"request body, or fetching the upstream credential)"
	case a.err != "":
		phase = "the upstream request failed (" + a.err + ") and the handler has not finished"
	case a.headers == 0:
		phase = fmt.Sprintf("still waiting on upstream %s for response headers, %s after sending",
			a.target, now.Sub(a.sent).Round(time.Second))
	case a.end == "":
		since := "no byte relayed to the caller yet"
		if !rl.lastWrite.IsZero() {
			since = "last byte to the caller " + now.Sub(rl.lastWrite).Round(time.Second).String() + " ago"
		}
		phase = fmt.Sprintf("still waiting on upstream %s mid-response: status %d, %d bytes read so far, %s",
			a.target, a.status, a.bytes, since)
	default:
		phase = "the upstream response ended (" + a.end + ") and the handler has not finished"
	}
	return fmt.Sprintf("request #%d %s %s still in flight after %s listener=%s caller=%s auth=%s: %s",
		rl.id, rl.method, quoteIfNeeded(rl.path), now.Sub(rl.start).Round(time.Second),
		rl.listener, quoteIfNeeded(rl.caller), rl.auth, phase)
}

func (rl *reqLog) lastAttempt() *upstreamAttempt {
	if len(rl.attempts) == 0 {
		return nil
	}
	return rl.attempts[len(rl.attempts)-1]
}

// finalLine is the request's one line.
func (rl *reqLog) finalLine() string {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	status := rl.status
	if !rl.wroteHeader {
		status = http.StatusOK // what net/http sends for a handler that wrote nothing
	}
	var b strings.Builder
	fmt.Fprintf(&b, "request #%d %s %s %d listener=%s peer=%s caller=%s auth=%s", rl.id, rl.method,
		quoteIfNeeded(rl.path), status, rl.listener, quoteIfNeeded(rl.peer), quoteIfNeeded(rl.caller), rl.auth)
	if len(rl.attempts) == 0 {
		b.WriteString(" upstream=-")
	}
	for i, a := range rl.attempts {
		key := "upstream"
		if len(rl.attempts) > 1 {
			key = "upstream" + strconv.Itoa(i+1)
		}
		fmt.Fprintf(&b, " %s=%s", key, a.target)
		switch {
		case a.err != "":
			fmt.Fprintf(&b, " %s_error=%s", key, strconv.Quote(a.err))
		default:
			fmt.Fprintf(&b, " %s_status=%d %s_headers_ms=%d %s_bytes=%d", key, a.status,
				key, a.headers.Milliseconds(), key, a.bytes)
			if a.end != "" && a.end != "eof" {
				fmt.Fprintf(&b, " %s_end=%s", key, strconv.Quote(a.end))
			}
		}
	}
	fmt.Fprintf(&b, " bytes_out=%d", rl.bytesOut)
	if rl.bytesOut > 0 {
		fmt.Fprintf(&b, " first_byte_ms=%d", rl.firstByte.Milliseconds())
	}
	fmt.Fprintf(&b, " elapsed_ms=%d streamed=%s end=%s", time.Since(rl.start).Milliseconds(),
		map[bool]string{true: "yes", false: "no"}[rl.streamed], rl.endState())
	if rl.writeErr != nil {
		fmt.Fprintf(&b, " — RESPONSE DELIVERY FAILED after %d bytes: %s (the status is what the "+
			"bridge intended to send; the client did not receive it)", rl.bytesOut, oneLine(rl.writeErr.Error()))
	}
	b.WriteString(rl.note)
	return b.String()
}

// endState is how the response to the caller ended: clean, or why not.
func (rl *reqLog) endState() string {
	switch {
	case rl.aborted:
		return "aborted" // the bridge cut the caller's connection so a truncated reply reads as a failure
	case rl.writeErr != nil:
		return "client-gone"
	}
	if a := rl.lastAttempt(); a != nil && strings.HasPrefix(a.end, "cut") {
		return "upstream-cut"
	}
	return "clean"
}

// quoteIfNeeded keeps a field one token: a value with a space, a quote or a control byte is
// quoted. A path is the agent's, so it can hold anything.
func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if r <= ' ' || r == '"' || r == 0x7f || r > 0x7e {
			return strconv.Quote(s)
		}
	}
	return s
}

// loggedWriter is the caller's ResponseWriter, counting what reaches it.
type loggedWriter struct {
	http.ResponseWriter
	rl *reqLog
}

func (w *loggedWriter) WriteHeader(code int) {
	w.rl.mu.Lock()
	if !w.rl.wroteHeader {
		w.rl.status, w.rl.wroteHeader = code, true
		ct := w.ResponseWriter.Header().Get("Content-Type")
		w.rl.streamed = strings.HasPrefix(ct, "text/event-stream") ||
			strings.HasPrefix(ct, "application/vnd.amazon.eventstream")
	}
	w.rl.mu.Unlock()
	w.ResponseWriter.WriteHeader(code)
}

func (w *loggedWriter) Write(p []byte) (int, error) {
	w.rl.mu.Lock()
	if !w.rl.wroteHeader {
		w.rl.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.rl.mu.Lock()
	}
	w.rl.mu.Unlock()
	n, err := w.ResponseWriter.Write(p)
	w.rl.mu.Lock()
	if n > 0 {
		if w.rl.bytesOut == 0 {
			w.rl.firstByte = time.Since(w.rl.start)
		}
		w.rl.bytesOut += int64(n)
		w.rl.lastWrite = time.Now()
	}
	if err != nil && w.rl.writeErr == nil {
		w.rl.writeErr = err
	}
	w.rl.mu.Unlock()
	return n, err
}

// Flush passes a flush through: every streaming relay asserts http.Flusher on its writer.
func (w *loggedWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the server's writer.
func (w *loggedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// observedTransport is the RoundTripper every upstream client sends through: upstreamTransport
// (net/http's default when nil), with the attempt recorded in the request's log.
type observedTransport struct{}

func (observedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := upstreamTransport
	if base == nil {
		base = http.DefaultTransport
	}
	rl := reqLogFrom(req.Context())
	if rl == nil {
		return base.RoundTrip(req)
	}
	a := &upstreamAttempt{target: redactedTarget(req.URL), sent: time.Now()}
	rl.mu.Lock()
	rl.attempts = append(rl.attempts, a)
	rl.mu.Unlock()
	resp, err := base.RoundTrip(req)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if err != nil {
		a.err = describeUpstreamError(req.Context(), err)
		return nil, err
	}
	a.headers = max(time.Since(a.sent), time.Microsecond)
	a.status = resp.StatusCode
	resp.Body = &observedBody{ReadCloser: resp.Body, rl: rl, a: a, ctx: req.Context()}
	return resp, nil
}

// observedUpstream is the transport each upstream client is built with.
var observedUpstream http.RoundTripper = observedTransport{}

// redactedTarget is scheme://host/path: never a query string, never userinfo.
func redactedTarget(u *url.URL) string {
	if u == nil {
		return "-"
	}
	return quoteIfNeeded((&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath}).String())
}

// observedBody counts an upstream response body and records how it ended.
type observedBody struct {
	io.ReadCloser
	rl  *reqLog
	a   *upstreamAttempt
	ctx context.Context
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.rl.mu.Lock()
	b.a.bytes += int64(n)
	if err != nil && b.a.end == "" {
		if errors.Is(err, io.EOF) {
			b.a.end = "eof"
		} else {
			b.a.end = "cut: " + describeUpstreamError(b.ctx, err)
		}
	}
	b.rl.mu.Unlock()
	return n, err
}

func (b *observedBody) Close() error {
	b.rl.mu.Lock()
	if b.a.end == "" {
		b.a.end = "closed-early" // the handler stopped reading: a relayed error, or the caller left
	}
	b.rl.mu.Unlock()
	return b.ReadCloser.Close()
}

// describeUpstreamError names a transport failure's class and its text: dns, connect refused,
// connection reset, tls, timeout, canceled. The text is the network stack's, which names
// addresses and never a credential; any URL in it is cut to its host and path.
func describeUpstreamError(ctx context.Context, err error) string {
	if ctx != nil && ctx.Err() != nil {
		if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
			if errors.Is(cause, errViaHeaderTimeout) {
				return fmt.Sprintf("timeout: the upstream %s (%s)", cause, viaHeaderTimeout)
			}
			return "timeout: " + oneLine(cause.Error())
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return "canceled: the caller closed the request"
		}
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	text := oneLine(err.Error())
	var dnsErr *net.DNSError
	var recErr tls.RecordHeaderError
	var unknownCA x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verifyErr *tls.CertificateVerificationError
	var netErr net.Error
	switch {
	case errors.As(err, &dnsErr):
		return "dns: " + text
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connect refused: " + text
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset: " + text
	case errors.Is(err, syscall.EPIPE):
		// The upstream dropped the connection while the request was still being written: on macOS a
		// reset surfaces here as EPIPE rather than ECONNRESET.
		return "connection closed: " + text
	case errors.As(err, &recErr), errors.As(err, &unknownCA), errors.As(err, &hostErr),
		errors.As(err, &invalid), errors.As(err, &verifyErr), strings.Contains(text, "tls:"):
		return "tls: " + text
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded),
		errors.As(err, &netErr) && netErr.Timeout():
		return "timeout: " + text
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "connection closed: " + text
	}
	return "error: " + text
}
