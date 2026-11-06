package callbroker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func serveTestHTTPFront(t *testing.T, front HTTPFront) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := newHTTPFrontServer(front)
	go func() { _ = server.Serve(frontListener(listener)) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return "http://" + listener.Addr().String()
}

func TestSelectedPackHTTPFrontBindsHostIdentity(t *testing.T) {
	var originRequests atomic.Int32
	var originAuthorization atomic.Value
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		originAuthorization.Store(r.Header.Get("Authorization"))
		if r.URL.Path == "/items/a1" {
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"a1"}`))
	}))
	h, _ := selectedHTTPReadFixture(t, origin, executor)
	_, endpoint := selectedHTTPHostFrames(t, h)
	frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 5 * time.Second})

	req, err := http.NewRequest(http.MethodGet, frontURL+"/project?view=compact", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "jail-forged")
	req.Header.Set("Yolo-Verdict", "runs")
	req.Header.Set("Yolo-Request", "forged-correlation")
	resp, err := http.DefaultClient.Do(req)
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
	if resp.Header.Get("Yolo-Authorization-Stripped") != "true" {
		t.Fatal("front did not disclose that the jail Authorization header was stripped")
	}
	if resp.Header.Get("Yolo-Verdict") != "" || resp.Header.Get("Yolo-Request") != "" {
		t.Fatalf("client-controlled Yolo-* authority escaped into the response: verdict=%q request=%q", resp.Header.Get("Yolo-Verdict"), resp.Header.Get("Yolo-Request"))
	}
	if got := originAuthorization.Load(); got != "Bearer synthetic-only-host-value" {
		t.Fatalf("remote Authorization = %v; jail-controlled authorization crossed", got)
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
			h, _ := selectedHTTPReadFixture(t, origin, executor)
			t.Setenv("SYNTHETIC_HOST_TOKEN", tc.token)
			_, endpoint := selectedHTTPHostFrames(t, h)
			frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 5 * time.Second})

			resp, err := http.Get(frontURL + "/project")
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
	h, _ := selectedHTTPReadFixture(t, origin, executor)
	_, endpoint := selectedHTTPHostFrames(t, h)
	frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 5 * time.Second})
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
			req, err := http.NewRequest(tc.method, frontURL+"/project", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
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
		var refusal struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(body).Decode(&refusal); err != nil {
			t.Fatal(err)
		}
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

	t.Run("host reports post-send receiver failure", func(t *testing.T) {
		var originRequests atomic.Int32
		_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originRequests.Add(1)
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "short")
		}))
		h, _ := selectedHTTPReadFixture(t, origin, executor)
		_, endpoint := selectedHTTPHostFrames(t, h)
		frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 5 * time.Second})
		resp, err := http.Get(frontURL + "/project")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		checkWarning(t, resp.StatusCode, resp.Body, 1)
		if originRequests.Load() != 1 {
			t.Fatalf("origin requests=%d, want one possibly-transmitted request", originRequests.Load())
		}
	})

	t.Run("front transport drops after host receives request", func(t *testing.T) {
		var dispatches atomic.Int32
		_, endpoint := selectedHTTPHostFramesWithHandler(t, func(s *hostservice.Session) {
			dispatches.Add(1)
			_ = s.Conn().Close()
		})
		frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 5 * time.Second})
		resp, err := http.Get(frontURL + "/project")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		checkWarning(t, resp.StatusCode, resp.Body, dispatches.Load())
	})
}

func TestHTTPFrontRejectsHeaderCaptureOverflowBeforeDispatch(t *testing.T) {
	var dispatches atomic.Int32
	_, endpoint := selectedHTTPHostFramesWithHandler(t, func(s *hostservice.Session) {
		dispatches.Add(1)
		s.Stdout(`{"status":200,"headers":{},"body_base64":""}`)
		s.Exit(0)
	})
	frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 5 * time.Second})
	prefix := "POST /graphql HTTP/1.1\r\nHost: 127.0.0.1\r\nX-Padding: "
	suffix := "\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
	padding := 65627 - len(prefix) - len(suffix)
	if padding < 0 {
		t.Fatal("padded request fixture underflow")
	}
	request := prefix + strings.Repeat(" ", padding) + suffix
	if len(request) != 65627 {
		t.Fatalf("padded request size=%d, want 65627", len(request))
	}
	resp := rawHTTPFrontRequest(t, frontURL, request, 3*time.Second)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || dispatches.Load() != 0 {
		t.Fatalf("header capture overflow reached host: status=%d dispatches=%d", resp.StatusCode, dispatches.Load())
	}
}

func TestHTTPFrontBoundsBodyReadByRequestTimeout(t *testing.T) {
	var dispatches atomic.Int32
	_, endpoint := selectedHTTPHostFramesWithHandler(t, func(s *hostservice.Session) {
		dispatches.Add(1)
		s.Stdout(`{"status":200,"headers":{},"body_base64":""}`)
		s.Exit(0)
	})
	frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 300 * time.Millisecond})
	started := time.Now()
	resp := rawHTTPFrontRequest(t, frontURL, "POST /graphql HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 1\r\nConnection: close\r\n\r\n", 3*time.Second)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || time.Since(started) > 2*time.Second || dispatches.Load() != 0 {
		t.Fatalf("stalled body was not bounded before host dispatch: status=%d elapsed=%s dispatches=%d", resp.StatusCode, time.Since(started), dispatches.Load())
	}
}

func rawHTTPFrontRequest(t *testing.T, frontURL, request string, timeout time.Duration) *http.Response {
	t.Helper()
	address := strings.TrimPrefix(frontURL, "http://")
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
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

func TestPackHTTPFrontRefusalShape(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	var receiverWrites atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			receiverWrites.Add(1)
		}
		if r.URL.Path == "/items/a1" {
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
		}
	}))
	h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
	askAnswer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"POST","path":"/graphql","query":"","headers":[],"body":{"mode":"original","present":true,"sha256":%q}},"writes":[{"cell":"change","chain":[{"type":"project","id":"team:alpha"}],"description":"rename task"}]}}`, digestHTTPBody(true, []byte(original)))
	classifier := fixtureHTTPClassifier(t, "cat <<'ANSWER'\n"+askAnswer+"\nANSWER")
	h.classifier = classifier
	h.classifier.Cells = append([]loopholedecl.CallBrokerCell(nil), h.input.Cells...)
	h.input.ProgramIdentities = classifier.Identities
	launchInput, err := json.Marshal(h.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, launchInput, 0600); err != nil {
		t.Fatal(err)
	}
	h.original = launchInput
	_, endpoint := selectedHTTPHostFrames(t, h)
	frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 5 * time.Second})

	req, err := http.NewRequest(http.MethodPost, frontURL+"/graphql", strings.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "jail-forged")
	req.Header.Set("Yolo-Request", "forged-correlation")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	requestID := resp.Header.Get("Yolo-Request")
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Yolo-Verdict") != "asks" || requestID == "" || requestID == "forged-correlation" || len(body.Errors) != 1 || !strings.Contains(body.Errors[0].Message, "yolo approve ") {
		t.Fatalf("ask refusal missing the HTTP contract or actionable menu: status=%d headers=%v body=%+v", resp.StatusCode, resp.Header, body)
	}
	if resp.Header.Get("Yolo-Authorization-Stripped") != "true" || receiverWrites.Load() != 0 {
		t.Fatalf("ask forwarded jail credentials or caused a target write: stripped=%q writes=%d", resp.Header.Get("Yolo-Authorization-Stripped"), receiverWrites.Load())
	}
	pending, _, err := h.store.List()
	if err != nil || len(pending) != 1 || requestID != pending[0].ID {
		t.Fatalf("Yolo-Request is not the opaque host pending correlator: id=%q pending=%+v err=%v", requestID, pending, err)
	}
}

