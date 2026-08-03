package entrypoint

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

func probeSurface(name string, defaults, managed map[string]any) manifest.Surface {
	s := manifest.Surface{
		Agent: "parity", Name: name, Codec: "json",
		Path: "~/.parity/" + name + ".json",
	}
	if defaults != nil {
		s.Defaults = defaults
	}
	if managed != nil {
		s.Managed = managed
	}
	return s
}

func probeRun(t *testing.T, s manifest.Surface, hostLayer map[string]any,
	computed map[string]any, overlays []agentcfg.Overlay) (jail, host map[string]string) {
	t.Helper()

	var hostBytes []byte
	if hostLayer != nil {
		hostBytes, _ = json.Marshal(hostLayer)
	}

	// JAIL: stateful → Compose fold.
	var jerr bytes.Buffer
	ej := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &jerr}
	out, err := renderSurfaceStatefulSurface(ej, s, hostBytes, computed, overlays)
	if err != nil {
		t.Fatalf("jail render: %v", err)
	}
	jail = out.Result.Provenance

	// HOST: rmw → rmwProvenance, host layer seeded as the pre-existing file.
	eh := &Env{Home: t.TempDir(), Vars: map[string]string{}, hostTarget: true}
	p := filepath.Join(eh.Home, ".parity", s.Name+".json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if hostBytes != nil {
		if err := os.WriteFile(p, hostBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := renderSurfaceRMWSurface(eh, s, computed, overlays); err != nil {
		t.Fatalf("host render: %v", err)
	}
	rec, found := hostProvenance(t, eh.Home, "parity", s.Name)
	if !found {
		t.Fatal("no host record")
	}
	jf, _ := os.ReadFile(filepath.Join(ej.Home, ".parity", s.Name+".json"))
	hf, _ := os.ReadFile(p)
	t.Logf("  jailfile=%s", bytes.ReplaceAll(bytes.TrimSpace(jf), []byte("\n"), []byte(" ")))
	t.Logf("  hostfile=%s", bytes.ReplaceAll(bytes.TrimSpace(hf), []byte("\n"), []byte(" ")))
	return jail, rec
}

func TestZZProbeParity(t *testing.T) {
	cases := []struct {
		name     string
		surface  manifest.Surface
		host     map[string]any
		computed map[string]any
		overlays []agentcfg.Overlay
	}{
		{name: "managed-only",
			surface: probeSurface("a", nil, map[string]any{"m": 1})},
		{name: "overlay-untouched-by-managed",
			surface:  probeSurface("b", nil, map[string]any{"m": 1}),
			overlays: []agentcfg.Overlay{{Pack: "ov", Data: map[string]any{"o": 2}}}},
		{name: "overlay-contested-by-managed",
			surface:  probeSurface("c", nil, map[string]any{"k": 1}),
			overlays: []agentcfg.Overlay{{Pack: "ov", Data: map[string]any{"k": 2}}}},
		{name: "nested-siblings",
			surface:  probeSurface("d", nil, map[string]any{"prefs": map[string]any{"owned": true}}),
			overlays: []agentcfg.Overlay{{Pack: "ov", Data: map[string]any{"prefs": map[string]any{"sib": true}}}}},
		{name: "host-key-in-no-layer",
			surface: probeSurface("e", nil, map[string]any{"m": 1}),
			host:    map[string]any{"mine": "keep"}},
		{name: "empty-layers",
			surface: probeSurface("f", nil, nil)},
		{name: "defaults-corrected-by-host",
			surface: probeSurface("g", map[string]any{"d": "fallback"}, nil),
			host:    map[string]any{"d": "user"}},
		{name: "defaults-absent-from-host",
			surface: probeSurface("h", map[string]any{"d": "fallback"}, nil)},
		{name: "managed-null-tombstone",
			surface: probeSurface("i", nil, map[string]any{"gone": nil}),
			host:    map[string]any{"gone": "was-here"}},
		{name: "computed-object",
			surface:  probeSurface("j", nil, nil),
			computed: map[string]any{"tbl": map[string]any{"srv": map[string]any{"cmd": "x"}}}},
		{name: "computed-scalar",
			surface:  probeSurface("k", nil, nil),
			computed: map[string]any{"flag": true}},
		{name: "computed-beaten-by-managed",
			surface:  probeSurface("l", nil, map[string]any{"tbl": map[string]any{"z": 1}}),
			computed: map[string]any{"tbl": map[string]any{"srv": map[string]any{"cmd": "x"}}}},
		{name: "two-overlays-later-wins",
			surface: probeSurface("m", nil, nil),
			overlays: []agentcfg.Overlay{
				{Pack: "first", Data: map[string]any{"k": 1}},
				{Pack: "second", Data: map[string]any{"k": 2}}}},
		{name: "defaults-beaten-by-overlay",
			surface:  probeSurface("n", map[string]any{"d": "fallback"}, nil),
			overlays: []agentcfg.Overlay{{Pack: "ov", Data: map[string]any{"d": "ov"}}}},
		{name: "defaults-beaten-by-managed",
			surface: probeSurface("o", map[string]any{"k": "d"}, map[string]any{"k": "m"})},
		{name: "host-beaten-by-overlay",
			surface:  probeSurface("p", nil, nil),
			host:     map[string]any{"k": "user"},
			overlays: []agentcfg.Overlay{{Pack: "ov", Data: map[string]any{"k": "ov"}}}},
		{name: "host-beaten-by-computed-object",
			surface:  probeSurface("q", nil, nil),
			host:     map[string]any{"tbl": map[string]any{"stale": map[string]any{"cmd": "old"}}},
			computed: map[string]any{"tbl": map[string]any{"srv": map[string]any{"cmd": "x"}}}},
		{name: "nested-sibling-under-host-parent",
			surface: probeSurface("r", nil, map[string]any{"prefs": map[string]any{"owned": true}}),
			host:    map[string]any{"prefs": map[string]any{"mine": 1}, "other": 2}},
		{name: "overlay-null-tombstone",
			surface:  probeSurface("s", nil, nil),
			host:     map[string]any{"k": "user"},
			overlays: []agentcfg.Overlay{{Pack: "ov", Data: map[string]any{"k": nil}}}},
		{name: "nested-defaults-under-host-parent",
			surface: probeSurface("t", map[string]any{"prefs": map[string]any{"d": 1}}, nil),
			host:    map[string]any{"prefs": map[string]any{"mine": 2}}},
		{name: "nested-overlay-sibling-no-managed",
			surface:  probeSurface("u", nil, nil),
			host:     map[string]any{"prefs": map[string]any{"mine": 2}},
			overlays: []agentcfg.Overlay{{Pack: "ov", Data: map[string]any{"prefs": map[string]any{"sib": 3}}}}},
		{name: "managed-nested-tombstone",
			surface: probeSurface("v", nil, map[string]any{"prefs": map[string]any{"gone": nil}}),
			host:    map[string]any{"prefs": map[string]any{"gone": 1, "keep": 2}}},
	}
	for _, tc := range cases {
		jail, host := probeRun(t, tc.surface, tc.host, tc.computed, tc.overlays)
		t.Logf("%-32s jail=%v host=%v  AGREE=%v", tc.name, jail, host, equalProv(jail, host))
	}
}

func equalProv(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
