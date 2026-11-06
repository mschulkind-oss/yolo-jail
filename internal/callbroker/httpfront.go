package callbroker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const (
	httpFrontBodyLimit          = 3 << 20
	httpFrontHeaderLimit        = 64
	httpFrontHeaderBytes        = 16 << 10
	httpFrontFrameLimit         = 48 << 20
	httpFrontErrorBodySize      = 4 << 10
	httpFrontHeaderCaptureLimit = 64 << 10
)

const httpFrontPossibleTransmissionMessage = "The host may have transmitted this request; the remote effect or credential use may have occurred, and a spent one-time allowance is not refunded. Inspect remote state and approval before resubmitting."

// HTTPFront is the jail-side, non-authoritative HTTP adapter for a selected
// HTTP call broker. Host authorization is asserted only by svcendpoint's TLS
// preamble; request headers, including Yolo-* and Authorization, carry no trust.
type HTTPFront struct {
	Endpoint string
	Timeout  time.Duration
}

// ServeHTTP implements one-shot HTTP forwarding through the selected host
// endpoint. It does not retain request bodies or credentials between calls.
func (f HTTPFront) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers, authorization := frontHeaders(r)
	w.Header().Set("Connection", "close")
	if f.Endpoint == "" {
		f.writeRequestRefusal(w, "Selected host broker endpoint is unavailable; relaunch with the selected pack.", "", "", authorization)
		return
	}
	if r.Method == http.MethodConnect || r.Method == "PRI" {
		f.writeRequestRefusal(w, "This HTTP method is not supported by the selected call broker.", "", "", authorization)
		return
	}
	path := r.URL.EscapedPath()
	if r.URL.IsAbs() || r.URL.Opaque != "" {
		f.writeRequestRefusal(w, "Absolute-form HTTP targets are not accepted; send a path to the selected local front.", "", "", authorization)
		return
	}
	if path == "" {
		path = "/"
	}
	query := r.URL.RawQuery
	if len(path)+len(query)+boolLength(query != "") > httpRawAggregateLimit {
		f.writeRequestRefusal(w, "HTTP request path and query exceed the 16 KiB limit; reduce the request and retry.", "", "", authorization)
		return
	}
	if len(headers) > httpFrontHeaderLimit {
		f.writeRequestRefusal(w, "HTTP request exceeds the 64-header limit; reduce the request headers and retry.", "", "", authorization)
		return
	}
	headerBytes := 0
	for _, header := range headers {
		for _, value := range header.Values {
			headerBytes += len(value)
		}
	}
	if headerBytes > httpFrontHeaderBytes {
		f.writeRequestRefusal(w, "HTTP request headers exceed the 16 KiB value limit; reduce the request headers and retry.", "", "", authorization)
		return
	}

	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	presence, _ := r.Context().Value(httpFrontConnContextKey{}).(*httpFrontConn)
	hasContentLength := false
	if presence != nil {
		var known bool
		hasContentLength, known = presence.requestBodyHeader()
		if !known {
			f.writeRequestRefusal(w, "HTTP request headers exceed the front's capture limit; reduce the headers and retry.", "", "", authorization)
			return
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = presence.SetReadDeadline(deadline)
		}
	}
	if r.ContentLength > httpFrontBodyLimit {
		f.writeRequestRefusal(w, "HTTP request body exceeds 3 MiB; reduce the body and retry.", "", "", authorization)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, httpFrontBodyLimit+1))
	if err != nil || len(body) > httpFrontBodyLimit {
		f.writeRequestRefusal(w, "HTTP request body exceeds 3 MiB or could not be read; reduce the body and retry.", "", "", authorization)
		return
	}
	if len(r.Trailer) != 0 {
		f.writeRequestRefusal(w, "HTTP request trailers are unsupported by the selected call broker; retry without trailers.", "", "", authorization)
		return
	}
	bodyPresent := len(body) != 0 || r.ContentLength > 0 || len(r.TransferEncoding) != 0 || r.Body != http.NoBody || hasContentLength
	call := HTTPCall{Method: r.Method, Path: path, Query: query, Headers: headers, BodyPresent: bodyPresent,
		Body: body}
	if err := validateHTTPFields(call.Method, call.Path, call.Query); err != nil {
		f.writeRequestRefusal(w, "HTTP request method, path or query is invalid; correct it and retry.", "", "", authorization)
		return
	}

	result, err := f.callHost(ctx, "act", call)
	if err != nil {
		if httpFrontMayHaveTransmitted(err) {
			f.writeRequestRefusal(w, httpFrontPossibleTransmissionMessage, "refused", "", authorization)
		} else {
			f.writeRequestRefusal(w, "The selected host broker could not be reached; inspect host launch state before retrying.", "", "", authorization)
		}
		return
	}
	if result.stderr != "" {
		f.writeRequestRefusal(w, httpFrontPossibleTransmissionMessage, "refused", "", authorization)
		return
	}
	var verdict Verdict
	if err := decodeFrontJSON(result.stdout, &verdict); err == nil && (verdict.Verdict == "asks" || verdict.Verdict == "refused") {
		f.writeRequestRefusal(w, frontVerdictMessage(verdict), verdict.Verdict, verdict.RequestID, authorization)
		return
	}
	if result.exit != 0 {
		f.writeRequestRefusal(w, httpFrontPossibleTransmissionMessage, "refused", "", authorization)
		return
	}
	var reply struct {
		Status  int         `json:"status"`
		Headers http.Header `json:"headers"`
		Body    []byte      `json:"body_base64"`
	}
	if err := decodeFrontJSON(result.stdout, &reply); err != nil || reply.Status < http.StatusOK || reply.Status > 599 {
		f.writeRequestRefusal(w, httpFrontPossibleTransmissionMessage, "refused", "", authorization)
		return
	}
	copyFrontResponseHeaders(w.Header(), reply.Headers)
	if authorization {
		w.Header().Set("Yolo-Authorization-Stripped", "true")
	}
	if r.Method != http.MethodHead && reply.Status != http.StatusNoContent && reply.Status != http.StatusNotModified {
		w.Header().Set("Content-Length", strconv.Itoa(len(reply.Body)))
	}
	w.WriteHeader(reply.Status)
	if r.Method == http.MethodHead || reply.Status == http.StatusNoContent || reply.Status == http.StatusNotModified {
		return
	}
	if _, err := w.Write(reply.Body); err != nil {
		fmt.Fprintln(os.Stderr, "call-broker-http-front: local client response delivery is incomplete; inspect remote state before resubmitting")
		return
	}
}

