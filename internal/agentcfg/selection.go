package agentcfg

// selection.go is the RESERVED SELECTION namespace of a computed layer, and the
// edge-triggered apply that lifts it onto a stateful surface's root
// (docs/reference/providers.md#selection-write-on-activation-never-on-absence).
//
// A pack's derive may return its selection under one reserved top-level key rather
// than as ordinary computed keys:
//
//	derive returns   { selection = { model_provider = "llamacpp", model = "llama" } }
//	the agent file gets   model_provider = "llamacpp"   and   model = "llama"
//
// The indirection exists because a selection key cannot travel as an ordinary
// computed key. Every ordinary computed key is RE-ASSERTED on every boot — that is
// what "regenerate, don't reconcile" means, and it is the right semantics for an MCP
// table or an LSP toggle, whose content is yolo's own output. A selection key names a
// choice the agent ALSO owns: pi and opencode both let a user change the model
// interactively mid-session, and a key yolo re-asserted each boot would silently
// revert that choice on the next launch — the exact hazard §5.1 refuses. So the
// selection is written under a namespace the render treats differently, and the
// rendered FILE is identical in shape to what a plain computed key would have
// produced: the namespace is an implementation detail of the layer, never of the file.
//
// The three properties the apply implements, all §5.1:
//
//	write on activation    a key the file does not have gets the selected value.
//	clear yolo's own       a key the selection stops naming is cleared when the file
//	                       still holds what yolo wrote, and kept when the user changed
//	                       it (OQ-PSW2, docs/reference/providers.md#oq-psw2).
//	user edit wins         a key whose value the user changed since yolo wrote it is
//	                       left alone, until a NEW selection value differs from the
//	                       last one yolo wrote — an explicit selection outranks a
//	                       stale interactive choice, but never an un-stale one.
//
// A SCALAR here is a string, a boolean, or a number. A selection value is a scalar or an
// ARRAY of scalars (pi's enabledModels), never an object: the namespace's body is a flat
// map, and a table under it would be indistinguishable — to
// internal/entrypoint.hostTableKeys's sentinel probe, which asks the derive which of its
// keys are wholesale-managed tables — from a table yolo owns and would be regenerated as
// one. An array is a leaf to every reader (replaced whole, never merged into), so it
// carries no such ambiguity. Objects are therefore refused, not flattened, at
// TakeSelection.

import (
	"encoding/json"
	"reflect"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
)

// SelectionKey is the reserved top-level key of a computed layer. See the package
// file comment for what travels under it and why it is a namespace rather than a
// config key.
const SelectionKey = "selection"

// TakeSelection splits a computed layer into its ordinary keys and its selection.
// rest is every key except the reserved one (a new map; the input is never
// mutated), selection is the validated flat scalar map (nil when the derive emitted
// nothing under SelectionKey), and problems says what was refused.
//
// Nothing here fails a render. A malformed namespace is a pack-authoring mistake in
// one optional layer, and refusing the whole computed layer over it would cost the
// surface its MCP tables and every other key to teach one author a schema. Each
// problem names the value dropped, so the mistake is legible rather than silent.
func TakeSelection(computed map[string]any) (rest, selection map[string]any, problems []string) {
	raw, present := computed[SelectionKey]
	if !present {
		return computed, nil, nil
	}
	rest = withoutKey(computed, SelectionKey)
	body, isMap := raw.(map[string]any)
	if !isMap {
		return rest, nil, []string{
			"reserved " + SelectionKey + " key is not a table; dropped — it must be a flat " +
				"map of scalar key to value (docs/reference/providers.md#selection-write-on-activation-never-on-absence)",
		}
	}
	selection = map[string]any{}
	for _, k := range sortedMapKeys(body) {
		v := body[k]
		if !isSelectionValue(v) {
			problems = append(problems, "reserved "+SelectionKey+" key "+k+
				" is not a scalar (string, number, or boolean) or an array of scalars; "+
				"dropped — a table under "+SelectionKey+" would read as a yolo-managed table")
			continue
		}
		selection[k] = v
	}
	return rest, selection, problems
}

// DropSelection is TakeSelection for a surface that does not apply the namespace:
// the reserved key is removed so it can never reach the agent's file, and any
// problem is reported. A selection landing on a non-stateful surface is a pack
// authoring mistake — `computed` mode overwrites wholesale every boot and `rmw`
// has no capture baseline, so neither can honor the edge-triggered apply — and the
// alternative to dropping is a literal `selection` table written into the agent's
// config, which is worse than the mistake it reports.
func DropSelection(computed map[string]any) (map[string]any, []string) {
	if _, present := computed[SelectionKey]; !present {
		return computed, nil
	}
	return withoutKey(computed, SelectionKey), []string{
		"derive emitted the reserved " + SelectionKey + " namespace, which only a stateful " +
			"surface applies; dropped (docs/reference/providers.md#selection-write-on-activation-never-on-absence)",
	}
}

