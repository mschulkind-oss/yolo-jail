package agentcfg

import (
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"reflect"
	"testing"
)

// TestBuiltinManifestValid asserts the yolo-shipped manifest passes the
// manifest validator (catches a malformed builtin at test time, not runtime).
func TestBuiltinManifestValid(t *testing.T) {
	m := BuiltinManifest()
	if m.Len() == 0 {
		t.Fatal("builtin manifest is empty")
	}
	s, ok := m.Lookup("pi", "settings")
	if !ok {
		t.Fatal("builtin manifest missing pi/settings")
	}
	if s.Codec != "json" {
		t.Errorf("pi/settings codec = %q, want json", s.Codec)
	}
	if s.ManagedMap()["defaultProjectTrust"] != "always" {
		t.Errorf("pi/settings should enforce defaultProjectTrust=always, got %v", s.ManagedMap()["defaultProjectTrust"])
	}
}

// TestBuiltinClaudeSettingsSurface asserts claude/settings is in the manifest
// with the json codec at the right path and the static force-managed keys the
// bespoke ConfigureClaude asserts (internal/entrypoint/claude.go): the YOLO
// permissions posture, skipDangerousModePermissionPrompt, and the disabled
// auto-updater preference.
func TestBuiltinClaudeSettingsSurface(t *testing.T) {
	m := BuiltinManifest()
	s, ok := m.Lookup("claude", "settings")
	if !ok {
		t.Fatal("builtin manifest missing claude/settings")
	}
	if s.Codec != "json" {
		t.Errorf("claude/settings codec = %q, want json", s.Codec)
	}
	if s.Path != "~/.claude/settings.json" {
		t.Errorf("claude/settings path = %q, want ~/.claude/settings.json", s.Path)
	}
	if s.ManagedMap()["skipDangerousModePermissionPrompt"] != true {
		t.Errorf("claude/settings should enforce skipDangerousModePermissionPrompt=true, got %v", s.ManagedMap()["skipDangerousModePermissionPrompt"])
	}
	perms, ok := s.ManagedMap()["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("claude/settings managed permissions not an object: %T", s.ManagedMap()["permissions"])
	}
	if perms["defaultMode"] != "acceptEdits" {
		t.Errorf("permissions.defaultMode = %v, want acceptEdits", perms["defaultMode"])
	}
	if !reflect.DeepEqual(perms["allow"], []any{}) {
		t.Errorf("permissions.allow = %#v, want []", perms["allow"])
	}
	if !reflect.DeepEqual(perms["deny"], []any{}) {
		t.Errorf("permissions.deny = %#v, want []", perms["deny"])
	}
	if !reflect.DeepEqual(perms["additionalDirectories"], []any{"/"}) {
		t.Errorf("permissions.additionalDirectories = %#v, want [/]", perms["additionalDirectories"])
	}
	prefs, ok := s.ManagedMap()["preferences"].(map[string]any)
	if !ok {
		t.Fatalf("claude/settings managed preferences not an object: %T", s.ManagedMap()["preferences"])
	}
	if prefs["autoUpdaterStatus"] != "disabled" {
		t.Errorf("preferences.autoUpdaterStatus = %v, want disabled", prefs["autoUpdaterStatus"])
	}
}

// TestBuiltinClaudeConfigSurface asserts claude/config (.claude.json) is present
// with the json codec, the managed workspace-project MCP-enable key, and the
// user-overridable trust-dialog default.
func TestBuiltinClaudeConfigSurface(t *testing.T) {
	m := BuiltinManifest()
	s, ok := m.Lookup("claude", "config")
	if !ok {
		t.Fatal("builtin manifest missing claude/config")
	}
	if s.Codec != "json" {
		t.Errorf("claude/config codec = %q, want json", s.Codec)
	}
	if s.Path != "~/.claude.json" {
		t.Errorf("claude/config path = %q, want ~/.claude.json", s.Path)
	}
	mProj, ok := s.ManagedMap()["projects"].(map[string]any)
	if !ok {
		t.Fatalf("claude/config managed projects not an object: %T", s.ManagedMap()["projects"])
	}
	// A11: the manifest holds the ${workspace} placeholder, not a jail literal.
	ws, ok := mProj[WorkspacePlaceholder].(map[string]any)
	if !ok {
		t.Fatalf("claude/config managed projects[%s] not an object: %T",
			WorkspacePlaceholder, mProj[WorkspacePlaceholder])
	}
	if ws["enableAllProjectMcpServers"] != true {
		t.Errorf("managed projects[/workspace].enableAllProjectMcpServers = %v, want true", ws["enableAllProjectMcpServers"])
	}
	dProj, ok := s.DefaultsMap()["projects"].(map[string]any)
	if !ok {
		t.Fatalf("claude/config default projects not an object: %T", s.DefaultsMap()["projects"])
	}
	dws, ok := dProj[WorkspacePlaceholder].(map[string]any)
	if !ok {
		t.Fatalf("claude/config default projects[%s] not an object: %T",
			WorkspacePlaceholder, dProj[WorkspacePlaceholder])
	}
	if dws["hasTrustDialogAccepted"] != true {
		t.Errorf("default projects[%s].hasTrustDialogAccepted = %v, want true",
			WorkspacePlaceholder, dws["hasTrustDialogAccepted"])
	}
}

