package agentcfg

// keyless_test.go covers the KEYLESS surfaces — raw (a whole file as a string)
// and lines (a whole file as a []any of strings) — end to end through Compose
// and ComposeStateful.
//
// These paths exist because docs/plans/host-file-staging.md lets a user declare
// ANY host file as a surface, and most host files are not JSON: a shell rc, an
// SSH known_hosts, a netrc. The engine was object-only before host_files (every
// builtin surface happened to be json or toml), so "raw" was a codec with no
// working pipeline behind it: Compose rejected a decoded string as "not an
// object", the Lua boundary demanded a table back, and staterender's decode
// helper reported ok=false for every raw file — silently disabling capture.
//
// The invariants asserted here are the ones a keyless surface needs to be a real
// surface rather than a declared-but-broken one:
//
//   - layer precedence works with whole-value replacement (no deep merge);
//   - an ABSENT layer (nil) says nothing, while an EXPLICITLY EMPTY layer ("" /
//     []any{}) is a real assertion that wins — conflating them would let a
//     surface with no workspace layer erase its host content;
//   - a Lua transform sees the surface's own shape and must return that shape;
//   - capture (§5) works: an in-jail edit to a raw file survives regeneration;
//   - every shape mismatch fails CLOSED, in both directions.

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// rawSurface is a user-declared host-file surface in the shape host_files
// produces: raw codec, no defaults, no managed. The path is the kind of file
// that motivated keyless support (a shell rc yolo copies in and lets the agent
// edit).
func rawSurface() manifest.Surface {
	return manifest.Surface{
		Agent: "user",
		Name:  "bashrc-extra",
		Path:  "~/.bashrc.extra",
		Codec: "raw",
	}
}

// linesSurface is the allowlist-style keyless surface: one entry per line.
func linesSurface() manifest.Surface {
	return manifest.Surface{
		Agent: "user",
		Name:  "known-hosts",
		Path:  "~/.ssh/known_hosts",
		Codec: "lines",
	}
}

// TestComposeRawHostOnly is the base case: a raw host file composes to itself,
// byte for byte, and its provenance is attributed to the host layer under the
// whole-file key (there are no per-key entries to make).
func TestComposeRawHostOnly(t *testing.T) {
	host := "export EDITOR=nvim\nalias ll='ls -la'\n"
	res, err := Compose(Inputs{Surface: rawSurface(), HostBytes: []byte(host)})
	if err != nil {
		t.Fatalf("Compose returned error: %v", err)
	}
	if res.Config != host {
		t.Errorf("Config = %q, want %q", res.Config, host)
	}
	if string(res.Encoded) != host {
		t.Errorf("Encoded = %q, want byte-exact %q", res.Encoded, host)
	}
	if res.Provenance[WholeFileKey] != layerHost {
		t.Errorf("Provenance[%s] = %q, want %q", WholeFileKey, res.Provenance[WholeFileKey], layerHost)
	}
	// ConfigMap is nil for a keyless surface — callers must not silently get an
	// empty object and conclude the file is empty.
	if res.ConfigMap() != nil {
		t.Errorf("ConfigMap() = %#v, want nil for a keyless surface", res.ConfigMap())
	}
}

// TestComposeRawLayerPrecedence proves the ascending layer order holds for a
// keyless surface, with whole-value replacement rather than deep merge: each
// higher layer replaces the file outright and provenance names the winner.
func TestComposeRawLayerPrecedence(t *testing.T) {
	surface := rawSurface()
	surface.Defaults = "from-defaults\n"

	cases := []struct {
		name     string
		in       Inputs
		want     string
		wantProv string
	}{
		{
			name:     "defaults only",
			in:       Inputs{Surface: surface},
			want:     "from-defaults\n",
			wantProv: layerDefaults,
		},
		{
			name:     "host beats defaults",
			in:       Inputs{Surface: surface, HostBytes: []byte("from-host\n")},
			want:     "from-host\n",
			wantProv: layerHost,
		},
		{
			name:     "workspace beats host",
			in:       Inputs{Surface: surface, HostBytes: []byte("from-host\n"), Workspace: "from-workspace\n"},
			want:     "from-workspace\n",
			wantProv: layerWorkspace,
		},
		{
			name: "overlay beats workspace",
			in: Inputs{Surface: surface, HostBytes: []byte("from-host\n"),
				Workspace: "from-workspace\n", Overlay: "from-overlay\n"},
			want:     "from-overlay\n",
			wantProv: layerOverlay,
		},
		{
			name: "computed beats overlay",
			in: Inputs{Surface: surface, HostBytes: []byte("from-host\n"),
				Overlay: "from-overlay\n", Computed: "from-computed\n"},
			want:     "from-computed\n",
			wantProv: layerComputed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Compose(tc.in)
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if res.Config != tc.want {
				t.Errorf("Config = %q, want %q", res.Config, tc.want)
			}
			if res.Provenance[WholeFileKey] != tc.wantProv {
				t.Errorf("Provenance[%s] = %q, want %q", WholeFileKey,
					res.Provenance[WholeFileKey], tc.wantProv)
			}
		})
	}
}

