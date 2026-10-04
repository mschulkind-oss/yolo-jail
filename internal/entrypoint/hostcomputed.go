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
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
// selection and the first activation outranks it — the jail's rule for a host value (HC-D17).
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

// overwriteLeaves is the computed layer's LEAVES as the write lands them, for the overwrite
// report: the derive's leaves plus the selection keys, at the root where both mechanisms put
// them. lift is the rmw arm's edge-decided selection (hostRMWSelection); the stateful arm
// passes the derive's whole selection namespace, and its composition decides which of those
// keys move. The wholesale tables are not here: a table's entries are reported by name, as
// EntryLosses, never as overwritten values.
func (h hostLayer) overwriteLeaves(lift map[string]any) map[string]any {
	if len(h.leaves) == 0 && len(lift) == 0 {
		return nil
	}
	return mergeSurfaceRoot(h.leaves, lift)
}

// hostLeafInputs are the user-scope inputs a host derive reads, in the order a label names
// them: the `profile` selection, then the three tables (HostInputs).
var hostLeafInputs = []string{"profile", manifest.SourceProviders, manifest.SourceLSPServers,
	manifest.SourceMCPServers}

// hostLeafAttribution answers, for a computed leaf this render overwrites a value of the
// user's with, WHICH OF THEIR OWN INPUTS it is computed from — the label the overwrite report
// carries, and so the input the remedy names.
//
// It ASKS THE DERIVE rather than keeping a key-to-input table: each input is taken away in
// turn and the derive re-run over the rest, and an input whose absence changes the leaf is one
// it is computed from. A table core kept would be a list of vendor keys (claude's
// `env.ENABLE_LSP_TOOL` comes from lsp_servers, pi's `defaultModel` from profile), which is
// exactly what core does not know; the derive is the one authority on its own output. The
// re-runs are lazy and memoized, so a render that overwrites no computed leaf runs none.
type hostLeafAttribution struct {
	e      *Env
	s      manifest.Surface
	script string
	sel    surfaceSelection
	tables map[string]map[string]any
	base   map[string]any
	// without is the derive's output with one input taken away, by input name; nil for an
	// input that is empty already (taking it away changes nothing) or whose re-run failed.
	without map[string]map[string]any
	ran     map[string]bool
}

func newHostLeafAttribution(e *Env, s manifest.Surface, script string, sel surfaceSelection,
	tables map[string]map[string]any, derived map[string]any) *hostLeafAttribution {
	return &hostLeafAttribution{e: e, s: s, script: script, sel: sel, tables: tables,
		base: derived, without: map[string]map[string]any{}, ran: map[string]bool{}}
}

// rerun is the derive's output without input, and whether there is one to compare.
func (a *hostLeafAttribution) rerun(input string) (map[string]any, bool) {
	if a.ran[input] {
		out, ok := a.without[input]
		return out, ok
	}
	a.ran[input] = true
	sel, tables := a.sel, a.tables
	if input == "profile" {
		if sel.Profile == "" && sel.Provider == "" && len(sel.ActiveSet) == 0 {
			return nil, false
		}
		// No profile at this agent's CLI name, as the selection reads when the user selects
		// none: the agent's own built-in source stays, being no input of theirs.
		sel = surfaceSelection{NativeCapabilities: a.sel.NativeCapabilities}
	} else {
		if len(tables[input]) == 0 {
			return nil, false
		}
		tables = make(map[string]map[string]any, len(a.tables))
		for k, v := range a.tables {
			tables[k] = v
		}
		tables[input] = map[string]any{}
	}
	out, _, err := deriveComputedLayer(a.e, a.s, a.script, sel, tables)
	if err != nil {
		return nil, false
	}
	a.without[input] = out
	return out, true
}

// label is the overwrite line for the leaf at path: "<key> (computed from your <inputs>)", or
// "<key> (computed by its pack)" for a leaf no input of the user's moves.
func (a *hostLeafAttribution) label(path []string) string {
	key := strings.Join(path, ".")
	base, _ := derivedLeaf(a.base, path)
	var from []string
	for _, input := range hostLeafInputs {
		out, ok := a.rerun(input)
		if !ok {
			continue
		}
		if v, has := derivedLeaf(out, path); !has || !sameJSON(v, base) {
			from = append(from, input)
		}
	}
	// A PROFILE RESOLVES OVER THE PROVIDER TABLE, so a value the profile moves always moves with
	// the table too: naming both would send the reader to two keys for one choice. The table is
	// named only for a leaf no profile explains (a menu built from every provider, say).
	if len(from) > 1 && from[0] == "profile" && from[1] == manifest.SourceProviders {
		from = append(from[:1], from[2:]...)
	}
	if len(from) == 0 {
		return key + ComputedByPackLabel
	}
	return key + ComputedFromLabel + joinInputs(from) + ")"
}

// The two computed-overwrite label forms, exported so the CLI's report reads the same
// spelling the render writes (splitOverwriteLabel) rather than a copy of it.
const (
	ComputedFromLabel   = " (computed from your "
	ComputedByPackLabel = " (computed by its pack)"
)

// joinInputs names inputs as a label does: "profile", "profile and providers",
// "profile, providers and lsp_servers".
func joinInputs(in []string) string {
	switch len(in) {
	case 0:
		return ""
	case 1:
		return in[0]
	}
	return strings.Join(in[:len(in)-1], ", ") + " and " + in[len(in)-1]
}

// derivedLeaf is the value at path in a derive's output: a top-level key the selection
// namespace carries is read there, where the write lifts it from; anything else is read from
// the rest of the output.
func derivedLeaf(derived map[string]any, path []string) (any, bool) {
	rest, selection, _ := agentcfg.TakeSelection(derived)
	if len(path) == 1 {
		if v, ok := selection[path[0]]; ok {
			return v, true
		}
	}
	return valueAtPath(stripNils(rest), path)
}

// computedOverwritePaths are the computed leaves whose value differs from the file's, as key
// segments, minus every key managed asserts (managed outranks computed, and managedOverwrites
// has already named it).
func computedOverwritePaths(existing *jsonx.OrderedMap, leaves, managed map[string]any) [][]string {
	if len(leaves) == 0 {
		return nil
	}
	var paths [][]string
	collectOverwritePaths(existing, leaves, nil, &paths)
	out := paths[:0]
	for _, p := range paths {
		if layerAssertsPath(managed, strings.Join(p, ".")) {
			continue
		}
		out = append(out, p)
	}
	return out
}
