package hostskills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
)

// slashedname_test.go pins the internal half of RC-D48 (docs/design/readable-config.md): the
// ownership record a yolo-written plugin manifest carries (x-yolo-pack) and the claimant query
// (ClaimsAt). The agent-facing spelling of a pack's names is pack-system.md's, not an escape.

// TestAPluginManifestRecordsThePacksOwnName: the dropped-pack sweep reads YoloPluginOwner, which
// prefers x-yolo-pack, so a manifest whose Name is not the pack's name still names its owner.
func TestAPluginManifestRecordsThePacksOwnName(t *testing.T) {
	dir := t.TempDir()
	if err := writePluginManifest(dir, "yolo-packs/pi-fork", "d"); err != nil {
		t.Fatal(err)
	}
	if owner, ok := YoloPluginOwner(dir); !ok || owner != "yolo-packs/pi-fork" {
		t.Errorf("YoloPluginOwner = %q, %v, want the pack's own name yolo-packs/pi-fork", owner, ok)
	}
	// An older manifest, with no x-yolo-pack, still names its owner by Name.
	old := t.TempDir()
	if err := os.MkdirAll(filepath.Join(old, pluginManifestDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, pluginManifestDir, pluginManifestName),
		[]byte(`{"name":"matt","x-yolo-managed-by":"yolo-jail"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if owner, ok := YoloPluginOwner(old); !ok || owner != "matt" {
		t.Errorf("YoloPluginOwner(old manifest) = %q, %v, want matt", owner, ok)
	}
}

// TestClaimsAtNamesAWrappedPluginsPack is the query the LSP plugin asks, with no probe layer.
func TestClaimsAtNamesAWrappedPluginsPack(t *testing.T) {
	d := Destination{Dir: t.TempDir(), Layers: []Layer{
		{Pack: "lsp_servers/yolo-lsp", Tier: TierNamespaced,
			Plugins: []*pluginpack.Plugin{{Dir: filepath.Join(t.TempDir(), "yolo-lsp")}}},
	}}
	claims := ClaimsAt(d, "yolo-lsp")
	if len(claims) != 1 || claims[0].Pack != "lsp_servers/yolo-lsp" {
		t.Errorf("ClaimsAt(yolo-lsp) = %+v, want the pack lsp_servers/yolo-lsp", claims)
	}
}
