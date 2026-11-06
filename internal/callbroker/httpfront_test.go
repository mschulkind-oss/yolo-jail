package callbroker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// testFrontLoophole is the selected loophole name every test front serves.
const testFrontLoophole = "tasks"

// testFrontGuard bounds a wait that only a regression would reach. It is a
// failure guard, never a pass condition, so host load cannot turn it red.
const testFrontGuard = time.Minute

// testHTTPFront is a running selected front as its published client would see
// it: the sealed loopback address, trust in exactly the sealed certificate, and
// the local token. Handled counts ordinary (non-seal) host frames; Frames holds
// the most recent ones.
type testHTTPFront struct {
	Address string
	TLS     *tls.Config
	Token   string
	Handled *atomic.Int32
	Frames  chan *jsonx.OrderedMap
}

func (f testHTTPFront) URL() string { return "https://" + f.Address }

func (f testHTTPFront) Client() *http.Client {
	return &http.Client{Transport: &http.Transport{TLSClientConfig: f.TLS.Clone(), DisableKeepAlives: true}}
}

func (f testHTTPFront) Bearer() string { return "Bearer " + f.Token }

// readinessPipe points the readiness variable at a fresh pipe and returns its
// read end. The write end is kept alive until cleanup.
func readinessPipe(t *testing.T) *bufio.Reader {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })
	t.Setenv(paths.JailDaemonReadyFDEnv, strconv.Itoa(int(w.Fd())))
	return bufio.NewReader(r)
}

func readReadinessLine(t *testing.T, ready *bufio.Reader) string {
	t.Helper()
	line := make(chan string, 1)
	go func() {
		text, _ := ready.ReadString('\n')
		line <- text
	}()
	select {
	case text := <-line:
		return text
	case <-time.After(testFrontGuard):
		t.Fatal("front wrote no readiness line")
		return ""
	}
}

// startTestHTTPFront runs the production front startup against endpoint and
// waits for its readiness line.
func startTestHTTPFront(t *testing.T, endpoint, generation string, timeout time.Duration) {
	t.Helper()
	ready := readinessPipe(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTPFront(ctx, endpoint, testFrontLoophole, generation, timeout) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(testFrontGuard):
			t.Error("HTTP front did not stop")
		}
	})
	if line := readReadinessLine(t, ready); line != "ready http-front-"+testFrontLoophole+"\n" {
		t.Fatalf("front readiness = %q, want exactly the ready line", line)
	}
}

func countingFrontHandler(inner hostservice.Handler) (hostservice.Handler, *atomic.Int32, chan *jsonx.OrderedMap) {
	handled := new(atomic.Int32)
	frames := make(chan *jsonx.OrderedMap, 16)
	return func(s *hostservice.Session) {
		if op, _ := s.Get("op"); op != httpFrontSealOp {
			handled.Add(1)
			select {
			case frames <- s.Request:
			default:
			}
		}
		inner(s)
	}, handled, frames
}

func trustOnly(t *testing.T, der []byte) *tls.Config {
	t.Helper()
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

// serveSelectedTestHTTPFront starts the front against a prepared selected
// handler and reads the address, certificate and token from the host's leaves.
func serveSelectedTestHTTPFront(t *testing.T, h *HTTPHandler, f *onceFixture, generation string, timeout time.Duration) testHTTPFront {
	t.Helper()
	handler, handled, frames := countingFrontHandler(h.Handle)
	_, endpoint := selectedHTTPHostFramesWithHandler(t, handler)
	startTestHTTPFront(t, endpoint, generation, timeout)
	raw, err := os.ReadFile(f.inputPath + ".prepared.seal")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := callbrokerinput.DecodePreparedHTTPFrontSeal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if host, _, err := net.SplitHostPort(sealed.Address); err != nil || host != "127.0.0.1" {
		t.Fatalf("front sealed a non-loopback address %q", sealed.Address)
	}
	token, err := os.ReadFile(f.inputPath + ".prepared.token")
	if err != nil {
		t.Fatal(err)
	}
	return testHTTPFront{Address: sealed.Address, TLS: trustOnly(t, sealed.CertificateDER), Token: string(token), Handled: handled, Frames: frames}
}

// serveStubSealTestHTTPFront starts the front against a host stub that seals
// any request with a token it mints, binding header and prefix as the local
// credential field; every other frame goes to inner.
func serveStubSealTestHTTPFront(t *testing.T, header, prefix string, timeout time.Duration, inner hostservice.Handler) testHTTPFront {
	t.Helper()
	token, err := svcendpoint.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(token))
	type sealedFront struct {
		address string
		der     []byte
	}
	sealedCh := make(chan sealedFront, 1)
	handler, handled, frames := countingFrontHandler(func(s *hostservice.Session) {
		if op, _ := s.Get("op"); op != httpFrontSealOp {
			inner(s)
			return
		}
		raw, _ := s.Get("front")
		encoded, _ := jsonx.DumpsCompact(raw)
		var req httpFrontSealRequest
		der, err := base64.StdEncoding.DecodeString(func() string { _ = json.Unmarshal([]byte(encoded), &req); return req.CertificateBase64 }())
		if err != nil {
			s.Stderr("stub seal could not decode the certificate\n")
			s.Exit(64)
			return
		}
		sealedCh <- sealedFront{address: req.Address, der: der}
		reply, _ := json.Marshal(httpFrontSealReply{TokenSHA256: hex.EncodeToString(sum[:]), CredentialHeader: header, CredentialPrefix: prefix})
		s.StdoutBytes(reply)
		s.Exit(0)
	})
	_, endpoint := selectedHTTPHostFramesWithHandler(t, handler)
	startTestHTTPFront(t, endpoint, strings.Repeat("c", 32), timeout)
	var sealed sealedFront
	select {
	case sealed = <-sealedCh:
	default:
		t.Fatal("front reported ready before the host sealed it")
	}
	return testHTTPFront{Address: sealed.address, TLS: trustOnly(t, sealed.der), Token: token, Handled: handled, Frames: frames}
}

