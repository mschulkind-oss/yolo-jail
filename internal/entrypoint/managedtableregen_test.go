package entrypoint

// managedtableregen_test.go pins the JAIL half of the wholesale table write: a boot regenerates
// ~/.claude.json's `mcpServers` from config alone, whatever the file held.
//
// regenerateManagedTables cleared the table by ranging over dest.Keys() while deleting from
// it, and Keys() is the OrderedMap's own slice, which Delete shifts in place. So the loop
// stepped over every other entry: with a, b, c, d in the file and nothing configured, b
// survived the boot — a server config no longer declares, still live in the jail — while the
// boot notice named all four as dropped. Two entries, the size every earlier test seeded, is
// the one size the loop could not get wrong.
//
// The boot goes through ConfigurePackSurfaces, the loop darwin.go and the container
// entrypoint both run, so deleting the table write's call site fails this too.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestABootDropsEveryHandAddedServerFromClaudeJSON(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"numStartups":3,"mcpServers":{`+
		`"alpha":{"command":"/bin/alpha"},"bravo":{"command":"/bin/bravo"},`+
		`"charlie":{"command":"/bin/charlie"},"delta":{"command":"/bin/delta"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	e := &Env{Home: home, Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &stderr}
	withCtxRoot(t, t.TempDir(), "claude")
	ConfigurePackSurfaces(e, testPacksForAgent(t, "claude"))
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("claude boot render failed: %v", fails)
	}

	doc := decodeJSONFile(t, path)
	if doc["numStartups"] != float64(3) {
		t.Errorf("the boot lost a key yolo does not own: %v", doc)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	var kept []string
	for name := range servers {
		kept = append(kept, name)
	}
	sort.Strings(kept)
	if len(kept) != 0 {
		t.Errorf("with no MCP server configured the boot kept %v in ~/.claude.json — "+
			"config is the table's only source, so every hand-added server goes", kept)
	}
	notice := stderr.String()
	if !strings.Contains(notice, "alpha, bravo, charlie, delta") {
		t.Errorf("the boot notice must name every server it drops; got %q", notice)
	}
}
