package luahook

// derivesandbox_test.go pins the SHARED half of this package — the sandbox the
// VM builds (openSandboxLibs + ForbiddenGlobals + the wall-clock budget) and the
// Go<->Lua marshallers — through the DERIVE entry point.
//
// WHY IT IS A SEPARATE FILE FROM derive_test.go. These proofs are not about what
// a derive does; they are about the properties every script this package runs
// gets, whoever runs it. They lived in vm_test.go and were asserted through
// Apply — the config TRANSFORM, which docs/design/lua-transform-removal.md
// removes (§4.1, §5.5). Deleting vm_test.go with the transform would take the
// forbidden-globals, safe-libs, timeout, error-location, compile-error and
// round-trip proofs with it and leave the derive sandbox asserted by
// TestDerive_Sandboxed alone — one script, one global. P1 of that design
// forbids that order: the proofs are re-expressed HERE first, against the caller
// that stays, and vm_test.go goes afterwards.
//
// Each test names the vm_test.go original it re-homes, so the pair can be
// checked against each other for as long as both exist.

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestDeriveSandbox_ForbiddenGlobalUnavailable re-homes
// TestRealVM_ForbiddenGlobalUnavailable: os (and its friends) are absent from
// the sandbox environment, so reaching for one is a loud Go error rather than a
// silent escape. The VM env-strip is the boundary being asserted — not
// ValidateSandbox, the static lint that never had a production caller and goes
// with the transform.
//
// derive_test.go's TestDerive_Sandboxed proves this for `os` alone; this is the
// whole ForbiddenGlobals-shaped table vm_test.go carried.
func TestDeriveSandbox_ForbiddenGlobalUnavailable(t *testing.T) {
	forbidden := []struct {
		name, body string
	}{
		{"os.execute", `os.execute("id")`},
		{"io.open", `io.open("/etc/passwd")`},
		{"require", `require("socket")`},
		{"loadstring", `loadstring("return 1")()`},
		{"dofile", `dofile("/tmp/x.lua")`},
		{"package", `local p = package.loaded`},
	}
	for _, tc := range forbidden {
		t.Run(tc.name, func(t *testing.T) {
			script := `yolo.derive("pi", "settings", function(ctx) ` + tc.body + ` return {} end)`
			if _, err := runDerive(t, "pi", "settings", script, nil); err == nil {
				t.Fatalf("%s was reachable in the derive sandbox — want a loud error", tc.name)
			}
		})
	}
}

// TestDeriveSandbox_SafeLibsAvailable re-homes TestRealVM_SafeLibsAvailable: the
// positive companion. The deterministic, side-effect-free stock libs (string,
// table, math, and the base builtins) ARE opened, so a legitimate producer using
// them runs.
//
// The live tables are the only Go values a derive can read, so they double as
// this test's marshalling fixture; the entries are shaped for the Lua calls
// rather than for MCP.
func TestDeriveSandbox_SafeLibsAvailable(t *testing.T) {
	script := `
yolo.derive("pi", "settings", function(ctx)
  local list = {}
  for _, v in ipairs(ctx.mcp_servers.list) do list[#list + 1] = v end
  table.insert(list, "d")
  return {
    upper = string.upper(ctx.mcp_servers.name),
    count = #ctx.mcp_servers.list,
    max   = math.max(1, 2, 3),
    list  = list,
  }
end)`
	tables := map[string]map[string]any{
		"mcp_servers": {"name": "pi", "list": []any{"a", "b", "c"}},
	}
	got, err := runDerive(t, "pi", "settings", script, tables)
	if err != nil {
		t.Fatalf("Derive errored: %v", err)
	}
	if got["upper"] != "PI" {
		t.Errorf("string.upper failed: %v", got["upper"])
	}
	// F4: an integral Lua number comes back as an integer, so #t and math.max
	// yield int64 rather than float64.
	if got["count"] != int64(3) {
		t.Errorf("table length failed: %v (%T)", got["count"], got["count"])
	}
	if got["max"] != int64(3) {
		t.Errorf("math.max failed: %v (%T)", got["max"], got["max"])
	}
	if !reflect.DeepEqual(got["list"], []any{"a", "b", "c", "d"}) {
		t.Errorf("table.insert failed: %#v", got["list"])
	}
}