func okFrontHost(dispatches *atomic.Int32) hostservice.Handler {
	return func(s *hostservice.Session) {
		if dispatches != nil {
			dispatches.Add(1)
		}
		s.Stdout(`{"status":200,"headers":{},"body_base64":""}`)
		s.Exit(0)
	}
}

func frameHTTPCall(t *testing.T, frame *jsonx.OrderedMap) HTTPCall {
	t.Helper()
	encoded, err := jsonx.DumpsCompact(frame)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		HTTP HTTPCall `json:"http"`
	}
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.HTTP
}

func lastFrame(t *testing.T, front testHTTPFront) *jsonx.OrderedMap {
	t.Helper()
	select {
	case frame := <-front.Frames:
		return frame
	default:
		t.Fatal("no host frame was captured")
		return nil
	}
}

func frameHasHeader(call HTTPCall, name string) bool {
	for _, header := range call.Headers {
		if strings.EqualFold(header.Name, name) {
			return true
		}
	}
	return false
}

type frontRefusal struct {
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func decodeFrontRefusal(t *testing.T, body io.Reader) frontRefusal {
	t.Helper()
	var refusal frontRefusal
	if err := json.NewDecoder(body).Decode(&refusal); err != nil {
		t.Fatal(err)
	}
	return refusal
}

func rawHTTPFrontRequest(t *testing.T, front testHTTPFront, request string) *http.Response {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: testFrontGuard}, "tcp", front.Address, front.TLS)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(testFrontGuard))
	if _, err := io.WriteString(conn, request); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	resp.Body = &frontResponseBody{ReadCloser: resp.Body, conn: conn}
	return resp
}

type frontResponseBody struct {
	io.ReadCloser
	conn net.Conn
}

func (b *frontResponseBody) Close() error {
	_ = b.conn.Close()
	return b.ReadCloser.Close()
}

func TestHTTPFrontRefusesMissingOrWrongTokenBeforeBody(t *testing.T) {
	var requests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	h, f, generation := selectedPreparedHTTPReadFixture(t, origin, executor)
	// A front that read the body first would wait out this timeout, far past the
	// request's failure guard.
	front := serveSelectedTestHTTPFront(t, h, f, generation, time.Hour)
	_, grantsBefore, err := f.store.List()
	if err != nil {
		t.Fatal(err)
	}
	for name, authorization := range map[string]string{
		"missing": "",
		"wrong":   "Authorization: Bearer " + strings.Repeat("0", 64) + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			// Content-Length promises ten bytes that never come.
			resp := rawHTTPFrontRequest(t, front, "POST /graphql HTTP/1.1\r\nHost: 127.0.0.1\r\n"+authorization+"Content-Length: 10\r\n\r\n")
			defer resp.Body.Close()
			refusal := decodeFrontRefusal(t, resp.Body)
			if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("Yolo-Verdict") != "refused" || len(refusal.Errors) != 1 ||
				!strings.Contains(refusal.Errors[0].Message, "missing or invalid local front token; use the token this jail's launch published") {
				t.Fatalf("unauthenticated request: status=%d headers=%v refusal=%+v", resp.StatusCode, resp.Header, refusal)
			}
		})
	}
	pending, grantsAfter, err := f.store.List()
	if err != nil || len(pending) != 0 || !reflect.DeepEqual(grantsBefore, grantsAfter) {
		t.Fatalf("unauthenticated request changed host state: pending=%+v grants=%+v err=%v", pending, grantsAfter, err)
	}
	if front.Handled.Load() != 0 || requests.Load() != 0 {
		t.Fatalf("unauthenticated request reached the host: handled=%d origin=%d", front.Handled.Load(), requests.Load())
	}
}

