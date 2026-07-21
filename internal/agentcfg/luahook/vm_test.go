package luahook

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// realVM is the gopher-lua-backed VM under test, with a short timeout so the
// infinite-loop test fails fast.
func realVM() GopherLuaVM { return GopherLuaVM{Timeout: 2 * time.Second} }

// TestRealVM_DropsListElement is the §6.5 pi permission-gate shape, run through
// the ACTUAL Lua VM: the transform iterates ctx.config.extensions, drops the
// permission-gate entry via plain Lua, reassigns ctx.config.extensions, and
// records a stage exclude. Proves the ctx bridge, list round-trip, and stage
// closure all work end to end.
func TestRealVM_DropsListElement(t *testing.T) {
	script := `
yolo.transform("pi", function(ctx)
  local kept = {}
  for _, ext in ipairs(ctx.config.extensions) do
    if not ext:find("permission%-gate") then kept[#kept + 1] = ext end
  end
  ctx.config.extensions = kept
  ctx.stage.exclude("extensions/permission-gate.ts")
end)`
	ctx := NewCtx("pi", "settings", map[string]any{
		"theme":        "dark",
		"defaultModel": "claude-fable-5",
		"extensions": []any{
			"extensions/permission-gate.ts",
			"extensions/git-helper.ts",
		},
	}, map[string]any{"defaultProjectTrust": "always"})

	got, err := Apply(Transform{VM: realVM(), Script: script}, ctx)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	wantExts := []any{"extensions/git-helper.ts"}
	if !reflect.DeepEqual(got["extensions"], wantExts) {
		t.Errorf("extensions = %#v, want %#v", got["extensions"], wantExts)
	}
	if got["theme"] != "dark" || got["defaultModel"] != "claude-fable-5" {
		t.Errorf("unexpected mutation of untouched keys: %#v", got)
	}
	if excl := ctx.Stage.Excluded(); len(excl) != 1 || excl[0] != "extensions/permission-gate.ts" {
		t.Errorf("Stage.Excluded() = %v, want [extensions/permission-gate.ts]", excl)
	}

	// Enforce re-applies the managed key AFTER the hook (§3.1).
	ctx.Enforce()
	if got["defaultProjectTrust"] != "always" {
		t.Errorf("managed key not enforced: %v", got["defaultProjectTrust"])
	}
}

// TestRealVM_ForbiddenGlobalUnavailable proves os (and its friends) are absent
// from the sandbox: os.execute is a runtime error (indexing nil), surfaced as a
// loud Go error (§3.4). Note ValidateSandbox would also reject this statically;
// here we bypass that and confirm the VM env-strip is itself the boundary.
func TestRealVM_ForbiddenGlobalUnavailable(t *testing.T) {
	forbidden := []struct {
		name, script string
	}{
		{"os.execute", `yolo.transform("pi", function(ctx) os.execute("id") end)`},
		{"io.open", `yolo.transform("pi", function(ctx) io.open("/etc/passwd") end)`},
		{"require", `yolo.transform("pi", function(ctx) require("socket") end)`},
		{"loadstring", `yolo.transform("pi", function(ctx) loadstring("return 1")() end)`},
		{"dofile", `yolo.transform("pi", function(ctx) dofile("/tmp/x.lua") end)`},
		{"package", `yolo.transform("pi", function(ctx) local p = package.loaded end)`},
	}
	for _, tc := range forbidden {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewCtx("pi", "settings", map[string]any{}, nil)
			_, err := Apply(Transform{VM: realVM(), Script: tc.script}, ctx)
			if err == nil {
				t.Fatalf("%s was reachable in the sandbox — want a loud error", tc.name)
			}
		})
	}
}

