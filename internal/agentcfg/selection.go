package agentcfg

// selection.go is the RESERVED SELECTION namespace of a computed layer, and the
// edge-triggered apply that lifts it onto a stateful surface's root
// (docs/design/provider-catalog-and-selection.md §5.1).
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
//	never on absence       a key the selection stops naming is left exactly as it is —
//	                       not cleared, not defaulted (OQ-CS2: the no-profile case is
//	                       the agent's own).
//	user edit wins         a key whose value the user changed since yolo wrote it is
//	                       left alone, until a NEW selection value differs from the
//	                       last one yolo wrote — an explicit selection outranks a
//	                       stale interactive choice, but never an un-stale one.
//
// A SCALAR here is a string, a boolean, or a number. A selection value is always a
// scalar: the namespace's body is a flat map, and a table under it would be
// indistinguishable — to internal/entrypoint.hostTableKeys's sentinel probe, which
// asks the derive which of its keys are wholesale-managed tables — from a table yolo
// owns and would be regenerated as one. Non-scalar values are therefore refused, not
// flattened, at TakeSelection.

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
				"map of scalar key to value (docs/design/provider-catalog-and-selection.md §5.1)",
		}
	}
	selection = map[string]any{}
	for _, k := range sortedMapKeys(body) {
		v := body[k]
		if !isScalar(v) {
			problems = append(problems, "reserved "+SelectionKey+" key "+k+
				" is not a scalar (string, number, or boolean); dropped — a table under "+
				SelectionKey+" would read as a yolo-managed table")
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
			"surface applies; dropped (docs/design/provider-catalog-and-selection.md §5.1)",
	}
}

// ApplySelection is the edge-triggered apply: given the NEW selection, the surface
// file's current top-level keys, and the selection record (what yolo's selection
// mechanism last wrote, per key), it returns the keys to LIFT onto the computed
// layer's root and the record to persist.
//
// Every key the selection names or the record remembers is decided, and every
// decision is LIFTED rather than omitted — including "keep the value that is
// already there". Lifting the current value is what makes a deactivation hold: the
// stateful render rewrites the file wholesale from its layers, the capture overlay
// may still carry a STALE value for the key (the user's edit from before yolo took
// the key over), and a key no layer asserts would fall back to that stale value and
// silently change. Lifting the current value puts it in the computed layer, which
// outranks the overlay, so the file keeps exactly what it had — the never-clear
// guarantee expressed in the layer the render already re-asserts.
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
//	not selected                  lift cur (or nothing, if the key is absent); the
//	                              record keeps what it knew — never clear (OQ-CS2)
//	not in the file               lift V, record V — activation, and re-activation
//	                              after the user removed the key
//	cur == wrote                  lift V, record V when V differs — the selection
//	                              changed; else lift cur, nothing moves
//	cur != wrote                  the user changed it interactively. Lift cur —
//	                              unless V differs from wrote, which is a NEW
//	                              selection and outranks the stale choice: lift V,
//	                              record V
//
// A nil or empty selection with no record returns nil, nil, which is every surface
// whose derive emits no selection: no lift, no record, no sidecar, and a render
// byte-identical to a build without this mechanism.
func ApplySelection(selection, file, record map[string]any) (lift, next map[string]any) {
	if len(selection) == 0 && len(record) == 0 {
		return nil, nil
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
			// Deactivated. Keep whatever the file has, and keep the memory of what
			// yolo wrote — a profile reactivated later still needs it to tell its
			// own write from the user's.
			if inFile && isScalar(cur) {
				lift[k] = cur
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
			if wrote, ok := record[k]; ok && !sameScalar(wrote, V) {
				lift[k] = V
				next[k] = V
			} else if isScalar(cur) {
				lift[k] = cur
			}
		}
	}
	return lift, next
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
		if !isScalar(v) {
			continue
		}
		out[k] = selectionScalar(v)
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

// isScalar reports whether v is a value a selection may carry: a string, a boolean,
// or a number. Everything else — a table, a list, nil — is refused at TakeSelection,
// for the hostTableKeys reason the package comment spells out.
func isScalar(v any) bool {
	switch v.(type) {
	case string, bool, int, int32, int64, float32, float64:
		return true
	}
	return false
}

// sameScalar reports whether two selection values are the same choice, after number
// normalization (see selectionScalar for why the normalization is not optional).
func sameScalar(a, b any) bool {
	return reflect.DeepEqual(selectionScalar(a), selectionScalar(b))
}
