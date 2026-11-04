package configmigrate

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// DroppedDefault is one written value a conversion left out because it is what the key means when
// it is absent (config-migration.md §4 item 4, OQ-RC18).
type DroppedDefault struct {
	// Path is the new-vocabulary key, dotted; Value is what was written, compact JSON.
	Path, Value string
}

// DroppedInert is one old key a conversion left out because it says nothing a launch reads, such
// as an off switch for a loophole no selected pack runs.
type DroppedInert struct {
	// Path is the old key, dotted; Value is what was written, compact JSON; Why says why.
	Path, Value, Why string
}

// writerCounts says, per default key, how many files of the graph write it. A value is dropped
// only from the one file that writes it: with two writers, the lower layer's value is what
// remains when the higher one's is dropped, and the file cannot know which that is.
type writerCounts map[string]int

func defaultKey(path []string) string { return dotted(path) }

func lookup(values *jsonx.OrderedMap, path []string) (any, bool) {
	node := values
	for i, step := range path {
		v, ok := node.Get(step)
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			return v, true
		}
		next, isMap := v.(*jsonx.OrderedMap)
		if !isMap {
			return nil, false
		}
		node = next
	}
	return nil, false
}

// add counts one writer for each default key that values writes.
func (w writerCounts) add(values *jsonx.OrderedMap) {
	for _, d := range config.WrittenDefaults() {
		if _, ok := lookup(values, d.Path); ok {
			w[defaultKey(d.Path)]++
		}
	}
}

// dropDefaults removes from values every key that holds exactly its default and that no other
// file writes, and any table that holds nothing afterwards. The plan's equality check, which
// fills every default on both sides (config.ApplyWrittenDefaults), is what proves each drop.
func dropDefaults(values *jsonx.OrderedMap, writers writerCounts) []DroppedDefault {
	var out []DroppedDefault
	for _, d := range config.WrittenDefaults() {
		if writers[defaultKey(d.Path)] != 1 {
			continue
		}
		v, ok := lookup(values, d.Path)
		if !ok {
			continue
		}
		got, err := jsonx.DumpsSnapshot(v)
		if err != nil {
			continue
		}
		if want, _ := jsonx.DumpsSnapshot(d.Value); got != want {
			continue
		}
		out = append(out, DroppedDefault{Path: dotted(d.Path), Value: describe(v)})
		removePath(values, d.Path)
	}
	return out
}

// removePath deletes the key at path and every parent table the deletion leaves empty.
func removePath(values *jsonx.OrderedMap, path []string) {
	if len(path) == 1 {
		values.Delete(path[0])
		return
	}
	v, _ := values.Get(path[0])
	child, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return
	}
	removePath(child, path[1:])
	if child.Len() == 0 {
		values.Delete(path[0])
	}
}