// ApplySelection is the edge-triggered apply: given the NEW selection, the surface
// file's current top-level keys, and the selection record (what yolo's selection
// mechanism last wrote, per key), it returns the keys to LIFT onto the computed
// layer's root and the record to persist.
//
// Every key the selection names or the record remembers is decided, and every
// decision except a CLEAR is LIFTED rather than omitted — including "keep the value
// that is already there". Lifting the current value is what makes a kept value hold:
// the stateful render rewrites the file wholesale from its layers, the capture
// overlay may still carry a STALE value for the key (the user's edit from before yolo
// took the key over), and a key no layer asserts would fall back to that stale value
// and silently change. Lifting the current value puts it in the computed layer, which
// outranks the overlay, so the file keeps exactly what it had.
//
// A clear (OQ-PSW2) is the one decision that omits, and omission is safe there
// because no capture overlay carries the key: the overlay is narrowed against the
// computed layer every boot (narrowOverlay), so a key yolo's selection wrote never
// sits in it, and omitting the key falls through to the host layer or the agent's own
// default, which is the point. A tombstone would delete the user's host value too. An
// ADOPTING boot seeds its overlay from the file, which holds the very value being
// cleared, so the caller hands the clears to the render too
// (StatefulInputs.SelectionCleared), which narrows them out of the overlay on either
// branch (docs/reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote).
//
// The record, not last_render, is what tells a yolo-written value from a
// user-written one, and that is forced rather than chosen: last_render is the bytes
// of the render one boot ago, so it converges to whatever the file holds — a user's
// edit is captured into the overlay on the very next boot (that capture is what
// keeps the edit alive across the wholesale rewrite), the overlay wins the fold, and
// last_render then records the user's value as if yolo had written it. One boot
// later, "is this mine?" is unanswerable from it and a same-selection re-render
// would revert the user. The record is written only by the selection mechanism, so
// it only moves when yolo's selection moves.
//
// Per key, with V the selected value, cur the file's value, and wrote the recorded
// one:
//
//	not selected                  cur == wrote: lift nothing and forget the key, so the
//	                              file falls back to the agent's default or the host
//	                              layer (OQ-PSW2); cur != wrote: lift cur, the user's
//	not in the file               lift V, record V — activation, and re-activation
//	                              after the user removed the key
//	cur == wrote                  lift V, record V when V differs — the selection
//	                              changed; else lift cur, nothing moves
//	cur != wrote                  the user changed it interactively. Lift cur —
//	                              unless V differs from wrote, which is a NEW
//	                              selection and outranks the stale choice: lift V,
//	                              record V
//	no record, cur == V           adopt: lift V, record V — a value equal to the
//	                              selection cannot be told from yolo's own write
//	cur is the host layer's       lift V, record V — a value the user's HOST config
//	                              supplies is not an in-jail edit, and a selection
//	                              outranks it (OQ-SW1); see HostOwnedKeys
//
// A nil or empty selection with no record returns nil, nil, which is every surface
// whose derive emits no selection: no lift, no record, no sidecar, and a render
// byte-identical to a build without this mechanism.
func ApplySelection(selection, file, record map[string]any) (lift, next map[string]any) {
	return ApplySelectionOver(selection, file, record, nil)
}

// ApplySelectionOver is ApplySelection with the keys whose current file value belongs to
// the HOST layer (HostOwnedKeys). Without that set every unrecorded file value reads as the
// user's in-jail edit, which is how a host settings.json rendered into a jail's file once
// made `-p` inert for pi forever (OQ-SW1, docs/reference/providers.md#oq-sw1).
func ApplySelectionOver(selection, file, record map[string]any, hostOwned map[string]bool) (lift, next map[string]any) {
	lift, next, _ = ApplySelectionReport(selection, file, record, hostOwned)
	return lift, next
}

// SelectionClear is one key a deselect cleared: a value yolo's selection wrote, still in the
// file unedited, whose profile is no longer selected, so it is omitted from the render
// (OQ-PSW2) and falls back to the native default or the host layer.
type SelectionClear struct {
	Key   string
	Value any // the value that was cleared, as the file held it
}

