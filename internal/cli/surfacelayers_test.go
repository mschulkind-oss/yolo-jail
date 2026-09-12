package cli

// surfacelayers_test.go pins `config ls`'s LAYER COLUMNS to the surfaces' own producers.
//
// THESE ARE THE TESTS THE HAND-MAINTAINED MAPS NEVER HAD, and their absence is the whole
// reason the maps drifted. `surfaceHasHostLayer` and `surfaceHasComputedLayer` were
// `map[string]bool`s of "agent/name" beside builtinLayers, and nothing in the suite
// enumerated the surfaces to notice that three of them were missing from the second one
// (docs/design/host-render-target.md §3.4). The shape that fixes that is not "the
// derivation returns the right answer for the two surfaces I thought of" — it is an
// enumeration over the manifest, so a surface added tomorrow is covered without anyone
// registering it here.

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestHostColumnIsDerivedForEverySurface enumerates every surface and requires the `host`
// column to agree with the surface's own ReadsHost — the declaration the boot render reads
// (entrypoint.hostSurfaceBytes, via Surface.HasHostLayer).
//
// THE ORACLE IS THE DECLARATION, NOT THE DERIVED PATH, since OQ-CO10 (2026-09-12). It was
// `HostSource != ""` — the /ctx path a loader computed — which made "this surface has a
// host layer" unanswerable by anything that had not run packload, and made a derivation
// that came up empty indistinguishable from a surface that never asked for one.
//
// The SYNTHETIC surface at the end is the load-bearing half, and it is there because the
// enumeration alone cannot fail against a CORRECT lookup table. A map keyed on
// "agent/name" cannot hold an identity nobody has written into it, so a column that
// consults one reports no host layer for this surface however plainly the surface declares
// it. That is exactly the failure the retired map produced for a newly added surface, and
// it is the reason this test cannot be satisfied by re-enumerating.
func TestHostColumnIsDerivedForEverySurface(t *testing.T) {
	check := func(s manifest.Surface, why string) {
		got := slices.Contains(builtinLayers(s), "host")
		if want := s.ReadsHost; got != want {
			t.Errorf("%s/%s (%s): `host` column = %v, want %v — the column must be the "+
				"surface's own `readsHost` declaration, not a table keyed on its identity",
				s.Agent, s.Name, why, got, want)
		}
	}
	for _, s := range surfaceManifest().Surfaces() {
		check(s, "shipped")
	}
	// NO HostSource on purpose, beside the identity no table can name: the column must
	// answer from the DECLARATION, which is readable from the manifest alone, and not from
	// the /ctx path a loader derives — a surface described host-side, where no /ctx exists,
	// has a host layer exactly the same way.
	check(manifest.Surface{
		Agent:     "zz-future",
		Name:      "settings",
		Path:      "~/.zz-future/settings.json",
		Codec:     "json",
		ReadsHost: true,
	}, "synthetic: an identity no lookup table can name")
}

// TestComputedColumnAgreesWithEveryPackDerive is the enumeration that found the defect.
//
// The ORACLE is luahook.Derive — the function the BOOT PATH invokes for a surface's
// dynamic layer (entrypoint.deriveComputedLayer) — whose documented contract is that it
// returns (nil, nil) exactly when the script registers no producer for this surface. The
// column under test comes from luahook.DeriveRegistrations instead, which LISTS the
// registrations without invoking one. The two share their setup (deriveSession) and
// nothing else, so this is a comparison of two readers rather than a restatement of one.
//
// It asserts the COLUMN, not the predicate, so it fails if builtinLayers stops asking.
// And it covers a new surface for free: add one with a derive and the oracle says yes on
// the next run, whether or not anyone remembered this file. Run against the shipped corpus
// on the day the maps were retired, it reported claude/config, pi/settings and pi/models —
// three surfaces `config ls` had been describing as having no computed layer, pi/models
// being described as composing from no layers at all.
func TestComputedColumnAgreesWithEveryPackDerive(t *testing.T) {
	// Which pack's derive.lua serves each surface identity.
	scripts := map[manifest.SurfaceKey]string{}
	for _, p := range packload.Embedded() {
		surfaces, probs := p.Surfaces()
		if len(probs) > 0 {
			t.Fatalf("embedded pack %s: %v", p.Name, probs)
		}
		script := packload.DeriveScript(p)
		for _, s := range surfaces {
			scripts[s.Key()] = script
		}
	}

	core := map[manifest.SurfaceKey]bool{}
	for _, k := range agentcfg.CoreComputedSurfaces() {
		core[k] = true
	}

	for _, s := range surfaceManifest().Surfaces() {
		got := slices.Contains(builtinLayers(s), "computed")
		script, isPackSurface := scripts[s.Key()]
		if !isPackSurface {
			// A CORE surface has no pack and so no derive.lua to read: its dynamic layer
			// is Go (mise/config's [tools] table, entrypoint.ConfigureMisePrism). The
			// declaration is agentcfg.CoreComputedSurfaces and this is the only place the
			// answer is stated rather than derived — named here so a core surface that
			// grows or loses one has to come through this test.
			if want := core[s.Key()]; got != want {
				t.Errorf("core surface %s: `computed` column = %v, want %v (per "+
					"agentcfg.CoreComputedSurfaces)", s.Key(), got, want)
			}
			continue
		}
		out, err := (luahook.GopherLuaVM{}).Derive(script, &luahook.DeriveCtx{
			Agent:      s.Agent,
			Surface:    s.Name,
			UnknownAPI: func(string) {}, // tolerant, as the boot path is
		})
		// (nil, nil) is luahook's "no producer registered" — the identity, no computed
		// layer. Anything else, an error included, means a producer ran.
		want := !(out == nil && err == nil)
		if got != want {
			t.Errorf("surface %s: `computed` column = %v, but its pack's derive.lua %s a "+
				"producer for it (luahook.Derive returned %v / %v). The column must be "+
				"derived from the registrations, not enumerated beside the printer",
				s.Key(), got, map[bool]string{true: "registers", false: "does not register"}[want],
				out, err)
		}
	}
}

// TestConfigRenderExplainNamesTheComputedLayerItOmits is the call-site pin for the OTHER
// reader of the computed column: `config render --explain` composes without the dynamic
// layer and says so, and the surfaces it must say it for are the ones that have one.
//
// pi is the case that was wrong. Neither pi surface was in the retired map, so the note
// was omitted for both — including pi/models, whose ENTIRE content is the dynamic layer,
// so `--explain` printed an empty provenance list under a header promising "what the jail
// gets" (§6). Silently omitting the computed layer is the exact failure A7 fixed for the
// `host` layer, reproduced one column over.
func TestConfigRenderExplainNamesTheComputedLayerItOmits(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := configRender([]string{"pi", "--explain"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc != 0: %s", errw.String())
	}
	got := out.String()
	// One note per rendered pi surface: both register a derive (packs/pi/derive.lua).
	if n := strings.Count(got, "also has a `computed` layer"); n != 2 {
		t.Errorf("counted %d computed-layer notes, want 2 (pi/settings and pi/models both "+
			"register a derive):\n%s", n, got)
	}
}
