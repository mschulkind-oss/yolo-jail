package agentcfg

// listcontrib.go is the `config-list` half of the composer: a pack adding ENTRIES to one
// array inside a surface another pack (or it itself) owns, without replacing the array
// (docs/design/additive-config-lists.md, both questions ruled 2026-09-23).
//
// It is a SECOND, deliberately narrow operation beside the merge patch rather than a change
// to it. RFC 7386 keeps its meaning everywhere — an array in a layer still replaces, and a
// null still deletes — and the design's warning is the reason: making arrays additive would
// break every deliberate empty array and every overlay that exists to replace one.
//
// WHERE IT FOLDS (OQ-AL2, and this file's placement of it):
//
//	defaults < host < workspace < config-overlay…            the ordinary merge patch
//	  → list contributions   (pack order, then declaration order)
//	  → capture overlay      (the merge patch of in-jail edits)
//	  → list capture         (per-entry in-jail edits at a list path: removes, then adds)
//	  → computed → managed
//
// So a later list contribution can re-add an entry an earlier overlay's replacement dropped,
// and only the capture, computed and managed layers can replace the assembled array.
//
// PER-ENTRY CAPTURE (OQ-AL1) is the half that makes this survive a `stateful` surface's
// read-back. A whole-array capture of `pi install` appending one package would hold every
// pack-contributed entry too, outrank every contribution, and freeze the list: later
// additions masked, a dropped pack's entries never removed. So at every LIST PATH — a path
// some list contribution targets, or one the list-capture sidecar already names — capture
// records per-entry additions and removals relative to the last render instead, and the
// capture overlay never records the path at all. ListRecord is that record; ComposeStateful
// (staterender.go) decides it and Compose (compose.go) applies it.
//
// `rmw` has no capture and no last render, so its equivalent is yolo's own record of what
// it INSERTED (ListInsertRecord, ReconcileInsertedList): an inserted entry the file no longer
// holds was removed by the user and is declined from then on; one no pack contributes any
// more is removed from the file; an entry already in the file that yolo did not insert is
// the user's and is never recorded, so it is never removed on a pack drop.
//
// ELEMENT IDENTITY is canonical JSON after normalizing the decoders' number types
// (entryKey). A pack.json decodes `1` as float64 while a TOML file decodes it as int64 and
// jsonx keeps it as an integer literal; comparing the raw values would call `1` and `1`
// different and write the entry twice. Nothing about the entries is interpreted: no package
// syntax is parsed, no URL normalized, nothing sorted (rule 2).

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// layerConfigList is the provenance label for a top-level key that ONLY list contributions
// created — no layer below them held it. A key that already existed keeps its own label
// (the array was assembled from that layer's value plus contributions, and Result.Lists
// carries the per-entry account). Never asserted (LayerAsserted is false for it): the key's
// entries may be the user's too, so no retirement or revert may remove the whole array on
// the strength of this label.
const layerConfigList = "config-list"

// LayerConfigList is layerConfigList exported, for the rmw provenance writer that has to
// speak the same vocabulary as Compose (see the exported constant block in compose.go).
const LayerConfigList = layerConfigList

// Entry sources in ListProvenance, beside ListContributionLayer's "config-list:<pack>".
const (
	// ListSourceBase marks an entry the array already held when the contributions folded
	// in: the owner's defaults, the host file, the workspace, or a config-overlay.
	ListSourceBase = "base"
	// ListSourceCaptured marks an entry the per-entry capture added (an in-jail edit).
	ListSourceCaptured = "captured"
)

// ListContributionLayer is the per-entry source label of a list contribution by pack.
func ListContributionLayer(pack string) string { return layerConfigList + ":" + pack }

// ListContribution is one decoded `config-list` contribution: the pack that declared it,
// the RFC 6901 pointer of the array inside the surface, and the entries to add in
// declaration order. Built by NewListContribution, which is the one validation point the
// engine trusts.
type ListContribution struct {
	// Pack names the contributing pack, for provenance and for every refusal.
	Pack string
	// Path is the RFC 6901 JSON Pointer of the target array. Never the root.
	Path string
	// Add are the entries, decoded to the plain value model. Never contains a nil.
	Add []any
}

