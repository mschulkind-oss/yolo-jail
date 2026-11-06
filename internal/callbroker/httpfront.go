package callbroker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
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

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

const httpFrontUnauthenticatedMessage = "missing or invalid local front token; use the token this jail's launch published"

// HTTPFront is the jail-side, non-authoritative HTTP adapter for a selected
// HTTP call broker. Host authorization is asserted only by svcendpoint's TLS
// preamble; request headers, including Yolo-* and Authorization, carry no trust.
//
// A local caller authenticates with the sealed generation's token in exactly one
// designated field (Header, with Prefix before the token). The front holds only
// the token's SHA-256, so a front with no seal reply refuses every request.
type HTTPFront struct {
	Endpoint    string
	Timeout     time.Duration
	Generation  string
	TokenSHA256 []byte
	Header      string
	Prefix      string
}

// authenticate checks the one designated field before anything else about the
// request is read. It returns the presented token on success.
func (f HTTPFront) authenticate(r *http.Request) (string, bool) {
	if f.Header == "" || len(f.TokenSHA256) != sha256.Size {
		return "", false
	}
	values := r.Header.Values(f.Header)
	if len(values) != 1 || !strings.HasPrefix(values[0], f.Prefix) {
		return "", false
	}
	presented := strings.TrimPrefix(values[0], f.Prefix)
	sum := sha256.Sum256([]byte(presented))
	if subtle.ConstantTimeCompare(sum[:], f.TokenSHA256) != 1 {
		return "", false
	}
	return presented, true
}

// tokenOutsideField reports whether the authenticated token also appears in the
// request target, Host, or any header field other than the designated one.
func (f HTTPFront) tokenOutsideField(r *http.Request, token string) bool {
	places := []string{r.URL.EscapedPath(), r.URL.Path, lenientUnescape(r.URL.EscapedPath()), r.URL.RawQuery, r.Host}
	// Each query piece is decoded on its own, and leniently, so one malformed
	// escape anywhere in the query cannot hide an encoded token elsewhere.
	for _, pair := range strings.FieldsFunc(r.URL.RawQuery, func(c rune) bool { return c == '&' || c == ';' }) {
		for _, part := range strings.SplitN(pair, "=", 2) {
			places = append(places, lenientUnescape(part), lenientUnescape(strings.ReplaceAll(part, "+", " ")))
		}
	}
	for key, values := range r.Header {
		if strings.EqualFold(key, f.Header) {
			continue
		}
		places = append(places, key)
		places = append(places, values...)
	}
	for _, place := range places {
		if strings.Contains(place, token) {
			return true
		}
	}
	return false
}

// lenientUnescape decodes every well-formed %XX escape in s and keeps any
// malformed one literally, so a bad escape never stops the decoding of the rest.
func lenientUnescape(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if decoded, err := hex.DecodeString(s[i+1 : i+3]); err == nil {
				out = append(out, decoded[0])
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}

// ServeHTTP implements one-shot HTTP forwarding through the selected host
// endpoint. It does not retain request bodies or credentials between calls.
func (f HTTPFront) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Connection", "close")
	token, ok := f.authenticate(r)
	if !ok {
		f.writeRefusal(w, http.StatusUnauthorized, httpFrontUnauthenticatedMessage, "", "", false)
		return
	}
	headers, authorization := frontHeaders(r, f.Header)
	if f.tokenOutsideField(r, token) {
		f.writeRequestRefusal(w, errFrontTokenOutsideSlot.Error(), "", "", authorization)
		return
	}
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
	if bytes.Contains(body, []byte(token)) {
		f.writeRequestRefusal(w, errFrontTokenOutsideSlot.Error(), "", "", authorization)
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
	payload, err := json.Marshal(struct {
		Operation  string   `json:"op"`
		Generation string   `json:"front_generation"`
		HTTP       HTTPCall `json:"http"`
	}{op, f.Generation, call})
	if err != nil {
		return httpFrontHostResult{}, err
	}
	return f.exchange(ctx, payload)
}

// seal asks the host to bind this front's address and certificate to its
// generation, and returns the token hash and designated field the host chose.
func (f HTTPFront) seal(ctx context.Context, address string, der []byte) (httpFrontSealReply, error) {
	payload, err := json.Marshal(struct {
		Operation  string               `json:"op"`
		Generation string               `json:"front_generation"`
		Front      httpFrontSealRequest `json:"front"`
	}{httpFrontSealOp, f.Generation, httpFrontSealRequest{Address: address, CertificateBase64: base64.StdEncoding.EncodeToString(der)}})
	if err != nil {
		return httpFrontSealReply{}, err
	}
	result, err := f.exchange(ctx, payload)
	if err != nil {
		return httpFrontSealReply{}, err
	}
	if result.exit != 0 {
		message := strings.TrimSpace(result.stderr)
		if message == "" {
			message = fmt.Sprintf("the host refused with exit %d and no reason", result.exit)
		}
		return httpFrontSealReply{}, errors.New(message)
	}
	var reply httpFrontSealReply
	if err := decodeFrontJSON(result.stdout, &reply); err != nil {
		return httpFrontSealReply{}, fmt.Errorf("the host's seal reply is malformed (%v); update yolo on the host and relaunch", err)
	}
	sum, err := hex.DecodeString(reply.TokenSHA256)
	if err != nil || len(sum) != sha256.Size || reply.TokenSHA256 != strings.ToLower(reply.TokenSHA256) || !validHTTPFrontFieldName(reply.CredentialHeader) || strings.ContainsAny(reply.CredentialPrefix, "\r\n\x00") {
		return httpFrontSealReply{}, errors.New("the host's seal reply names no usable token hash or credential field; update yolo on the host and relaunch")
	}
	return reply, nil
}

func validHTTPFrontFieldName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c > 0x7e || c <= ' ' || strings.ContainsRune(`"(),/:;<=>?@[\]{}`, c) {
			return false
		}
	}
	return true
}

