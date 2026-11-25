package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A selected pack whose local source is gone declares nothing, so the approval hook still sees
// git as selected with its live authority (2026-11-24: "selection unknown" cost `yolo approve`
// the git broker's revalidator over a removed `tillr`). An unreadable manifest, which could
// declare anything, still makes every source unknown. The skipped pack is named on stderr.
func TestApprovalStoresKeepGitSelectedWhenAnUnrelatedPackVanished(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_USER_LAYER", "")
	t.Cleanup(packload.ReleaseEmbedded)
	root := filepath.Join(home, "src")
	writeApprovePackFile(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"),
		"packs = ['git', 'src/gone']\n[pack_sources]\nsrc = '"+root+"'\n")

	stores, diagnostics, err := approvalStores()
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("approvalStores: %v %q", err, diagnostics)
	}
	git := storeForSource(stores, "git")
	if git == nil || git.ApprovalStatus != "selected" || git.Revalidate == nil {
		t.Fatalf("a vanished unrelated pack made git unselected or deny-only: %+v", git)
	}

	if err := os.MkdirAll(filepath.Join(root, "gone"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeApprovePackFile(t, filepath.Join(root, "gone", "pack.toml"), "this is [not toml")
	stores, diagnostics, err = approvalStores()
	if err != nil || len(diagnostics) == 0 || !strings.HasPrefix(diagnostics[0], "INCOMPLETE:") {
		t.Fatalf("an unreadable pack did not make the selection incomplete: %v %q", err, diagnostics)
	}
	// Failing closed still says what failed: the pack by name, and its own error.
	if !strings.Contains(diagnostics[0], "pack src/gone:") || !strings.Contains(diagnostics[0], "pack.toml") {
		t.Fatalf("the incomplete-selection diagnostic does not name the failing pack and its error: %q", diagnostics[0])
	}
	if git := storeForSource(stores, "git"); git == nil || git.ApprovalStatus != "unknown" || git.Revalidate != nil {
		t.Fatalf("an unreadable pack left git with live authority: %+v", git)
	}
}
