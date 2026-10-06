package entrypoint

// retiredassert_test.go builds a home AS THE RETIRED `assert` LEFT IT, for the tests whose
// subject is what happens to such a home after the retirement (config-ownership-and-promotion.md
// §4.5, OQ-CO14 face 2): switching it to `own`, reading it into a jail, reverting it.
//
// `host_management: "assert"` is gone, and with it render.OwnershipAssert and its census, so no
// production entry renders under it any more. What it DID is still exactly expressible, because
// its census was one rule: every composing surface, whatever its pack declared, went through the
// rmw arm, and that arm recorded. The rmw arm is still here — an owned host runs it for a surface
// its pack declares `rmw` (render.HostOwnedModes), recording into the same provenance record — and
// entrypoint branches on the contract nowhere but through the census. So a pack whose every
// writing surface is re-declared `rmw`, rendered under OwnershipOwn, runs the same writer over
// the same layers and leaves the same file and the same record an `assert` apply left. The one
// difference is bookkeeping beside them, not in them: the rmw arm's selection record lands in the
// owned capture store, where `assert` kept it beside the provenance record.
//
// hostassertbaseline_test.go pins the premise: the bytes this leaves on its fixture home are the
// `assert` baseline, written out in full (assertBaselineBytes).

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// asRetiredAssert returns a copy of p whose every config surface declares `rmw`, except one
// declaring `unrendered`, which `assert` honored by writing nothing. p is not modified.
func asRetiredAssert(t *testing.T, p *packload.Pack) *packload.Pack {
	t.Helper()
	if p.Decl == nil {
		return p
	}
	decl := *p.Decl
	decl.Contributes = make([]packdecl.Contribution, len(p.Decl.Contributes))
	for i, c := range p.Decl.Contributes {
		if c.Kind == packdecl.KindConfig && len(c.Raw) > 0 {
			c.Raw = rmwDeclared(t, c.Raw)
		}
		decl.Contributes[i] = c
	}
	cp := *p
	cp.Decl = &decl
	return &cp
}

// rmwDeclared rewrites one `config` contribution body — a surface object or an array of them —
// so each surface declares `rmw`.
func rmwDeclared(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	force := func(s map[string]any) {
		if mode, _ := s["mode"].(string); mode != manifest.ModeUnrendered {
			s["mode"] = manifest.ModeRMW
		}
	}
	var many []map[string]any
	if err := json.Unmarshal(raw, &many); err == nil {
		for _, s := range many {
			force(s)
		}
		out, err := json.Marshal(many)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	var one map[string]any
	if err := json.Unmarshal(raw, &one); err != nil {
		t.Fatalf("a config contribution is neither a surface nor a list of them: %v\n%s", err, raw)
	}
	force(one)
	out, err := json.Marshal(one)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// renderAsRetiredAssert is one writing `yolo host apply --assert` of p into home as it ran under
// `host_management: "assert"`, before the retirement: asRetiredAssert's copy rendered through
// the host entry under OwnershipOwn, with the overlays collected at the host's own posture when
// the caller passes none.
func renderAsRetiredAssert(t *testing.T, p *packload.Pack, home string, overlays *packoverlay.OverlaySet,
	inputs *HostInputs) []HostRenderResult {
	t.Helper()
	results, err := RenderHostPack(asRetiredAssert(t, p), home, render.OwnershipOwn, false, overlays, inputs)
	if err != nil {
		t.Fatalf("the retired assert's apply of %s: %v", p.Name, err)
	}
	return results
}
