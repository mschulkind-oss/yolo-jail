package runcmd

import (
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func shquoteJoin(args []string) string { return shquote.Join(args) }

func shquoteJoinDebug(args []string) string { return shquote.Join(args) }

// writeTracking wraps runtime.WriteContainerTracking.
func writeTracking(cname, workspaceResolved string) error {
	return runtime.WriteContainerTracking(cname, workspaceResolved)
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

// insertStrsAt inserts vs at index i (run_cmd[image_idx:image_idx] = [...]).
func insertStrsAt(s []string, i int, vs []string) []string {
	out := make([]string, 0, len(s)+len(vs))
	out = append(out, s[:i]...)
	out = append(out, vs...)
	out = append(out, s[i:]...)
	return out
}