// TestBuiltinCopilotConfigSurface asserts copilot/config (config.json) is in the
// manifest with the json codec at the right path, and — the §7 subtlety — that
// yolo:true is a USER-OVERRIDABLE DEFAULT (the bespoke write-if-absent), not a
// force-managed key. ConfigureCopilot never overwrites an existing config.json,
// so a managed yolo:true would misrepresent the port.
func TestBuiltinCopilotConfigSurface(t *testing.T) {
	m := BuiltinManifest()
	s, ok := m.Lookup("copilot", "config")
	if !ok {
		t.Fatal("builtin manifest missing copilot/config")
	}
	if s.Codec != "json" {
		t.Errorf("copilot/config codec = %q, want json", s.Codec)
	}
	if s.Path != "~/.copilot/config.json" {
		t.Errorf("copilot/config path = %q, want ~/.copilot/config.json", s.Path)
	}
	if s.DefaultsMap()["yolo"] != true {
		t.Errorf("copilot/config should default yolo=true, got %v", s.DefaultsMap()["yolo"])
	}
	// yolo:true must NOT be force-managed (write-if-absent = default semantics).
	if _, present := s.ManagedMap()["yolo"]; present {
		t.Error("copilot/config yolo must be a DEFAULT, not managed (write-if-absent semantics)")
	}
}

// TestBuiltinOpencodeConfigSurface asserts opencode/config (opencode.json) is in
// the manifest with the json codec at the right path, and that the two static
// keys land in the correct layers per the bespoke ConfigureOpencode
// (internal/entrypoint/agent_configs.go): $schema is a USER-OVERRIDABLE DEFAULT
// (bespoke setDefault) while permission="allow" is FORCE-MANAGED (bespoke .Set).
// It also pins the documented MCP gap: the dynamic "mcp" block is a transform,
// NOT static data, so it must appear in neither the defaults nor managed layer.
func TestBuiltinOpencodeConfigSurface(t *testing.T) {
	m := BuiltinManifest()
	s, ok := m.Lookup("opencode", "config")
	if !ok {
		t.Fatal("builtin manifest missing opencode/config")
	}
	if s.Codec != "json" {
		t.Errorf("opencode/config codec = %q, want json", s.Codec)
	}
	if s.Path != "~/.config/opencode/opencode.json" {
		t.Errorf("opencode/config path = %q, want ~/.config/opencode/opencode.json", s.Path)
	}

	// $schema is a DEFAULT (user-overridable), not managed.
	if s.DefaultsMap()["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("opencode/config should default $schema, got %v", s.DefaultsMap()["$schema"])
	}
	if _, present := s.ManagedMap()["$schema"]; present {
		t.Error("opencode/config $schema must be a DEFAULT, not managed (setDefault semantics)")
	}

	// permission="allow" is FORCE-MANAGED, not a default.
	if s.ManagedMap()["permission"] != "allow" {
		t.Errorf("opencode/config should enforce permission=allow, got %v", s.ManagedMap()["permission"])
	}
	if _, present := s.DefaultsMap()["permission"]; present {
		t.Error("opencode/config permission must be MANAGED, not a default (.Set semantics)")
	}

	// Documented MCP gap: the dynamic mcp translation is a transform-shaped
	// concern, so "mcp" must not be baked into either static layer.
	if _, present := s.DefaultsMap()["mcp"]; present {
		t.Error("opencode/config must NOT bake mcp into defaults (it is a dynamic transform)")
	}
	if _, present := s.ManagedMap()["mcp"]; present {
		t.Error("opencode/config must NOT bake mcp into managed (it is a dynamic transform)")
	}
}

