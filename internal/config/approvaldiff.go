package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ApprovalChangeSet is the complete structural explanation of a change between two
// canonical approval trees. ReplayVerified is false when applying the groups cannot
// reproduce current, including when a resolved-mount change has no printed mount block.
type ApprovalChangeSet struct {
	Groups         []ApprovalChangeGroup
	ReplayVerified bool
}

// ApprovalChangeGroup is one exact config path (or one unmatched array-table entry).
type ApprovalChangeGroup struct {
	Number           int
	Path             string
	Verb             string
	Added            bool
	Removed          bool
	Previous         any
	Current          any
	HasPrevious      bool
	HasCurrent       bool
	AddedItems       []any
	RemovedItems     []any
	AddedPositions   []int
	RemovedPositions []int
	Reordered        bool

	PathParts  []ApprovalPathPart
	path       []ApprovalPathPart
	arrayPath  []ApprovalPathPart
	arrayIndex int
}

type ApprovalPathPart struct {
	Key        string
	Index      int
	ArrayIndex bool
}

// BuildApprovalChangeSet computes deterministic groups and verifies that replaying them
// against previous produces current. The synthetic resolved-mount record is only credited
// to a separately printed mount disclosure; it is never presented as a config path.
func BuildApprovalChangeSet(previous, current *jsonx.OrderedMap, mountDisclosed bool) ApprovalChangeSet {
	var groups []ApprovalChangeGroup
	walkApprovalValue(&groups, nil, previous, true, current, true)
	groups = append([]ApprovalChangeGroup(nil), groups...)
	// This is an approval-record field, not a config key. The mount block owns its disclosure.
	filtered := groups[:0]
	for _, group := range groups {
		if len(group.path) > 0 && !group.path[0].ArrayIndex && group.path[0].Key == mountRecordKey {
			continue
		}
		filtered = append(filtered, group)
	}
	groups = filtered
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Path < groups[j].Path })
	for i := range groups {
		groups[i].Number = i + 1
	}
	replayed := jsonx.DeepCopyMap(previous)
	if replayed == nil {
		replayed = jsonx.NewOrderedMap()
	}
	applyApprovalGroups(&replayed, groups)
	if mountDisclosed {
		if value, ok := current.Get(mountRecordKey); ok {
			replayed.Set(mountRecordKey, jsonx.DeepCopy(value))
		} else {
			replayed.Delete(mountRecordKey)
		}
	}
	got, gotErr := SnapshotJSON(replayed)
	want, wantErr := SnapshotJSON(current)
	return ApprovalChangeSet{Groups: groups, ReplayVerified: gotErr == nil && wantErr == nil && got == want}
}

func walkApprovalValue(out *[]ApprovalChangeGroup, path []ApprovalPathPart, old any, hasOld bool, cur any, hasCur bool) {
	if hasOld && hasCur && approvalValueEqual(old, cur) {
		return
	}
	oldMap, oldIsMap := old.(*jsonx.OrderedMap)
	curMap, curIsMap := cur.(*jsonx.OrderedMap)
	if hasOld && hasCur && oldIsMap && curIsMap {
		keys := make(map[string]struct{}, oldMap.Len()+curMap.Len())
		for _, key := range oldMap.Keys() {
			keys[key] = struct{}{}
		}
		for _, key := range curMap.Keys() {
			keys[key] = struct{}{}
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			before, hasBefore := oldMap.Get(key)
			after, hasAfter := curMap.Get(key)
			next := append(append([]ApprovalPathPart(nil), path...), ApprovalPathPart{Key: key})
			walkApprovalValue(out, next, before, hasBefore, after, hasAfter)
		}
		return
	}
	oldList, oldIsList := old.([]any)
	curList, curIsList := cur.([]any)
	if hasOld && hasCur && oldIsList && curIsList {
		if listHasTable(oldList) || listHasTable(curList) {
			walkApprovalTables(out, path, oldList, curList)
			return
		}
		group := makeApprovalGroup(path, old, true, cur, true)
		if sameMultiset(oldList, curList) {
			group.Verb, group.Reordered = "reordered", true
		} else {
			group.Verb = "changed"
			group.AddedItems, group.AddedPositions, group.RemovedItems, group.RemovedPositions = unmatchedItems(oldList, curList)
		}
		*out = append(*out, group)
		return
	}
	group := makeApprovalGroup(path, old, hasOld, cur, hasCur)
	if !hasOld {
		group.Verb = "added"
	} else if !hasCur {
		group.Verb = "removed"
	} else {
		group.Verb = "changed"
	}
	*out = append(*out, group)
}

