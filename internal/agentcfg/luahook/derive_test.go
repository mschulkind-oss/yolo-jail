package luahook

import (
	"reflect"
	"testing"
)

// derive builds a DeriveCtx with the given live tables and runs one script.
func runDerive(t *testing.T, agent, surface, script string, tables map[string]map[string]any) (map[string]any, error) {
	t.Helper()
	return GopherLuaVM{}.Derive(script, &DeriveCtx{Agent: agent, Surface: surface, Tables: tables})
}

// A passthrough derive (agy/copilot MCP): read ctx.mcp_servers, return it under
// a renamed key.
func TestDerive_Passthrough(t *testing.T) {
	script := `
yolo.derive("agy", "mcp", function(ctx)
  return { mcpServers = ctx.mcp_servers }
end)`
	tables := map[string]map[string]any{
		"mcp_servers": {"fs": map[string]any{"command": "mcp-fs", "args": []any{"/work"}}},
	}
	got, err := runDerive(t, "agy", "mcp", script, tables)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"mcpServers": map[string]any{
		"fs": map[string]any{"command": "mcp-fs", "args": []any{"/work"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("passthrough:\n got: %#v\nwant: %#v", got, want)
	}
}

// The tombstone sentinel decodes to Go nil (an RFC-7386 delete), NOT a dropped
// key — the behavior the computed[] tombstone/false-flag relied on and that a
// bare Lua nil would silently lose.
func TestDerive_TombstoneBecomesNil(t *testing.T) {
	script := `
yolo.derive("claude", "settings", function(ctx)
  return { mcpServers = ctx.tombstone }
end)`
	got, err := runDerive(t, "claude", "settings", script, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, present := got["mcpServers"]
	if !present {
		t.Fatal("tombstoned key must be PRESENT with a nil value (RFC-7386 delete), not dropped")
	}
	if v != nil {
		t.Errorf("tombstone should decode to Go nil, got %#v", v)
	}
}

// A plain Lua nil, by contrast, drops the key (Lua tables can't hold nil) — this
// documents WHY the sentinel is needed rather than `= nil`.
func TestDerive_BareNilDropsKey(t *testing.T) {
	script := `
yolo.derive("x", "y", function(ctx)
  return { gone = nil, kept = "here" }
end)`
	got, err := runDerive(t, "x", "y", script, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := got["gone"]; present {
		t.Error("a bare Lua nil assignment drops the key (expected — use ctx.tombstone to delete)")
	}
	if got["kept"] != "here" {
		t.Errorf("kept: %#v", got)
	}
}

// The claude enabledPlugins flag pattern: a conditional key that is the value
// when its LSP is present and a NESTED tombstone when absent.
func TestDerive_NestedTombstoneInFlags(t *testing.T) {
	script := `
yolo.derive("claude", "settings", function(ctx)
  local plugin = { python = "pyright", go = "gopls" }
  local out = {}
  for lang, id in pairs(plugin) do
    out[id] = ctx.lsp_servers[lang] and true or ctx.tombstone
  end
  return { enabledPlugins = out }
end)`
	tables := map[string]map[string]any{"lsp_servers": {"python": map[string]any{}}}
	got, err := runDerive(t, "claude", "settings", script, tables)
	if err != nil {
		t.Fatal(err)
	}
	plugins, _ := got["enabledPlugins"].(map[string]any)
	if plugins["pyright"] != true {
		t.Errorf("python LSP present → pyright enabled, got %#v", plugins["pyright"])
	}
	v, present := plugins["gopls"]
	if !present || v != nil {
		t.Errorf("go LSP absent → gopls must be a nil tombstone (present, nil), got present=%v v=%#v", present, v)
	}
}

// whenAny pattern (ENABLE_LSP_TOOL): set when the table has ANY entry.
func TestDerive_WhenAny(t *testing.T) {
	script := `
yolo.derive("claude", "settings", function(ctx)
  return { env = { ENABLE_LSP_TOOL = next(ctx.lsp_servers) and "1" or ctx.tombstone } }
end)`
	withLSP, err := runDerive(t, "claude", "settings", script,
		map[string]map[string]any{"lsp_servers": {"go": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	if env, _ := withLSP["env"].(map[string]any); env["ENABLE_LSP_TOOL"] != "1" {
		t.Errorf("with an LSP, ENABLE_LSP_TOOL=1, got %#v", withLSP["env"])
	}
	none, err := runDerive(t, "claude", "settings", script, nil)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := none["env"].(map[string]any)
	if v, present := env["ENABLE_LSP_TOOL"]; !present || v != nil {
		t.Errorf("no LSP → ENABLE_LSP_TOOL tombstoned (present, nil), got present=%v v=%#v", present, v)
	}
}

// opencode's projection is the hardest: rename env→environment, fold command+args
// into one array, inject type+enabled. It must stay obvious Lua and produce the
// exact shape the Go projection did.
func TestDerive_OpencodeProjection(t *testing.T) {
	script := `
yolo.derive("opencode", "config", function(ctx)
  local out = {}
  for name, s in pairs(ctx.mcp_servers) do
    local cmd = { s.command }
    for _, a in ipairs(s.args or {}) do cmd[#cmd+1] = a end
    out[name] = { type = "local", enabled = true, command = cmd, environment = s.env }
  end
  return { mcp = out }
end)`
	tables := map[string]map[string]any{
		"mcp_servers": {"fs": map[string]any{
			"command": "mcp-fs", "args": []any{"/work"}, "env": map[string]any{"ROOT": "/work"},
		}},
	}
	got, err := runDerive(t, "opencode", "config", script, tables)
	if err != nil {
		t.Fatal(err)
	}
	mcp, _ := got["mcp"].(map[string]any)
	fs, _ := mcp["fs"].(map[string]any)
	if fs["type"] != "local" || fs["enabled"] != true {
		t.Errorf("injected keys wrong: %#v", fs)
	}
	if !reflect.DeepEqual(fs["command"], []any{"mcp-fs", "/work"}) {
		t.Errorf("command should fold command+args: %#v", fs["command"])
	}
	if !reflect.DeepEqual(fs["environment"], map[string]any{"ROOT": "/work"}) {
		t.Errorf("env should rename to environment: %#v", fs["environment"])
	}
}

// A script that registers no derive for this surface is the identity (no computed
// layer) — same as a surface with no computed[] declarations.
func TestDerive_NoRegistrationIsNil(t *testing.T) {
	got, err := runDerive(t, "claude", "config", `yolo.derive("other", "x", function() return {} end)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("no derive for this surface → nil computed layer, got %#v", got)
	}
}

// A derive that returns a non-table fails loud (fail-closed).
func TestDerive_NonTableReturnFails(t *testing.T) {
	_, err := runDerive(t, "a", "b", `yolo.derive("a", "b", function() return "nope" end)`, nil)
	if err == nil {
		t.Fatal("a derive returning a non-table must be a loud error")
	}
}

// The live tables are exposed per surface identity: the SAME agent's two surfaces
// get their own derive fns (claude config vs settings).
func TestDerive_PerSurfaceRegistration(t *testing.T) {
	script := `
yolo.derive("claude", "config", function(ctx) return { which = "config" } end)
yolo.derive("claude", "settings", function(ctx) return { which = "settings" } end)`
	cfg, _ := runDerive(t, "claude", "config", script, nil)
	set, _ := runDerive(t, "claude", "settings", script, nil)
	if cfg["which"] != "config" || set["which"] != "settings" {
		t.Errorf("per-surface derives crossed: config=%#v settings=%#v", cfg, set)
	}
}

// Forbidden globals are absent in a derive too (same sandbox as transform).
func TestDerive_Sandboxed(t *testing.T) {
	_, err := runDerive(t, "a", "b", `yolo.derive("a","b",function() return { x = os.time() } end)`, nil)
	if err == nil {
		t.Fatal("os must be absent in a derive sandbox")
	}
}

// runEnvDerive is runDerive for the ENV producer: DeriveCtx.Env selects the
// yolo.env(agent, fn) registration instead of a (agent, surface) derive.
func runEnvDerive(t *testing.T, agent, script string, tables map[string]map[string]any) (map[string]any, error) {
	t.Helper()
	return GopherLuaVM{}.Derive(script, &DeriveCtx{
		Agent:            agent,
		Env:              true,
		SelectedProvider: "zai",
		ProfileName:      "zai",
		Tables:           tables,
	})
}

// The env registration is a THIRD registration, invoked through DeriveCtx.Env, and it
// sees the resolved selection the runner hands it (ctx.selected_provider,
// ctx.profile_name) beside the live tables every derive sees.
func TestDeriveEnv_ProducerRunsAndSeesTheSelection(t *testing.T) {
	script := `
yolo.env("claude", function(ctx)
  local out = {}
  local p = ctx.providers[ctx.selected_provider]
  if p then out.ENDPOINT = p.endpoints.anthropic.base_url end
  out.PROFILE = ctx.profile_name
  return out
end)`
	tables := map[string]map[string]any{
		"providers": {"zai": map[string]any{
			"endpoints": map[string]any{
				"anthropic": map[string]any{"base_url": "https://api.z.ai/api/anthropic"},
			},
		}},
	}
	got, err := runEnvDerive(t, "claude", script, tables)
	if err != nil {
		t.Fatal(err)
	}
	if got["ENDPOINT"] != "https://api.z.ai/api/anthropic" {
		t.Errorf("env producer should read the selected provider's endpoint, got %#v", got)
	}
	if got["PROFILE"] != "zai" {
		t.Errorf("env producer should see ctx.profile_name, got %#v", got)
	}
}

// THE COLLISION THE SEPARATE REGISTRATION EXISTS TO MAKE UNREPRESENTABLE: a pack may
// declare a real surface named "env" (yolo.derive(agent, "env", fn)), and that
// registration must satisfy the SURFACE path only — never the environment composition.
// If env were keyed as a surface, this script would hand the launch's provider
// environment to whatever a surface named "env" produced.
func TestDeriveEnv_ASurfaceNamedEnvIsNotTheEnvProducer(t *testing.T) {
	script := `yolo.derive("claude", "env", function(ctx) return { WRONG = "surface" } end)`
	got, err := runEnvDerive(t, "claude", script, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("a yolo.derive(agent, \"env\", …) registration must NOT answer the env "+
			"composition — got %#v from a surface producer", got)
	}
	// And the converse: the surface registration still fires for the surface itself,
	// with the env registration nowhere in its way.
	both := `
yolo.derive("claude", "env", function(ctx) return { WHO = "surface" } end)
yolo.env("claude", function(ctx) return { WHO = "env" } end)`
	surface, err := runDerive(t, "claude", "env", both, nil)
	if err != nil {
		t.Fatal(err)
	}
	if surface["WHO"] != "surface" {
		t.Errorf("the surface named env keeps its own derive, got %#v", surface)
	}
}

// A script that registers no yolo.env for this agent is the identity: no environment
// to compose (the same contract a surface with no derive has).
func TestDeriveEnv_NoRegistrationIsNil(t *testing.T) {
	got, err := runEnvDerive(t, "claude", `yolo.env("other", function() return {} end)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("no yolo.env for this agent → nil, got %#v", got)
	}
}

// A tombstoned env value is a REMOVAL: it decodes present-with-nil, which the runner
// turns into agentenv.Var{Unset: true} — the vocabulary a plain Lua nil cannot spell.
func TestDeriveEnv_TombstoneIsARemoval(t *testing.T) {
	script := `yolo.env("claude", function(ctx) return { AWS_PROFILE = ctx.tombstone, KEEP = "1" } end)`
	got, err := runEnvDerive(t, "claude", script, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, present := got["AWS_PROFILE"]; !present || v != nil {
		t.Errorf("tombstoned env var must be present-with-nil (a removal), got present=%v v=%#v",
			present, v)
	}
	if got["KEEP"] != "1" {
		t.Errorf("KEEP should survive beside the tombstone, got %#v", got)
	}
}

// A non-table return fails loud on the env path too (fail-closed).
func TestDeriveEnv_NonTableReturnFails(t *testing.T) {
	_, err := runEnvDerive(t, "claude", `yolo.env("claude", function() return "nope" end)`, nil)
	if err == nil {
		t.Fatal("an env producer returning a non-table must be a loud error")
	}
}
