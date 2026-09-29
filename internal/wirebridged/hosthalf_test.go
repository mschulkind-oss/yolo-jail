package wirebridged

// hosthalf_test.go pins the bridge's HOST HALF (hosthalf.go; docs/design/host-notch-services.md):
// its inputs come from the launch's input file, a provider key from that input rather than a jail
// key file, the Codex route's view from the host broker socket, and it publishes no endpoint file.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// writeHostInput writes a launch's input file and returns the getenv a host half reads it by.
func writeHostInput(t *testing.T, env map[string]string) func(string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	data, _ := json.Marshal(launchservice.Input{Service: ServiceName, Env: env})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return func(k string) string {
		if k == launchservice.InputEnv {
			return path
		}
		return ""
	}
}

// readinessPipe points this daemon's readiness descriptor at a pipe and returns the first line
// written to it.
func readinessPipe(t *testing.T) <-chan string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.JailDaemonReadyFDEnv, strconv.Itoa(int(w.Fd())))
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })
	out := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(r).ReadString('\n')
		out <- strings.TrimSpace(line)
	}()
	return out
}

// The host half serves the adapter route its input selects, behind the caller token, with the
// provider key the input carries, reports ready on its pipe, and publishes no endpoint file: a
// jail-path file is one of the inputs the host half moved off. Deleting the hostHalf test in
// servePlan publishes the file and fails this.
func TestTheHostHalfServesFromItsInputAndPublishesNoFile(t *testing.T) {
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	t.Setenv("CEREBRAS_API_KEY", "")
	up := withUpstream(t)
	addr := freeLoopback(t)
	endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
	old := EndpointFile
	EndpointFile = endpointFile
	t.Cleanup(func() { EndpointFile = old })
	ready := readinessPipe(t)

	getenv := writeHostInput(t, map[string]string{
		"YOLO_PROVIDERS": strings.Replace(bridgedProviders, "127.0.0.1:8214", addr, 1),
		"YOLO_PROFILES":  `{"cerebras-fast":{"provider":"cerebras"}}`, "YOLO_USE_PROFILES": `{"claude":"cerebras-fast"}`,
		CallerTokenEnv:     testCallerToken,
		"CEREBRAS_API_KEY": "cb-from-the-launch",
	})
	e, why := hostHalfEnv(getenv, nil)
	if why != "" {
		t.Fatal(why)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runHostHalf(ctx, e) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case line := <-ready:
		if line != "ready wire-bridge" {
			t.Fatalf("readiness = %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the host half never reported ready")
	}
	if code, _ := sendAs(t, "http://"+addr+"/v1/messages", anthropicHi, nil); code != http.StatusUnauthorized {
		t.Errorf("a caller without the launch's token got %d, want 401", code)
	}
	code, body := sendAs(t, "http://"+addr+"/v1/messages", anthropicHi,
		map[string]string{"Authorization": "Bearer " + testCallerToken})
	if code != http.StatusOK {
		t.Fatalf("the agent's request got %d: %s", code, body)
	}
	up.mu.Lock()
	got := up.requests[0].Header.Get("Authorization")
	up.mu.Unlock()
	if got != "Bearer cb-from-the-launch" {
		t.Errorf("upstream Authorization = %q, want the key the launch's input carried", got)
	}
	if _, err := os.Stat(endpointFile); !os.IsNotExist(err) {
		t.Errorf("the host half published %s; it must publish no endpoint file", endpointFile)
	}
}

// A host half never reads a jail home's key files, even when one exists under HOME.
func TestTheHostHalfReadsNoKeyFile(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "")
	home := t.TempDir()
	writeKeyChannel(t, home, "export CEREBRAS_API_KEY=${CEREBRAS_API_KEY:-'from-a-jail-file'}")
	getenv := writeHostInput(t, map[string]string{"HOME": home, "JAIL_HOME": home})
	e, why := hostHalfEnv(getenv, nil)
	if why != "" {
		t.Fatal(why)
	}
	if key, _ := keyFor(e, "CEREBRAS_API_KEY", "claude"); key != "" {
		t.Errorf("the host half read %q from a key file; its key comes from the launch's input only", key)
	}
}

// A selection that routes nothing at the bridge is a launch that should not have started it: it
// answers failed on its pipe and exits, rather than idling for the launch's lifetime.
func TestAHostHalfWithNothingToServeFailsItsReadiness(t *testing.T) {
	ready := readinessPipe(t)
	e, why := hostHalfEnv(writeHostInput(t, map[string]string{CallerTokenEnv: testCallerToken}), nil)
	if why != "" {
		t.Fatal(why)
	}
	if rc := runHostHalf(context.Background(), e); rc == 0 {
		t.Error("a host half with nothing to serve exited 0")
	}
	if line := <-ready; !strings.HasPrefix(line, "failed wire-bridge nothing to serve") {
		t.Errorf("readiness = %q", line)
	}
}

// The Codex route's access-token view comes from the host broker's private socket the launch
// names, never from a jail endpoint (HS-D3).
func TestTheHostHalfCodexRouteTakesItsViewFromTheHostSocket(t *testing.T) {
	up := withUpstream(t)
	dir, err := os.MkdirTemp("/tmp", "yj-wbhost-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "broker.sock")
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		_ = hostservice.ServeUnix(func(s *hostservice.Session) {
			if action, _ := s.Get("action"); action != "token" {
				s.Exit(2)
				return
			}
			if view, _ := s.Get("view"); view != "access" {
				s.Exit(3)
				return
			}
			_ = s.JSON(map[string]any{"access_token": "host-access", "account_id": "acct",
				"expires_at": time.Now().Add(time.Hour).UnixMilli(), "generation": 1})
			s.Exit(0)
		}, socket, stop)
	}()
	waitFor(t, func() bool { _, err := os.Stat(socket); return err == nil })
	e, why := hostHalfEnv(writeHostInput(t, map[string]string{openauthclient.HostSocketEnv: socket}), nil)
	if why != "" {
		t.Fatal(why)
	}
	h, _, reason := adapterHandler(route{Agent: "claude", ProviderName: "openai-codex",
		UpstreamBaseURL: CodexResponsesBaseURL, CodexAccessToken: true}, e)
	if reason != "" {
		t.Fatal(reason)
	}
	up.responses = append(up.responses, func() *http.Response {
		return jsonResponse(200, `{"id":"r","model":"gpt","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	rec, _ := postMessage(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	up.mu.Lock()
	got := up.requests[0].Header.Get("Authorization")
	up.mu.Unlock()
	if got != "Bearer host-access" {
		t.Errorf("upstream Authorization = %q, want the host socket's view", got)
	}
}
