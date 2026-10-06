package macosuser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// resourcesLine drives the real buildPlan and returns its "resources are NOT enforced"
// line, or "" when it printed none.
func resourcesLine(t *testing.T, res *jsonx.OrderedMap) string {
	t.Helper()
	var buf bytes.Buffer
	deps := mockDeps(nil)
	deps.Out = &buf
	opts := newOpts("/Users/Shared/proj")
	if res != nil {
		opts.Config.Set("resources", res)
	}
	buildPlan(deps, opts, nil)
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "resources are NOT enforced on macos-user") {
			return line
		}
	}
	return ""
}

// TestMacosUserResourcesLineLeavesOutANormalIo is IO-D8: an `io` resolving to "normal" makes
// no call on any backend, so it is honored here already and the line does not name it —
// and a resources block holding nothing else prints no line at all. Any other value is
// named, because nothing applies it on this backend until build step 5.
func TestMacosUserResourcesLineLeavesOutANormalIo(t *testing.T) {
	for _, normal := range []any{"normal", nil, jsonx.NewOrderedMap()} {
		res := jsonx.NewOrderedMap()
		res.Set("io", normal)
		if line := resourcesLine(t, res); line != "" {
			t.Errorf("io=%v alone printed %q, want no line", normal, line)
		}
		res.Set("memory", "8g")
		line := resourcesLine(t, res)
		if !strings.Contains(line, "— memory.") {
			t.Errorf("io=%v beside memory: %q, want memory named and io left out", normal, line)
		}
	}
	for _, declared := range []string{"low", "idle"} {
		res := jsonx.NewOrderedMap()
		res.Set("io", declared)
		if line := resourcesLine(t, res); !strings.Contains(line, "— io.") {
			t.Errorf("io=%q must be named: %q", declared, line)
		}
	}
}
