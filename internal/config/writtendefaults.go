package config

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// writtendefaults.go is the one place the keys whose absence means a fixed value say what that
// value is. The readers (run's resolveNetModeSource, PerfLoggingEnabled, BriefingProvenance and
// UpdateCheckEnabled) return these constants for an absent key, and `yolo config migrate`
// (internal/configmigrate) drops a written value equal to one, so the file and the loader cannot
// disagree about what "the default" is: there is no second list. TestWrittenDefaultsAreWhatTheReadersAnswerForAnAbsentKey
// fails if a reader stops answering its constant.
//
// A key joins only when its reader's answer for an absent key is a fixed value and its absence is
// the same in every scope that may write it. Facts that follow from another key's value (a
// maker from a model id) are not here: the legacy provider composer does not infer them, so
// dropping one would change meaning (docs/reference/config/rulings.md#RC-D26).

const (
	// DefaultNetworkMode is `network.mode` when no flag and no config names one.
	DefaultNetworkMode = "bridge"
	// DefaultPerfLogging is `perf_logging` when absent.
	DefaultPerfLogging = false
	// DefaultBriefingProvenance is `briefing_provenance` when absent.
	DefaultBriefingProvenance = false
	// DefaultUpdateCheck is `update_check` when absent.
	DefaultUpdateCheck = true
)

// WrittenDefault is one key and the value its absence means.
type WrittenDefault struct {
	Path  []string
	Value any
}

// WrittenDefaults lists every key whose absence means a fixed value, in the readable vocabulary.
func WrittenDefaults() []WrittenDefault {
	return []WrittenDefault{
		{Path: []string{"network", "mode"}, Value: DefaultNetworkMode},
		{Path: []string{perfLoggingKey}, Value: DefaultPerfLogging},
		{Path: []string{briefingProvenanceKey}, Value: DefaultBriefingProvenance},
		{Path: []string{updateCheckKey}, Value: DefaultUpdateCheck},
	}
}

// ApplyWrittenDefaults fills every absent key of WrittenDefaults into cfg, in place. It is what a
// reader's absent-key answer amounts to, spelled as a value, so two configs that differ only by
// a written default compare equal after it.
func ApplyWrittenDefaults(cfg *jsonx.OrderedMap) {
	for _, d := range WrittenDefaults() {
		node := cfg
		for _, step := range d.Path[:len(d.Path)-1] {
			next, ok := node.Get(step)
			child, isMap := next.(*jsonx.OrderedMap)
			if !ok || !isMap {
				child = jsonx.NewOrderedMap()
				node.Set(step, child)
			}
			node = child
		}
		last := d.Path[len(d.Path)-1]
		if _, ok := node.Get(last); !ok {
			node.Set(last, d.Value)
		}
	}
}
