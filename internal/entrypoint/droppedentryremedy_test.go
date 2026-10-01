package entrypoint

// droppedentryremedy_test.go pins the REMEDY the jail boot's drop notice gives
// (noteDroppedManagedEntries), through the boot loop a jail runs (ConfigurePackSurfaces), so
// deleting the notice's call in regenerateManagedTables fails these as surely as rewording it.
//
// The defect (docs/design/diagnostics-past-the-boundary.md §3.1): the notice interpolated the
// dropped table's name into its subject and then hardcoded `mcp_servers` in its remedy, so an
// entry dropped from any table but an MCP one was handed advice that cannot keep it. The only
// shipped table that reaches the notice is claude/config's `mcpServers`, so the fixture here
// is a pack of its own: an `rmw` surface whose derive builds an `lspServers` table from
// lsp_servers, the shape copilot's LSP table has.

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// lspTablePack owns one rmw surface whose derive regenerates `lspServers` in full from the
// config's lsp_servers, by name.
func lspTablePack(t *testing.T) *packload.Pack {
	t.Helper()
	dir := t.TempDir()
	writeHostFile(t, filepath.Join(dir, "pack.json"), `{"name":"acme","description":"d","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"acme"},
	  {"kind":"config","config":[{"agent":"acme","name":"config","codec":"json",
	   "path":"~/.acme.json","mode":"rmw"}]}]}`)
	writeHostFile(t, filepath.Join(dir, "derive.lua"), `yolo.derive("acme", "config", function(ctx)
  local out = {}
  for name, s in pairs(ctx.lsp_servers) do
    out[name] = { command = s.command }
  end
  return { lspServers = ctx.in_full(out) }
end)
`)
	p, problems := packload.LoadDir(dir, "acme")
	if p == nil {
		t.Fatalf("the acme fixture did not load: %v", problems)
	}
	return p
}

// bootLSPTable boots the fixture over a ~/.acme.json holding one hand-added LSP server, with
// lspServers (the YOLO_LSP_SERVERS JSON, "" for none) configured, and returns the boot's stderr
// and the rendered file.
func bootLSPTable(t *testing.T, lspServers string) (string, map[string]any) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".acme.json")
	writeHostFile(t, path, `{"lspServers":{"handmade":{"command":"/bin/handmade"}}}`)
	vars := map[string]string{}
	if lspServers != "" {
		vars["YOLO_LSP_SERVERS"] = lspServers
	}
	var stderr bytes.Buffer
	e := &Env{Home: home, Workspace: t.TempDir(), Vars: vars, Stderr: &stderr}
	withCtxRoot(t, t.TempDir(), "acme")
	ConfigurePackSurfaces(e, []*packload.Pack{lspTablePack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("the acme boot render failed: %v\n%s", fails, stderr.String())
	}
	return stderr.String(), decodeJSONFile(t, path)
}

// An entry dropped from an LSP table is told where an LSP server is declared. The old notice
// said "add under `mcp_servers` to keep it", which for this table keeps nothing.
func TestTheBootDropNoticeNamesTheRemedyForATableThatIsNotMCP(t *testing.T) {
	notice, _ := bootLSPTable(t, "")
	if !strings.Contains(notice, "acme/config: dropping from lspServers (not in config): handmade") {
		t.Fatalf("the boot must announce the dropped entry by surface, table and name; got %q", notice)
	}
	remedy := notice[strings.Index(notice, "handmade"):]
	if !strings.Contains(remedy, "an LSP server under `lsp_servers`") {
		t.Errorf("the remedy for an entry dropped from lspServers must name lsp_servers, where an "+
			"LSP server is declared; got %q", remedy)
	}
	if !strings.Contains(remedy, "lspServers") {
		t.Errorf("the remedy must name the table it is the remedy for, so it reads as this "+
			"table's and not a stock line; got %q", remedy)
	}
	if strings.Contains(remedy, "add under `mcp_servers` to keep it") {
		t.Errorf("the remedy still hands every table the MCP advice; got %q", remedy)
	}
}

// Every place the remedy says to declare something "under" is a config table. The first
// wording read "declare it in yolo-jail.jsonc under the table lspServers is built from", which
// parses as "under the table lspServers" — the agent's own key, which no config table is called.
func TestTheBootDropRemedyDeclaresOnlyUnderAConfigTable(t *testing.T) {
	notice, _ := bootLSPTable(t, "")
	i := strings.Index(notice, "handmade")
	if i < 0 {
		t.Fatalf("the boot must announce the dropped entry; got %q", notice)
	}
	remedy := notice[i:]
	config := map[string]bool{manifest.SourceMCPServers: true, manifest.SourceLSPServers: true,
		manifest.SourceProviders: true}
	unders := regexp.MustCompile(`under (\S+)`).FindAllStringSubmatch(remedy, -1)
	if len(unders) == 0 {
		t.Fatalf("the remedy names no config table to declare the entry under; got %q", remedy)
	}
	for _, m := range unders {
		if where := strings.Trim(m[1], "`,.;()"); !config[where] {
			t.Errorf("the remedy says to declare it under %q, which is not a config table "+
				"(%s, %s, %s); got %q", where, manifest.SourceMCPServers,
				manifest.SourceLSPServers, manifest.SourceProviders, remedy)
		}
	}
}

// The remedy is one a reader can follow: the entry declared where the notice says goes back
// into the table, and the next boot drops nothing.
func TestFollowingTheBootDropRemedyKeepsTheEntry(t *testing.T) {
	notice, doc := bootLSPTable(t, `{"handmade":{"command":"/bin/handmade"}}`)
	if strings.Contains(notice, "dropping from") {
		t.Errorf("an entry declared under lsp_servers must not be dropped; got %q", notice)
	}
	table, _ := doc["lspServers"].(map[string]any)
	if _, kept := table["handmade"]; !kept {
		t.Errorf("lspServers = %v, want the entry declared under lsp_servers kept", doc["lspServers"])
	}
}

// The one shipped table that reaches the notice keeps the remedy that was already right for
// it: an MCP server goes under mcp_servers.
func TestTheBootDropNoticeForClaudesMCPTableStillNamesMCPServers(t *testing.T) {
	home := t.TempDir()
	writeHostFile(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"handmade":{"command":"/bin/handmade"}}}`)
	var stderr bytes.Buffer
	e := &Env{Home: home, Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &stderr}
	withCtxRoot(t, t.TempDir(), "claude")
	ConfigurePackSurfaces(e, testPacksForAgent(t, "claude"))
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("claude boot render failed: %v", fails)
	}
	notice := stderr.String()
	i := strings.Index(notice, "claude/config: dropping from mcpServers (not in config): handmade")
	if i < 0 {
		t.Fatalf("the boot must announce the dropped MCP server; got %q", notice)
	}
	line := notice[i:]
	if nl := strings.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	if !strings.Contains(line, "an MCP server under `mcp_servers`") {
		t.Errorf("the remedy for claude's mcpServers must name mcp_servers; got %q", line)
	}
}
