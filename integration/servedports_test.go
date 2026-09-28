package integration

// servedports_test.go is the container tier of SERVED ADDRESSES (docs/plans/notch-convergence.md
// NC-D41 to NC-D44): on a jail that shares its launcher's network namespace, every jail daemon and
// pack service listens on a port the launch picked, so two jails on one loopback stop contending
// for the declared :1460, :1461 and :8214 to :8216.
//
// ⚠ REACHABILITY CARVE-OUT (AGENTS.md). Inside a development jail this suite is nested, and a
// nested podman is forced onto --net=host: the shared-namespace case under test, and the only one
// a nested run can see. The workspace below also declares `network.mode: "host"`, so on a
// host-run suite (CI's rootless job) the same test runs the jail on the host's own stack. That
// arm is verified only there, never by a nested run.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// servedURLProblem says why got is not where this suite's launch serves a daemon declared at
// declared (a loopback host:port) under path, or "". A jail with a network namespace of its own
// serves the declared address; one sharing its launcher's (shared) serves the same loopback host
// on a port the launch picked, which is never the declared one.
func servedURLProblem(got, declared, path string, shared bool) string {
	want := "http://" + declared + path
	if !shared {
		if got != want {
			return fmt.Sprintf("%q, want the declared %q", got, want)
		}
		return ""
	}
	u, err := url.Parse(got)
	host, _, _ := net.SplitHostPort(declared)
	if err != nil || u.Scheme != "http" || u.Hostname() != host || u.Path != path || u.Port() == "" {
		return fmt.Sprintf("%q, want http://%s:<picked port>%s", got, host, path)
	}
	if u.Host == declared {
		return fmt.Sprintf("%q is the declared address, which another jail on this loopback may "+
			"hold; a shared-namespace launch serves on a port it picked", got)
	}
	return ""
}

// portIn is the port of a loopback URL, or 0.
func portIn(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(u.Port())
	return n
}

var servedLine = regexp.MustCompile(`(?m)^(A|B)-(BASE|CODE|ADAPTER|ADAPTER_UP)=(.*)$`)

func servedValues(out, who string) map[string]string {
	got := map[string]string{}
	for _, m := range servedLine.FindAllStringSubmatch(out, -1) {
		if m[1] == who {
			got[m[2]] = strings.TrimSpace(m[3])
		}
	}
	return got
}