type httpFrontConnContextKey struct{}

// httpFrontConn records the raw content-length header because net/http normalizes
// absent and explicit Content-Length: 0 to the same Request state. The front closes
// each client connection, so one captured request header unambiguously describes its
// request body-presence bit.
type httpFrontConn struct {
	net.Conn
	mu                  sync.Mutex
	header              []byte
	headerComplete      bool
	headerCaptureFailed bool
	hasContentLength    bool
}

func (c *httpFrontConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.observeHeader(p[:n])
	}
	return n, err
}

func (c *httpFrontConn) observeHeader(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.headerComplete {
		return
	}
	remaining := httpFrontHeaderCaptureLimit - len(c.header)
	if remaining > len(data) {
		remaining = len(data)
	}
	if remaining > 0 {
		c.header = append(c.header, data[:remaining]...)
	}
	end := bytes.Index(c.header, []byte("\r\n\r\n"))
	if end < 0 {
		if len(c.header) >= httpFrontHeaderCaptureLimit {
			c.headerComplete = true
			c.headerCaptureFailed = true
		}
		return
	}
	for _, line := range bytes.Split(c.header[:end], []byte("\r\n"))[1:] {
		name, _, ok := bytes.Cut(line, []byte(":"))
		if ok && strings.EqualFold(string(name), "Content-Length") {
			c.hasContentLength = true
			break
		}
	}
	c.headerComplete = true
}

func (c *httpFrontConn) requestBodyHeader() (hasContentLength, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasContentLength, c.headerComplete && !c.headerCaptureFailed
}

