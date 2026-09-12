package entrypoint

// hostownednullprecedence_test.go measures ONE case of §11's criterion end to end, because
// it is the case the unit tests beside it cannot state: a key whose value is `null` AND
// which a pack layer also declares.
//
// ⚠ IT IS NOT A DUPLICATE OF hostownedkeysandvalues_test.go's null case. That fixture's
// nulls sit at keys NO layer declares, so they land in empty space and the only question is
// whether they arrive at all. This one puts a null at a key a pack's `defaults` DOES declare,
// where the question is instead WHICH ONE WINS — and the two answers were different until
// 2026-09-12, when `defaults` won and the user's value changed across the switch.
//
// WHY IT IS THE SWITCH THAT SETTLES IT, rather than a preference about nulls. The two
// mechanisms disagreed about whether a null-valued key is PRESENT:
//
//	seed WITHOUT the key       -> `assert` writes the default   (an absent key is filled)
//	seed with `"theme": null`  -> `assert` leaves the null      (a null-valued key is not)
//	                           -> `own` wrote the default       <- the bug
//
// Both halves are asserted below, in that order, so the case cannot be "fixed" by making
// `assert` stop filling defaults. §11 (OQ-CO12) requires the switch to keep every key AND
// EVERY VALUE; a value that changes is a violation under the relaxed criterion exactly as it
// was under the byte one — the relaxation freed FORMATTING, not values.
//
// Every test here renders into a t.TempDir() home. ⚠ Never point one at a real home.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// nullPrecedencePack declares `theme` in `defaults` — the layer BELOW the capture overlay,
// and the one whose whole job is to fill a key the user did not set.
func nullPrecedencePack(t *testing.T) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path":     "~/.acme/settings.json",
		"defaults": map[string]any{"theme": "system"},
		"managed":  map[string]any{"permissions": map[string]any{"defaultMode": "default"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// seedAndAssert writes seed into a fresh home and applies once under `assert`, returning the
// home and the bytes that home then holds.
func seedAndAssert(t *testing.T, seed string) (home, path string, asserted []byte) {
	t.Helper()
	home = t.TempDir()
	path = filepath.Join(home, ".acme/settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderHostPack(nullPrecedencePack(t), home, render.OwnershipAssert, false, nil); err != nil {
		t.Fatalf("the `assert` apply: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return home, path, b
}

// themeOf decodes the file and returns `theme` and whether the key is PRESENT — the
// distinction the whole case turns on, and one a plain lookup would lose.
func themeOf(t *testing.T, data []byte) (v any, present bool) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode: %v\n%s", err, data)
	}
	v, present = m["theme"]
	return v, present
}

// FIRST HALF: what `assert` does, which is the baseline the switch has to reproduce.
func TestAssertFillsAnAbsentDefaultButNotANullOne(t *testing.T) {
	t.Run("absent key: the default is written", func(t *testing.T) {
		_, _, asserted := seedAndAssert(t, `{"zebra": 1}`)
		v, present := themeOf(t, asserted)
		if !present || v != "system" {
			t.Fatalf("theme = %#v (present=%v), want \"system\" — if `assert` has stopped "+
				"filling an absent default, the case below no longer measures a "+
				"DISAGREEMENT between the two mechanisms and needs rewriting rather than "+
				"deleting", v, present)
		}
	})
	t.Run("null-valued key: the user's null is left alone", func(t *testing.T) {
		_, _, asserted := seedAndAssert(t, `{"zebra": 1, "theme": null}`)
		v, present := themeOf(t, asserted)
		if !present || v != nil {
			t.Fatalf("theme = %#v (present=%v), want a present null — rmw fills a default "+
				"only where the key is ABSENT, and a null-valued key is present", v, present)
		}
	})
}

// SECOND HALF, AND THE CRITERION: switching to `own` must not change that value.
func TestSwitchingToOwnKeepsANullThatALowerLayerDeclares(t *testing.T) {
	home, path, asserted := seedAndAssert(t, `{"zebra": 1, "theme": null}`)
	if v, present := themeOf(t, asserted); !present || v != nil {
		t.Fatalf("the assert baseline is not what this test is about: theme = %#v "+
			"(present=%v)", v, present)
	}

	for _, pass := range []string{"first", "second"} {
		if _, err := RenderHostPack(nullPrecedencePack(t), home, render.OwnershipOwn, false, nil); err != nil {
			t.Fatalf("the %s `own` apply: %v", pass, err)
		}
		owned, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		v, present := themeOf(t, owned)
		if !present {
			t.Fatalf("%s `own` apply: the key is GONE. A literal null is carried beside the "+
				"layer stack (agentcfg.Inputs.LiteralNulls) because no merge patch can hold "+
				"one; if it is not being read off the file on this branch, it is not being "+
				"read at all.\n%s", pass, owned)
		}
		if v != nil {
			t.Fatalf("%s `own` apply: theme = %#v, want the user's null.\n\nA literal null "+
				"folds at the CAPTURE OVERLAY's precedence — it IS a captured value — so "+
				"`defaults` must lose to it exactly as it loses to a non-null captured "+
				"value. Getting %q here means reinstateAt is treating a layer BELOW the "+
				"overlay as evidence against the file, through `spoken` "+
				"(agentcfg.Compose's overlayIdx) or through `present` "+
				"(agentcfg.reinstateAt). docs/design/config-ownership-and-promotion.md §11, "+
				"OQ-CO12: the switch keeps every key AND every value.\n%s",
				pass, v, v, owned)
		}
	}
}