// TestTwoJailsOnOneLoopbackServeOnTheirOwnPorts launches two bridged jails at once on one shared
// loopback, the second while the first still runs, and has each drive its own wire bridge with
// its own caller token. Before served addresses the second jail's bridge found :8214 held and its
// launch refused (or, had it started, its claude reached the first jail's bridge and was refused
// 401 for the wrong token). Now each serves on its own picked port and answers 200, and each
// jail's OpenAI adapter listens on a port of its own too.
func TestTwoJailsOnOneLoopbackServeOnTheirOwnPorts(t *testing.T) {
	requireJail(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stubPort := ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-stub", "model": "qwen-3.8-27b",
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
				"message": map[string]string{"role": "assistant", "content": "ok"}}},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	packHome(t, fmt.Sprintf(`{
		"packs": ["claude", "cerebras"],
		"use_profiles": {"claude": "cerebras"},
		"env_sources": [{"CEREBRAS_API_KEY": "served-ports-integration-key"}],
		"providers": {"cerebras": {"endpoints": {"openai": {"base_url": "http://127.0.0.1:%d/v1"}}}}
	}`, stubPort))

	const release = "release-served-ports"
	session := func(who string) string {
		return `set -u
. ~/.config/yolo-agent-env/claude.sh
for i in $(seq 1 50); do (exec 3<>/dev/tcp/127.0.0.1/` + strconv.Itoa(stubPort) + `) 2>/dev/null && break; sleep 0.1; done
echo "` + who + `-BASE=$ANTHROPIC_BASE_URL"
code=$(curl -sS -o /dev/null -w '%{http_code}' "$ANTHROPIC_BASE_URL/v1/messages" \
  -H 'content-type: application/json' -H "authorization: Bearer $ANTHROPIC_AUTH_TOKEN" \
  -d '{"model":"qwen-3.8-27b","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}')
echo "` + who + `-CODE=$code"
adapter=$(printf '%s' "${YOLO_SERVED_ADDRESSES:-}" | jq -r '."127.0.0.1:1460" // empty' 2>/dev/null)
echo "` + who + `-ADAPTER=$adapter"
up=no
if [ -n "$adapter" ]; then
  for _ in $(seq 1 150); do (exec 3<>/dev/tcp/127.0.0.1/${adapter##*:}) 2>/dev/null && { up=yes; break; }; sleep 0.2; done
fi
echo "` + who + `-ADAPTER_UP=$up"
for _ in $(seq 1 1200); do [ -f /workspace/` + release + ` ] && break; sleep 0.5; done`
	}
	// Each workspace is its own jail; both declare the shared namespace (see the file header).
	dirA := writeProject(t, `{"network": {"mode": "host"}}`)
	dirB := writeProject(t, `{"network": {"mode": "host"}}`)
	releaseAll := func() {
		for _, d := range []string{dirA, dirB} {
			_ = os.WriteFile(filepath.Join(d, release), []byte("go\n"), 0o644)
		}
	}
	t.Cleanup(releaseAll)

	awaitSession := func(r *bgRun, who string) map[string]string {
		t.Helper()
		deadline := time.Now().Add(jailTimeout())
		for {
			if v := servedValues(r.combined(), who); v["ADAPTER_UP"] != "" {
				return v
			}
			select {
			case err := <-r.done:
				t.Fatalf("launch %s exited (%v) before its session reported:\n%s", who, err, r.combined())
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("launch %s's session did not report within %s:\n%s", who, jailTimeout(), r.combined())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	a := startYoloBackground(t, "A", dirA, session("A"))
	gotA := awaitSession(a, "A")
	// B starts while A's jail, and A's bridge and adapter, still hold their ports.
	b := startYoloBackground(t, "B", dirB, session("B"))
	gotB := awaitSession(b, "B")
	releaseAll()
	t.Logf("jail A: bridge %s, adapter %s; jail B: bridge %s, adapter %s",
		gotA["BASE"], gotA["ADAPTER"], gotB["BASE"], gotB["ADAPTER"])

	for who, got := range map[string]map[string]string{"A": gotA, "B": gotB} {
		if got["CODE"] != "200" {
			t.Errorf("jail %s's claude got HTTP %s from its bridge at %s, want 200 — it reached "+
				"no bridge, or the other jail's, which refuses its caller token", who, got["CODE"], got["BASE"])
		}
		if p := servedURLProblem(got["BASE"], "127.0.0.1:8214", "", true); p != "" {
			t.Errorf("jail %s's ANTHROPIC_BASE_URL is %s", who, p)
		}
		if got["ADAPTER"] == "" || got["ADAPTER"] == "127.0.0.1:1460" || got["ADAPTER_UP"] != "yes" {
			t.Errorf("jail %s's OpenAI adapter: served at %q, listening %s — want a picked port, "+
				"bound", who, got["ADAPTER"], got["ADAPTER_UP"])
		}
	}
	if portIn(gotA["BASE"]) == portIn(gotB["BASE"]) || gotA["ADAPTER"] == gotB["ADAPTER"] {
		t.Errorf("the two jails serve on the same port (bridges %s and %s, adapters %s and %s)",
			gotA["BASE"], gotB["BASE"], gotA["ADAPTER"], gotB["ADAPTER"])
	}
	for _, r := range []*bgRun{a, b} {
		if rc := r.wait(t, jailTimeout()); rc != 0 {
			t.Errorf("launch %s exited %d:\n%s", r.name, rc, r.combined())
		}
	}
}
