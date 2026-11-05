package ghbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func TestAccessClientRejectsLegacyAndMalformedReplies(t *testing.T) {
	for _, body := range []string{
		`{"verdict":"asks","rule":"old act-shaped response"}`,
		`{"schema":"yolo.access.prediction/1","verdict":"asks"}`,
		`{"schema":"yolo.access.prediction/1","verdict":"maybe","call":{},"command_digest":"x","covered_by":"","next":[],"relies_on_until":null,"rule":"x","source":"github"}`,
	} {
		if hasAccessSchema([]byte(body), "yolo.access.prediction/1") {
			t.Errorf("accepted unsupported response %s", body)
		}
	}
}

func TestAccessForwardPreservesStdinBytesAndEmpty(t *testing.T) {
	for _, body := range []string{"a\x00\xffz", ""} {
		req := map[string]any{}
		err := addCallContext(req, []string{"api", "--input", "-"}, ForwardEnv{Stdin: strings.NewReader(body), OriginRepo: func() string { return "owner/repo" }})
		if err != nil {
			t.Fatal(err)
		}
		encoded, ok := req["stdin"].(string)
		if !ok {
			t.Fatalf("stdin field absent for body %q: %#v", body, req)
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || string(decoded) != body {
			t.Fatalf("decoded=%q err=%v want=%q", decoded, err, body)
		}
		if req["repo"] != "owner/repo" {
			t.Fatalf("repo resolution=%#v", req["repo"])
		}
	}
	req := map[string]any{}
	if err := addCallContext(req, []string{"api", "repos/o/r"}, ForwardEnv{Stdin: strings.NewReader("ignored"), OriginRepo: func() string { return "" }}); err != nil {
		t.Fatal(err)
	}
	if _, exists := req["stdin"]; exists {
		t.Fatalf("stdin sent when command did not request it: %#v", req)
	}
}

func TestForwardAccessAcceptsFullGhCommand(t *testing.T) {
	b, executed := windowFixture(t)
	env, _ := frontedEndpoint(t, b, productionEndpointDirs(authorityContainer)["container"])
	response, code := ForwardAccess("evaluate", []string{"gh", "pr", "merge", "40", "--squash", "-R", "o/r"}, env)
	if code != 0 {
		t.Fatalf("access evaluate exit=%d response=%s", code, response)
	}
	var got evaluation
	if err := json.Unmarshal(response, &got); err != nil {
		t.Fatal(err)
	}
	if got.Verdict != "asks" || len(got.Call.Canonical) == 0 || got.Call.Canonical[0] != "gh" || executed.Load() != 0 {
		t.Fatalf("full command prediction=%+v executed=%d", got, executed.Load())
	}
}

func TestForwardAccessWireEnvelopeThroughEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   string
		argv []string
	}{
		{name: "status", op: "status"},
		{name: "evaluate", op: "evaluate", argv: []string{"api", "repos/o/r"}},
		{name: "ask", op: "ask", argv: []string{"api", "-X", "POST", "repos/o/r/issues/1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type wireFrame struct {
				topLevel    []string
				hasAccess   bool
				op          string
				version     int64
				argv        []string
				argvPresent bool
			}
			var frames []wireFrame
			handler := func(s *hostservice.Session) {
				frame := wireFrame{topLevel: append([]string(nil), s.Request.Keys()...)}
				if value, ok := s.Get("access"); ok {
					if access, ok := value.(*jsonx.OrderedMap); ok {
						frame.hasAccess = true
						if op, ok := access.Get("op"); ok {
							frame.op, _ = op.(string)
						}
						if version, ok := access.Get("version"); ok {
							frame.version, _ = jsonx.AsInt(version)
						}
						if argv, ok := access.Get("argv"); ok {
							frame.argvPresent = true
							encoded, err := json.Marshal(argv)
							if err != nil {
								t.Errorf("marshal nested argv %T: %v", argv, err)
							} else if err := json.Unmarshal(encoded, &frame.argv); err != nil {
								t.Errorf("decode nested argv %T: %v", argv, err)
							}
						}
					}
				}
				frames = append(frames, frame)
				if len(frames) > 2 {
					t.Errorf("ForwardAccess exceeded fake endpoint's two-response cap")
					s.Exit(ExitUsage)
					return
				}

				// Return schemas to the real ForwardAccess call; the wire capture above
				// observes the request after it crossed the published endpoint.
				requestOp := frame.op
				if requestOp == "" {
					if op, ok := s.Get("op"); ok {
						requestOp, _ = op.(string)
					}
				}
				if requestOp == "status" {
					_ = s.JSON(map[string]any{"schema": "yolo.access/1", "source": "github", "state": "running",
						"repositories": []string{}, "read": "granted", "grants": []any{}, "working_set": []any{},
						"requests": []any{}, "limits": map[string]any{}})
				} else {
					_ = s.JSON(map[string]any{"schema": "yolo.access.prediction/1", "verdict": "refused", "source": "github",
						"call": map[string]any{}, "command_digest": "", "covered_by": "", "next": []any{},
						"relies_on_until": nil, "rule": "fixture"})
				}
				s.Exit(0)
			}
			endpoint := startAccessEndpoint(t, handler, productionEndpointDirs(authorityContainer)["container"])
			env := ForwardEnv{Getenv: func(name string) string {
				if name == EndpointEnv {
					return endpoint
				}
				return ""
			}, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, OriginRepo: func() string { return "o/r" }}
			response, code := ForwardAccess(tc.op, tc.argv, env)
			if code != 0 {
				t.Fatalf("ForwardAccess(%q) exit=%d response=%s", tc.op, code, response)
			}
			wantSchema := "yolo.access.prediction/1"
			if tc.op == "status" {
				wantSchema = "yolo.access/1"
			}
			if !hasAccessSchema(response, wantSchema) {
				t.Fatalf("ForwardAccess(%q) returned invalid fixture response: %s", tc.op, response)
			}
			wantFrames := 2
			if tc.op == "status" {
				wantFrames = 1
			}
			if len(frames) != wantFrames {
				t.Fatalf("captured %d frames, want %d: %+v", len(frames), wantFrames, frames)
			}
			for i, frame := range frames {
				if len(frame.topLevel) != 1 || frame.topLevel[0] != "access" || !frame.hasAccess {
					t.Errorf("frame %d must have access as its sole top-level key: %+v", i, frame)
				}
				wantOp := "status"
				if i == wantFrames-1 {
					wantOp = tc.op
				}
				if frame.op != wantOp || frame.version != 1 {
					t.Errorf("frame %d access operation/version = %q/%d, want %q/1: %+v", i, frame.op, frame.version, wantOp, frame)
				}
				wantArgv := i == wantFrames-1 && tc.op != "status"
				if frame.argvPresent != wantArgv {
					t.Errorf("frame %d nested argv present=%v, want %v: %+v", i, frame.argvPresent, wantArgv, frame)
				}
				if wantArgv && strings.Join(frame.argv, "\x00") != strings.Join(tc.argv, "\x00") {
					t.Errorf("frame %d nested argv=%q, want %q", i, frame.argv, tc.argv)
				}
			}
		})
	}
}

