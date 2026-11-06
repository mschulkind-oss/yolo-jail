package mcpauthdaemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestTheBrokerReadsTheHomeTheShareNames: a share naming a setup's home has its replicas read
// there, the legacy home is read for a share naming none, and a link at the setup's directory is
// refused, not followed (named-setups.md §9). Red if root() stops reading Share.HomeRel.
func TestTheBrokerReadsTheHomeTheShareNames(t *testing.T) {
	ws := t.TempDir()
	setupHome := paths.SetupHomeState(ws, "podman-self")
	for _, home := range []string{setupHome, paths.LegacyHomeState(ws)} {
		if err := os.MkdirAll(filepath.Join(home, "codex"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "codex", "which"), []byte(home), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	replica := mcpauth.Replica{Pack: "codex", Root: []string{"home", "codex"}}
	for rel, want := range map[string]string{"setups/podman-self/home": setupHome, "": paths.LegacyHomeState(ws)} {
		b := broker{share: mcpauth.Share{Workspace: ws, Home: rel}}
		r, err := b.root(replica)
		if err != nil {
			t.Fatalf("Home %q: %v", rel, err)
		}
		got, err := r.ReadFile("which")
		r.Close()
		if err != nil || string(got) != want {
			t.Errorf("Home %q: the broker read the replicas of %q (%v), want %q", rel, got, err, want)
		}
	}

	if err := os.RemoveAll(paths.SetupStateDir(ws, "podman-self")); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(elsewhere, "home", "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, paths.SetupStateDir(ws, "podman-self")); err != nil {
		t.Fatal(err)
	}
	b := broker{share: mcpauth.Share{Workspace: ws, Home: "setups/podman-self/home"}}
	if r, err := b.root(replica); err == nil {
		r.Close()
		t.Fatal("the broker followed a link at the setup's directory")
	}
}
