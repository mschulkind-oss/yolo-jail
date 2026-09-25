package luahook

// The in-full sentinel, ctx.in_full (CO13, docs/design/config-ownership-and-promotion.md
// #co13--how-a-derive-says-it-fills-a-computed-table-in-full--decided): a derive's
// declaration that it regenerates a computed table in full. What these pin is the decode:
// the wrapper leaves the layer entirely, the key it sat under is reported beside it, and
// every shape it cannot honor is refused rather than read at the wrong depth.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func deriveLayer(t *testing.T, script string, tables map[string]map[string]any) (DeriveOutput, error) {
	t.Helper()
	return GopherLuaVM{}.DeriveLayer(script, &DeriveCtx{Agent: "acme", Surface: "cfg", Tables: tables})
}

// The declaration is reported, sorted, and the layer holds the PLAIN tables — the same
// values an unwrapped derive would have produced, so no reader downstream has a wrapper to
// strip. The table beside them that is NOT declared in full is the control: it is a table
// too, and it is not in the list, which is the whole distinction.
func TestDeriveLayer_InFullDeclaresTopLevelTables(t *testing.T) {
	script := `
yolo.derive("acme", "cfg", function(ctx)
  local servers = {}
  for name, s in pairs(ctx.mcp_servers) do servers[name] = { command = s.command } end
  return {
    zServers = ctx.in_full(servers),
    aTools   = ctx.in_full({ node = "22" }),
    env      = { ENABLE_LSP_TOOL = "1" },
    gone     = ctx.tombstone,
  }
end)`
	out, err := deriveLayer(t, script, map[string]map[string]any{
		"mcp_servers": {"fs": map[string]any{"command": "mcp-fs"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"aTools", "zServers"}; !reflect.DeepEqual(out.InFull, want) {
		t.Errorf("InFull = %v, want %v (sorted, and never env, which is not declared in full)", out.InFull, want)
	}
	want := map[string]any{
		"zServers": map[string]any{"fs": map[string]any{"command": "mcp-fs"}},
		"aTools":   map[string]any{"node": "22"},
		"env":      map[string]any{"ENABLE_LSP_TOOL": "1"},
		"gone":     nil,
	}
	if !reflect.DeepEqual(out.Layer, want) {
		t.Errorf("layer:\n got: %#v\nwant: %#v", out.Layer, want)
	}
	// Derive is the same run without the declarations.
	plain, err := GopherLuaVM{}.Derive(script, &DeriveCtx{Agent: "acme", Surface: "cfg",
		Tables: map[string]map[string]any{"mcp_servers": {"fs": map[string]any{"command": "mcp-fs"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain, out.Layer) {
		t.Errorf("Derive = %#v, want DeriveLayer's layer exactly", plain)
	}
}

// An EMPTY wrapped table is still an object and still declared: "yolo regenerates this
// table and has nothing in it this run". What an empty declared table may CLAIM is the
// consumer's rule (agentcfg's adoption drop claims nothing for one), not the decoder's.
func TestDeriveLayer_InFullEmptyTableIsAnEmptyObject(t *testing.T) {
	out, err := deriveLayer(t, `
yolo.derive("acme", "cfg", function(ctx)
  return { mcpServers = ctx.in_full({}) }
end)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.InFull, []string{"mcpServers"}) {
		t.Errorf("InFull = %v, want [mcpServers]", out.InFull)
	}
	if v, ok := out.Layer["mcpServers"].(map[string]any); !ok || len(v) != 0 {
		t.Errorf("mcpServers = %#v, want an empty OBJECT", out.Layer["mcpServers"])
	}
}

// No declaration at all is nil, not an empty slice — the zero value every existing derive
// produces.
func TestDeriveLayer_NoDeclarationIsNil(t *testing.T) {
	out, err := deriveLayer(t, `
yolo.derive("acme", "cfg", function(ctx) return { env = { A = "1" } } end)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.InFull != nil {
		t.Errorf("InFull = %#v, want nil", out.InFull)
	}
}

// The shapes the declaration cannot mean, each refused with a message naming the sentinel:
// a non-table argument (at the call), an array (no named entries to call stale), and a
// wrapper below the top level (nothing reads a nested declaration, so honoring it there
// would be silent).
func TestDeriveLayer_InFullRefusals(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"non-table": {`return { x = ctx.in_full("nope") }`, "table expected"},
		"array":     {`return { x = ctx.in_full({ "a", "b" }) }`, "not an object"},
		"nested":    {`return { outer = { inner = ctx.in_full({ a = 1 }) } }`, "below the top level"},
		"in-array":  {`return { outer = { ctx.in_full({ a = 1 }) } }`, "userdata"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := deriveLayer(t, `yolo.derive("acme", "cfg", function(ctx) `+tc.body+` end)`, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// inFullProbeCtx is a live-config world in which every shipped derive produces every
// table it CAN produce: an MCP server, an LSP server, and one provider reachable by each
// agent's gate (an openai endpoint speaking openai-responses, which codex, opencode, pi and
// omp all accept). A table a derive does not produce here cannot have its declaration
// checked, so the world is populated rather than minimal.
func inFullProbeCtx(agent, surface string) *DeriveCtx {
	return &DeriveCtx{
		Agent:   agent,
		Surface: surface,
		Tables: map[string]map[string]any{
			"mcp_servers": {"probe": map[string]any{"command": "/bin/true"}},
			"lsp_servers": {"probelsp": map[string]any{"command": "/bin/true"}},
			"providers": {"probeprov": map[string]any{
				"endpoints": map[string]any{"openai": map[string]any{
					"base_url": "https://probe.example/v1", "wire_api": "openai-responses",
				}},
				"api_key_env_name": "PROBE_API_KEY",
				"models":           map[string]any{"default": "probe-model"},
			}},
		},
	}
}

// inFullSelectedProbeCtx is inFullProbeCtx with a provider SELECTED — the shipped
// `openai-codex` subscription, with the capabilities-only row packs/openai-auth declares.
// Two tables are produced only under a selection (claude/settings' `modelPicker` and
// pi/settings' `subagents`), and both are tables the classification says are NOT declared
// in full, so without this second world neither half of their classification is checked.
func inFullSelectedProbeCtx(agent, surface string) *DeriveCtx {
	ctx := inFullProbeCtx(agent, surface)
	ctx.SelectedProvider = "openai-codex"
	ctx.ProfileName = "codex"
	ctx.Profile = map[string]string{}
	ctx.Tables["providers"]["openai-codex"] = map[string]any{
		"capabilities": []any{"web_search"},
		"endpoints": map[string]any{"openai-responses": map[string]any{
			"base_url": "https://chatgpt.com/backend-api/codex", "wire_api": "openai-responses",
		}},
	}
	return ctx
}

// deriveWithoutInFull runs one producer the way an entrypoint OLDER than the sentinel
// would: ctx carries no in_full field at all. It is DeriveLayer's body with that one
// difference, kept here rather than behind a production knob no caller needs.
func deriveWithoutInFull(t *testing.T, script string, ctx *DeriveCtx) (map[string]any, []string) {
	t.Helper()
	s, err := newDeriveSession(GopherLuaVM{}, script, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	s.ctxTable.RawSetString(inFullName, lua.LNil)
	fn, ok := s.derives[deriveKey{ctx.Agent, ctx.Surface}]
	if !ok {
		t.Fatalf("no producer for %s/%s", ctx.Agent, ctx.Surface)
	}
	if err := s.L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, s.ctxTable); err != nil {
		t.Fatalf("%s/%s without ctx.in_full: %v — a derive.lua that calls the sentinel "+
			"unguarded kills every jail whose entrypoint predates it", ctx.Agent, ctx.Surface, err)
	}
	ret := s.L.Get(-1)
	s.L.Pop(1)
	tbl, isTable := ret.(*lua.LTable)
	if !isTable {
		t.Fatalf("%s/%s returned %s", ctx.Agent, ctx.Surface, ret.Type())
	}
	out, inFull, err := deriveTableToGo(tbl, s.sentinel, s.emptyArr)
	if err != nil {
		t.Fatal(err)
	}
	return out, inFull
}

// THE SHIPPED DECLARATIONS, per surface, and the two properties each must keep.
//
//  1. WHICH tables are declared. Every table whose entries TRACK a live table (an MCP, LSP
//     or provider catalog) is declared in full; a table with a FIXED key set (claude's
//     `env`, `modelPicker`; pi's `subagents`) is not. That is the classification the CO13
//     section of docs/design/config-ownership-and-promotion.md measures a declaration
//     against, and a surface missing from `want` fails, so a new producer has to be
//     classified by whoever adds it. Both halves are checked by PRODUCING the table: a
//     declared key the probe worlds never produce, or a fixed-key-set table (`notInFull`)
//     they never produce, is a classification nothing checked, and fails. Two worlds are
//     needed because `modelPicker` and `subagents` exist only under a selected provider.
//  2. THE SKEW FALLBACK. Each derive.lua reaches the sentinel through a local guard, so an
//     entrypoint older than ctx.in_full runs it and gets the SAME layer with no declaration
//     — which that build reads the way it always did. The yolo.env incident (DeriveCtx
//     .UnknownAPI) is what an unguarded call costs.
func TestShippedDerivesDeclareTheirInFullTables(t *testing.T) {
	want := map[string][]string{
		"agy/mcp":         {"mcpServers"},
		"claude/config":   {"mcpServers"},
		"claude/settings": nil, // env is asserted leaf by leaf — the CO13 case itself
		"codex/config":    {"mcp_servers", "model_providers"},
		"copilot/lsp":     {"lspServers"},
		"copilot/mcp":     {"mcpServers"},
		"oh-omp/models":   {"providers"},
		"opencode/config": {"mcp", "provider"},
		"pi/mcp":          {"mcpServers"},
		"pi/models":       {"providers"},
		"pi/settings":     nil,
	}
	// The fixed-key-set tables, the other half of the classification: each must be
	// PRODUCED by one of the two probe worlds and never declared, or a later in_full around
	// it passes this test while an adopting render drops the user's own leaves under it.
	notInFull := map[string][]string{
		"claude/settings": {"env", "modelPicker"},
		"pi/settings":     {"subagents"},
	}
	paths, err := filepath.Glob("../../../packs/*/derive.lua")
	if err != nil || len(paths) == 0 {
		t.Fatalf("found no packs/*/derive.lua (%v): the glob is wrong, or the tree moved", err)
	}
	seen := map[string]bool{}
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		regs, err := (GopherLuaVM{}).DeriveRegistrations(string(src))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		for _, r := range regs {
			id := r.Agent + "/" + r.Surface
			seen[id] = true
			wantKeys, classified := want[id]
			if !classified {
				t.Errorf("%s is a surface producer nobody classified: add it to want with the "+
					"tables it regenerates in full (nil for none)", id)
				continue
			}
			out, err := (GopherLuaVM{}).DeriveLayer(string(src), inFullProbeCtx(r.Agent, r.Surface))
			if err != nil {
				t.Errorf("%s: %v", id, err)
				continue
			}
			if !reflect.DeepEqual(out.InFull, wantKeys) {
				t.Errorf("%s declares %v in full, want %v", id, out.InFull, wantKeys)
			}
			for _, k := range wantKeys {
				if _, produced := out.Layer[k]; !produced {
					t.Errorf("%s: probe world produced no %q, so its declaration went "+
						"unchecked — populate inFullProbeCtx", id, k)
				}
			}
			// THE SELECTED WORLD. Not an exact-list comparison, since a selection may leave a
			// catalog out; instead every object-valued key it produces is classified — declared
			// exactly when `want` lists it.
			sel, err := (GopherLuaVM{}).DeriveLayer(string(src), inFullSelectedProbeCtx(r.Agent, r.Surface))
			if err != nil {
				t.Errorf("%s (selected): %v", id, err)
				continue
			}
			for k, v := range sel.Layer {
				if _, isObj := v.(map[string]any); !isObj {
					continue
				}
				if declared, expected := listHas(sel.InFull, k), listHas(wantKeys, k); declared != expected {
					t.Errorf("%s (selected): %q declared in full = %v, want %v", id, k, declared, expected)
				}
			}
			for _, k := range notInFull[id] {
				if listHas(wantKeys, k) {
					t.Fatalf("%s: %q is classified both ways — fix want or notInFull", id, k)
				}
				_, inBase := out.Layer[k].(map[string]any)
				_, inSel := sel.Layer[k].(map[string]any)
				if !inBase && !inSel {
					t.Errorf("%s: neither probe world produced the table %q, so its "+
						"classification (not in full) went unchecked — populate a probe world", id, k)
				}
			}

			for world, ctx := range map[string]func(string, string) *DeriveCtx{
				"base": inFullProbeCtx, "selected": inFullSelectedProbeCtx,
			} {
				layer := out.Layer
				if world == "selected" {
					layer = sel.Layer
				}
				old, oldInFull := deriveWithoutInFull(t, string(src), ctx(r.Agent, r.Surface))
				if oldInFull != nil {
					t.Errorf("%s (%s): an older build declared %v — the fallback must pass the bare table",
						id, world, oldInFull)
				}
				if !reflect.DeepEqual(old, layer) {
					t.Errorf("%s (%s): an older build gets a different layer\n old: %#v\n new: %#v",
						id, world, old, layer)
				}
			}
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s is classified but no shipped derive registers it — did a pack drop it?", id)
		}
	}
}

func listHas(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
