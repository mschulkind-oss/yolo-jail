package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
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
	if err := ConfigurePackByName(e, "copilot"); err != nil {
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

	if err := ConfigurePackByName(e, "copilot"); err != nil {
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

	if err := ConfigurePackByName(e, "copilot"); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(cfg)
	if err := ConfigurePackByName(e, "copilot"); err != nil {
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
	if err := ConfigurePackByName(e, "copilot"); err != nil {
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
	if err := ConfigurePackByName(e, "copilot"); err != nil {
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

// COPILOT'S SETTINGS LIVE IN settings.json, AND yolo WRITES THEM THERE. Since 1.0.35 copilot
// keeps its user settings in ~/.copilot/settings.json and, at every start, moves any
// settings-schema key it finds in config.json there, deleting it from config.json (1.0.48
// app.js: "Settings migration: moved ${m} setting(s) from config.json to settings.json").
// `statusLine` is such a key. Written into config.json, yolo's footer default was drained by
// copilot on every start and put back by the next boot, forever
// (docs/design/model-lists-and-pickers.md §14.2, "A side defect for packs/copilot"). The
// default now lands in settings.json, beside the settings copilot or the user keep there, and
// config.json no longer receives it.
func TestCopilotStatusLineDefaultLandsInSettingsJSON(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	dir := filepath.Join(e.Home, ".copilot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark","model":"claude-sonnet-5"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePackByName(e, "copilot"); err != nil {
		t.Fatal(err)
	}
	settings := decodeJSONFile(t, settingsPath)
	line, _ := settings["statusLine"].(map[string]any)
	if cmd, _ := line["command"].(string); line["type"] != "command" || !strings.Contains(cmd, "footer.sh") {
		t.Errorf("settings.json statusLine = %v, want yolo's footer command", settings["statusLine"])
	}
	if settings["theme"] != "dark" || settings["model"] != "claude-sonnet-5" {
		t.Errorf("settings.json lost copilot's own settings: %v", settings)
	}
	cfg := decodeJSONFile(t, filepath.Join(dir, "config.json"))
	if _, present := cfg["statusLine"]; present {
		t.Errorf("config.json still receives statusLine %v — copilot moves it to settings.json "+
			"at every start, so the next boot writes it back", cfg["statusLine"])
	}

	// A footer the user set in settings.json is theirs: yolo's is a DEFAULT.
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"mine.sh"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePackByName(e, "copilot"); err != nil {
		t.Fatal(err)
	}
	if line, _ := decodeJSONFile(t, settingsPath)["statusLine"].(map[string]any); line["command"] != "mine.sh" {
		t.Errorf("settings.json statusLine = %v, want the user's own kept", line)
	}
}

// copilot105Header is the two lines Copilot CLI 1.0.105 writes at the top of its own
// ~/.copilot/config.json (docs/lab-reports/2026-11-03-mac-slice-12a-witness-w1.md §14).
const copilot105Header = "// User settings belong in settings.json.\n// This file is managed automatically.\n"

// A config.json Copilot itself wrote, comment header and all, must be read, get the pack's
// config entry, keep every agent-owned key, and keep its header: before this, every launch warned
// "is not valid JSON … refusing to rewrite it" and the `yolo` entry was never rendered.
// Through the real boot writer, so deleting the codec's header handling fails it.
func TestCopilotConfigWithCopilotsOwnCommentHeaderIsRenderedAndKeepsIt(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	dir := filepath.Join(e.Home, ".copilot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.json")
	live := copilot105Header + `{
  "copilotTokens": {"gh": "SECRET"},
  "trustedFolders": ["/workspace"]
}
`
	if err := os.WriteFile(cfg, []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePackByName(e, "copilot"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), copilot105Header) {
		t.Errorf("the render dropped or moved Copilot's comment header:\n%s", got)
	}
	for _, want := range []string{`"yolo": true`, "copilotTokens", "SECRET", "trustedFolders"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the render of a commented config.json lacks %q:\n%s", want, got)
		}
	}
	if err := ConfigurePackByName(e, "copilot"); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(cfg); string(again) != string(got) {
		t.Errorf("a second render changed the file:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// The header rule reads a LEADING run of whole-line comments only. A comment inside the
// object is still strict JSON's to refuse: the file is left untouched rather than rewritten
// without it. A header-only file is an empty object whose header survives the encode.
func TestJSONSurfaceCommentHeaderIsTheOnlyCommentRead(t *testing.T) {
	s := manifest.Surface{Agent: "copilot", Name: "config", Codec: "json", Path: "~/.copilot/config.json"}
	raw := []byte(copilot105Header + "{\n  // mine\n  \"a\": 1\n}\n")
	if _, err := decodeSurfaceBytes(s, "/x/config.json", raw); err == nil {
		t.Fatal("a comment inside the object was accepted; it would be lost on write")
	} else if _, ok := asRMWRefusal(err); !ok {
		t.Errorf("got %v, want a refusal", err)
	}
	obj, err := decodeSurfaceBytes(s, "/x/config.json", []byte(copilot105Header))
	if err != nil || obj.Len() != 0 {
		t.Fatalf("header-only file = %v, %v; want an empty object", obj, err)
	}
	obj.Set("yolo", true)
	text, err := encodeSurfaceObject(s, obj, []byte(copilot105Header), nil)
	if err != nil || !strings.HasPrefix(text, copilot105Header) || !strings.Contains(text, `"yolo": true`) {
		t.Errorf("encode of a header-only file = %q, %v", text, err)
	}
	// A file with no header gains none.
	if text, _ := encodeSurfaceObject(s, obj, []byte(`{"a":1}`), nil); strings.HasPrefix(text, "//") {
		t.Errorf("a file without comments gained a header: %q", text)
	}
}