// TestRealVM_SafeLibsAvailable is the positive companion: the AllowedGlobals
// (string/table/math + base builtins) ARE present, so a legitimate transform
// using them runs.
func TestRealVM_SafeLibsAvailable(t *testing.T) {
	script := `
yolo.transform("pi", function(ctx)
  ctx.config.upper = string.upper(ctx.config.name)
  ctx.config.count = #ctx.config.list
  ctx.config.max = math.max(1, 2, 3)
  table.insert(ctx.config.list, "d")
end)`
	ctx := NewCtx("pi", "settings", map[string]any{
		"name": "pi",
		"list": []any{"a", "b", "c"},
	}, nil)
	got, err := Apply(Transform{VM: realVM(), Script: script}, ctx)
	if err != nil {
		t.Fatalf("Apply errored: %v", err)
	}
	if got["upper"] != "PI" {
		t.Errorf("string.upper failed: %v", got["upper"])
	}
	if got["count"] != float64(3) {
		t.Errorf("table length failed: %v (%T)", got["count"], got["count"])
	}
	if got["max"] != float64(3) {
		t.Errorf("math.max failed: %v", got["max"])
	}
	if !reflect.DeepEqual(got["list"], []any{"a", "b", "c", "d"}) {
		t.Errorf("table.insert failed: %#v", got["list"])
	}
}

