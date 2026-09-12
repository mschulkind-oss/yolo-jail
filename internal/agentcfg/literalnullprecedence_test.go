package agentcfg

// literalnullprecedence_test.go pins WHERE a literal null folds, which is the half of
// literalnull.go that shipped wrong on 2026-09-12 and the half a reader is most likely to
// get backwards.
//
// THE RULE, stated once. A literal null is a CAPTURED VALUE — the one shape the overlay
// sidecar cannot spell, carried beside the layer stack instead of inside it
// (Inputs.LiteralNulls) — so it folds at the CAPTURE OVERLAY's precedence. Everything the
// overlay outranks loses to it; everything that outranks the overlay beats it.
//
//	defaults  host  workspace  config-overlay:<pack>  │  overlay  │  computed  managed
//	          lose to the file's null                  │  the null │  beat it
//
// ⚠ IT WAS "A LAYER ALWAYS WINS" — every layer, at every precedence — and that is a bug
// rather than a conservative choice, because it made the null the ONE captured value in this
// engine that a LOWER layer could overwrite. Measured end to end in
// internal/entrypoint/hostownednullprecedence_test.go: a file holding `"theme": null` against
// a pack whose `defaults` says `"system"` keeps the null under `assert` and took the default
// under `own`, so a VALUE changed across the switch that
// docs/design/config-ownership-and-promotion.md §11 (OQ-CO12) says keeps every value.
//
// The two halves of the fix are independently mutable and this file kills both: narrow the
// evidence set back to "every layer but the overlay" (Compose's overlayIdx) and the
// below-the-line cases go red on `spoken`; restore reinstateAt's `present` veto and they go
// red on `present`. The above-the-line cases are what stops either being "fixed" by deleting
// the evidence set altogether.

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// TestLiteralNullsLoseOnlyToLayersAboveTheFile walks the fold order and asserts the null's
// position in it, one layer per case.
func TestLiteralNullsLoseOnlyToLayersAboveTheFile(t *testing.T) {
	// The control, and the reason the rule is not a judgement call: the SAME key carrying a
	// NON-null captured value beats the same layer. If this case ever disagrees with the
	// null cases below, the two are folding at different precedences and one of them is
	// wrong — that is the whole defect this file exists for.
	t.Run("control: a non-null captured value beats defaults", func(t *testing.T) {
		cfg, _ := composeWith(t, Inputs{
			Surface: manifest.Surface{Agent: "acme", Name: "settings", Codec: "json",
				Defaults: map[string]any{"theme": "system"}},
			Overlay: map[string]any{"theme": "dark"},
		})
		if !reflect.DeepEqual(cfg, map[string]any{"theme": "dark"}) {
			t.Fatalf("the capture overlay no longer outranks defaults, so the null cases "+
				"below are measuring nothing: %#v", cfg)
		}
	})

	for _, tc := range []struct {
		name string
		in   Inputs
		want map[string]any
	}{
		{
			// THE MEASURED BUG. `defaults` fills an ABSENT key; a null-valued key is
			// present, which is exactly how rmw reads it under `assert`.
			name: "defaults loses",
			in: Inputs{
				Surface: manifest.Surface{Agent: "acme", Name: "settings", Codec: "json",
					Defaults: map[string]any{"theme": "system"}},
				LiteralNulls: map[string]any{"theme": nil},
			},
			want: map[string]any{"theme": nil},
		},
		{
			name: "the host layer loses",
			in: Inputs{
				Surface:      manifest.Surface{Agent: "acme", Name: "settings", Codec: "json"},
				HostBytes:    []byte(`{"theme":"from-host"}`),
				LiteralNulls: map[string]any{"theme": nil},
			},
			want: map[string]any{"theme": nil},
		},
		{
			name: "the workspace layer loses",
			in: Inputs{
				Surface:      manifest.Surface{Agent: "acme", Name: "settings", Codec: "json"},
				Workspace:    map[string]any{"theme": "from-workspace"},
				LiteralNulls: map[string]any{"theme": nil},
			},
			want: map[string]any{"theme": nil},
		},
		{
			// A pack's config-overlay folds BELOW the capture overlay, so a key of the
			// user's outranks it — with a null exactly as with any other value. This is
			// the same answer `{}` already gets since OQ-CO12's second fix, and the two
			// have to agree or "the user's file wins over a pack's overlay" is true for
			// one spelling of a value and false for another.
			name: "a pack's config-overlay loses",
			in: Inputs{
				Surface:      manifest.Surface{Agent: "acme", Name: "settings", Codec: "json"},
				Overlays:     []Overlay{{Pack: "acme", Data: map[string]any{"theme": "from-pack"}}},
				LiteralNulls: map[string]any{"theme": nil},
			},
			want: map[string]any{"theme": nil},
		},
		{
			// ABOVE THE LINE. A value yolo computed THIS BOOT is a decision, not a
			// default, and the file does not get to undo it.
			name: "computed wins",
			in: Inputs{
				Surface:      manifest.Surface{Agent: "acme", Name: "settings", Codec: "json"},
				Computed:     map[string]any{"theme": "computed-this-boot"},
				LiteralNulls: map[string]any{"theme": nil},
			},
			want: map[string]any{"theme": "computed-this-boot"},
		},
		{
			name: "managed wins",
			in: Inputs{
				Surface: manifest.Surface{Agent: "acme", Name: "settings", Codec: "json",
					Managed: map[string]any{"theme": "locked"}},
				LiteralNulls: map[string]any{"theme": nil},
			},
			want: map[string]any{"theme": "locked"},
		},
		{
			// AND THE TOMBSTONE CASE STAYS DELETED. Not a duplicate of "computed wins":
			// there the key is present after the fold and `present` alone could explain
			// the answer, while here the fold DELETED it, so only the evidence set can.
			name: "a computed tombstone stays deleted, with a defaults value underneath",
			in: Inputs{
				Surface: manifest.Surface{Agent: "acme", Name: "settings", Codec: "json",
					Defaults: map[string]any{"theme": "system"}},
				Computed:     map[string]any{"theme": nil},
				LiteralNulls: map[string]any{"theme": nil},
			},
			want: map[string]any{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := composeWith(t, tc.in)
			if !reflect.DeepEqual(cfg, tc.want) {
				t.Errorf("got %#v, want %#v\n\na literal null folds at the CAPTURE "+
					"OVERLAY's precedence — see this file's header for the order",
					cfg, tc.want)
			}
		})
	}
}

// AND THE PROVENANCE SAYS `overlay`, including when the null OVERRODE a lower layer rather
// than landing in empty space. It is the user's value that is in the file, so a reader asking
// "whose key is this?" must not be told `defaults` — the layer whose value is precisely the
// one that lost.
func TestAnOverridingLiteralNullIsAttributedToTheOverlay(t *testing.T) {
	_, res := composeWith(t, Inputs{
		Surface: manifest.Surface{Agent: "acme", Name: "settings", Codec: "json",
			Defaults: map[string]any{"theme": "system"}},
		LiteralNulls: map[string]any{"theme": nil},
	})
	if got := res.Provenance["theme"]; got != layerOverlay {
		t.Errorf("provenance for the reinstated null = %q, want %q", got, layerOverlay)
	}
	// And `overlay` still does not mean "yolo asserted this", which is what every reader
	// asking whether yolo wrote a key goes through.
	if LayerAsserted(layerOverlay) {
		t.Error("LayerAsserted(overlay) is true, so a captured null now reads as yolo's own")
	}
}
