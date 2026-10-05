package config

// mcppacks_test.go pins the config side of the `mcp` contribution kind
// (docs/design/mcp-presets-removal.md OQ-MP3): the kind's entry carries exactly the fields a user's
// own mcp_servers entry takes (risk R2), and a server name two selected packs ship is refused by
// validation — the call both `yolo check` and the launch preflight make.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// packdecl may not import this package, so its copy of the entry field set is pinned here,
// where both are in reach. A field added to knownMCPServerKeys and not to the kind would make a
// pack's server second-class to a hand-written one; the reverse would let a pack say what a user
// cannot.
func TestTheMCPKindCarriesEveryMCPServerKey(t *testing.T) {
	var want []string
	for k := range knownMCPServerKeys {
		want = append(want, k)
	}
	sort.Strings(want)
	if got := packdecl.MCPEntryKeys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("packdecl's mcp entry keys %v differ from mcp_servers' %v", got, want)
	}
}

// mcpLocalPack writes a local pack at dir/name shipping one MCP server named server.
func mcpLocalPack(t *testing.T, dir, name, server string) string {
	t.Helper()
	root := filepath.Join(dir, name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","contributes":[{"kind":"mcp","name":"` + server +
		`","config":{"command":"/bin/true"}}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTwoSelectedPacksShippingOneMCPServerAreRefused(t *testing.T) {
	packsDir := t.TempDir()
	one := mcpLocalPack(t, packsDir, "one", "browser")
	two := mcpLocalPack(t, packsDir, "two", "browser")
	other := mcpLocalPack(t, packsDir, "other", "search")

	selectionHome(t, `[{"source":"file://`+one+`","name":"one"},{"source":"file://`+other+`","name":"other"}]`)
	if errs, _ := ValidateConfig(decode(t, `{}`), t.TempDir(), nil); len(errs) != 0 {
		t.Errorf("two packs shipping two different servers were refused: %v", errs)
	}

	selectionHome(t, `[{"source":"file://`+one+`","name":"one"},{"source":"file://`+two+`","name":"two"}]`)
	errs, _ := ValidateConfig(decode(t, `{}`), t.TempDir(), nil)
	found := false
	for _, e := range errs {
		if strings.Contains(e, `MCP server "browser" is shipped by packs one and two`) &&
			strings.Contains(e, "Remove one of those packs from `packs`") {
			found = true
		}
	}
	if !found {
		t.Errorf("a server name two selected packs ship was not refused with its next step: %v", errs)
	}
}
