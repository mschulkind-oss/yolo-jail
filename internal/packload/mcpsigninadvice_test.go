package packload

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

func adviceOriginals() fstest.MapFS {
	out := fstest.MapFS{}
	for _, name := range []string{"z-provision", "a-provision"} {
		out[name+"/pack.toml"] = &fstest.MapFile{Data: []byte(`name="` + name + `"
[[mcp_sign_in_bundle]]
bundle="original"
format="claude-plugin"
origin="https://example.invalid/fixture"
revision="0123456789abcdef0123456789abcdef01234567"
license="original/LICENSE"
`)}
		out[name+"/original/LICENSE"] = &fstest.MapFile{Data: []byte("Synthetic license")}
		out[name+"/original/.claude-plugin/plugin.json"] = &fstest.MapFile{Data: []byte(`{"name":"fixture-original"}`)}
		out[name+"/original/.mcp.json"] = &fstest.MapFile{Data: []byte(`{"fixture-server":{"type":"http","url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"fixture-client","callbackPort":31111}}}`)}
	}
	return out
}

func TestProvisioningCandidatesOriginalOnlyExclusionMatrix(t *testing.T) {
	for _, tc := range []string{"matching", "URL", "transport", "client", "format", "selected", "alias", "shadow", "unreadable mapping", "missing context", "unknown context", "malformed declaration", "revision", "malformed bundle", "ambiguous key", "no embedded FS"} {
		t.Run(tc, func(t *testing.T) {
			originals := adviceOriginals()
			server := mcpsignin.Once{Name: "fixture-server", URL: "https://mcp.fixture.invalid/mcp", Transport: "http", ExpectedClientID: "fixture-client"}
			context := &mcpsignin.AdviceContext{Version: 1, MappingReadable: true, Formats: []string{"claude-plugin"}}
			switch tc {
			case "URL":
				server.URL = "https://other.fixture.invalid/mcp"
			case "transport":
				server.Transport = "sse"
			case "client":
				server.ExpectedClientID = "different-client"
			case "format":
				context.Formats = nil
			case "selected", "alias":
				context.SelectedEmbedded = []string{"a-provision", "z-provision"}
			case "shadow":
				context.ShadowNames = []string{"a-provision", "z-provision"}
			case "unreadable mapping":
				context.MappingReadable = false
			case "missing context":
				context = nil
			case "unknown context":
				context.Version = 99
			case "malformed declaration", "revision", "malformed bundle", "ambiguous key":
				for _, name := range []string{"a-provision", "z-provision"} {
					switch tc {
					case "malformed declaration":
						originals[name+"/pack.toml"].Data = []byte("not=toml[broken")
					case "revision":
						originals[name+"/pack.toml"].Data = []byte(strings.ReplaceAll(string(originals[name+"/pack.toml"].Data), "0123456789abcdef0123456789abcdef01234567", "not-a-revision"))
					case "malformed bundle":
						originals[name+"/original/.mcp.json"].Data = []byte("broken")
					case "ambiguous key":
						originals[name+"/original/.mcp.json"].Data = []byte(`{"x":{"url":"https://mcp.fixture.invalid/mcp"},"x":{"url":"https://mcp.fixture.invalid/mcp"}}`)
					}
				}
			case "no embedded FS":
				originals = nil
			}
			candidates := ProvisioningCandidates(originals, server, context)
			if tc != "matching" {
				if len(candidates) != 0 {
					t.Fatalf("excluded original became advice: %+v", candidates)
				}
				return
			}
			if len(candidates) != 2 || candidates[0].Pack != "a-provision" || candidates[1].Pack != "z-provision" {
				t.Fatalf("not all sorted candidates: %+v", candidates)
			}
			recovery := ProvisioningRecovery(server, candidates)
			if !strings.Contains(recovery, "yolo pack add a-provision; yolo pack add z-provision") || !strings.Contains(recovery, "any one serves") || !strings.Contains(recovery, "oauth.client_id <id> --user") {
				t.Fatal("recovery chose a candidate or omitted alternative")
			}
		})
	}
}