// TestBuiltinCodexConfigSurface asserts codex/config (config.toml) is in the
// manifest with the TOML codec at the right path, and the two static
// force-managed scalars the bespoke ConfigureCodex asserts
// (internal/entrypoint/codex.go): approval_policy="never" and
// sandbox_mode="danger-full-access", both written with .Set (force-managed).
// ConfigureCodex has no setDefault keys, so Defaults must be empty; the dynamic
// mcp_servers block is a transform and must appear in neither static layer.
func TestBuiltinCodexConfigSurface(t *testing.T) {
	m := BuiltinManifest()
	s, ok := m.Lookup("codex", "config")
	if !ok {
		t.Fatal("builtin manifest missing codex/config")
	}
	if s.Codec != "toml" {
		t.Errorf("codex/config codec = %q, want toml", s.Codec)
	}
	if s.Path != "~/.codex/config.toml" {
		t.Errorf("codex/config path = %q, want ~/.codex/config.toml", s.Path)
	}

	// Both scalars are FORCE-MANAGED (.Set semantics).
	if s.ManagedMap()["approval_policy"] != "never" {
		t.Errorf("codex/config should enforce approval_policy=never, got %v", s.ManagedMap()["approval_policy"])
	}
	if s.ManagedMap()["sandbox_mode"] != "danger-full-access" {
		t.Errorf("codex/config should enforce sandbox_mode=danger-full-access, got %v", s.ManagedMap()["sandbox_mode"])
	}
	// No setDefault keys in ConfigureCodex — Defaults is empty.
	if len(s.DefaultsMap()) != 0 {
		t.Errorf("codex/config Defaults should be empty, got %#v", s.Defaults)
	}
	// The managed scalars must not leak into the defaults layer.
	if _, present := s.DefaultsMap()["approval_policy"]; present {
		t.Error("codex/config approval_policy must be MANAGED, not a default (.Set semantics)")
	}
	if _, present := s.DefaultsMap()["sandbox_mode"]; present {
		t.Error("codex/config sandbox_mode must be MANAGED, not a default (.Set semantics)")
	}
	// Documented MCP gap: the dynamic mcp_servers translation is a transform, so
	// it must not be baked into either static layer.
	if _, present := s.DefaultsMap()["mcp_servers"]; present {
		t.Error("codex/config must NOT bake mcp_servers into defaults (it is a dynamic transform)")
	}
	if _, present := s.ManagedMap()["mcp_servers"]; present {
		t.Error("codex/config must NOT bake mcp_servers into managed (it is a dynamic transform)")
	}
}

// TestBuiltinAgySettingsSurface asserts agy/settings (settings.json) is in the
// manifest with the json codec at agy's antigravity-cli path, and the single
// force-managed key permissionMode="allow" (the YOLO posture). agy has NO host
// mount and no bespoke writer — it is born on the prism (docs/plans/
// antigravity-agy-support.md) — so Defaults is empty and the dynamic
// mcp_config.json (a separate sibling) must not leak into either static layer.
func TestBuiltinAgySettingsSurface(t *testing.T) {
	m := BuiltinManifest()
	s, ok := m.Lookup("agy", "settings")
	if !ok {
		t.Fatal("builtin manifest missing agy/settings")
	}
	if s.Codec != "json" {
		t.Errorf("agy/settings codec = %q, want json", s.Codec)
	}
	if s.Path != "~/.gemini/antigravity-cli/settings.json" {
		t.Errorf("agy/settings path = %q, want ~/.gemini/antigravity-cli/settings.json", s.Path)
	}
	// permissionMode is FORCE-MANAGED (the container is the sandbox).
	if s.ManagedMap()["permissionMode"] != "allow" {
		t.Errorf("agy/settings should enforce permissionMode=allow, got %v", s.ManagedMap()["permissionMode"])
	}
	// No setDefault keys — Defaults is empty (yolo owns the file outright).
	if len(s.DefaultsMap()) != 0 {
		t.Errorf("agy/settings Defaults should be empty, got %#v", s.Defaults)
	}
	// The dynamic mcp_config.json is a separate pure-overwrite sibling, not a
	// manifest layer — it must not be baked into settings.
	if _, present := s.ManagedMap()["mcpServers"]; present {
		t.Error("agy/settings must NOT bake mcpServers into managed (it is a separate dynamic sibling)")
	}
}

