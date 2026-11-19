package run

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

func TestFrozenLaunchProducerAdviceThroughAuthenticatedStatus(t *testing.T) {
	packHome(t)
	manifest := `name="fixture-agent"
mcp_header_form="bare"
[native_sign_in]
formats=["claude-plugin"]
[[skills]]
agent="fixture-agent"
into=".fixture/skills"
`
	originals := fstest.MapFS{
		"fixture-agent/pack.toml": {Data: []byte(manifest)},
		"fixture-provision/pack.toml": {Data: []byte(`name="fixture-provision"
[[mcp_sign_in_bundle]]
bundle="original"
format="claude-plugin"
origin="https://example.invalid/fixture"
revision="0123456789abcdef0123456789abcdef01234567"
license="original/LICENSE"
`)},
		"fixture-provision/original/LICENSE":                    {Data: []byte("Synthetic fixture license")},
		"fixture-provision/original/.claude-plugin/plugin.json": {Data: []byte(`{"name":"fixture-original"}`)},
		"fixture-provision/original/.mcp.json":                  {Data: []byte(`{"fixture-key":{"type":"http","url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"fixture-client","callbackPort":31111}}}`)},
	}
	old := packload.EmbeddedFS()
	packload.SetEmbeddedFS(originals)
	t.Cleanup(func() { packload.SetEmbeddedFS(old) })
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pack.toml"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDirForUse(root, "fixture-agent")
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	pack.Official = true
	userAny, err := jsonx.Decode([]byte(`{"packs":["fixture-agent"],"mcp_servers":{"fixture-server":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once","oauth":{"client_id":"fixture-client"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	user := userAny.(*jsonx.OrderedMap)
	packs := []*packload.Pack{pack}
	frozen := frozenMCPForLaunch(user, user, packs, packs)
	options := &Options{launchInputMCP: frozen, Stderr: io.Discard}
	options.freezeDeliveredMCPSignIn(t.TempDir(), packs, nil)
	input := onceLaunchInput(t, frozen.EffectiveJSON, frozen.UserOnlyJSON)
	input.MCPSignIn = options.launchInputMCP.SignIn
	bytes, err := mcpShareFromPacks(input, packs)
	if err != nil {
		t.Fatal(err)
	}
	var share mcpauth.Share
	if err := json.Unmarshal(bytes, &share); err != nil {
		t.Fatal(err)
	}
	if share.SignIn == nil {
		t.Fatalf("verified producer lost sign-in packet: %s", bytes)
	}
	// Header helper form is deliberately bare: formats come from loadable
	// declarations, never the helper grammar.
	parent, err := os.MkdirTemp(testtmp.ShortDir(), "yj-status-producer-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(parent)
	frontDir := filepath.Join(parent, "private-session-not-container")
	if err := os.MkdirAll(frontDir, 0700); err != nil {
		t.Fatal(err)
	}
	upstream, endpoint := filepath.Join(parent, "upstream.sock"), filepath.Join(frontDir, "front.endpoint")
	stop, frontStop := make(chan struct{}), make(chan struct{})
	done, frontDone := make(chan struct{}), make(chan struct{})
	state := filepath.Join(t.TempDir(), "absent-state")
	handler := mcpauthdaemon.StatusHandlerForShare(state, share, mcpauthdaemon.BindShareSession(share, share.Jail, endpoint), nil)
	go func() { _ = hostservice.ServeFrontedUnix(handler, upstream, stop); close(done) }()
	statusProducerWaitPath(t, upstream)
	go func() { _ = svcendpoint.ServeFront(endpoint, "127.0.0.1", upstream, frontStop); close(frontDone) }()
	statusProducerWaitPath(t, endpoint)
	defer func() { close(frontStop); <-frontDone; close(stop); <-done }()
	conn, err := svcendpoint.DialLocal(endpoint, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if err := frameproto.WriteRequest(conn, []byte(`{"op":"status"}`)); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatal(err)
		}
		if frame.StreamID == frameproto.StreamStdout {
			output.Write(frame.Payload)
		}
		if frame.StreamID == frameproto.StreamExit {
			code, err := frameproto.ExitCode(frame.Payload)
			if err != nil || code != 0 {
				t.Fatal("authenticated status refused frozen share")
			}
			break
		}
	}
	if !strings.Contains(output.String(), "yolo pack add fixture-provision, then relaunch") || !strings.Contains(output.String(), "https://example.invalid/fixture") {
		t.Fatalf("producer→framed status lost advice: %s", output.String())
	}
	if _, err := os.Lstat(state); !os.IsNotExist(err) {
		t.Fatal("framed status created missing state")
	}
}

func statusProducerWaitPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("synthetic endpoint did not publish")
		case <-ticker.C:
		}
	}
}
