package agentcfg

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// piSurface is the builtin pi manifest from docs/plans/agent-settings-composition.md
// §6.5 ①: json codec, a defaults layer, and a jail-enforced managed key.
func piSurface() manifest.Surface {
	return manifest.Surface{
		Agent:    "pi",
		Name:     "settings",
		Path:     "~/.pi/agent/settings.json",
		Codec:    "json",
		Defaults: map[string]any{"theme": "system"},
		Managed:  map[string]any{"defaultProjectTrust": "always"},
	}
}

// The §6.5 host file yolo never writes: theme + defaultModel + two extensions.
const piHostJSON = `{
  "theme": "dark",
  "defaultModel": "claude-fable-5",
  "extensions": ["extensions/permission-gate.ts", "extensions/git-helper.ts"]
}`

// The §6.5 ② user transform: drop the permission-gate extension and exclude its
// file from the staged tree.
const piTransformScript = `
yolo.transform("pi", function(ctx)
  local kept = {}
  for _, ext in ipairs(ctx.config.extensions) do
    if not ext:find("permission%-gate") then kept[#kept + 1] = ext end
  end
  ctx.config.extensions = kept
  ctx.stage.exclude("extensions/permission-gate.ts")
end)
`

// TestComposePiWorkedExample is the §6.5 end-to-end acceptance test: the exact
// inputs from the design doc must produce the exact output in §6.5 ④.
func TestComposePiWorkedExample(t *testing.T) {
	res, err := Compose(Inputs{
		Surface:   piSurface(),
		HostBytes: []byte(piHostJSON),
		Script:    piTransformScript,
		VM:        &luahook.GopherLuaVM{},
	})
	if err != nil {
		t.Fatalf("Compose returned error: %v", err)
	}

	// §6.5 ④ — what lands in the jail.
	want := map[string]any{
		"theme":               "dark",                            // from host, over defaults "system"
		"defaultModel":        "claude-fable-5",                  // from host
		"extensions":          []any{"extensions/git-helper.ts"}, // gate dropped by transform
		"defaultProjectTrust": "always",                          // managed, enforced last
	}
	if !reflect.DeepEqual(res.Config, want) {
		t.Errorf("composed config mismatch:\n got: %#v\nwant: %#v", res.Config, want)
	}

	// The transform asked to keep the permission-gate file out of the tree.
	if !reflect.DeepEqual(res.Excluded, []string{"extensions/permission-gate.ts"}) {
		t.Errorf("stage excludes = %v, want [extensions/permission-gate.ts]", res.Excluded)
	}

	// Provenance (the --explain data): host wins theme+defaultModel, managed wins
	// defaultProjectTrust, and extensions was last touched by the transform.
	wantProv := map[string]string{
		"theme":               layerHost,
		"defaultModel":        layerHost,
		"extensions":          layerTransform,
		"defaultProjectTrust": layerManaged,
	}
	if !reflect.DeepEqual(res.Provenance, wantProv) {
		t.Errorf("provenance mismatch:\n got: %#v\nwant: %#v", res.Provenance, wantProv)
	}
}

// TestComposeIdentityNoScript: with no config.lua, Compose is a plain
// merge+enforce (defaults<host, then managed).
func TestComposeIdentityNoScript(t *testing.T) {
	res, err := Compose(Inputs{
		Surface:   piSurface(),
		HostBytes: []byte(piHostJSON),
		// no Script, no VM — identity transform
	})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	// Both extensions survive (no transform ran); managed key still enforced.
	exts, ok := res.Config["extensions"].([]any)
	if !ok || len(exts) != 2 {
		t.Errorf("extensions = %v, want both host extensions intact", res.Config["extensions"])
	}
	if res.Config["defaultProjectTrust"] != "always" {
		t.Errorf("managed key not enforced: %v", res.Config["defaultProjectTrust"])
	}
	if len(res.Excluded) != 0 {
		t.Errorf("identity transform should exclude nothing, got %v", res.Excluded)
	}
}

// TestComposeManagedWinsOverTransform: a transform that tries to set a managed
// key is overridden by Enforce (§3.1 managed wins last).
func TestComposeManagedWinsOverTransform(t *testing.T) {
	script := `
yolo.transform("pi", function(ctx)
  ctx.config.defaultProjectTrust = "never"
end)
`
	res, err := Compose(Inputs{
		Surface:   piSurface(),
		HostBytes: []byte(piHostJSON),
		Script:    script,
		VM:        &luahook.GopherLuaVM{},
	})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if res.Config["defaultProjectTrust"] != "always" {
		t.Errorf("managed key should win over transform: got %v, want always", res.Config["defaultProjectTrust"])
	}
	if res.Provenance["defaultProjectTrust"] != layerManaged {
		t.Errorf("provenance for enforced key = %q, want %q", res.Provenance["defaultProjectTrust"], layerManaged)
	}
}

