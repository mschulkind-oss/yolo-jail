package awsauthdaemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// handler_test.go drives the handler over the REAL host-to-host transport
// (hostservice.ServeUnix plus a frameproto client), because Session cannot be
// constructed from outside internal/hostservice. Nothing here runs `aws`: the Broker's
// Minter carries a canned Runner.

// shortTempDir is os.MkdirTemp under /tmp rather than t.TempDir().
//
// AF_UNIX sun_path is 104 bytes on darwin, and t.TempDir() under a long TMPDIR
// produces a path that overruns it — the darwin-only class AGENTS.md says to
// reproduce on Linux with a long TMPDIR. A short, explicit /tmp dir is the fix at the
// point the path is MINTED.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-aa-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type reply struct {
	stdout string
	stderr string
	rc     int
}

func (r reply) decode(t *testing.T) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &body); err != nil {
		t.Fatalf("stdout is not JSON (%q): %v", r.stdout, err)
	}
	return body
}

// serveHandler starts the handler on a private AF_UNIX socket and returns a
// request function.
func serveHandler(t *testing.T, cfg HandlerConfig) func(request map[string]any) reply {
	t.Helper()
	// The access log is the package-global Logger; a leaked write from one test into
	// another's buffer is a measured flake in this repo, so silence it per test.
	saved := hostservice.Logger
	hostservice.Logger = log.New(io.Discard, "", 0)
	socket := filepath.Join(shortTempDir(t), "d.sock")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = hostservice.ServeUnix(BuildHandler(cfg), socket, stop)
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
		hostservice.Logger = saved
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the handler never bound its socket")
		}
		time.Sleep(2 * time.Millisecond)
	}
	return func(request map[string]any) reply {
		t.Helper()
		conn, err := dialUnix(socket)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := frameproto.WriteRequest(conn, body); err != nil {
			t.Fatal(err)
		}
		var out reply
		out.rc = -1
		for {
			frame, err := frameproto.ReadFrame(conn)
			if err != nil {
				return out
			}
			switch frame.StreamID {
			case frameproto.StreamStdout:
				out.stdout += string(frame.Payload)
			case frameproto.StreamStderr:
				out.stderr += string(frame.Payload)
			case frameproto.StreamExit:
				rc, err := frameproto.ExitCode(frame.Payload)
				if err != nil {
					t.Fatal(err)
				}
				out.rc = rc
				return out
			}
		}
	}
}

func testHandlerBroker(t *testing.T, cfg awsauth.Config, run awsauth.Runner) awsauth.Broker {
	t.Helper()
	dir := t.TempDir()
	return awsauth.Broker{
		StatePath: filepath.Join(dir, awsauth.StateFileName),
		LockPath:  filepath.Join(dir, awsauth.LockFileName),
		Config:    cfg,
		Minter:    awsauth.Minter{Run: run, Binary: "aws"},
	}
}

func cannedRunner(out awsauth.Output, calls *atomic.Int32) awsauth.Runner {
	return func(_ context.Context, _ []string) awsauth.Output {
		if calls != nil {
			calls.Add(1)
		}
		return out
	}
}

func processOutput(expires time.Time, keyID string) awsauth.Output {
	return awsauth.Output{Spawned: true, Stdout: `{"Version":1,"AccessKeyId":"` + keyID +
		`","SecretAccessKey":"zzsecretzz","SessionToken":"zztokenzz","Expiration":"` +
		expires.UTC().Format(time.RFC3339) + `"}`}
}

// assumeOutput is `aws sts assume-role --output json` output — the shape the N2/N3
// arms parse, which is NOT the `--format process` shape above.
func assumeOutput(expires time.Time, keyID string) awsauth.Output {
	return awsauth.Output{Spawned: true, Stdout: `{"Credentials":{"AccessKeyId":"` + keyID +
		`","SecretAccessKey":"zzsecretzz","SessionToken":"zztokenzz","Expiration":"` +
		expires.UTC().Format(time.RFC3339) + `"},"AssumedRoleUser":{"Arn":"arn:x"}}`}
}

func unnarrowed(profile string) awsauth.Config {
	return awsauth.Config{Profile: profile, Narrowing: awsauth.Narrowing{Kind: awsauth.NarrowNone}}
}

func dialUnix(path string) (net.Conn, error) { return net.Dial("unix", path) }

// TestTheDefaultActionAnswersTheContainerCredentialsBody: the handler's JSON IS the
// protocol body, which is what keeps the SessionToken -> Token rename to one place.
func TestTheDefaultActionAnswersTheContainerCredentialsBody(t *testing.T) {
	var calls atomic.Int32
	broker := testHandlerBroker(t, unnarrowed("bedrock"),
		cannedRunner(processOutput(time.Now().Add(time.Hour), "ASIA1"), &calls))
	do := serveHandler(t, HandlerConfig{Broker: broker})

	// No action at all defaults to "credentials": the adapter sends a bare request.
	got := do(map[string]any{})
	if got.rc != 0 {
		t.Fatalf("rc = %d, stderr = %q", got.rc, got.stderr)
	}
	body := got.decode(t)
	if len(body) != 4 {
		t.Errorf("body has %d keys, want 4: %v", len(body), body)
	}
	for _, key := range []string{"AccessKeyId", "SecretAccessKey", "Token", "Expiration"} {
		if _, ok := body[key]; !ok {
			t.Errorf("body has no %q: %v", key, body)
		}
	}
	if _, wrong := body["SessionToken"]; wrong {
		t.Error("the body carries `aws`'s SessionToken spelling")
	}
	// The second request is served from the cache: two agents in one jail, one mint.
	if second := do(map[string]any{"action": "credentials"}); second.rc != 0 {
		t.Fatalf("second rc = %d, stderr = %q", second.rc, second.stderr)
	}
	if calls.Load() != 1 {
		t.Errorf("minted %d times across two requests, want 1", calls.Load())
	}
}