// askingHTTPWriteFront is a prepared, sealed write fixture whose classifier
// answers one write that asks.
func askingHTTPWriteFront(t *testing.T, original string) (testHTTPFront, *HTTPHandler, *onceFixture, *atomic.Int32) {
	t.Helper()
	var receiverWrites atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			receiverWrites.Add(1)
		}
		if r.URL.Path == "/items/a1" {
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
		}
	}))
	base, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
	askAnswer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"POST","path":"/graphql","query":"","headers":[],"body":{"mode":"original","present":true,"sha256":%q}},"writes":[{"cell":"change","chain":[{"type":"project","id":"team:alpha"}],"description":"rename task"}]}}`, digestHTTPBody(true, []byte(original)))
	classifier := fixtureHTTPClassifier(t, "cat <<'ANSWER'\n"+askAnswer+"\nANSWER")
	base.classifier = classifier
	base.classifier.Cells = append([]loopholedecl.CallBrokerCell(nil), base.input.Cells...)
	base.input.ProgramIdentities = classifier.Identities
	h, generation := preparedHTTPFixture(t, base, f)
	return serveSelectedTestHTTPFront(t, h, f, generation, 30*time.Second), h, f, &receiverWrites
}

func TestHTTPFrontValidTokenAskIs403(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	front, _, f, writes := askingHTTPWriteFront(t, original)
	_, grantsBefore, err := f.store.List()
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, front.URL()+"/graphql", strings.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", front.Bearer())
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, grantsAfter, err := f.store.List()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Yolo-Verdict") != "asks" || front.Handled.Load() != 1 || writes.Load() != 0 || !reflect.DeepEqual(grantsBefore, grantsAfter) {
		t.Fatalf("authenticated ask: status=%d verdict=%q handled=%d writes=%d grants %+v -> %+v", resp.StatusCode, resp.Header.Get("Yolo-Verdict"), front.Handled.Load(), writes.Load(), grantsBefore, grantsAfter)
	}
}

func TestPackHTTPFrontRefusalShape(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	front, h, _, writes := askingHTTPWriteFront(t, original)
	req, err := http.NewRequest(http.MethodPost, front.URL()+"/graphql", strings.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", front.Bearer())
	req.Header.Set("Yolo-Request", "forged-correlation")
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := decodeFrontRefusal(t, resp.Body)
	requestID := resp.Header.Get("Yolo-Request")
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Yolo-Verdict") != "asks" || requestID == "" || requestID == "forged-correlation" || len(body.Errors) != 1 || !strings.Contains(body.Errors[0].Message, "yolo approve ") {
		t.Fatalf("ask refusal missing the HTTP contract or actionable menu: status=%d headers=%v body=%+v", resp.StatusCode, resp.Header, body)
	}
	if resp.Header.Get("Yolo-Authorization-Stripped") != "" || writes.Load() != 0 {
		t.Fatalf("designated credential reported as stripped or ask caused a target write: stripped=%q writes=%d", resp.Header.Get("Yolo-Authorization-Stripped"), writes.Load())
	}
	pending, _, err := h.store.List()
	if err != nil || len(pending) != 1 || requestID != pending[0].ID {
		t.Fatalf("Yolo-Request is not the opaque host pending correlator: id=%q pending=%+v err=%v", requestID, pending, err)
	}
}

func TestHTTPFrontStripsTheLocalCredential(t *testing.T) {
	t.Run("designated Authorization never reaches the host frame", func(t *testing.T) {
		_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"id":"a1"}`))
		}))
		h, f, generation := selectedPreparedHTTPReadFixture(t, origin, executor)
		front := serveSelectedTestHTTPFront(t, h, f, generation, 30*time.Second)
		req, err := http.NewRequest(http.MethodGet, front.URL()+"/project", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", front.Bearer())
		resp, err := front.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		call := frameHTTPCall(t, lastFrame(t, front))
		if resp.StatusCode != http.StatusOK || frameHasHeader(call, "Authorization") || resp.Header.Get("Yolo-Authorization-Stripped") != "" {
			t.Fatalf("designated credential: status=%d frame headers=%+v stripped=%q", resp.StatusCode, call.Headers, resp.Header.Get("Yolo-Authorization-Stripped"))
		}
	})
	t.Run("another designated field strips a caller Authorization", func(t *testing.T) {
		front := serveStubSealTestHTTPFront(t, "X-Api-Key", "", 30*time.Second, okFrontHost(nil))
		req, err := http.NewRequest(http.MethodGet, front.URL()+"/project", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Api-Key", front.Token)
		req.Header.Set("Authorization", "Bearer caller-supplied")
		resp, err := front.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		call := frameHTTPCall(t, lastFrame(t, front))
		if resp.StatusCode != http.StatusOK || frameHasHeader(call, "X-Api-Key") || frameHasHeader(call, "Authorization") || resp.Header.Get("Yolo-Authorization-Stripped") != "true" {
			t.Fatalf("X-Api-Key credential: status=%d frame headers=%+v stripped=%q", resp.StatusCode, call.Headers, resp.Header.Get("Yolo-Authorization-Stripped"))
		}
	})
	t.Run("token outside its field is refused locally", func(t *testing.T) {
		front := serveStubSealTestHTTPFront(t, "Authorization", "Bearer ", 30*time.Second, okFrontHost(nil))
		for name, build := range map[string]func() *http.Request{
			"query": func() *http.Request {
				req, _ := http.NewRequest(http.MethodGet, front.URL()+"/project?copy="+front.Token, nil)
				return req
			},
			"path": func() *http.Request {
				req, _ := http.NewRequest(http.MethodGet, front.URL()+"/project/"+front.Token, nil)
				return req
			},
			"percent-encoded path": func() *http.Request {
				req, _ := http.NewRequest(http.MethodGet, front.URL()+"/project/"+percentEncodeAll(front.Token), nil)
				return req
			},
			"Host": func() *http.Request {
				req, _ := http.NewRequest(http.MethodGet, front.URL()+"/project", nil)
				req.Host = front.Token
				return req
			},
			"encoded query pair beside a malformed escape": func() *http.Request {
				req, _ := http.NewRequest(http.MethodGet, front.URL()+"/project?a=%zz&b="+percentEncodeAll(front.Token), nil)
				return req
			},
			"another header": func() *http.Request {
				req, _ := http.NewRequest(http.MethodGet, front.URL()+"/project", nil)
				req.Header.Set("X-Echo", front.Token)
				return req
			},
			"body": func() *http.Request {
				req, _ := http.NewRequest(http.MethodPost, front.URL()+"/graphql", strings.NewReader(`{"note":"`+front.Token+`"}`))
				return req
			},
		} {
			t.Run(name, func(t *testing.T) {
				before := front.Handled.Load()
				req := build()
				req.Header.Set("Authorization", front.Bearer())
				resp, err := front.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("outside its field")) || front.Handled.Load() != before {
					t.Fatalf("token outside its field: status=%d body=%q handled %d -> %d", resp.StatusCode, body, before, front.Handled.Load())
				}
			})
		}
	})
	t.Run("two designated fields are unauthenticated", func(t *testing.T) {
		front := serveStubSealTestHTTPFront(t, "Authorization", "Bearer ", 30*time.Second, okFrontHost(nil))
		resp := rawHTTPFrontRequest(t, front, "GET /project HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: "+front.Bearer()+"\r\nAuthorization: "+front.Bearer()+"\r\nConnection: close\r\n\r\n")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || front.Handled.Load() != 0 {
			t.Fatalf("duplicate designated field: status=%d handled=%d", resp.StatusCode, front.Handled.Load())
		}
	})
}

