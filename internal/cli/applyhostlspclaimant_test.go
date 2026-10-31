package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
)

// TestLSPPluginClaimantNamesAPackWhoseNameHoldsASlash: a readable-config pack is named by its
// source handle (OQ-RC15), so `lsp_servers/yolo-lsp` is a pack name a user can write — a source
// called lsp_servers holding a pack called yolo-lsp. The claimant query once found other claims by
// comparing pack names with a probe layer of exactly that name, on the premise that no configured
// pack name holds a "/"; such a pack's wrapped yolo-lsp plugin was then never reported, and the LSP
// plugin was written over it.
func TestLSPPluginClaimantNamesAPackWhoseNameHoldsASlash(t *testing.T) {
	plugin := filepath.Join(t.TempDir(), jailcontent.LSPPluginDir)
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	d := hostskills.Destination{Dir: t.TempDir(), Layers: []hostskills.Layer{{
		Pack: "lsp_servers/" + jailcontent.LSPPluginDir, Tier: hostskills.TierNamespaced,
		Plugins: []*pluginpack.Plugin{{Dir: plugin}},
	}}}
	if got := lspPluginClaimant(d); got != "lsp_servers/"+jailcontent.LSPPluginDir {
		t.Errorf("lspPluginClaimant = %q, want lsp_servers/%s", got, jailcontent.LSPPluginDir)
	}

	// No claim, no claimant; and an unresolved layer leaves the destination alone, as before.
	if got := lspPluginClaimant(hostskills.Destination{Dir: d.Dir}); got != "" {
		t.Errorf("empty destination: lspPluginClaimant = %q, want none", got)
	}
	d.Layers = append(d.Layers, hostskills.Layer{Pack: "other", Unresolved: true})
	if got := lspPluginClaimant(d); got != "" {
		t.Errorf("unresolved destination: lspPluginClaimant = %q, want none", got)
	}
}
