package entrypoint

// hostcomputed.go turns one surface's derive output at the host into the layers each host
// mechanism writes, by the per-key landing of HC-D10 (docs/design/host-computed-layer.md §6.4):
//
//  1. A table the derive declares IN FULL (ctx.in_full) is yolo's, written wholesale — with the
//     entries the pack's declared layers put in it (overlays below the derive, managed above,
//     the §5 precedence config-overlay < computed < managed, per entry).
//  2. Any other object ASSERTS ONLY ITS LEAVES, and so does every non-object value: the rest of
//     what the file holds under that key stays. claude/settings' `env` is the shipped case.
//  3. A TOMBSTONE IS DROPPED before either arm sees it. At the host the only layer below a
//     derive is the user's real file, so a tombstone could only delete a key of theirs
//     (claude/settings' `mcpServers` is the shipped one).
//
// Rule 2 is an obligation on the `rmw` arm, whose computed write (regenerateManagedTables)
// clears and rewrites EVERY object it is handed; so the leaves reach that arm through
// surfaceContribs.hostLeaves, applied leaf by leaf after the tables, and only the tables reach
// regenerateManagedTables. The `stateful` arm reads the in-full declaration already, so it is
// handed tables and leaves in one computed map with the tables named in inFull.

import (
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// hostLayer is one surface's computed layer at the host, split by how it lands.
type hostLayer struct {
	// tables are the wholesale tables, keyed by top-level key: the derive's in-full tables
	// and the key-name probe's (hostTableKeys), each holding the declared overlay entries,
	// then the derive's, then managed's.
	tables map[string]any
	// leaves are asserted leaf by leaf: every other key the derive returns, nils stripped at
	// every depth and empty objects pruned (an empty object asserts nothing, and written it
	// would add a key the user never had).
	leaves map[string]any
	// selection is the reserved namespace (agentcfg.SelectionKey) the derive emitted, nil
	// when none. Only the stateful arm applies it itself; the rmw arm's edge-triggered apply
	// is hostRMWSelection's.
	selection map[string]any
}

// empty reports that the derive computed nothing this surface would write.
func (h hostLayer) empty() bool {
	return len(h.tables) == 0 && len(h.leaves) == 0 && h.selection == nil
}

// inFull is the tables' key set: the in-full declaration the stateful arm adopts by.
func (h hostLayer) inFull() []string {
	if len(h.tables) == 0 {
		return nil
	}
	return sortedKeys(h.tables)
}

// statefulComputed is the computed slot the stateful arm composes: tables, leaves and the
// selection namespace in one map, the shape a jail's boot hands the same writer.
func (h hostLayer) statefulComputed() map[string]any {
	if len(h.tables) == 0 && len(h.leaves) == 0 && h.selection == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range h.leaves {
		out[k] = v
	}
	for k, v := range h.tables {
		out[k] = v
	}
	if h.selection != nil {
		out[agentcfg.SelectionKey] = h.selection
	}
	return out
}

// buildHostLayer splits derived (the derive's output, nil for a surface with no producer) by
// HC-D10's rules. tableKeys are the keys the key-name probe claims as tables; inFull is what
// the real derive declared.
func buildHostLayer(s manifest.Surface, derived map[string]any, inFull, tableKeys []string,
	overlays []agentcfg.Overlay) hostLayer {
	rest, selection, _ := agentcfg.TakeSelection(derived)
	isTable := map[string]bool{}
	for _, k := range tableKeys {
		isTable[k] = true
	}
	for _, k := range inFull {
		if _, isObj := rest[k].(map[string]any); isObj && k != agentcfg.SelectionKey {
			isTable[k] = true
		}
	}
	h := hostLayer{selection: selection}
	if len(isTable) > 0 {
		h.tables = map[string]any{}
	}
	for k := range isTable {
		entries := map[string]any{}
		for _, ov := range overlays {
			if layer, isMap := ov.Data.(map[string]any); isMap {
				mergeEntries(entries, layer[k])
			}
		}
		if t, isObj := stripNils(rest[k]).(map[string]any); isObj {
			mergeEntries(entries, t)
		}
		if managed, isMap := s.Managed.(map[string]any); isMap {
			mergeEntries(entries, managed[k])
		}
		h.tables[k] = entries
	}
	for _, k := range sortedKeys(rest) {
		if isTable[k] {
			continue
		}
		v := pruneEmptyObjects(stripNils(rest[k]))
		if v == nil {
			continue
		}
		if h.leaves == nil {
			h.leaves = map[string]any{}
		}
		h.leaves[k] = v
	}
	return h
}

// stripNils deep-copies v without any nil value at any depth: the host drops a tombstone
// before either arm sees it (HC-D10 rule 3). A JSON null would otherwise be written literally
// by the rmw arm, and a TOML one would delete the user's key.
func stripNils(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, sub := range t {
			if sub == nil {
				continue
			}
			out[k] = stripNils(sub)
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			if e == nil {
				continue
			}
			out = append(out, stripNils(e))
		}
		return out
	}
	return v
}

