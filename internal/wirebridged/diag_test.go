package wirebridged

// diag_test.go pins the diagnostics as BEHAVIOUR, not as sentences.
//
// The rule this file is written to (AGENTS.md): a test that asserts the string a
// comment promises is not a test. So every case here drives a PRODUCTION entry
// point — serve(), ServeHTTP(), resolveRoute(), signalNotReady() — and asserts on
// the bytes the daemon actually wrote to its sink. Delete the logf at any call
// site below and the matching test goes red; there is no assertion here that a
// deleted call site could still satisfy.
//
// The sink is the package's diagSink (diag.go), which in a jail is os.Stderr and
// therefore ~/.local/state/yolo-jail-daemons/wire-bridge.log by the supervisor's
// redirection. captureDiag swaps it for a buffer and resets the logOnce ledger,
// which is process-global and would otherwise make these tests order-dependent.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridge"
)

// captureDiag redirects the package sink to a buffer for the duration of one
// test and returns a reader for it. The logOnce ledger is reset with it: it is
// process-global by design (a 200ms poll loop must not repeat itself for a jail's
// whole lifetime), which across tests would mean the second test to provoke a
// message sees nothing.
func captureDiag(t *testing.T) func() string {
	t.Helper()
	buf := &bytes.Buffer{}
	diagMu.Lock()
	prevSink, prevSeen := diagSink, diagSeen
	diagSink, diagSeen = buf, map[string]bool{}
	diagMu.Unlock()
	t.Cleanup(func() {
		diagMu.Lock()
		diagSink, diagSeen = prevSink, prevSeen
		diagMu.Unlock()
	})
	return func() string {
		diagMu.Lock()
		defer diagMu.Unlock()
		return buf.String()
	}
}

// wantLines asserts every fragment appears in the captured log, reporting the
// whole body on failure — a diagnostic test whose own failure is undiagnosable
// would be a poor advertisement for the change.
func wantLines(t *testing.T, got string, fragments ...string) {
	t.Helper()
	for _, want := range fragments {
		if !strings.Contains(got, want) {
			t.Errorf("the daemon log does not contain %q. Full log:\n%s", want, got)
		}
	}
}

// noReadinessPipe makes the readiness descriptor definitively absent. The tests
// below call production paths that answer the boot when it is present, and the
// process running `go test` may legitimately have inherited one.
func noReadinessPipe(t *testing.T) {
	t.Helper()
	t.Setenv(paths.JailDaemonReadyFDEnv, "")
}

// THE HEADLINE CASE: the 127.0.0.1:8214 collision, reproduced at a kernel-chosen
// port. A bind failure must name the address it tried, the syscall error
// verbatim, AND what holds the port — the third fact being the one that existed
// in no log on either side and cost four wrong hypotheses.
func TestServeReportsTheAddressTheErrnoAndThePortHolderOnABindConflict(t *testing.T) {
	noReadinessPipe(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	addr := held.Addr().String()

	logged := captureDiag(t)
	rc := serve(context.Background(), route{
		ProviderName:    "cerebras",
		ListenAddr:      addr,
		UpstreamBaseURL: "https://upstream.example/v1",
	}, entrypoint.NewEnv(map[string]string{}))
	if rc != 1 {
		t.Errorf("a bind conflict is a boot FAILURE, not an idle: rc = %d, want 1", rc)
	}
	got := logged()
	wantLines(t, got,
		addr,                     // the address it tried
		"cerebras",               // whose base_url named it
		"address already in use", // the syscall error, verbatim
	)
	if _, err := os.Stat("/proc/net/tcp"); err == nil {
		// The holder is THIS process, and naming it is the whole point.
		wantLines(t, got, "held by pid "+strconv.Itoa(os.Getpid()))
	} else if !strings.Contains(got, "could not be identified") {
		t.Errorf("without /proc the log must say it could not identify the holder, not stay "+
			"silent about it. Full log:\n%s", got)
	}
}

// The bind conflict also has to reach the BOOT, which is waiting on a pipe rather
// than reading the log: the entrypoint's refusal message is composed from this
// detail, and a boot that refuses without naming the holder sends its reader back
// to the same empty `ss` output.
func TestBindConflictDetailReachesTheReadinessPipeOnOneLine(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	t.Setenv(paths.JailDaemonReadyFDEnv, strconv.Itoa(int(write.Fd())))
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	addr := held.Addr().String()

	captureDiag(t)
	if rc := serve(context.Background(), route{ProviderName: "cerebras", ListenAddr: addr,
		UpstreamBaseURL: "https://upstream.example/v1"}, entrypoint.NewEnv(map[string]string{})); rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	buf := make([]byte, 4096)
	n, err := read.Read(buf)
	if err != nil {
		t.Fatalf("nothing reached the readiness pipe: %v", err)
	}
	line := string(buf[:n])
	if !strings.HasPrefix(line, "failed "+ServiceName+" ") {
		t.Errorf("readiness record = %q, want a `failed <service> <detail>` record", line)
	}
	if !strings.Contains(line, addr) {
		t.Errorf("the readiness detail does not name the address: %q", line)
	}
	// ONE LINE, or the entrypoint's line-framed scanner reads the rest of the
	// detail as a second readiness record and reports "unexpected readiness"
	// instead of the bind conflict.
	if strings.Count(strings.TrimSuffix(line, "\n"), "\n") != 0 {
		t.Errorf("the readiness record spans more than one line, which breaks the protocol: %q", line)
	}
}

// A publish failure must name the file it could not write AND the live address the
// unpublished listener is on, and it must free the port on the way out — the
// listener is closed explicitly for exactly this reason.
func TestPublishFailureNamesTheFileAndReleasesTheListener(t *testing.T) {
	noReadinessPipe(t)
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}

	// A regular file where the endpoint directory should be: MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := EndpointFile
	EndpointFile = filepath.Join(blocker, "wire-bridge.endpoint")
	t.Cleanup(func() { EndpointFile = old })

	logged := captureDiag(t)
	if rc := serve(context.Background(), route{ProviderName: "cerebras", ListenAddr: addr,
		UpstreamBaseURL: "https://upstream.example/v1"}, entrypoint.NewEnv(map[string]string{})); rc != 1 {
		t.Errorf("an unpublishable endpoint is a boot failure: rc = %d, want 1", rc)
	}
	wantLines(t, logged(), "cannot publish", EndpointFile, addr)

	reclaimed, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the listener survived a failed publish, so the port is still held: %v", err)
	}
	_ = reclaimed.Close()
}