// TestRealVM_InfiniteLoopFailsClosed proves the instruction/wall-clock budget
// converts a runaway loop into a Go error instead of hanging the render (§3.4
// fail-closed). Runs with a very short timeout so the test is fast.
func TestRealVM_InfiniteLoopFailsClosed(t *testing.T) {
	script := `yolo.transform("pi", function(ctx) while true do end end)`
	ctx := NewCtx("pi", "settings", map[string]any{"theme": "dark"}, nil)

	done := make(chan struct{})
	var got map[string]any
	var err error
	go func() {
		got, err = Apply(Transform{VM: GopherLuaVM{Timeout: 200 * time.Millisecond}, Script: script}, ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("infinite-loop transform did not time out — sandbox is not fail-closed")
	}
	if err == nil {
		t.Fatal("infinite loop returned nil error, want a surfaced timeout error")
	}
	if got != nil {
		t.Errorf("Apply returned non-nil config on timeout: %#v", got)
	}
}

// TestRealVM_LuaErrorHasLocation proves a runtime Lua error (indexing a nil
// field) surfaces as a Go error carrying the source location + message (§3.4
// "file, line, and message").
func TestRealVM_LuaErrorHasLocation(t *testing.T) {
	script := `yolo.transform("pi", function(ctx)
  local x = ctx.config.missing.deep
end)`
	ctx := NewCtx("pi", "settings", map[string]any{}, nil)
	_, err := Apply(Transform{VM: realVM(), Script: script}, ctx)
	if err == nil {
		t.Fatal("nil-index transform returned nil error, want a loud failure")
	}
	msg := err.Error()
	// gopher-lua encodes location as "<source>:<line>: <message>".
	if !strings.Contains(msg, ":") || !strings.Contains(strings.ToLower(msg), "nil") {
		t.Errorf("error lacks location/message detail: %q", msg)
	}
}

// TestRealVM_ManagedNotSurvivingEnforce proves ctx.managed mutations don't
// survive: the sandbox exposes managed read-only (a write raises), and even if
// a transform sets the key on ctx.config, Enforce re-applies the original
// managed value afterward. Two-pronged: an attempted write to ctx.managed is a
// loud error, and enforcement wins regardless.
func TestRealVM_ManagedNotSurvivingEnforce(t *testing.T) {
	// (a) writing to ctx.managed is rejected by the read-only proxy.
	writeScript := `yolo.transform("pi", function(ctx)
  ctx.managed.defaultProjectTrust = "never"
end)`
	ctx := NewCtx("pi", "settings", map[string]any{}, map[string]any{"defaultProjectTrust": "always"})
	if _, err := Apply(Transform{VM: realVM(), Script: writeScript}, ctx); err == nil {
		t.Fatal("writing ctx.managed succeeded, want a read-only error")
	}

	// (b) a transform CAN read ctx.managed and CAN set ctx.config, but Enforce
	// re-asserts the managed value, so the transform cannot drop it from the
	// generated file.
	overrideScript := `yolo.transform("pi", function(ctx)
  -- read-only view is visible
  ctx.config.saw = ctx.managed.defaultProjectTrust
  -- try to override the enforced key in the output
  ctx.config.defaultProjectTrust = "never"
end)`
	ctx2 := NewCtx("pi", "settings", map[string]any{}, map[string]any{"defaultProjectTrust": "always"})
	got, err := Apply(Transform{VM: realVM(), Script: overrideScript}, ctx2)
	if err != nil {
		t.Fatalf("Apply errored: %v", err)
	}
	if got["saw"] != "always" {
		t.Errorf("ctx.managed not readable: saw = %v", got["saw"])
	}
	if got["defaultProjectTrust"] != "never" {
		t.Errorf("pre-Enforce override not applied: %v", got["defaultProjectTrust"])
	}
	ctx2.Enforce()
	if got["defaultProjectTrust"] != "always" {
		t.Errorf("managed mutation survived Enforce: %v, want always", got["defaultProjectTrust"])
	}
}

// TestRealVM_OtherAgentIsNoop proves a script that registers a transform for a
// different agent leaves this surface's config untouched (§3.4).
func TestRealVM_OtherAgentIsNoop(t *testing.T) {
	script := `yolo.transform("claude", function(ctx) ctx.config.wrecked = true end)`
	ctx := NewCtx("pi", "settings", map[string]any{"theme": "dark"}, nil)
	got, err := Apply(Transform{VM: realVM(), Script: script}, ctx)
	if err != nil {
		t.Fatalf("Apply errored: %v", err)
	}
	if _, ok := got["wrecked"]; ok {
		t.Errorf("a different agent's transform fired: %#v", got)
	}
	if got["theme"] != "dark" {
		t.Errorf("config mutated by unrelated transform: %#v", got)
	}
}

// TestRealVM_CompileErrorSurfaces proves a syntax error in the script fails
// closed with a Go error rather than silently passing through.
func TestRealVM_CompileErrorSurfaces(t *testing.T) {
	script := `yolo.transform("pi", function(ctx) this is not lua end)`
	ctx := NewCtx("pi", "settings", map[string]any{}, nil)
	if _, err := Apply(Transform{VM: realVM(), Script: script}, ctx); err == nil {
		t.Fatal("syntax error returned nil, want a loud compile error")
	}
}

// TestRealVM_NestedRoundTrip exercises the marshalling fidelity notes: nested
// maps and arrays survive Go -> Lua -> Go, and numbers come back as float64.
func TestRealVM_NestedRoundTrip(t *testing.T) {
	script := `yolo.transform("pi", function(ctx)
  ctx.config.nested.arr[2] = ctx.config.nested.arr[2] + 10
  ctx.config.nested.added = { x = 1, y = { "deep" } }
end)`
	ctx := NewCtx("pi", "settings", map[string]any{
		"nested": map[string]any{
			"arr":  []any{float64(1), float64(2), float64(3)},
			"name": "n",
		},
	}, nil)
	got, err := Apply(Transform{VM: realVM(), Script: script}, ctx)
	if err != nil {
		t.Fatalf("Apply errored: %v", err)
	}
	nested := got["nested"].(map[string]any)
	if !reflect.DeepEqual(nested["arr"], []any{float64(1), float64(12), float64(3)}) {
		t.Errorf("nested array round-trip failed: %#v", nested["arr"])
	}
	added := nested["added"].(map[string]any)
	if added["x"] != float64(1) {
		t.Errorf("added.x = %v (%T), want float64(1)", added["x"], added["x"])
	}
	if !reflect.DeepEqual(added["y"], []any{"deep"}) {
		t.Errorf("added.y round-trip failed: %#v", added["y"])
	}
}

// TestRealVM_IntRoundTripBecomesFloat documents the Lua number model: a Go
// int64 in the input comes back as float64 (Lua has no integer type). This is
// the fidelity caveat marshal.go warns about, asserted so a future change that
// silently alters it is caught.
func TestRealVM_IntRoundTripBecomesFloat(t *testing.T) {
	script := `yolo.transform("pi", function(ctx) ctx.config.echo = ctx.config.n end)`
	ctx := NewCtx("pi", "settings", map[string]any{"n": int64(42)}, nil)
	got, err := Apply(Transform{VM: realVM(), Script: script}, ctx)
	if err != nil {
		t.Fatalf("Apply errored: %v", err)
	}
	if got["echo"] != float64(42) {
		t.Errorf("int64 did not round-trip to float64(42): %v (%T)", got["echo"], got["echo"])
	}
	if got["n"] != float64(42) {
		t.Errorf("input int64 came back as %v (%T), want float64(42)", got["n"], got["n"])
	}
}