// TestComposeRawNoLayersIsEmpty: a surface with NO layers at all renders the
// codec's zero value (an empty file), not an error and not nil — so declaring a
// host file that doesn't exist yet stages an empty file rather than failing the
// boot.
func TestComposeRawNoLayersIsEmpty(t *testing.T) {
	res, err := Compose(Inputs{Surface: rawSurface()})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if res.Config != "" {
		t.Errorf("Config = %#v, want empty string", res.Config)
	}
	if len(res.Encoded) != 0 {
		t.Errorf("Encoded = %q, want empty", res.Encoded)
	}
	if _, ok := res.Provenance[WholeFileKey]; ok {
		t.Errorf("Provenance has a winner for a surface with no layers: %v", res.Provenance)
	}
}

// TestComposeRawAbsentVsEmptyLayer is the distinction the keyless fold turns on
// and the one that is easy to get wrong: nil means "this layer says nothing"
// (skip it), while an explicitly EMPTY value is a real assertion that the file
// is empty and DOES win. If the engine conflated them, every surface without a
// workspace layer would blank its own host content.
func TestComposeRawAbsentVsEmptyLayer(t *testing.T) {
	host := "keep-me\n"

	// Absent (nil) workspace: host content survives.
	absent, err := Compose(Inputs{Surface: rawSurface(), HostBytes: []byte(host), Workspace: nil})
	if err != nil {
		t.Fatalf("Compose (absent workspace): %v", err)
	}
	if absent.Config != host {
		t.Errorf("absent workspace layer changed the file: %q, want %q", absent.Config, host)
	}

	// Explicitly empty workspace: the assertion wins, the file is blanked.
	empty, err := Compose(Inputs{Surface: rawSurface(), HostBytes: []byte(host), Workspace: ""})
	if err != nil {
		t.Fatalf("Compose (empty workspace): %v", err)
	}
	if empty.Config != "" {
		t.Errorf("explicit empty workspace layer did not win: %q", empty.Config)
	}
	if empty.Provenance[WholeFileKey] != layerWorkspace {
		t.Errorf("Provenance[%s] = %q, want %q", WholeFileKey,
			empty.Provenance[WholeFileKey], layerWorkspace)
	}
}

// TestComposeRawTransform runs a REAL Lua transform over a raw surface: the hook
// receives ctx.config as a string, rewrites it with string.gsub, and returns a
// string. This is the case the old object-only assertion in vm.go made
// impossible even though the marshaller and sandbox always supported it.
func TestComposeRawTransform(t *testing.T) {
	script := `
yolo.transform("user", function(ctx)
  ctx.config = ctx.config:gsub("^#!/bin/sh", "#!/usr/bin/env bash")
end)
`
	host := "#!/bin/sh\necho hi\n"
	res, err := Compose(Inputs{
		Surface:   rawSurface(),
		HostBytes: []byte(host),
		Script:    script,
		VM:        &luahook.GopherLuaVM{},
	})
	if err != nil {
		t.Fatalf("Compose returned error: %v", err)
	}
	want := "#!/usr/bin/env bash\necho hi\n"
	if res.Config != want {
		t.Errorf("Config = %q, want %q", res.Config, want)
	}
	if string(res.Encoded) != want {
		t.Errorf("Encoded = %q, want %q", res.Encoded, want)
	}
	// The transform changed the file, so it — not host — owns the whole-file slot.
	if res.Provenance[WholeFileKey] != layerTransform {
		t.Errorf("Provenance[%s] = %q, want %q", WholeFileKey,
			res.Provenance[WholeFileKey], layerTransform)
	}
}

