package cli

// hostapplyentryremedy_test.go pins that the dropped-entry remedy names the tables that lost
// entries in THIS run, not a list of MCP keys.
//
// The group collects every yolo-owned table's losses — Copilot's `lspServers` as much as any
// agent's MCP table — while the remedy was one fixed sentence: an example naming codex/config
// and `mcp_servers`, and a key list of `mcpServers`, `mcp_servers` or `mcp`. So an LSP server
// dropped from ~/.copilot/lsp-config.json, with codex not even selected, was told to add an
// MCP overlay for codex, and following that list literally could not keep it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copilotHandmadeLSPHome selects only the shipped copilot pack, with one LSP server added by
// hand to ~/.copilot/lsp-config.json, which a host apply under `assert` drops: the
// `lspServers` table is yolo's, and nothing declares this entry in it.
func copilotHandmadeLSPHome(t *testing.T) (home, lsp string) {
	t.Helper()
	home = t.TempDir()
	selectPacks(t, home, `"copilot"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	lsp = filepath.Join(home, ".copilot", "lsp-config.json")
	writeFile(t, lsp, `{"lspServers":{"handmade":{"command":"gopls","args":["serve"]}}}`)
	return home, lsp
}

func TestTheDroppedEntryRemedyNamesTheTableThatLostTheEntry(t *testing.T) {
	home, _ := copilotHandmadeLSPHome(t)
	survey, report := surveyApply(t)
	if !strings.Contains(report, "lspServers.handmade (dropped") {
		t.Fatalf("fixture premise: the dry run does not report the hand-added LSP server:\n%s", report)
	}
	groups := groupsWithKey(hostApplyRemedyGroups(survey, home, false), mcpEntryRemedyKey(home))
	if len(groups) != 1 {
		t.Fatalf("the dropped entry is not one group keyed on the local pack: %+v",
			hostApplyRemedyGroups(survey, home, false))
	}
	remedy := groups[0].Remedy
	if n := strings.Count(report, remedy); n != 1 {
		t.Errorf("the remedy is stated once for the group; it appears %d times:\n%s", n, report)
	}
	for _, want := range []string{`"surface": "copilot/lsp"`, `"lspServers"`, "`lspServers`"} {
		if !strings.Contains(remedy, want) {
			t.Errorf("the remedy for an entry dropped from copilot/lsp's `lspServers` does not "+
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
	home, lsp := copilotHandmadeLSPHome(t)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"),
		`{"contributes":[{"kind":"config-overlay","surface":"copilot/lsp",`+
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
	copilotHandmadeLSPHome(t)
	rc, declined := applyWith(t, true, strings.NewReader("n\n"))
	if rc == 0 {
		t.Fatalf("a declined first apply must not proceed\n%s", declined)
	}
	if !strings.Contains(declined, "lspServers.handmade") {
		t.Fatalf("fixture premise: the prompt does not list the LSP server:\n%s", declined)
	}
	if n := strings.Count(declined, `"surface": "copilot/lsp"`); n < 2 {
		t.Errorf("the prompt trailer and the abort line must both name the surface that loses "+
			"the entry; copilot/lsp's example appears %d time(s):\n%s", n, declined)
	}
	if strings.Contains(declined, "codex/config") {
		t.Errorf("the decline names codex/config, which is not selected:\n%s", declined)
	}
}