func TestHTTPFrontPreservesEmptyBodyPresence(t *testing.T) {
	observed := make(chan bool, 2)
	_, endpoint := selectedHTTPHostFramesWithHandler(t, func(s *hostservice.Session) {
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
	frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 5 * time.Second})
	address := strings.TrimPrefix(frontURL, "http://")

	send := func(request string) {
		t.Helper()
		conn, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := io.WriteString(conn, request); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("front response status=%d", response.StatusCode)
		}
	}
	send("POST /graphql HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
	send("POST /graphql HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	if absent, explicitEmpty := <-observed, <-observed; absent || !explicitEmpty {
		t.Fatalf("front lost empty HTTP request body presence: absent=%v explicit_empty=%v", absent, explicitEmpty)
	}
}

func TestHTTPFrontRejectsOversizeBodyBeforeHostDispatch(t *testing.T) {
	var originRequests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
	}))
	h, _ := selectedHTTPReadFixture(t, origin, executor)
	_, endpoint := selectedHTTPHostFrames(t, h)
	frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 5 * time.Second})

	body := strings.NewReader(strings.Repeat("x", httpFrontBodyLimit+1))
	req, err := http.NewRequest(http.MethodPost, frontURL+"/graphql", body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var refusal struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&refusal); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden || originRequests.Load() != 0 || len(refusal.Errors) != 1 || !strings.Contains(refusal.Errors[0].Message, "3 MiB") {
		t.Fatalf("oversize body reached dispatch: status=%d origin=%d body=%+v", resp.StatusCode, originRequests.Load(), refusal)
	}
}

func TestHTTPFrontRefusesInvalidHostAuthenticationBeforeDispatch(t *testing.T) {
	var originRequests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
	}))
	h, _ := selectedHTTPReadFixture(t, origin, executor)
	_, endpoint := selectedHTTPHostFrames(t, h)
	published, err := svcendpoint.Read(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	published.Token = strings.Repeat("0", len(published.Token))
	if err := os.WriteFile(endpoint, []byte(published.Format()), 0600); err != nil {
		t.Fatal(err)
	}
	frontURL := serveTestHTTPFront(t, HTTPFront{Endpoint: endpoint, Timeout: 5 * time.Second})
	resp, err := http.Get(frontURL + "/project")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var refusal struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&refusal); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Yolo-Verdict") != "refused" || len(refusal.Errors) != 1 || originRequests.Load() != 0 {
		t.Fatalf("invalid selected host authentication reached dispatch: status=%d headers=%v refusal=%+v requests=%d", resp.StatusCode, resp.Header, refusal, originRequests.Load())
	}
}

func TestHTTPFrontRequiresLoopbackListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ServeHTTPFront(ctx, "/selected/host.endpoint", "0.0.0.0:0"); err == nil || !strings.Contains(err.Error(), "literal loopback") {
		t.Fatalf("non-loopback listener accepted: %v", err)
	}
}
