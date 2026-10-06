package entrypoint

// hostleafrecord.go is the HOST'S COMPUTED-LEAF RECORD and the clear it enables
// (docs/design/host-computed-layer.md HC-D25, revising HC-D10 rule 4): `yolo host apply` removes a
// leaf its derive asserted into a real file once the derive stops asserting it, when the file
// still holds exactly what yolo wrote there.
//
// WHY. At the host the rmw arm writes a derive's leaves into the user's own file (HC-D10 rule 2),
// and the file is re-read as the next apply's input, so a leaf yolo stopped asserting used to stay
// forever: rule 4 said so, and named ENABLE_LSP_TOOL as its harmless example. CLAUDE_CODE_USE_BEDROCK
// is not harmless. claude's settings derive asserts it while claude is on a Bedrock provider
// (providers.md#pv-d8), so `yolo host apply` on `bedrock` wrote it into ~/.claude/settings.json;
// moving claude to `codex` and applying again left it there, every later launch composed it as the
// user's host layer, and claude ran in Bedrock mode with no AWS credential while PP-D1's line
// blamed the user for a key yolo wrote. The ruling's own premise, "yolo deletes nothing it did not
// write", is the rule this keeps from both sides: what yolo wrote, and only that, it clears.
//
// THE RECORD IS THE AUTHORITY, at leaf grain. The provenance record is per top-level key, and
// `env` reads `computed` whenever any leaf under it is yolo's, so it cannot say which variable
// under `env` yolo wrote. So each asserted leaf is recorded with the value yolo wrote, and only
// when yolo's write is what put it there: a leaf the file already held with exactly that value,
// with no record of yolo's, is the user's, and is never recorded and never cleared. Per leaf:
//
//	asserted, file lacks it or differs     yolo writes it: record the value
//	asserted, file equals it, recorded      still yolo's: keep the record
//	asserted, file equals it, not recorded  the user's own value: record nothing
//	not asserted, file equals the record    yolo's, unedited: CLEAR it, forget it
//	not asserted, file differs or lacks it  the user changed or removed it: forget it
//
// A clear removes the leaf alone, from the file's own content BEFORE any layer writes
// (applyRMWLayers), so a live layer still asserting the same path, another pack's config-overlay
// say, sets it again and the clear takes back only yolo's stale write. Its parent object stays,
// even when the clear empties it, since the user may have written the parent themselves and an
// empty object is inert. The selection
// namespace keeps its own record and its own edge (hostRMWSelection); this is the record of every
// OTHER leaf.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// readHostLeafRecord is one surface's computed-leaf record, nil when there is none.
func readHostLeafRecord(e *Env, agent, name string) map[string]any {
	path := e.renderTarget().LeafRecordPath(agent, name)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return agentcfg.ParseLeafRecord(data)
}

