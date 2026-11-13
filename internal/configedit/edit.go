// Package configedit plans byte-preserving edits to human-authored config documents.
// The splice's rules and the commands that use it: docs/reference/config/editing.md.
package configedit

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Action selects the byte splice to plan.
type Action uint8

// Edit actions supported by the splice planner.
const (
	Set Action = iota + 1
	Add
	Append
	Remove
)

// Mutation identifies one key or array path and the value it sets, adds, or appends.
type Mutation struct {
	Action Action
	Path   []configdoc.Step
	Value  any
}

// Hunk records one exact byte-range replacement in the original document.
type Hunk struct {
	Start, End    int
	Before, After []byte
}

// Plan is an immutable splice candidate; Original and Updated are owned copies.
type Plan struct {
	Format            configdoc.Format
	Original, Updated []byte
	Hunks             []Hunk
	Noop              bool
	Expected          any
}

// PlanEdit locates a value through configdoc's positioned index and changes only its bytes.
func PlanEdit(data []byte, format configdoc.Format, m Mutation) (*Plan, error) {
	doc, err := configdoc.Parse(data, format)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if len(m.Path) == 0 {
		return nil, fmt.Errorf("config edit path is empty")
	}
	root := cloneMap(doc.Values)
	path := m.Path
	parentPath := path[:len(path)-1]
	last := path[len(path)-1]
	parent, parentOK := lookup(root, parentPath)
	start, end := 0, 0
	var replacement []byte
	var hunks []Hunk
	noop := false
	switch m.Action {
	case Set:
		old, exists := lookup(root, path)
		normalized, err := semanticValue(m.Value, format)
		if err != nil {
			return nil, err
		}
		span, ok, e := doc.Index.Value(path...)
		if e != nil {
			return nil, e
		}
		if !ok {
			if format == configdoc.TOML && exists {
				desired := cloneMap(doc.Values)
				if !setValue(desired, path, normalized) {
					return nil, fmt.Errorf("cannot prepare expected structural config value")
				}
				return planStructuralSet(data, format, path, old, normalized, desired)
			}
			return nil, fmt.Errorf("cannot set missing config value")
		}
		if !exists {
			return nil, fmt.Errorf("cannot set missing config value")
		}
		if semanticEqual(old, normalized) {
			noop = true
			start, end = span.Start, span.End
			break
		}
		start, end = span.Start, span.End
		if hasCommentWithin(doc.Index.Comments(), span) {
			if structureChangedDeep(old, normalized) {
				desired := cloneMap(doc.Values)
				if !setValue(desired, path, normalized) {
					return nil, fmt.Errorf("cannot prepare expected structural config value")
				}
				return planStructuralSet(data, format, path, old, normalized, desired)
			}
			hunks, err = planCommentedSet(doc, path, old, normalized)
			if err != nil {
				return nil, err
			}
			normalized = orderedLike(old, normalized)
			if len(hunks) == 0 {
				noop = true
			}
		} else {
			replacement, err = configdoc.EncodeValue(m.Value, format)
			if err != nil {
				return nil, err
			}
		}
		if !setValue(root, path, normalized) {
			return nil, fmt.Errorf("cannot set config value")
		}
	case Add:
		if last.IsIndex {
			return nil, fmt.Errorf("add path must end in a key")
		}
		if !parentOK && format == configdoc.TOML {
			start, end, replacement, err = addTOMLTable(doc, root, path, m.Value)
			if err != nil {
				return nil, err
			}
			break
		}
		if !parentOK {
			// JSONC has no implicit tables: create each missing parent object, as TOML does.
			k, anc, ok := missingJSONCParents(root, path)
			if !ok {
				return nil, fmt.Errorf("add parent does not exist and cannot be created: an ancestor of the key is not an object (or a missing step is an array index); fix that value in the file by hand, then rerun the command")
			}
			wrapped := m.Value
			for i := len(path) - 1; i > k; i-- {
				w := jsonx.NewOrderedMap()
				w.Set(path[i].Key, wrapped)
				wrapped = w
			}
			start, end, replacement, err = insertMember(doc, path[:k], path[k].Key, wrapped)
			if err != nil {
				return nil, err
			}
			normalized, err := semanticValue(wrapped, format)
			if err != nil {
				return nil, err
			}
			anc.Set(path[k].Key, normalized)
			break
		}
		obj, ok := parent.(*jsonx.OrderedMap)
		if !ok {
			return nil, fmt.Errorf("add parent is not an object")
		}
		if _, exists := obj.Get(last.Key); exists {
			noop = true
			break
		}
		start, end, replacement, err = insertMember(doc, parentPath, last.Key, m.Value)
		if err != nil {
			return nil, err
		}
		normalized, err := semanticValue(m.Value, format)
		if err != nil {
			return nil, err
		}
		obj.Set(last.Key, normalized)
	case Append:
		if format == configdoc.TOML && tomlArrayTableAt(data, path) {
			rowsValue, exists := lookup(root, path)
			rows, valid := rowsValue.([]any)
			if !exists || !valid {
				return nil, fmt.Errorf("append target is not a TOML array-table")
			}
			normalized, err := semanticValue(m.Value, format)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if semanticEqual(row, normalized) {
					noop = true
					break
				}
			}
			if !noop {
				updated, err := appendTOMLArrayTableItem(data, path, normalized)
				if err != nil {
					return nil, err
				}
				start, end = commonDifference(data, updated)
				newEnd := len(updated) - (len(data) - end)
				hunks = []Hunk{{Start: start, End: end, Before: append([]byte(nil), data[start:end]...), After: append([]byte(nil), updated[start:newEnd]...)}}
				setValue(root, path, append(append([]any(nil), rows...), normalized))
			}
			break
		}
		span, ok, e := doc.Index.Value(path...)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, fmt.Errorf("cannot append to missing config value")
		}
		v, ok := lookup(root, path)
		if !ok {
			return nil, fmt.Errorf("cannot append to missing config value")
		}
		items, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("append target is %T, not an array", v)
		}
		normalized, err := semanticValue(m.Value, format)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if reflect.DeepEqual(item, normalized) {
				noop = true
				break
			}
		}
		if noop {
			break
		}
		start, end, replacement, err = appendArray(doc, path, span, items, m.Value)
		if err != nil {
			return nil, err
		}
		setValue(root, path, append(append([]any(nil), items...), normalized))
	case Remove:
		if format == configdoc.TOML && last.IsIndex {
			itemPath := path
			_, ok, e := doc.Index.Item(itemPath...)
			if e != nil {
				return nil, e
			}
			arrayTable, e := isTOMLArrayTableItem(doc, itemPath)
			if e != nil {
				return nil, e
			}
			if ok && arrayTable {
				items, valid := parent.([]any)
				if !valid || last.Index < 0 || last.Index >= len(items) {
					return nil, fmt.Errorf("remove target is not an array-table item")
				}
				if len(items) == 1 {
					ranges, handled, e := tomlStructureRanges(doc, path)
					if e != nil {
						return nil, e
					}
					if !handled {
						return nil, fmt.Errorf("cannot locate TOML array-table item for removal")
					}
					hunks = commentPreservingHunks(data, ranges, doc.Index.Comments())
					if pathHasIndex(parentPath) {
						scope := parentPath[:len(parentPath)-1]
						lastKey := parentPath[len(parentPath)-1]
						if lastKey.IsIndex {
							return nil, fmt.Errorf("nested array-table parent has no empty-list key")
						}
						insertAt, insertEnd, insertion, insertErr := insertMember(doc, scope, lastKey.Key, []any{})
						if insertErr != nil {
							return nil, insertErr
						}
						insertHunk := Hunk{Start: insertAt, End: insertEnd, Before: append([]byte(nil), data[insertAt:insertEnd]...), After: insertion}
						combined := false
						for i := range hunks {
							if hunks[i].Start == insertAt {
								hunks[i].After = append(append([]byte(nil), insertion...), hunks[i].After...)
								combined = true
								break
							}
						}
						if !combined {
							hunks = append(hunks, insertHunk)
						}
						sort.Slice(hunks, func(i, j int) bool { return hunks[i].Start < hunks[j].Start })
					} else {
						emptyKey := tomlPathString(parentPath) + " = []" + newlineOf(data)
						hunks = append([]Hunk{{Start: 0, End: 0, Before: nil, After: []byte(emptyKey)}}, hunks...)
					}
					setValue(root, parentPath, []any{})
					break
				}
				ranges, handled, e := tomlStructureRanges(doc, path)
				if e != nil {
					return nil, e
				}
				if handled {
					hunks = commentPreservingHunks(data, ranges, doc.Index.Comments())
					setValue(root, parentPath, append(append([]any(nil), items[:last.Index]...), items[last.Index+1:]...))
					break
				}
			}
		}
		if !last.IsIndex && format == configdoc.TOML {
			if _, exists := lookup(root, path); exists {
				ranges, handled, e := tomlStructureRanges(doc, path)
				if e != nil {
					return nil, e
				}
				if handled {
					hunks = commentPreservingHunks(data, ranges, doc.Index.Comments())
					// A TOML inline table is one line and holds no comment, so its member goes
					// whole, with one separating comma (inlineTableMemberSpan).
					if len(ranges) == 1 {
						if span, inline := inlineTableMemberSpan(data, ranges[0]); inline {
							hunks = []Hunk{{Start: span.Start, End: span.End, Before: append([]byte(nil), data[span.Start:span.End]...)}}
						}
					}
					deleteValue(root, path)
					break
				}
			}
		}
		if last.IsIndex {
			item, ok, e := doc.Index.Item(path...)
			if e != nil {
				return nil, e
			}
			if !ok {
				noop = true
				break
			}
			items, ok := parent.([]any)
			if !ok || last.Index < 0 || last.Index >= len(items) {
				return nil, fmt.Errorf("remove target is not an array item")
			}
			start, end, replacement, err = removeArrayItem(doc, parentPath, last.Index)
			if err != nil {
				return nil, err
			}
			if hasCommentInRange(doc.Index.Comments(), start, end) {
				if last.Index > 0 && last.Index < len(items)-1 && end == item.End {
					nextPath := append(append([]configdoc.Step(nil), parentPath...), configdoc.Step{Index: last.Index + 1, IsIndex: true})
					next, found, e := doc.Index.Item(nextPath...)
					if e != nil || !found {
						return nil, fmt.Errorf("cannot locate following array item")
					}
					comma := findComma(data, item.End, next.Start, doc.Index.Comments())
					if comma < 0 {
						return nil, fmt.Errorf("cannot locate following array separator")
					}
					end = comma + 1
				}
				replacement = preserveComments(data, start, end, doc.Index.Comments())
				if last.Index > 0 && last.Index < len(items)-1 {
					replacement = append(replacement, ',')
				}
			}
			setValue(root, parentPath, append(append([]any(nil), items[:last.Index]...), items[last.Index+1:]...))
		} else {
			member, ok, e := doc.Index.Member(path...)
			if e != nil {
				return nil, e
			}
			if !ok {
				noop = true
				break
			}
			if !parentOK {
				return nil, fmt.Errorf("remove parent does not exist")
			}
			if format == configdoc.TOML {
				start, end, replacement, err = removeTOMLMember(data, member)
			} else {
				start, end, replacement, err = removeJSONMember(doc, member, parentPath)
			}
			if err != nil {
				return nil, err
			}
			if hasCommentInRange(doc.Index.Comments(), start, end) {
				replacement = preserveComments(data, start, end, doc.Index.Comments())
			}
			obj, ok := parent.(*jsonx.OrderedMap)
			if !ok {
				return nil, fmt.Errorf("remove parent is not an object")
			}
			obj.Delete(last.Key)
		}
	default:
		return nil, fmt.Errorf("unknown config edit action %d", m.Action)
	}
	updated := append([]byte(nil), data...)
	if !noop {
		updated = applyHunks(data, hunks)
		if len(hunks) == 0 {
			updated = splice(data, start, end, replacement)
		}
		planned, err := configdoc.Parse(updated, format)
		if err != nil {
			return nil, fmt.Errorf("planned splice does not parse: %w", err)
		}
		if !semanticEqual(planned.Values, root) {
			return nil, fmt.Errorf("planned splice changes effective document values: expected %#v, got %#v", root, planned.Values)
		}
	}
	planHunks := hunks
	if len(planHunks) == 0 {
		planHunks = []Hunk{{Start: start, End: end, Before: append([]byte(nil), data[start:end]...), After: append([]byte(nil), replacement...)}}
	}
	return &Plan{
		Format: format, Original: append([]byte(nil), data...), Updated: updated,
		Noop: noop, Expected: root, Hunks: planHunks,
	}, nil
}

