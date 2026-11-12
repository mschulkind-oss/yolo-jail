package callbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func selectedHTTPReadFixture(t *testing.T, origin string, executor httpExecutor) (*HTTPHandler, *onceFixture) {
	return selectedHTTPReadFixtureWithNative(t, origin, executor, false)
}

func selectedHTTPReadFixtureWithNative(t *testing.T, origin string, executor httpExecutor, native bool) (*HTTPHandler, *onceFixture) {
	t.Helper()
	f := selectedOnceFixture(t)
	moduleDir := filepath.Join(f.home, "test-pack", "loopholes", "tasks")
	manifestPath := filepath.Join(moduleDir, "manifest.jsonc")
	original, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	binding := loopholedecl.CallBrokerHTTP{Origin: origin, Credential: loopholedecl.CallBrokerHTTPCredential{Source: "host_env", Key: "SYNTHETIC_HOST_TOKEN", Header: "Authorization", Prefix: "Bearer "}, RequestHeaders: []string{"Content-Type"}}
	var httpDeclaration = fmt.Sprintf(`"front":"http","http":{"origin":%q,"credential":{"source":"host_env","key":"SYNTHETIC_HOST_TOKEN","header":"Authorization","prefix":"Bearer "},"request_headers":["Content-Type"]},"read_over_post":["/graphql"]`, origin)
	updated := strings.Replace(string(original), `"front":"cli","program":"tasks"`, httpDeclaration, 1)
	if native {
		updated = strings.Replace(updated, `"cli":["lookup","{id}"]`, `"http_method":"GET","http_path":"/items/{id}","http_resource":"task","http_id_pointer":"/id","http_parent_pointer":"/project/id"`, 1)
	}
	if updated == string(original) {
		t.Fatal("fixture did not replace the selected CLI declaration")
	}
	if err := os.WriteFile(manifestPath, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	declared, err := loopholedecl.Decode([]byte(updated), moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	answer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"GET","path":"/project","query":"","headers":[],"body":{"mode":"original","present":false,"sha256":%q}},"reads":[[{"type":"project","id":"team:alpha"}]]}}`, digestHTTPBody(false, nil))
	script := "cat >/dev/null\ncat <<'ANSWER'\n" + answer + "\nANSWER"
	if native {
		forPath := func(path string) string {
			return fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"GET","path":%q,"query":"","headers":[],"body":{"mode":"original","present":false,"sha256":%q}},"reads":[[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}]]}}`, path, digestHTTPBody(false, nil))
		}
		script = "input=$(cat)\ncase \"$input\" in *'\"path\":\"/items/a1\"'*) cat <<'LOOKUP'\n" + forPath("/items/a1") + "\nLOOKUP\n;; *) cat <<'READ'\n" + forPath("/task/a1") + "\nREAD\n;; esac"
	}
	classifier := fixtureHTTPClassifier(t, script)
	f.h.input.Front, f.h.input.Program = "http", ""
	f.h.input.DeclarationHash = loopholedecl.CallBrokerDeclarationHash(declared.CallBroker)
	f.h.input.HTTPBinding = &config.ApprovedCallBrokerHTTP{Pack: f.h.input.Pack, Source: f.h.input.Source,
		DeclarationHash: f.h.input.DeclarationHash, HTTP: binding, ReadOverPost: []string{"/graphql"}}
	f.h.input.ProgramIdentities = classifier.Identities
	f.h.input.Lookups = declared.CallBroker.Lookups
	data, err := json.Marshal(f.h.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	h, err := newHTTPHandler(f.inputPath, f.home, f.store, f.h.audit)
	if err != nil {
		t.Fatal(err)
	}
	h.scopePath, h.bound, h.start = f.scopePath, f.scope, f.h.start
	h.httpClient = executor
	if _, _, err := h.currentAuthority(); err != nil {
		t.Fatalf("selected HTTP fixture is not live: %v", err)
	}
	return h, f
}

func TestHTTPConstructorValidatesPreparedGenerationBeforeAnyLiveFrontState(t *testing.T) {
	h, f := selectedHTTPReadFixture(t, "https://fixture.example", httpExecutor{})
	in := h.input
	if in.FrontJailID == "" {
		in.FrontJailID = "fixture-front-id"
	}
	scope, err := brokerscope.ReadFile(in.ScopeFile)
	if err != nil {
		t.Fatal(err)
	}
	in.Roots = nil
	scope.Repos = nil
	in.ScopeFile, err = brokerscope.Write(scope)
	if err != nil {
		t.Fatal(err)
	}
	generation := strings.Repeat("a", 32)
	launch := writePreparedHTTPGeneration(t, f, &in, scope, generation)
	constructed, err := newHTTPHandler(f.inputPath, f.home, f.store, h.audit)
	if err != nil {
		t.Fatalf("valid pre-lease identity should construct without listener, certificate, trust or live start: %v", err)
	}
	if constructed.start != "" {
		t.Fatalf("constructor unexpectedly established live front state: start=%q", constructed.start)
	}

	validReference := `"http_front_generation":"` + generation + `"`
	for _, tc := range []struct {
		name, raw string
	}{
		{"null", strings.Replace(string(launch), validReference, `"http_front_generation":null`, 1)},
		{"empty", strings.Replace(string(launch), validReference, `"http_front_generation":""`, 1)},
		{"wrong type", strings.Replace(string(launch), validReference, `"http_front_generation":17`, 1)},
		{"malformed", strings.Replace(string(launch), validReference, `"http_front_generation":"not-a-generation"`, 1)},
		{"case-fold alias", strings.Replace(string(launch), `"http_front_generation"`, `"HTTP_FRONT_GENERATION"`, 1)},
		{"case-fold semantic duplicate", strings.Replace(string(launch), validReference, validReference+`,"HTTP_FRONT_GENERATION":""`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(f.inputPath, []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := newHTTPHandler(f.inputPath, f.home, f.store, h.audit); err == nil {
				t.Fatal("present invalid or aliased reference was accepted")
			}
		})
	}

	in.HTTPFrontGeneration = generation
	launch, _ = json.Marshal(in)
	if err := os.WriteFile(f.inputPath, launch, 0600); err != nil {
		t.Fatal(err)
	}

	in.HTTPFrontGeneration = strings.Repeat("b", 32)
	launch, _ = json.Marshal(in)
	if err := os.WriteFile(f.inputPath, launch, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newHTTPHandler(f.inputPath, f.home, f.store, h.audit); err == nil {
		t.Fatal("mismatched generation reference was accepted")
	}
	in.HTTPFrontGeneration = ""
	launch, _ = json.Marshal(in)
	if err := os.WriteFile(f.inputPath, launch, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.inputPath + ".prepared"); err != nil {
		t.Fatal(err)
	}
	if _, err := newHTTPHandler(f.inputPath, f.home, f.store, h.audit); err != nil {
		t.Fatalf("legacy G3 launch with omitted prepared-generation reference was refused: %v", err)
	}
}

// writePreparedHTTPGeneration writes a Task 1 prepared record for in and the
// launch input that references it, returning the launch bytes.
func writePreparedHTTPGeneration(t *testing.T, f *onceFixture, in *callbrokerinput.Launch, scope brokerscope.File, generation string) []byte {
	t.Helper()
	in.HTTPFrontGeneration = generation
	prepared := callbrokerinput.PreparedHTTPClientGeneration{
		Version: callbrokerinput.PreparedHTTPClientGenerationVersion, Phase: "prepared", Generation: generation,
		Container: scope.Container, FrontJailID: in.FrontJailID, Runtime: "podman", Setup: "podman-self",
		PackTree: "/host/selected-pack-tree", LaunchID: scope.LaunchID, Loophole: in.Loophole, Front: "http",
		Pack: in.Pack, Source: in.Source, DeclarationHash: in.DeclarationHash,
		Roots: append([]string{}, in.Roots...), ProgramIdentities: in.ProgramIdentities,
		Clients: []callbrokerinput.PreparedHTTPClientPlan{{Bin: "fixture-http-client", Pack: "client-pack", Front: in.Loophole,
			RecipePack: "client-pack", RecipeIdentity: "sha256:fixture", TokenVariable: "FIXTURE_API_TOKEN",
			URLVariable: "FIXTURE_BASE_URL", TrustVariable: "SSL_CERT_FILE"}},
	}
	body, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath+".prepared", body, 0600); err != nil {
		t.Fatal(err)
	}
	launch, err := json.Marshal(*in)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, launch, 0600); err != nil {
		t.Fatal(err)
	}
	return launch
}