func percentEncodeAll(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, "%%%02X", s[i])
	}
	return b.String()
}

func TestHTTPFrontGenerationFromTheLaunchList(t *testing.T) {
	gen := strings.Repeat("c", 32)
	for list, wantErr := range map[string]bool{
		"tasks=" + gen:                         false,
		"other=x, tasks=" + gen:                false,
		"":                                     true,
		"other=" + gen:                         true,
		"tasks=not-a-generation":               true,
		"tasks=,tasks=" + gen:                  true,
		"tasks=" + gen + ",tasks=":             true,
		"tasks=" + gen + ",tasks=" + gen:       true,
		"tasks=" + gen + ",tasks=" + gen + "d": true,
	} {
		got, err := httpFrontGeneration(list, testFrontLoophole)
		if (err != nil) != wantErr || (!wantErr && got != gen) {
			t.Errorf("httpFrontGeneration(%q) = %q, %v; want error=%v", list, got, err, wantErr)
		}
	}
}

func TestHTTPFrontRefusesAMalformedSealReply(t *testing.T) {
	good := strings.Repeat("ab", 32)
	for name, stdout := range map[string]string{
		"empty":           ``,
		"uppercase hash":  `{"token_sha256":"` + strings.ToUpper(good) + `","credential_header":"Authorization","credential_prefix":"Bearer "}`,
		"short hash":      `{"token_sha256":"` + good[:62] + `","credential_header":"Authorization","credential_prefix":"Bearer "}`,
		"non-hex hash":    `{"token_sha256":"` + strings.Repeat("zz", 32) + `","credential_header":"Authorization","credential_prefix":"Bearer "}`,
		"empty header":    `{"token_sha256":"` + good + `","credential_header":"","credential_prefix":"Bearer "}`,
		"bad header name": `{"token_sha256":"` + good + `","credential_header":"X Api","credential_prefix":""}`,
		"CRLF in prefix":  `{"token_sha256":"` + good + `","credential_header":"Authorization","credential_prefix":"Bearer\r\n"}`,
		"unknown key":     `{"token_sha256":"` + good + `","credential_header":"Authorization","credential_prefix":"Bearer ","extra":"x"}`,
		"trailing JSON":   `{"token_sha256":"` + good + `","credential_header":"Authorization","credential_prefix":"Bearer "}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, endpoint := selectedHTTPHostFramesWithHandler(t, func(s *hostservice.Session) {
				s.Stdout(stdout)
				s.Exit(0)
			})
			ready := readinessPipe(t)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- serveHTTPFront(ctx, endpoint, testFrontLoophole, strings.Repeat("c", 32), 0) }()
			// The readiness line is the event: a front that accepted the reply
			// writes ready and serves until canceled.
			line := readReadinessLine(t, ready)
			cancel()
			err := <-done
			if !strings.HasPrefix(line, "failed http-front-"+testFrontLoophole+" ") {
				t.Fatalf("malformed seal reply readiness = %q", line)
			}
			if err == nil || !strings.Contains(err.Error(), "HTTP front could not seal its generation: ") {
				t.Fatalf("front accepted a malformed seal reply: %v", err)
			}
		})
	}
}

func TestHTTPFrontRefusesPlainHTTPAndOtherTrust(t *testing.T) {
	front := serveStubSealTestHTTPFront(t, "Authorization", "Bearer ", 30*time.Second, okFrontHost(nil))
	// A plain-HTTP request carrying the valid token: a front that served plain
	// HTTP would answer it (200 from this host stub, or any HTTP status line).
	// A TLS front answers no HTTP at all: at most a TLS alert record, then close.
	conn, err := net.DialTimeout("tcp", front.Address, testFrontGuard)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(testFrontGuard))
	if _, err := io.WriteString(conn, "GET /project HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: "+front.Bearer()+"\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	answer, err := io.ReadAll(conn)
	_ = conn.Close()
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatal("front neither answered nor closed a plain-HTTP connection")
	}
	if bytes.HasPrefix(answer, []byte("HTTP/")) || (len(answer) != 0 && answer[0] != 0x15) {
		t.Fatalf("plain HTTP got a non-TLS answer: %q", answer)
	}
	other := &http.Client{Timeout: testFrontGuard, Transport: &http.Transport{TLSClientConfig: trustOnly(t, frontFixtureCertificate(t, "127.0.0.1", true))}}
	_, err = other.Get(front.URL() + "/project")
	var verify *tls.CertificateVerificationError
	if !errors.As(err, &verify) {
		t.Fatalf("a client trusting another certificate did not fail verification: %v", err)
	}
	if front.Handled.Load() != 0 {
		t.Fatalf("an untrusted connection reached the host: handled=%d", front.Handled.Load())
	}
	source, err := os.ReadFile("httpfront.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(source, []byte("InsecureSkipVerify")) {
		t.Fatal("httpfront.go names InsecureSkipVerify")
	}
}

func TestHTTPFrontSealsBeforeReady(t *testing.T) {
	t.Run("ready only after the seal reply", func(t *testing.T) {
		// serveStubSealTestHTTPFront asserts the exact ready line and that the
		// host had already sealed when it arrived.
		front := serveStubSealTestHTTPFront(t, "Authorization", "Bearer ", 30*time.Second, okFrontHost(nil))
		if front.Address == "" {
			t.Fatal("front sealed no address")
		}
	})
	t.Run("a refused seal reports failed and serves nothing", func(t *testing.T) {
		var sealAttempts, ordinary atomic.Int32
		_, endpoint := selectedHTTPHostFramesWithHandler(t, func(s *hostservice.Session) {
			if op, _ := s.Get("op"); op != httpFrontSealOp {
				ordinary.Add(1)
				s.Exit(64)
				return
			}
			sealAttempts.Add(1)
			s.Stderr("HTTP front seal refused: fixture refusal\n")
			s.Exit(64)
		})
		ready := readinessPipe(t)
		t.Setenv(paths.HTTPFrontGenerationsEnv, testFrontLoophole+"="+strings.Repeat("c", 32))
		if rc := HTTPFrontMain([]string{"--endpoint", endpoint, "--loophole", testFrontLoophole}); rc == 0 {
			t.Fatal("front exited zero after a refused seal")
		}
		line := readReadinessLine(t, ready)
		if !strings.HasPrefix(line, "failed http-front-"+testFrontLoophole+" ") || !strings.Contains(line, "HTTP front could not seal its generation: ") ||
			!strings.Contains(line, "fixture refusal") || strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") {
			t.Fatalf("refused seal readiness = %q", line)
		}
		if sealAttempts.Load() != 1 || ordinary.Load() != 0 {
			t.Fatalf("seal attempts=%d ordinary frames=%d", sealAttempts.Load(), ordinary.Load())
		}
	})
}

func TestHTTPFrontRefusesInvalidHostAuthenticationBeforeServing(t *testing.T) {
	var originRequests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
	}))
	h, f, generation := selectedPreparedHTTPReadFixture(t, origin, executor)
	_, endpoint := selectedHTTPHostFrames(t, h)
	published, err := svcendpoint.Read(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	published.Token = strings.Repeat("0", len(published.Token))
	if err := os.WriteFile(endpoint, []byte(published.Format()), 0600); err != nil {
		t.Fatal(err)
	}
	ready := readinessPipe(t)
	t.Setenv(paths.HTTPFrontGenerationsEnv, testFrontLoophole+"="+generation)
	if rc := HTTPFrontMain([]string{"--endpoint", endpoint, "--loophole", testFrontLoophole}); rc == 0 {
		t.Fatal("front served after failing host authentication")
	}
	if line := readReadinessLine(t, ready); !strings.HasPrefix(line, "failed http-front-"+testFrontLoophole+" HTTP front could not seal its generation: ") {
		t.Fatalf("failed host authentication readiness = %q", line)
	}
	for _, leaf := range []string{".prepared.seal", ".prepared.token"} {
		if _, err := os.Stat(f.inputPath + leaf); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unauthenticated front left %s: %v", leaf, err)
		}
	}
	if originRequests.Load() != 0 {
		t.Fatalf("origin requests=%d", originRequests.Load())
	}
}

func TestHTTPFrontRequiresItsSelectedIdentity(t *testing.T) {
	t.Setenv(paths.JailDaemonReadyFDEnv, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, args := range map[string][3]string{
		"no endpoint":        {"", testFrontLoophole, strings.Repeat("c", 32)},
		"no loophole":        {"/selected/host.endpoint", "", strings.Repeat("c", 32)},
		"invalid generation": {"/selected/host.endpoint", testFrontLoophole, "not-a-generation"},
	} {
		if err := ServeHTTPFront(ctx, args[0], args[1], args[2]); err == nil {
			t.Fatalf("%s: front started", name)
		}
	}
	t.Setenv(paths.HTTPFrontGenerationsEnv, "other="+strings.Repeat("c", 32))
	ready := readinessPipe(t)
	if rc := HTTPFrontMain([]string{"--endpoint", "/selected/host.endpoint", "--loophole", testFrontLoophole}); rc != 1 {
		t.Fatalf("front without its own generation rc=%d, want 1", rc)
	}
	if line := readReadinessLine(t, ready); !strings.HasPrefix(line, "failed http-front-"+testFrontLoophole+" ") || !strings.Contains(line, paths.HTTPFrontGenerationsEnv) {
		t.Fatalf("front without its own generation readiness = %q", line)
	}
	// A direct caller with a loophole but an invalid generation still answers.
	if err := ServeHTTPFront(ctx, "/selected/host.endpoint", testFrontLoophole, "bad"); err == nil {
		t.Fatal("front started with an invalid generation")
	}
	if line := readReadinessLine(t, ready); !strings.HasPrefix(line, "failed http-front-"+testFrontLoophole+" ") {
		t.Fatalf("direct invalid-generation readiness = %q", line)
	}
}

func TestSelectedPackHTTPFrontBindsHostIdentity(t *testing.T) {
	var originRequests atomic.Int32
	var originAuthorization atomic.Value
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		originAuthorization.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"id":"a1"}`))
	}))
	h, f, generation := selectedPreparedHTTPReadFixture(t, origin, executor)
	front := serveSelectedTestHTTPFront(t, h, f, generation, 30*time.Second)

	req, err := http.NewRequest(http.MethodGet, front.URL()+"/project?view=compact", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", front.Bearer())
	req.Header.Set("Yolo-Verdict", "runs")
	req.Header.Set("Yolo-Request", "forged-correlation")
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var reply map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || originRequests.Load() != 1 {
		t.Fatalf("selected request did not reach the counted receiver once: status=%d requests=%d body=%v", resp.StatusCode, originRequests.Load(), reply)
	}
	if resp.Header.Get("Yolo-Verdict") != "" || resp.Header.Get("Yolo-Request") != "" {
		t.Fatalf("client-controlled Yolo-* authority escaped into the response: verdict=%q request=%q", resp.Header.Get("Yolo-Verdict"), resp.Header.Get("Yolo-Request"))
	}
	if got := originAuthorization.Load(); got != "Bearer synthetic-only-host-value" {
		t.Fatalf("remote Authorization = %v; the local token crossed instead of the host credential", got)
	}
}