// TestDeriveSandbox_InfiniteLoopFailsClosed re-homes
// TestRealVM_InfiniteLoopFailsClosed: the wall-clock budget converts a runaway
// loop into a Go error instead of hanging the boot render. Runs with a very
// short timeout so the test is fast.
func TestDeriveSandbox_InfiniteLoopFailsClosed(t *testing.T) {
	script := `yolo.derive("pi", "settings", function(ctx) while true do end end)`

	done := make(chan struct{})
	var got map[string]any
	var err error
	go func() {
		got, err = GopherLuaVM{Timeout: 200 * time.Millisecond}.Derive(script,
			&DeriveCtx{Agent: "pi", Surface: "settings"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("infinite-loop derive did not time out — the sandbox is not fail-closed")
	}
	if err == nil {
		t.Fatal("infinite loop returned nil error, want a surfaced timeout error")
	}
	if got != nil {
		t.Errorf("Derive returned a non-nil computed layer on timeout: %#v", got)
	}
}

// TestDeriveSandbox_LuaErrorHasLocation re-homes TestRealVM_LuaErrorHasLocation:
// a runtime Lua error (indexing a nil field) surfaces as a Go error carrying the
// source location and message, not a bare "failed".
func TestDeriveSandbox_LuaErrorHasLocation(t *testing.T) {
	script := `
yolo.derive("pi", "settings", function(ctx)
  local x = ctx.mcp_servers.missing.deep
  return {}
end)`
	_, err := runDerive(t, "pi", "settings", script, nil)
	if err == nil {
		t.Fatal("nil-index derive returned nil error, want a loud failure")
	}
	msg := err.Error()
	// gopher-lua encodes location as "<source>:<line>: <message>".
	if !strings.Contains(msg, ":") || !strings.Contains(strings.ToLower(msg), "nil") {
		t.Errorf("error lacks location/message detail: %q", msg)
	}
}

// TestDeriveSandbox_CompileErrorSurfaces re-homes
// TestRealVM_CompileErrorSurfaces: a syntax error in the script fails closed
// with a Go error rather than silently registering nothing and rendering the
// identity.
func TestDeriveSandbox_CompileErrorSurfaces(t *testing.T) {
	script := `yolo.derive("pi", "settings", function(ctx) this is not lua end)`
	if _, err := runDerive(t, "pi", "settings", script, nil); err == nil {
		t.Fatal("syntax error returned nil, want a loud compile error")
	}
}

// TestDeriveSandbox_NestedRoundTrip re-homes TestRealVM_NestedRoundTrip: the
// marshallers' fidelity contract. Nested maps and arrays survive Go -> Lua -> Go
// in both directions — in through buildDeriveCtxTable's goToLua, out through
// deriveTableToGo's luaToGo.
func TestDeriveSandbox_NestedRoundTrip(t *testing.T) {
	script := `
yolo.derive("pi", "settings", function(ctx)
  local src = ctx.mcp_servers.nested
  local arr = {}
  for i, v in ipairs(src.arr) do arr[i] = v end
  arr[2] = arr[2] + 10
  return { nested = { arr = arr, name = src.name, added = { x = 1, y = { "deep" } } } }
end)`
	tables := map[string]map[string]any{
		"mcp_servers": {"nested": map[string]any{
			"arr":  []any{float64(1), float64(2), float64(3)},
			"name": "n",
		}},
	}
	got, err := runDerive(t, "pi", "settings", script, tables)
	if err != nil {
		t.Fatalf("Derive errored: %v", err)
	}
	nested, _ := got["nested"].(map[string]any)
	if nested == nil {
		t.Fatalf("nested is %T, want map[string]any: %#v", got["nested"], got)
	}
	// F4: integral values come back as int64. Note the INPUT was float64(1..3) and
	// the output is int64 — a float64 that happens to be integral is not
	// distinguishable from an int inside Lua (one numeric type), so this direction
	// of the round-trip is lossy by construction. That trade is deliberate: JSON has
	// no int/float distinction so it is invisible there, while TOML DOES, and
	// preserving integers is what stops a producer rewriting `8192` as `8192.0`.
	if !reflect.DeepEqual(nested["arr"], []any{int64(1), int64(12), int64(3)}) {
		t.Errorf("nested array round-trip failed: %#v", nested["arr"])
	}
	if nested["name"] != "n" {
		t.Errorf("nested string round-trip failed: %#v", nested["name"])
	}
	added, _ := nested["added"].(map[string]any)
	if added["x"] != int64(1) {
		t.Errorf("added.x = %v (%T), want int64(1) — F4 preserves integrality", added["x"], added["x"])
	}
	if !reflect.DeepEqual(added["y"], []any{"deep"}) {
		t.Errorf("added.y round-trip failed: %#v", added["y"])
	}
}

// TestDeriveSandbox_IntRoundTripsAsInt re-homes TestRealVM_IntRoundTripsAsInt —
// F4, and the reason it is not a nicety.
//
// The original test asserted the OPPOSITE — that an int64 comes back as float64 —
// documenting the Lua single-number-type model as an accepted fidelity caveat. It
// was not acceptable: probed on a real TOML surface, an IDENTITY hook that touched
// nothing turned `max_tokens = 8192` into `8192.0`, because the TOML emitter
// faithfully renders whatever it is handed. So any Lua at all in the pipeline
// silently corrupted every integer in every TOML surface.
//
// luaToGo returns an integer type for an integral Lua number. Lua numbers are
// float64 internally, so integral values are exactly representable and this
// round-trips losslessly; genuinely fractional values stay floats (asserted
// below). The derive path is now the only caller, and pi/models is a real TOML
// surface, so this proof has to hold here or the corruption returns.
func TestDeriveSandbox_IntRoundTripsAsInt(t *testing.T) {
	script := `
yolo.derive("pi", "models", function(ctx)
  return { echo = ctx.mcp_servers.n, n = ctx.mcp_servers.n, f = ctx.mcp_servers.f }
end)`
	tables := map[string]map[string]any{
		"mcp_servers": {"n": int64(42), "f": 1.5},
	}
	got, err := runDerive(t, "pi", "models", script, tables)
	if err != nil {
		t.Fatalf("Derive errored: %v", err)
	}
	if got["echo"] != int64(42) {
		t.Errorf("int did not survive the round-trip: %v (%T), want int64(42)", got["echo"], got["echo"])
	}
	if got["n"] != int64(42) {
		t.Errorf("input int came back as %v (%T), want int64(42)", got["n"], got["n"])
	}
	// A real fraction must NOT be coerced to an int.
	if got["f"] != 1.5 {
		t.Errorf("fractional value corrupted: %v (%T), want 1.5", got["f"], got["f"])
	}
}