// selectedPreparedHTTPReadFixture is the selected read fixture rebuilt from a
// launch input that references a valid prepared generation, still bound to the
// fixture's live scope and broker start.
func selectedPreparedHTTPReadFixture(t *testing.T, origin string, executor httpExecutor) (*HTTPHandler, *onceFixture, string) {
	t.Helper()
	base, f := selectedHTTPReadFixture(t, origin, executor)
	h, generation := preparedHTTPFixture(t, base, f)
	return h, f, generation
}

// preparedHTTPFixture rebuilds a selected HTTP fixture's handler from a launch
// input that references a valid prepared generation, keeping its live scope,
// broker start, executor and store revalidation.
func preparedHTTPFixture(t *testing.T, base *HTTPHandler, f *onceFixture) (*HTTPHandler, string) {
	t.Helper()
	in := base.input
	scope, err := brokerscope.ReadFile(in.ScopeFile)
	if err != nil {
		t.Fatal(err)
	}
	generation := strings.Repeat("a", 32)
	writePreparedHTTPGeneration(t, f, &in, scope, generation)
	h, err := newHTTPHandler(f.inputPath, f.home, f.store, base.audit)
	if err != nil {
		t.Fatalf("prepared selected HTTP fixture did not construct: %v", err)
	}
	h.scopePath, h.bound, h.start = f.scopePath, f.scope, f.h.start
	h.httpClient = base.httpClient
	if f.store.Revalidate != nil {
		f.store.Revalidate = h.revalidateHTTP
	}
	if _, _, err := h.currentAuthority(); err != nil {
		t.Fatalf("prepared selected HTTP fixture is not live: %v", err)
	}
	return h, generation
}

func selectedHTTPHostFrame(t *testing.T, h *HTTPHandler) func(string, HTTPCall) (int, string) {
	forward, _ := selectedHTTPHostFrames(t, h)
	return forward
}

func selectedHTTPHostFrames(t *testing.T, h *HTTPHandler) (func(string, HTTPCall) (int, string), string) {
	return selectedHTTPHostFramesWithHandler(t, h.Handle)
}

func selectedHTTPHostFramesWithHandler(t *testing.T, handler hostservice.Handler) (func(string, HTTPCall) (int, string), string) {
	t.Helper()
	raw, endpoint := selectedHTTPRawFramesAt(t, handler)
	return func(op string, call HTTPCall) (int, string) {
		t.Helper()
		return raw(map[string]any{"op": op, "http": call, "jail_id": "forged"})
	}, endpoint
}

// selectedHTTPRawFrames sends arbitrary frame objects through the same
// authenticated launch front as selectedHTTPHostFramesWithHandler.
func selectedHTTPRawFrames(t *testing.T, handler hostservice.Handler) func(map[string]any) (int, string) {
	raw, _ := selectedHTTPRawFramesAt(t, handler)
	return raw
}

func selectedHTTPRawFramesAt(t *testing.T, handler hostservice.Handler) (func(map[string]any) (int, string), string) {
	t.Helper()
	root := shortCallBrokerTempDir(t)
	socket := filepath.Join(root, "broker.sock")
	endpointDir := filepath.Join(root, "launch")
	if err := os.Mkdir(endpointDir, 0700); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(endpointDir, "broker.endpoint")
	stop := make(chan struct{})
	upstream := make(chan error, 1)
	front := make(chan error, 1)
	go func() {
		upstream <- hostservice.ServeHTTPBrokerFrontedUnix(handler, socket, stop, validateHTTPBrokerFrame)
	}()
	t.Cleanup(func() {
		close(stop)
		select {
		case err := <-upstream:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("HTTP upstream failed to stop")
		}
		select {
		case err := <-front:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("HTTP front failed to stop")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("HTTP upstream did not bind")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { front <- svcendpoint.ServeFront(endpoint, "127.0.0.1", socket, stop) }()
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("HTTP front did not bind")
		}
		time.Sleep(time.Millisecond)
	}
	return func(frame map[string]any) (int, string) {
		t.Helper()
		conn, err := svcendpoint.Dial(endpoint, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		payload, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		if err := frameproto.WriteRequest(conn, payload); err != nil {
			t.Fatal(err)
		}
		var response strings.Builder
		for {
			frame, err := frameproto.ReadFrame(conn)
			if err != nil {
				t.Fatal(err)
			}
			if frame.StreamID == frameproto.StreamExit {
				rc, err := frameproto.ExitCode(frame.Payload)
				if err != nil {
					t.Fatal(err)
				}
				return rc, response.String()
			}
			response.Write(frame.Payload)
		}
	}, endpoint
}

func selectedHTTPWriteFixture(t *testing.T, origin string, executor httpExecutor, body []byte) (*HTTPHandler, *onceFixture) {
	return selectedHTTPWriteFixtureWithBlocked(t, origin, executor, body, false)
}

func selectedHTTPWriteFixtureWithBlocked(t *testing.T, origin string, executor httpExecutor, body []byte, blocked bool) (*HTTPHandler, *onceFixture) {
	t.Helper()
	h, f := selectedHTTPReadFixtureWithNative(t, origin, executor, true)
	writeAnswer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"POST","path":"/graphql","query":"","headers":[],"body":{"mode":"original","present":true,"sha256":%q}},"writes":[{"cell":"change","chain":[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}],"description":"rename task a1"}]}}`, digestHTTPBody(true, body))
	if blocked {
		writeAnswer = strings.Replace(writeAnswer, `"description":"rename task a1"}]`, `"description":"rename task a1"},{"cell":"blocked","chain":[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}],"description":"blocked second write"}]`, 1)
	}
	lookupAnswer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"GET","path":"/items/a1","query":"","headers":[],"body":{"mode":"original","present":false,"sha256":%q}},"reads":[[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}]]}}`, digestHTTPBody(false, nil))
	script := "input=$(cat)\ncase \"$input\" in *'\"path\":\"/items/a1\"'*) cat <<'LOOKUP'\n" + lookupAnswer + "\nLOOKUP\n;; *) cat <<'WRITE'\n" + writeAnswer + "\nWRITE\n;; esac"
	classifier := fixtureHTTPClassifier(t, script)
	h.classifier.Identities = classifier.Identities
	h.input.ProgramIdentities = classifier.Identities
	data, err := json.Marshal(h.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	h.original = data
	f.store.Revalidate = h.revalidateHTTP
	return h, f
}

func TestSelectedHTTPCustomSignerRemainsFailClosedBeforeCredentialOrLookup(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprintf("native-%t", native), func(t *testing.T) {
			var requests atomic.Int32
			_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
			}))
			h, f := selectedHTTPReadFixtureWithNative(t, origin, executor, native)
			marker := filepath.Join(f.home, "signer-started")
			signer := filepath.Join(f.home, "test-pack", "signer")
			if err := os.WriteFile(signer, []byte("#!/bin/sh\necho invoked >"+marker+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(f.home, "test-pack", "loopholes", "tasks", "manifest.jsonc")
			manifest, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(string(manifest), `"classifier":["{program:classifier}"]`, `"classifier":["{program:classifier}"],"executor":"{program:signer}"`, 1)
			if changed == string(manifest) {
				t.Fatal("signer declaration not selected")
			}
			if err := os.WriteFile(manifestPath, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
			declared, err := loopholedecl.Decode([]byte(changed), filepath.Dir(manifestPath))
			if err != nil {
				t.Fatal(err)
			}
			h.input.Executor = "{program:signer}"
			h.input.DeclarationHash = loopholedecl.CallBrokerDeclarationHash(declared.CallBroker)
			h.input.HTTPBinding.DeclarationHash = h.input.DeclarationHash
			identity := h.classifier.Identities["classifier"]
			h.input.ProgramIdentities["signer"] = loopholedecl.CallBrokerProgramIdentity{Pack: identity.Pack, Bin: "signer", Path: signer, SHA256: strings.Repeat("0", 64)}
			data, err := json.Marshal(h.input)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.inputPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			h.original = data
			if _, _, err := h.currentAuthority(); err != nil {
				t.Fatalf("selected custom signer fixture is not live: %v", err)
			}
			forward := selectedHTTPHostFrame(t, h)
			call := HTTPCall{Method: "GET", Path: "/project"}
			if native {
				call.Path = "/task/a1"
			}
			if rc, message := forward("act", call); rc == 0 || requests.Load() != 0 {
				t.Fatalf("custom signer path reached origin: rc=%d message=%q requests=%d", rc, message, requests.Load())
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("unsupported signer started before authority: %v", err)
			}
		})
	}
}