func TestHTTPFrontReframesRedactedHostBody(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
	}{
		{name: "shrinks", token: "synthetic-only-host-value"},
		{name: "expands", token: "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const prefix, suffix = "before-", "-after"
			want := []byte(prefix + "[redacted]" + suffix)
			_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, prefix+tc.token+suffix)
			}))
			h, f, generation := selectedPreparedHTTPReadFixture(t, origin, executor)
			t.Setenv("SYNTHETIC_HOST_TOKEN", tc.token)
			front := serveSelectedTestHTTPFront(t, h, f, generation, 30*time.Second)

			req, err := http.NewRequest(http.MethodGet, front.URL()+"/project", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", front.Bearer())
			resp, err := front.Client().Do(req)
			if err != nil {
				t.Fatalf("front response failed framing after host redaction: %v", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != http.StatusOK || !bytes.Equal(body, want) {
				t.Fatalf("redacted response framing: status=%d body=%q want=%q readErr=%v content-length=%q", resp.StatusCode, body, want, err, resp.Header.Get("Content-Length"))
			}
			if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(want)) {
				t.Fatalf("local Content-Length=%q, want actual redacted body length %d", got, len(want))
			}
		})
	}
}

func TestHTTPFrontKeepsHeadAndBodylessStatusResponsesBodyless(t *testing.T) {
	var status atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := int(status.Load()); code != 0 {
			w.WriteHeader(code)
			return
		}
		_, _ = io.WriteString(w, "representation")
	}))
	h, f, generation := selectedPreparedHTTPReadFixture(t, origin, executor)
	front := serveSelectedTestHTTPFront(t, h, f, generation, 30*time.Second)
	for _, tc := range []struct {
		name   string
		method string
		status int
	}{
		{name: "head", method: http.MethodHead, status: http.StatusOK},
		{name: "no-content", method: http.MethodGet, status: http.StatusNoContent},
		{name: "not-modified", method: http.MethodGet, status: http.StatusNotModified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status.Store(int32(tc.status))
			req, err := http.NewRequest(tc.method, front.URL()+"/project", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", front.Bearer())
			resp, err := front.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != tc.status || len(body) != 0 || resp.Header.Get("Content-Length") != "" {
				t.Fatalf("bodyless response: status=%d body=%q content-length=%q err=%v", resp.StatusCode, body, resp.Header.Get("Content-Length"), err)
			}
		})
	}
}