// The serving line is the positive disclosure: what was bound, what is upstream,
// and WHERE the credential came from (never the credential).
func TestServingLineNamesTheRouteAndTheCredentialSource(t *testing.T) {
	noReadinessPipe(t)
	home := t.TempDir()
	t.Setenv("WIREBRIDGE_DIAG_KEY", "secret-value")
	endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
	old := EndpointFile
	EndpointFile = endpointFile
	t.Cleanup(func() { EndpointFile = old })

	ctx, cancel := context.WithCancel(context.Background())
	logged := captureDiag(t)
	done := make(chan int, 1)
	go func() {
		done <- serve(ctx, route{ProviderName: "cerebras", ListenAddr: "127.0.0.1:0",
			UpstreamBaseURL: "https://upstream.example/v1", KeyEnvName: "WIREBRIDGE_DIAG_KEY"},
			entrypoint.NewEnv(map[string]string{"JAIL_HOME": home}))
	}()
	waitFor(t, func() bool { return strings.Contains(logged(), "serving provider") })
	cancel()
	if rc := <-done; rc != 0 {
		t.Errorf("a cancelled serve is not a failure: rc = %d", rc)
	}
	got := logged()
	wantLines(t, got, "binding 127.0.0.1:0", "serving provider \"cerebras\"",
		"https://upstream.example/v1", "$WIREBRIDGE_DIAG_KEY from process environment",
		"stopping:")
	if strings.Contains(got, "secret-value") {
		t.Fatalf("THE CREDENTIAL REACHED THE LOG (§5's forbidden list):\n%s", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

// A truncated upstream stream: no finish_reason, so the translator never emits
// message_stop, so nothing closes the anthropic grammar. The client used to be
// left waiting on it with a 200 in the log and no other record anywhere.
func TestTruncatedUpstreamStreamIsReportedToBothTheClientAndTheLog(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "text/event-stream", body: strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"He"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"content":"llo"},"finish_reason":null}]}`,
		"",
	}, "\n")}
	upstream := httptest.NewServer(up.handler())
	defer upstream.Close()
	srv := httptest.NewServer(NewHandler(upstream.URL, "k"))
	defer srv.Close()

	logged := captureDiag(t)
	resp, err := http.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"m","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	// The client is told, the only way SSE allows.
	if !strings.Contains(string(body), "event: error") {
		t.Errorf("a truncated stream must end in an anthropic error event, or the client waits "+
			"forever on a message_stop:\n%s", body)
	}
	if !strings.Contains(string(body), "before it completed") {
		t.Errorf("the error event does not say what went wrong:\n%s", body)
	}
	// And so is the log.
	wantLines(t, logged(), "without a finish_reason", "no message_stop", upstream.URL)
}

// The OTHER silent exit from the scan loop: bufio.Scanner's own error, which was
// never read. An upstream that dies mid-body ends the loop exactly like a clean
// finish, and the request line said 200.
func TestAnUpstreamThatDiesMidStreamIsReported(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		// A chunked response with one complete chunk, then the connection dies
		// without the terminating chunk: the client's reader sees an unexpected
		// EOF, which is what sc.Err() now carries.
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n" +
			"Transfer-Encoding: chunked\r\n\r\n")
		chunk := `data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"He"},"finish_reason":null}]}` + "\n\n"
		fmt.Fprintf(buf, "%x\r\n%s\r\n", len(chunk), chunk)
		_ = buf.Flush()
		_ = conn.Close()
	}))
	defer upstream.Close()
	srv := httptest.NewServer(NewHandler(upstream.URL, "k"))
	defer srv.Close()

	logged := captureDiag(t)
	resp, err := http.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"m","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := logged()
	// Either arm is a correct report of a stream that did not finish; what is not
	// acceptable is silence, which is what shipped.
	if !strings.Contains(got, "ended in an error") && !strings.Contains(got, "without a finish_reason") {
		t.Errorf("a stream cut off mid-body was not reported at all. Full log:\n%s", got)
	}
	if !strings.Contains(string(body), "event: error") {
		t.Errorf("the client was left without an error event:\n%s", body)
	}
}

// The complete stream must stay silent about truncation, or the report above is
// noise on every successful request rather than a signal.
func TestACompleteStreamReportsNoTruncation(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "text/event-stream", body: strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")}
	upstream := httptest.NewServer(up.handler())
	defer upstream.Close()
	srv := httptest.NewServer(NewHandler(upstream.URL, "k"))
	defer srv.Close()

	logged := captureDiag(t)
	resp, err := http.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"m","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "event: error") {
		t.Fatalf("a complete stream must carry no error event:\n%s", body)
	}
	if got := logged(); strings.Contains(got, "without a finish_reason") {
		t.Errorf("a complete stream reported a truncation:\n%s", got)
	}
}

// failingWriter is a client that cannot receive the response — a hung-up claude,
// or a broken loopback pipe.
type failingWriter struct {
	hdr http.Header
	err error
}

func (f *failingWriter) Header() http.Header {
	if f.hdr == nil {
		f.hdr = http.Header{}
	}
	return f.hdr
}
func (f *failingWriter) Write([]byte) (int, error) { return 0, f.err }
func (f *failingWriter) WriteHeader(int)           {}

// A response the client never received used to be logged as the status the bridge
// CHOSE, with no hint that the bytes did not land. That is a discarded error
// reported as a success.
func TestAFailedResponseWriteIsReportedRatherThanLoggedAsASuccess(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "application/json", body: openaiResp}
	upstream := httptest.NewServer(up.handler())
	defer upstream.Close()

	logged := captureDiag(t)
	h := NewHandler(upstream.URL, "k")
	h.ServeHTTP(&failingWriter{err: errors.New("broken pipe on the loopback")},
		httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(anthropicReq)))
	wantLines(t, logged(), "RESPONSE DELIVERY FAILED", "broken pipe on the loopback",
		"the client did not receive it")
}

// A 5xx that the bridge relays as 502 is a status REWRITE, and claude's retry
// policy reads the status. Nothing said so.
func TestTheFiveHundredToFiveOhTwoDowngradeIsNamed(t *testing.T) {
	up := &stubUpstream{t: t, status: http.StatusServiceUnavailable, contentType: "application/json",
		body: `{"error":{"message":"upstream is overloaded"}}`}
	upstream := httptest.NewServer(up.handler())
	defer upstream.Close()
	srv := httptest.NewServer(NewHandler(upstream.URL, "k"))
	defer srv.Close()

	logged := captureDiag(t)
	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(anthropicReq))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	wantLines(t, logged(), "upstream returned 503", "relaying it as 502")
}

// The Codex route's once-only 401 retry: a decision taken silently looks exactly
// like no decision at all, and it costs an extra upstream round trip that the
// request line's duration quietly absorbs.
func TestTheUnauthorizedRetryIsReported(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","model":"terra","status":"completed","output":[]}`)
	}))
	defer upstream.Close()
	h := newHandler(upstream.URL, "/responses", "", wirebridge.TranslateResponsesRequest,
		wirebridge.TranslateResponsesResponse,
		func() streamTranslator { return wirebridge.NewResponsesStreamTranslator() }).(*bridgeHandler)
	h.accessToken = func() (string, string, error) { return "view", "acct", nil }
	h.retryUnauthorized = true
	srv := httptest.NewServer(h)
	defer srv.Close()

	logged := captureDiag(t)
	resp, err := http.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"terra","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	wantLines(t, logged(), "upstream answered 401", "retrying once")
}