func TestSelectedHTTPWritePossibleTransmissionNeverRefundsOrRetries(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	for _, scenario := range []string{"response over cap", "cancel after request"} {
		t.Run(scenario, func(t *testing.T) {
			var writes atomic.Int32
			var cancel context.CancelFunc
			release := make(chan struct{})
			defer close(release)
			_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() == "/items/a1" {
					_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
					return
				}
				writes.Add(1)
				if scenario == "response over cap" {
					_, _ = w.Write(bytes.Repeat([]byte("x"), httpResponseLimit+1))
				} else {
					cancel()
					<-release
				}
			}))
			h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
			ctx, stop := context.WithCancel(context.Background())
			cancel = stop
			defer stop()
			h.shutdown = ctx
			forward := selectedHTTPHostFrame(t, h)
			call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
			if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) {
				t.Fatalf("ask: rc=%d message=%q", rc, message)
			}
			pending, _, err := f.store.List()
			if err != nil || len(pending) != 1 {
				t.Fatalf("pending: %+v err=%v", pending, err)
			}
			if _, err := f.store.Decide(pending[0].ID, "pending", brokerstore.Decision{}); err != nil {
				t.Fatal(err)
			}
			if rc, message := forward("act", call); rc == 0 || writes.Load() != 1 || !strings.Contains(strings.ToLower(message), "inspect") {
				t.Fatalf("possibly transmitted write not disclosed: rc=%d message=%q writes=%d", rc, message, writes.Load())
			}
			_, grants, err := f.store.List()
			if err != nil || len(grants) != 1 || grants[0].Uses != 0 {
				t.Fatalf("possibly transmitted write refunded: grants=%+v err=%v", grants, err)
			}
			if scenario == "response over cap" {
				if rc, message := forward("act", call); rc == 0 || writes.Load() != 1 {
					t.Fatalf("automatic retry followed unusable response: rc=%d message=%q writes=%d", rc, message, writes.Load())
				}
			}
		})
	}
}

func TestSelectedHTTPHostResponseCapAndKnownTokenRedaction(t *testing.T) {
	const secret = "synthetic-only-host-value"
	var mode, first, second atomic.Int32
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		second.Add(1)
	}))
	defer redirect.Close()
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first.Add(1)
		switch mode.Load() {
		case 1:
			_, _ = w.Write(bytes.Repeat([]byte("x"), httpResponseLimit+1))
		case 2:
			w.Header().Set("Location", redirect.URL+"?token="+secret)
			w.WriteHeader(http.StatusFound)
		default:
			w.Header().Set("X-Echo", "before-"+secret+"-after")
			w.Header().Set("Set-Cookie", "token="+secret)
			_, _ = w.Write([]byte("prefix-" + secret[:8]))
			_, _ = w.Write([]byte(secret[8:] + "-suffix"))
		}
	}))
	h, _ := selectedHTTPReadFixture(t, origin, executor)
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "GET", Path: "/project"}
	rc, encoded := forward("act", call)
	if rc != 0 || first.Load() != 1 {
		t.Fatalf("redaction read: rc=%d message=%q first=%d", rc, encoded, first.Load())
	}
	var reply struct {
		Headers http.Header `json:"headers"`
		Body    []byte      `json:"body_base64"`
	}
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil || strings.Contains(string(reply.Body), secret) || !strings.Contains(string(reply.Body), "[redacted]") || strings.Contains(fmt.Sprint(reply.Headers), secret) || reply.Headers.Get("Set-Cookie") != "" {
		t.Fatalf("known token escaped host response: body=%q headers=%v err=%v", reply.Body, reply.Headers, err)
	}
	mode.Store(1)
	if rc, message := forward("act", call); rc == 0 || first.Load() != 2 || second.Load() != 0 || strings.Contains(message, secret) {
		t.Fatalf("oversized response retried or leaked: rc=%d message=%q first=%d second=%d", rc, message, first.Load(), second.Load())
	}
	mode.Store(2)
	if rc, message := forward("act", call); rc == 0 || first.Load() != 3 || second.Load() != 0 || strings.Contains(message, secret) {
		t.Fatalf("known token in Location leaked or followed: rc=%d message=%q first=%d second=%d", rc, message, first.Load(), second.Load())
	}
}

