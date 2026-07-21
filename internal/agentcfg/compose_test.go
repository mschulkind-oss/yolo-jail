package agentcfg

import (
	"reflect"
	"testing"

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

// TestProvenanceLines are sorted and tab-separated for --explain.
func TestProvenanceLines(t *testing.T) {
	r := &Result{Provenance: map[string]string{"b": "host", "a": "managed"}}
	got := r.ProvenanceLines()
	want := []string{"a\tmanaged", "b\thost"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProvenanceLines = %v, want %v", got, want)
	}
}