func insertMember(doc *configdoc.Document, parent []configdoc.Step, key string, value any) (int, int, []byte, error) {
	encoded, err := configdoc.EncodeValue(value, doc.Format)
	if err != nil {
		return 0, 0, nil, err
	}
	if doc.Format == configdoc.JSONC {
		span, ok, err := doc.Index.Table(parent...)
		if err != nil {
			return 0, 0, nil, err
		}
		if !ok && len(parent) == 0 {
			span = configdoc.Span{Start: 0, End: len(doc.Bytes)}
			ok = true
		}
		if !ok {
			return 0, 0, nil, fmt.Errorf("JSONC parent is not an object")
		}
		return insertJSONMember(doc, span, parent, key, encoded)
	}
	if _, ok := lookup(doc.Values, parent); !ok {
		return 0, 0, nil, fmt.Errorf("TOML parent table does not exist")
	}
	span, ok, err := doc.Index.Table(parent...)
	if err != nil {
		return 0, 0, nil, err
	}
	if ok && span.Start < len(doc.Bytes) && doc.Bytes[span.Start] == '{' {
		tableValue, _ := lookup(doc.Values, parent)
		table, valid := tableValue.(*jsonx.OrderedMap)
		if !valid {
			return 0, 0, nil, fmt.Errorf("inline TOML table parent is invalid")
		}
		name := tomlKey(key)
		close := span.End - 1
		if table.Len() == 0 {
			return close, close, []byte(name + " = " + string(encoded)), nil
		}
		keys := table.Keys()
		path := append(append([]configdoc.Step(nil), parent...), configdoc.Step{Key: keys[len(keys)-1]})
		last, found, e := doc.Index.Value(path...)
		if e != nil || !found {
			return 0, 0, nil, fmt.Errorf("cannot locate last inline TOML key")
		}
		return last.End, last.End, []byte(", " + name + " = " + string(encoded)), nil
	}
	name := tomlKey(key)
	if pathHasIndex(parent) && !(ok && tomlHeaderAt(doc.Bytes, span)) {
		if at, headerPath, implicit, e := indexedImplicitTOMLScope(doc, parent); e != nil {
			return 0, 0, nil, e
		} else if implicit {
			text := []byte("[" + tomlHeaderPathString(headerPath) + "]" + newlineOf(doc.Bytes) + name + " = " + string(encoded) + newlineOf(doc.Bytes))
			if at > 0 && doc.Bytes[at-1] != '\n' {
				text = append([]byte(newlineOf(doc.Bytes)), text...)
			}
			return at, at, text, nil
		}
	}
	if (len(parent) > 0 && parent[len(parent)-1].IsIndex) || (ok && tomlHeaderAt(doc.Bytes, span)) {
		_, at, descendant, e := tomlScopeInsertion(doc, parent)
		if e != nil {
			return 0, 0, nil, e
		}
		if descendant {
			text := []byte(name + " = " + string(encoded) + newlineOf(doc.Bytes))
			if at > 0 && doc.Bytes[at-1] != '\n' {
				text = append([]byte(newlineOf(doc.Bytes)), text...)
			}
			return at, at, text, nil
		}
	}
	if !pathHasIndex(parent) && !(ok && span.Start < len(doc.Bytes) && tomlHeaderAt(doc.Bytes, span)) {
		fullPath := append(append([]configdoc.Step(nil), parent...), configdoc.Step{Key: key})
		at, found, err := firstTOMLHeaderUnder(doc, nil)
		if err != nil {
			return 0, 0, nil, err
		}
		if !found {
			at = len(doc.Bytes)
		}
		text := []byte(tomlPathString(fullPath) + " = " + string(encoded) + newlineOf(doc.Bytes))
		if at > 0 && doc.Bytes[at-1] != '\n' {
			text = append([]byte(newlineOf(doc.Bytes)), text...)
		}
		return at, at, text, nil
	}
	end := len(doc.Bytes)
	if ok {
		end = span.End
	}
	text := []byte(name + " = " + string(encoded) + newlineOf(doc.Bytes))
	if end > 0 && doc.Bytes[end-1] != '\n' {
		text = append([]byte(newlineOf(doc.Bytes)), text...)
	}
	return end, end, text, nil
}