func TestSelectedHTTPHostRejectsMalformedBoundedFramesBeforeOrigin(t *testing.T) {
	var requests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	h, _ := selectedHTTPReadFixture(t, origin, executor)
	var handlerCalls atomic.Int32
	_, endpoint := selectedHTTPHostFramesWithHandler(t, func(s *hostservice.Session) {
		handlerCalls.Add(1)
		h.Handle(s)
	})
	base := `{"op":"act","http":{"method":"GET","path":"/project","query":"","headers":[],"body_present":false,"body_base64":null}}`
	cases := []struct {
		name, raw string
		closed    bool
	}{
		{"unknown nested field", strings.Replace(base, `"path":"/project"`, `"path":"/project","unknown":"x"`, 1), true},
		{"uppercase method only", strings.Replace(base, `"method":"GET"`, `"Method":"GET"`, 1), true},
		{"variant method first POST", strings.Replace(base, `"method":"GET"`, `"Method":"POST","method":"GET"`, 1), true},
		{"variant method last POST", strings.Replace(base, `"method":"GET"`, `"method":"GET","Method":"POST"`, 1), true},
		{"variant body presence", strings.Replace(base, `"body_present":false`, `"Body_Present":true,"body_present":false`, 1), true},
		{"uppercase header fields", strings.Replace(base, `"headers":[]`, `"headers":[{"Name":"Content-Type","Values":["application/json"]}]`, 1), true},
		{"mixed header name", strings.Replace(base, `"headers":[]`, `"headers":[{"Name":"Content-Type","name":"Content-Type","values":["application/json"]}]`, 1), true},
		{"mixed header values", strings.Replace(base, `"headers":[]`, `"headers":[{"name":"Content-Type","Values":["x"],"values":["application/json"]}]`, 1), true},
		{"duplicate nested field", strings.Replace(base, `"method":"GET"`, `"method":"GET","method":"POST"`, 1), true},
		{"trailing value", base + ` {}`, true},
		{"malformed base64", strings.Replace(base, `"body_base64":null`, `"body_base64":"***"`, 1), false},
		{"raw aggregate above 16KiB", strings.Replace(base, `"path":"/project"`, fmt.Sprintf(`"path":"/%s"`, strings.Repeat("p", (16<<10)+1)), 1), false},
		{"name above 256", strings.Replace(base, `"headers":[]`, fmt.Sprintf(`"headers":[{"name":%q,"values":["value"]}]`, strings.Repeat("X", 257)), 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := handlerCalls.Load()
			originBefore := requests.Load()
			conn, err := svcendpoint.Dial(endpoint, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := frameproto.WriteRequest(conn, []byte(tc.raw)); err != nil {
				t.Fatal(err)
			}
			for {
				frame, err := frameproto.ReadFrame(conn)
				if err != nil {
					if !tc.closed {
						t.Fatalf("invalid field closed without refusal: %v", err)
					}
					break
				}
				if frame.StreamID == frameproto.StreamExit {
					rc, err := frameproto.ExitCode(frame.Payload)
					if err != nil || rc == 0 || tc.closed {
						t.Fatalf("invalid request returned rc=%d err=%v closed=%v", rc, err, tc.closed)
					}
					break
				}
			}
			if tc.closed && handlerCalls.Load() != before {
				t.Fatalf("invalid HTTP schema reached handler: before=%d after=%d", before, handlerCalls.Load())
			}
			if requests.Load() != originBefore {
				t.Fatalf("invalid request reached origin: before=%d after=%d", originBefore, requests.Load())
			}
		})
	}
	conn, err := svcendpoint.Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(conn, binary.BigEndian, uint32(4_456_449)); err != nil {
		t.Fatal(err)
	}
	before := requests.Load()
	if _, err := frameproto.ReadFrame(conn); err == nil || requests.Load() != before {
		t.Fatalf("oversized authenticated frame reached handler/origin: err=%v requests=%d", err, requests.Load())
	}
}

func TestHTTPCredentialParentSwapCannotReadOutsideApprovedFile(t *testing.T) {
	root := shortCallBrokerTempDir(t)
	approved := filepath.Join(root, "approved")
	outside := filepath.Join(root, "outside")
	for _, path := range []string{approved, outside} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	const ownedValue, outsideValue = "synthetic-approved-value", "synthetic-outside-value"
	for path, value := range map[string]string{approved: ownedValue, outside: outsideValue} {
		if err := os.WriteFile(filepath.Join(path, "token"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	selected := filepath.Join(approved, "token")
	spec := loopholedecl.CallBrokerHTTPCredential{Source: "fenced_file", Key: selected, Header: "Authorization"}
	called := false
	value, err := loadHTTPCredential(spec, []string{selected}, func() {
		called = true
		if renameErr := os.Rename(approved, approved+"-held"); renameErr != nil {
			t.Error(renameErr)
			return
		}
		if linkErr := os.Symlink(outside, approved); linkErr != nil {
			t.Error(linkErr)
		}
	})
	if !called || value == outsideValue || err == nil && value != ownedValue {
		t.Fatalf("swapped parent escaped held approved file: hook=%t value=%q err=%v", called, value, err)
	}
}

func TestHTTPCredentialParentSymlinkCannotEscapeApprovedPath(t *testing.T) {
	root := shortCallBrokerTempDir(t)
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(realDir, "token")
	if err := os.WriteFile(file, []byte("fixture-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "approved-link")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Fatal(err)
	}
	apparent := filepath.Join(alias, "token")
	spec := loopholedecl.CallBrokerHTTPCredential{Source: "fenced_file", Key: apparent, Header: "Authorization"}
	if value, err := loadHTTPCredential(spec, []string{apparent}); err == nil || value != "" {
		t.Fatalf("symlink ancestor escaped selected credential path: value=%q err=%v", value, err)
	}
}

func selectedHTTPFencedFileFixture(t *testing.T, origin string, executor httpExecutor, credentialPath string) (*HTTPHandler, *onceFixture) {
	t.Helper()
	h, f := selectedHTTPReadFixture(t, origin, executor)
	manifestPath := filepath.Join(f.home, "test-pack", "loopholes", "tasks", "manifest.jsonc")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(manifest), `"source":"host_env","key":"SYNTHETIC_HOST_TOKEN"`, fmt.Sprintf(`"source":"fenced_file","key":%q`, credentialPath), 1)
	changed = strings.Replace(changed, `"env_from_host":["SYNTHETIC_HOST_TOKEN"]`, fmt.Sprintf(`"env_from_host":["SYNTHETIC_HOST_TOKEN"],"credential_paths":[%q]`, credentialPath), 1)
	if changed == string(manifest) {
		t.Fatal("HTTP fixture did not select fenced file")
	}
	if err := os.WriteFile(manifestPath, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	declared, err := loopholedecl.Decode([]byte(changed), filepath.Dir(manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	h.input.DeclarationHash = loopholedecl.CallBrokerDeclarationHash(declared.CallBroker)
	h.input.HTTPBinding.DeclarationHash = h.input.DeclarationHash
	h.input.HTTPBinding.HTTP.Credential.Source = "fenced_file"
	h.input.HTTPBinding.HTTP.Credential.Key = credentialPath
	data, err := json.Marshal(h.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	h.original = data
	if _, _, err := h.currentAuthority(); err != nil {
		t.Fatalf("fenced-file fixture authority: %v", err)
	}
	return h, f
}

func TestSelectedHTTPFencedFileIsReadOnlyAfterDecisionAndRejectsUnsafeFiles(t *testing.T) {
	const secret = "host-only-fixture-token"
	var requests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Errorf("host credential not bound to file: %q", r.Header.Get("Authorization"))
		}
		requests.Add(1)
		_, _ = w.Write([]byte(`{"id":"team:alpha"}`))
	}))
	root := shortCallBrokerTempDir(t)
	credentialPath := filepath.Join(root, "credential")
	h, f := selectedHTTPFencedFileFixture(t, origin, executor, credentialPath)
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "GET", Path: "/project"}
	if rc, message := forward("evaluate", call); rc != 0 || !strings.Contains(message, `"verdict":"runs"`) || requests.Load() != 0 {
		t.Fatalf("evaluate opened missing credential: rc=%d message=%q requests=%d", rc, message, requests.Load())
	}
	if rc, message := forward("act", call); rc == 0 || requests.Load() != 0 {
		t.Fatalf("missing credential reached origin: rc=%d message=%q requests=%d", rc, message, requests.Load())
	}
	write := func(value []byte, mode os.FileMode) {
		t.Helper()
		if err := os.Remove(credentialPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.WriteFile(credentialPath, value, mode); err != nil {
			t.Fatal(err)
		}
	}
	write([]byte(secret), 0600)
	if rc, message := forward("act", call); rc != 0 || requests.Load() != 1 {
		t.Fatalf("private host file did not execute: rc=%d message=%q requests=%d", rc, message, requests.Load())
	}
	other := filepath.Join(root, "other")
	if err := os.WriteFile(other, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(credentialPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, credentialPath); err != nil {
		t.Fatal(err)
	}
	if rc, message := forward("act", call); rc == 0 || requests.Load() != 1 {
		t.Fatalf("symlink credential executed: rc=%d message=%q requests=%d", rc, message, requests.Load())
	}
	for _, value := range [][]byte{nil, []byte("bad\nvalue"), bytes.Repeat([]byte("x"), (8<<10)+1)} {
		write(value, 0600)
		if rc, message := forward("act", call); rc == 0 || requests.Load() != 1 {
			t.Fatalf("invalid credential %d bytes executed: rc=%d message=%q requests=%d", len(value), rc, message, requests.Load())
		}
	}
	write([]byte(secret), 0644)
	if rc, message := forward("act", call); rc == 0 || requests.Load() != 1 {
		t.Fatalf("world-readable credential executed: rc=%d message=%q requests=%d", rc, message, requests.Load())
	}
	if os.Getuid() == 0 {
		write([]byte(secret), 0600)
		if err := os.Chown(credentialPath, 1, 1); err != nil {
			t.Fatal(err)
		}
		if rc, message := forward("act", call); rc == 0 || requests.Load() != 1 {
			t.Fatalf("non-owner credential executed: rc=%d message=%q requests=%d", rc, message, requests.Load())
		}
	}
	if data := mustRead(t, filepath.Join(f.home, "audit.jsonl")); bytes.Contains(data, []byte(secret)) || bytes.Contains(h.original, []byte(secret)) {
		t.Fatal("host credential value entered audit or launch input")
	}
}

func TestSelectedHTTPClassifierRequestedFactsRequireExactReadAndFreshAct(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	var lookups, writes atomic.Int32
	var wrongID atomic.Bool
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/items/a1" {
			lookups.Add(1)
			if wrongID.Load() {
				_, _ = w.Write([]byte(`{"id":"b1","project":{"id":"team:alpha"}}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
			}
			return
		}
		writes.Add(1)
	}))
	h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
	writeAnswer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"POST","path":"/graphql","query":"","headers":[],"body":{"mode":"original","present":true,"sha256":%q}},"writes":[{"cell":"change","chain":[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}],"description":"rename task a1"}]}}`, digestHTTPBody(true, []byte(original)))
	lookupAnswer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"GET","path":"/items/a1","query":"","headers":[],"body":{"mode":"original","present":false,"sha256":%q}},"reads":[[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}]]}}`, digestHTTPBody(false, nil))
	lookupRequest := `{"version":"yolo.call-broker-classifier/1","lookups":[{"name":"task-project","arguments":["a1"]}]}`
	script := "input=$(cat)\ncase \"$input\" in *'\"path\":\"/items/a1\"'*) cat <<'READ'\n" + lookupAnswer + "\nREAD\n;; *'\"facts\":'*) cat <<'WRITE'\n" + writeAnswer + "\nWRITE\n;; *) cat <<'ASK'\n" + lookupRequest + "\nASK\n;; esac"
	classifier := fixtureHTTPClassifier(t, script)
	h.classifier.Identities = classifier.Identities
	h.input.ProgramIdentities = classifier.Identities
	data, err := json.Marshal(h.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	h.original = data
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
	if rc, message := forward("evaluate", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) || lookups.Load() < 1 || writes.Load() != 0 {
		t.Fatalf("classifier-requested lookup evaluate: rc=%d message=%q lookups=%d writes=%d", rc, message, lookups.Load(), writes.Load())
	}
	if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) || writes.Load() != 0 {
		t.Fatalf("classifier-requested lookup ask: rc=%d message=%q writes=%d", rc, message, writes.Load())
	}
	requests, _, err := f.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("pending: requests=%+v err=%v", requests, err)
	}
	if _, err := f.store.Decide(requests[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	before := lookups.Load()
	wrongID.Store(true)
	if rc, message := forward("act", call); rc == 0 || lookups.Load() <= before || writes.Load() != 0 {
		t.Fatalf("failed lookup fact guessed authority: rc=%d message=%q lookups=%d writes=%d", rc, message, lookups.Load(), writes.Load())
	}
	_, grants, err := f.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 1 {
		t.Fatalf("lookup failure spent approval: grants=%+v err=%v", grants, err)
	}
	wrongID.Store(false)
	if rc, message := forward("act", call); rc != 0 || writes.Load() != 1 || lookups.Load() <= before {
		t.Fatalf("fresh classifier fact did not permit exact act: rc=%d message=%q lookups=%d writes=%d", rc, message, lookups.Load(), writes.Load())
	}
}

func TestSelectedHTTPWritePreflightFailureBeforeSpend(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	for _, scenario := range []string{"DNS failed", "TLS rejected", "canceled", "original URL token"} {
		t.Run(scenario, func(t *testing.T) {
			var lookups, writes atomic.Int32
			_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() == "/items/a1" {
					lookups.Add(1)
					_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
					return
				}
				writes.Add(1)
			}))
			h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
			forward := selectedHTTPHostFrame(t, h)
			call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
			if scenario == "original URL token" {
				call.Query = "token=synthetic-only-host-value"
			}
			if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) {
				t.Fatalf("ask: rc=%d message=%q", rc, message)
			}
			requests, _, err := f.store.List()
			if err != nil || len(requests) != 1 {
				t.Fatalf("pending: %v %+v", err, requests)
			}
			if _, err := f.store.Decide(requests[0].ID, "pending", brokerstore.Decision{}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.shutdown = ctx
			var hookRan atomic.Bool
			h.beforeHTTPCommit = func() {
				hookRan.Store(true)
				switch scenario {
				case "DNS failed":
					h.httpClient.resolve = func(context.Context, string) ([]net.IPAddr, error) {
						return nil, errors.New("fixture DNS unavailable")
					}
				case "TLS rejected":
					h.httpClient.roots = x509.NewCertPool()
				case "canceled":
					cancel()
				}
			}
			before := lookups.Load()
			rc, message := forward("act", call)
			if !hookRan.Load() || rc == 0 || writes.Load() != 0 || lookups.Load() < before || !strings.Contains(message, "not spent") {
				t.Fatalf("preflight failure not exercised or undisclosed: hook=%v rc=%d message=%q reads=%d writes=%d", hookRan.Load(), rc, message, lookups.Load(), writes.Load())
			}
			_, grants, err := f.store.List()
			if err != nil || len(grants) != 1 || grants[0].Uses != 1 {
				t.Fatalf("preflight failure spent an untransmitted write: grants=%+v err=%v", grants, err)
			}
		})
	}
}

func TestSelectedHTTPReadWithdrawalDuringPinnedTLSHandshakeCannotSend(t *testing.T) {
	var requests atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseGate := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseGate()
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}), func() {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})
	h, f := selectedHTTPReadFixture(t, origin, executor)
	forward := selectedHTTPHostFrame(t, h)
	finished := make(chan int, 1)
	go func() {
		rc, _ := forward("act", HTTPCall{Method: "GET", Path: "/project"})
		finished <- rc
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("standing read never reached TLS handshake")
	}
	_, granted, err := loopholegrant.Read(f.grantPath)
	if err != nil || granted == nil {
		t.Fatalf("read grant: %v", err)
	}
	withdrawn := *granted
	withdrawn.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.NotAllowed}}
	if err := loopholegrant.Replace(context.Background(), f.grantPath, *granted, withdrawn); err != nil {
		t.Fatal(err)
	}
	releaseGate()
	select {
	case rc := <-finished:
		if rc == 0 || requests.Load() != 0 {
			t.Fatalf("withdrawn standing read sent: rc=%d requests=%d", rc, requests.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("standing read did not return after TLS release")
	}
}

func TestSelectedHTTPWriteWithdrawalDuringPinnedTLSHandshakeCannotSpend(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	// hangGuard only turns a hang into a red test. It is not a budget the broker has
	// to beat: every pass condition below is an event (handshake entered, forwarder
	// returned, the gated connection's handshake result, its close).
	const hangGuard = 90 * time.Second
	var block, writes atomic.Bool
	var gated, resumed atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseGate := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseGate()
	// The witness binds every later event to the ONE connection that was gated. block is
	// set only by beforeHTTPCommit, after the ask and the act's own lookup reads, so their
	// handshakes are never the gated connection and cannot stand in for it.
	type handshakeResult struct {
		version uint16
		err     error
	}
	type endResult struct {
		n   int
		err error
	}
	var witnessMu sync.Mutex
	var gatedConn net.Conn
	var otherHandshakes, otherHandshakeFailures int
	gatedHandshake := make(chan handshakeResult, 1)
	gatedEnd := make(chan endResult, 1)
	witness := &pinnedTLSWitness{
		gate: func(conn net.Conn) {
			if !block.Load() {
				return
			}
			gated.Add(1)
			witnessMu.Lock()
			first := gatedConn == nil
			if first {
				gatedConn = conn
			}
			witnessMu.Unlock()
			if first {
				entered <- struct{}{}
			}
			<-release
			resumed.Add(1)
		},
		handshake: func(conn net.Conn, state tls.ConnectionState, err error) {
			witnessMu.Lock()
			isGated := gatedConn != nil && conn == gatedConn
			if !isGated {
				otherHandshakes++
				if err != nil {
					otherHandshakeFailures++
				}
			}
			witnessMu.Unlock()
			if isGated {
				gatedHandshake <- handshakeResult{state.Version, err}
			}
		},
		ended: func(conn net.Conn, n int, err error) {
			witnessMu.Lock()
			isGated := gatedConn != nil && conn == gatedConn
			witnessMu.Unlock()
			if isGated {
				gatedEnd <- endResult{n, err}
			}
		},
	}
	_, executor, origin := fixturePinnedHTTPServerWithTLSWitness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/items/a1" {
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
			return
		}
		writes.Store(true)
	}), witness)
	h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
	if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) {
		t.Fatalf("ask: rc=%d message=%q", rc, message)
	}
	pending, _, err := f.store.List()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %+v err=%v", pending, err)
	}
	if _, err := f.store.Decide(pending[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	h.beforeHTTPCommit = func() { block.Store(true) }
	type result struct {
		rc      int
		message string
	}
	finished := make(chan result, 1)
	// returned closes when the forwarder goroutine is gone, whether it produced a
	// result or the frame helper ended it with t.Fatal (which sends no result).
	returned := make(chan struct{})
	gateState := func() string {
		witnessMu.Lock()
		defer witnessMu.Unlock()
		return fmt.Sprintf("gated handshakes=%d resumed=%d writes=%v ungated handshakes=%d (failed %d)", gated.Load(), resumed.Load(), writes.Load(), otherHandshakes, otherHandshakeFailures)
	}
	// Registered after every fixture, so it runs before the deferred gate release above
	// and before any fixture t.Cleanup: the handshake hook is released and the forwarder
	// is gone before the TLS server, the front or the store are torn down.
	defer func() {
		releaseGate()
		select {
		case <-returned:
		case <-time.After(hangGuard):
			t.Errorf("forwarder still live %s after the TLS gate was released; fixtures are torn down under it: %s", hangGuard, gateState())
		}
	}()
	go func() {
		defer close(returned)
		rc, message := forward("act", call)
		finished <- result{rc, message}
	}()
	early := func() string {
		select {
		case got := <-finished:
			return fmt.Sprintf("rc=%d message=%q", got.rc, got.message)
		default:
			return "no result (the frame helper failed the test from the forwarder)"
		}
	}
	select {
	case <-entered:
	case <-returned:
		t.Fatalf("prepared write returned before reaching the gated TLS handshake: %s; %s", early(), gateState())
	case <-time.After(hangGuard):
		t.Fatalf("prepared write neither reached the TLS handshake nor returned within the %s hang guard: %s", hangGuard, gateState())
	}
	_, granted, err := loopholegrant.Read(f.grantPath)
	if err != nil || granted == nil {
		t.Fatalf("read grant: %v", err)
	}
	withdrawn := *granted
	withdrawn.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.NotAllowed}}
	if err := loopholegrant.Replace(context.Background(), f.grantPath, *granted, withdrawn); err != nil {
		t.Fatal(err)
	}
	releaseGate()
	select {
	case <-returned:
	case <-time.After(hangGuard):
		t.Fatalf("forwarder did not return within the %s hang guard after the TLS gate was released: %s", hangGuard, gateState())
	}
	var response result
	select {
	case response = <-finished:
	default:
		t.Fatalf("forwarder ended without a result after the TLS gate was released: %s", gateState())
	}
	_, grants, err := f.store.List()
	if response.rc == 0 || writes.Load() || err != nil || len(grants) != 1 || grants[0].Uses != 1 {
		t.Fatalf("withdrawn during TLS preparation: rc=%d message=%q grants=%+v err=%v; %s", response.rc, response.message, grants, err, gateState())
	}
	// A nonzero rc with nothing written and nothing spent is also what a FAILED or
	// timed-out TLS preparation produces (the handler context is 30s, shorter than the
	// hang guard). So the refusal counts as a withdrawal only with positive proof that
	// the gated write connection's TLS preparation SUCCEEDED and was then abandoned unused.
	outcome := fmt.Sprintf("rc=%d message=%q", response.rc, response.message)
	// 1. The server's Handshake returned nil on the gated connection: the client verified
	// the pinned certificate and name and sent its Finished.
	select {
	case got := <-gatedHandshake:
		if got.err != nil || got.version != tls.VersionTLS13 {
			t.Fatalf("gated write connection did not complete a TLS 1.3 handshake (version=%#x err=%v), so the refusal is a TLS failure and not a withdrawal: %s; %s", got.version, got.err, outcome, gateState())
		}
	case <-time.After(hangGuard):
		t.Fatalf("no TLS handshake result for the gated write connection within the %s hang guard: %s; %s", hangGuard, outcome, gateState())
	}
	// 2. The client then closed that connection with no request bytes. Only the prepared
	// request's close sends a close_notify, which the server reads as a clean io.EOF. A
	// handshake the client abandoned, context expiry included, closes the socket bare,
	// and the witness reports that as a non-EOF error (pinnedTLSWitnessEndedUnused).
	select {
	case got := <-gatedEnd:
		if !pinnedTLSWitnessEndedUnused(got.n, got.err) {
			t.Fatalf("gated write connection was not closed unused by a completed TLS preparation (first server read n=%d err=%v; want n=0 and a close_notify io.EOF): %s; %s", got.n, got.err, outcome, gateState())
		}
	case <-time.After(hangGuard):
		t.Fatalf("gated write connection was never read to its end within the %s hang guard: %s; %s", hangGuard, outcome, gateState())
	}
	// 3. The broker itself reports the refusal as a preparation that failed before the
	// spend (httpWriteFailure), not as a failure after preparation.
	if !strings.Contains(response.message, "No HTTP request bytes were transmitted and Allow once was not spent") {
		t.Fatalf("withdrawal was not reported as a refusal before spend: %s; %s", outcome, gateState())
	}
}

func TestSelectedHTTPWriteWithdrawalBeforeTransmitDoesNotSpend(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	var lookups, transmissions atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/items/a1" {
			lookups.Add(1)
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
			return
		}
		transmissions.Add(1)
	}))
	h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
	if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) {
		t.Fatalf("ask: rc=%d message=%q", rc, message)
	}
	requests, _, err := f.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("pending: %v %+v", err, requests)
	}
	if _, err := f.store.Decide(requests[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	var hookRan atomic.Bool
	h.beforeHTTPCommit = func() {
		hookRan.Store(true)
		_, granted, readErr := loopholegrant.Read(f.grantPath)
		if readErr != nil || granted == nil {
			t.Errorf("read grant at commit boundary: %v", readErr)
			return
		}
		withdrawn := *granted
		withdrawn.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.NotAllowed}}
		if err := loopholegrant.Replace(context.Background(), f.grantPath, *granted, withdrawn); err != nil {
			t.Errorf("withdraw at commit boundary: %v", err)
		}
	}
	before := lookups.Load()
	rc, message := forward("act", call)
	if !hookRan.Load() || rc == 0 || transmissions.Load() != 0 || lookups.Load() <= before {
		t.Fatalf("boundary withdrawal not exercised: hook=%v rc=%d message=%q lookups=%d transmissions=%d", hookRan.Load(), rc, message, lookups.Load(), transmissions.Load())
	}
	_, grants, err := f.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 1 {
		t.Fatalf("pre-transmission withdrawal spent a grant: grants=%+v err=%v", grants, err)
	}
}

func TestSelectedHTTPMultiwriteRefusesEntireBundleBeforeOneHostPOST(t *testing.T) {
	const original = `{"operationName":"RenameTwo","title":"ready"}`
	var writes atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/items/a1" {
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
			return
		}
		writes.Add(1)
	}))
	h, f := selectedHTTPWriteFixtureWithBlocked(t, origin, executor, []byte(original), true)
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
	if rc, message := forward("act", call); rc == 0 || writes.Load() != 0 {
		t.Fatalf("blocked second write partially dispatched: rc=%d message=%q writes=%d", rc, message, writes.Load())
	}
	requests, grants, err := f.store.List()
	if err != nil || len(requests) != 0 || len(grants) != 0 {
		t.Fatalf("blocked multiwrite left approvals: requests=%+v grants=%+v err=%v", requests, grants, err)
	}
}

func TestSelectedHTTPWriteRedirectDoesNotSendSecondRequestOrRefund(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	var first, second atomic.Int32
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		second.Add(1)
	}))
	defer redirect.Close()
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/items/a1" {
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
			return
		}
		first.Add(1)
		w.Header().Set("Location", redirect.URL)
		w.WriteHeader(http.StatusFound)
	}))
	h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
	if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) {
		t.Fatalf("ask: rc=%d message=%q", rc, message)
	}
	requests, _, err := f.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("pending: %+v err=%v", requests, err)
	}
	if _, err := f.store.Decide(requests[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	if rc, message := forward("act", call); rc != 0 || !strings.Contains(message, `"status":302`) || first.Load() != 1 || second.Load() != 0 {
		t.Fatalf("redirect unexpectedly followed: rc=%d message=%q first=%d second=%d", rc, message, first.Load(), second.Load())
	}
	if rc, message := forward("act", call); rc == 0 || first.Load() != 1 || second.Load() != 0 {
		t.Fatalf("redirected response retried: rc=%d message=%q first=%d second=%d", rc, message, first.Load(), second.Load())
	}
	_, grants, err := f.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 0 {
		t.Fatalf("redirect request did not spend once: grants=%+v err=%v", grants, err)
	}
}

func TestSelectedHTTPWriteRevokedReadOrChangedNativeParentCannotSpend(t *testing.T) {
	const original = `{"operationName":"Rename","query":"mutation Rename { renameWorkItem(id: \"a1\", title: \"ready\") { id } }"}`
	for _, scenario := range []string{"parent changed", "read withdrawn", "pack deselected", "binding changed", "audit failed", "staged classifier changed", "lease removed"} {
		t.Run(scenario, func(t *testing.T) {
			var parentChanged atomic.Bool
			var writes, lookups atomic.Int32
			_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() == "/items/a1" {
					lookups.Add(1)
					parent := "team:alpha"
					if parentChanged.Load() {
						parent = "team:beta"
					}
					_, _ = fmt.Fprintf(w, `{"id":"a1","project":{"id":%q}}`, parent)
					return
				}
				writes.Add(1)
			}))
			h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
			forward := selectedHTTPHostFrame(t, h)
			call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
			if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) {
				t.Fatalf("ask: rc=%d message=%q", rc, message)
			}
			requests, _, err := f.store.List()
			if err != nil || len(requests) != 1 {
				t.Fatalf("pending: %v %+v", err, requests)
			}
			if _, err := f.store.Decide(requests[0].ID, "pending", brokerstore.Decision{}); err != nil {
				t.Fatal(err)
			}
			before := lookups.Load()
			switch scenario {
			case "parent changed":
				parentChanged.Store(true)
			case "read withdrawn":
				_, grant, err := loopholegrant.Read(f.grantPath)
				if err != nil || grant == nil {
					t.Fatalf("read grant: %v", err)
				}
				withdrawn := *grant
				withdrawn.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.NotAllowed}}
				if err := loopholegrant.Replace(context.Background(), f.grantPath, *grant, withdrawn); err != nil {
					t.Fatal(err)
				}
			case "pack deselected":
				if err := os.WriteFile(f.userPath, []byte("packs = []\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "binding changed":
				if err := os.WriteFile(f.inputPath, bytes.Replace(h.original, []byte(origin), []byte("https://elsewhere.example"), 1), 0600); err != nil {
					t.Fatal(err)
				}
			case "audit failed":
				bad := filepath.Join(f.home, "audit-blocked")
				if err := os.Mkdir(bad, 0700); err != nil {
					t.Fatal(err)
				}
				h.audit = brokeraudit.Open(bad, nil)
			case "staged classifier changed":
				identity := h.classifier.Identities["classifier"]
				file, err := os.OpenFile(identity.Path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("\n# changed after approval\n"); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			case "lease removed":
				lease := filepath.Join(h.store.Dir, ".lifetime-"+h.start)
				paused := lease + ".paused"
				if err := os.Rename(lease, paused); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := os.Rename(paused, lease); err != nil {
						t.Error(err)
					}
				}()
			}
			if rc, message := forward("act", call); rc == 0 || writes.Load() != 0 {
				t.Fatalf("changed authority dispatched: rc=%d message=%q writes=%d", rc, message, writes.Load())
			}
			if scenario != "parent changed" && lookups.Load() != before {
				t.Fatalf("revoked authority still ran lookup: before=%d after=%d", before, lookups.Load())
			}
			_, grants, err := f.store.List()
			if err != nil || len(grants) != 1 || grants[0].Uses != 1 {
				t.Fatalf("refused write spent approval: err=%v grants=%+v", err, grants)
			}
		})
	}
}

func TestSelectedHTTPHostExactOnceRequiresResubmittedBodyAndFreshNativeChain(t *testing.T) {
	const original = `{"operationName":"Rename","query":"mutation Rename { renameWorkItem(id: \"a1\", title: \"ready\") { id } }"}`
	var lookups, writes atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/items/a1" {
			lookups.Add(1)
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
			return
		}
		if r.Method != "POST" || r.URL.EscapedPath() != "/graphql" {
			t.Errorf("unexpected HTTP write request %s %s", r.Method, r.URL.EscapedPath())
		}
		body := make([]byte, len(original))
		_, _ = r.Body.Read(body)
		if string(body) != original || r.Header.Get("Authorization") != "Bearer synthetic-only-host-value" {
			t.Errorf("write bytes or host credential changed: body=%q authorization=%q", body, r.Header.Get("Authorization"))
		}
		writes.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
	if rc, message := forward("evaluate", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) || writes.Load() != 0 {
		t.Fatalf("evaluate write: rc=%d message=%q writes=%d", rc, message, writes.Load())
	}
	if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) || writes.Load() != 0 {
		t.Fatalf("ask write: rc=%d message=%q writes=%d", rc, message, writes.Load())
	}
	pending, _, err := f.store.List()
	if err != nil || len(pending) != 1 || pending[0].HTTPOriginalDigest == "" || pending[0].HTTPCanonicalDigest == "" || pending[0].OriginalArgv != nil {
		t.Fatalf("HTTP pending custody: pending=%+v err=%v", pending, err)
	}
	if record, _ := json.Marshal(pending[0]); bytes.Contains(record, []byte("Rename")) || bytes.Contains(record, []byte("ready")) || bytes.Contains(record, []byte("body_base64")) {
		t.Fatal("pending record archived the HTTP body")
	}
	if _, err := ApprovalAuthority(pending[0]); err != nil {
		t.Fatalf("host approval cannot revalidate HTTP metadata: %v", err)
	}
	if _, err := f.store.Decide(pending[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	changed := call
	changed.Body = []byte(strings.Replace(original, "ready", "changed", 1))
	if rc, message := forward("act", changed); rc == 0 || writes.Load() != 0 {
		t.Fatalf("changed resubmitted body dispatched: rc=%d message=%q writes=%d", rc, message, writes.Load())
	}
	if rc, message := forward("act", call); rc != 0 || writes.Load() != 1 {
		t.Fatalf("approved original did not dispatch exactly once: rc=%d message=%q writes=%d lookups=%d", rc, message, writes.Load(), lookups.Load())
	}
	if rc, message := forward("act", call); rc == 0 || writes.Load() != 1 {
		t.Fatalf("spent Allow once dispatched again: rc=%d message=%q writes=%d", rc, message, writes.Load())
	}
	pending, grants, err := f.store.List()
	if err != nil || len(grants) != 1 || grants[0].EndedBy() != brokerstore.EndUsed || grants[0].Uses != 0 || writes.Load() != 1 {
		t.Fatalf("write/spend mismatch: pending=%d grants=%+v err=%v writes=%d", len(pending), grants, err, writes.Load())
	}
}

func TestHTTPNativeJSONPointerSelectsOnlyScalarStrings(t *testing.T) {
	body := []byte(` {"a/b":{"~key":[{"id":"a1"}]},"missing":null,"number":7} `)
	for _, tc := range []struct {
		pointer, want string
	}{
		{"/a~1b/~0key/0/id", "a1"},
		{"/a~1b/~0key/01/id", ""},
		{"/a~1b/~0key/0", ""},
		{"/missing", ""},
		{"/number", ""},
		{"/a~9b", ""},
		{"a/b", ""},
	} {
		got, err := httpPointerString(body, tc.pointer)
		if got != tc.want || (err == nil) != (tc.want != "") {
			t.Errorf("pointer %q: got %q err %v, want %q", tc.pointer, got, err, tc.want)
		}
	}
}

func TestSelectedHTTPNativeLookupUsesCurrentApprovedRootsWithoutReopeningRevokedOnes(t *testing.T) {
	var lookups, reads atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/items/a1" {
			lookups.Add(1)
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:beta"}}`))
			return
		}
		reads.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	h, f := selectedHTTPReadFixtureWithNative(t, origin, executor, true)
	identity := h.classifier.Identities["classifier"]
	binary, err := os.ReadFile(identity.Path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(string(binary), `"team:alpha"`, `"team:beta"`)
	if changed == string(binary) {
		t.Fatal("native classifier fixture did not select beta")
	}
	if err := os.WriteFile(identity.Path, []byte(changed), 0700); err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256([]byte(changed))
	identity.SHA256 = fmt.Sprintf("%x", checksum[:])
	h.classifier.Identities["classifier"] = identity
	h.input.ProgramIdentities["classifier"] = identity
	f.scope.Repos = []string{"team:alpha"}
	h.bound.Repos = []string{"team:alpha"}
	h.input.Roots = []string{"team:alpha"}
	h.classifier.Roots = []string{"team:alpha"}
	launch, err := json.Marshal(h.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, launch, 0600); err != nil {
		t.Fatal(err)
	}
	h.original = launch
	if _, err := brokerscope.Write(f.scope); err != nil {
		t.Fatal(err)
	}
	expanded := f.scope
	expanded.Repos = []string{"team:alpha", "team:beta"}
	if _, err := brokerscope.Write(expanded); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.currentAuthority(); err != nil {
		t.Fatalf("newly approved host root is not live: %v", err)
	}
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "GET", Path: "/task/a1"}
	if rc, message := forward("act", call); rc != 0 || lookups.Load() != 1 || reads.Load() != 1 {
		t.Fatalf("live beta root did not reach native lookup: rc=%d message=%q lookups=%d reads=%d", rc, message, lookups.Load(), reads.Load())
	}
	if _, err := brokerscope.Write(f.scope); err != nil {
		t.Fatal(err)
	}
	if rc, message := forward("act", call); rc == 0 || lookups.Load() != 1 || reads.Load() != 1 {
		t.Fatalf("withdrawn beta root still dispatched: rc=%d message=%q lookups=%d reads=%d", rc, message, lookups.Load(), reads.Load())
	}
}

