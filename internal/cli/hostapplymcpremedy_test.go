package cli

// hostapplymcpremedy_test.go pins HC-D2 (docs/design/host-computed-layer.md §7): the remedy a
// host apply prints for a dropped MCP entry is one that KEEPS the entry at the host.
//
// It used to say: declare it under `mcp_servers` in your user config, "one entry there reaches
// every agent". That is true of a jail and false here — host apply renders no derive for
// content, so `mcp_servers` reaches no host file — and following it did nothing: measured by
// the design's research pass, the dry run still warned and the --assert still dropped the
// entry. The one declaration that reaches a host MCP table today is a `config-overlay` naming
// the surface, for example in the conventional local pack.
//
// So the test FOLLOWS both pieces of advice, in the real apply: the old one leaves the loss in
// place (the reason the remedy had to change), and the one the remedy now names removes it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexHandmadeHome is a home selecting the shipped codex pack whose ~/.codex/config.toml holds
// one MCP server added by hand, which a host apply under `assert` drops: the codex/config
// `mcp_servers` table is yolo's, and nothing declares this entry in it.
func codexHandmadeHome(t *testing.T, userConfig string) (home, config string) {
	t.Helper()
	home = t.TempDir()
	selectPacks(t, home, `"codex"`)
	if userConfig != "" {
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), userConfig)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	config = filepath.Join(home, ".codex", "config.toml")
	writeFile(t, config, "[mcp_servers.handmade]\ncommand = \"echo\"\n")
	return home, config
}

func TestTheHostMCPRemedyNamesWhatReachesTheHost(t *testing.T) {
	home, _ := codexHandmadeHome(t, "")
	survey, report := surveyApply(t)
	if !strings.Contains(report, "handmade") {
		t.Fatalf("fixture premise: the dry run does not report the hand-added entry:\n%s", report)
	}
	remedy := mcpEntryRemedy(home, survey.DroppedTables())
	if n := strings.Count(report, remedy); n != 1 {
		t.Errorf("the remedy is stated once for the group; it appears %d times:\n%s", n, report)
	}
	groups := groupsWithKey(hostApplyRemedyGroups(survey, home, false), mcpEntryRemedyKey(home))
	if len(groups) != 1 {
		t.Errorf("the dropped entry is not one group keyed on the file the remedy names (%s): %+v",
			mcpEntryRemedyKey(home), hostApplyRemedyGroups(survey, home, false))
	}
	if !strings.Contains(remedy, "config-overlay") {
		t.Errorf("the remedy does not name a `config-overlay`, the one declaration that "+
			"reaches a host MCP table: %q", remedy)
	}
	if want := filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"); !strings.Contains(remedy, want) {
		t.Errorf("the remedy does not name the file the declaration goes in (%s): %q", want, remedy)
	}
	if strings.Contains(remedy, "every agent") {
		t.Errorf("the remedy promises that one entry reaches every agent, which is a jail's "+
			"behavior and not this notch's: %q", remedy)
	}
}

// THE OLD ADVICE, followed: an identical `mcp_servers` entry in the user config changes
// nothing at the host. This is the measured case and the reason for HC-D2 — if host apply ever
// starts consuming `mcp_servers` (OQ-HC1), this fails, and the remedy should say so again.
func TestAnMCPServersEntryDoesNotKeepAHostEntry(t *testing.T) {
	_, config := codexHandmadeHome(t,
		`{"packs":["codex"],"mcp_servers":{"handmade":{"command":"echo"}}}`)
	if _, report := surveyApply(t); !strings.Contains(report, "handmade") {
		t.Fatalf("with the entry under mcp_servers the dry run no longer reports it lost — host "+
			"apply now consumes mcp_servers, so the remedy may name it again (HC-D2):\n%s", report)
	}
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("assert rc=%d\n%s", rc, report)
	}
	if data, _ := os.ReadFile(config); strings.Contains(string(data), "handmade") {
		t.Fatalf("the entry survived with only mcp_servers declaring it:\n%s", data)
	}
}

// THE NEW ADVICE, followed: the config-overlay the remedy describes, in the file it names,
// keeps the entry — no loss line, no prompt, and the entry is in the file after the --assert.
func TestFollowingTheHostMCPRemedyKeepsTheEntry(t *testing.T) {
	home, config := codexHandmadeHome(t, "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"),
		`{"contributes":[{"kind":"config-overlay","surface":"codex/config",`+
			`"config":{"managed":{"mcp_servers":{"handmade":{"command":"echo"}}}}}]}`)
	if _, report := surveyApply(t); strings.Contains(report, "would be dropped") ||
		strings.Contains(report, "handmade (") {
		t.Errorf("following the remedy left the entry reported as lost:\n%s", report)
	}
	// No stdin: a first-apply loss confirmation would refuse, so rc 0 is the proof none fired.
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert rc=%d\n%s", rc, report)
	}
	if data, _ := os.ReadFile(config); !strings.Contains(string(data), "handmade") {
		t.Errorf("following the remedy did not keep the entry:\n%s", data)
	}
}