// ApplySelectionReport is ApplySelectionOver that also reports every key the deactivated arm
// cleared, sorted by key. It is the ONE implementation of the arm: the report is what the arm
// did, never a second reading of the same rule. The caller hands it to the stateful render
// (StatefulInputs.SelectionCleared), which keeps those keys out of the capture overlay and
// reports back the ones that left the file (StatefulOutput.SelectionCleared); that second
// report, not this one, is what the boot log (OQ-PSW4) prints.
func ApplySelectionReport(selection, file, record map[string]any, hostOwned map[string]bool) (lift, next map[string]any, cleared []SelectionClear) {
	if len(selection) == 0 && len(record) == 0 {
		return nil, nil, nil
	}
	lift = map[string]any{}
	next = map[string]any{}
	for k, v := range record {
		next[k] = v
	}
	// The key set is the union: a key the selection stopped naming still needs its
	// current value lifted (the never-clear case), and a key only the record
	// remembers is one yolo is still accountable for.
	for _, k := range unionKeys(selection, record) {
		V, selected := selection[k]
		cur, inFile := file[k]
		switch {
		case !selected:
			// Deactivated. If yolo wrote this value (cur == record[k]), clear it:
			// omit from lift so it falls back to native/host defaults, and drop
			// from the record. Only an interactive user edit (cur != record[k])
			// is preserved.
			if inFile {
				if isSelectionValue(cur) {
					if wrote, ok := record[k]; ok && sameScalar(cur, wrote) {
						delete(next, k)
						cleared = append(cleared, SelectionClear{Key: k, Value: cur})
					} else {
						lift[k] = cur
					}
				}
			} else {
				delete(next, k)
			}
		case !inFile:
			// Activation: the key is not in the file, so nothing of the user's is
			// in the way.
			lift[k] = V
			next[k] = V
		case sameScalar(cur, record[k]):
			// The file holds exactly what yolo last wrote, so the value is yolo's
			// to move. A differing selection is a real change; an equal one is a
			// re-render of the same choice and writes nothing new.
			if !sameScalar(cur, V) {
				lift[k] = V
				next[k] = V
			} else {
				lift[k] = cur
			}
		default:
			// The user changed the value yolo wrote (or yolo never wrote it). Their
			// value stands — unless the selection itself has moved off the last
			// value yolo wrote, which is an explicit new choice outranking a stale
			// interactive one.
			wrote, recorded := record[k]
			switch {
			case recorded && !sameScalar(wrote, V):
				lift[k] = V
				next[k] = V
			case hostOwned[k]:
				// The HOST's value, not the user's (OQ-SW1): the user's host config put it
				// there, and "just because they're the host files doesn't mean you want them
				// that way in the jail". The selection is the more specific choice.
				lift[k] = V
				next[k] = V
			case !recorded && sameScalar(cur, V):
				// ADOPTION: nothing is recorded for the key, and the file already holds
				// exactly the selected value. That value is indistinguishable from yolo's
				// own write, so it is recorded as one. This is the upgrade path for a key
				// that moved INTO the selection (pi's enabledModels, re-asserted every
				// boot as a computed key before), whose file value would otherwise read
				// as the user's forever and never move with the selection again.
				lift[k] = V
				next[k] = V
			case isSelectionValue(cur):
				lift[k] = cur
			}
		}
	}
	return lift, next, cleared
}

// dropSelectionCleared removes every cleared key from an object overlay (a new map; the
// input is never mutated), and returns any other overlay as it came. Top level only, because
// a selection key is a top-level key of the surface (TakeSelection's body is a flat map).
func dropSelectionCleared(overlay any, cleared []SelectionClear) any {
	om, isMap := overlay.(map[string]any)
	if !isMap {
		return overlay
	}
	for _, c := range cleared {
		om = withoutKey(om, c.Key)
	}
	return om
}

