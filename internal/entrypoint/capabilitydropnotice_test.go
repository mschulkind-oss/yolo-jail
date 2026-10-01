package entrypoint

// capabilitydropnotice_test.go pins what the jail boot's drop notice (noteDroppedManagedEntries)
// says about an MCP server that capability-driven MCP delivery withheld: one that IS declared
// under `mcp_servers`, with a `provides` the agent's active source performs itself, so the derive
// boundary (luahook's eligibleMCPServers) never handed it to the derive.
//
// The defect, MEASURED 2026-10-01 in a nested jail (docs/reference/mcp-configuration.md, "A launch
// with a provides server"): a launch on a source without the capability wrote the server into
// ~/.claude.json, and the next launch on claude's own login dropped it and said
//
//	claude/config: dropping from mcpServers (not in config): probe-search — … to keep it,
//	declare it in … (an MCP server under `mcp_servers`, …)
//
// The server was under `mcp_servers` already, so following the notice changed nothing.
//
// Every test here drives the boot loop (ConfigurePackSurfaces) over the shipped claude and kilo
// packs and the wire tables the launcher composes (capabilityJailVars), in one home booted twice,
// the sequence the measurement ran. Deleting the record of what the boundary withheld, or the
// notice's read of it, fails them.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// probeSearch is the measured server: it claims web_search and needs no credential.
func probeSearch() map[string]any {
	return map[string]any{"command": "/bin/true", "args": []any{"search"}, "provides": "web_search"}
}

// bootTwice boots one jail home on profile first ("" for the agent's own login) and then on
// second, and returns the second boot's stderr and the claude mcpServers it rendered. extra is
// written into ~/.claude.json's mcpServers between the boots, the way an agent's own `mcp add`
// would.
func bootTwice(t *testing.T, packs []*packload.Pack, servers map[string]any, first, second string,
	extra map[string]any) (string, map[string]any) {
	t.Helper()
	home, ws := t.TempDir(), t.TempDir()
	boot := func(profile string) (*Env, string) {
		use := map[string]string{}
		if profile != "" {
			use["claude"] = profile
		}
		var stderr bytes.Buffer
		e := &Env{Home: home, Workspace: ws, Vars: capabilityJailVars(t, packs, servers, use), Stderr: &stderr}
		ConfigurePackSurfaces(e, packs)
		if fails := e.GenFailures(); len(fails) != 0 {
			t.Fatalf("boot render failed: %v\n%s", fails, stderr.String())
		}
		return e, stderr.String()
	}
	e, _ := boot(first)
	if len(extra) > 0 {
		doc := decodeJSONFile(t, e.ClaudeJSONPath())
		table, _ := doc["mcpServers"].(map[string]any)
		if table == nil {
			table = map[string]any{}
		}
		for k, v := range extra {
			table[k] = v
		}
		doc["mcpServers"] = table
		writeHostFile(t, e.ClaudeJSONPath(), dumpJSON(t, doc))
	}
	e, notice := boot(second)
	return notice, renderedMCPServers(t, e, "claude")
}

// noticeLine is the one line of notice that names name, "" when none does.
func noticeLine(notice, name string) string {
	for _, l := range strings.Split(notice, "\n") {
		if strings.Contains(l, ": "+name) || strings.Contains(l, ", "+name) || strings.Contains(l, " "+name+" (") {
			return l
		}
	}
	return ""
}

// The measured sequence: on kilo (no web_search) the server is delivered; back on claude's own
// login, which searches natively, the boundary withholds it. The notice must say it is declared and
// why it is withheld, and must not send the reader to declare it.
func TestTheBootDropNoticeSaysACapabilityWithheldServerIsInConfig(t *testing.T) {
	packs := capabilityPacks(t, "claude", "kilo")
	notice, rendered := bootTwice(t, packs, map[string]any{"probe-search": probeSearch()}, "kilo", "", nil)
	if _, kept := rendered["probe-search"]; kept {
		t.Fatalf("probe-search was delivered under claude's own login; this test is about the "+
			"withheld case (mcpServers = %v)", rendered)
	}
	line := noticeLine(notice, "probe-search")
	if line == "" {
		t.Fatalf("the boot must still announce that probe-search left ~/.claude.json; got %q", notice)
	}
	for _, wrong := range []string{"not in config", "to keep it, declare it"} {
		if strings.Contains(line, wrong) {
			t.Errorf("the notice says %q of a server that IS declared under mcp_servers; got %q",
				wrong, line)
		}
	}
	for _, want := range []string{
		"claude/config:",
		"mcpServers",
		"`mcp_servers`",         // where it is declared
		"web_search",            // the capability that withheld it
		"claude's own login",    // the source that performs it
		"remove its `provides`", // the one thing that delivers it here
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the notice for a capability-withheld server must name %q; got %q", want, line)
		}
	}
}

// A selected provider that performs the job is named as that provider, not as the agent's login:
// the source decides, so the notice names the source.
func TestTheBootDropNoticeNamesTheProviderThatWithheldTheServer(t *testing.T) {
	packs := capabilityPacks(t, "claude", "kilo", "zai")
	notice, _ := bootTwice(t, packs, map[string]any{"probe-search": probeSearch()}, "kilo", "zai", nil)
	line := noticeLine(notice, "probe-search")
	if line == "" {
		t.Fatalf("the boot must announce that probe-search left ~/.claude.json; got %q", notice)
	}
	if !strings.Contains(line, `provider "zai"`) {
		t.Errorf("the notice must name the selected provider that performs web_search; got %q", line)
	}
	if strings.Contains(line, "own login") {
		t.Errorf("the notice names the agent's login while a provider is selected; got %q", line)
	}
}

// One table can lose both kinds at once. Each keeps its own remedy: the hand-added entry is still
// told where to declare it, and the withheld one is not.
func TestTheBootDropNoticeKeepsTheDeclareRemedyForAnEntryNotInConfig(t *testing.T) {
	packs := capabilityPacks(t, "claude", "kilo")
	notice, _ := bootTwice(t, packs, map[string]any{"probe-search": probeSearch()}, "kilo", "",
		map[string]any{"handmade": map[string]any{"command": "/bin/handmade"}})
	hand := noticeLine(notice, "handmade")
	if !strings.Contains(hand, "dropping from mcpServers (not in config): handmade") ||
		!strings.Contains(hand, "an MCP server under `mcp_servers`") {
		t.Errorf("the hand-added entry lost its declare-it remedy; got %q", hand)
	}
	if strings.Contains(hand, "probe-search") {
		t.Errorf("the withheld server is listed among the entries not in config; got %q", hand)
	}
	if withheld := noticeLine(notice, "probe-search"); withheld == "" || withheld == hand {
		t.Errorf("the withheld server must get a line of its own; got %q", notice)
	}
}

// A server with no `provides`, or one claiming a job the source does not do, is delivered, so it
// is never reported withheld.
func TestTheBootDropNoticeReportsNothingForADeliveredServer(t *testing.T) {
	packs := capabilityPacks(t, "claude", "kilo")
	servers := map[string]any{
		"plain":  map[string]any{"command": "/bin/true"},
		"search": map[string]any{"command": "/bin/true", "provides": "code_search"},
	}
	notice, rendered := bootTwice(t, packs, servers, "kilo", "", nil)
	for name := range servers {
		if _, kept := rendered[name]; !kept {
			t.Errorf("%s was not delivered under claude's own login (mcpServers = %v)", name, rendered)
		}
	}
	if strings.Contains(notice, "mcpServers") {
		t.Errorf("a boot that drops nothing from mcpServers printed a notice about it: %q", notice)
	}
}
