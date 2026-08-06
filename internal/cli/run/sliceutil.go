package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func shquoteJoin(args []string) string { return shquote.Join(args) }

func shquoteJoinDebug(args []string) string { return shquote.Join(args) }

// redactSecretsForDebug returns a copy of argv with secret-bearing env values
// ("…_TOKEN=<value>") masked, so YOLO_DEBUG can print the launch argv without
// leaking the per-jail broker token (issue #31).
func redactSecretsForDebug(argv []string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if eq := strings.IndexByte(a, '='); eq > 0 && strings.HasSuffix(a[:eq], "_TOKEN") {
			out[i] = a[:eq+1] + "<redacted>"
		} else {
			out[i] = a
		}
	}
	return out
}

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

// insertStrsAt inserts vs at index i.
func insertStrsAt(s []string, i int, vs []string) []string {
	out := make([]string, 0, len(s)+len(vs))
	out = append(out, s[:i]...)
	out = append(out, vs...)
	out = append(out, s[i:]...)
	return out
}