func TestSelectedHTTPNativeLookupCanonicalMustMatchInstantiatedRequest(t *testing.T) {
	var requests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
	}))
	h, f := selectedHTTPReadFixtureWithNative(t, origin, executor, true)
	identity := h.classifier.Identities["classifier"]
	binary, err := os.ReadFile(identity.Path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(binary), `"canonical":{"method":"GET","path":"/items/a1"`, `"canonical":{"method":"GET","path":"/items/b1"`, 1)
	if changed == string(binary) {
		t.Fatal("fixture canonical request did not change")
	}
	if err := os.WriteFile(identity.Path, []byte(changed), 0700); err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256([]byte(changed))
	identity.SHA256 = fmt.Sprintf("%x", checksum[:])
	h.classifier.Identities["classifier"] = identity
	h.input.ProgramIdentities["classifier"] = identity
	launch, err := json.Marshal(h.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, launch, 0600); err != nil {
		t.Fatal(err)
	}
	h.original = launch
	forward := selectedHTTPHostFrame(t, h)
	if rc, message := forward("act", HTTPCall{Method: "GET", Path: "/task/a1"}); rc == 0 || requests.Load() != 0 {
		t.Fatalf("altered canonical lookup reached origin: rc=%d message=%q requests=%d", rc, message, requests.Load())
	}
}