// clearsThatLeft is the part of cleared whose value no longer stands in the rendered file:
// the key is absent, or it holds a different value (a lower layer's). A key still holding the
// cleared value is dropped from the report, since the file does not show that clear. nil when
// nothing left.
func clearsThatLeft(cleared []SelectionClear, rendered map[string]any) []SelectionClear {
	var out []SelectionClear
	for _, c := range cleared {
		if v, stands := rendered[c.Key]; stands && sameScalar(v, c.Value) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// HostOwnedKeys names the top-level keys whose value in the surface file belongs to the
// HOST layer rather than to an in-jail edit, for ApplySelectionOver. A key is the host's
// when its file value either
//
//   - equals what the host layer supplies NOW (host), or
//   - is unchanged since the previous render (lastRender) AND that render's provenance
//     names the host layer as the key's winner — a host value an earlier launch rendered,
//     whose source has since moved on the host.
//
// A value the user edited in the jail matches neither: it differs from the host layer, and
// it differs from the previous render (or, one boot later, the provenance names the
// capture overlay). A user edit that happens to equal the host's value is indistinguishable
// from it and harmless to treat as the host's. Any input may be nil.
func HostOwnedKeys(file, host, lastRender map[string]any, provenance map[string]string) map[string]bool {
	var out map[string]bool
	for k, cur := range file {
		if !isSelectionValue(cur) {
			continue
		}
		owned := false
		if hv, ok := host[k]; ok && sameScalar(cur, hv) {
			owned = true
		} else if lv, ok := lastRender[k]; ok && provenance[k] == layerHost && sameScalar(cur, lv) {
			owned = true
		}
		if owned {
			if out == nil {
				out = map[string]bool{}
			}
			out[k] = true
		}
	}
	return out
}

// DecodeSurfaceObject decodes bytes with the named codec and returns the object's
// top-level keys, or nil when the bytes are absent, undecodable, or not an object.
// The fail-silent shape is deliberate: its one caller reads the agent's own file to
// decide what the selection mechanism may touch, and a file it cannot read is a file
// it has no claim over — the same fail-safe readProvenanceRecord takes, for the same
// reason.
func DecodeSurfaceObject(codecName string, data []byte) map[string]any {
	c, ok := codec.LookupCodec(codecName)
	if !ok {
		return nil
	}
	decoded, ok := decodeKind(c, codec.KindObject, data)
	if !ok {
		return nil
	}
	m, _ := decoded.(map[string]any)
	return m
}

// ParseSelectionRecord decodes a persisted selection record (JSON, key → value) into
// the map ApplySelection reads, or nil when there is nothing trustworthy. Numbers
// are normalized the way the record's own writer normalized them, so a recorded
// integer compares equal to the value the file decodes it back to.
//
// A non-scalar value is dropped rather than passed on, mirroring the gate TakeSelection
// applies to the derive's own emit: the writer can only ever record scalars, so a
// non-scalar in the file is a hand edit or corruption, and the fail-safe answer is the
// same one an absent record gets — that key claims nothing, and whatever now sits in the
// agent's file reads as the user's. nil when nothing survives.
func ParseSelectionRecord(data []byte) map[string]any {
	if len(data) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if !isSelectionValue(v) {
			continue
		}
		out[k] = selectionValue(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// withoutKey returns m without the named key, as a new map. Values are shared, not
// copied: the engine treats layer values as immutable.
func withoutKey(m map[string]any, key string) map[string]any {
	if _, present := m[key]; !present {
		return m
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k == key {
			continue
		}
		out[k] = v
	}
	return out
}

// sortedMapKeys returns m's keys in a deterministic order, so a re-render decides
// and reports the same way twice over the same inputs.
func sortedMapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// unionKeys returns the keys of both maps, sorted. Sorted so a caller that reports
// on the decisions does so in a stable order, and so two runs over the same inputs
// cannot differ.
func unionKeys(a, b map[string]any) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]any{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// selectionScalar normalizes one value to the form the selection mechanism compares
// with. The three sources of a value decode numbers differently — the derive
// marshals an integral Lua number to int64, the TOML file decodes one to int64, and
// the JSON record round-trips it as float64 — and the comparison must read `8080`,
// `8080` and `8080` as the same choice rather than as two edits, which would hand a
// user's own value to yolo. Non-numbers pass through untouched.
func selectionScalar(v any) any {
	switch t := v.(type) {
	case int:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case float32:
		return float64(t)
	default:
		return v
	}
}

// isScalar reports whether v is a string, a boolean, or a number.
func isScalar(v any) bool {
	switch v.(type) {
	case string, bool, int, int32, int64, float32, float64:
		return true
	}
	return false
}

// isSelectionValue reports whether v is a value a selection may carry: a scalar, or an
// ARRAY of scalars (pi's enabledModels). An array is a LEAF — the render replaces it whole
// and never merges into it — so it cannot read as a yolo-managed table, which is the only
// thing the refusal protects (hostTableKeys claims object-valued keys alone). Everything
// else — an object, an array holding one, nil — is refused at TakeSelection.
func isSelectionValue(v any) bool {
	if isScalar(v) {
		return true
	}
	list, ok := v.([]any)
	if !ok {
		return false
	}
	for _, el := range list {
		if !isScalar(el) {
			return false
		}
	}
	return true
}

// sameScalar reports whether two selection values are the same choice, after number
// normalization (see selectionScalar for why the normalization is not optional). Arrays
// compare element by element, in order: a reordered list is a different choice, since pi
// starts a session on the FIRST enabled model.
func sameScalar(a, b any) bool {
	return reflect.DeepEqual(selectionValue(a), selectionValue(b))
}

// selectionValue is selectionScalar applied to a scalar or to every element of an array,
// so a derive's []any of int64 and a record's []any of float64 compare equal.
func selectionValue(v any) any {
	list, ok := v.([]any)
	if !ok {
		return selectionScalar(v)
	}
	out := make([]any, len(list))
	for i, el := range list {
		out[i] = selectionScalar(el)
	}
	return out
}