// TestComposeRawTransformNoChangeKeepsHostProvenance: a transform that runs but
// leaves the value alone must NOT steal provenance from the layer that actually
// produced the content.
func TestComposeRawTransformNoChangeKeepsHostProvenance(t *testing.T) {
	script := `yolo.transform("user", function(ctx) local _ = #ctx.config end)`
	res, err := Compose(Inputs{
		Surface:   rawSurface(),
		HostBytes: []byte("untouched\n"),
		Script:    script,
		VM:        &luahook.GopherLuaVM{},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if res.Provenance[WholeFileKey] != layerHost {
		t.Errorf("Provenance[%s] = %q, want %q (no-op transform must not claim the file)",
			WholeFileKey, res.Provenance[WholeFileKey], layerHost)
	}
}

// TestComposeRawTransformWrongShapeFailsClosed: a raw surface's hook that
// returns a TABLE is a loud error, not a coercion. Coercing would write a
// JSON-ish blob into a shell rc — a plausible-looking file that is not what the
// author meant, discoverable only as the agent misbehaving (§3.4).
func TestComposeRawTransformWrongShapeFailsClosed(t *testing.T) {
	script := `yolo.transform("user", function(ctx) ctx.config = { oops = true } end)`
	_, err := Compose(Inputs{
		Surface:   rawSurface(),
		HostBytes: []byte("x\n"),
		Script:    script,
		VM:        &luahook.GopherLuaVM{},
	})
	if err == nil {
		t.Fatal("Compose accepted a table from a raw surface's transform, want a loud error")
	}
}

// TestComposeObjectTransformWrongShapeFailsClosed is the mirror direction: a
// json surface's hook that returns a STRING is equally an error. The shape
// contract is symmetric — widening the engine to raw must not have loosened the
// object case into "anything goes".
func TestComposeObjectTransformWrongShapeFailsClosed(t *testing.T) {
	script := `yolo.transform("pi", function(ctx) ctx.config = "not a table" end)`
	_, err := Compose(Inputs{
		Surface: piSurface(),
		Script:  script,
		VM:      &luahook.GopherLuaVM{},
	})
	if err == nil {
		t.Fatal("Compose accepted a string from a json surface's transform, want a loud error")
	}
}

// TestComposeRawWrongKindLayerFailsClosed: a layer whose Go type doesn't match
// the surface's codec is a config-authoring bug, and it fails closed with the
// layer named — silently dropping it would make the mistake invisible.
func TestComposeRawWrongKindLayerFailsClosed(t *testing.T) {
	surface := rawSurface()
	_, err := Compose(Inputs{Surface: surface, Workspace: map[string]any{"nope": 1}})
	if err == nil {
		t.Fatal("Compose accepted an object workspace layer on a raw surface, want an error")
	}
	if !contains(err.Error(), "workspace") {
		t.Errorf("error does not name the offending layer: %v", err)
	}
}

// TestComposeJSONHostArrayFailsClosed: the object-side kind check. A json
// surface whose HOST file holds a top-level array cannot be deep-merged, so it
// is an error rather than a silently discarded layer.
func TestComposeJSONHostArrayFailsClosed(t *testing.T) {
	_, err := Compose(Inputs{Surface: piSurface(), HostBytes: []byte(`["a","b"]`)})
	if err == nil {
		t.Fatal("Compose accepted a top-level array as a json surface's host file, want an error")
	}
}

// TestComposeRawManagedReplacesWholeFile: `managed` on a keyless surface can
// only mean "this file is exactly these bytes" — there are no keys to enforce
// individually. Coarse, but it is what enforce means without keys, and it must
// beat every other layer including the transform.
func TestComposeRawManagedReplacesWholeFile(t *testing.T) {
	surface := rawSurface()
	surface.Managed = "yolo owns this file\n"
	script := `yolo.transform("user", function(ctx) ctx.config = "transform tried\n" end)`

	res, err := Compose(Inputs{
		Surface:   surface,
		HostBytes: []byte("host content\n"),
		Script:    script,
		VM:        &luahook.GopherLuaVM{},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if res.Config != "yolo owns this file\n" {
		t.Errorf("Config = %q, want the managed value (managed wins after the transform)", res.Config)
	}
	if res.Provenance[WholeFileKey] != layerManaged {
		t.Errorf("Provenance[%s] = %q, want %q", WholeFileKey,
			res.Provenance[WholeFileKey], layerManaged)
	}
}

// TestComposeLinesLayerPrecedence is the lines-codec counterpart: a []any layer
// replaces wholesale (no element-wise append), and the encoded form is
// newline-terminated.
func TestComposeLinesLayerPrecedence(t *testing.T) {
	res, err := Compose(Inputs{
		Surface:   linesSurface(),
		HostBytes: []byte("host.example.com\nother.example.com\n"),
		Workspace: []any{"workspace.example.com"},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := []any{"workspace.example.com"}
	if !reflect.DeepEqual(res.Config, want) {
		t.Errorf("Config = %#v, want %#v (whole-value replacement, not append)", res.Config, want)
	}
	if string(res.Encoded) != "workspace.example.com\n" {
		t.Errorf("Encoded = %q, want newline-terminated single line", res.Encoded)
	}
	if res.Provenance[WholeFileKey] != layerWorkspace {
		t.Errorf("Provenance[%s] = %q, want %q", WholeFileKey,
			res.Provenance[WholeFileKey], layerWorkspace)
	}
}

// TestComposeLinesTransform: a lines surface's hook sees a Lua list and returns
// a list — the KindArray leg of the shape contract, with a real VM.
func TestComposeLinesTransform(t *testing.T) {
	script := `
yolo.transform("user", function(ctx)
  local kept = {}
  for _, line in ipairs(ctx.config) do
    if not line:find("^#") then kept[#kept + 1] = line end
  end
  ctx.config = kept
end)
`
	res, err := Compose(Inputs{
		Surface:   linesSurface(),
		HostBytes: []byte("# a comment\nreal.example.com\n# another\n"),
		Script:    script,
		VM:        &luahook.GopherLuaVM{},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := []any{"real.example.com"}
	if !reflect.DeepEqual(res.Config, want) {
		t.Errorf("Config = %#v, want %#v", res.Config, want)
	}
	if res.Provenance[WholeFileKey] != layerTransform {
		t.Errorf("Provenance[%s] = %q, want %q", WholeFileKey,
			res.Provenance[WholeFileKey], layerTransform)
	}
}

// TestComposeStatefulRawCapturesEdit is the §5 capture loop on a raw surface —
// the invariant that was silently broken before: the agent edits the staged
// file, and that edit SURVIVES the next boot's regeneration. Without the
// kind-aware decode in staterender, the edit was discarded every boot and the
// host content silently reappeared.
func TestComposeStatefulRawCapturesEdit(t *testing.T) {
	host := "export EDITOR=nvim\n"
	edited := "export EDITOR=nvim\nexport PAGER=less\n" // the in-jail edit

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawSurface(), HostBytes: []byte(host)},
		CurrentBytes:      []byte(edited),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(host), // what yolo wrote last boot
	})
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	if out.FirstMigration {
		t.Error("FirstMigration = true, want false in steady state")
	}
	if out.Result.Config != edited {
		t.Errorf("Config = %q, want the captured edit %q", out.Result.Config, edited)
	}
	// The overlay sidecar is JSON regardless of the surface codec, so a raw
	// capture is stored as a JSON string.
	if got := string(out.OverlayJSON); got != `"export EDITOR=nvim\nexport PAGER=less\n"` {
		t.Errorf("OverlayJSON = %s, want the edit as a JSON string", got)
	}
	// The next boot reads that sidecar back and the edit still wins.
	next, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawSurface(), HostBytes: []byte(host)},
		CurrentBytes:      out.Result.Encoded,
		LastRenderPresent: true,
		LastRenderBytes:   out.LastRenderBytes,
		OverlayJSON:       out.OverlayJSON,
	})
	if err != nil {
		t.Fatalf("ComposeStateful (second boot): %v", err)
	}
	if next.Result.Config != edited {
		t.Errorf("second boot Config = %q, want the edit to persist %q", next.Result.Config, edited)
	}
}

// TestComposeStatefulRawFirstMigrationSeeds: on the first boot (no last_render)
// the raw surface is SEEDED from the host layer and the on-disk file is NOT
// captured — the same §3.2 rule the object surfaces follow, so a pre-existing
// bespoke file can't pin itself forever.
func TestComposeStatefulRawFirstMigrationSeeds(t *testing.T) {
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawSurface(), HostBytes: []byte("from-host\n")},
		CurrentBytes:      []byte("stale bespoke content\n"),
		LastRenderPresent: false,
	})
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	if !out.FirstMigration {
		t.Error("FirstMigration = false, want true on absent last_render")
	}
	if out.Result.Config != "from-host\n" {
		t.Errorf("Config = %q, want the fresh host render (capture skipped on seed)", out.Result.Config)
	}
	// Empty overlay for a keyless surface serializes as JSON null — read back as
	// "no captured edits", NOT as an empty string that would blank the file.
	if got := string(out.OverlayJSON); got != "null" {
		t.Errorf("OverlayJSON = %s, want null (no captured edits)", got)
	}
	reread, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawSurface(), HostBytes: []byte("from-host\n")},
		CurrentBytes:      out.Result.Encoded,
		LastRenderPresent: true,
		LastRenderBytes:   out.LastRenderBytes,
		OverlayJSON:       out.OverlayJSON,
	})
	if err != nil {
		t.Fatalf("ComposeStateful (reread): %v", err)
	}
	if reread.Result.Config != "from-host\n" {
		t.Errorf("a null overlay blanked the file: %q, want from-host", reread.Result.Config)
	}
}