// The readiness descriptor: readinessRequested used to accept any integer while
// the writer refused anything under 3, so with the variable set to "2" the daemon
// believed a boot was waiting and then answered it nowhere.
func TestAnUnusableReadinessDescriptorIsReportedAndNotClaimedAsRequested(t *testing.T) {
	t.Setenv(paths.JailDaemonReadyFDEnv, "2")
	logged := captureDiag(t)
	if readinessRequested() {
		t.Error("fd 2 is not a readiness pipe this daemon can answer on, so a boot must not be " +
			"treated as waiting: the two halves of that question have to agree")
	}
	signalNotReady(ServiceName, "some reason")
	wantLines(t, logged(), paths.JailDaemonReadyFDEnv, `"2"`, "not a descriptor")
}

// The credential channel: falling back to the process environment because the
// file is absent is normal; doing it because the file is THERE AND UNREADABLE is
// a fault wearing the normal case's clothes.
func TestAnUnreadableCredentialChannelReportsTheDegradation(t *testing.T) {
	home := t.TempDir()
	// A directory where the file belongs: readable by no uid, including root,
	// which a mode-0000 file would not be under this jail's UID 0.
	if err := os.MkdirAll(userEnvFilePath(home), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIREBRIDGE_DIAG_FALLBACK", "from-env")
	logged := captureDiag(t)
	key, source := resolveKey("WIREBRIDGE_DIAG_FALLBACK", home)
	if key != "from-env" || source != "process environment" {
		t.Fatalf("resolveKey = %q from %q, want the environment fallback", key, source)
	}
	wantLines(t, logged(), userEnvFilePath(home), "could not be read",
		"falling back to this process's own environment")
}

// An absent channel file is the ordinary `yolo host` notch, and must stay quiet —
// otherwise the report above cannot mean anything.
func TestAnAbsentCredentialChannelIsSilent(t *testing.T) {
	logged := captureDiag(t)
	resolveKey("WIREBRIDGE_DIAG_MISSING", t.TempDir())
	if got := logged(); got != "" {
		t.Errorf("an absent channel file is the expected case, not a degradation:\n%s", got)
	}
}

// A malformed YOLO_USE_PROFILES entry is dropped by design. It used to be dropped
// silently, and the resulting idle line read `claude's active profile  resolves to
// no provider` — an empty profile name that looks like a bug in the message.
func TestAMalformedUseProfilesEntryIsReported(t *testing.T) {
	logged := captureDiag(t)
	_, idle := resolveRoute(routeEnv(bridgedProviders,
		`{"cerebras-fast":{"provider":"cerebras"}}`, `{"claude":42}`))
	if idle == "" {
		t.Fatal("a malformed selection must idle the bridge, not guess a profile")
	}
	wantLines(t, logged(), "YOLO_USE_PROFILES entry \"claude\"", "not a profile name")
}

// An idle reason is reported once per DISTINCT reason, not once per poll tick: the
// idle loop re-evaluates the decision every 200ms for the daemon's whole lifetime.
func TestIdleReasonsAreReportedOncePerDistinctReason(t *testing.T) {
	logged := captureDiag(t)
	logIdle("reason one")
	logIdle("reason one")
	logIdle("reason two")
	got := logged()
	if n := strings.Count(got, "reason one"); n != 1 {
		t.Errorf("the same idle reason was reported %d times, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "reason two") {
		t.Errorf("a CHANGED idle reason must be reported — that is the attach becoming visible:\n%s", got)
	}
}

func TestOneLineFlattensAMultiLineDetail(t *testing.T) {
	if got := oneLine("bind: address in use\nheld by pid 7"); strings.Contains(got, "\n") {
		t.Errorf("oneLine left a newline in %q, which would be read as a second readiness record", got)
	}
}

// logWriter is the sink the OpenAI credential service's own stderr frames reach
// instead of io.Discard. Line-buffered, so a frame split mid-sentence is one
// record rather than two.
func TestLogWriterPrefixesWholeLines(t *testing.T) {
	logged := captureDiag(t)
	w := logWriter("OpenAI credential service: ")
	fmt.Fprint(w, "refreshing the ")
	fmt.Fprint(w, "access view\nsecond line\n")
	got := logged()
	wantLines(t, got, "OpenAI credential service: refreshing the access view",
		"OpenAI credential service: second line")
	if strings.Contains(got, "refreshing the \n") {
		t.Errorf("a partial frame was logged as its own record:\n%s", got)
	}
}
