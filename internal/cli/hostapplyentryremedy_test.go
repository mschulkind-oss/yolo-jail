package cli

// hostapplyentryremedy_test.go pins that the dropped-entry remedy names the tables that lost
// entries in THIS run, not a list of MCP keys.
//
// The group collects every yolo-owned table's losses — Copilot's `lspServers` as much as any
// agent's MCP table — while the remedy was one fixed sentence: an example naming codex/config
// and `mcp_servers`, and a key list of `mcpServers`, `mcp_servers` or `mcp`. So an LSP server
// dropped from ~/.copilot/lsp-config.json, with codex not even selected, was told to add an
// MCP overlay for codex, and following that list literally could not keep it.
//
// THE FIXTURE IS A PACK OF ITS OWN, not the shipped copilot pack the case was measured on. Under
// `host_management: "own"` — the one contract that renders since the `assert` retirement
// (OQ-CO14) — copilot/lsp composes `stateful`, and its first render ADOPTS a hand-added server
// rather than dropping it, so no shipped surface loses an entry from a table other than an MCP
// one any more. A surface declaring `rmw` still runs the read-modify-write `assert` ran for every
// surface, with its derive's table regenerated in full, so the loss and the remedy that names its
// table are reached through one: lspTablePackJSON, a copy of copilot/lsp's declaration and derive
// at mode `rmw`.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lspTablePackJSON declares one `rmw` surface whose `lspServers` table its derive regenerates in
// full (lspTableDerive) — copilot/lsp's shape, under its own agent name so it claims no shipped
// surface.
const lspTablePackJSON = `{"name":"lspowner","contributes":[
  {"kind":"config","config":[{"agent":"acme","name":"lsp","codec":"json","mode":"rmw",
    "path":"~/.acme/lsp-config.json","defaults":{"lspServers":{}}}]}]}`

// lspTableDerive projects the user config's `lsp_servers` into the surface's `lspServers` and
// declares the table in full, as packs/copilot/derive.lua does for copilot/lsp.
const lspTableDerive = `yolo.derive("acme", "lsp", function(ctx)
  local out = {}
  for name, s in pairs(ctx.lsp_servers) do
    out[name] = { command = s.command, args = s.args or ctx.empty_array }
  end
  return { lspServers = ctx.in_full(out) }
end)
`

// handmadeLSPHome selects lspTablePackJSON under `own`, with one LSP server added by hand to
// ~/.acme/lsp-config.json, which the host apply drops: the `lspServers` table is yolo's, and
// nothing declares this entry in it.
func handmadeLSPHome(t *testing.T) (home, lsp string) {
	t.Helper()
	home = t.TempDir()
	packDir := filepath.Join(t.TempDir(), "lspowner")
	writeFile(t, filepath.Join(packDir, "pack.json"), lspTablePackJSON)
	writeFile(t, filepath.Join(packDir, "derive.lua"), lspTableDerive)
	selectPacksWith(t, home, `{"source":"file://`+packDir+`","name":"lspowner"}`,
		`,"host_management":"own"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	lsp = filepath.Join(home, ".acme", "lsp-config.json")
	writeFile(t, lsp, `{"lspServers":{"handmade":{"command":"gopls","args":["serve"]}}}`)
	return home, lsp
}

func TestTheDroppedEntryRemedyNamesTheTableThatLostTheEntry(t *testing.T) {
	home, _ := handmadeLSPHome(t)
	survey, report := surveyApply(t)
	if !strings.Contains(report, "lspServers.handmade (dropped") {
		t.Fatalf("fixture premise: the dry run does not report the hand-added LSP server:\n%s", report)
	}
	groups := groupsWithKey(hostApplyRemedyGroups(survey, home, false), mcpEntryRemedyKey(home))
	if len(groups) != 1 {
		t.Fatalf("the dropped entry is not one group keyed on the file its remedy names: %+v",
			hostApplyRemedyGroups(survey, home, false))
	}
	remedy := groups[0].Remedy
	if n := strings.Count(report, remedy); n != 1 {
		t.Errorf("the remedy is stated once for the group; it appears %d times:\n%s", n, report)
	}
	for _, want := range []string{`"surface": "acme/lsp"`, `"lspServers"`, "`lspServers`"} {
		if !strings.Contains(remedy, want) {
			t.Errorf("the remedy for an entry dropped from acme/lsp's `lspServers` does not "+
				"name %s: %q", want, remedy)
		}
	}
	for _, unwanted := range []string{"codex/config", `"mcp_servers"`, "`mcpServers`"} {
		if strings.Contains(remedy, unwanted) {
			t.Errorf("the remedy names %s, which lost nothing in this run (codex is not even "+
				"selected): %q", unwanted, remedy)
		}
	}
}

// THE REMEDY, followed: the config-overlay it describes, in the file it names, keeps the LSP
// server — no loss line, no prompt, and the entry is in the file after the --assert.
func TestFollowingTheDroppedLSPEntryRemedyKeepsTheEntry(t *testing.T) {
	home, lsp := handmadeLSPHome(t)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"),
		`{"contributes":[{"kind":"config-overlay","surface":"acme/lsp",`+
			`"config":{"managed":{"lspServers":{"handmade":{"command":"gopls","args":["serve"]}}}}}]}`)
	if _, report := surveyApply(t); strings.Contains(report, "handmade (") {
		t.Errorf("following the remedy left the entry reported as lost:\n%s", report)
	}
	// No stdin: a first-apply loss confirmation would refuse, so rc 0 is the proof none fired.
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert rc=%d\n%s", rc, report)
	}
	if data, _ := os.ReadFile(lsp); !strings.Contains(string(data), "handmade") {
		t.Errorf("following the remedy did not keep the entry:\n%s", data)
	}
}

// A DECLINED first apply names the same tables: confirmHostLosses' trailer and the abort line
// after it read the one remedy, built from the losses the prompt listed.
func TestADeclinedApplyNamesTheTableThatWouldLoseTheEntry(t *testing.T) {
	handmadeLSPHome(t)
	rc, declined := applyWith(t, true, strings.NewReader("n\n"))
	if rc == 0 {
		t.Fatalf("a declined first apply must not proceed\n%s", declined)
	}
	if !strings.Contains(declined, "lspServers.handmade") {
		t.Fatalf("fixture premise: the prompt does not list the LSP server:\n%s", declined)
	}
	if n := strings.Count(declined, `"surface": "acme/lsp"`); n < 2 {
		t.Errorf("the prompt trailer and the abort line must both name the surface that loses "+
			"the entry; acme/lsp's example appears %d time(s):\n%s", n, declined)
	}
	if strings.Contains(declined, "codex/config") {
		t.Errorf("the decline names codex/config, which is not selected:\n%s", declined)
	}
}
