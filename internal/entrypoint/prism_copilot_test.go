package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B1 end-to-end through the real BOOT WRITER (not just the engine): a live
// copilot config.json with an OAuth token, and no last_render sidecar — the exact
// data-loss scenario. ConfigureCopilotPrism must preserve the token.
func TestB1CopilotBootPreservesTokenOnFirstMigration(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	dir := filepath.Join(e.Home, ".copilot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"copilot_tokens":{"gh":"SECRET"},"logged_in_users":["ada"],"last_logged_in_user":"ada"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"copilot_tokens", "SECRET", "logged_in_users", "last_logged_in_user"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("first-migration boot lost %q — the OAuth wipe:\n%s", want, got)
		}
	}
	if !strings.Contains(string(got), `"yolo": true`) && !strings.Contains(string(got), `"yolo":true`) {
		t.Errorf("yolo default missing:\n%s", got)
	}
}

// B2: copilot/config is READ-MODIFY-WRITE, not composed. Two things must hold, and
// the second is the point of the change: the agent's live OAuth state survives, and
// the token NEVER reaches the capture sidecars — the overlay lives in
// <workspace>/.yolo/prism/, which a user may commit to git.
func TestCopilotConfigIsRMWAndKeepsTokensOutOfSidecars(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	dir := filepath.Join(e.Home, ".copilot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.json")
	live := `{"copilot_tokens":{"gh":"SECRET"},"logged_in_users":["ada"],"theme":"dark"}`
	if err := os.WriteFile(cfg, []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"copilot_tokens", "SECRET", "logged_in_users", "theme"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("RMW lost agent-owned %q:\n%s", want, got)
		}
	}
	// yolo's own default is still asserted.
	if !strings.Contains(string(got), `"yolo"`) {
		t.Errorf("yolo default not asserted:\n%s", got)
	}

	// No sidecars at all: the secret is off the capture path.
	for _, p := range []string{
		prismOverlayPath(e, "copilot", "config"),
		prismLastRenderPath(e, "copilot", "config"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			data, _ := os.ReadFile(p)
			t.Errorf("RMW surface must write no sidecar; %s exists with:\n%s", filepath.Base(p), data)
		}
	}
}

// A second boot must be idempotent and must not clobber a value the agent changed
// for a key yolo only DEFAULTS.
func TestCopilotRMWIsIdempotentAndRespectsAgentChoice(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	if err := os.MkdirAll(filepath.Join(e.Home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(e.Home, ".copilot", "config.json")

	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(cfg)
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(cfg); string(second) != string(first) {
		t.Errorf("RMW is not idempotent:\nfirst:  %s\nsecond: %s", first, second)
	}

	// The agent turns yolo's default off; yolo must not force it back (it is a
	// DEFAULT, not managed).
	if err := os.WriteFile(cfg, []byte(`{"yolo":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(cfg); !strings.Contains(string(data), "false") {
		t.Errorf("a DEFAULT must not overwrite the agent's own value:\n%s", data)
	}
}

// F7 boundary: RMW asserts a STATIC key set and cannot express a REMOVAL from a
// dynamic table. This pins the limit so nobody moves a dynamic-table surface onto it
// expecting removals to work.
//
// With no record of what yolo asserted last boot, "the agent added this key" and
// "yolo added it and config has since dropped it" are indistinguishable on disk — so
// RMW preserves an unknown key, which is right for agent state and WRONG for a stale
// yolo-owned entry. claude/config keeps its bespoke writer and managed-MCP sidecar for
// exactly this reason.
func TestRMWPreservesUnknownKeysAndCannotExpressRemoval(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	dir := filepath.Join(e.Home, ".copilot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.json")
	// A key that LOOKS like stale yolo output sitting next to real agent state.
	if err := os.WriteFile(cfg,
		[]byte(`{"yolo":true,"looks_stale":"?","copilot_tokens":{"gh":"t"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, cfg)
	// Both are preserved, because RMW cannot tell them apart. That is the documented
	// trade: keeping a dead key is cosmetic, dropping a live one loses a token.
	for _, want := range []string{"looks_stale", "copilot_tokens"} {
		if !strings.Contains(got, want) {
			t.Errorf("RMW dropped %q; it must preserve unknown keys:\n%s", want, got)
		}
	}
}