// TestBuiltinCopilotMCPLSPSurfaces pins copilot's two dynamic sibling surfaces
// (mcp-config.json / lsp-config.json). Both are json-codec, at the copilot paths,
// and carry ONLY an empty-wrapper Default (the full table is the boot-time
// computed layer). No Managed (yolo forces no individual server), and the wrapper
// key must be a DEFAULT (so it deep-merges UNDER the computed table, never
// suppressing a real server), never Managed.
func TestBuiltinCopilotMCPLSPSurfaces(t *testing.T) {
	m := BuiltinManifest()

	mcp, ok := m.Lookup("copilot", "mcp")
	if !ok {
		t.Fatal("builtin manifest missing copilot/mcp")
	}
	if mcp.Codec != "json" {
		t.Errorf("copilot/mcp codec = %q, want json", mcp.Codec)
	}
	if mcp.Path != "~/.copilot/mcp-config.json" {
		t.Errorf("copilot/mcp path = %q, want ~/.copilot/mcp-config.json", mcp.Path)
	}
	if _, ok := mcp.DefaultsMap()["mcpServers"].(map[string]any); !ok {
		t.Errorf("copilot/mcp should default an empty mcpServers wrapper, got %#v", mcp.DefaultsMap()["mcpServers"])
	}
	if len(mcp.ManagedMap()) != 0 {
		t.Errorf("copilot/mcp Managed should be empty (yolo forces no server), got %#v", mcp.Managed)
	}

	lsp, ok := m.Lookup("copilot", "lsp")
	if !ok {
		t.Fatal("builtin manifest missing copilot/lsp")
	}
	if lsp.Codec != "json" {
		t.Errorf("copilot/lsp codec = %q, want json", lsp.Codec)
	}
	if lsp.Path != "~/.copilot/lsp-config.json" {
		t.Errorf("copilot/lsp path = %q, want ~/.copilot/lsp-config.json", lsp.Path)
	}
	if _, ok := lsp.DefaultsMap()["lspServers"].(map[string]any); !ok {
		t.Errorf("copilot/lsp should default an empty lspServers wrapper, got %#v", lsp.DefaultsMap()["lspServers"])
	}
	if len(lsp.ManagedMap()) != 0 {
		t.Errorf("copilot/lsp Managed should be empty, got %#v", lsp.Managed)
	}
}

// TestBuiltinAgyMCPSurface pins agy's dynamic mcp_config.json sibling: json
// codec, agy's antigravity-cli path (distinct from copilot/mcp), and the same
// empty-wrapper Default / empty Managed shape.
func TestBuiltinAgyMCPSurface(t *testing.T) {
	m := BuiltinManifest()
	s, ok := m.Lookup("agy", "mcp")
	if !ok {
		t.Fatal("builtin manifest missing agy/mcp")
	}
	if s.Codec != "json" {
		t.Errorf("agy/mcp codec = %q, want json", s.Codec)
	}
	if s.Path != "~/.gemini/antigravity-cli/mcp_config.json" {
		t.Errorf("agy/mcp path = %q, want ~/.gemini/antigravity-cli/mcp_config.json", s.Path)
	}
	if _, ok := s.DefaultsMap()["mcpServers"].(map[string]any); !ok {
		t.Errorf("agy/mcp should default an empty mcpServers wrapper, got %#v", s.DefaultsMap()["mcpServers"])
	}
	if len(s.ManagedMap()) != 0 {
		t.Errorf("agy/mcp Managed should be empty, got %#v", s.Managed)
	}
}

// TestBuiltinMiseConfigSurface pins the mise global-config surface (§4.1): the
// TOML codec, the ~/.config/mise/config.toml path, and — crucially — that the
// static surface is EMPTY (no Defaults, no Managed). The [tools] table is
// entirely dynamic (the YOLO_MISE_TOOLS computed layer at boot), so baking any
// runtime into a static layer here would resurrect the very stale-shadow bug
// the port exists to kill.
func TestBuiltinMiseConfigSurface(t *testing.T) {
	m := BuiltinManifest()
	s, ok := m.Lookup("mise", "config")
	if !ok {
		t.Fatal("builtin manifest missing mise/config")
	}
	if s.Codec != "toml" {
		t.Errorf("mise/config codec = %q, want toml", s.Codec)
	}
	if s.Path != "~/.config/mise/config.toml" {
		t.Errorf("mise/config path = %q, want ~/.config/mise/config.toml", s.Path)
	}
	// The surface is override-only: NO default runtime and NO managed key. All
	// tool content is the dynamic YOLO_MISE_TOOLS computed layer.
	if len(s.DefaultsMap()) != 0 {
		t.Errorf("mise/config Defaults should be empty (mise is override-only), got %#v", s.Defaults)
	}
	if len(s.ManagedMap()) != 0 {
		t.Errorf("mise/config Managed should be empty (yolo asserts no mise key), got %#v", s.Managed)
	}
}

