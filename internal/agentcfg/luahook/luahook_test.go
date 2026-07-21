package luahook

import (
	"errors"
	"reflect"
	"testing"
)

// fakeVM is a hand-rolled stand-in for the gopher-lua-backed LuaVM (see the
// package doc TODO). It exercises the CONTRACT, not real Lua: a transform is a
// Go func(*Ctx) error keyed by agent, and Run dispatches on ctx.Agent exactly
// as the real VM would dispatch on the agent passed to yolo.transform(agent,
// fn). Script text is used only as the lookup key here — enough to prove the
// ctx bridge shape and the error/read-only contracts a real VM must honor.
type fakeVM struct {
	hooks map[string]func(*Ctx) error
}

func (f fakeVM) Run(script string, ctx *Ctx) error {
	hook, ok := f.hooks[script]
	if !ok {
		// A script that registers no hook for this agent is a no-op in the
		// real VM (the registration for another agent simply doesn't fire).
		return nil
	}
	return hook(ctx)
}

// piGateScript is the lookup key standing in for the §6.5 pi permission-gate
// transform. The fake maps it to dropGateHook below.
const piGateScript = "pi-permission-gate"

// dropGateHook is the Go mirror of the §6.5 Lua transform: drop any extension
// whose name contains "permission-gate" and exclude its file from staging.
func dropGateHook(ctx *Ctx) error {
	exts, _ := ctx.Config["extensions"].([]any)
	kept := make([]any, 0, len(exts))
	for _, e := range exts {
		s, _ := e.(string)
		if !contains(s, "permission-gate") {
			kept = append(kept, e)
		}
	}
	ctx.Config["extensions"] = kept
	ctx.Stage.Exclude("extensions/permission-gate.ts")
	return nil
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestApply_DropsListElement is the §6.5 pi permission-gate shape: a transform
// drops a list element from ctx.config and records a stage exclude. Apply must
// return the mutated config.
func TestApply_DropsListElement(t *testing.T) {
	vm := fakeVM{hooks: map[string]func(*Ctx) error{piGateScript: dropGateHook}}
	ctx := NewCtx("pi", "settings", map[string]any{
		"theme":        "dark",
		"defaultModel": "claude-fable-5",
		"extensions": []any{
			"extensions/permission-gate.ts",
			"extensions/git-helper.ts",
		},
	}, map[string]any{"defaultProjectTrust": "always"})

	got, err := Apply(Transform{VM: vm, Script: piGateScript}, ctx)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	wantExts := []any{"extensions/git-helper.ts"}
	if !reflect.DeepEqual(got["extensions"], wantExts) {
		t.Errorf("extensions = %#v, want %#v", got["extensions"], wantExts)
	}
	// Untouched keys survive the transform unchanged.
	if got["theme"] != "dark" || got["defaultModel"] != "claude-fable-5" {
		t.Errorf("unexpected mutation of untouched keys: %#v", got)
	}
	// The stage exclude was recorded (the §6.5 "don't stage the file" half).
	if excl := ctx.Stage.Excluded(); len(excl) != 1 || excl[0] != "extensions/permission-gate.ts" {
		t.Errorf("Stage.Excluded() = %v, want [extensions/permission-gate.ts]", excl)
	}

	// Enforce (the §3.1 step AFTER the hook) re-applies the managed key; the
	// transform could not have dropped it from the generated file.
	ctx.Enforce()
	if got["defaultProjectTrust"] != "always" {
		t.Errorf("managed key not enforced: defaultProjectTrust = %v, want always", got["defaultProjectTrust"])
	}
}

// TestApply_ErrorSurfaces: a transform that errors must surface a Go error
// (§3.4 loud failure / fail-closed) and return a nil config, not a partial one.
func TestApply_ErrorSurfaces(t *testing.T) {
	sentinel := errors.New("attempt to index a nil value (field 'extensions')")
	vm := fakeVM{hooks: map[string]func(*Ctx) error{
		"boom": func(*Ctx) error { return sentinel },
	}}
	ctx := NewCtx("pi", "settings", map[string]any{"theme": "dark"}, nil)

	got, err := Apply(Transform{VM: vm, Script: "boom"}, ctx)
	if err == nil {
		t.Fatal("Apply returned nil error, want a surfaced Lua error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error does not wrap the underlying Lua error: %v", err)
	}
	if got != nil {
		t.Errorf("Apply returned non-nil config on error: %#v (must be nil, fail-closed)", got)
	}
}

// TestManaged_ReadOnly: mutating ctx.managed must NOT affect the enforced layer.
// The transform can scribble on the Managed view, but Enforce re-applies the
// ORIGINAL enforced keys, so yolo has the last write (§3.1).
func TestManaged_ReadOnly(t *testing.T) {
	enforced := map[string]any{"defaultProjectTrust": "always"}
	vm := fakeVM{hooks: map[string]func(*Ctx) error{
		"tamper": func(ctx *Ctx) error {
			// Try to subvert the managed layer from inside the transform.
			ctx.Managed["defaultProjectTrust"] = "never"
			delete(ctx.Managed, "defaultProjectTrust")
			ctx.Config["defaultProjectTrust"] = "never"
			return nil
		},
	}}
	ctx := NewCtx("pi", "settings", map[string]any{}, enforced)

	if _, err := Apply(Transform{VM: vm, Script: "tamper"}, ctx); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	ctx.Enforce()

	if ctx.Config["defaultProjectTrust"] != "always" {
		t.Errorf("enforced key was overridden by transform: got %v, want always", ctx.Config["defaultProjectTrust"])
	}
	// The caller's original enforced map must be untouched by the transform's
	// scribbling on the Managed view (deep-copy isolation).
	if enforced["defaultProjectTrust"] != "always" {
		t.Errorf("original enforced layer mutated: got %v, want always", enforced["defaultProjectTrust"])
	}
}

// TestManaged_NestedReadOnly: the read-only guarantee holds at depth — mutating
// a nested table inside ctx.managed must not reach the enforced layer.
func TestManaged_NestedReadOnly(t *testing.T) {
	enforced := map[string]any{
		"limits": map[string]any{"cpu": int64(4)},
	}
	vm := fakeVM{hooks: map[string]func(*Ctx) error{
		"nested": func(ctx *Ctx) error {
			inner, _ := ctx.Managed["limits"].(map[string]any)
			inner["cpu"] = int64(999) // must not leak into enforced
			return nil
		},
	}}
	ctx := NewCtx("pi", "settings", map[string]any{}, enforced)
	if _, err := Apply(Transform{VM: vm, Script: "nested"}, ctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ctx.Enforce()

	inner, _ := ctx.Config["limits"].(map[string]any)
	if inner == nil || inner["cpu"] != int64(4) {
		t.Errorf("nested enforced value corrupted: got %v, want cpu=4", ctx.Config["limits"])
	}
	orig := enforced["limits"].(map[string]any)
	if orig["cpu"] != int64(4) {
		t.Errorf("original nested enforced layer mutated: got %v, want cpu=4", orig["cpu"])
	}
}

// TestApply_IdentityWhenNoScript: no config.lua registered → pass-through
// (§3.4 "Neither present → identity"). Apply returns the config unchanged and
// needs no VM.
func TestApply_IdentityWhenNoScript(t *testing.T) {
	cfg := map[string]any{"theme": "dark"}
	ctx := NewCtx("pi", "settings", cfg, nil)
	got, err := Apply(Transform{Script: ""}, ctx)
	if err != nil {
		t.Fatalf("identity Apply errored: %v", err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("identity transform changed config: %#v", got)
	}
}

// TestApply_ScriptWithoutVM: a non-empty script with no VM is a loud error, not
// a silent pass-through — the pipeline must not skip a declared transform.
func TestApply_ScriptWithoutVM(t *testing.T) {
	ctx := NewCtx("pi", "settings", map[string]any{}, nil)
	if _, err := Apply(Transform{Script: "x"}, ctx); err == nil {
		t.Fatal("Apply with script but no VM returned nil error, want loud failure")
	}
}

// TestValidateSandbox_ForbiddenGlobals: the static lint rejects scripts that
// reach for os/io/require/etc. (§3.4), and accepts a clean transform.
func TestValidateSandbox_ForbiddenGlobals(t *testing.T) {
	bad := []struct {
		name, script string
	}{
		{"os", `local x = os.getenv("PATH")`},
		{"io", `io.open("/etc/passwd")`},
		{"require", `local m = require("socket")`},
		{"load", `load("return 1")()`},
		{"dofile", `dofile("/tmp/x.lua")`},
	}
	for _, tc := range bad {
		if err := ValidateSandbox(tc.script); err == nil {
			t.Errorf("ValidateSandbox(%q) = nil, want rejection of %s", tc.script, tc.name)
		}
	}

	clean := `
yolo.transform("pi", function(ctx)
  local kept = {}
  for _, ext in ipairs(ctx.config.extensions) do
    if not ext:find("permission%-gate") then kept[#kept + 1] = ext end
  end
  ctx.config.extensions = kept
  ctx.stage.exclude("extensions/permission-gate.ts")
end)`
	if err := ValidateSandbox(clean); err != nil {
		t.Errorf("ValidateSandbox rejected a clean transform: %v", err)
	}
}

// TestValidateSandbox_SubstringNotFlagged: a forbidden name embedded in a longer
// identifier (e.g. "position" contains "os") must not trip the lint.
func TestValidateSandbox_SubstringNotFlagged(t *testing.T) {
	// "position", "iota", "requires_review" contain os/io/require as substrings.
	script := `local position = 1; local iota = 2; local requires_review = true`
	if err := ValidateSandbox(script); err != nil {
		t.Errorf("ValidateSandbox flagged an identifier substring: %v", err)
	}
}
