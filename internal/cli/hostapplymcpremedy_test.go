package cli

// hostapplymcpremedy_test.go pins HC-D2 (docs/design/host-computed-layer.md §7): the remedy a
// host apply prints for a dropped MCP entry is one that KEEPS the entry at the host.
//
// HC-D2 first made it name a per-surface `config-overlay`, because `mcp_servers` reached no
// host file while host apply ran no derive for content. OQ-HC1 (2026-09-28) made the host run
// the jail's derives over the user's own `mcp_servers`, so the remedy names that key again —
// one entry there reaches every agent's host file, as it reaches a jail — with the overlay kept
// as the per-surface alternative (HC-D20).
//
// So the test FOLLOWS both pieces of advice, in the real apply, and each must keep the entry.
//
// THE FIXTURE IS CLAUDE'S ~/.claude.json, not codex's TOML, which measured the case. Under
// `host_management: "own"` — the one contract that renders since the `assert` retirement
// (OQ-CO14) — codex/config composes `stateful`, and its first render ADOPTS a hand-added server
// instead of dropping it, so there is no loss there for a remedy to name. claude/config declares
// `rmw`, which `own` still runs: its `mcpServers` table is regenerated from the declarations, so
// it is the shipped surface where a hand-added entry still goes.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// claudeHandmadeHome is a home selecting the shipped claude pack under `own`, whose
// ~/.claude.json holds one MCP server added by hand, which the host apply drops: claude/config's
// `mcpServers` table is yolo's, and nothing declares this entry in it. userConfig, when given,
// replaces the user config whole.
func claudeHandmadeHome(t *testing.T, userConfig string) (home, config string) {
	t.Helper()
	home = t.TempDir()
	selectPacksWith(t, home, `"claude"`, `,"host_management":"own"`)
	if userConfig != "" {
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), userConfig)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	config = filepath.Join(home, ".claude.json")
	writeFile(t, config, `{"mcpServers":{"handmade":{"command":"echo"}}}`)
	return home, config
}

func TestTheHostMCPRemedyNamesWhatReachesTheHost(t *testing.T) {
	home, _ := claudeHandmadeHome(t, "")
	survey, report := surveyApply(t)
	if !strings.Contains(report, "mcpServers.handmade (dropped") {
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
	if !strings.Contains(remedy, manifest.EntryKindHomes()) {
		t.Errorf("the host's list of where each kind is declared is not the one the jail boot's "+
			"drop notice gives (manifest.EntryKindHomes, %q): %q", manifest.EntryKindHomes(), remedy)
	}
	if !strings.Contains(remedy, "`mcp_servers`") {
		t.Errorf("the remedy does not name `mcp_servers`, which reaches every host MCP "+
			"surface since the host runs the jail's derives (OQ-HC1): %q", remedy)
	}
	if want := filepath.Join(home, ".config", "yolo-jail", "config.jsonc"); !strings.Contains(remedy, want) {
		t.Errorf("the remedy does not name the user config the declaration goes in (%s): %q",
			want, remedy)
	}
	if !strings.Contains(remedy, "config-overlay") {
		t.Errorf("the remedy dropped the per-surface `config-overlay` alternative: %q", remedy)
	}
	if strings.Contains(remedy, "reach jails only") {
		t.Errorf("the remedy still says `mcp_servers` reaches jails only: %q", remedy)
	}
}

// THE `mcp_servers` ADVICE, followed: an identical entry in the user config keeps the host
// entry — no loss line, no prompt — because host apply now runs claude's derive over the user's
// `mcp_servers` (OQ-HC1; §6.7 item 1 of the design). Before the ruling this was the measured
// case that forced HC-D2 (measured on codex's file): the dry run warned and the --assert
// dropped the entry.
func TestAnMCPServersEntryKeepsTheHostEntry(t *testing.T) {
	_, config := claudeHandmadeHome(t,
		`{"packs":["claude"],"host_management":"own","mcp_servers":{"handmade":{"command":"echo"}}}`)
	// The entry exactly as claude's derive writes that server, so the file holds what the
	// user's config declares and nothing is replaced either.
	writeFile(t, config, `{"mcpServers":{"handmade":{"command":"echo"}}}`)
	if _, report := surveyApply(t); strings.Contains(report, "handmade (") {
		t.Errorf("with the entry under mcp_servers the dry run still reports it lost:\n%s", report)
	}
	// No stdin: a first-apply loss confirmation would refuse, so rc 0 is the proof none fired.
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert rc=%d\n%s", rc, report)
	}
	if data, _ := os.ReadFile(config); !strings.Contains(string(data), "handmade") {
		t.Fatalf("the entry mcp_servers declares was dropped from the host file:\n%s", data)
	}
}

// THE NEW ADVICE, followed: the config-overlay the remedy describes, in the file it names,
// keeps the entry — no loss line, no prompt, and the entry is in the file after the --assert.
func TestFollowingTheHostMCPRemedyKeepsTheEntry(t *testing.T) {
	home, config := claudeHandmadeHome(t, "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"),
		`{"contributes":[{"kind":"config-overlay","surface":"claude/config",`+
			`"config":{"managed":{"mcpServers":{"handmade":{"command":"echo"}}}}}]}`)
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
