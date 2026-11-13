package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
)

// The jail half's exchange: one framed {"op":"header","server":…} request, the broker's stdout
// passed through untouched (it is the header value pi sends), its stderr too, and its exit status
// returned. A fake broker on a net.Pipe stands in for the daemon (mcpauthdaemon's own tests run
// the real handler behind a real front).
func TestMCPAuthHeaderExchangePassesTheBrokersAnswerThrough(t *testing.T) {
	for _, tc := range []struct {
		name           string
		stdout, stderr string
		rc             int
	}{
		{"token", "Bearer at-2\n", "", 0},
		{"refusal", "", "mcp-auth: slack is not signed in yet\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			asked := make(chan map[string]string, 1)
			go func() {
				defer server.Close()
				body, err := frameproto.ReadRequestBytes(server)
				if err != nil {
					return
				}
				var req map[string]string
				_ = json.Unmarshal(body, &req)
				asked <- req
				if tc.stdout != "" {
					_, _ = frameproto.WriteFrame(server, frameproto.StreamStdout, []byte(tc.stdout))
				}
				if tc.stderr != "" {
					_, _ = frameproto.WriteFrame(server, frameproto.StreamStderr, []byte(tc.stderr))
				}
				_, _ = frameproto.WriteExit(server, tc.rc)
			}()
			var stdout, stderr strings.Builder
			rc := mcpAuthExchange(client, "slack", &stdout, &stderr)
			if req := <-asked; req["op"] != "header" || req["server"] != "slack" {
				t.Fatalf("request = %v", req)
			}
			if rc != tc.rc || stdout.String() != tc.stdout || stderr.String() != tc.stderr {
				t.Fatalf("rc=%d stdout=%q stderr=%q", rc, stdout.String(), stderr.String())
			}
		})
	}
}

// In a jail with no broker endpoint, the verb says so and names the way to one; it never falls
// back to the host's store, which a jail cannot read.
func TestMCPAuthHeaderInAJailWithNoBrokerSaysSo(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	called := false
	prev := mcpAuthHostHeader
	t.Cleanup(func() { mcpAuthHostHeader = prev })
	mcpAuthHostHeader = func(context.Context, string, func(string)) (string, error) { called = true; return "", nil }
	var stdout, stderr strings.Builder
	rc := mcpAuthMain([]string{"header", "slack"}, func(string) string { return "" }, &stdout, &stderr)
	if rc != 1 || stdout.String() != "" || !strings.Contains(stderr.String(), mcpAuthEndpointEnv) || !strings.Contains(stderr.String(), "yolo pack add mcp-auth") {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, stdout.String(), stderr.String())
	}
	if called {
		t.Fatal("a jail read the host's store")
	}
}

// The host answer composes the user's own config the way `yolo host` does: a sign_in = "once"
// server it declares is served (here: not signed in yet, naming Claude's sign-in, because the
// selected claude pack's store in the real home is a source), and a server it does not declare
// is refused with exit 2. No authorization server is reached: nothing is signed in.
func TestMCPAuthHostHeaderComposesTheUsersOwnConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the real home's store is a source on Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	writeUserConfig(t, `{"packs":["claude"],"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`)
	_, err := mcpAuthHostHeader(context.Background(), "slack", nil)
	var refusal *mcpauthdaemon.HeaderRefusal
	if !errors.As(err, &refusal) || refusal.Code != 1 || !strings.Contains(refusal.Message, "not signed in") ||
		!strings.Contains(refusal.Message, "run /mcp in claude and authenticate slack") {
		t.Fatalf("slack: %v", err)
	}
	_, err = mcpAuthHostHeader(context.Background(), "linear", nil)
	if !errors.As(err, &refusal) || refusal.Code != 2 {
		t.Fatalf("linear: %v", err)
	}
}

// Through Main, as pi runs it at `yolo host`: the routed verb prints ONLY the header value (no
// banner, no update notice), and a refusal keeps its exit status. Deleting the early route or the
// host answer fails here.
func TestMCPAuthHeaderThroughMainPrintsOnlyTheHeaderValue(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	prev := mcpAuthHostHeader
	t.Cleanup(func() { mcpAuthHostHeader = prev })
	mcpAuthHostHeader = func(_ context.Context, server string, _ func(string)) (string, error) {
		if server != "slack" {
			return "", &mcpauthdaemon.HeaderRefusal{Code: 2, Message: "mcp-auth: not served"}
		}
		return "at-9", nil
	}
	rc, out, errs := captureStd(t, func() int { return Main([]string{"yolo", "mcp-auth", "header", "slack"}) })
	if rc != 0 || out != "Bearer at-9\n" || errs != "" {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	rc, out, errs = captureStd(t, func() int { return Main([]string{"yolo", "mcp-auth", "header", "linear"}) })
	if rc != 2 || out != "" || !strings.Contains(errs, "not served") {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	rc, _, errs = captureStd(t, func() int { return Main([]string{"yolo", "mcp-auth", "status"}) })
	if rc != 2 || !strings.Contains(errs, "yolo mcp-auth --help") {
		t.Fatalf("a usage error: rc=%d stderr=%q", rc, errs)
	}
}