// NewListContribution validates and decodes one contribution. packdecl has already refused
// every malformed manifest; this re-checks because a caller other than the collector (a
// test, a future loader) must not be able to hand the engine a root pointer or a null the
// TOML codec cannot encode.
func NewListContribution(pack, pointer string, add json.RawMessage) (ListContribution, error) {
	tokens, err := jsonptr.Parse(pointer)
	if err != nil {
		return ListContribution{}, err
	}
	if len(tokens) == 0 {
		return ListContribution{}, fmt.Errorf("config-list path must name an array inside the " +
			"surface, not the whole document")
	}
	var entries []any
	if err := json.Unmarshal(add, &entries); err != nil {
		return ListContribution{}, fmt.Errorf("config-list `add` must be a JSON array: %v", err)
	}
	for i, e := range entries {
		if e == nil {
			return ListContribution{}, fmt.Errorf("config-list `add[%d]` is null, which TOML "+
				"cannot encode", i)
		}
	}
	return ListContribution{Pack: pack, Path: pointer, Add: entries}, nil
}

// ListProvenance is the per-entry account of one list path in one render — the data behind
// rule 5's "show the ordered contributors, including an indication when a higher layer
// replaces their result". Nothing new is persisted for it; it is derived per render.
type ListProvenance struct {
	// Path is the RFC 6901 pointer.
	Path string
	// Entries are the assembled list, in final order, each with its source: ListSourceBase,
	// ListContributionLayer(pack), or ListSourceCaptured.
	Entries []ListEntry
	// ReplacedBy names the layer that replaced the assembled array wholesale — "overlay"
	// (a captured deletion or non-array edit), "computed" or "managed" — or "" when the
	// assembled list is what the file holds.
	ReplacedBy string
}

// ListEntry is one entry of a ListProvenance.
type ListEntry struct {
	Value  any
	Source string
}

// ListRecord is the per-entry capture at one list path of a `stateful` surface: the entries
// in-jail edits added and removed relative to the rendered list. The two are disjoint by
// construction (accumulate), and neither is ever retired for converging — a user's removal
// of a contributed entry has to survive the pack being dropped and re-added.
type ListRecord struct {
	Add    []any `json:"add"`
	Remove []any `json:"remove"`
}

// empty reports whether the record changes nothing.
func (r ListRecord) empty() bool { return len(r.Add) == 0 && len(r.Remove) == 0 }

// accumulate folds one boot's per-entry delta into the record, symmetrically: an entry
// added leaves Remove and joins Add, an entry removed leaves Add and joins Remove. It never
// mutates the receiver's slices.
func (r ListRecord) accumulate(added, removed []any) ListRecord {
	out := ListRecord{Add: append([]any(nil), r.Add...), Remove: append([]any(nil), r.Remove...)}
	for _, e := range added {
		out.Remove = withoutEntry(out.Remove, e)
		if !containsEntry(out.Add, e) {
			out.Add = append(out.Add, e)
		}
	}
	for _, e := range removed {
		out.Add = withoutEntry(out.Add, e)
		if !containsEntry(out.Remove, e) {
			out.Remove = append(out.Remove, e)
		}
	}
	return out
}

// ParseListCapture decodes the list-capture sidecar into pointer → record. FAIL-SAFE: an
// absent, empty or corrupt sidecar reads as nil (no list paths learned from it), an entry
// whose key is not a non-root RFC 6901 pointer is dropped, and a null element is dropped.
// Losing the sidecar costs per-entry history, never a file: the next render recaptures
// from the last render forward.
func ParseListCapture(data []byte) map[string]ListRecord {
	var raw map[string]ListRecord
	if len(data) == 0 || json.Unmarshal(data, &raw) != nil {
		return nil
	}
	out := make(map[string]ListRecord, len(raw))
	for p, rec := range raw {
		if tokens, err := jsonptr.Parse(p); err != nil || len(tokens) == 0 {
			continue
		}
		out[p] = ListRecord{Add: dropNilEntries(rec.Add), Remove: dropNilEntries(rec.Remove)}
	}
	return out
}