// TestComposeOverlayLayer: the capture-diff overlay (§5) merges above workspace
// and below the transform+managed.
func TestComposeOverlayLayer(t *testing.T) {
	res, err := Compose(Inputs{
		Surface:   piSurface(),
		HostBytes: []byte(piHostJSON),
		Overlay:   map[string]any{"theme": "solarized"}, // in-jail edit survives regen
	})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if res.Config["theme"] != "solarized" {
		t.Errorf("overlay should override host theme: got %v", res.Config["theme"])
	}
	if res.Provenance["theme"] != layerOverlay {
		t.Errorf("provenance for theme = %q, want %q", res.Provenance["theme"], layerOverlay)
	}
}

// TestComposeComputedLayer: the runtime-computed layer (yolo's per-boot dynamic
// content — MCP tables, LSP-plugin toggles) merges ABOVE overlay and BELOW the
// transform+managed. This is the mechanism that lets a surface carrying static
// managed keys ALSO carry yolo-regenerated dynamic keys in the same file: the
// caller computes the dynamic map from live config and hands it in as Computed.
// Its precedence embodies §2 principle 1 (regenerate, don't reconcile): the
// fresh computation wins over a stale in-jail edit to the SAME key.
func TestComposeComputedLayer(t *testing.T) {
	res, err := Compose(Inputs{
		Surface:   piSurface(),
		HostBytes: []byte(piHostJSON),
		Overlay:   map[string]any{"theme": "solarized", "defaultModel": "stale-edit"},
		Computed:  map[string]any{"defaultModel": "computed-wins"},
	})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	// Computed wins over the overlay's stale edit to the same key...
	if res.Config["defaultModel"] != "computed-wins" {
		t.Errorf("computed should override overlay: got %v", res.Config["defaultModel"])
	}
	if res.Provenance["defaultModel"] != layerComputed {
		t.Errorf("provenance defaultModel = %q, want %q", res.Provenance["defaultModel"], layerComputed)
	}
	// ...but an overlay key the computed layer does NOT touch still survives.
	if res.Config["theme"] != "solarized" {
		t.Errorf("overlay-only key should survive: got %v", res.Config["theme"])
	}
	if res.Provenance["theme"] != layerOverlay {
		t.Errorf("provenance theme = %q, want %q", res.Provenance["theme"], layerOverlay)
	}
}

// TestComposeComputedBelowManagedAndTransform: managed still wins over computed
// (the hard floor), and a transform can still reshape a computed value (computed
// is below the transform, same as every pre-transform layer).
func TestComposeComputedBelowManagedAndTransform(t *testing.T) {
	script := `
yolo.transform("pi", function(ctx)
  ctx.config.defaultModel = ctx.config.defaultModel .. "-reshaped"
end)
`
	res, err := Compose(Inputs{
		Surface:  piSurface(),
		Computed: map[string]any{"defaultModel": "computed", "defaultProjectTrust": "never"},
		Script:   script,
		VM:       &luahook.GopherLuaVM{},
	})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	// The transform saw the computed value and reshaped it.
	if res.Config["defaultModel"] != "computed-reshaped" {
		t.Errorf("transform should reshape computed value: got %v", res.Config["defaultModel"])
	}
	// Managed still stomps a computed attempt to loosen the managed key.
	if res.Config["defaultProjectTrust"] != "always" {
		t.Errorf("managed must win over computed: got %v", res.Config["defaultProjectTrust"])
	}
}

// TestComposeComputedTombstone: a null in the computed layer deletes the key
// from the render (RFC-7386), so a computed layer can prune a key an earlier
// layer set — e.g. removing an LSP plugin that is no longer configured. This is
// how the computed layer expresses "this dynamic entry is gone this boot"
// without any sidecar memory (§2 principle 1: absence is deletion).
func TestComposeComputedTombstone(t *testing.T) {
	res, err := Compose(Inputs{
		Surface:  piSurface(),
		Overlay:  map[string]any{"defaultModel": "was-here"},
		Computed: map[string]any{"defaultModel": nil}, // prune it this boot
	})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if _, present := res.Config["defaultModel"]; present {
		t.Errorf("computed null should delete the key, still present: %v", res.Config["defaultModel"])
	}
	if _, present := res.Provenance["defaultModel"]; present {
		t.Errorf("provenance should not claim a tombstoned key is present: %v", res.Provenance["defaultModel"])
	}
}