// TestALapsedSessionIsA4xxNamingTheLoginCommand is OQ-SSO6 at the wire: the jail
// gets a Code and a Message, the Message carries the command, and nothing waits.
func TestALapsedSessionIsA4xxNamingTheLoginCommand(t *testing.T) {
	broker := testHandlerBroker(t, unnarrowed("bedrock"),
		cannedRunner(awsauth.Output{Spawned: true, Code: 255,
			Stderr: "Error when retrieving token from sso: Token has expired and refresh failed"}, nil))
	do := serveHandler(t, HandlerConfig{Broker: broker})
	got := do(map[string]any{"action": "credentials"})
	if got.rc == 0 {
		t.Fatalf("a lapsed session returned rc 0: %q", got.stdout)
	}
	body := got.decode(t)
	if len(body) != 2 {
		t.Errorf("4xx body has %d keys, want exactly Code and Message: %v", len(body), body)
	}
	message, _ := body["Message"].(string)
	if !strings.Contains(message, "aws sso login --profile bedrock") {
		t.Errorf("Message does not carry the login command verbatim: %q", message)
	}
	if body["Code"] != "ExpiredToken" {
		t.Errorf("Code = %v, want ExpiredToken", body["Code"])
	}
	// The stderr stream carries the same fact, for the daemon's own log.
	if !strings.Contains(got.stderr, "ExpiredToken") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

// TestStatusIsFingerprintOnlyOverTheWire: `status` is answerable from a jail only
// because there is no field a credential could travel in.
func TestStatusIsFingerprintOnlyOverTheWire(t *testing.T) {
	broker := testHandlerBroker(t, unnarrowed("bedrock"),
		cannedRunner(processOutput(time.Now().Add(time.Hour), "ASIASECRET"), nil))
	do := serveHandler(t, HandlerConfig{Broker: broker})
	if got := do(map[string]any{"action": "credentials"}); got.rc != 0 {
		t.Fatal(got.stderr)
	}
	got := do(map[string]any{"action": "status"})
	if got.rc != 0 {
		t.Fatalf("rc = %d, stderr = %q", got.rc, got.stderr)
	}
	for _, secret := range []string{"ASIASECRET", "zzsecretzz", "zztokenzz"} {
		if strings.Contains(got.stdout, secret) {
			t.Errorf("status carries %q: %s", secret, got.stdout)
		}
	}
	body := got.decode(t)
	if body["fingerprint"] != awsauth.Fingerprint("ASIASECRET") {
		t.Errorf("fingerprint = %v", body["fingerprint"])
	}
	if body["narrowing"] != string(awsauth.NarrowNone) {
		t.Errorf("narrowing = %v", body["narrowing"])
	}
}

// TestARequestCannotNameTheProfileRoleOrPolicy is OQ-SSO4 at the wire: those three
// are user-config only, so no request field may influence them. The workspace config
// is jail-writable and an agent that could name a profile could name `admin`.
func TestARequestCannotNameTheProfileRoleOrPolicy(t *testing.T) {
	var calls atomic.Int32
	broker := testHandlerBroker(t, unnarrowed("configured"),
		cannedRunner(processOutput(time.Now().Add(time.Hour), "ASIA1"), &calls))
	var seenArgv [][]string
	broker.Minter.Run = func(_ context.Context, argv []string) awsauth.Output {
		seenArgv = append(seenArgv, argv)
		calls.Add(1)
		return processOutput(time.Now().Add(time.Hour), "ASIA1")
	}
	do := serveHandler(t, HandlerConfig{Broker: broker})
	got := do(map[string]any{
		"action": "credentials", "profile": "admin",
		"role_arn": "arn:aws:iam::1:role/admin", "session_policy": `{"Statement":[]}`,
		"unnarrowed": true,
	})
	if got.rc != 0 {
		t.Fatalf("rc = %d, stderr = %q", got.rc, got.stderr)
	}
	if len(seenArgv) != 1 {
		t.Fatalf("ran %d commands", len(seenArgv))
	}
	joined := strings.Join(seenArgv[0], " ")
	if !strings.Contains(joined, "--profile configured") {
		t.Errorf("argv did not use the configured profile: %s", joined)
	}
	if strings.Contains(joined, "admin") || strings.Contains(joined, "assume-role") {
		t.Errorf("a request field reached the argv: %s", joined)
	}
}

func TestPingAnswersWithoutMinting(t *testing.T) {
	var calls atomic.Int32
	broker := testHandlerBroker(t, unnarrowed("p"),
		cannedRunner(processOutput(time.Now().Add(time.Hour), "ASIA1"), &calls))
	do := serveHandler(t, HandlerConfig{Broker: broker})
	got := do(map[string]any{"action": "ping"})
	if got.rc != 0 || got.decode(t)["pong"] != true {
		t.Errorf("ping = %+v", got)
	}
	if calls.Load() != 0 {
		t.Errorf("ping minted %d times", calls.Load())
	}
}

func TestAnUnknownActionIsRefusedRatherThanDefaulted(t *testing.T) {
	broker := testHandlerBroker(t, unnarrowed("p"),
		cannedRunner(processOutput(time.Now().Add(time.Hour), "ASIA1"), nil))
	do := serveHandler(t, HandlerConfig{Broker: broker})
	got := do(map[string]any{"action": "login"})
	if got.rc != 2 {
		t.Errorf("rc = %d, want 2 — an unknown action must not fall through to credentials", got.rc)
	}
	if !strings.Contains(got.stderr, "unknown action") {
		t.Errorf("stderr = %q", got.stderr)
	}
}
