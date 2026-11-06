package config

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// MA-D11 (docs/design/mcp-auth-sharing.md): `mcp_servers.<name>.sign_in` takes "per_agent" or
// "once", only on a remote server.
func TestMCPSignInShape(t *testing.T) {
	for _, tc := range []struct {
		name, cfg, want string
	}{
		{"once", `{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`, ""},
		{"per agent", `{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"per_agent"}}}`, ""},
		{"bad value", `{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"always"}}}`, `config.mcp_servers.slack.sign_in: must be "per_agent" or "once"`},
		{"not a string", `{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":true}}}`, `config.mcp_servers.slack.sign_in: must be "per_agent" or "once"`},
		{"local server", `{"mcp_servers":{"fs":{"command":"/bin/true","sign_in":"once"}}}`, `config.mcp_servers.fs.sign_in: applies only to a remote server (url)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var errs []string
			validateMCPServers(decode(t, tc.cfg), &errs)
			joined := strings.Join(errs, "\n")
			if tc.want == "" && joined != "" {
				t.Fatalf("errors = %v, want none", errs)
			}
			if tc.want != "" && !strings.Contains(joined, tc.want) {
				t.Fatalf("errors = %v, want one containing %q", errs, tc.want)
			}
		})
	}
}

// A workspace `sign_in` is refused naming the user-scope verb (MA-D11): a project must not be
// able to move a user's sign-in into the shared tier.
func TestMCPSignInInAWorkspaceIsRefusedNamingTheVerb(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`)
	errs, _ := ValidateConfig(decode(t, `{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`), ws, nil)
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "yolo config set mcp_servers.slack.sign_in once --user") {
		t.Fatalf("errors = %v, want the refusal naming the user-scope verb", errs)
	}
}

func TestMCPSignInInTheUserConfigIsQuiet(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(ws)
	errs, _ := ValidateConfig(decode(t, `{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`), ws, nil)
	for _, e := range errs {
		if strings.Contains(e, "sign_in") {
			t.Fatalf("user-scope sign_in refused: %v", errs)
		}
	}
}

// The TOML walk refuses the key in a workspace file with the same advice.
func TestMCPSignInInAWorkspaceTOMLIsRefusedNamingTheVerb(t *testing.T) {
	values := decode(t, `{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`)
	v := firstReadableTOMLViolation(values, SchemaScopeWorkspaceOnly)
	if v == nil || !strings.Contains(v.message, "yolo config set mcp_servers.slack.sign_in once --user") {
		t.Fatalf("violation = %+v, want the refusal naming the user-scope verb", v)
	}
	if v := firstReadableTOMLViolation(values, SchemaScopeUserOnly); v != nil {
		t.Fatalf("user TOML refused sign_in: %+v", v)
	}
}

// A pack's MCP contribution may not carry sign_in: it is the user's own decision (MA-D11), so
// the pack entry keys are the config keys less the user-only ones.
func TestTheMCPKindCarriesEveryMCPServerKeyButTheUserOnlyOnes(t *testing.T) {
	var want []string
	for k := range knownMCPServerKeys {
		if _, userOnly := userOnlyMCPServerKeys[k]; !userOnly {
			want = append(want, k)
		}
	}
	sort.Strings(want)
	if got := packdecl.MCPEntryKeys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("packdecl's mcp entry keys %v differ from mcp_servers' %v", got, want)
	}
	if _, ok := knownMCPServerKeys["sign_in"]; !ok {
		t.Error("sign_in is not a known mcp_servers key")
	}
}