func TestHTTPFrontWarnsWhenPostTransmissionOutcomeIsUncertain(t *testing.T) {
	checkWarning := func(t *testing.T, status int, body io.Reader, dispatches int32) {
		t.Helper()
		refusal := decodeFrontRefusal(t, body)
		message := ""
		if len(refusal.Errors) == 1 {
			message = strings.ToLower(refusal.Errors[0].Message)
		}
		if status != http.StatusForbidden || len(refusal.Errors) != 1 || !strings.Contains(message, "may have transmitted") || !strings.Contains(message, "inspect remote state and approval before resubmitting") || strings.Contains(message, "repair its launch or approval and retry") {
			t.Fatalf("post-transmission warning lost or became retry advice: status=%d refusal=%+v", status, refusal)
		}
		if dispatches != 1 {
			t.Fatalf("host dispatches=%d, want exactly one before warning", dispatches)
		}
	}
	get := func(t *testing.T, front testHTTPFront) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, front.URL()+"/project", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", front.Bearer())
		resp, err := front.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	t.Run("host reports post-send receiver failure", func(t *testing.T) {
		var originRequests atomic.Int32
		_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originRequests.Add(1)
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "short")
		}))
		h, f, generation := selectedPreparedHTTPReadFixture(t, origin, executor)
		front := serveSelectedTestHTTPFront(t, h, f, generation, 30*time.Second)
		resp := get(t, front)
		defer resp.Body.Close()
		checkWarning(t, resp.StatusCode, resp.Body, front.Handled.Load())
		if originRequests.Load() != 1 {
			t.Fatalf("origin requests=%d, want one possibly-transmitted request", originRequests.Load())
		}
	})

	t.Run("front transport drops after host receives request", func(t *testing.T) {
		front := serveStubSealTestHTTPFront(t, "Authorization", "Bearer ", 30*time.Second, func(s *hostservice.Session) {
			_ = s.Conn().Close()
		})
		resp := get(t, front)
		defer resp.Body.Close()
		checkWarning(t, resp.StatusCode, resp.Body, front.Handled.Load())
	})
}