func insertJSONMember(doc *configdoc.Document, table configdoc.Span, parent []configdoc.Step, key string, value []byte) (int, int, []byte, error) {
	data := doc.Bytes
	open, close := table.Start, table.End
	for close > open && (data[close-1] == ' ' || data[close-1] == '\t' || data[close-1] == '\r' || data[close-1] == '\n') {
		close--
	}
	close--
	for open < close && data[open] != '{' {
		open++
	}
	if open >= close || data[close] != '}' {
		return 0, 0, nil, fmt.Errorf("JSONC object span is malformed")
	}
	v, ok := lookup(doc.Values, parent)
	obj, valid := v.(*jsonx.OrderedMap)
	if !ok || !valid {
		return 0, 0, nil, fmt.Errorf("JSONC parent is not an object")
	}
	name := strconv.Quote(key)
	multiline := bytes.Contains(data[open:close], []byte("\n")) || bytes.Contains(data[open:close], []byte("\r"))
	if obj.Len() == 0 {
		if multiline {
			indent := inferIndent(data, open) + "  "
			return open + 1, open + 1, []byte(newlineOf(data) + indent + name + ": " + reindentValue(value, indent, newlineOf(data))), nil
		}
		return open + 1, open + 1, []byte("\n  " + name + ": " + reindentValue(value, "  ", "\n") + "\n"), nil
	}
	keys := obj.Keys()
	lastPath := append(append([]configdoc.Step(nil), parent...), configdoc.Step{Key: keys[len(keys)-1]})
	last, ok, err := doc.Index.Value(lastPath...)
	if err != nil || !ok {
		return 0, 0, nil, fmt.Errorf("cannot locate last JSONC member: %v", err)
	}
	var trailingComment configdoc.Span
	foundComment := false
	for _, comment := range doc.Index.Comments() {
		if comment.Start >= last.End && comment.Start < close {
			trailingComment = comment
			foundComment = true
		}
	}
	if foundComment {
		indent := inferIndent(data, last.Start)
		if !multiline {
			if trailingComment.Start+1 >= trailingComment.End || data[trailingComment.Start+1] != '*' {
				return 0, 0, nil, fmt.Errorf("cannot insert after a single-line JSONC line comment")
			}
			commaBefore := findComma(data, last.End, trailingComment.Start, doc.Index.Comments())
			commaAfter := findComma(data, trailingComment.End, close, doc.Index.Comments())
			var out []byte
			if commaBefore < 0 && commaAfter < 0 {
				out = append(out, ',')
			}
			out = append(out, data[last.End:trailingComment.End]...)
			end := trailingComment.End
			if commaBefore < 0 && commaAfter >= 0 {
				out = append(out, data[end:commaAfter+1]...)
				end = commaAfter + 1
			}
			spaceStart := end
			for end < close && (data[end] == ' ' || data[end] == '\t') {
				out = append(out, data[end])
				end++
			}
			if end == spaceStart {
				out = append(out, ' ')
			}
			out = append(out, []byte(name+": "+string(value))...)
			return last.End, end, out, nil
		}
		end := lineEndAt(data, trailingComment.End)
		if end < len(data) && data[end] == '\r' {
			end++
		}
		if end < len(data) && data[end] == '\n' {
			end++
		}
		comma := findComma(data, last.End, end, doc.Index.Comments())
		var out []byte
		if comma < 0 {
			out = append(out, ',')
		}
		out = append(out, data[last.End:end]...)
		out = append(out, []byte(indent+name+": "+reindentValue(value, indent, newlineOf(data))+newlineOf(data))...)
		return last.End, end, out, nil
	}
	if multiline {
		indent := inferIndent(data, last.Start)
		return last.End, last.End, []byte("," + newlineOf(data) + indent + name + ": " + reindentValue(value, indent, newlineOf(data))), nil
	}
	return last.End, last.End, []byte(", " + name + ": " + string(value)), nil
}

// reindentValue re-bases a multi-line encoded value so its continuation lines sit under the
// member's own indent; the encoder always starts at column zero.
func reindentValue(value []byte, indent, nl string) string {
	return strings.ReplaceAll(string(value), "\n", nl+indent)
}

// missingJSONCParents finds the deepest existing ancestor object of path: k is its depth, so
// path[k] is the first key a JSONC add must create. ok is false when that ancestor is not an
// object or a step to create is an array index.
func missingJSONCParents(root any, path []configdoc.Step) (k int, anc *jsonx.OrderedMap, ok bool) {
	for k = len(path) - 1; k > 0; k-- {
		if _, found := lookup(root, path[:k]); found {
			break
		}
	}
	v, _ := lookup(root, path[:k])
	anc, isObj := v.(*jsonx.OrderedMap)
	if !isObj {
		return 0, nil, false
	}
	for _, s := range path[k:] {
		if s.IsIndex {
			return 0, nil, false
		}
	}
	return k, anc, true
}

func addTOMLTable(doc *configdoc.Document, root *jsonx.OrderedMap, path []configdoc.Step, value any) (int, int, []byte, error) {
	if len(path) < 2 {
		return 0, 0, nil, fmt.Errorf("new TOML parent table is incomplete")
	}
	keys := make([]string, len(path))
	for i, s := range path {
		if s.IsIndex {
			return 0, 0, nil, fmt.Errorf("new TOML table path cannot contain array indices")
		}
		keys[i] = s.Key
	}
	normalized, err := semanticValue(value, configdoc.TOML)
	if err != nil {
		return 0, 0, nil, err
	}
	current := any(root)
	for _, key := range keys[:len(keys)-1] {
		m, ok := current.(*jsonx.OrderedMap)
		if !ok {
			return 0, 0, nil, fmt.Errorf("TOML parent %q is not a table", key)
		}
		next, exists := m.Get(key)
		if !exists {
			break
		}
		current = next
	}
	encoded, err := configdoc.EncodeValue(value, doc.Format)
	if err != nil {
		return 0, 0, nil, err
	}
	out := make([]byte, 0)
	if len(doc.Bytes) > 0 {
		if doc.Bytes[len(doc.Bytes)-1] != '\n' {
			out = append(out, []byte(newlineOf(doc.Bytes))...)
		}
		out = append(out, []byte(newlineOf(doc.Bytes))...)
	}
	out = append(out, '[')
	for i, key := range keys[:len(keys)-1] {
		if i > 0 {
			out = append(out, '.')
		}
		out = append(out, []byte(tomlKey(key))...)
	}
	out = append(out, ']')
	out = append(out, []byte(newlineOf(doc.Bytes))...)
	out = append(out, []byte(tomlKey(keys[len(keys)-1])+" = ")...)
	out = append(out, encoded...)
	out = append(out, []byte(newlineOf(doc.Bytes))...)
	current = any(root)
	for _, key := range keys[:len(keys)-1] {
		m := current.(*jsonx.OrderedMap)
		next, exists := m.Get(key)
		if !exists {
			next = jsonx.NewOrderedMap()
			m.Set(key, next)
		}
		child, ok := next.(*jsonx.OrderedMap)
		if !ok {
			return 0, 0, nil, fmt.Errorf("TOML parent %q is not a table", key)
		}
		current = child
	}
	current.(*jsonx.OrderedMap).Set(keys[len(keys)-1], normalized)
	return len(doc.Bytes), len(doc.Bytes), out, nil
}

func appendArray(doc *configdoc.Document, path []configdoc.Step, span configdoc.Span, items []any, value any) (int, int, []byte, error) {
	encoded, err := configdoc.EncodeValue(value, doc.Format)
	if err != nil {
		return 0, 0, nil, err
	}
	data := doc.Bytes
	open, close := span.Start, span.End-1
	if open < 0 || close < open || data[open] != '[' || data[close] != ']' {
		return 0, 0, nil, fmt.Errorf("array span is malformed")
	}
	if len(items) == 0 {
		if bytes.Contains(data[open:close], []byte("\n")) {
			return close, close, []byte("  " + string(encoded) + newlineOf(data) + inferIndent(data, close)), nil
		}
		return close, close, encoded, nil
	}
	lastPath := append(append([]configdoc.Step(nil), path...), configdoc.Step{Index: len(items) - 1, IsIndex: true})
	last, ok, err := doc.Index.Item(lastPath...)
	if err != nil || !ok {
		return 0, 0, nil, fmt.Errorf("cannot locate last array item")
	}
	for _, comment := range doc.Index.Comments() {
		if comment.Start < last.End || comment.Start >= close {
			continue
		}
		lineEnd := lineEndAt(data, comment.End)
		end := lineEnd
		if end < len(data) && data[end] == '\r' {
			end++
		}
		if end < len(data) && data[end] == '\n' {
			end++
		}
		closeLine := lineStartAt(data, close)
		if end > closeLine {
			continue
		}
		comma := findComma(data, last.End, comment.Start, doc.Index.Comments())
		var out []byte
		if comma < 0 {
			out = append(out, ',')
		}
		out = append(out, data[last.End:closeLine]...)
		out = append(out, []byte(inferIndent(data, last.Start)+string(encoded)+newlineOf(data))...)
		return last.End, closeLine, out, nil
	}
	sep := []byte(", ")
	if bytes.Contains(data[open:close], []byte("\n")) {
		sep = []byte("," + newlineOf(data) + inferIndent(data, last.Start))
	}
	return last.End, last.End, append(sep, encoded...), nil
}
func semanticValue(value any, format configdoc.Format) (any, error) {
	encoded, err := configdoc.EncodeValue(value, format)
	if err != nil {
		return nil, err
	}
	var source []byte
	if format == configdoc.JSONC {
		source = append([]byte(`{"value":`), encoded...)
		source = append(source, '}')
	} else {
		source = append([]byte("value = "), encoded...)
	}
	doc, err := configdoc.Parse(source, format)
	if err != nil {
		return nil, fmt.Errorf("parse encoded value: %w", err)
	}
	value, ok := doc.Values.Get("value")
	if !ok {
		return nil, fmt.Errorf("encoded value was not retained")
	}
	return value, nil
}