// TestComposeUnknownCodec fails loud.
func TestComposeUnknownCodec(t *testing.T) {
	s := piSurface()
	s.Codec = "bogus"
	if _, err := Compose(Inputs{Surface: s}); err == nil {
		t.Fatal("expected error for unknown codec, got nil")
	}
}

// TestComposeLuaErrorFailsClosed: a Lua error aborts the render (no partial
// config), per §3.4.
func TestComposeLuaErrorFailsClosed(t *testing.T) {
	script := `yolo.transform("pi", function(ctx) error("boom") end)`
	_, err := Compose(Inputs{
		Surface:   piSurface(),
		HostBytes: []byte(piHostJSON),
		Script:    script,
		VM:        &luahook.GopherLuaVM{},
	})
	if err == nil {
		t.Fatal("expected fail-closed error from Lua error, got nil")
	}
}

// TestComposeScriptWithoutVM is a loud error (a declared transform with no VM).
func TestComposeScriptWithoutVM(t *testing.T) {
	_, err := Compose(Inputs{
		Surface:   piSurface(),
		HostBytes: []byte(piHostJSON),
		Script:    piTransformScript,
		// VM omitted
	})
	if err == nil {
		t.Fatal("expected error for script without VM, got nil")
	}
}

// TestComposeClaudeSettingsEnforcesManaged proves the builtin claude/settings
// surface, composed against a representative host settings.json, yields the
// YOLO force-managed posture regardless of what the host tried to set. This is
// the Compose-through-the-engine analogue of the pi worked example.
func TestComposeClaudeSettingsEnforcesManaged(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("claude", "settings")
	if !ok {
		t.Fatal("builtin manifest missing claude/settings")
	}
	// A host that tries to loosen the posture: a permissive allow-list, the
	// auto-updater left on. Managed must stomp all of it.
	host := `{
	  "permissions": {"allow": ["Bash(rm -rf /)"], "defaultMode": "plan"},
	  "preferences": {"autoUpdaterStatus": "enabled"},
	  "skipDangerousModePermissionPrompt": false,
	  "someHostOnlyKey": "kept"
	}`
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(host)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	// The whole managed permissions object wins (shallow Enforce replaces it).
	perms, ok := res.Config["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions not an object: %T", res.Config["permissions"])
	}
	if !reflect.DeepEqual(perms["allow"], []any{}) {
		t.Errorf("permissions.allow = %#v, want [] (host allow-list must not survive)", perms["allow"])
	}
	if perms["defaultMode"] != "acceptEdits" {
		t.Errorf("permissions.defaultMode = %v, want acceptEdits", perms["defaultMode"])
	}
	if res.Config["skipDangerousModePermissionPrompt"] != true {
		t.Errorf("skipDangerousModePermissionPrompt = %v, want true", res.Config["skipDangerousModePermissionPrompt"])
	}
	prefs, ok := res.Config["preferences"].(map[string]any)
	if !ok || prefs["autoUpdaterStatus"] != "disabled" {
		t.Errorf("preferences = %#v, want autoUpdaterStatus=disabled", res.Config["preferences"])
	}
	// A host key with no managed/default counterpart passes through untouched.
	if res.Config["someHostOnlyKey"] != "kept" {
		t.Errorf("host-only key dropped: %v", res.Config["someHostOnlyKey"])
	}
	if res.Provenance["permissions"] != layerManaged {
		t.Errorf("provenance permissions = %q, want %q", res.Provenance["permissions"], layerManaged)
	}
}

// TestComposeDeepEnforcePreservesHostSibling proves the deep-merge Enforce fix:
// a host key UNDER the same object as a managed key survives (yolo forces
// permissions.allow=[] but the host's permissions.ask stays), instead of the
// whole permissions object being clobbered. This is the closed fidelity gap.
func TestComposeDeepEnforcePreservesHostSibling(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("claude", "settings")
	if !ok {
		t.Fatal("builtin manifest missing claude/settings")
	}
	host := `{"permissions": {"allow": ["X"], "ask": ["Bash(git push)"]}}`
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(host)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	perms := res.Config["permissions"].(map[string]any)
	// Managed key wins...
	if !reflect.DeepEqual(perms["allow"], []any{}) {
		t.Errorf("managed permissions.allow should win as []: %#v", perms["allow"])
	}
	// ...but the host sibling with no managed counterpart survives (the fix).
	if !reflect.DeepEqual(perms["ask"], []any{"Bash(git push)"}) {
		t.Errorf("host sibling permissions.ask should survive deep Enforce, got %#v", perms["ask"])
	}
}