// pruneEmptyObjects drops every object left empty, recursively, and answers nil for a value
// that is nothing but empty objects: a leaf layer asserts only what it names.
func pruneEmptyObjects(v any) any {
	m, isMap := v.(map[string]any)
	if !isMap {
		return v
	}
	out := map[string]any{}
	for k, sub := range m {
		if p := pruneEmptyObjects(sub); p != nil {
			out[k] = p
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// hostSelectionBaseline is the host's OQ-SW1 set for the edge-triggered selection apply: every
// top-level key the file holds that the selection record does not. At the host the real file
// is what a jail's host layer carries, so a value yolo's selection never wrote predates the
// selection and the first activation outranks it — the jail's rule for a host value (HC-D16).
// Once the record holds a key, a value differing from it is the user's own later pick, which
// stands (OQ-PSW2): so a recorded key is never in this set.
func hostSelectionBaseline(file, record map[string]any) map[string]bool {
	var out map[string]bool
	for k := range file {
		if _, recorded := record[k]; recorded {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		out[k] = true
	}
	return out
}

// hostRMWSelection is the edge-triggered selection apply for a surface the host writes through
// `rmw` (OQ-HC3): the lifted keys become leaves, a deselected value yolo wrote is cleared, and
// the record to persist after the write is returned. The same agentcfg.ApplySelectionReport a
// stateful render runs, over the file as it stands and the record beside the provenance record
// (render.Target.SelectionPath).
func hostRMWSelection(e *Env, s manifest.Surface, path string, selection map[string]any) (
	lift map[string]any, clears []string, next map[string]any, touched bool) {
	record := readSelectionRecord(e, s.Agent, s.Name)
	if len(selection) == 0 && len(record) == 0 {
		return nil, nil, nil, false
	}
	data, _ := os.ReadFile(path)
	file := agentcfg.DecodeSurfaceObject(s.Codec, data)
	all, next, cleared := agentcfg.ApplySelectionReport(selection, file, record,
		hostSelectionBaseline(file, record))
	// Only the values the RECORD now holds are written: a lift that keeps the user's own
	// value is already in the file, and asserting it would attribute it to yolo's computed
	// layer, which a revert then removes.
	for k, v := range all {
		if wrote, ok := next[k]; ok && sameJSON(wrote, v) {
			if lift == nil {
				lift = map[string]any{}
			}
			lift[k] = v
		}
	}
	for _, c := range cleared {
		clears = append(clears, c.Key)
	}
	sort.Strings(clears)
	return lift, clears, next, true
}

// lossInNewTable reports whether any loss line ("<table>.<entry> (...)") is in a table the
// previous provenance record does not attribute to a layer yolo asserts — HC-D8's "first apply
// of this table". A nil record is a first apply of the whole surface, which the caller already
// knows; so is a table yolo already owned.
func lossInNewTable(losses []string, previous map[string]string) bool {
	if previous == nil {
		return false
	}
	for _, l := range losses {
		i := strings.Index(l, ".")
		if i <= 0 {
			continue
		}
		layer := previous[l[:i]]
		if last, retired := agentcfg.RetiredOf(layer); retired {
			layer = last
		}
		if !agentcfg.LayerAsserted(layer) {
			return true
		}
	}
	return false
}
