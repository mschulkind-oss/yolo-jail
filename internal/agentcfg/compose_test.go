package agentcfg

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
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

// TestComposeMergesThenEnforces is the end-to-end acceptance test for one
// surface: defaults < host, then the managed floor, with per-key provenance.
//
// It is what is left of the §6.5 worked example after the Lua transform was
// removed (docs/design/lua-transform-removal.md §5.5). That example's whole
// point was its ② step — a script dropping an element out of the `extensions`
// array — which is the capability §6 of the removal doc records as a deliberate
// gap. The layering either side of that step is still the pipeline's contract,
// so it is asserted here without a script.
func TestComposeMergesThenEnforces(t *testing.T) {
	res, err := Compose(Inputs{
		Surface:   piSurface(),
		HostBytes: []byte(piHostJSON),
	})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}

	want := map[string]any{
		"theme":        "dark",           // from host, over defaults "system"
		"defaultModel": "claude-fable-5", // from host
		"extensions": []any{ // host array, intact: nothing below managed reshapes it
			"extensions/permission-gate.ts",
			"extensions/git-helper.ts",
		},
		"defaultProjectTrust": "always", // managed, enforced last
	}
	if !reflect.DeepEqual(res.Config, want) {
		t.Errorf("composed config mismatch:\n got: %#v\nwant: %#v", res.Config, want)
	}

	// Provenance (the --explain data): host wins every key it set, managed wins
	// the key it enforces.
	wantProv := map[string]string{
		"theme":               layerHost,
		"defaultModel":        layerHost,
		"extensions":          layerHost,
		"defaultProjectTrust": layerManaged,
	}
	if !reflect.DeepEqual(res.Provenance, wantProv) {
		t.Errorf("provenance mismatch:\n got: %#v\nwant: %#v", res.Provenance, wantProv)
	}
}

// TestComposeOverlayLayer: the capture-diff overlay (§5) merges above workspace
// and below computed+managed.
func TestComposeOverlayLayer(t *testing.T) {
	res, err := Compose(Inputs{
		Surface:   piSurface(),
		HostBytes: []byte(piHostJSON),
		Overlay:   map[string]any{"theme": "solarized"}, // in-jail edit survives regen
	})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if res.ConfigMap()["theme"] != "solarized" {
		t.Errorf("overlay should override host theme: got %v", res.ConfigMap()["theme"])
	}
	if res.Provenance["theme"] != layerOverlay {
		t.Errorf("provenance for theme = %q, want %q", res.Provenance["theme"], layerOverlay)
	}
}

// TestComposeComputedLayer: the runtime-computed layer (yolo's per-boot dynamic
// content — MCP tables, LSP-plugin toggles) merges ABOVE overlay and BELOW
// managed. This is the mechanism that lets a surface carrying static
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
	if res.ConfigMap()["defaultModel"] != "computed-wins" {
		t.Errorf("computed should override overlay: got %v", res.ConfigMap()["defaultModel"])
	}
	if res.Provenance["defaultModel"] != layerComputed {
		t.Errorf("provenance defaultModel = %q, want %q", res.Provenance["defaultModel"], layerComputed)
	}
	// ...but an overlay key the computed layer does NOT touch still survives.
	if res.ConfigMap()["theme"] != "solarized" {
		t.Errorf("overlay-only key should survive: got %v", res.ConfigMap()["theme"])
	}
	if res.Provenance["theme"] != layerOverlay {
		t.Errorf("provenance theme = %q, want %q", res.Provenance["theme"], layerOverlay)
	}
}