// TestComposeClaudeConfigEnforcesManaged proves the builtin claude/config
// (.claude.json) surface enforces the workspace-project MCP-enable key AND, now
// that Enforce deep-merges, preserves the sibling hasTrustDialogAccepted default
// under the SAME projects["/workspace"] object — the fidelity gap the shallow
// Enforce used to have (managed nested object clobbering its default sibling) is
// closed.
func TestComposeClaudeConfigEnforcesManaged(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("claude", "config")
	if !ok {
		t.Fatal("builtin manifest missing claude/config")
	}
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	proj, ok := res.Config["projects"].(map[string]any)
	if !ok {
		t.Fatalf("projects not an object: %T", res.Config["projects"])
	}
	ws, ok := proj["/workspace"].(map[string]any)
	if !ok {
		t.Fatalf("projects[/workspace] not an object: %T", proj["/workspace"])
	}
	if ws["enableAllProjectMcpServers"] != true {
		t.Errorf("projects[/workspace].enableAllProjectMcpServers = %v, want true", ws["enableAllProjectMcpServers"])
	}
	if res.Provenance["projects"] != layerManaged {
		t.Errorf("provenance projects = %q, want %q", res.Provenance["projects"], layerManaged)
	}
	// Deep-merge Enforce now preserves the sibling default alongside the managed
	// key under the same object — both coexist.
	if ws["hasTrustDialogAccepted"] != true {
		t.Errorf("deep Enforce should preserve the sibling default hasTrustDialogAccepted=true, got %v", ws["hasTrustDialogAccepted"])
	}
}

// TestComposeGeminiSettingsLayers proves the builtin gemini/settings surface
// composes with the correct layer semantics per §7: the FORCE-MANAGED
// general.* auto-update disables win over a host that tried to enable them,
// while the security.* posture is a USER-OVERRIDABLE DEFAULT that a host value
// legitimately replaces (the bespoke setDefault behavior, faithfully modeled).
func TestComposeGeminiSettingsLayers(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("gemini", "settings")
	if !ok {
		t.Fatal("builtin manifest missing gemini/settings")
	}
	// A host that (a) turns the auto-updater back on and (b) tightens the
	// approval posture. Managed must stomp (a); the default must yield to (b).
	host := `{
	  "general": {"enableAutoUpdate": true, "enableAutoUpdateNotification": true},
	  "security": {"approvalMode": "default"}
	}`
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(host)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}

	// Managed general.* wins (shallow Enforce replaces the whole general object).
	gen, ok := res.Config["general"].(map[string]any)
	if !ok {
		t.Fatalf("general not an object: %T", res.Config["general"])
	}
	if gen["enableAutoUpdate"] != false {
		t.Errorf("general.enableAutoUpdate = %v, want false (managed must win)", gen["enableAutoUpdate"])
	}
	if gen["enableAutoUpdateNotification"] != false {
		t.Errorf("general.enableAutoUpdateNotification = %v, want false (managed must win)", gen["enableAutoUpdateNotification"])
	}
	if res.Provenance["general"] != layerManaged {
		t.Errorf("provenance general = %q, want %q", res.Provenance["general"], layerManaged)
	}

	// Default security.* yields to the host (setDefault semantics: host wins).
	sec, ok := res.Config["security"].(map[string]any)
	if !ok {
		t.Fatalf("security not an object: %T", res.Config["security"])
	}
	if sec["approvalMode"] != "default" {
		t.Errorf("security.approvalMode = %v, want default (host overrides the yolo default)", sec["approvalMode"])
	}
	if res.Provenance["security"] != layerHost {
		t.Errorf("provenance security = %q, want %q", res.Provenance["security"], layerHost)
	}
}