// marshalListCapture renders the sidecar: one entry per path in paths, sorted by key
// (encoding/json), both arrays always present so a reader never has to tell nil from [].
func marshalListCapture(paths []string, records map[string]ListRecord) ([]byte, error) {
	out := make(map[string]ListRecord, len(paths))
	for _, p := range paths {
		rec := records[p]
		if rec.Add == nil {
			rec.Add = []any{}
		}
		if rec.Remove == nil {
			rec.Remove = []any{}
		}
		out[p] = rec
	}
	return json.MarshalIndent(out, "", "  ")
}

// ListCaptureEntryCount is how many captured list entries a list-capture sidecar holds
// (adds plus removes over every path), for the boot notice and the CLI counts. 0 for an
// absent or corrupt sidecar.
func ListCaptureEntryCount(data []byte) int {
	n := 0
	for _, rec := range ParseListCapture(data) {
		n += len(rec.Add) + len(rec.Remove)
	}
	return n
}

// ListCaptureRecords is ParseListCapture sorted by path, for a reader that prints them.
func ListCaptureRecords(data []byte) ([]string, map[string]ListRecord) {
	recs := ParseListCapture(data)
	paths := make([]string, 0, len(recs))
	for p := range recs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, recs
}

// ListCaptureRefusal is the launch refusal OQ-AL1 rules for a list path that does not
// capture per entry yet — "" when the mechanism does, else the reason, naming the surface
// and its mode. Keyed on the RESOLVED mechanism (render.ModeSet.Mechanism), never on the
// declared mode alone: a `stateful` surface at the host under `assert` renders through
// `rmw`, and that is the mechanism whose capture decides.
//
// The three composing mechanisms all capture per entry now — `stateful` through the
// list-capture sidecar, `rmw` through the insert record, `computed` trivially (it captures
// nothing, so a dropped pack's entries vanish on the next render). What remains refused:
//
//   - a KEYLESS surface (raw/lines). Its capture is the whole file, and it has no keys for a
//     pointer to address, so there is no per-entry capture it could ever have.
//   - any mechanism this table does not name. A new one starts refused, so adding it is the
//     moment someone has to say how it captures a list path.
//
// Never composed into a whole-array capture that would freeze the list: the caller refuses
// the surface (fatal at boot under A12, a `refused:` row at the host).
func ListCaptureRefusal(mechanism string, s manifest.Surface) string {
	if s.Kind() != codec.KindObject {
		return fmt.Sprintf("config-list on %s/%s (mode %s, codec %s): a keyless surface has no "+
			"keys for a list path to address and captures the whole file, never per entry — "+
			"refusing rather than composing into a capture that would freeze the list",
			s.Agent, s.Name, s.ResolvedMode(), s.Codec)
	}
	switch mechanism {
	case manifest.ModeStateful, manifest.ModeComputed, manifest.ModeRMW:
		return ""
	}
	return fmt.Sprintf("config-list on %s/%s (mode %s, rendered through %q here): this "+
		"mechanism does not capture a list path per entry — refusing rather than composing "+
		"into a capture that would freeze the list", s.Agent, s.Name, s.ResolvedMode(), mechanism)
}

// ListPaths returns the distinct pointers the contributions target, sorted.
func ListPaths(lists []ListContribution) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		if !seen[l.Path] {
			seen[l.Path] = true
			out = append(out, l.Path)
		}
	}
	sort.Strings(out)
	return out
}

// ContributedEntries is the ordered, de-duplicated union of every entry the contributions
// add at one path — pack order, then declaration order, first occurrence wins.
func ContributedEntries(lists []ListContribution, path string) []any {
	var out []any
	for _, l := range lists {
		if l.Path != path {
			continue
		}
		for _, e := range l.Add {
			if !containsEntry(out, e) {
				out = append(out, e)
			}
		}
	}
	return out
}

// ListPathReplacedBy reports whether a layer replaces the value at pointer wholesale: it
// holds the path itself (any value — an object patch over an array discards the array,
// RFC 7386), or an ancestor as a non-object. It is the narrowing predicate for a list
// record — a record under such a layer is provably dead — and the ReplacedBy test for
// ListProvenance. An ancestor held as an OBJECT merges, so it replaces nothing below it.
func ListPathReplacedBy(layer any, pointer string) bool {
	tokens, err := jsonptr.Parse(pointer)
	if err != nil || len(tokens) == 0 {
		return false
	}
	return replacesPath(layer, tokens)
}

func replacesPath(layer any, tokens []string) bool { return replacesPathDepth(layer, tokens) >= 0 }