// A11: surface DATA must not hardcode the jail's workspace path. claude/config
// asserts keys under projects[<workspace>], and the workspace root is NOT always
// "/workspace" — Env.WorkspaceDir() honors YOLO_WORKSPACE, and macos-user has no
// /workspace at all. The manifest therefore carries the ${workspace} placeholder
// and the render substitutes it, which is also what lets these surfaces become
// pack data later (a pack cannot ship a jail-specific literal).
func TestClaudeConfigUsesWorkspacePlaceholder(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("claude", "config")
	if !ok {
		t.Fatal("builtin manifest missing claude/config")
	}
	for _, layer := range []struct {
		name string
		m    map[string]any
	}{{"managed", s.ManagedMap()}, {"defaults", s.DefaultsMap()}} {
		proj, ok := layer.m["projects"].(map[string]any)
		if !ok {
			t.Fatalf("%s projects not an object: %T", layer.name, layer.m["projects"])
		}
		if _, bad := proj["/workspace"]; bad {
			t.Errorf("%s projects still hardcodes the literal /workspace", layer.name)
		}
		if _, ok := proj[WorkspacePlaceholder]; !ok {
			t.Errorf("%s projects missing the %s key: %v", layer.name, WorkspacePlaceholder, proj)
		}
	}
}

// The substitution must rewrite the placeholder key to the real workspace root,
// and must leave a surface that does not use it untouched.
func TestSubstituteWorkspaceRewritesKeys(t *testing.T) {
	s, _ := BuiltinManifest().Lookup("claude", "config")
	got := SubstituteWorkspace(s, "/home/me/proj")

	proj := got.ManagedMap()["projects"].(map[string]any)
	if _, ok := proj["/home/me/proj"]; !ok {
		t.Errorf("managed projects not substituted: %v", proj)
	}
	if _, bad := proj[WorkspacePlaceholder]; bad {
		t.Errorf("placeholder survived substitution: %v", proj)
	}
	// The original manifest surface must be unchanged (no aliasing).
	orig, _ := BuiltinManifest().Lookup("claude", "config")
	if _, ok := orig.ManagedMap()["projects"].(map[string]any)[WorkspacePlaceholder]; !ok {
		t.Error("SubstituteWorkspace mutated the shared manifest surface")
	}

	// A surface with no placeholder is returned as-is.
	pi, _ := BuiltinManifest().Lookup("pi", "settings")
	if !reflect.DeepEqual(SubstituteWorkspace(pi, "/x"), pi) {
		t.Error("a surface without the placeholder must be unchanged")
	}
}

// D3: a pack-declared surface must compose exactly like a builtin, and must be able to
// OVERRIDE one. This is the end of the seam — data in, composed file out — and it is
// what makes "agent support as pack data" mechanically possible rather than aspirational.
func TestManifestWithComposesPackDeclaredSurface(t *testing.T) {
	extra, problems := manifest.DecodeSurfaces([]byte(`[
	  {"agent":"acme","name":"settings","path":"~/.acme/settings.json","codec":"json",
	   "defaults":{"theme":"dark"},"managed":{"telemetry":false}}
	]`))
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	m, err := ManifestWith(extra...)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := m.Lookup("acme", "settings")
	if !ok {
		t.Fatal("pack surface not in the merged manifest")
	}
	// It composes through the ordinary engine: managed wins over a host value, a
	// default yields to one.
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(`{"theme":"light","telemetry":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	cfg := res.ConfigMap()
	if cfg["telemetry"] != false {
		t.Errorf("managed key did not win: %v", cfg["telemetry"])
	}
	if cfg["theme"] != "light" {
		t.Errorf("default should yield to the host value: %v", cfg["theme"])
	}
	// Every builtin still resolves.
	if _, ok := m.Lookup("claude", "settings"); !ok {
		t.Error("merging a pack surface dropped a builtin")
	}
}

// A pack may REPLACE a builtin surface — that is the override path an official-pack
// world needs, and it must still be validated.
func TestManifestWithOverridesBuiltin(t *testing.T) {
	extra, _ := manifest.DecodeSurfaces([]byte(`[
	  {"agent":"codex","name":"config","path":"~/.codex/config.toml","codec":"toml",
	   "mode":"computed","managed":{"approval_policy":"overridden"}}
	]`))
	m, err := ManifestWith(extra...)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := m.Lookup("codex", "config")
	mg, _ := s.ManagedMap()["approval_policy"].(string)
	if mg != "overridden" {
		t.Errorf("pack did not override the builtin: %v", s.ManagedMap())
	}
	if s.ResolvedMode() != manifest.ModeComputed {
		t.Errorf("override's mode not applied: %q", s.ResolvedMode())
	}
	// No duplicate: the override replaced rather than added.
	count := 0
	for _, x := range m.Surfaces() {
		if x.Agent == "codex" && x.Name == "config" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one codex/config, got %d", count)
	}
}