func (f HTTPFront) exchange(ctx context.Context, payload []byte) (httpFrontHostResult, error) {
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
	f.writeRefusal(w, http.StatusForbidden, message, verdict, requestID, authorization)
}

func (f HTTPFront) writeRefusal(w http.ResponseWriter, status int, message, verdict, requestID string, authorization bool) {
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
	w.WriteHeader(status)
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

// frontHeaders copies the request headers into the host frame. The designated
// local credential field never crosses. When that field is not Authorization, a
// caller Authorization is dropped too and reported as stripped.
func frontHeaders(r *http.Request, designated string) ([]HTTPHeader, bool) {
	keys := make([]string, 0, len(r.Header)+1)
	authorization := false
	for key := range r.Header {
		if strings.EqualFold(key, designated) {
			continue
		}
		if strings.EqualFold(key, "Authorization") {
			authorization = len(r.Header.Values(key)) != 0
			continue
		}
		keys = append(keys, key)
	}
	if r.Host != "" {
		keys = append(keys, "Host")
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToLower(keys[i]) < strings.ToLower(keys[j]) })
	out := make([]HTTPHeader, 0, len(keys))
	for _, key := range keys {
		var values []string
		if strings.EqualFold(key, "Host") {
			values = []string{r.Host}
		} else {
			values = slices.Clone(r.Header.Values(key))
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
	loophole := fs.String("loophole", "", "selected HTTP loophole this front serves")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *endpoint == "" || *loophole == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "call-broker-http-front: --endpoint and --loophole are required by a selected HTTP pack")
		return 2
	}
	generation, err := httpFrontGeneration(os.Getenv(paths.HTTPFrontGenerationsEnv), *loophole)
	if err != nil {
		reportHTTPFrontReadiness("failed", *loophole, err.Error())
		fmt.Fprintln(os.Stderr, "call-broker-http-front: "+err.Error())
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := ServeHTTPFront(ctx, *endpoint, *loophole, generation); err != nil {
		fmt.Fprintln(os.Stderr, "call-broker-http-front: "+err.Error())
		return 1
	}
	return 0
}

// httpFrontGeneration finds loophole's generation in the launch's
// `<loophole>=<generation>[,…]` list.
func httpFrontGeneration(list, loophole string) (string, error) {
	found, seen := "", false
	for _, entry := range strings.Split(list, ",") {
		name, generation, ok := strings.Cut(strings.TrimSpace(entry), "=")
		if !ok || name != loophole {
			continue
		}
		if seen {
			return "", fmt.Errorf("%s names loophole %q twice; relaunch the jail", paths.HTTPFrontGenerationsEnv, loophole)
		}
		found, seen = generation, true
	}
	if !callbrokerinput.ValidHTTPFrontGeneration(found) {
		return "", fmt.Errorf("%s carries no valid generation for loophole %q; relaunch the jail with the selected pack", paths.HTTPFrontGenerationsEnv, loophole)
	}
	return found, nil
}

// reportHTTPFrontReadiness writes one readiness line to the inherited
// descriptor, when the launch passed one: "ready http-front-<loophole>" or
// "failed http-front-<loophole> <reason>". The pattern is wirebridged's
// signalReadinessOnFD: the descriptor is duplicated so closing the write
// never closes the inherited one.
func reportHTTPFrontReadiness(kind, loophole, reason string) {
	raw := os.Getenv(paths.JailDaemonReadyFDEnv)
	if raw == "" {
		return
	}
	fd, err := strconv.Atoi(raw)
	if err != nil || fd < 3 {
		fmt.Fprintf(os.Stderr, "call-broker-http-front: %s=%q is not an inherited descriptor; no readiness answer can be sent\n", paths.JailDaemonReadyFDEnv, raw)
		return
	}
	message := kind + " http-front-" + loophole
	if reason != "" {
		message += " " + strings.Join(strings.Fields(reason), " ")
	}
	dup, err := syscall.Dup(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "call-broker-http-front: cannot duplicate readiness descriptor %d to answer %q: %v\n", fd, message, err)
		return
	}
	pipe := os.NewFile(uintptr(dup), "jail-daemon-ready")
	defer pipe.Close()
	if _, err := fmt.Fprintln(pipe, message); err != nil {
		fmt.Fprintf(os.Stderr, "call-broker-http-front: could not write readiness %q to fd %d: %v\n", message, fd, err)
	}
}

