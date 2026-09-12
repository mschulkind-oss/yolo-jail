package agentcfg

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
)

// A captured key an owning layer overrides OUTRIGHT is dead where it sits, and promotion
// moves it DOWN the fold, so it cannot help. The two owners are asked separately because
// they enter the pass at different granularities.
func TestDeadOverlayKeysReportsWhatAnOwningLayerOverrides(t *testing.T) {
	overlay := []byte(`{"model":"mine","tools":{"neovim":"nightly"},"keepMe":1}`)

	managed := map[string]any{"model": "theirs"}
	// The computed layer overrides the captured LEAF, not merely the table it sits in:
	// dropOverriddenKeys is the dual of the merge computed gets, so a captured sibling
	// computed does not hold (mise's own `tools.neovim`, live in this repo's jail) reaches
	// the file and is NOT dead.
	computed := map[string]any{"tools": map[string]any{"neovim": "stable"}}

	got := DeadOverlayKeys(codec.KindObject, overlay, computed, managed)
	if want := []string{"model", "tools"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DeadOverlayKeys = %v, want %v", got, want)
	}
	// The same table, one leaf over: computed regenerates `tools` but says nothing about
	// this entry, so the capture reaches the file and is live.
	sibling := map[string]any{"tools": map[string]any{"node": "22"}}
	if got := DeadOverlayKeys(codec.KindObject, overlay, sibling, nil); len(got) != 0 {
		t.Errorf("DeadOverlayKeys = %v, want none: `tools.neovim` is not a key computed holds", got)
	}
}

// THE GRANULARITY IS THE WHOLE POINT, and it is the one thing a simpler implementation
// (drop every top-level key an owner names) gets wrong: claude/settings' managed layer
// asserts `permissions.defaultMode` while Claude's own `permissions.ask` reaches the file
// untouched. A captured sibling inside an object-valued owner is LIVE, so the key is not
// dead and promote must not drop it.
func TestDeadOverlayKeysKeepsALiveSiblingUnderAnObjectOwner(t *testing.T) {
	overlay := []byte(`{"permissions":{"ask":["Bash"],"defaultMode":"plan"}}`)
	managed := map[string]any{"permissions": map[string]any{"defaultMode": "acceptEdits"}}

	if got := DeadOverlayKeys(codec.KindObject, overlay, nil, managed); len(got) != 0 {
		t.Errorf("DeadOverlayKeys = %v, want none: `ask` still reaches the file, so "+
			"`permissions` is not dead", got)
	}
	// The fully-owned form of the same key IS dead, so the test above is not passing for
	// want of any detection at all.
	overlay = []byte(`{"permissions":{"defaultMode":"plan"}}`)
	if got := DeadOverlayKeys(codec.KindObject, overlay, nil, managed); len(got) != 1 {
		t.Errorf("DeadOverlayKeys = %v, want [permissions]", got)
	}
}

// Every degenerate sidecar answers "nothing is dead" rather than panicking: promote reports
// an absent or unreadable overlay as having no keys, which is the same answer from the
// other direction.
func TestDeadOverlayKeysOnDegenerateSidecars(t *testing.T) {
	for _, data := range [][]byte{nil, []byte(""), []byte("   "), []byte("not json"), []byte(`["a"]`)} {
		if got := DeadOverlayKeys(codec.KindObject, data, nil, map[string]any{"a": 1}); got != nil {
			t.Errorf("DeadOverlayKeys(%q) = %v, want nil", data, got)
		}
	}
}

// The per-key delete: the named keys go, everything else stays, and what came back is the
// encoding the next boot reads.
func TestDeleteOverlayKeysRemovesOnlyTheNamedKeys(t *testing.T) {
	in := []byte("{\n  \"model\": \"mine\",\n  \"theme\": \"dark\",\n  \"env\": null\n}\n")

	updated, preImage, removed, err := DeleteOverlayKeys(in, []string{"model", "nosuch"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"model"}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed = %v, want %v (a key nothing captured is not removed)", removed, want)
	}
	left, ok := parseOverlayKind(codec.KindObject, updated).(map[string]any)
	if !ok {
		t.Fatalf("the rewritten sidecar does not read back as an overlay: %s", updated)
	}
	if _, still := left["model"]; still {
		t.Errorf("the promoted key survived the delete:\n%s", updated)
	}
	if v, ok := left["theme"]; !ok || v != "dark" {
		t.Errorf("an unpromoted key was lost:\n%s", updated)
	}
	// A TOMBSTONE is a captured deletion and is durable state like any other captured key:
	// dropping it here would resurrect the key on the next render.
	v, ok := left["env"]
	if !ok || v != nil {
		t.Errorf("the null tombstone was lost:\n%s", updated)
	}
	if !bytes.Equal(preImage, in) {
		t.Errorf("preImage is not the input verbatim:\ngot  %q\nwant %q", preImage, in)
	}
}

// THE PRE-IMAGE CONTRACT: writing it back restores the sidecar byte-for-byte, so a
// rollback cannot itself be a change. Stated as bytes rather than as "the same keys",
// because a rollback that re-encoded would rewrite a file the user never asked to touch.
func TestDeleteOverlayKeysPreImageRoundTripsExactly(t *testing.T) {
	in := []byte("{\"b\":2,\"a\":1}") // unsorted and unindented: NOT what marshalOverlay emits
	_, preImage, _, err := DeleteOverlayKeys(in, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(preImage, in) {
		t.Fatalf("rolling back would have rewritten the file:\ngot  %s\nwant %s", preImage, in)
	}
	// And it is a COPY: a caller that keeps the pre-image for a rollback must not be holding
	// a slice some later write can scribble on.
	in[1] = 'X'
	if bytes.Equal(preImage, in) {
		t.Error("preImage aliases the caller's buffer")
	}
}

// Emptying the overlay leaves `{}` — the empty merge patch — and NOT `null`, which
// parseOverlayKind would read as "no captured edits" for an object surface anyway but
// which no boot ever writes. One encoder, one shape.
func TestDeleteOverlayKeysEmptiesToAnEmptyPatch(t *testing.T) {
	updated, _, _, err := DeleteOverlayKeys([]byte(`{"only":"key"}`), []string{"only"})
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if uerr := json.Unmarshal(updated, &v); uerr != nil {
		t.Fatal(uerr)
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 0 {
		t.Errorf("emptied overlay = %s, want {}", updated)
	}
}

// "There is no captured key here" must be an ERROR, not a silent success: a caller that
// took it for success would report promoting a key it never removed — and would then reset
// nothing while the destination now declares the value, which is the double declaration
// promotion exists to end.
func TestDeleteOverlayKeysRefusesASidecarWithNoKeys(t *testing.T) {
	for name, in := range map[string][]byte{
		"absent":      nil,
		"empty":       []byte("   \n"),
		"undecodable": []byte("{oops"),
		"keyless":     []byte(`"the whole file"`),
		"list":        []byte(`["a"]`),
	} {
		if _, _, _, err := DeleteOverlayKeys(in, []string{"a"}); err == nil {
			t.Errorf("%s sidecar: DeleteOverlayKeys returned no error", name)
		}
	}
}
