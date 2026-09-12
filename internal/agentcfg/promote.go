package agentcfg

// promote.go is the ENGINE side of `yolo config promote`
// (docs/design/config-ownership-and-promotion.md §5): the two questions the CLI cannot
// answer without the capture overlay's own reader, exported so the verb cannot grow a
// second one.
//
// Both take the SIDECAR BYTES rather than a decoded value, and that is the point of the
// pair. The overlay sidecar is always JSON regardless of the surface codec, may carry null
// tombstones, and is parsed by parseOverlayKind with a defined answer for every degenerate
// state (absent, empty, undecodable, wrong shape — staterender.go's §3.3 handling). A
// caller that decoded it itself would be a second reader of a format whose whole
// correctness argument is that it has one — which is exactly the defect
// docs/design/config-ownership-and-promotion.md §5.2 was written against, one layer up.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
)

// DeadOverlayKeys reports the TOP-LEVEL captured keys that a higher-ranking layer
// overrides ENTIRELY, so promoting them could not help: they do not reach the file where
// they already sit, and the destination is strictly lower in the fold (§5.4).
//
// # It is narrowOverlay, asked a different question
//
// The boot render narrows the accumulated overlay against `computed` and `managed` on
// every boot and persists the result, so in steady state a sidecar holds no dead key at
// all. What this answers is the case that narrowing CANNOT have covered, and it is a
// property of WHEN the pass runs rather than of what it can see: narrowOverlay ran at the
// last boot, against the layers as they were declared THEN. A pack that started asserting
// a key since — a new managed key, a `needs`-joined pack, an edited local pack — leaves a
// captured key dead where it sits, and the sidecar will not be canonicalized until the
// next boot. Promote reads the sidecar in between, so it has to ask the question itself.
//
// (§5.2 named a second dead class — a key the Lua `transform` rewrote, which narrowOverlay
// never saw because its signature takes only the two owner layers. That class is EMPTY as
// of docs/design/lua-transform-removal.md: the transform step is gone from Compose, so
// nothing folds above the overlay but `computed` and `managed`, and both are parameters
// here.)
//
// Running the ENGINE's own pass rather than a comparison of its own is what keeps
// promote's answer and the next boot's identical. A key this drops is precisely a key the
// next boot would delete from the sidecar unpromoted.
//
// TOP-LEVEL, because promotion moves top-level keys. A key the pass narrows only PARTLY —
// some leaves dead under an object-valued owner, others live — is not dead: the live
// leaves still reach the file, and dropOverriddenKeys keeps them for the reason its own
// docstring gives (claude/settings' `permissions.ask` under a managed `permissions`).
//
// A KEYLESS surface (raw/lines) has no top-level keys — the whole file is one value — so
// this reports nothing for one. Promote refuses those surfaces before it gets here, on the
// stronger ground that there is no key to name.
func DeadOverlayKeys(kind codec.Kind, overlayJSON []byte, computed, managed any) []string {
	before, ok := parseOverlayKind(kind, overlayJSON).(map[string]any)
	if !ok {
		return nil
	}
	after, _ := narrowOverlay(kind, before, computed, managed).(map[string]any)
	var dead []string
	for k := range before {
		if _, alive := after[k]; !alive {
			dead = append(dead, k)
		}
	}
	sort.Strings(dead)
	return dead
}

// DeleteOverlayKeys removes top-level keys from a capture overlay sidecar, returning the
// new sidecar content, the PRE-IMAGE that undoes it, and the keys it actually removed.
//
// This is the per-key half of `yolo config reset`, which is whole-surface only: it deletes
// the overlay sidecar AND the last_render sidecar AND truncates the surface file, because
// discarding every captured edit means all three. Promotion discards ONE key and declares
// it somewhere else, so it must touch exactly one of those three.
//
// # Why the sidecar alone, and why last_render must NOT move
//
// last_render is the BRANCH SELECTOR and the accumulate base (staterender.go's file
// header): ComposeStateful trusts it or takes the first-migration path, and steady-state
// capture diffs the current file against it. Truncating or deleting it would turn the next
// boot into a first migration, which ADOPTS the on-disk file — re-capturing the very key
// just promoted, from a file that still holds it, and putting it back in the sidecar one
// layer above its new declaration. That is the double declaration the verb exists to end.
// Leaving it alone is what makes the next boot a plain steady-state render: the key is
// gone from the overlay, the pack's config-overlay now supplies it, and the composed file
// is unchanged — so capture sees no delta and records nothing.
//
// The SURFACE FILE is untouched for the same reason from the other side. It already holds
// the promoted value; the next render puts the same value there from the new layer.
// Truncating it (which is what reset does) would make a successful promotion look like a
// loss to the agent reading that file right now.
//
// # The pre-image contract
//
// preImage is the CALLER'S OWN INPUT BYTES, verbatim — not a re-encoding of the value
// before the delete. Writing it back to the sidecar path restores the file byte-for-byte,
// including whatever formatting or key order it had, so a rollback cannot itself be a
// change. It is non-nil whenever this returns without error, and a caller that writes it
// back has undone the delete exactly.
//
// An overlay that is absent, empty, undecodable, or not an object is an ERROR rather than
// a no-op: every one of them means "there is no captured key here to promote", and a
// caller that treated them as success would report promoting a key it never removed. (That
// is also why this does not use parseOverlayKind's forgiving reading, which is right for a
// boot that must not break on a mangled home and wrong for a write that must not lie.)
//
// A key not present is simply not in `removed`; it is the caller's business whether that is
// an error (`--keys` naming a key nothing captured is, §5.6).
func DeleteOverlayKeys(overlayJSON []byte, keys []string) (updated, preImage []byte, removed []string, err error) {
	if len(bytes.TrimSpace(overlayJSON)) == 0 {
		return nil, nil, nil, fmt.Errorf("agentcfg: overlay sidecar is empty — there are no captured keys to remove")
	}
	var decoded any
	if uerr := json.Unmarshal(overlayJSON, &decoded); uerr != nil {
		return nil, nil, nil, fmt.Errorf("agentcfg: overlay sidecar does not decode: %w", uerr)
	}
	m, isObject := decoded.(map[string]any)
	if !isObject {
		return nil, nil, nil, fmt.Errorf("agentcfg: overlay sidecar holds %T, not an object — "+
			"a keyless surface has no top-level key to remove", decoded)
	}
	for _, k := range keys {
		if _, present := m[k]; !present {
			continue
		}
		delete(m, k)
		removed = append(removed, k)
	}
	// marshalOverlay, not a local encoder: the sidecar this writes back is read by the next
	// boot's parseOverlayKind, so it has to be the same encoding the boot itself persists —
	// sorted keys, indented, `{}` for an emptied overlay rather than `null`.
	updated, err = marshalOverlay(m)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("agentcfg: re-encoding the overlay sidecar: %w", err)
	}
	return updated, append([]byte(nil), overlayJSON...), removed, nil
}