// writeHostLeafRecord persists record, removing the file when it is empty: a surface with no leaf
// of yolo's in it keeps no record.
func writeHostLeafRecord(e *Env, agent, name string, record map[string]any) {
	path := e.renderTarget().LeafRecordPath(agent, name)
	if path == "" {
		return
	}
	if len(record) == 0 {
		_ = os.Remove(path)
		return
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		e.warn("warning: could not encode the computed-leaf record for " + agent + "/" + name +
			": " + err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.warn("warning: could not create the directory for the computed-leaf record of " +
			agent + "/" + name + ": " + err.Error())
		return
	}
	if err := writeSidecar(e.renderTarget(), path, string(data)+"\n"); err != nil {
		e.warn("warning: could not write the computed-leaf record for " + agent + "/" + name +
			": " + err.Error())
	}
}

// flattenLeaves is every non-object value under leaves, keyed by its pointer. An array is a leaf,
// replaced whole, as the rmw arm writes it.
func flattenLeaves(leaves map[string]any) map[string]any {
	out := map[string]any{}
	var walk func(m map[string]any, path []string)
	walk = func(m map[string]any, path []string) {
		for k, v := range m {
			at := append(append([]string(nil), path...), k)
			if sub, isMap := v.(map[string]any); isMap {
				walk(sub, at)
				continue
			}
			out[jsonptr.Format(at)] = v
		}
	}
	walk(leaves, nil)
	return out
}

// leafAt is the value doc holds at pointer, and whether it holds one.
func leafAt(doc map[string]any, pointer string) (any, bool) {
	steps, err := jsonptr.Parse(pointer)
	if err != nil || len(steps) == 0 {
		return nil, false
	}
	var cur any = doc
	for _, step := range steps {
		m, isMap := cur.(map[string]any)
		if !isMap {
			return nil, false
		}
		v, ok := m[step]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// hostRMWLeafRecord decides the computed-leaf record for one rmw write at the host: the pointers
// to clear, sorted, and the record to persist after the write. leaves are the derive's asserted
// leaves (hostLayer.leaves, before the selection lift, which has a record of its own); the file is
// read as it stands. touched is false when there is nothing to record and nothing recorded, so
// the caller writes no record at all.
func hostRMWLeafRecord(e *Env, s manifest.Surface, path string, leaves map[string]any) (
	clears []string, next map[string]any, touched bool) {
	record := readHostLeafRecord(e, s.Agent, s.Name)
	asserted := flattenLeaves(leaves)
	if len(asserted) == 0 && len(record) == 0 {
		return nil, nil, false
	}
	data, _ := os.ReadFile(path)
	file := agentcfg.DecodeSurfaceObject(s.Codec, data)
	next = map[string]any{}
	for p, v := range asserted {
		cur, inFile := leafAt(file, p)
		wrote, recorded := record[p]
		switch {
		case !inFile || !sameJSON(cur, v):
			next[p] = v
		case recorded && sameJSON(wrote, v):
			next[p] = v
		}
	}
	for p, wrote := range record {
		if _, still := asserted[p]; still {
			continue
		}
		if cur, inFile := leafAt(file, p); inFile && sameJSON(cur, wrote) {
			clears = append(clears, p)
		}
	}
	sort.Strings(clears)
	return clears, next, true
}

// hostStatefulLeafRecord decides the computed-leaf record for one `stateful` write at an OWNED
// host, from the file before the write (before), the record before it (record) and the file
// the write left (after): each leaf the derive asserted that the write landed is recorded, unless
// the file already held that value before and no record claimed it — a value the user wrote
// before yolo asserted it stays the user's (PP-D1), as in the rmw arm. A leaf the derive no longer
// asserts drops out: the composition regenerated the file without it, so there is nothing of
// yolo's left at that pointer to name. touched is false when there is nothing to record and
// nothing recorded, so the caller writes no record at all.
//
// It is what makes the record mean the same thing under both writers. The rmw arm always kept
// it; the stateful arm wrote none until 2026-10-05 (CO-D15), so a Bedrock switch `own` wrote into
// claude's settings — composed whole — was reported at another provider's launch as the user's
// own, and `--revert` had no per-value record to withdraw it by.
func hostStatefulLeafRecord(leaves, before, after, record map[string]any) (next map[string]any, touched bool) {
	asserted := flattenLeaves(leaves)
	if len(asserted) == 0 && len(record) == 0 {
		return nil, false
	}
	next = map[string]any{}
	for p, v := range asserted {
		cur, landed := leafAt(after, p)
		if !landed || !sameJSON(cur, v) {
			continue // outranked (a captured edit of the user's) or not written: not yolo's value
		}
		pre, inBefore := leafAt(before, p)
		wrote, recorded := record[p]
		if !inBefore || !sameJSON(pre, v) || (recorded && sameJSON(wrote, v)) {
			next[p] = v
		}
	}
	return next, true
}

// deleteLeaf removes the value obj holds at pointer, leaving its parent in place. A pointer obj
// does not reach removes nothing.
func deleteLeaf(obj *jsonx.OrderedMap, pointer string) {
	steps, err := jsonptr.Parse(pointer)
	if err != nil || len(steps) == 0 {
		return
	}
	cur := obj
	for _, step := range steps[:len(steps)-1] {
		v, ok := cur.Get(step)
		if !ok {
			return
		}
		sub, isMap := v.(*jsonx.OrderedMap)
		if !isMap {
			return
		}
		cur = sub
	}
	cur.Delete(steps[len(steps)-1])
}
