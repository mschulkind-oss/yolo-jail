package mcpauthdaemon

import (
	"io/fs"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

type adviceReadSpy struct {
	fs.FS
	reads atomic.Int64
}

func (s *adviceReadSpy) Open(name string) (fs.File, error) {
	s.reads.Add(1)
	return s.FS.Open(name)
}

func daemonAdviceOriginals() fstest.MapFS {
	return fstest.MapFS{
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
}

func TestStatusHandlerDerivesAdviceOnlyForNoSignInPoint(t *testing.T) {
	for _, mode := range []string{"no point", "placed", "not capturable", "client", "selected", "shadow", "missing context", "unknown context", "not once", "live", "requested other"} {
		t.Run(mode, func(t *testing.T) {
			f := newOnceDaemonFixture(t)
			spy := &adviceReadSpy{FS: daemonAdviceOriginals()}
			old := packload.EmbeddedFS()
			packload.SetEmbeddedFS(spy)
			defer packload.SetEmbeddedFS(old)
			originalSources := f.b.share.Sources
			originalServer := f.b.share.Once[0]
			f.b.share.Sources = nil
			context := &mcpsignin.AdviceContext{Version: 1, MappingReadable: true, Formats: []string{"claude-plugin"}}
			f.b.share.Once[0].Name = "fixture-server"
			f.b.share.Once[0].URL = "https://mcp.fixture.invalid/mcp"
			f.b.share.Once[0].ExpectedClientID = "fixture-client"
			f.b.share.SignIn = &mcpsignin.Launch{Version: 1, Advice: context}
			switch mode {
			case "placed", "not capturable":
				f.b.share.SignIn.Decisions = []mcpsignin.Decision{{Server: "fixture-server", Agent: "fixture-agent", Capturable: mode == "placed", Point: &mcpsignin.Point{NativeName: "plugin:delivered:fixture-key"}}}
			case "client":
				f.b.share.Once[0].ExpectedClientID = "different"
			case "selected":
				context.SelectedEmbedded = []string{"fixture-provision"}
			case "shadow":
				context.ShadowNames = []string{"fixture-provision"}
			case "missing context":
				f.b.share.SignIn.Advice = nil
			case "unknown context":
				context.Version = 99
			case "not once":
				f.b.share.Once = nil
			case "live":
				f.b.share.Once[0] = originalServer
				f.b.share.Sources = originalSources
				adoptHeaderFixture(t, f)
			}
			request := map[string]any{"op": "status"}
			if mode == "requested other" {
				request["server"] = "unconfigured-other"
			}
			reply := askBroker(t, f.b, request)
			if reply.rc != 0 {
				t.Fatalf("status: %+v", reply)
			}
			if mode == "no point" {
				if spy.reads.Load() == 0 || !strings.Contains(reply.stdout, "yolo pack add fixture-provision, then relaunch") || !strings.Contains(reply.stdout, "0123456789abcdef0123456789abcdef01234567") {
					t.Fatal("framed status lost immutable advice")
				}
			} else if strings.Contains(reply.stdout, "yolo pack add fixture-provision") {
				t.Fatal("excluded candidate advised")
			}
			if mode == "placed" || mode == "not capturable" || mode == "not once" || mode == "live" || mode == "requested other" {
				if spy.reads.Load() != 0 {
					t.Fatal("other verdict read embedded originals")
				}
			}
			for _, field := range []string{"fixture-original", "callback_port", "fixture-key"} {
				if mode == "no point" && strings.Contains(reply.stdout, field) {
					t.Fatal("candidate promoted to authority/native point")
				}
			}
		})
	}
}