// replacesPathDepth is replacesPath's answer with WHERE: the index of the token at which the
// layer replaces the path (the path itself, or the shallowest non-object ancestor), or -1
// when it replaces nothing there. tokens[:depth+1] is then the layer entry to remove to stop
// the replacement.
func replacesPathDepth(layer any, tokens []string) int {
	node, ok := layer.(map[string]any)
	if !ok {
		return -1
	}
	for i, tok := range tokens {
		v, present := node[tok]
		if !present {
			return -1
		}
		if i == len(tokens)-1 {
			return i
		}
		sub, isObj := v.(map[string]any)
		if !isObj {
			return i
		}
		node = sub
	}
	return -1
}

// ── element identity ────────────────────────────────────────────────────────────────────

// entryKey is an entry's identity: canonical JSON (sorted keys) of its normalized value.
func entryKey(v any) string {
	b, err := json.Marshal(normalizeEntry(v))
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(b)
}

// normalizeEntry lands every decoder's value model in one: jsonx ordered maps and integer
// literals through jsonx.Plain, every Go integer kind to float64 (what a pack.json decode
// produces), recursively.
func normalizeEntry(v any) any {
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		return normalizeEntry(jsonx.PlainMap(t))
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = normalizeEntry(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeEntry(e)
		}
		return out
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint())
	case reflect.Float32:
		return rv.Float()
	}
	return jsonx.Plain(v)
}

// EntriesEqual reports whether two list entries are the same entry under the element rule.
func EntriesEqual(a, b any) bool { return entryKey(a) == entryKey(b) }

func containsEntry(list []any, e any) bool {
	k := entryKey(e)
	for _, x := range list {
		if entryKey(x) == k {
			return true
		}
	}
	return false
}

// withoutEntry returns list minus every occurrence of e, as a new slice.
func withoutEntry(list []any, e any) []any {
	k := entryKey(e)
	out := make([]any, 0, len(list))
	for _, x := range list {
		if entryKey(x) != k {
			out = append(out, x)
		}
	}
	return out
}

// subtractEntries is a − b by whole-value presence, order-preserving and de-duplicated.
func subtractEntries(a, b []any) []any {
	var out []any
	for _, e := range a {
		if !containsEntry(b, e) && !containsEntry(out, e) {
			out = append(out, e)
		}
	}
	return out
}

func dropNilEntries(list []any) []any {
	out := make([]any, 0, len(list))
	for _, e := range list {
		if e != nil {
			out = append(out, e)
		}
	}
	return out
}

// ── paths over the plain value model ───────────────────────────────────────────────────

type pathState int

const (
	pathAbsent  pathState = iota // a step is missing; depth is the first missing step
	pathFound                    // the leaf exists; v is its value
	pathBlocked                  // an ancestor at depth is not an object; v is that value
)

// walkPath resolves tokens (all object keys) in root.
func walkPath(root map[string]any, tokens []string) (v any, st pathState, depth int) {
	node := root
	for i, tok := range tokens {
		val, ok := node[tok]
		if !ok {
			return nil, pathAbsent, i
		}
		if i == len(tokens)-1 {
			return val, pathFound, i
		}
		sub, isObj := val.(map[string]any)
		if !isObj {
			return val, pathBlocked, i
		}
		node = sub
	}
	return nil, pathAbsent, 0
}

// setPath returns root with value at tokens, copying every map along the path (layer values
// are shared by reference through the fold, so writing in place would edit a declared
// layer) and creating missing parents as {}. The caller has already ruled out a non-object
// ancestor.
func setPath(root map[string]any, tokens []string, value any) map[string]any {
	out := make(map[string]any, len(root)+1)
	for k, v := range root {
		out[k] = v
	}
	if len(tokens) == 1 {
		out[tokens[0]] = value
		return out
	}
	child, _ := root[tokens[0]].(map[string]any)
	out[tokens[0]] = setPath(child, tokens[1:], value)
	return out
}