func TestSelectedHTTPHostNativeParentLookupRequiresCurrentExactReadAndReturnedID(t *testing.T) {
	var lookups, reads atomic.Int32
	var returnWrongID, returnWrongParent, duplicateFact, missingParent atomic.Bool
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/items/a1" {
			lookups.Add(1)
			if returnWrongID.Load() {
				_, _ = w.Write([]byte(`{"id":"b1","project":{"id":"team:alpha"}}`))
			} else if returnWrongParent.Load() {
				_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:beta"}}`))
			} else if duplicateFact.Load() {
				_, _ = w.Write([]byte(`{"id":"a1","id":"b1","project":{"id":"team:alpha"}}`))
			} else if missingParent.Load() {
				_, _ = w.Write([]byte(`{"id":"a1","project":{}}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
			}
			return
		}
		if r.URL.EscapedPath() != "/task/a1" {
			t.Errorf("unexpected host request path %q", r.URL.EscapedPath())
		}
		reads.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	h, f := selectedHTTPReadFixtureWithNative(t, origin, executor, true)
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "GET", Path: "/task/a1"}
	if rc, message := forward("evaluate", call); rc != 0 || !strings.Contains(message, `"verdict":"runs"`) || lookups.Load() != 1 || reads.Load() != 0 {
		t.Fatalf("evaluate native lookup: rc=%d message=%q lookups=%d reads=%d", rc, message, lookups.Load(), reads.Load())
	}
	if rc, message := forward("act", call); rc != 0 || lookups.Load() != 2 || reads.Load() != 1 {
		t.Fatalf("act fresh native lookup: rc=%d message=%q lookups=%d reads=%d", rc, message, lookups.Load(), reads.Load())
	}
	returnWrongID.Store(true)
	if rc, message := forward("act", call); rc == 0 || lookups.Load() != 3 || reads.Load() != 1 {
		t.Fatalf("wrong native ID reached read: rc=%d message=%q lookups=%d reads=%d", rc, message, lookups.Load(), reads.Load())
	}
	returnWrongID.Store(false)
	returnWrongParent.Store(true)
	if rc, message := forward("act", call); rc == 0 || lookups.Load() != 4 || reads.Load() != 1 {
		t.Fatalf("changed native parent reached read: rc=%d message=%q lookups=%d reads=%d", rc, message, lookups.Load(), reads.Load())
	}
	returnWrongParent.Store(false)
	duplicateFact.Store(true)
	if rc, message := forward("act", call); rc == 0 || lookups.Load() != 5 || reads.Load() != 1 {
		t.Fatalf("duplicate native fact reached read: rc=%d message=%q lookups=%d reads=%d", rc, message, lookups.Load(), reads.Load())
	}
	duplicateFact.Store(false)
	missingParent.Store(true)
	if rc, message := forward("act", call); rc == 0 || lookups.Load() != 6 || reads.Load() != 1 {
		t.Fatalf("missing native parent reached read: rc=%d message=%q lookups=%d reads=%d", rc, message, lookups.Load(), reads.Load())
	}
	_, granted, err := loopholegrant.Read(f.grantPath)
	if err != nil || granted == nil {
		t.Fatalf("current native read grant: %v", err)
	}
	withdrawn := *granted
	withdrawn.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.NotAllowed}}
	if err := loopholegrant.Replace(context.Background(), f.grantPath, *granted, withdrawn); err != nil {
		t.Fatal(err)
	}
	if rc, message := forward("act", call); rc == 0 || lookups.Load() != 6 || reads.Load() != 1 {
		t.Fatalf("withdrawn read reached lookup: rc=%d message=%q lookups=%d reads=%d", rc, message, lookups.Load(), reads.Load())
	}
}

func TestSelectedHTTPHostReadRechecksSelectionAndSendsPinnedBytes(t *testing.T) {
	var requests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.URL.EscapedPath() != "/project" || r.Header.Get("Authorization") != "Bearer synthetic-only-host-value" || r.Header.Get("Cookie") != "" {
			t.Errorf("host received unexpected request: method=%q path=%q authorization=%q cookie=%q", r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization"), r.Header.Get("Cookie"))
		}
		_, _ = w.Write([]byte(`{"id":"team:alpha"}`))
	}))
	h, f := selectedHTTPReadFixture(t, origin, executor)
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "GET", Path: "/project", Headers: []HTTPHeader{{Name: "Authorization", Values: []string{"jail-forged"}}, {Name: "Cookie", Values: []string{"jail-cookie"}}}}
	if rc, message := forward("evaluate", call); rc != 0 || !strings.Contains(message, `"verdict":"runs"`) || requests.Load() != 0 {
		t.Fatalf("evaluation made a remote request: rc=%d response=%q requests=%d", rc, message, requests.Load())
	}
	rc, message := forward("act", call)
	if rc != 0 || !strings.Contains(message, `"status":200`) || !strings.Contains(message, `"body_base64":"eyJpZCI6InRlYW06YWxwaGEifQ=="`) || requests.Load() != 1 {
		t.Fatalf("host read: rc=%d response=%q requests=%d", rc, message, requests.Load())
	}
	secretQuery := call
	secretQuery.Query = "token=synthetic-only-host-value"
	if rc, message := forward("act", secretQuery); rc == 0 || requests.Load() != 1 {
		t.Fatalf("known host credential in original URL reached origin: rc=%d response=%q requests=%d", rc, message, requests.Load())
	}
	if rc, message := forward("act", HTTPCall{Method: "PUT", Path: "/project"}); rc == 0 || requests.Load() != 1 {
		t.Fatalf("unsafe read reached origin: rc=%d response=%q requests=%d", rc, message, requests.Load())
	}
	const privateBody = `{"operationName":"Rename","title":"private-fixture-sentinel"}`
	if rc, message := forward("ask", HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(privateBody)}); rc == 0 || requests.Load() != 1 {
		t.Fatalf("unverified HTTP write reached origin: rc=%d response=%q requests=%d", rc, message, requests.Load())
	}
	pending, grants, err := f.store.List()
	if err != nil || len(pending) != 0 || len(grants) != 0 {
		t.Fatalf("unverified HTTP write persisted authority: pending=%d grants=%d err=%v", len(pending), len(grants), err)
	}
	if audit := mustRead(t, filepath.Join(f.home, "audit.jsonl")); bytes.Contains(audit, []byte(privateBody)) {
		t.Fatal("HTTP body entered host audit")
	}
	goodAudit := h.audit
	blocked := filepath.Join(f.home, "audit-blocked")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	h.audit = brokeraudit.Open(blocked, nil)
	if rc, message := forward("act", call); rc == 0 || requests.Load() != 1 {
		t.Fatalf("audit failure reached origin: rc=%d response=%q requests=%d", rc, message, requests.Load())
	}
	h.audit = goodAudit
	_, granted, err := loopholegrant.Read(f.grantPath)
	if err != nil || granted == nil {
		t.Fatalf("read grant fixture: %v", err)
	}
	withdrawn := *granted
	withdrawn.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.NotAllowed}}
	if err := loopholegrant.Replace(context.Background(), f.grantPath, *granted, withdrawn); err != nil {
		t.Fatal(err)
	}
	if rc, message := forward("act", call); rc == 0 || requests.Load() != 1 {
		t.Fatalf("read withdrawal reached origin: rc=%d response=%q requests=%d", rc, message, requests.Load())
	}
	if err := os.WriteFile(f.userPath, []byte("packs = []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if rc, message := forward("act", call); rc == 0 || requests.Load() != 1 {
		t.Fatalf("deselection reached origin: rc=%d response=%q requests=%d", rc, message, requests.Load())
	}
}