func tomlStructureRanges(doc *configdoc.Document, path []configdoc.Step) ([]configdoc.Span, bool, error) {
	var ranges []configdoc.Span
	var walk func([]configdoc.Step, any) error
	walk = func(at []configdoc.Step, value any) error {
		switch node := value.(type) {
		case *jsonx.OrderedMap:
			if len(at) > 0 {
				header, isHeader, err := isTOMLHeaderTable(doc, at)
				if err != nil {
					return err
				}
				if isHeader {
					ranges = append(ranges, header)
				}
				member, found, err := doc.Index.Member(at...)
				if err != nil {
					return err
				}
				if found {
					ranges = append(ranges, member)
					return nil
				}
			}
			for _, key := range node.Keys() {
				child, _ := node.Get(key)
				if err := walk(appendStep(at, configdoc.Step{Key: key}), child); err != nil {
					return err
				}
			}
		case []any:
			if len(at) > 0 {
				member, found, err := doc.Index.Member(at...)
				if err != nil {
					return err
				}
				if found {
					ranges = append(ranges, member)
					return nil
				}
			}
			for i, child := range node {
				childPath := appendStep(at, configdoc.Step{Index: i, IsIndex: true})
				item, found, err := doc.Index.Item(childPath...)
				if err != nil {
					return err
				}
				arrayTable, err := isTOMLArrayTableItem(doc, childPath)
				if err != nil {
					return err
				}
				if found && arrayTable {
					ranges = append(ranges, item)
				}
				if err := walk(childPath, child); err != nil {
					return err
				}
			}
		default:
			if len(at) > 0 {
				member, found, err := doc.Index.Member(at...)
				if err != nil {
					return err
				}
				if found {
					ranges = append(ranges, member)
				}
			}
		}
		return nil
	}
	value, ok := lookup(doc.Values, path)
	if !ok {
		return nil, false, nil
	}
	if err := walk(path, value); err != nil {
		return nil, false, err
	}
	if len(ranges) == 0 {
		return nil, false, nil
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	merged := make([]configdoc.Span, 0, len(ranges))
	for _, span := range ranges {
		if span.Start < 0 || span.End > len(doc.Bytes) || span.End < span.Start {
			return nil, false, fmt.Errorf("invalid TOML structural span")
		}
		if len(merged) > 0 && span.Start <= merged[len(merged)-1].End {
			if span.End > merged[len(merged)-1].End {
				merged[len(merged)-1].End = span.End
			}
			continue
		}
		merged = append(merged, span)
	}
	return merged, true, nil
}

func commentPreservingHunks(data []byte, ranges []configdoc.Span, comments []configdoc.Span) []Hunk {
	hunks := make([]Hunk, 0, len(ranges))
	for _, span := range ranges {
		hunks = append(hunks, Hunk{Start: span.Start, End: span.End, Before: append([]byte(nil), data[span.Start:span.End]...), After: preserveComments(data, span.Start, span.End, comments)})
	}
	return hunks
}

func deleteValue(root *jsonx.OrderedMap, path []configdoc.Step) {
	if len(path) == 0 {
		return
	}
	parent, ok := lookup(root, path[:len(path)-1])
	if !ok {
		return
	}
	last := path[len(path)-1]
	if last.IsIndex {
		if items, ok := parent.([]any); ok && last.Index >= 0 && last.Index < len(items) {
			setValue(root, path[:len(path)-1], append(items[:last.Index], items[last.Index+1:]...))
		}
		return
	}
	if object, ok := parent.(*jsonx.OrderedMap); ok {
		object.Delete(last.Key)
	}
}

func indexedOwnerPath(path []configdoc.Step) ([]configdoc.Step, bool) {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].IsIndex {
			return append([]configdoc.Step(nil), path[:i+1]...), true
		}
	}
	return nil, false
}

func tomlHeaderPathString(path []configdoc.Step) string {
	var out strings.Builder
	for _, step := range path {
		if step.IsIndex {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte('.')
		}
		out.WriteString(tomlKey(step.Key))
	}
	return out.String()
}

// indexedImplicitTOMLScope locates an implicit table beneath a concrete array-table row.
// Its returned path omits source row indices because TOML headers cannot express them.
func indexedImplicitTOMLScope(doc *configdoc.Document, scope []configdoc.Step) (int, []configdoc.Step, bool, error) {
	ownerPath, ok := indexedOwnerPath(scope)
	if !ok || len(ownerPath) == len(scope) {
		return 0, nil, false, nil
	}
	owner, boundary, found, err := indexedOwnerLifetime(doc, ownerPath)
	if err != nil || !found {
		return 0, nil, false, err
	}
	arrayTable, err := isTOMLArrayTableItem(doc, ownerPath)
	if err != nil || !arrayTable {
		return 0, nil, false, err
	}
	at, hasDescendant, err := firstTOMLHeaderUnderBetween(doc, scope, owner.Start, boundary)
	if err != nil {
		return 0, nil, false, err
	}
	if !hasDescendant {
		return 0, nil, false, nil
	}
	return at, append([]configdoc.Step(nil), scope...), true, nil
}

// indexedOwnerLifetime returns an indexed row's source start and the next source row
// of any indexed ancestor, which bounds ownership beyond contiguous table extents.
func indexedOwnerLifetime(doc *configdoc.Document, ownerPath []configdoc.Step) (configdoc.Span, int, bool, error) {
	owner, found, err := doc.Index.Item(ownerPath...)
	if err != nil || !found {
		return configdoc.Span{}, 0, false, err
	}
	boundary := len(doc.Bytes)
	for i, step := range ownerPath {
		if !step.IsIndex {
			continue
		}
		nextPath := append([]configdoc.Step(nil), ownerPath[:i+1]...)
		nextPath[i].Index++
		next, ok, err := doc.Index.Item(nextPath...)
		if err != nil {
			return configdoc.Span{}, 0, false, err
		}
		if ok && next.Start > owner.Start && next.Start < boundary {
			boundary = next.Start
		}
	}
	return owner, boundary, true, nil
}