// mintHTTPFrontCertificate makes the front's in-memory P-256 certificate. Like
// svcendpoint's mintCert, the key never leaves this process and the certificate
// is its own trust anchor (IsCA for non-Go verifiers); the host pins its DER.
func mintHTTPFrontCertificate() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "yolo-http-front"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, der, nil
}

// ServeHTTPFront runs the selected loophole's local TLS front until its context
// is canceled. It binds its own loopback port, seals that address and its
// certificate to generation through the host, and only then serves and reports
// ready.
func ServeHTTPFront(ctx context.Context, endpoint, loophole, generation string) error {
	return serveHTTPFront(ctx, endpoint, loophole, generation, 0)
}

// httpFrontSealTimeout bounds the startup seal exchange. It is fixed and
// independent of the per-request timeout, which bounds only served requests.
const httpFrontSealTimeout = 30 * time.Second

func serveHTTPFront(ctx context.Context, endpoint, loophole, generation string, timeout time.Duration) (err error) {
	ready := false
	defer func() {
		// A front with no loophole has no readiness name to answer under.
		if err != nil && !ready && loophole != "" {
			reportHTTPFrontReadiness("failed", loophole, err.Error())
		}
	}()
	if endpoint == "" || loophole == "" || !callbrokerinput.ValidHTTPFrontGeneration(generation) {
		return errors.New("HTTP front requires a selected host endpoint, loophole and generation; relaunch the jail with the selected pack")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("HTTP front cannot bind a loopback port; relaunch the jail: %w", err)
	}
	certificate, der, err := mintHTTPFrontCertificate()
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("HTTP front cannot mint its certificate; relaunch the jail: %w", err)
	}
	front := HTTPFront{Endpoint: endpoint, Timeout: timeout, Generation: generation}
	if front.Timeout <= 0 {
		front.Timeout = 30 * time.Second
	}
	sealCtx, cancelSeal := context.WithTimeout(ctx, httpFrontSealTimeout)
	reply, err := front.seal(sealCtx, listener.Addr().String(), der)
	cancelSeal()
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("HTTP front could not seal its generation: %w", err)
	}
	front.TokenSHA256, _ = hex.DecodeString(reply.TokenSHA256)
	front.Header, front.Prefix = reply.CredentialHeader, reply.CredentialPrefix
	config := &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12}
	server := newHTTPFrontServer(front)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(frontListener(tls.NewListener(listener, config)))
	}()
	ready = true
	reportHTTPFrontReadiness("ready", loophole, "")
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