func TestHTTPFrontRejectsHeaderCaptureOverflowBeforeDispatch(t *testing.T) {
	front := serveStubSealTestHTTPFront(t, "Authorization", "Bearer ", 30*time.Second, okFrontHost(nil))
	prefix := "POST /graphql HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: " + front.Bearer() + "\r\nX-Padding: "
	suffix := "\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
	padding := 65627 - len(prefix) - len(suffix)
	if padding < 0 {
		t.Fatal("padded request fixture underflow")
	}
	request := prefix + strings.Repeat(" ", padding) + suffix
	if len(request) != 65627 {
		t.Fatalf("padded request size=%d, want 65627", len(request))
	}
	resp := rawHTTPFrontRequest(t, front, request)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || front.Handled.Load() != 0 {
		t.Fatalf("header capture overflow reached host: status=%d dispatches=%d", resp.StatusCode, front.Handled.Load())
	}
}

// The body read is bounded by the front's request timeout: a body that never
// arrives is refused before any host dispatch. The test is event-driven: it
// starts sending only after the front's readiness line (the seal is done by
// then, under its own fixed deadline), and passes on the refusal response
// itself. Whenever the request deadline passes, before or during the body read,
// the outcome is the same 403, so machine speed cannot change it; the guard on
// the raw connection only turns a hang into a failure.
func TestHTTPFrontBoundsBodyReadByRequestTimeout(t *testing.T) {
	front := serveStubSealTestHTTPFront(t, "Authorization", "Bearer ", 300*time.Millisecond, okFrontHost(nil))
	resp := rawHTTPFrontRequest(t, front, "POST /graphql HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: "+front.Bearer()+"\r\nContent-Length: 1\r\nConnection: close\r\n\r\n")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || front.Handled.Load() != 0 {
		t.Fatalf("stalled body was not bounded before host dispatch: status=%d dispatches=%d", resp.StatusCode, front.Handled.Load())
	}
}