// deletePath returns root without the leaf at tokens, copying along the path. An ancestor
// the deletion leaves EMPTY is removed too when prune says so for its prefix; an ancestor
// that was already empty is never touched (it is a value somebody wrote).
func deletePath(root map[string]any, tokens []string, prune func(prefix []string) bool) map[string]any {
	if len(tokens) == 0 {
		return root
	}
	cur, ok := root[tokens[0]]
	if !ok {
		return root
	}
	out := make(map[string]any, len(root))
	for k, v := range root {
		out[k] = v
	}
	if len(tokens) == 1 {
		delete(out, tokens[0])
		return out
	}
	child, isObj := cur.(map[string]any)
	if !isObj {
		return root
	}
	head := tokens[0]
	sub := deletePath(child, tokens[1:], func(p []string) bool {
		return prune(append([]string{head}, p...))
	})
	if len(sub) == 0 && len(child) > 0 && prune([]string{head}) {
		delete(out, head)
	} else {
		out[head] = sub
	}
	return out
}

// pruneAlways prunes every ancestor a deletion empties.
func pruneAlways([]string) bool { return true }

// jsonTypeName names a decoded value's JSON type for a refusal message.
func jsonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any, *jsonx.OrderedMap:
		return "an object"
	case []any:
		return "an array"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	}
	return "a number"
}

// ── the fold ────────────────────────────────────────────────────────────────────────────

// listTrace accumulates one list path's per-entry account while Compose folds.
type listTrace struct {
	tokens     []string
	entries    []ListEntry
	replacedBy string
}

func (t *listTrace) removeEntry(e any) {
	k := entryKey(e)
	kept := t.entries[:0:0]
	for _, x := range t.entries {
		if entryKey(x.Value) != k {
			kept = append(kept, x)
		}
	}
	t.entries = kept
}

func newTrace(tokens []string, base []any) *listTrace {
	t := &listTrace{tokens: tokens}
	for _, e := range base {
		t.entries = append(t.entries, ListEntry{Value: e, Source: ListSourceBase})
	}
	return t
}

// applyListContributions folds the contributions into the value the ordinary layers
// produced (rule 1): each contribution in order appends the first occurrence of every entry
// the array does not already hold. A missing path acts as [] with missing parents created;
// a non-object parent or a non-array value REFUSES the render, naming the surface, the path
// and the pack (rule 3) — never overwritten, never skipped. An empty `add` is a no-op and
// creates nothing.
//
// Returns the new value, the top-level keys the contributions CREATED (for the
// `config-list` provenance label), and the per-path traces.
func applyListContributions(s manifest.Surface, merged map[string]any, lists []ListContribution,
	traces map[string]*listTrace) (map[string]any, []string, error) {
	var created []string
	for _, l := range lists {
		if len(l.Add) == 0 {
			continue
		}
		tokens, err := jsonptr.Parse(l.Path)
		if err != nil || len(tokens) == 0 {
			return nil, nil, fmt.Errorf("agentcfg: surface %s/%s: config-list from pack %s: "+
				"path %q is not a pointer into the surface", s.Agent, s.Name, l.Pack, l.Path)
		}
		v, st, depth := walkPath(merged, tokens)
		var arr []any
		switch st {
		case pathBlocked:
			return nil, nil, fmt.Errorf("agentcfg: surface %s/%s: config-list from pack %s at %s: "+
				"%s holds %s, not an object — refusing this surface's render rather than "+
				"overwriting or skipping the conflicting value",
				s.Agent, s.Name, l.Pack, l.Path, jsonptr.Format(tokens[:depth+1]), jsonTypeName(v))
		case pathFound:
			a, isArr := v.([]any)
			if !isArr {
				return nil, nil, fmt.Errorf("agentcfg: surface %s/%s: config-list from pack %s "+
					"at %s: the value there is %s, not an array — refusing this surface's "+
					"render rather than overwriting or skipping the conflicting value",
					s.Agent, s.Name, l.Pack, l.Path, jsonTypeName(v))
			}
			arr = a
		case pathAbsent:
			if depth == 0 {
				created = append(created, tokens[0])
			}
		}
		tr := traces[l.Path]
		if tr == nil {
			tr = newTrace(tokens, arr)
			traces[l.Path] = tr
		}
		next := append([]any(nil), arr...)
		changed := st == pathAbsent
		for _, e := range l.Add {
			if containsEntry(next, e) {
				continue
			}
			next = append(next, e)
			tr.entries = append(tr.entries, ListEntry{Value: e, Source: ListContributionLayer(l.Pack)})
			changed = true
		}
		if changed {
			merged = setPath(merged, tokens, next)
		}
	}
	return merged, created, nil
}

