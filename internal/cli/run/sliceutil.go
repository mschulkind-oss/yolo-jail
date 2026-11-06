package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func shquoteJoin(args []string) string { return shquote.Join(args) }

func shquoteJoinDebug(args []string) string { return shquote.Join(args) }

// writeTracking wraps runtime.WriteLaunchRecord.
func writeTracking(rec runtime.LaunchRecord) error {
	return runtime.WriteLaunchRecord(rec)
}

// indexOfSlice returns the index of the first occurrence of target in s, or -1.
func indexOfSlice(s []string, target string) int {
	for i, v := range s {
		if v == target {
			return i
		}
	}
	return -1
}

// insertStrsAt inserts vs at index i.
func insertStrsAt(s []string, i int, vs []string) []string {
	out := make([]string, 0, len(s)+len(vs))
	out = append(out, s[:i]...)
	out = append(out, vs...)
	out = append(out, s[i:]...)
	return out
}
