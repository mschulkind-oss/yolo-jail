package cli

// hostownedlossreport_test.go pins HC-D5 (docs/design/host-computed-layer.md §7) where the user
// meets it: `yolo host apply` under `host_management: own` names an MCP entry as dropped only
// when the owned write drops it, and so does not ask the first-apply confirmation for a loss
// that never happens.
//
// Measured by the design's scratch test before the fix: a first owned apply kept a hand-added
// codex `mcp_servers` entry, while the report named it dropped. At the CLI that is a dry run
// warning "1 of your entries would be dropped" and an --assert that stops at the one-way-door
// prompt — which, with no terminal to answer it, refuses and writes nothing.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnOwnedHostApplyDoesNotReportAnEntryItKeeps(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"codex"`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["codex"],"host_management":"own"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	config := filepath.Join(home, ".codex", "config.toml")
	writeFile(t, config, "model = \"gpt-5\"\n\n[mcp_servers.mine]\ncommand = \"/bin/mine\"\n")

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("dry run rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "mcp_servers.mine") || strings.Contains(report, "would be dropped") {
		t.Errorf("the dry run reports `mine` as dropped, but the owned write keeps it:\n%s", report)
	}

	// No stdin: a first-apply loss confirmation would refuse, so rc 0 is the proof no prompt
	// was asked.
	rc, report = applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("the owned --assert stopped (rc=%d) — it asked to confirm a loss the write does "+
			"not cause:\n%s", rc, report)
	}
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/bin/mine") {
		t.Fatalf("fixture premise: the owned write was expected to keep `mine`:\n%s", data)
	}
}