func frontListener(listener net.Listener) net.Listener {
	return httpFrontListener{Listener: listener}
}

type httpFrontListener struct{ net.Listener }

func (l httpFrontListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &httpFrontConn{Conn: conn, header: make([]byte, 0, 1024)}, nil
}

func newHTTPFrontServer(front HTTPFront) *http.Server {
	return &http.Server{Handler: front, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: httpFrontHeaderCaptureLimit,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if tracked, ok := conn.(*httpFrontConn); ok {
				return context.WithValue(ctx, httpFrontConnContextKey{}, tracked)
			}
			return ctx
		}}
}

type httpFrontHostResult struct {
	stdout []byte
	stderr string
	exit   int
}

type httpFrontCallError struct {
	possibleTransmission bool
	cause                error
}

func (e *httpFrontCallError) Error() string { return e.cause.Error() }
func (e *httpFrontCallError) Unwrap() error { return e.cause }

func httpFrontMayHaveTransmitted(err error) bool {
	var callErr *httpFrontCallError
	return errors.As(err, &callErr) && callErr.possibleTransmission
}

func (f HTTPFront) callHost(ctx context.Context, op string, call HTTPCall) (httpFrontHostResult, error) {
	var conn net.Conn
	var err error
	if deadline, ok := ctx.Deadline(); ok {
		conn, err = svcendpoint.DialUntil(f.Endpoint, deadline)
	} else {
		conn, err = svcendpoint.Dial(f.Endpoint, 5*time.Second)
	}
	if err != nil {
		return httpFrontHostResult{}, err
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	payload, err := json.Marshal(struct {
		Operation string   `json:"op"`
		HTTP      HTTPCall `json:"http"`
	}{op, call})
	if err != nil {
		return httpFrontHostResult{}, err
	}
	if len(payload) > httpClassifierInputLimit {
		return httpFrontHostResult{}, errors.New("HTTP front frame exceeds the selected ingress limit")
	}
	if err := frameproto.WriteRequest(conn, payload); err != nil {
		return httpFrontHostResult{}, &httpFrontCallError{possibleTransmission: true, cause: err}
	}
	var result httpFrontHostResult
	for {
		frame, err := frameproto.ReadFrameBounded(conn, httpFrontFrameLimit)
		if err != nil {
			return httpFrontHostResult{}, &httpFrontCallError{possibleTransmission: true, cause: err}
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			if len(result.stdout)+len(frame.Payload) > httpFrontFrameLimit {
				return httpFrontHostResult{}, &httpFrontCallError{possibleTransmission: true, cause: errors.New("HTTP host response exceeds the front frame limit")}
			}
			result.stdout = append(result.stdout, frame.Payload...)
		case frameproto.StreamStderr:
			if len(result.stderr)+len(frame.Payload) > httpFrontErrorBodySize {
				return httpFrontHostResult{}, &httpFrontCallError{possibleTransmission: true, cause: errors.New("HTTP host refusal exceeds the front error limit")}
			}
			result.stderr += string(frame.Payload)
		case frameproto.StreamExit:
			code, err := frameproto.ExitCode(frame.Payload)
			if err != nil {
				return httpFrontHostResult{}, &httpFrontCallError{possibleTransmission: true, cause: err}
			}
			result.exit = code
			return result, nil
		default:
			return httpFrontHostResult{}, &httpFrontCallError{possibleTransmission: true, cause: fmt.Errorf("HTTP host returned unsupported frame stream %d", frame.StreamID)}
		}
	}
}

func (f HTTPFront) writeRequestRefusal(w http.ResponseWriter, message, verdict, requestID string, authorization bool) {
	if verdict == "" {
		verdict = "refused"
	}
	if requestID == "" {
		requestID, _ = opaqueHTTPRequestID()
	}
	if requestID == "" {
		requestID = "unavailable"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Yolo-Verdict", verdict)
	w.Header().Set("Yolo-Request", requestID)
	if authorization {
		w.Header().Set("Yolo-Authorization-Stripped", "true")
	}
	w.WriteHeader(http.StatusForbidden)
	body, _ := json.Marshal(struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}{Errors: []struct {
		Message string `json:"message"`
	}{{Message: message}}})
	if len(body) > httpFrontErrorBodySize {
		body = []byte(`{"errors":[{"message":"HTTP call refused; inspect host state before retrying."}]}`)
	}
	_, _ = w.Write(body)
}

func frontVerdictMessage(v Verdict) string {
	parts := make([]string, 0, 2)
	if v.Rule != "" {
		parts = append(parts, v.Rule)
	}
	if v.Next != "" {
		parts = append(parts, v.Next)
	}
	if len(parts) == 0 {
		return "HTTP call refused; inspect host approval state before retrying."
	}
	return strings.Join(parts, ". ")
}

func opaqueHTTPRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", raw[:]), nil
}

func decodeFrontJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing host response JSON")
	}
	return nil
}

func frontHeaders(r *http.Request) ([]HTTPHeader, bool) {
	keys := make([]string, 0, len(r.Header)+1)
	for key := range r.Header {
		keys = append(keys, key)
	}
	if r.Host != "" {
		keys = append(keys, "Host")
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToLower(keys[i]) < strings.ToLower(keys[j]) })
	out := make([]HTTPHeader, 0, len(keys))
	authorization := false
	for _, key := range keys {
		var values []string
		if strings.EqualFold(key, "Host") {
			values = []string{r.Host}
		} else {
			values = slices.Clone(r.Header.Values(key))
		}
		if strings.EqualFold(key, "Authorization") {
			authorization = len(values) != 0
		}
		out = append(out, HTTPHeader{Name: key, Values: values})
	}
	return out, authorization
}

func copyFrontResponseHeaders(dst, src http.Header) {
	connectionTokens := make(map[string]bool)
	for _, value := range src.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			connectionTokens[strings.ToLower(strings.TrimSpace(token))] = true
		}
	}
	for key, values := range src {
		lower := strings.ToLower(key)
		if connectionTokens[lower] || lower == "content-length" || lower == "set-cookie" || lower == "authorization" || lower == "cookie" || strings.HasPrefix(lower, "yolo-") || strings.HasPrefix(lower, "proxy-") ||
			lower == "connection" || lower == "keep-alive" || lower == "proxy-connection" || lower == "transfer-encoding" || lower == "upgrade" || lower == "trailer" {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func boolLength(v bool) int {
	if v {
		return 1
	}
	return 0
}

func HTTPFrontMain(args []string) int {
	fs := flag.NewFlagSet("call-broker-http-front", flag.ContinueOnError)
	endpoint := fs.String("endpoint", "", "selected host call-broker endpoint file")
	listen := fs.String("listen", "", "loopback HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *endpoint == "" || *listen == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "call-broker-http-front: --endpoint and --listen are required by a selected HTTP pack")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := ServeHTTPFront(ctx, *endpoint, *listen); err != nil {
		fmt.Fprintln(os.Stderr, "call-broker-http-front: "+err.Error())
		return 1
	}
	return 0
}

// ServeHTTPFront runs the local HTTP listener until its context is canceled.
// The caller chooses the listener address from the selected launch; it must be
// loopback-only so no network peer can use the jail's private host service.
func ServeHTTPFront(ctx context.Context, endpoint, address string) error {
	if endpoint == "" || address == "" {
		return errors.New("HTTP front requires a selected host endpoint and loopback listen address")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("HTTP front listen address must be a literal loopback address")
	}
	listenerIP := net.ParseIP(host)
	if listenerIP == nil || !listenerIP.IsLoopback() {
		return errors.New("HTTP front listen address must be a literal loopback address")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("HTTP front cannot bind %s; free the loopback address and relaunch: %w", address, err)
	}
	server := newHTTPFrontServer(HTTPFront{Endpoint: endpoint})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(frontListener(listener))
	}()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		<-done
		return nil
	case <-done:
		if ctx.Err() != nil {
			return nil
		}
		return errors.New("HTTP front listener stopped unexpectedly; restart the selected pack")
	}
}