func tomlArrayTableDescendantEnd(doc *configdoc.Document, target []configdoc.Step, initial int) (int, error) {
	end := initial
	var walk func([]configdoc.Step, any) error
	walk = func(path []configdoc.Step, value any) error {
		switch node := value.(type) {
		case *jsonx.OrderedMap:
			if len(path) > len(target) && configPathHasPrefix(path, target) {
				span, header, err := isTOMLHeaderTable(doc, path)
				if err != nil {
					return err
				}
				if header && span.End > end {
					end = span.End
				}
			}
			for _, key := range node.Keys() {
				child, _ := node.Get(key)
				if err := walk(appendStep(path, configdoc.Step{Key: key}), child); err != nil {
					return err
				}
			}
		case []any:
			for i, child := range node {
				itemPath := appendStep(path, configdoc.Step{Index: i, IsIndex: true})
				if len(itemPath) > len(target) && configPathHasPrefix(itemPath, target) {
					span, header, err := doc.Index.Item(itemPath...)
					if err != nil {
						return err
					}
					arrayTable, err := isTOMLArrayTableItem(doc, itemPath)
					if err != nil {
						return err
					}
					if header && arrayTable && span.End > end {
						end = span.End
					}
				}
				if err := walk(itemPath, child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(nil, doc.Values); err != nil {
		return 0, err
	}
	if ownerPath, ok := indexedOwnerPath(target); ok {
		_, boundary, found, err := indexedOwnerLifetime(doc, ownerPath)
		if err != nil {
			return 0, err
		}
		if found && end > boundary {
			end = boundary
		}
	}
	return end, nil
}

func tomlArrayFieldIndent(doc *configdoc.Document, itemPath []configdoc.Step, key string) string {
	fieldPath := appendStep(itemPath, configdoc.Step{Key: key})
	span, found, err := doc.Index.Member(fieldPath...)
	if err == nil && found {
		return inferIndent(doc.Bytes, span.Start)
	}
	existingValue, ok := lookup(doc.Values, itemPath)
	existing, valid := existingValue.(*jsonx.OrderedMap)
	if ok && valid {
		nearest := -1
		for _, existingKey := range existing.Keys() {
			fieldPath = appendStep(itemPath, configdoc.Step{Key: existingKey})
			span, found, err := doc.Index.Member(fieldPath...)
			if err == nil && found && span.Start > nearest {
				nearest = span.Start
			}
		}
		if nearest >= 0 {
			return inferIndent(doc.Bytes, nearest)
		}
	}
	return ""
}

func pathHasIndex(path []configdoc.Step) bool {
	for _, step := range path {
		if step.IsIndex {
			return true
		}
	}
	return false
}

func tomlPathString(path []configdoc.Step) string {
	var out strings.Builder
	for _, step := range path {
		if step.IsIndex {
			out.WriteString(fmt.Sprintf("[%d]", step.Index))
			continue
		}
		if out.Len() > 0 {
			out.WriteByte('.')
		}
		out.WriteString(tomlKey(step.Key))
	}
	return out.String()
}

func structureChangedDeep(before, after any) bool {
	oldMap, oldIsMap := before.(*jsonx.OrderedMap)
	newMap, newIsMap := after.(*jsonx.OrderedMap)
	if oldIsMap && newIsMap {
		if !sameKeys(oldMap, newMap) {
			return true
		}
		for _, key := range oldMap.Keys() {
			a, _ := oldMap.Get(key)
			b, _ := newMap.Get(key)
			if structureChangedDeep(a, b) {
				return true
			}
		}
		return false
	}
	oldItems, oldArray := before.([]any)
	newItems, newArray := after.([]any)
	if oldIsMap || newIsMap || oldArray || newArray {
		if oldArray && newArray {
			if len(oldItems) != len(newItems) {
				return true
			}
			for i := range oldItems {
				if structureChangedDeep(oldItems[i], newItems[i]) {
					return true
				}
			}
			return false
		}
		return true
	}
	return false
}

// planStructuralSet expresses a shape change as positioned member/item edits. Each
// step reparses the preceding bytes through configdoc, so no source coordinates go stale.
func planStructuralSet(data []byte, format configdoc.Format, path []configdoc.Step, before, after any, expected *jsonx.OrderedMap) (*Plan, error) {
	original := append([]byte(nil), data...)
	var reconcile func([]configdoc.Step, any, any) error
	reconcile = func(at []configdoc.Step, old, next any) error {
		if reflect.DeepEqual(old, next) {
			return nil
		}
		oldMap, oldIsMap := old.(*jsonx.OrderedMap)
		newMap, newIsMap := next.(*jsonx.OrderedMap)
		if oldIsMap && newIsMap {
			for _, key := range oldMap.Keys() {
				if _, exists := newMap.Get(key); !exists {
					p, err := PlanEdit(data, format, Mutation{Action: Remove, Path: appendStep(at, configdoc.Step{Key: key})})
					if err != nil {
						return err
					}
					data = p.Updated
				}
			}
			for _, key := range newMap.Keys() {
				nv, _ := newMap.Get(key)
				ov, exists := oldMap.Get(key)
				if !exists {
					p, err := PlanEdit(data, format, Mutation{Action: Add, Path: appendStep(at, configdoc.Step{Key: key}), Value: nv})
					if err != nil {
						return err
					}
					data = p.Updated
					continue
				}
				if err := reconcile(appendStep(at, configdoc.Step{Key: key}), ov, nv); err != nil {
					return err
				}
			}
			return nil
		}
		oldItems, oldArray := old.([]any)
		newItems, newArray := next.([]any)
		if oldArray && newArray {
			for i := len(oldItems) - 1; i >= len(newItems); i-- {
				p, err := PlanEdit(data, format, Mutation{Action: Remove, Path: appendStep(at, configdoc.Step{Index: i, IsIndex: true})})
				if err != nil {
					return err
				}
				data = p.Updated
			}
			for i := len(oldItems); i < len(newItems); i++ {
				var updated []byte
				var err error
				if format == configdoc.TOML && tomlArrayTableAt(data, at) {
					updated, err = appendTOMLArrayTableItem(data, at, newItems[i])
				} else {
					updated, err = appendArrayOccurrence(data, format, at, newItems[i])
				}
				if err != nil {
					return err
				}
				data = updated
			}
			for i := 0; i < len(oldItems) && i < len(newItems); i++ {
				if err := reconcile(appendStep(at, configdoc.Step{Index: i, IsIndex: true}), oldItems[i], newItems[i]); err != nil {
					return err
				}
			}
			return nil
		}
		if oldIsMap || newIsMap || oldArray || newArray {
			updated, err := replaceStructuralKind(data, format, at, next)
			if err != nil {
				return err
			}
			data = updated
			return nil
		}
		p, err := PlanEdit(data, format, Mutation{Action: Set, Path: at, Value: next})
		if err != nil {
			return err
		}
		data = p.Updated
		return nil
	}
	if err := reconcile(path, before, after); err != nil {
		return nil, err
	}
	parsed, err := configdoc.Parse(data, format)
	if err != nil {
		return nil, fmt.Errorf("planned structural splice does not parse: %w", err)
	}
	if !semanticEqual(parsed.Values, expected) {
		return nil, fmt.Errorf("planned structural splice changes effective document values: expected %#v, got %#v", expected, parsed.Values)
	}
	start, end := commonDifference(original, data)
	newEnd := len(data) - (len(original) - end)
	return &Plan{Format: format, Original: original, Updated: append([]byte(nil), data...), Expected: expected, Noop: bytes.Equal(original, data), Hunks: []Hunk{{Start: start, End: end, Before: append([]byte(nil), original[start:end]...), After: append([]byte(nil), data[start:newEnd]...)}}}, nil
}

func isTOMLHeaderTable(doc *configdoc.Document, path []configdoc.Step) (configdoc.Span, bool, error) {
	span, ok, err := doc.Index.Table(path...)
	if err != nil || !ok {
		return span, false, err
	}
	if _, hasValue, err := doc.Index.Value(path...); err != nil || hasValue {
		return span, false, err
	}
	return span, tomlHeaderAt(doc.Bytes, span), nil
}

func isTOMLArrayTableItem(doc *configdoc.Document, path []configdoc.Step) (bool, error) {
	if len(path) == 0 || !path[len(path)-1].IsIndex {
		return false, nil
	}
	if _, ok, err := doc.Index.Item(path...); err != nil || !ok {
		return false, err
	}
	_, ok, err := isTOMLHeaderTable(doc, path)
	return ok, err
}

func tomlHeaderAt(data []byte, span configdoc.Span) bool {
	if span.Start < 0 || span.Start >= len(data) {
		return false
	}
	start := span.Start
	for start < len(data) && (data[start] == ' ' || data[start] == '\t') {
		start++
	}
	return start < len(data) && data[start] == '['
}

func firstTOMLHeaderUnder(doc *configdoc.Document, prefix []configdoc.Step) (int, bool, error) {
	return firstTOMLHeaderUnderBetween(doc, prefix, -1, len(doc.Bytes)+1)
}

func firstTOMLHeaderUnderBetween(doc *configdoc.Document, prefix []configdoc.Step, after, before int) (int, bool, error) {
	first := len(doc.Bytes) + 1
	found := false
	var walk func([]configdoc.Step, any) error
	walk = func(path []configdoc.Step, value any) error {
		switch node := value.(type) {
		case *jsonx.OrderedMap:
			if len(path) > len(prefix) && configPathHasPrefix(path, prefix) {
				span, header, err := isTOMLHeaderTable(doc, path)
				if err != nil {
					return err
				}
				if header && span.Start > after && span.Start < before && span.Start < first {
					first, found = span.Start, true
				}
			}
			for _, key := range node.Keys() {
				child, _ := node.Get(key)
				if err := walk(appendStep(path, configdoc.Step{Key: key}), child); err != nil {
					return err
				}
			}
		case []any:
			for i, child := range node {
				itemPath := appendStep(path, configdoc.Step{Index: i, IsIndex: true})
				if header, err := isTOMLArrayTableItem(doc, itemPath); err != nil {
					return err
				} else if header && len(itemPath) > len(prefix) && configPathHasPrefix(itemPath, prefix) {
					span, _, err := doc.Index.Item(itemPath...)
					if err != nil {
						return err
					}
					if span.Start > after && span.Start < before && span.Start < first {
						first, found = span.Start, true
					}
				}
				if err := walk(itemPath, child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(nil, doc.Values); err != nil {
		return 0, false, err
	}
	return first, found, nil
}

func configPathHasPrefix(path, prefix []configdoc.Step) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func tomlScopeInsertion(doc *configdoc.Document, scope []configdoc.Step) (configdoc.Span, int, bool, error) {
	var table configdoc.Span
	var header bool
	if len(scope) > 0 && scope[len(scope)-1].IsIndex {
		var ok bool
		var err error
		table, ok, err = doc.Index.Item(scope...)
		if err != nil || !ok {
			return configdoc.Span{}, 0, false, err
		}
		header, err = isTOMLArrayTableItem(doc, scope)
		if err != nil {
			return configdoc.Span{}, 0, false, err
		}
	} else {
		var err error
		table, header, err = isTOMLHeaderTable(doc, scope)
		if err != nil {
			return configdoc.Span{}, 0, false, err
		}
	}
	if !header {
		return configdoc.Span{}, 0, false, nil
	}
	if at, found, err := firstTOMLHeaderUnderBetween(doc, scope, table.Start, table.End); err != nil {
		return configdoc.Span{}, 0, false, err
	} else if found {
		return table, at, true, nil
	}
	return table, table.End, false, nil
}

func nestedHeaderKindAssignment(doc *configdoc.Document, path []configdoc.Step, value []byte) (int, []byte, error) {
	if len(path) < 2 || path[len(path)-1].IsIndex {
		return 0, nil, fmt.Errorf("nested TOML table replacement has no key path")
	}
	scope := path[:len(path)-1]
	key := tomlKey(path[len(path)-1].Key)
	assignment := []byte(key + " = " + string(value) + newlineOf(doc.Bytes))
	if table, at, descendant, err := tomlScopeInsertion(doc, scope); err != nil {
		return 0, nil, err
	} else if table.End > 0 || at > 0 || descendant {
		if at > 0 && doc.Bytes[at-1] != '\n' {
			assignment = append([]byte(newlineOf(doc.Bytes)), assignment...)
		}
		return at, assignment, nil
	}
	if pathHasIndex(scope) {
		if at, headerPath, implicit, err := indexedImplicitTOMLScope(doc, scope); err != nil {
			return 0, nil, err
		} else if implicit {
			header := []byte("[" + tomlHeaderPathString(headerPath) + "]" + newlineOf(doc.Bytes))
			assignment = append(header, assignment...)
			if at > 0 && doc.Bytes[at-1] != '\n' {
				assignment = append([]byte(newlineOf(doc.Bytes)), assignment...)
			}
			return at, assignment, nil
		}
		return 0, nil, fmt.Errorf("cannot locate the indexed TOML parent scope")
	}
	at, found, err := firstTOMLHeaderUnder(doc, scope)
	if err != nil {
		return 0, nil, err
	}
	if !found {
		return 0, nil, fmt.Errorf("cannot locate descendant TOML header for implicit parent")
	}
	header := []byte("[" + tomlPathString(scope) + "]" + newlineOf(doc.Bytes))
	if at > 0 && doc.Bytes[at-1] != '\n' {
		header = append([]byte(newlineOf(doc.Bytes)), header...)
	}
	return at, append(header, assignment...), nil
}

func rootTableValueInsertion(doc *configdoc.Document, path []configdoc.Step) (int, bool, error) {
	if len(path) != 1 || path[0].IsIndex {
		return 0, false, nil
	}
	_, isHeader, err := isTOMLHeaderTable(doc, path)
	if err != nil || !isHeader {
		return 0, false, err
	}
	earliest, found, err := firstTOMLHeaderUnder(doc, nil)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}
	return earliest, true, nil
}

func tomlArrayTableAt(data []byte, path []configdoc.Step) bool {
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		return false
	}
	value, ok := lookup(doc.Values, path)
	items, valid := value.([]any)
	if !ok || !valid || len(items) == 0 {
		return false
	}
	itemPath := appendStep(path, configdoc.Step{Index: 0, IsIndex: true})
	arrayTable, err := isTOMLArrayTableItem(doc, itemPath)
	return err == nil && arrayTable
}

func appendTOMLArrayTableItem(data []byte, path []configdoc.Step, value any) ([]byte, error) {
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		return nil, err
	}
	rows, ok := lookup(doc.Values, path)
	items, valid := rows.([]any)
	if !ok || !valid || len(items) == 0 {
		return nil, fmt.Errorf("cannot locate existing TOML array-table rows")
	}
	keys := make([]string, 0, len(path))
	for _, step := range path {
		if !step.IsIndex {
			keys = append(keys, tomlKey(step.Key))
		}
	}
	row, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return nil, fmt.Errorf("TOML array-table item must be an object")
	}
	lastPath := appendStep(path, configdoc.Step{Index: len(items) - 1, IsIndex: true})
	last, found, err := doc.Index.Item(lastPath...)
	arrayTable, tableErr := isTOMLArrayTableItem(doc, lastPath)
	if err != nil || tableErr != nil || !found || !arrayTable || !tomlHeaderAt(data, last) {
		return nil, fmt.Errorf("cannot locate final TOML array-table header")
	}
	end, err := tomlArrayTableDescendantEnd(doc, lastPath, last.End)
	if err != nil {
		return nil, err
	}
	if ownerPath, ok := indexedOwnerPath(path); ok {
		_, boundary, found, err := indexedOwnerLifetime(doc, ownerPath)
		if err != nil || !found {
			return nil, fmt.Errorf("cannot locate owning TOML array-table lifetime")
		}
		if end > boundary {
			end = boundary
		}
	}
	nl := newlineOf(data)
	var out []byte
	if end > 0 && data[end-1] != '\n' {
		out = append(out, []byte(nl)...)
	}
	headerIndent := inferIndent(data, last.Start)
	out = append(out, []byte(headerIndent+"[["+strings.Join(keys, ".")+"]]"+nl)...)
	for _, key := range row.Keys() {
		field, _ := row.Get(key)
		encoded, e := configdoc.EncodeValue(field, configdoc.TOML)
		if e != nil {
			return nil, e
		}
		fieldIndent := tomlArrayFieldIndent(doc, lastPath, key)
		out = append(out, []byte(fieldIndent+tomlKey(key)+" = "+string(encoded)+nl)...)
	}
	updated := splice(data, end, end, out)
	if _, err := configdoc.Parse(updated, configdoc.TOML); err != nil {
		return nil, fmt.Errorf("appended TOML array-table item does not parse: %w", err)
	}
	return updated, nil
}

func appendArrayOccurrence(data []byte, format configdoc.Format, path []configdoc.Step, value any) ([]byte, error) {
	doc, err := configdoc.Parse(data, format)
	if err != nil {
		return nil, err
	}
	span, ok, err := doc.Index.Value(path...)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("cannot locate array for structural set")
	}
	current, ok := lookup(doc.Values, path)
	items, valid := current.([]any)
	if !ok || !valid {
		return nil, fmt.Errorf("structural set target is not an array")
	}
	start, end, replacement, err := appendArray(doc, path, span, items, value)
	if err != nil {
		return nil, err
	}
	updated := splice(data, start, end, replacement)
	if _, err := configdoc.Parse(updated, format); err != nil {
		return nil, fmt.Errorf("structural array splice does not parse: %w", err)
	}
	return updated, nil
}

func semanticEqual(a, b any) bool {
	am, aok := a.(*jsonx.OrderedMap)
	bm, bok := b.(*jsonx.OrderedMap)
	if aok || bok {
		if !aok || !bok || am.Len() != bm.Len() {
			return false
		}
		for _, key := range am.Keys() {
			av, _ := am.Get(key)
			bv, ok := bm.Get(key)
			if !ok || !semanticEqual(av, bv) {
				return false
			}
		}
		return true
	}
	aa, aok := a.([]any)
	ba, bok := b.([]any)
	if aok || bok {
		if !aok || !bok || len(aa) != len(ba) {
			return false
		}
		for i := range aa {
			if !semanticEqual(aa[i], ba[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func replaceStructuralKind(data []byte, format configdoc.Format, path []configdoc.Step, value any) ([]byte, error) {
	doc, err := configdoc.Parse(data, format)
	if err != nil {
		return nil, err
	}
	if format == configdoc.TOML {
		_, found, err := doc.Index.Member(path...)
		if err != nil {
			return nil, err
		}
		if !found {
			_, isHeader, err := isTOMLHeaderTable(doc, path)
			if err != nil {
				return nil, err
			}
			if !isHeader {
				return nil, fmt.Errorf("cannot change a TOML table/header kind at %s; target a concrete value", tomlPathString(path))
			}
			ranges, handled, err := tomlStructureRanges(doc, path)
			if err != nil || !handled {
				if err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("cannot locate TOML table contents at %s", tomlPathString(path))
			}
			encoded, err := configdoc.EncodeValue(value, format)
			if err != nil {
				return nil, err
			}
			hunks := commentPreservingHunks(data, ranges, doc.Index.Comments())
			if len(path) == 1 {
				insertAt, moveToRoot, err := rootTableValueInsertion(doc, path)
				if err != nil {
					return nil, err
				}
				if moveToRoot {
					assignment := append([]byte(tomlKey(path[0].Key)+" = "), encoded...)
					assignment = append(assignment, []byte(newlineOf(data))...)
					combined := false
					for i := range hunks {
						if hunks[i].Start == insertAt {
							hunks[i].After = append(append([]byte(nil), assignment...), hunks[i].After...)
							combined = true
							break
						}
					}
					if !combined {
						hunks = append(hunks, Hunk{Start: insertAt, End: insertAt, After: assignment})
					}
				}
			} else {
				insertAt, assignment, err := nestedHeaderKindAssignment(doc, path, encoded)
				if err != nil {
					return nil, err
				}
				combined := false
				for i := range hunks {
					if hunks[i].Start == insertAt {
						hunks[i].After = append(append([]byte(nil), assignment...), hunks[i].After...)
						combined = true
						break
					}
				}
				if !combined {
					hunks = append(hunks, Hunk{Start: insertAt, End: insertAt, After: assignment})
				}
			}
			sort.Slice(hunks, func(i, j int) bool { return hunks[i].Start < hunks[j].Start })
			updated := applyHunks(data, hunks)
			if _, err := configdoc.Parse(updated, format); err != nil {
				return nil, fmt.Errorf("TOML table kind splice does not parse: %w", err)
			}
			return updated, nil
		}
	}
	span, ok, err := doc.Index.Value(path...)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("cannot locate structure-changing value")
	}
	encoded, err := configdoc.EncodeValue(value, format)
	if err != nil {
		return nil, err
	}
	trivia := preserveComments(data, span.Start, span.End, doc.Index.Comments())
	replacement := append(encoded, trivia...)
	if format == configdoc.JSONC {
		trivia = preserveCommentPositions(data, span.Start, span.End, doc.Index.Comments())
		replacement = append(trivia, encoded...)
	}
	updated := splice(data, span.Start, span.End, replacement)
	if _, err := configdoc.Parse(updated, format); err != nil {
		return nil, fmt.Errorf("structure-changing splice does not parse: %w; use a TOML value form that can represent the requested structure", err)
	}
	return updated, nil
}

func commonDifference(before, after []byte) (int, int) {
	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	end := len(before)
	newEnd := len(after)
	for end > start && newEnd > start && before[end-1] == after[newEnd-1] {
		end--
		newEnd--
	}
	return start, end
}

func planCommentedSet(doc *configdoc.Document, path []configdoc.Step, old, next any) ([]Hunk, error) {
	var hunks []Hunk
	var walk func([]configdoc.Step, any, any) error
	walk = func(at []configdoc.Step, before, after any) error {
		if reflect.DeepEqual(before, after) {
			return nil
		}
		span, ok, err := doc.Index.Value(at...)
		if err != nil || !ok {
			return fmt.Errorf("cannot locate value while preserving comments")
		}
		comments := doc.Index.Comments()
		if hasCommentWithin(comments, span) {
			oldMap, oldIsMap := before.(*jsonx.OrderedMap)
			newMap, newIsMap := after.(*jsonx.OrderedMap)
			if oldIsMap && newIsMap && sameKeys(oldMap, newMap) {
				for _, key := range oldMap.Keys() {
					oldValue, _ := oldMap.Get(key)
					newValue, _ := newMap.Get(key)
					if err := walk(appendStep(at, configdoc.Step{Key: key}), oldValue, newValue); err != nil {
						return err
					}
				}
				return nil
			}
			oldItems, oldArray := before.([]any)
			newItems, newArray := after.([]any)
			if oldArray && newArray && len(oldItems) == len(newItems) {
				for i := range oldItems {
					if err := walk(appendStep(at, configdoc.Step{Index: i, IsIndex: true}), oldItems[i], newItems[i]); err != nil {
						return err
					}
				}
				return nil
			}
			return fmt.Errorf("cannot replace a commented config value with a different structure without relocating comments")
		}
		replacement, err := configdoc.EncodeValue(after, doc.Format)
		if err != nil {
			return err
		}
		hunks = append(hunks, Hunk{Start: span.Start, End: span.End, Before: append([]byte(nil), doc.Bytes[span.Start:span.End]...), After: replacement})
		return nil
	}
	if err := walk(path, old, next); err != nil {
		return nil, err
	}
	return hunks, nil
}

func orderedLike(before, after any) any {
	oldMap, oldIsMap := before.(*jsonx.OrderedMap)
	newMap, newIsMap := after.(*jsonx.OrderedMap)
	if oldIsMap && newIsMap && sameKeys(oldMap, newMap) {
		out := jsonx.NewOrderedMap()
		for _, key := range oldMap.Keys() {
			oldValue, _ := oldMap.Get(key)
			newValue, _ := newMap.Get(key)
			out.Set(key, orderedLike(oldValue, newValue))
		}
		return out
	}
	oldItems, oldArray := before.([]any)
	newItems, newArray := after.([]any)
	if oldArray && newArray && len(oldItems) == len(newItems) {
		out := make([]any, len(newItems))
		for i := range newItems {
			out[i] = orderedLike(oldItems[i], newItems[i])
		}
		return out
	}
	return after
}

func sameKeys(a, b *jsonx.OrderedMap) bool {
	if a.Len() != b.Len() {
		return false
	}
	for _, key := range a.Keys() {
		if _, ok := b.Get(key); !ok {
			return false
		}
	}
	return true
}

func appendStep(path []configdoc.Step, step configdoc.Step) []configdoc.Step {
	return append(append([]configdoc.Step(nil), path...), step)
}

func applyHunks(data []byte, hunks []Hunk) []byte {
	updated := append([]byte(nil), data...)
	for i := len(hunks) - 1; i >= 0; i-- {
		h := hunks[i]
		updated = splice(updated, h.Start, h.End, h.After)
	}
	return updated
}

func preserveCommentPositions(data []byte, start, end int, comments []configdoc.Span) []byte {
	out := append([]byte(nil), data[start:end]...)
	for i := start; i < end; i++ {
		b := data[i]
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			continue
		}
		inside := false
		for _, comment := range comments {
			if i >= comment.Start && i < comment.End {
				inside = true
				break
			}
		}
		if !inside {
			out[i-start] = ' '
		}
	}
	return out
}

func preserveComments(data []byte, start, end int, comments []configdoc.Span) []byte {
	var out []byte
	for i := start; i < end; i++ {
		keep := data[i] == ' ' || data[i] == '\t' || data[i] == '\r' || data[i] == '\n'
		if !keep {
			for _, comment := range comments {
				if i >= comment.Start && i < comment.End {
					keep = true
					break
				}
			}
		}
		if keep {
			out = append(out, data[i])
		}
	}
	return out
}

func hasCommentInRange(comments []configdoc.Span, start, end int) bool {
	return hasCommentWithin(comments, configdoc.Span{Start: start, End: end})
}

func hasCommentWithin(comments []configdoc.Span, span configdoc.Span) bool {
	for _, comment := range comments {
		if comment.Start < span.End && comment.End > span.Start {
			return true
		}
	}
	return false
}

func removeArrayItem(doc *configdoc.Document, parent []configdoc.Step, index int) (int, int, []byte, error) {
	v, ok := lookup(doc.Values, parent)
	items, valid := v.([]any)
	if !ok || !valid || index < 0 || index >= len(items) {
		return 0, 0, nil, fmt.Errorf("array item does not exist")
	}
	path := append(append([]configdoc.Step(nil), parent...), configdoc.Step{Index: index, IsIndex: true})
	item, found, err := doc.Index.Item(path...)
	if err != nil || !found {
		return 0, 0, nil, fmt.Errorf("cannot locate array item")
	}
	if len(items) <= 1 {
		return item.Start, item.End, nil, nil
	}
	if index == 0 {
		end := item.End
		for end < len(doc.Bytes) && (doc.Bytes[end] == ' ' || doc.Bytes[end] == '\t') {
			end++
		}
		if end < len(doc.Bytes) && doc.Bytes[end] == ',' {
			end++
		}
		return item.Start, end, nil, nil
	}
	priorPath := append(append([]configdoc.Step(nil), parent...), configdoc.Step{Index: index - 1, IsIndex: true})
	prior, found, err := doc.Index.Item(priorPath...)
	if err != nil || !found {
		return 0, 0, nil, fmt.Errorf("cannot locate prior array item")
	}
	comma := findComma(doc.Bytes, prior.End, item.Start, doc.Index.Comments())
	if comma < 0 {
		return 0, 0, nil, fmt.Errorf("cannot locate array separator")
	}
	if index == len(items)-1 {
		end := item.End
		for end < len(doc.Bytes) && (doc.Bytes[end] == ' ' || doc.Bytes[end] == '\t') {
			end++
		}
		return comma, end, nil, nil
	}
	nextPath := append(append([]configdoc.Step(nil), parent...), configdoc.Step{Index: index + 1, IsIndex: true})
	next, found, err := doc.Index.Item(nextPath...)
	if err != nil || !found {
		return 0, 0, nil, fmt.Errorf("cannot locate following array item")
	}
	commaAfter := findComma(doc.Bytes, item.End, next.Start, doc.Index.Comments())
	if commaAfter < 0 {
		return 0, 0, nil, fmt.Errorf("cannot locate following array separator")
	}
	for _, c := range doc.Index.Comments() {
		if c.Start >= comma && c.End <= next.Start {
			return comma, item.End, append([]byte(nil), doc.Bytes[comma+1:item.Start]...), nil
		}
	}
	end := next.Start
	spacing := append([]byte(nil), doc.Bytes[commaAfter+1:end]...)
	return comma, end, append([]byte(","), spacing...), nil
}
func jsonMemberSharesLine(doc *configdoc.Document, parent []configdoc.Step, member configdoc.Span, object *jsonx.OrderedMap) bool {
	line := lineStartAt(doc.Bytes, member.Start)
	for _, key := range object.Keys() {
		span, ok, _ := doc.Index.Value(appendStep(parent, configdoc.Step{Key: key})...)
		if ok && span.Start != member.End && lineStartAt(doc.Bytes, span.Start) == line {
			return true
		}
	}
	return false
}

func removeJSONInlineMember(doc *configdoc.Document, parent []configdoc.Step, member configdoc.Span, object *jsonx.OrderedMap) (int, int, []byte, error) {
	data := doc.Bytes
	key := findMemberKey(doc, parent, member)
	keys := object.Keys()
	position := -1
	for i, candidate := range keys {
		if candidate == key {
			position = i
			break
		}
	}
	if position < 0 {
		return 0, 0, nil, fmt.Errorf("cannot locate same-line JSONC member")
	}
	if position+1 < len(keys) {
		next, ok, err := doc.Index.Value(appendStep(parent, configdoc.Step{Key: keys[position+1]})...)
		if err != nil || !ok {
			return 0, 0, nil, fmt.Errorf("cannot locate following same-line JSONC member")
		}
		comma := findComma(data, member.End, next.Start, doc.Index.Comments())
		if comma < 0 {
			return 0, 0, nil, fmt.Errorf("cannot locate same-line JSONC separator")
		}
		start, end := member.Start, comma+1
		return start, end, preserveComments(data, start, end, doc.Index.Comments()), nil
	}
	if position > 0 {
		previous, ok, err := doc.Index.Value(appendStep(parent, configdoc.Step{Key: keys[position-1]})...)
		if err != nil || !ok {
			return 0, 0, nil, fmt.Errorf("cannot locate preceding same-line JSONC member")
		}
		comma := findComma(data, previous.End, member.Start, doc.Index.Comments())
		if comma < 0 {
			return 0, 0, nil, fmt.Errorf("cannot locate preceding same-line JSONC separator")
		}
		start, end := comma, member.End
		return start, end, preserveComments(data, start, end, doc.Index.Comments()), nil
	}
	return member.Start, member.End, preserveComments(data, member.Start, member.End, doc.Index.Comments()), nil
}

func removeJSONMember(doc *configdoc.Document, member configdoc.Span, parentPath []configdoc.Step) (int, int, []byte, error) {
	data := doc.Bytes
	v, ok := lookup(doc.Values, parentPath)
	obj, valid := v.(*jsonx.OrderedMap)
	if !ok || !valid {
		return 0, 0, nil, fmt.Errorf("JSONC parent is not an object")
	}
	if jsonMemberSharesLine(doc, parentPath, member, obj) {
		return removeJSONInlineMember(doc, parentPath, member, obj)
	}
	line := lineStartAt(data, member.Start)
	end := lineEndAt(data, member.End)
	if end < len(data) && data[end] == '\r' {
		end++
	}
	if end < len(data) && data[end] == '\n' {
		end++
	}
	comment := false
	for _, c := range doc.Index.Comments() {
		if c.Start >= member.End && c.Start < lineEndAt(data, member.End) {
			comment = true
			break
		}
	}
	trailing := []byte(nil)
	if comment {
		trailing = append([]byte(nil), data[member.End:end]...)
	}
	commaAfter := findComma(data, member.End, lineEndAt(data, member.End), doc.Index.Comments())
	if commaAfter >= 0 {
		if comment {
			idx := commaAfter - member.End
			trailing = append(trailing[:idx], trailing[idx+1:]...)
		}
		return line, end, trailing, nil
	}
	key := findMemberKey(doc, parentPath, member)
	keys := obj.Keys()
	for i, k := range keys {
		if k != key || i == 0 {
			continue
		}
		pp := append(append([]configdoc.Step(nil), parentPath...), configdoc.Step{Key: keys[i-1]})
		prior, found, e := doc.Index.Value(pp...)
		if e != nil || !found {
			return 0, 0, nil, fmt.Errorf("cannot locate preceding JSONC member")
		}
		comma := findComma(data, prior.End, member.Start, doc.Index.Comments())
		if comma >= 0 {
			repl := append([]byte(nil), data[comma+1:line]...)
			repl = append(repl, trailing...)
			return comma, end, repl, nil
		}
		break
	}
	return line, end, trailing, nil
}
func removeTOMLMember(data []byte, member configdoc.Span) (int, int, []byte, error) {
	start := lineStartAt(data, member.Start)
	indentEnd := start
	for indentEnd < len(data) && (data[indentEnd] == ' ' || data[indentEnd] == '\t') {
		indentEnd++
	}
	end := member.End
	for end < len(data) && data[end] != '\n' && data[end] != '\r' {
		if data[end] == '#' {
			return start, end, append(append([]byte(nil), data[start:indentEnd]...), data[member.End:end]...), nil
		}
		end++
	}
	if end < len(data) && data[end] == '\r' {
		end++
	}
	if end < len(data) && data[end] == '\n' {
		end++
	}
	return start, end, nil, nil
}
func findMemberKey(doc *configdoc.Document, parent []configdoc.Step, member configdoc.Span) string {
	v, _ := lookup(doc.Values, parent)
	obj, _ := v.(*jsonx.OrderedMap)
	if obj == nil {
		return ""
	}
	for _, k := range obj.Keys() {
		p := append(append([]configdoc.Step(nil), parent...), configdoc.Step{Key: k})
		s, ok, _ := doc.Index.Member(p...)
		if ok && s.Start == member.Start {
			return k
		}
	}
	return ""
}

// inlineTableMemberSpan widens the span of a member of a TOML inline table (`{ A = "1", B = "2" }`)
// to take one separating comma with it: the following one and the blanks after it, or, for the
// last member, the preceding one; the only member takes the blanks inside the braces. A span
// that is not an inline-table member is returned as it is, with false.
func inlineTableMemberSpan(data []byte, s configdoc.Span) (configdoc.Span, bool) {
	blank := func(b byte) bool { return b == ' ' || b == '\t' }
	before := s.Start - 1
	for before >= 0 && blank(data[before]) {
		before--
	}
	after := s.End
	for after < len(data) && blank(data[after]) {
		after++
	}
	if before < 0 || after >= len(data) || (data[before] != '{' && data[before] != ',') {
		return s, false
	}
	switch {
	case data[after] == ',':
		end := after + 1
		for end < len(data) && blank(data[end]) {
			end++
		}
		return configdoc.Span{Start: s.Start, End: end}, true
	case data[after] == '}' && data[before] == ',':
		return configdoc.Span{Start: before, End: s.End}, true
	case data[after] == '}' && data[before] == '{':
		return configdoc.Span{Start: before + 1, End: after}, true
	}
	return s, false
}

func findComma(data []byte, start, end int, comments []configdoc.Span) int {
	for i := start; i < end; i++ {
		if data[i] != ',' {
			continue
		}
		inside := false
		for _, c := range comments {
			if i >= c.Start && i < c.End {
				inside = true
				break
			}
		}
		if !inside {
			return i
		}
	}
	return -1
}
func lookup(root any, path []configdoc.Step) (any, bool) {
	cur := root
	for _, s := range path {
		if s.IsIndex {
			a, ok := cur.([]any)
			if !ok || s.Index < 0 || s.Index >= len(a) {
				return nil, false
			}
			cur = a[s.Index]
		} else {
			m, ok := cur.(*jsonx.OrderedMap)
			if !ok {
				return nil, false
			}
			v, ok := m.Get(s.Key)
			if !ok {
				return nil, false
			}
			cur = v
		}
	}
	return cur, true
}
func setValue(root any, path []configdoc.Step, v any) bool {
	if len(path) == 0 {
		return false
	}
	p, ok := lookup(root, path[:len(path)-1])
	if !ok {
		return false
	}
	s := path[len(path)-1]
	if s.IsIndex {
		a, ok := p.([]any)
		if !ok || s.Index < 0 || s.Index >= len(a) {
			return false
		}
		a[s.Index] = v
		return true
	}
	m, ok := p.(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	m.Set(s.Key, v)
	return true
}
func cloneMap(m *jsonx.OrderedMap) *jsonx.OrderedMap {
	n := jsonx.NewOrderedMap()
	if m != nil {
		for _, k := range m.Keys() {
			v, _ := m.Get(k)
			n.Set(k, cloneValue(v))
		}
	}
	return n
}
func cloneValue(v any) any {
	switch x := v.(type) {
	case *jsonx.OrderedMap:
		return cloneMap(x)
	case []any:
		y := make([]any, len(x))
		for i, v := range x {
			y[i] = cloneValue(v)
		}
		return y
	case map[string]any:
		y := make(map[string]any, len(x))
		for k, v := range x {
			y[k] = cloneValue(v)
		}
		return y
	default:
		return v
	}
}
func splice(data []byte, start, end int, repl []byte) []byte {
	out := make([]byte, 0, len(data)-(end-start)+len(repl))
	out = append(out, data[:start]...)
	out = append(out, repl...)
	out = append(out, data[end:]...)
	return out
}
func newlineOf(b []byte) string {
	if bytes.Contains(b, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}
func lineStartAt(b []byte, n int) int {
	if n > len(b) {
		n = len(b)
	}
	for n > 0 && b[n-1] != '\n' {
		n--
	}
	return n
}
func lineEndAt(b []byte, n int) int {
	for n < len(b) && b[n] != '\n' && b[n] != '\r' {
		n++
	}
	return n
}
func inferIndent(b []byte, n int) string {
	start := lineStartAt(b, n)
	i := start
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	return string(b[start:i])
}
func tomlKey(s string) string {
	if s != "" {
		ok := true
		for _, r := range s {
			if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				ok = false
				break
			}
		}
		if ok {
			return s
		}
	}
	return strconv.Quote(s)
}
