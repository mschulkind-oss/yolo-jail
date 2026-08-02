package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
	// The short-circuit only applies to the jail's OWN workspace (see
	// jailOwnWorkspace), so point the marker at this temp dir — that is what makes
	// ws "the workspace this jail was launched for" rather than some other one.
	t.Setenv("YOLO_WORKSPACE", ws)
	// The jail has a mounted config.jsonc whose include_if_found points at an
	// overrides.jsonc that does NOT exist in the jail (it's host-only). A
	// re-assemble would therefore drop mcp_servers.
	mustWrite(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{ "include_if_found": ["overrides.jsonc"], "mcp_presets": ["chrome-devtools"] }`)
	mustWrite(t, filepath.Join(ws, "yolo-jail.jsonc"), `{ "packages": ["ripgrep"] }`)
	// The host wrote a snapshot WITH the assembled mcp_servers.
	mustWrite(t, ConfigSnapshotPath(ws), `{
  "packages": ["ripgrep"],
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
	mustWrite(t, filepath.Join(ws, "yolo-jail.jsonc"), `{ "packages": ["ripgrep"] }`)
	// A stale snapshot with a DIFFERENT value must be ignored on the host.
	mustWrite(t, ConfigSnapshotPath(ws), `{ "packages": ["stale-should-be-ignored"] }`)

	cfg, err := LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	pkgs, _ := cfg.Get("packages")
	list, _ := pkgs.([]any)
	if len(list) != 1 || list[0] != "ripgrep" {
		t.Errorf("host LoadConfig must assemble from files, not the snapshot; got packages=%v", pkgs)
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
	mustWrite(t, filepath.Join(ws, "yolo-jail.jsonc"), `{ "packages": ["ripgrep"] }`)
	// No snapshot file written.

	cfg, err := LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatalf("fallback assemble should not error: %v", err)
	}
	if _, ok := cfg.Get("packages"); !ok {
		t.Errorf("fallback assemble should produce the workspace config; got %v", cfg.Keys())
	}
}

// TestLoadConfigInJailOtherWorkspaceAssembles is the regression for "a workspace
// config edit never took effect for a nested launch".
//
// The snapshot short-circuit exists for the jail's OWN workspace, whose snapshot the
// host wrote for this launch. When an in-jail CLI launches a jail for a DIFFERENT
// workspace — every nested launch, and every integration test — the short-circuit
// used to fire anyway and return that workspace's PREVIOUS snapshot, so the edited
// yolo-jail.jsonc was never read. Live symptom: dropping a tool from `blocked_tools`
// left its shim generated forever, because shims render from the config LoadConfig
// returns (integration TestShimPersistence). It also silently disabled
// CheckConfigChanges, which diffs the live config against that same snapshot and so
// compared it to itself.
//
// Off the own workspace the short-circuit's own rationale does not apply either: the
// user config IS readable there, so assembling drops no include_if_found override.
func TestLoadConfigInJailOtherWorkspaceAssembles(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	// In-jail, but this jail owns some OTHER workspace — so ws's snapshot was not
	// written for this launch and must not outrank ws's files.
	t.Setenv("YOLO_WORKSPACE", filepath.Join(home, "owned-workspace"))

	mustWrite(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	// The live workspace config says curl is UNBLOCKED...
	mustWrite(t, filepath.Join(ws, "yolo-jail.jsonc"), `{ "security": { "blocked_tools": [] } }`)
	// ...while the stale snapshot from a previous launch still blocks it.
	mustWrite(t, ConfigSnapshotPath(ws), `{ "security": { "blocked_tools": ["curl"] } }`)

	cfg, err := LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	sec, _ := cfg.Get("security")
	secMap, ok := sec.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("security section missing or not an object: %#v", sec)
	}
	blocked, _ := secMap.Get("blocked_tools")
	list, _ := blocked.([]any)
	if len(list) != 0 {
		t.Errorf("another workspace's snapshot outranked its live config: blocked_tools=%v, want empty "+
			"(a config edit must take effect for a nested/other-workspace launch)", list)
	}
}

// TestLoadConfigInJailIgnoresNonObjectSnapshot confirms a corrupt/non-object
// snapshot is ignored (fall back to assemble), never returned as config.
func TestLoadConfigInJailIgnoresNonObjectSnapshot(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	t.Setenv("YOLO_WORKSPACE", ws) // the own-workspace case this test is about

	mustWrite(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	mustWrite(t, filepath.Join(ws, "yolo-jail.jsonc"), `{ "packages": ["ripgrep"] }`)
	mustWrite(t, ConfigSnapshotPath(ws), `["not", "an", "object"]`)

	cfg, err := LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Get("packages"); !ok {
		t.Errorf("non-object snapshot must be ignored; expected assembled config, got %v", cfg.Keys())
	}
	// Sanity: the snapshot decode path really did reject the array.
	if _, ok := loadAssembledSnapshot(ws); ok {
		t.Error("loadAssembledSnapshot must reject a JSON array")
	}
}