// TestComposeGeminiSettingsDefaultsApply proves that with NO host file the
// default security posture is what lands (approvalMode=yolo), alongside the
// managed general disables — the empty-host baseline.
func TestComposeGeminiSettingsDefaultsApply(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("gemini", "settings")
	if !ok {
		t.Fatal("builtin manifest missing gemini/settings")
	}
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	sec, ok := res.Config["security"].(map[string]any)
	if !ok {
		t.Fatalf("security not an object: %T", res.Config["security"])
	}
	if sec["approvalMode"] != "yolo" {
		t.Errorf("security.approvalMode = %v, want yolo (default applies with no host)", sec["approvalMode"])
	}
	if sec["enablePermanentToolApproval"] != true {
		t.Errorf("security.enablePermanentToolApproval = %v, want true", sec["enablePermanentToolApproval"])
	}
	if res.Provenance["security"] != layerDefaults {
		t.Errorf("provenance security = %q, want %q", res.Provenance["security"], layerDefaults)
	}
	gen, ok := res.Config["general"].(map[string]any)
	if !ok || gen["enableAutoUpdate"] != false {
		t.Errorf("general = %#v, want managed disables applied", res.Config["general"])
	}
}

// TestComposeCopilotConfigDefaultApplies proves the builtin copilot/config
// surface, composed with NO host file, yields yolo:true from the defaults layer
// (the bespoke write-if-absent baseline: yolo owns a fresh config.json).
func TestComposeCopilotConfigDefaultApplies(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("copilot", "config")
	if !ok {
		t.Fatal("builtin manifest missing copilot/config")
	}
	res, err := Compose(Inputs{Surface: s, HostBytes: nil})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if res.Config["yolo"] != true {
		t.Errorf("yolo = %v, want true (default applies with no host)", res.Config["yolo"])
	}
	if res.Provenance["yolo"] != layerDefaults {
		t.Errorf("provenance yolo = %q, want %q", res.Provenance["yolo"], layerDefaults)
	}
}

// TestComposeCopilotConfigHostWins proves the default yields to a host that
// already set yolo — the bespoke code never overwrites an existing config.json,
// so a host yolo:false must survive (setDefault/write-if-absent semantics).
func TestComposeCopilotConfigHostWins(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("copilot", "config")
	if !ok {
		t.Fatal("builtin manifest missing copilot/config")
	}
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(`{"yolo": false}`)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if res.Config["yolo"] != false {
		t.Errorf("yolo = %v, want false (host overrides the yolo default)", res.Config["yolo"])
	}
	if res.Provenance["yolo"] != layerHost {
		t.Errorf("provenance yolo = %q, want %q", res.Provenance["yolo"], layerHost)
	}
}

// TestComposeOpencodeConfigLayers proves the builtin opencode/config surface
// composes with the correct layer semantics: the FORCE-MANAGED permission="allow"
// wins over a host that tried to lock it down, while the $schema DEFAULT yields
// to a host that already set it (the bespoke setDefault behavior). A host-only
// key passes through untouched.
func TestComposeOpencodeConfigLayers(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("opencode", "config")
	if !ok {
		t.Fatal("builtin manifest missing opencode/config")
	}
	// A host that (a) tightens permission and (b) pins its own $schema.
	host := `{
	  "permission": "ask",
	  "$schema": "https://example.com/custom.json",
	  "someHostOnlyKey": "kept"
	}`
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(host)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	// Managed permission wins.
	if res.Config["permission"] != "allow" {
		t.Errorf("permission = %v, want allow (managed must win)", res.Config["permission"])
	}
	if res.Provenance["permission"] != layerManaged {
		t.Errorf("provenance permission = %q, want %q", res.Provenance["permission"], layerManaged)
	}
	// Default $schema yields to the host.
	if res.Config["$schema"] != "https://example.com/custom.json" {
		t.Errorf("$schema = %v, want the host value (default yields)", res.Config["$schema"])
	}
	if res.Provenance["$schema"] != layerHost {
		t.Errorf("provenance $schema = %q, want %q", res.Provenance["$schema"], layerHost)
	}
	// Host-only key survives.
	if res.Config["someHostOnlyKey"] != "kept" {
		t.Errorf("host-only key dropped: %v", res.Config["someHostOnlyKey"])
	}
}

// TestComposeOpencodeConfigDefaultsApply proves that with NO host file the
// $schema default lands alongside the managed permission — the empty-host
// baseline (yolo owns a fresh opencode.json).
func TestComposeOpencodeConfigDefaultsApply(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("opencode", "config")
	if !ok {
		t.Fatal("builtin manifest missing opencode/config")
	}
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if res.Config["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("$schema = %v, want default (applies with no host)", res.Config["$schema"])
	}
	if res.Provenance["$schema"] != layerDefaults {
		t.Errorf("provenance $schema = %q, want %q", res.Provenance["$schema"], layerDefaults)
	}
	if res.Config["permission"] != "allow" {
		t.Errorf("permission = %v, want allow (managed applies)", res.Config["permission"])
	}
	if res.Provenance["permission"] != layerManaged {
		t.Errorf("provenance permission = %q, want %q", res.Provenance["permission"], layerManaged)
	}
}

