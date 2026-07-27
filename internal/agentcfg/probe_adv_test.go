package agentcfg_test

import (
	"fmt"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// 1. TOML int -> float with identity hook
func TestProbeTOMLIntFloat(t *testing.T) {
	s := manifest.Surface{Agent: "codex", Name: "config", Path: "~/.codex/config.toml", Codec: "toml",
		Managed: map[string]any{"approval_policy": "never"}}
	host := []byte("model_max_output_tokens = 8192\n")
	r1, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, HostBytes: host})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("NO HOOK:\n%s", r1.Encoded)
	script := `yolo.transform("codex", function(ctx) end)`
	r2, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, HostBytes: host, Script: script, VM: luahook.GopherLuaVM{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IDENTITY HOOK:\n%s", r2.Encoded)
}

// 2. pairs() nondeterminism building an array
func TestProbePairsNondet(t *testing.T) {
	s := manifest.Surface{Agent: "x", Name: "y", Path: "~/x.json", Codec: "json"}
	host := []byte(`{"m":{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8}}`)
	script := `
yolo.transform("x", function(ctx)
  local out = {}
  for k, v in pairs(ctx.config.m) do
    out[#out+1] = k
  end
  ctx.config.order = out
end)`
	seen := map[string]int{}
	for i := 0; i < 30; i++ {
		r, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, HostBytes: host, Script: script, VM: luahook.GopherLuaVM{}})
		if err != nil {
			t.Fatal(err)
		}
		seen[string(r.Encoded)]++
	}
	t.Logf("distinct outputs over 30 runs: %d", len(seen))
	for k, v := range seen {
		t.Logf("  %dx %s", v, k)
	}
}

// 3. yolo.transform last-one-wins for same agent
func TestProbeLastOneWins(t *testing.T) {
	s := manifest.Surface{Agent: "x", Name: "y", Path: "~/x.json", Codec: "json"}
	script := `
yolo.transform("x", function(ctx) ctx.config.first = true end)
yolo.transform("x", function(ctx) ctx.config.second = true end)
`
	r, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, HostBytes: []byte(`{}`), Script: script, VM: luahook.GopherLuaVM{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("both registered: %s", r.Encoded)
}

// 4. Surface.Transform ignored
func TestProbeSurfaceTransformDead(t *testing.T) {
	s := manifest.Surface{Agent: "x", Name: "y", Path: "~/x.json", Codec: "json", Transform: "/tmp/nonexistent-hook.lua"}
	r, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, HostBytes: []byte(`{"k":1}`)})
	if err != nil {
		t.Fatalf("err with Transform set and no Script: %v", err)
	}
	t.Logf("Transform set, no Script -> %s (no error, field ignored)", r.Encoded)
}

// 5. timeout
func TestProbeTimeout(t *testing.T) {
	s := manifest.Surface{Agent: "x", Name: "y", Path: "~/x.json", Codec: "json"}
	script := `yolo.transform("x", function(ctx) while true do end end)`
	_, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, HostBytes: []byte(`{}`), Script: script,
		VM: luahook.GopherLuaVM{Timeout: 300000000}})
	t.Logf("runaway loop err: %v", err)
}

// 6. Can Lua do the opencode fold? Also check surface/agent keying.
func TestProbeOpencodeFold(t *testing.T) {
	s := manifest.Surface{Agent: "opencode", Name: "config", Path: "~/x.json", Codec: "json"}
	computed := map[string]any{"mcp": map[string]any{
		"srv":  map[string]any{"command": "/bin/node", "args": []any{"a", "b"}, "env": map[string]any{"K": "V"}},
		"bare": map[string]any{"command": "/bin/x"},
	}}
	script := `
yolo.transform("opencode", function(ctx)
  local out = {}
  for name, cfg in pairs(ctx.config.mcp) do
    local cmd = { cfg.command }
    if cfg.args then for _, a in ipairs(cfg.args) do cmd[#cmd+1] = a end end
    local e = { type = "local", command = cmd, enabled = true }
    if cfg.env and next(cfg.env) ~= nil then e.environment = cfg.env end
    out[name] = e
  end
  ctx.config.mcp = out
end)`
	r, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, Computed: computed, Script: script, VM: luahook.GopherLuaVM{}})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(r.Encoded))
}