// applyListRecords applies the per-entry capture on top of the capture overlay: at each
// recorded path, remove the recorded removals (every occurrence), then append the recorded
// additions the array does not hold. A path the capture OVERLAY holds wholesale (a captured
// deletion or a non-array edit, under which the record is kept but inert) is left to the
// overlay; so is one whose value is not an array. A missing path is created only for a record
// that adds something.
//
// Returns the new value and the top-level keys of every path a record APPLIED at — created
// or changed alike. Compose labels those keys `overlay`: an applied record is the user's
// captured edit, which is what the whole-array capture's label said before this operation
// existed, and a lower layer's label there (`defaults`, `config-overlay:<pack>`) would tell a
// host revert that yolo wrote the whole array and may delete it.
func applyListRecords(merged map[string]any, records map[string]ListRecord, overlay any,
	traces map[string]*listTrace) (map[string]any, []string) {
	paths := make([]string, 0, len(records))
	for p := range records {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var applied []string
	for _, p := range paths {
		rec := records[p]
		if rec.empty() {
			continue
		}
		tokens, err := jsonptr.Parse(p)
		if err != nil || len(tokens) == 0 {
			continue
		}
		if replacesPath(overlay, tokens) {
			continue
		}
		v, st, _ := walkPath(merged, tokens)
		var arr []any
		switch st {
		case pathBlocked:
			continue
		case pathFound:
			a, isArr := v.([]any)
			if !isArr {
				continue
			}
			arr = a
		case pathAbsent:
			if len(rec.Add) == 0 {
				continue
			}
		}
		applied = append(applied, tokens[0])
		tr := traces[p]
		if tr == nil {
			tr = newTrace(tokens, arr)
			traces[p] = tr
		}
		next := append([]any(nil), arr...)
		for _, e := range rec.Remove {
			if containsEntry(next, e) {
				next = withoutEntry(next, e)
				tr.removeEntry(e)
			}
		}
		for _, e := range rec.Add {
			if !containsEntry(next, e) {
				next = append(next, e)
				tr.entries = append(tr.entries, ListEntry{Value: e, Source: ListSourceCaptured})
			}
		}
		merged = setPath(merged, tokens, next)
	}
	return merged, applied
}

// listProvenance renders the traces as Result.Lists, sorted by path.
func listProvenance(traces map[string]*listTrace) []ListProvenance {
	if len(traces) == 0 {
		return nil
	}
	paths := make([]string, 0, len(traces))
	for p := range traces {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]ListProvenance, 0, len(paths))
	for _, p := range paths {
		t := traces[p]
		out = append(out, ListProvenance{Path: p, Entries: t.entries, ReplacedBy: t.replacedBy})
	}
	return out
}

// ── rmw: yolo's own record of what it inserted ──────────────────────────────────────────

// ListInsertRecord is the `rmw` equivalent of a list capture, at one path: the entries yolo
// INSERTED into the file, and the entries it inserted that the file later stopped holding
// (DECLINED — the user removed them, so they are never re-added). The record persists; an
// unreadable one claims nothing (ParseListInsertRecords).
type ListInsertRecord struct {
	Inserted []any `json:"inserted"`
	Declined []any `json:"declined"`
	// Suspended marks a record carried over a render that did NOT reconcile the path — managed
	// or a dynamic table held it, or a stale path held a conflicting value — so the file's
	// array at the path was yolo's (or nobody's) write, not the user's edit. The next render
	// that reconciles it therefore reads the file as NO evidence of a removal (beforePresent
	// false): an inserted entry a managed array displaced was not declined by anybody.
	Suspended bool `json:"suspended,omitempty"`
}

// ParseListInsertRecords decodes the rmw list record, FAIL-SAFE like the provenance record:
// absent, empty or corrupt reads as nil, so nothing is claimed — every entry in the file
// then reads as the user's, and none is removed.
func ParseListInsertRecords(data []byte) map[string]ListInsertRecord {
	var raw map[string]ListInsertRecord
	if len(data) == 0 || json.Unmarshal(data, &raw) != nil {
		return nil
	}
	out := make(map[string]ListInsertRecord, len(raw))
	for p, rec := range raw {
		if tokens, err := jsonptr.Parse(p); err != nil || len(tokens) == 0 {
			continue
		}
		out[p] = ListInsertRecord{Inserted: dropNilEntries(rec.Inserted), Declined: dropNilEntries(rec.Declined),
			Suspended: rec.Suspended}
	}
	return out
}