// TestComposeManagedWinsOverComputed pins the hard floor against the computed
// layer specifically: a computed attempt to loosen a managed key is stomped.
//
// It was written to outlive TestComposeComputedBelowManagedAndTransform, which
// proved this and "a transform can reshape a computed value" at once and died
// with the transform (docs/design/lua-transform-removal.md §5.5). No other test
// in this file pins it against computed: TestComposeComputedLayer only ranks
// computed against overlay, and the *EnforcesManaged suite feeds host bytes.
func TestComposeManagedWinsOverComputed(t *testing.T) {
	res, err := Compose(Inputs{
		Surface:  piSurface(),
		Computed: map[string]any{"defaultModel": "computed", "defaultProjectTrust": "never"},
	})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	// The computed layer lands where nothing above it objects...
	if res.ConfigMap()["defaultModel"] != "computed" {
		t.Errorf("computed key should survive: got %v", res.ConfigMap()["defaultModel"])
	}
	if res.Provenance["defaultModel"] != layerComputed {
		t.Errorf("provenance defaultModel = %q, want %q", res.Provenance["defaultModel"], layerComputed)
	}
	// ...but managed is the floor: a computed attempt to loosen the enforced key
	// is stomped by Enforce, which runs after every merged layer.
	if res.ConfigMap()["defaultProjectTrust"] != "always" {
		t.Errorf("managed must win over computed: got %v", res.ConfigMap()["defaultProjectTrust"])
	}
	if res.Provenance["defaultProjectTrust"] != layerManaged {
		t.Errorf("provenance defaultProjectTrust = %q, want %q",
			res.Provenance["defaultProjectTrust"], layerManaged)
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
	if _, present := res.ConfigMap()["defaultModel"]; present {
		t.Errorf("computed null should delete the key, still present: %v", res.ConfigMap()["defaultModel"])
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

// TestComposeClaudeSettingsEnforcesManaged proves the builtin claude/settings
// surface, composed against a representative host settings.json, yields the
// YOLO force-managed posture regardless of what the host tried to set. This is
// the Compose-through-the-engine analogue of the pi worked example.
func TestComposeClaudeSettingsEnforcesManaged(t *testing.T) {
	s, ok := packManifest(t).Lookup("claude", "settings")
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
	perms, ok := res.ConfigMap()["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions not an object: %T", res.ConfigMap()["permissions"])
	}
	if !reflect.DeepEqual(perms["allow"], []any{}) {
		t.Errorf("permissions.allow = %#v, want [] (host allow-list must not survive)", perms["allow"])
	}
	if perms["defaultMode"] != "acceptEdits" {
		t.Errorf("permissions.defaultMode = %v, want acceptEdits", perms["defaultMode"])
	}
	if res.ConfigMap()["skipDangerousModePermissionPrompt"] != true {
		t.Errorf("skipDangerousModePermissionPrompt = %v, want true", res.ConfigMap()["skipDangerousModePermissionPrompt"])
	}
	// `preferences` is NO LONGER MANAGED, so the host's value survives — and that is the
	// assertion, not an omission. The pack forced preferences.autoUpdaterStatus="disabled"
	// until 2026-09-04, when it was deleted as unreadable (nothing in Claude Code reads a
	// `preferences` wrapper; see builtin_test.go's claude/settings cell) and as pointing
	// the wrong way anyway: an agent CLI is an AGENT dependency and wants to be current
	// (program-delivery.md §3.5). Re-managing the key would stomp this host value again
	// and fail here.
	prefs, ok := res.ConfigMap()["preferences"].(map[string]any)
	if !ok || prefs["autoUpdaterStatus"] != "enabled" {
		t.Errorf("preferences = %#v, want the host's autoUpdaterStatus=enabled passed "+
			"through — yolo no longer manages this key", res.ConfigMap()["preferences"])
	}
	if res.Provenance["preferences"] == layerManaged {
		t.Errorf("preferences provenance = %q — yolo must claim no layer for a key it no "+
			"longer sets", res.Provenance["preferences"])
	}
	// A host key with no managed/default counterpart passes through untouched.
	if res.ConfigMap()["someHostOnlyKey"] != "kept" {
		t.Errorf("host-only key dropped: %v", res.ConfigMap()["someHostOnlyKey"])
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
	s, ok := packManifest(t).Lookup("claude", "settings")
	if !ok {
		t.Fatal("builtin manifest missing claude/settings")
	}
	host := `{"permissions": {"allow": ["X"], "ask": ["Bash(git push)"]}}`
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(host)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	perms := res.ConfigMap()["permissions"].(map[string]any)
	// Managed key wins...
	if !reflect.DeepEqual(perms["allow"], []any{}) {
		t.Errorf("managed permissions.allow should win as []: %#v", perms["allow"])
	}
	// ...but the host sibling with no managed counterpart survives (the fix).
	if !reflect.DeepEqual(perms["ask"], []any{"Bash(git push)"}) {
		t.Errorf("host sibling permissions.ask should survive deep Enforce, got %#v", perms["ask"])
	}
}

// TestComposeManagedNilValueIsAssignedNotDeleted pins the ONE case where the
// managed floor and the merge fold disagree, so the floor can be moved out of
// luahook without changing meaning (docs/design/lua-transform-removal.md §4.2
// item 1, risk R1).
//
// engine.go's mergeValue is RFC 7386: a null under a key DELETES the key. The
// floor's enforceValue is not — a non-object managed value, nil included, is
// ASSIGNED by deep copy. So a managed key whose value is null survives into the
// render as an explicit null rather than removing what the host set. The two are
// one `if` apart and look interchangeable; reusing mergeValue during the move
// would silently flip this, and nothing else in the suite would notice.
//
// Also pinned: provenance still attributes the key to the layer that last SET
// it, because Compose's managed-provenance loop skips nil values on purpose (a
// null is not a value the managed layer is claiming). Asserting what the code
// does today, not what it ought to do — the move must be a no-op.
func TestComposeManagedNilValueIsAssignedNotDeleted(t *testing.T) {
	s := piSurface()
	s.Managed = map[string]any{"defaultProjectTrust": "always", "nulled": nil}

	res, err := Compose(Inputs{
		Surface:   s,
		HostBytes: []byte(`{"nulled": "host set this", "theme": "dark"}`),
	})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	v, present := res.ConfigMap()["nulled"]
	if !present {
		t.Fatal("a nil-valued managed key must ASSIGN null, not delete the key — " +
			"enforceValue is not RFC 7386 (§4.2 item 1)")
	}
	if v != nil {
		t.Errorf("nulled = %#v, want an explicit nil assigned over the host value", v)
	}
	// It reaches the bytes as a literal null, which is the observable difference
	// from the fold's tombstone behaviour.
	if !contains(string(res.Encoded), `"nulled": null`) {
		t.Errorf("encoded lacks the explicit null:\n%s", res.Encoded)
	}
	// Siblings and the ordinary managed key are unaffected.
	if res.ConfigMap()["theme"] != "dark" {
		t.Errorf("host sibling clobbered: %#v", res.ConfigMap())
	}
	if res.ConfigMap()["defaultProjectTrust"] != "always" {
		t.Errorf("non-nil managed key = %v, want always", res.ConfigMap()["defaultProjectTrust"])
	}
	// Provenance does NOT claim the nulled key for managed (the loop skips nils).
	if got := res.Provenance["nulled"]; got != layerHost {
		t.Errorf("provenance nulled = %q, want %q (the managed loop skips nil values)",
			got, layerHost)
	}
}

// TestComposeClaudeConfigEnforcesManaged proves the builtin claude/config
// (.claude.json) surface enforces the workspace-project MCP-enable key AND, now
// that Enforce deep-merges, preserves the sibling hasTrustDialogAccepted default
// under the SAME projects["/workspace"] object — the fidelity gap the shallow
// Enforce used to have (managed nested object clobbering its default sibling) is
// closed.
func TestComposeClaudeConfigEnforcesManaged(t *testing.T) {
	s, ok := packManifest(t).Lookup("claude", "config")
	if !ok {
		t.Fatal("builtin manifest missing claude/config")
	}
	// A11: the manifest carries ${workspace}; the render substitutes the real root.
	// Compose here through the same substitution the boot path applies, with a
	// NON-default root so a re-hardcoded literal would fail rather than pass by
	// coincidence.
	const root = "/somewhere/else"
	res, err := Compose(Inputs{Surface: SubstituteWorkspace(s, root), HostBytes: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	proj, ok := res.ConfigMap()["projects"].(map[string]any)
	if !ok {
		t.Fatalf("projects not an object: %T", res.ConfigMap()["projects"])
	}
	ws, ok := proj[root].(map[string]any)
	if !ok {
		t.Fatalf("projects[%s] not an object: %T (keys %v)", root, proj[root], proj)
	}
	if ws["enableAllProjectMcpServers"] != true {
		t.Errorf("projects[%s].enableAllProjectMcpServers = %v, want true", root, ws["enableAllProjectMcpServers"])
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

// TestComposeCopilotConfigDefaultApplies proves the builtin copilot/config
// surface, composed with NO host file, yields yolo:true from the defaults layer
// (the bespoke write-if-absent baseline: yolo owns a fresh config.json).

func TestComposeCopilotConfigDefaultApplies(t *testing.T) {
	s, ok := packManifest(t).Lookup("copilot", "config")
	if !ok {
		t.Fatal("builtin manifest missing copilot/config")
	}
	res, err := Compose(Inputs{Surface: s, HostBytes: nil})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if res.ConfigMap()["yolo"] != true {
		t.Errorf("yolo = %v, want true (default applies with no host)", res.ConfigMap()["yolo"])
	}
	if res.Provenance["yolo"] != layerDefaults {
		t.Errorf("provenance yolo = %q, want %q", res.Provenance["yolo"], layerDefaults)
	}
}

// TestComposeCopilotConfigHostWins proves the default yields to a host that
// already set yolo — the bespoke code never overwrites an existing config.json,
// so a host yolo:false must survive (setDefault/write-if-absent semantics).
func TestComposeCopilotConfigHostWins(t *testing.T) {
	s, ok := packManifest(t).Lookup("copilot", "config")
	if !ok {
		t.Fatal("builtin manifest missing copilot/config")
	}
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(`{"yolo": false}`)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if res.ConfigMap()["yolo"] != false {
		t.Errorf("yolo = %v, want false (host overrides the yolo default)", res.ConfigMap()["yolo"])
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
	s, ok := packManifest(t).Lookup("opencode", "config")
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
	if res.ConfigMap()["permission"] != "allow" {
		t.Errorf("permission = %v, want allow (managed must win)", res.ConfigMap()["permission"])
	}
	if res.Provenance["permission"] != layerManaged {
		t.Errorf("provenance permission = %q, want %q", res.Provenance["permission"], layerManaged)
	}
	// Default $schema yields to the host.
	if res.ConfigMap()["$schema"] != "https://example.com/custom.json" {
		t.Errorf("$schema = %v, want the host value (default yields)", res.ConfigMap()["$schema"])
	}
	if res.Provenance["$schema"] != layerHost {
		t.Errorf("provenance $schema = %q, want %q", res.Provenance["$schema"], layerHost)
	}
	// Host-only key survives.
	if res.ConfigMap()["someHostOnlyKey"] != "kept" {
		t.Errorf("host-only key dropped: %v", res.ConfigMap()["someHostOnlyKey"])
	}
}

// TestComposeOpencodeConfigDefaultsApply proves that with NO host file the
// $schema default lands alongside the managed permission — the empty-host
// baseline (yolo owns a fresh opencode.json).
func TestComposeOpencodeConfigDefaultsApply(t *testing.T) {
	s, ok := packManifest(t).Lookup("opencode", "config")
	if !ok {
		t.Fatal("builtin manifest missing opencode/config")
	}
	res, err := Compose(Inputs{Surface: s, HostBytes: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if res.ConfigMap()["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("$schema = %v, want default (applies with no host)", res.ConfigMap()["$schema"])
	}
	if res.Provenance["$schema"] != layerDefaults {
		t.Errorf("provenance $schema = %q, want %q", res.Provenance["$schema"], layerDefaults)
	}
	if res.ConfigMap()["permission"] != "allow" {
		t.Errorf("permission = %v, want allow (managed applies)", res.ConfigMap()["permission"])
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
	s, ok := packManifest(t).Lookup("codex", "config")
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
	if res.ConfigMap()["approval_policy"] != "never" {
		t.Errorf("approval_policy = %v, want never (managed must win)", res.ConfigMap()["approval_policy"])
	}
	if res.ConfigMap()["sandbox_mode"] != "danger-full-access" {
		t.Errorf("sandbox_mode = %v, want danger-full-access (managed must win)", res.ConfigMap()["sandbox_mode"])
	}
	if res.Provenance["approval_policy"] != layerManaged {
		t.Errorf("provenance approval_policy = %q, want %q", res.Provenance["approval_policy"], layerManaged)
	}
	if res.Provenance["sandbox_mode"] != layerManaged {
		t.Errorf("provenance sandbox_mode = %q, want %q", res.Provenance["sandbox_mode"], layerManaged)
	}
	// A host key with no managed/default counterpart passes through untouched.
	if res.ConfigMap()["model"] != "gpt-5" {
		t.Errorf("host-only key model dropped: %v", res.ConfigMap()["model"])
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
	s, ok := packManifest(t).Lookup("codex", "config")
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
