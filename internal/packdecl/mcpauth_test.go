package packdecl

import (
	"strings"
	"testing"
)

const codexMCPAuth = `[[mcp_auth]]
store = ".codex/.credentials.json"
format = "json"
entries_at = []
companions = []
empty_store = "delete"
lock = { protocol = "flock", path = ".codex/mcp-oauth-locks/file-store.lock" }
fields = { access_token = "access_token", refresh_token = "refresh_token", expires_at = { path = "expires_at", unit = "ms" }, client_id = "client_id" }
rereads_before_refresh = true
login_hint = "codex mcp login {name}"
`

func TestMCPAuthGroupedKindDecodesIntoManifest(t *testing.T) {
	m, problems := DecodeTOML([]byte(codexMCPAuth))
	if len(problems) != 0 {
		t.Fatalf("well-formed mcp_auth declaration was refused: %v", problems)
	}
	if m.MCPAuth == nil {
		t.Fatal("Manifest.MCPAuth is nil")
	}
	got := m.MCPAuth
	if got.Store != ".codex/.credentials.json" || got.Format != "json" || got.EmptyStore != "delete" || !got.RereadsBeforeRefresh {
		t.Errorf("MCPAuth = %+v", got)
	}
	if len(got.EntriesAt) != 0 || got.Lock.Protocol != "flock" || got.Lock.Path != ".codex/mcp-oauth-locks/file-store.lock" || got.Fields.ExpiresAt.Path != "expires_at" || got.Fields.ExpiresAt.Unit != "ms" {
		t.Errorf("MCPAuth declaration lost nested fields: %+v", got)
	}
	if len(m.Contributions()) != 1 || m.Contributions()[0].Kind != KindMCPAuth {
		t.Errorf("grouped mcp_auth declaration was not represented as its closed grouped kind: %#v", m.Contributions())
	}
	fp, ok := FootprintOf(KindMCPAuth)
	if !ok || fp.Combine != CombineExclusive || fp.MayBeReviewWorthy || !strings.Contains(fp.Claims, "no runtime consumer in core") {
		t.Errorf("MCPAuth footprint = %+v, found=%v", fp, ok)
	}
}

func TestMCPAuthRejectsPerBinBinding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		decode func() []string
	}{
		{
			name: "grouped TOML",
			decode: func() []string {
				_, problems := DecodeTOML([]byte(strings.Replace(codexMCPAuth, "[[mcp_auth]]", "[[mcp_auth]]\nbin = \"codex\"", 1)))
				return problems
			},
		},
		{
			name: "legacy JSON",
			decode: func() []string {
				_, problems := Decode([]byte(`{"contributes":[{"kind":"mcp-auth","bin":"codex","store":".codex/.credentials.json","format":"json","empty_store":"delete","lock":{"protocol":"none"}}]}`))
				return problems
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := tc.decode()
			if !strings.Contains(strings.Join(problems, "\n"), `"bin"`) {
				t.Fatalf("mcp-auth bin binding was not refused: %v", problems)
			}
		})
	}
}

func TestMCPAuthRefusesMalformedDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"absolute store", `store = "/tmp/credentials.json"`, "store"},
		{"escaping store", `store = "../credentials.json"`, "store"},
		{"unknown format", `store = ".tool/auth.json"\nformat = "sqlite"`, "format"},
		{"unknown lock protocol", `store = ".tool/auth.json"\nformat = "json"\nempty_store = "delete"\nlock = { protocol = "fcntl" }`, "lock.protocol"},
		{"staleness on non-proper lock", `store = ".tool/auth.json"\nformat = "json"\nempty_store = "delete"\nlock = { protocol = "flock", path = ".tool/auth.lock", stale_seconds = 15 }`, "stale_seconds"},
		{"unsupported with lock", `store = ".tool/auth"\nformat = "unsupported"\nunsupported_reason = "not measured"\nlock = { protocol = "none" }`, "lock"},
		{"missing empty store policy", `store = ".tool/auth.json"\nformat = "json"\nlock = { protocol = "none" }`, "empty_store"},
		{"unsupported without reason", `store = ".tool/auth"\nformat = "unsupported"`, "unsupported_reason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := "[[mcp_auth]]\n" + strings.ReplaceAll(tc.body, `\n`, "\n") + "\n"
			_, problems := DecodeTOML([]byte(data))
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Fatalf("problems %v do not name %q", problems, tc.want)
			}
		})
	}
}

func TestMCPAuthRefusesTwoDeclarationsInOnePack(t *testing.T) {
	data := codexMCPAuth + "\n[[mcp_auth]]\nstore = \".other/auth.json\"\nformat = \"unsupported\"\nunsupported_reason = \"not measured\"\n"
	_, problems := DecodeTOML([]byte(data))
	if !strings.Contains(strings.Join(problems, "\n"), "mcp_auth") || !strings.Contains(strings.Join(problems, "\n"), "one") {
		t.Fatalf("a second declaration should be refused by name: %v", problems)
	}
}

func TestMCPAuthTolerantDecodeKeepsFirstDeclaration(t *testing.T) {
	data := `{"contributes":[
		{"kind":"mcp-auth","store":".first/auth.json","format":"json","empty_store":"delete","lock":{"protocol":"none"}},
		{"kind":"mcp-auth","store":".second/auth.json","format":"json","empty_store":"delete","lock":{"protocol":"none"}}
	]}`
	m, problems, skipped := DecodeTolerant([]byte(data))
	if len(problems) != 0 || len(skipped) != 1 || !strings.Contains(skipped[0], "second \"mcp-auth\"") {
		t.Fatalf("tolerant decode problems=%v skipped=%v", problems, skipped)
	}
	if len(m.Contributes) != 1 || m.MCPAuth == nil || m.MCPAuth.Store != ".first/auth.json" {
		t.Fatalf("tolerant decode kept an ambiguous declaration: %#v", m)
	}
}
