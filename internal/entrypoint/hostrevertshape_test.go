package entrypoint

// hostrevertshape_test.go pins that `yolo host apply --revert` keeps an EMPTY declared default,
// so a revert never re-creates the file HC-D1 fixed (docs/design/host-computed-layer.md).
//
// pi/models declares `"defaults": {"providers": {}}` because pi 0.87.1 rejects a models.json
// without `providers`. A revert removes every key yolo wrote, `defaults` included, and leaves
// the file rather than deleting it, so on a home where the apply CREATED models.json it left
// `{}` — the file pi reports as `models.json error` at every start. An empty default holds
// nothing to withdraw: it is the shape the pack declares its file needs, so the revert keeps
// it and says so. A non-empty default is still content yolo wrote, and still goes.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// revertKeyNames lists "surface key" for each entry.
func revertKeyNames(keys []HostRevertedKey) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		out[k.Surface+" "+k.Key] = k.Action
	}
	return out
}

// THE MEASURED CASE: a fresh home, the shipped pi pack applied and then reverted. models.json
// keeps an object `providers`, and the dry run and the revert both name it as kept. Run over a
// home each writing contract left: `own`'s, and the retired `assert`'s (renderAsRetiredAssert),
// since a home `assert` wrote into is what `--revert` under `none` is for (OQ-CO14) and is where
// the case was measured.
func TestARevertKeepsPiModelsProviders(t *testing.T) {
	for _, apply := range []struct {
		name   string
		render func(t *testing.T, pi *packload.Pack, home string)
	}{
		{"own", func(t *testing.T, pi *packload.Pack, home string) {
			if _, err := RenderHostPack(pi, home, render.OwnershipOwn, false, nil, nil); err != nil {
				t.Fatalf("apply: %v", err)
			}
		}},
		{"retired assert", func(t *testing.T, pi *packload.Pack, home string) {
			renderAsRetiredAssert(t, pi, home, nil, nil)
		}},
	} {
		t.Run(apply.name, func(t *testing.T) {
			testARevertKeepsPiModelsProviders(t, apply.render)
		})
	}
}

func testARevertKeepsPiModelsProviders(t *testing.T, apply func(t *testing.T, pi *packload.Pack, home string)) {
	home := t.TempDir()
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatal(err)
	}
	apply(t, pi, home)
	requireObjectProviders(t, piModelsPath(home), "after the apply")

	for _, observe := range []bool{true, false} {
		rev, err := RevertHostRender([]*packload.Pack{pi}, home, observe)
		if err != nil {
			t.Fatalf("revert (observe=%v): %v", observe, err)
		}
		if _, listed := revertKeyNames(rev.Keys)["pi/models providers"]; listed {
			t.Errorf("observe=%v: the revert lists pi/models' `providers` among the keys it "+
				"removes: %+v", observe, rev.Keys)
		}
		kept := revertKeyNames(rev.Kept)
		if _, named := kept["pi/models providers"]; !named {
			t.Errorf("observe=%v: the revert keeps `providers` without saying so: kept %+v",
				observe, rev.Kept)
		}
		// The rest of the revert is unchanged: a non-empty default yolo filled still goes.
		if _, listed := revertKeyNames(rev.Keys)["pi/settings theme"]; !listed {
			t.Errorf("observe=%v: pi/settings' `theme` default is no longer reverted: %+v",
				observe, rev.Keys)
		}
	}
	requireObjectProviders(t, piModelsPath(home), "after the revert")
	if doc := decodeJSONFile(t, filepath.Join(home, ".pi", "agent", "settings.json")); len(doc) != 0 {
		t.Errorf("pi/settings kept keys yolo wrote after the revert: %v", doc)
	}
}

// shapeDefaultPack owns one rmw surface declaring an empty object, an empty array and a scalar
// as defaults — the three cases the rule is stated over.
func shapeDefaultPack(t *testing.T) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "shape", "name": "cfg", "codec": "json", "mode": "rmw", "path": "~/.shape/cfg.json",
		"defaults": map[string]any{"table": map[string]any{}, "list": []any{}, "fillMe": "byYolo"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "shape", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// THE RULE, generically: the empty object and the empty array stay, the scalar goes; and a
// default the user has since filled is the user's, so it stays too — kept as the user's value, not
// as shape. Until CO-D12 that last case went ("content, not shape"): under `none` no apply relabels
// a filled default as the user's, so the revert took the user's entries with yolo's empty table.
func TestARevertKeepsOnlyEmptyDefaultsStillAtTheirDeclaredValue(t *testing.T) {
	p := shapeDefaultPack(t)
	home := t.TempDir()
	if _, err := RenderHostPack(p, home, render.OwnershipOwn, false, nil, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	path := filepath.Join(home, ".shape", "cfg.json")
	rev, err := RevertHostRender([]*packload.Pack{p}, home, false)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	doc := decodeJSONFile(t, path)
	for _, key := range []string{"table", "list"} {
		if _, kept := doc[key]; !kept {
			t.Errorf("the empty default %q was removed: %v", key, doc)
		}
	}
	if _, kept := doc["fillMe"]; kept {
		t.Errorf("the scalar default survived the revert: %v", doc)
	}
	if len(rev.Kept) != 2 {
		t.Errorf("the revert names %d kept keys, want the two empty defaults: %+v", len(rev.Kept), rev.Kept)
	}

	// Filled since the apply: `table` holds the user's entry, so it is no longer the default the
	// pack declares — it is the user's, and the revert leaves it and says so.
	home = t.TempDir()
	if _, err := RenderHostPack(p, home, render.OwnershipOwn, false, nil, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	path = filepath.Join(home, ".shape", "cfg.json")
	if err := os.WriteFile(path, []byte(`{"table":{"mine":1},"list":[],"fillMe":"byYolo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rev, err = RevertHostRender([]*packload.Pack{p}, home, false)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if table, _ := decodeJSONFile(t, path)["table"].(map[string]any); table["mine"] != float64(1) {
		t.Errorf("a default the user filled was taken out: %v", decodeJSONFile(t, path))
	}
	named := false
	for _, k := range rev.Kept {
		if k.Key == "table" {
			named = k.Why != "" && k.Why != keptWhyShape
		}
	}
	if !named {
		t.Errorf("the filled default is not reported as kept for being the user's: %+v", rev.Kept)
	}
}
