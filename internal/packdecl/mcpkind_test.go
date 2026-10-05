package packdecl

// mcpkind_test.go pins the `mcp` contribution kind's declaration (kinds.go KindMCP;
// docs/design/mcp-presets-removal.md OQ-MP3): what it decodes to, what it refuses on both decode
// paths, its combine rule, and the one-pack duplicate refusal.

import (
	"encoding/json"
	"strings"
	"testing"
)

func mcpManifest(contribs ...string) []byte {
	return []byte(`{"name":"mcppack","contributes":[` + strings.Join(contribs, ",") + `]}`)
}

const mcpOK = `{"kind":"mcp","name":"browser","bin":"browser-mcp",` +
	`"config":{"command":"/bin/sh","args":["~/.local/share/x/wrapper","--flag"],` +
	`"env":{"A_VAR":"literal ${NOT_EXPANDED}"},"requires_env":["TOKEN"],"provides":"web_browsing"}}`

func TestTheMCPKindDecodesToItsEntry(t *testing.T) {
	m, problems := Decode(mcpManifest(mcpOK))
	if len(problems) > 0 {
		t.Fatalf("a well-formed mcp contribution was refused: %v", problems)
	}
	got := m.MCPContributions()
	if len(got) != 1 {
		t.Fatalf("MCPContributions() = %d entries, want 1", len(got))
	}
	if got[0].Name != "browser" || got[0].Bin != "browser-mcp" {
		t.Errorf("name/bin = %q/%q", got[0].Name, got[0].Bin)
	}
	var entry map[string]any
	if err := json.Unmarshal(got[0].Entry, &entry); err != nil {
		t.Fatalf("the entry is not JSON: %v", err)
	}
	if entry["command"] != "/bin/sh" || entry["provides"] != "web_browsing" {
		t.Errorf("entry = %v: the declared fields did not survive", entry)
	}
	// The accessor hands back a copy: editing it reaches no manifest.
	got[0].Entry[0] = 'X'
	if again := m.MCPContributions(); again[0].Entry[0] != '{' {
		t.Error("MCPContributions shares its bytes with the manifest")
	}
	if fp, ok := FootprintOf(KindMCP); !ok || fp.Combine != CombineExclusive || fp.MayBeReviewWorthy {
		t.Errorf("KindMCP footprint = %+v: exclusive by server name, never review-worthy (OQ-MP3)", fp)
	}
}

// Every refusal on BOTH decode paths: a malformed entry is one both ends of the version boundary
// understand, so the jail refuses what the author is told.
func TestTheMCPKindRefusesAMalformedEntry(t *testing.T) {
	for _, tc := range []struct {
		name, contrib, want string
	}{
		{"no name", `{"kind":"mcp","config":{"command":"x"}}`, `needs "name"`},
		{"spaced name", `{"kind":"mcp","name":"a b","config":{"command":"x"}}`, "no whitespace"},
		{"no config", `{"kind":"mcp","name":"a"}`, `needs "config"`},
		{"config not an object", `{"kind":"mcp","name":"a","config":["x"]}`, "must be an object"},
		{"unknown key", `{"kind":"mcp","name":"a","config":{"command":"x","url":"http://h"}}`,
			`"url", which no mcp_servers entry takes`},
		{"no command", `{"kind":"mcp","name":"a","config":{"args":["x"]}}`, `needs "command"`},
		{"empty command", `{"kind":"mcp","name":"a","config":{"command":" "}}`, "non-empty string"},
		{"args not strings", `{"kind":"mcp","name":"a","config":{"command":"x","args":[1]}}`,
			"list of strings"},
		{"env not strings", `{"kind":"mcp","name":"a","config":{"command":"x","env":{"A":1}}}`,
			"string values"},
		{"env bad name", `{"kind":"mcp","name":"a","config":{"command":"x","env":{"1A":"v"}}}`,
			"not a variable name"},
		{"requires_env bad name", `{"kind":"mcp","name":"a","config":{"command":"x","requires_env":["a-b"]}}`,
			"not a variable name"},
		{"empty provides", `{"kind":"mcp","name":"a","config":{"command":"x","provides":""}}`,
			"non-empty string"},
		{"bare tilde", `{"kind":"mcp","name":"a","config":{"command":"~"}}`, "names the home itself"},
		{"tilde slash alone", `{"kind":"mcp","name":"a","config":{"command":"/bin/sh","args":["~/"]}}`,
			"names the home itself"},
		{"escaping the home", `{"kind":"mcp","name":"a","config":{"command":"/bin/sh","args":["~/../etc/x"]}}`,
			`must not contain ".."`},
		{"bin with a slash", `{"kind":"mcp","name":"a","bin":"../x","config":{"command":"x"}}`,
			"bare program name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, strict := Decode(mcpManifest(tc.contrib))
			if !anyContains(strict, tc.want) {
				t.Errorf("strict decode problems %v lack %q", strict, tc.want)
			}
			_, tolerant, skipped := DecodeTolerant(mcpManifest(tc.contrib))
			if !anyContains(tolerant, tc.want) {
				t.Errorf("tolerant decode problems %v (skipped %v) lack %q", tolerant, skipped, tc.want)
			}
		})
	}
}

// One pack declaring one server name twice is refused on the authoring path: the second entry
// would land on the first's key.
func TestTheMCPKindRefusesOneNameTwiceInAPack(t *testing.T) {
	_, problems := Decode(mcpManifest(mcpOK, `{"kind":"mcp","name":"browser","config":{"command":"y"}}`))
	if !anyContains(problems, `MCP server "browser" is already declared at contributes[0]`) {
		t.Errorf("a duplicated server name was not refused: %v", problems)
	}
	if _, problems := Decode(mcpManifest(mcpOK, `{"kind":"mcp","name":"other","config":{"command":"y"}}`)); len(problems) > 0 {
		t.Errorf("two servers of two names were refused: %v", problems)
	}
}

// The kind's entry field set is the one place the schema could say less, or more, than a user's
// own mcp_servers entry; mcpEntryKeys is sorted so the error message lists it stably.
func TestTheMCPEntryKeysAreSorted(t *testing.T) {
	keys := MCPEntryKeys()
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Fatalf("MCPEntryKeys() not sorted: %v", keys)
		}
	}
}

func anyContains(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}