// TestComposeCodexConfigEnforcesManaged proves the builtin codex/config surface
// composes THROUGH THE TOML CODEC end to end: composed against a host
// config.toml that tries to loosen the posture, the force-managed scalars win,
// and the encoded bytes are valid TOML that round-trips back to the composed
// config. This is the toml-codec analogue of the pi/claude worked examples.
func TestComposeCodexConfigEnforcesManaged(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("codex", "config")
	if !ok {
		t.Fatal("builtin manifest missing codex/config")
	}
	if s.Codec != "toml" {
		t.Fatalf("codex/config codec = %q, want toml (this test exercises the toml codec)", s.Codec)
	}
	// A host config.toml that tries to loosen the posture and add its own key.
	host := "approval_policy = \"on-request\"\n" +
		"sandbox_mode = \"read-only\"\n" +
		"model = \"gpt-5\"\n"
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(host)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}

	// Managed scalars win over the host.
	if res.Config["approval_policy"] != "never" {
		t.Errorf("approval_policy = %v, want never (managed must win)", res.Config["approval_policy"])
	}
	if res.Config["sandbox_mode"] != "danger-full-access" {
		t.Errorf("sandbox_mode = %v, want danger-full-access (managed must win)", res.Config["sandbox_mode"])
	}
	if res.Provenance["approval_policy"] != layerManaged {
		t.Errorf("provenance approval_policy = %q, want %q", res.Provenance["approval_policy"], layerManaged)
	}
	if res.Provenance["sandbox_mode"] != layerManaged {
		t.Errorf("provenance sandbox_mode = %q, want %q", res.Provenance["sandbox_mode"], layerManaged)
	}
	// A host key with no managed/default counterpart passes through untouched.
	if res.Config["model"] != "gpt-5" {
		t.Errorf("host-only key model dropped: %v", res.Config["model"])
	}
	if res.Provenance["model"] != layerHost {
		t.Errorf("provenance model = %q, want %q", res.Provenance["model"], layerHost)
	}

	// The encoded bytes must be VALID TOML and round-trip back to the composed
	// config — the codec's decode(encode(x)) == x contract for this shape.
	c, ok := codec.LookupCodec("toml")
	if !ok {
		t.Fatal("toml codec not registered")
	}
	decoded, derr := c.Decode(res.Encoded)
	if derr != nil {
		t.Fatalf("encoded codex config is not valid TOML: %v\n---\n%s", derr, res.Encoded)
	}
	back, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("decoded codex config is not a table: %T", decoded)
	}
	if !reflect.DeepEqual(back, res.Config) {
		t.Errorf("toml round-trip mismatch:\n got: %#v\nwant: %#v", back, res.Config)
	}
}

// TestComposeCodexConfigDefaultsApply proves that with NO host file the managed
// scalars are exactly what lands (there are no default keys for codex), and the
// output is valid TOML.
func TestComposeCodexConfigDefaultsApply(t *testing.T) {
	s, ok := BuiltinManifest().Lookup("codex", "config")
	if !ok {
		t.Fatal("builtin manifest missing codex/config")
	}
	res, err := Compose(Inputs{Surface: s, HostBytes: nil})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	want := map[string]any{
		"approval_policy": "never",
		"sandbox_mode":    "danger-full-access",
	}
	if !reflect.DeepEqual(res.Config, want) {
		t.Errorf("empty-host codex config mismatch:\n got: %#v\nwant: %#v", res.Config, want)
	}
	c, _ := codec.LookupCodec("toml")
	if _, derr := c.Decode(res.Encoded); derr != nil {
		t.Fatalf("encoded codex config is not valid TOML: %v\n---\n%s", derr, res.Encoded)
	}
}

// TestProvenanceLines are sorted and tab-separated for --explain.
func TestProvenanceLines(t *testing.T) {
	r := &Result{Provenance: map[string]string{"b": "host", "a": "managed"}}
	got := r.ProvenanceLines()
	want := []string{"a\tmanaged", "b\thost"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProvenanceLines = %v, want %v", got, want)
	}
}