func makeApprovalGroup(path []ApprovalPathPart, old any, hasOld bool, cur any, hasCur bool) ApprovalChangeGroup {
	return ApprovalChangeGroup{Path: formatApprovalPath(path), path: append([]ApprovalPathPart(nil), path...), PathParts: append([]ApprovalPathPart(nil), path...),
		Previous: jsonx.DeepCopy(old), Current: jsonx.DeepCopy(cur), HasPrevious: hasOld, HasCurrent: hasCur}
}

func walkApprovalTables(out *[]ApprovalChangeGroup, path []ApprovalPathPart, old, cur []any) {
	if sameMultiset(old, cur) {
		group := makeApprovalGroup(path, old, true, cur, true)
		group.Verb, group.Reordered = "reordered", true
		*out = append(*out, group)
		return
	}
	pairs := lcsPairs(old, cur)
	matchedOld, matchedCur := make(map[int]bool, len(pairs)), make(map[int]bool, len(pairs))
	for _, pair := range pairs {
		matchedOld[pair.old], matchedCur[pair.cur] = true, true
	}
	parent := append([]ApprovalPathPart(nil), path...)
	for i, item := range old {
		if matchedOld[i] {
			continue
		}
		entryPath := append(append([]ApprovalPathPart(nil), path...), ApprovalPathPart{Index: i, ArrayIndex: true})
		group := makeApprovalGroup(entryPath, item, true, nil, false)
		group.Verb, group.Removed, group.arrayPath, group.arrayIndex = "removed", true, parent, i
		*out = append(*out, group)
	}
	for i, item := range cur {
		if matchedCur[i] {
			continue
		}
		entryPath := append(append([]ApprovalPathPart(nil), path...), ApprovalPathPart{Index: i, ArrayIndex: true})
		group := makeApprovalGroup(entryPath, nil, false, item, true)
		group.Verb, group.Added, group.arrayPath, group.arrayIndex = "added", true, parent, i
		*out = append(*out, group)
	}
}

type approvalPair struct{ old, cur int }