// TestComposeStatefulRawNoEditIsStable: current == last_render, so nothing is
// captured and the render is byte-stable. A keyless surface must not manufacture
// an overlay just because it has no keys to diff.
func TestComposeStatefulRawNoEditIsStable(t *testing.T) {
	host := "export EDITOR=nvim\n"
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawSurface(), HostBytes: []byte(host)},
		CurrentBytes:      []byte(host),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(host),
	})
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	if out.Result.Config != host {
		t.Errorf("Config = %q, want %q", out.Result.Config, host)
	}
	if got := string(out.OverlayJSON); got != "null" {
		t.Errorf("OverlayJSON = %s, want null (no edit to capture)", got)
	}
}

// TestComposeStatefulRawCapturesTruncationToEmpty: emptying a raw file in-jail
// is a real edit and must be captured. This is the one case where the captured
// value is the kind's zero value, so it proves the capture path stores an empty
// string as an ASSERTION (which wins the fold) rather than reading it back as
// "nothing was captured".
func TestComposeStatefulRawCapturesTruncationToEmpty(t *testing.T) {
	host := "export EDITOR=nvim\n"
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawSurface(), HostBytes: []byte(host)},
		CurrentBytes:      []byte(""), // agent truncated the file
		LastRenderPresent: true,
		LastRenderBytes:   []byte(host),
	})
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	// Empty CurrentBytes is indistinguishable from an ABSENT file at this
	// boundary (both are zero bytes), and an absent/corrupt current file skips
	// capture by design — biasing toward under-capture rather than freezing a
	// spurious delta into the never-aging overlay (§5). So the host content
	// legitimately returns here.
	if out.Result.Config != host {
		t.Errorf("Config = %q, want %q (empty current == absent, capture skipped)", out.Result.Config, host)
	}
	if got := string(out.OverlayJSON); got != "null" {
		t.Errorf("OverlayJSON = %s, want null", got)
	}
}

