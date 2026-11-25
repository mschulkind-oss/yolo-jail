package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGapUserConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUserScopeSelectionGapIsEmptyForAResolvingSelection(t *testing.T) {
	writeGapUserConfig(t, "packs = [\"claude\"]\n")
	if gap := UserScopeSelectionGap(); gap != "" {
		t.Fatalf("a shipped pack alone has a gap: %s", gap)
	}
}

func TestUserScopeSelectionGapNamesAnUnfetchedRemoteAndTheFetchCommand(t *testing.T) {
	writeGapUserConfig(t, "packs = [\"claude\", {git = \"https://example.invalid/acme/craft\", ref = \"main\"}]\n")
	gap := UserScopeSelectionGap()
	if !strings.Contains(gap, "craft") || !strings.Contains(gap, "not been fetched") || !strings.Contains(gap, "yolo pack install") {
		t.Fatalf("gap = %q", gap)
	}
}

func TestUserScopeSelectionGapNamesAVanishedLocalDirectory(t *testing.T) {
	root := t.TempDir()
	writeGapUserConfig(t, "packs = [\"claude\", \"src/mine\"]\n[pack_sources]\nsrc = '"+root+"'\n")
	gap := UserScopeSelectionGap()
	if !strings.Contains(gap, "mine") || !strings.Contains(gap, "does not exist") || !strings.Contains(gap, "yolo pack remove") {
		t.Fatalf("gap = %q", gap)
	}
}

func TestUserScopeSelectionGapNamesAPackWhoseManifestDoesNotRead(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken", "pack.toml"), []byte("this is [not toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGapUserConfig(t, "packs = [\"claude\", \"src/broken\"]\n[pack_sources]\nsrc = '"+root+"'\n")
	gap := UserScopeSelectionGap()
	if !strings.Contains(gap, "broken") || !strings.Contains(gap, "does not read") || !strings.Contains(gap, "yolo pack ls") {
		t.Fatalf("gap = %q", gap)
	}
}
