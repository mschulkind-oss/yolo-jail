package agentcfg

import (
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
	if s.Managed["defaultProjectTrust"] != "always" {
		t.Errorf("pi/settings should enforce defaultProjectTrust=always, got %v", s.Managed["defaultProjectTrust"])
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
	if s.Managed["skipDangerousModePermissionPrompt"] != true {
		t.Errorf("claude/settings should enforce skipDangerousModePermissionPrompt=true, got %v", s.Managed["skipDangerousModePermissionPrompt"])
	}
	perms, ok := s.Managed["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("claude/settings managed permissions not an object: %T", s.Managed["permissions"])
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
	prefs, ok := s.Managed["preferences"].(map[string]any)
	if !ok {
		t.Fatalf("claude/settings managed preferences not an object: %T", s.Managed["preferences"])
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
	mProj, ok := s.Managed["projects"].(map[string]any)
	if !ok {
		t.Fatalf("claude/config managed projects not an object: %T", s.Managed["projects"])
	}
	ws, ok := mProj["/workspace"].(map[string]any)
	if !ok {
		t.Fatalf("claude/config managed projects[/workspace] not an object: %T", mProj["/workspace"])
	}
	if ws["enableAllProjectMcpServers"] != true {
		t.Errorf("managed projects[/workspace].enableAllProjectMcpServers = %v, want true", ws["enableAllProjectMcpServers"])
	}
	dProj, ok := s.Defaults["projects"].(map[string]any)
	if !ok {
		t.Fatalf("claude/config default projects not an object: %T", s.Defaults["projects"])
	}
	dws, ok := dProj["/workspace"].(map[string]any)
	if !ok {
		t.Fatalf("claude/config default projects[/workspace] not an object: %T", dProj["/workspace"])
	}
	if dws["hasTrustDialogAccepted"] != true {
		t.Errorf("default projects[/workspace].hasTrustDialogAccepted = %v, want true", dws["hasTrustDialogAccepted"])
	}
}

// TestBuiltinGeminiSettingsSurface asserts gemini/settings is in the manifest
// with the json codec at the right path, and — the §7 subtlety — that the
// security posture (approvalMode / enablePermanentToolApproval) is modeled as
// USER-OVERRIDABLE DEFAULTS (bespoke setDefault) while the auto-update disables
// are FORCE-MANAGED (bespoke .Set). A default in managed or vice versa is the
// exact mistake this test guards against.
func TestBuiltinGeminiSettingsSurface(t *testing.T) {
	m := BuiltinManifest()
	s, ok := m.Lookup("gemini", "settings")
	if !ok {
		t.Fatal("builtin manifest missing gemini/settings")
	}
	if s.Codec != "json" {
		t.Errorf("gemini/settings codec = %q, want json", s.Codec)
	}
	if s.Path != "~/.gemini/settings.json" {
		t.Errorf("gemini/settings path = %q, want ~/.gemini/settings.json", s.Path)
	}

	// security.* is a DEFAULT (user-overridable), not managed.
	sec, ok := s.Defaults["security"].(map[string]any)
	if !ok {
		t.Fatalf("gemini/settings default security not an object: %T", s.Defaults["security"])
	}
	if sec["approvalMode"] != "yolo" {
		t.Errorf("default security.approvalMode = %v, want yolo", sec["approvalMode"])
	}
	if sec["enablePermanentToolApproval"] != true {
		t.Errorf("default security.enablePermanentToolApproval = %v, want true", sec["enablePermanentToolApproval"])
	}
	// The security posture must NOT leak into the managed layer (the §7 bug: a
	// setDefault posture must stay a default, or it silently changes behavior).
	if _, present := s.Managed["security"]; present {
		t.Error("gemini/settings security must be a DEFAULT, not managed (setDefault semantics)")
	}

	// general.* is FORCE-MANAGED, not a default.
	gen, ok := s.Managed["general"].(map[string]any)
	if !ok {
		t.Fatalf("gemini/settings managed general not an object: %T", s.Managed["general"])
	}
	if gen["enableAutoUpdate"] != false {
		t.Errorf("managed general.enableAutoUpdate = %v, want false", gen["enableAutoUpdate"])
	}
	if gen["enableAutoUpdateNotification"] != false {
		t.Errorf("managed general.enableAutoUpdateNotification = %v, want false", gen["enableAutoUpdateNotification"])
	}
	if _, present := s.Defaults["general"]; present {
		t.Error("gemini/settings general must be MANAGED, not a default (.Set semantics)")
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
	if s.Defaults["yolo"] != true {
		t.Errorf("copilot/config should default yolo=true, got %v", s.Defaults["yolo"])
	}
	// yolo:true must NOT be force-managed (write-if-absent = default semantics).
	if _, present := s.Managed["yolo"]; present {
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
	if s.Defaults["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("opencode/config should default $schema, got %v", s.Defaults["$schema"])
	}
	if _, present := s.Managed["$schema"]; present {
		t.Error("opencode/config $schema must be a DEFAULT, not managed (setDefault semantics)")
	}

	// permission="allow" is FORCE-MANAGED, not a default.
	if s.Managed["permission"] != "allow" {
		t.Errorf("opencode/config should enforce permission=allow, got %v", s.Managed["permission"])
	}
	if _, present := s.Defaults["permission"]; present {
		t.Error("opencode/config permission must be MANAGED, not a default (.Set semantics)")
	}

	// Documented MCP gap: the dynamic mcp translation is a transform-shaped
	// concern, so "mcp" must not be baked into either static layer.
	if _, present := s.Defaults["mcp"]; present {
		t.Error("opencode/config must NOT bake mcp into defaults (it is a dynamic transform)")
	}
	if _, present := s.Managed["mcp"]; present {
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
	if s.Managed["approval_policy"] != "never" {
		t.Errorf("codex/config should enforce approval_policy=never, got %v", s.Managed["approval_policy"])
	}
	if s.Managed["sandbox_mode"] != "danger-full-access" {
		t.Errorf("codex/config should enforce sandbox_mode=danger-full-access, got %v", s.Managed["sandbox_mode"])
	}
	// No setDefault keys in ConfigureCodex — Defaults is empty.
	if len(s.Defaults) != 0 {
		t.Errorf("codex/config Defaults should be empty, got %#v", s.Defaults)
	}
	// The managed scalars must not leak into the defaults layer.
	if _, present := s.Defaults["approval_policy"]; present {
		t.Error("codex/config approval_policy must be MANAGED, not a default (.Set semantics)")
	}
	if _, present := s.Defaults["sandbox_mode"]; present {
		t.Error("codex/config sandbox_mode must be MANAGED, not a default (.Set semantics)")
	}
	// Documented MCP gap: the dynamic mcp_servers translation is a transform, so
	// it must not be baked into either static layer.
	if _, present := s.Defaults["mcp_servers"]; present {
		t.Error("codex/config must NOT bake mcp_servers into defaults (it is a dynamic transform)")
	}
	if _, present := s.Managed["mcp_servers"]; present {
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
	if s.Managed["permissionMode"] != "allow" {
		t.Errorf("agy/settings should enforce permissionMode=allow, got %v", s.Managed["permissionMode"])
	}
	// No setDefault keys — Defaults is empty (yolo owns the file outright).
	if len(s.Defaults) != 0 {
		t.Errorf("agy/settings Defaults should be empty, got %#v", s.Defaults)
	}
	// The dynamic mcp_config.json is a separate pure-overwrite sibling, not a
	// manifest layer — it must not be baked into settings.
	if _, present := s.Managed["mcpServers"]; present {
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
	if _, ok := mcp.Defaults["mcpServers"].(map[string]any); !ok {
		t.Errorf("copilot/mcp should default an empty mcpServers wrapper, got %#v", mcp.Defaults["mcpServers"])
	}
	if len(mcp.Managed) != 0 {
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
	if _, ok := lsp.Defaults["lspServers"].(map[string]any); !ok {
		t.Errorf("copilot/lsp should default an empty lspServers wrapper, got %#v", lsp.Defaults["lspServers"])
	}
	if len(lsp.Managed) != 0 {
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
	if _, ok := s.Defaults["mcpServers"].(map[string]any); !ok {
		t.Errorf("agy/mcp should default an empty mcpServers wrapper, got %#v", s.Defaults["mcpServers"])
	}
	if len(s.Managed) != 0 {
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
	if len(s.Defaults) != 0 {
		t.Errorf("mise/config Defaults should be empty (mise is override-only), got %#v", s.Defaults)
	}
	if len(s.Managed) != 0 {
		t.Errorf("mise/config Managed should be empty (yolo asserts no mise key), got %#v", s.Managed)
	}
}