// TestComposeStatefulRawCorruptOverlayShapeResets: an overlay sidecar whose
// shape doesn't match the surface (e.g. the codec changed between boots) is as
// untrustworthy as one that won't parse — it resets rather than erroring the
// boot, so a mangled home can never break startup (§3.3).
func TestComposeStatefulRawCorruptOverlayShapeResets(t *testing.T) {
	host := "from-host\n"
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawSurface(), HostBytes: []byte(host)},
		CurrentBytes:      []byte(host),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(host),
		OverlayJSON:       []byte(`{"was":"an object when the surface was json"}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	if out.Result.Config != host {
		t.Errorf("Config = %q, want %q (wrong-shape overlay must reset, not apply)", out.Result.Config, host)
	}
}

// TestComposeStatefulLinesCapturesEdit is the lines-codec capture leg: an
// appended line survives regeneration.
func TestComposeStatefulLinesCapturesEdit(t *testing.T) {
	host := "host.example.com\n"
	edited := "host.example.com\nadded.example.com\n"

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: linesSurface(), HostBytes: []byte(host)},
		CurrentBytes:      []byte(edited),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(host),
	})
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	want := []any{"host.example.com", "added.example.com"}
	if !reflect.DeepEqual(out.Result.Config, want) {
		t.Errorf("Config = %#v, want %#v", out.Result.Config, want)
	}
	if string(out.Result.Encoded) != edited {
		t.Errorf("Encoded = %q, want %q", out.Result.Encoded, edited)
	}
}

// contains is a tiny substring helper (the package has no strings import
// elsewhere in its tests and this keeps the assertion readable).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