func TestAccessForwardCannotTurnIntoLegacyActAfterEndpointRotation(t *testing.T) {
	legacy := "/workspace/.yolo/durable/worktrees/access-legacy-broker-fixture-5a361ba58"
	source := filepath.Join(legacy, "internal/ghbroker")
	if _, err := os.Stat(source); os.IsNotExist(err) {
		t.Skip("historical worktree 5a361ba58 is needed for the actual handler proof")
	}
	sha, err := exec.Command("git", "-C", legacy, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(sha)) != "5a361ba5826acbf120280180c9f918dbc1748c89" {
		t.Fatalf("historical worktree must be 5a361ba5826acbf120280180c9f918dbc1748c89: %s %v", sha, err)
	}
	root := t.TempDir()
	fixtureDir, err := os.MkdirTemp(os.TempDir(), "gh-hist-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fixtureDir) })
	fixture, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	overlayFile := filepath.Join(root, "overlay.json")
	virtual := filepath.Join(source, "legacy_handler_5a361_test.go")
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{virtual: filepath.Join(fixture, "testdata/legacy_handler_5a361_test.go")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlayFile, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "legacy-ghbroker.test")
	build := exec.Command("go", "test", "-c", "-overlay", overlayFile, "-o", binary, "./internal/ghbroker")
	build.Dir = legacy // Build under the original HOME and effective Go settings; only the binary gets fixture HOME.
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build historical broker: %v\n%s", err, output)
	}
	historical := exec.Command(binary, "-test.run=^(TestActualLegacyHandler5a361|TestLegacySocketWaitReportsEarlyBindFailure)$", "-test.v")
	historical.Dir = root
	historical.Env = append(os.Environ(), "YOLO_LEGACY_FIXTURE_DIR="+fixtureDir, "HOME="+filepath.Join(root, "home"))
	var output bytes.Buffer
	historical.Stdout, historical.Stderr = &output, &output
	if err := historical.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- historical.Wait() }()
	t.Cleanup(func() {
		_ = historical.Process.Kill()
		select {
		case <-done:
		default:
		}
	})
	var legacyEndpoint string
	deadline := time.Now().Add(10 * time.Second)
	for legacyEndpoint == "" {
		data, err := os.ReadFile(filepath.Join(fixtureDir, "endpoint"))
		if err == nil {
			legacyEndpoint = string(data)
			break
		}
		select {
		case err := <-done:
			t.Fatalf("historical handler exited before publishing endpoint: %v\n%s", err, output.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("historical endpoint not ready: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	env := ForwardEnv{Getenv: func(name string) string {
		if name == EndpointEnv {
			return legacyEndpoint
		}
		return ""
	}, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, OriginRepo: func() string { return "o/r" }}
	if code := Forward(writeReq().Argv, env); code != 0 {
		t.Fatalf("actual historical ACT positive control exit=%d; %s", code, output.String())
	}
	before, err := os.ReadFile(filepath.Join(fixtureDir, "synthetic-write"))
	if err != nil || len(before) == 0 {
		t.Fatalf("historical fake executor did not write: %v", err)
	}
	b, executed := windowFixture(t)
	modernFixtureDir, err := os.MkdirTemp(os.TempDir(), "gh-modern-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(modernFixtureDir) })
	modernEndpoint := startAccessEndpointAt(t, b.Handle, productionEndpointDirs(authorityContainer)["container"], modernFixtureDir)
	calls := 0
	env.Getenv = func(name string) string {
		if name != EndpointEnv {
			return ""
		}
		calls++
		if calls == 1 {
			return modernEndpoint
		}
		return legacyEndpoint
	}
	var rejected bytes.Buffer
	env.Stderr = &rejected
	_, code := ForwardAccess("ask", writeReq().Argv, env)
	if code != ExitUsage || calls != 2 {
		t.Fatalf("rotated access frame must reach and be refused by historical handler: exit=%d lookups=%d stderr=%q historical=%s", code, calls, rejected.String(), output.String())
	}
	after, err := os.ReadFile(filepath.Join(fixtureDir, "synthetic-write"))
	if err != nil || !bytes.Equal(before, after) || executed.Load() != 0 {
		t.Fatalf("rotated frame wrote: before=%q after=%q current executions=%d err=%v", before, after, executed.Load(), err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir, "done"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("historical broker assertion failed: %v\n%s", err, output.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("historical broker did not finish: %s", output.String())
	}
	requests, grants, err := b.store.List()
	if err != nil || len(requests) != 0 || len(grants) != 0 {
		t.Fatalf("modern broker unexpectedly filed or granted: requests=%+v grants=%+v err=%v", requests, grants, err)
	}
}

func startAccessEndpoint(t *testing.T, handler func(*hostservice.Session), dirName string) string {
	t.Helper()
	// The daemon socket path must not inherit t.TempDir's test-name prefix: that can
	// exceed Darwin's AF_UNIX sun_path even when TMPDIR itself is short.
	dir, err := os.MkdirTemp("/tmp", "ghb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return startAccessEndpointAt(t, handler, dirName, dir)
}

func startAccessEndpointAt(t *testing.T, handler func(*hostservice.Session), dirName, root string) string {
	t.Helper()
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	epDir := filepath.Join(root, dirName)
	if err := os.Mkdir(epDir, 0700); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(epDir, "github-broker.endpoint")
	sock := filepath.Join(root, "daemon.sock")
	stop := make(chan struct{})
	daemonDone, frontDone := make(chan error, 1), make(chan error, 1)
	go func() { daemonDone <- hostservice.ServeFrontedUnix(handler, sock, stop) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon socket unavailable")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { frontDone <- svcendpoint.ServeFront(endpoint, "127.0.0.1", sock, stop) }()
	t.Cleanup(func() { close(stop); <-daemonDone; <-frontDone })
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("front unavailable")
		}
		time.Sleep(time.Millisecond)
	}
	return endpoint
}

func TestForwardNormalizesStatusSeparatorBeforeLegacyDispatch(t *testing.T) {
	b, _ := windowFixture(t)
	env, _ := frontedEndpoint(t, b, productionEndpointDirs(authorityContainer)["container"])
	if got := b.ServeContext(context.Background(), writeReq(), paths.JailShortHash(b.authority.scope.Container), func([]byte) {}, func([]byte) {}); got != ExitTempFail {
		t.Fatal(got)
	}
	requests, _, err := b.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("requests=%+v err=%v", requests, err)
	}
	var stdout, stderr strings.Builder
	env.Stdout, env.Stderr = &stdout, &stderr
	for _, operation := range []string{"status", "wait"} {
		stdout.Reset()
		stderr.Reset()
		if got := Forward([]string{"--", operation, requests[0].ID}, env); got != ExitTempFail {
			t.Fatalf("normalized %s exit=%d stdout=%q stderr=%q", operation, got, stdout.String(), stderr.String())
		}
		if operation == "status" && !strings.Contains(stdout.String(), requests[0].ID) {
			t.Fatalf("normalized status omitted request %s: %q", requests[0].ID, stdout.String())
		}
		if operation == "wait" && strings.Contains(stderr.String(), "use `yolo gh") {
			t.Fatalf("normalized wait took the legacy argument path: %q", stderr.String())
		}
	}
}

func TestRefusedAccessPredictionHasSafeNextStep(t *testing.T) {
	for _, tc := range []struct{ rule, who, where, run string }{
		{"`gh auth token` is never brokered", "agent", "jail", "yolo gh -- api <endpoint>"},
		{"no read grant", "human", "host", "yolo allow github read"},
		{"unsupported command", "agent", "jail", "yolo gh --help"},
		{"required request notice, host authority, audit or executor is unavailable", "human", "host", "yolo check"},
		{"host gh is unavailable", "human", "host", "yolo check"},
	} {
		v := newPrediction(Request{Argv: []string{"auth", "token"}}, Decision{}, evaluation{Verdict: "refused", Rule: tc.rule})
		if len(v.Next) != 1 {
			t.Errorf("rule %q next=%+v", tc.rule, v.Next)
			continue
		}
		if v.Next[0].Who != tc.who || v.Next[0].Where != tc.where || v.Next[0].Run != tc.run {
			t.Errorf("rule %q next=%+v", tc.rule, v.Next)
		}
		if tc.run != "yolo check" {
			continue // Other pre-existing template commands contain placeholders, not this repair's run step.
		}
		cmd := exec.Command("bash", "-n")
		cmd.Stdin = strings.NewReader(v.Next[0].Run + "\n")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("rule %q next.run is invalid bash: %q: %s", tc.rule, v.Next[0].Run, output)
		}
	}
	b, _ := windowFixture(t)
	env, _ := frontedEndpoint(t, b, productionEndpointDirs(authorityContainer)["container"])
	body, code := ForwardAccess("evaluate", []string{"auth", "token"}, env)
	var got evaluation
	if err := json.Unmarshal(body, &got); err != nil || code != 0 || got.Verdict != "refused" || len(got.Next) == 0 || got.Next[0].Run != "yolo gh -- api <endpoint>" {
		t.Fatalf("refused prediction=%+v exit=%d err=%v body=%s", got, code, err, body)
	}
	b.execute = nil
	body, code = ForwardAccess("evaluate", writeReq().Argv, env)
	if err := json.Unmarshal(body, &got); err != nil || code != 0 || got.Verdict != "refused" || len(got.Next) != 1 || got.Next[0].Where != "host" || got.Next[0].Run != "yolo check" {
		t.Fatalf("missing-host-executor prediction=%+v exit=%d err=%v body=%s", got, code, err, body)
	}
	b.runner = nil
	body, code = ForwardAccess("evaluate", []string{"repo", "view", "o/r"}, env)
	if err := json.Unmarshal(body, &got); err != nil || code != 0 || got.Rule != "host gh is unavailable" || len(got.Next) != 1 || got.Next[0] != (accessNext{Who: "human", Where: "host", Run: "yolo check"}) {
		t.Fatalf("standing read with missing host gh prediction=%+v exit=%d err=%v body=%s", got, code, err, body)
	}
}

func TestForwardAccessAskStdinBindingThroughEndpoint(t *testing.T) {
	b, executed := windowFixture(t)
	env, _ := frontedEndpoint(t, b, productionEndpointDirs(authorityContainer)["container"])
	argv := []string{"api", "-X", "POST", "repos/o/r/issues/1", "--input", "-"}
	var ids []string
	for _, body := range []string{"first exact body", "different body"} {
		callEnv := env
		callEnv.Stdin = strings.NewReader(body)
		response, code := ForwardAccess("ask", argv, callEnv)
		if code != 0 {
			t.Fatalf("access ask exit=%d response=%s", code, response)
		}
		var got evaluation
		if err := json.Unmarshal(response, &got); err != nil || got.Schema != "yolo.access.prediction/1" || got.Verdict != "asks" || got.RequestID == "" {
			t.Fatalf("response=%s parsed=%+v err=%v", response, got, err)
		}
		ids = append(ids, got.RequestID)
	}
	if ids[0] == ids[1] {
		t.Fatalf("distinct stdin bodies joined the same request: %v", ids)
	}
	requests, _, err := b.store.List()
	if err != nil || len(requests) != 2 || executed.Load() != 0 {
		t.Fatalf("requests=%+v executed=%d err=%v", requests, executed.Load(), err)
	}
	for i, body := range []string{"first exact body", "different body"} {
		sum := sha256.Sum256([]byte(body))
		want := hex.EncodeToString(sum[:])
		found := false
		for _, q := range requests {
			if q.ID == ids[i] && q.StdinDigest == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("request %s did not bind exact stdin digest %s: %+v", ids[i], want, requests)
		}
	}
}