// MarshalListInsertRecords renders the record for the given paths, sorted, both arrays
// always present.
func MarshalListInsertRecords(paths []string, recs map[string]ListInsertRecord) ([]byte, error) {
	out := make(map[string]ListInsertRecord, len(paths))
	for _, p := range paths {
		r := recs[p]
		if r.Inserted == nil {
			r.Inserted = []any{}
		}
		if r.Declined == nil {
			r.Declined = []any{}
		}
		out[p] = r
	}
	return json.MarshalIndent(out, "", "  ")
}

// ReconcileInsertedList is the rmw per-entry step at one path.
//
//	before         the file's array at the path BEFORE this render (beforePresent=false
//	               when the file did not hold the key — no evidence of a removal, so
//	               nothing is declined for it: a deleted file must not decline every
//	               pack's entries forever)
//	current        the array after this render's ordinary layers were written
//	contributed    ContributedEntries for the path (empty for a dropped pack's path)
//
// Rules, in order: an inserted entry `before` no longer holds was removed by the user and
// moves to Declined; an inserted entry no longer contributed is removed from the file; a
// contributed entry that is declined is skipped; one already present but not recorded is
// the user's (or another layer's) and stays unrecorded; one that is absent is appended and
// recorded. Declined persists.
//
// Returns the new array and record, and whether the array changed.
func ReconcileInsertedList(before []any, beforePresent bool, current, contributed []any,
	rec ListInsertRecord) ([]any, ListInsertRecord, bool) {
	declined := append([]any(nil), rec.Declined...)
	next := append([]any(nil), current...)
	changed := false
	for _, e := range rec.Inserted {
		if beforePresent && !containsEntry(before, e) {
			if !containsEntry(declined, e) {
				declined = append(declined, e)
			}
			continue
		}
		if !containsEntry(contributed, e) && containsEntry(next, e) {
			next = withoutEntry(next, e)
			changed = true
		}
	}
	var inserted []any
	for _, e := range contributed {
		if containsEntry(declined, e) {
			continue
		}
		if containsEntry(next, e) {
			if containsEntry(rec.Inserted, e) && !containsEntry(inserted, e) {
				inserted = append(inserted, e)
			}
			continue
		}
		next = append(next, e)
		inserted = append(inserted, e)
		changed = true
	}
	return next, ListInsertRecord{Inserted: inserted, Declined: declined}, changed
}

// InsertRecordFromRender derives the insert record a STATEFUL render implies, for a caller
// that keeps one beside a stateful surface (the host under `own`, so switching to `assert`
// finds what yolo inserted): per list path of the list capture — every path, the sidecar
// naming each one — Inserted is the entries the render took from a contribution and the
// user's record does not also add, and Declined is the user's recorded removals. A path a
// higher layer replaced is SUSPENDED, the file holding that layer's array rather than the
// assembled one. nil when the surface has no list path.
func InsertRecordFromRender(res *Result, listCaptureJSON []byte) ([]byte, error) {
	recs := ParseListCapture(listCaptureJSON)
	if len(recs) == 0 || res == nil {
		return nil, nil
	}
	byPath := map[string]ListProvenance{}
	for _, lp := range res.Lists {
		byPath[lp.Path] = lp
	}
	paths := make([]string, 0, len(recs))
	out := make(map[string]ListInsertRecord, len(recs))
	for p, rec := range recs {
		paths = append(paths, p)
		lp := byPath[p]
		var inserted []any
		for _, e := range lp.Entries {
			if e.Source != ListSourceBase && e.Source != ListSourceCaptured &&
				!containsEntry(rec.Add, e.Value) && !containsEntry(inserted, e.Value) {
				inserted = append(inserted, e.Value)
			}
		}
		out[p] = ListInsertRecord{Inserted: inserted, Declined: rec.Remove, Suspended: lp.ReplacedBy != ""}
	}
	sort.Strings(paths)
	return MarshalListInsertRecords(paths, out)
}