func TestHTTPFrontPreservesEmptyBodyPresence(t *testing.T) {
	observed := make(chan bool, 2)
	front := serveStubSealTestHTTPFront(t, "Authorization", "Bearer ", 30*time.Second, func(s *hostservice.Session) {
		value, ok := s.Get("http")
		if !ok {
			s.Exit(64)
			return
		}
		call, ok := value.(*jsonx.OrderedMap)
		if !ok {
			s.Exit(64)
			return
		}
		value, ok = call.Get("body_present")
		if !ok {
			s.Exit(64)
			return
		}
		present, _ := value.(bool)
		observed <- present
		s.Stdout(`{"status":200,"headers":{},"body_base64":""}`)
		s.Exit(0)
	})
	send := func(request string) {
		t.Helper()
		resp := rawHTTPFrontRequest(t, front, request)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("front response status=%d", resp.StatusCode)
		}
	}
	auth := "Authorization: " + front.Bearer() + "\r\n"
	send("POST /graphql HTTP/1.1\r\nHost: 127.0.0.1\r\n" + auth + "Connection: close\r\n\r\n")
	send("POST /graphql HTTP/1.1\r\nHost: 127.0.0.1\r\n" + auth + "Content-Length: 0\r\nConnection: close\r\n\r\n")
	if absent, explicitEmpty := <-observed, <-observed; absent || !explicitEmpty {
		t.Fatalf("front lost empty HTTP request body presence: absent=%v explicit_empty=%v", absent, explicitEmpty)
	}
}

func TestHTTPFrontRejectsOversizeBodyBeforeHostDispatch(t *testing.T) {
	var originRequests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
	}))
	h, f, generation := selectedPreparedHTTPReadFixture(t, origin, executor)
	front := serveSelectedTestHTTPFront(t, h, f, generation, 30*time.Second)

	// The declared length alone refuses: no body bytes are sent, so the client
	// never races the front's close while writing 3 MiB.
	resp := rawHTTPFrontRequest(t, front, "POST /graphql HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: "+front.Bearer()+"\r\nContent-Length: "+strconv.Itoa(httpFrontBodyLimit+1)+"\r\n\r\n")
	defer resp.Body.Close()
	refusal := decodeFrontRefusal(t, resp.Body)
	if resp.StatusCode != http.StatusForbidden || originRequests.Load() != 0 || front.Handled.Load() != 0 || len(refusal.Errors) != 1 || !strings.Contains(refusal.Errors[0].Message, "3 MiB") {
		t.Fatalf("oversize body reached dispatch: status=%d origin=%d handled=%d body=%+v", resp.StatusCode, originRequests.Load(), front.Handled.Load(), refusal)
	}
}