func lcsPairs(old, cur []any) []approvalPair {
	dp := make([][]int, len(old)+1)
	for i := range dp {
		dp[i] = make([]int, len(cur)+1)
	}
	for i := len(old) - 1; i >= 0; i-- {
		for j := len(cur) - 1; j >= 0; j-- {
			if approvalValueEqual(old[i], cur[j]) {
				dp[i][j] = 1 + dp[i+1][j+1]
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	remaining, oi, cj := dp[0][0], 0, 0
	pairs := make([]approvalPair, 0, remaining)
	for remaining > 0 {
		found := false
		for i := oi; i < len(old) && !found; i++ {
			for j := cj; j < len(cur); j++ {
				if approvalValueEqual(old[i], cur[j]) && 1+dp[i+1][j+1] == remaining {
					pairs = append(pairs, approvalPair{i, j})
					oi, cj, remaining, found = i+1, j+1, remaining-1, true
					break
				}
			}
		}
		if !found {
			break
		}
	}
	return pairs
}

func unmatchedItems(old, cur []any) (added []any, addedPositions []int, removed []any, removedPositions []int) {
	pairs := lcsPairs(old, cur)
	matchedOld, matchedCur := make([]bool, len(old)), make([]bool, len(cur))
	for _, p := range pairs {
		matchedOld[p.old], matchedCur[p.cur] = true, true
	}
	for i, v := range old {
		if !matchedOld[i] {
			removed = append(removed, jsonx.DeepCopy(v))
			removedPositions = append(removedPositions, i+1)
		}
	}
	for i, v := range cur {
		if !matchedCur[i] {
			added = append(added, jsonx.DeepCopy(v))
			addedPositions = append(addedPositions, i+1)
		}
	}
	return added, addedPositions, removed, removedPositions
}

func sameMultiset(old, cur []any) bool {
	if len(old) != len(cur) {
		return false
	}
	counts := make(map[string]int, len(old))
	for _, v := range old {
		key, ok := approvalValueKey(v)
		if !ok {
			return false
		}
		counts[key]++
	}
	for _, v := range cur {
		key, ok := approvalValueKey(v)
		if !ok || counts[key] == 0 {
			return false
		}
		counts[key]--
	}
	return true
}

func approvalValueEqual(a, b any) bool {
	ka, oka := approvalValueKey(a)
	kb, okb := approvalValueKey(b)
	return oka && okb && ka == kb
}

func approvalValueKey(v any) (string, bool) {
	text, err := jsonx.DumpsSnapshot(v)
	return text, err == nil
}

func listHasTable(values []any) bool {
	for _, value := range values {
		if _, ok := value.(*jsonx.OrderedMap); ok {
			return true
		}
	}
	return false
}

func formatApprovalPath(path []ApprovalPathPart) string {
	var b strings.Builder
	for _, part := range path {
		if part.ArrayIndex {
			fmt.Fprintf(&b, "[%d]", part.Index+1)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		if bareTOMLKey(part.Key) {
			b.WriteString(part.Key)
		} else {
			writeTOMLBasicString(&b, part.Key)
		}
	}
	return b.String()
}

func bareTOMLKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func writeTOMLBasicString(b *strings.Builder, value string) {
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0xffff {
				fmt.Fprintf(b, `\u%04X`, r)
			} else if r > 0xffff {
				fmt.Fprintf(b, `\U%08X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func applyApprovalGroups(root **jsonx.OrderedMap, groups []ApprovalChangeGroup) {
	var arrayGroups []ApprovalChangeGroup
	var other []ApprovalChangeGroup
	for _, group := range groups {
		if group.arrayPath != nil {
			arrayGroups = append(arrayGroups, group)
		} else {
			other = append(other, group)
		}
	}
	for _, group := range other {
		applyApprovalEdit(root, group.path, group.Current, group.HasCurrent)
	}
	// Array-table removals use original indexes and therefore run descending; additions
	// use final indexes and run ascending after all removals.
	sort.SliceStable(arrayGroups, func(i, j int) bool {
		a, b := arrayGroups[i], arrayGroups[j]
		if formatApprovalPath(a.arrayPath) != formatApprovalPath(b.arrayPath) {
			return formatApprovalPath(a.arrayPath) < formatApprovalPath(b.arrayPath)
		}
		if a.Removed != b.Removed {
			return a.Removed
		}
		if a.Removed {
			return a.arrayIndex > b.arrayIndex
		}
		return a.arrayIndex < b.arrayIndex
	})
	for _, group := range arrayGroups {
		value, ok := approvalValueAt(*root, group.arrayPath)
		list, valid := value.([]any)
		if !ok || !valid {
			continue
		}
		if group.Removed {
			if group.arrayIndex >= 0 && group.arrayIndex < len(list) {
				list = append(list[:group.arrayIndex], list[group.arrayIndex+1:]...)
			}
		} else {
			at := group.arrayIndex
			if at < 0 {
				at = 0
			}
			if at > len(list) {
				at = len(list)
			}
			list = append(list, nil)
			copy(list[at+1:], list[at:])
			list[at] = jsonx.DeepCopy(group.Current)
		}
		applyApprovalEdit(root, group.arrayPath, list, true)
	}
}

func applyApprovalEdit(root **jsonx.OrderedMap, path []ApprovalPathPart, value any, exists bool) {
	if len(path) == 0 {
		if m, ok := value.(*jsonx.OrderedMap); ok && exists {
			*root = jsonx.DeepCopyMap(m)
		}
		return
	}
	parent, ok := approvalValueAt(*root, path[:len(path)-1])
	if len(path) == 1 {
		parent, ok = *root, true
	}
	if !ok {
		return
	}
	last := path[len(path)-1]
	if last.ArrayIndex {
		list, ok := parent.([]any)
		if !ok || last.Index < 0 || last.Index >= len(list) {
			return
		}
		if exists {
			list[last.Index] = jsonx.DeepCopy(value)
		}
		return
	}
	m, ok := parent.(*jsonx.OrderedMap)
	if !ok {
		return
	}
	if exists {
		m.Set(last.Key, jsonx.DeepCopy(value))
	} else {
		m.Delete(last.Key)
	}
}

func approvalValueAt(root *jsonx.OrderedMap, path []ApprovalPathPart) (any, bool) {
	var value any = root
	for _, part := range path {
		if part.ArrayIndex {
			list, ok := value.([]any)
			if !ok || part.Index < 0 || part.Index >= len(list) {
				return nil, false
			}
			value = list[part.Index]
		} else {
			m, ok := value.(*jsonx.OrderedMap)
			if !ok {
				return nil, false
			}
			var has bool
			value, has = m.Get(part.Key)
			if !has {
				return nil, false
			}
		}
	}
	return value, true
}
