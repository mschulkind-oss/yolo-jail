package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a tiny test helper.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadConfigInJailReadsSnapshot is the regression for the config-diff
// ping-pong: inside a jail, LoadConfig must COPY the host-written snapshot
// (which carries the user-level include_if_found overrides the jail can't see)
// rather than re-assembling a reduced config from the mounted files.
func TestLoadConfigInJailReadsSnapshot(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// The jail has a mounted config.jsonc whose include_if_found points at an
	// overrides.jsonc that does NOT exist in the jail (it's host-only). A
	// re-assemble would therefore drop mcp_servers.
	mustWrite(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{ "include_if_found": ["overrides.jsonc"], "mcp_presets": ["chrome-devtools"] }`)
	mustWrite(t, filepath.Join(ws, "yolo-jail.jsonc"), `{ "agents": ["claude"] }`)
	// The host wrote a snapshot WITH the assembled mcp_servers.
	mustWrite(t, ConfigSnapshotPath(ws), `{
  "agents": ["claude"],
  "mcp_servers": { "tavily": { "command": "npx" } }
}`)

	// In-jail marker set → LoadConfig must return the snapshot verbatim.
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	cfg, err := LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Get("mcp_servers"); !ok {
		t.Errorf("in-jail LoadConfig must copy the snapshot (with mcp_servers); got keys %v", cfg.Keys())
	}
}

// TestLoadConfigHostStillAssembles confirms the snapshot-copy path is gated on
// the in-jail marker: on the host (no YOLO_VERSION) LoadConfig re-assembles as
// before, so the snapshot is NOT authoritative there.
func TestLoadConfigHostStillAssembles(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "") // host: explicitly empty

	mustWrite(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{ "mcp_presets": ["chrome-devtools"] }`)
	mustWrite(t, filepath.Join(ws, "yolo-jail.jsonc"), `{ "agents": ["pi"] }`)
	// A stale snapshot with a DIFFERENT value must be ignored on the host.
	mustWrite(t, ConfigSnapshotPath(ws), `{ "agents": ["stale-should-be-ignored"] }`)

	cfg, err := LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	agents, _ := cfg.Get("agents")
	list, _ := agents.([]any)
	if len(list) != 1 || list[0] != "pi" {
		t.Errorf("host LoadConfig must assemble from files, not the snapshot; got agents=%v", agents)
	}
}

// TestLoadConfigInJailFallsBackWhenNoSnapshot confirms that inside a jail with
// NO snapshot present (e.g. never run through the approval gate), LoadConfig
// falls back to the normal re-assemble instead of erroring.
func TestLoadConfigInJailFallsBackWhenNoSnapshot(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "9.9.9-test")

	mustWrite(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{ "mcp_presets": ["chrome-devtools"] }`)
	mustWrite(t, filepath.Join(ws, "yolo-jail.jsonc"), `{ "agents": ["claude"] }`)
	// No snapshot file written.

	cfg, err := LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatalf("fallback assemble should not error: %v", err)
	}
	if _, ok := cfg.Get("agents"); !ok {
		t.Errorf("fallback assemble should produce the workspace config; got %v", cfg.Keys())
	}
}

// TestLoadConfigInJailIgnoresNonObjectSnapshot confirms a corrupt/non-object
// snapshot is ignored (fall back to assemble), never returned as config.
func TestLoadConfigInJailIgnoresNonObjectSnapshot(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "9.9.9-test")

	mustWrite(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	mustWrite(t, filepath.Join(ws, "yolo-jail.jsonc"), `{ "agents": ["claude"] }`)
	mustWrite(t, ConfigSnapshotPath(ws), `["not", "an", "object"]`)

	cfg, err := LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Get("agents"); !ok {
		t.Errorf("non-object snapshot must be ignored; expected assembled config, got %v", cfg.Keys())
	}
	// Sanity: the snapshot decode path really did reject the array.
	if _, ok := loadAssembledSnapshot(ws); ok {
		t.Error("loadAssembledSnapshot must reject a JSON array")
	}
}
