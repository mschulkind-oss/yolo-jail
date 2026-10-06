package macosuser

// setupcensus_test.go is the orchestrator's half of the macos-user notice block, checked against
// the setup census (internal/setupcensus; docs/design/backend-parity.md OQ-BP-1, BP-D19). The run
// arm's half, and the route table naming which printer owns each census-Warned cell, are
// internal/cli/run's setupcensusnotices_test.go. Driven through RunMacosUser's dry run rather
// than buildPlan alone, so deleting the call is as visible as deleting the line.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/setupcensus"
)

func TestTheOrchestratorWarnsWhatTheCensusWarns(t *testing.T) {
	for _, c := range []struct {
		path, key, value, says string
	}{
		{"resources", "resources", `{"memory": "4g"}`, "resources are NOT enforced on macos-user"},
		{"resources.pids_limit", "resources", `{"pids_limit": 100}`, "so pids_limit are read"},
		{"resources.io", "resources", `{"io": "idle"}`, "so io are read"},
		{"per_side_paths", "per_side_paths", `["build"]`, "per_side_paths is NOT enforced on macos-user"},
		{"cache_relocations", "cache_relocations", `{"npm": "/Volumes/big/npm"}`,
			"cache_relocations are NOT implemented on macos-user"},
	} {
		t.Run(c.path, func(t *testing.T) {
			e, ok := setupcensus.Find(c.path)
			if !ok || e.MacosUser.Disposition != setupcensus.Warned {
				t.Fatalf("the census marks %s %s on macos-user, and this orchestrator line exists: "+
					"one of them is wrong", c.path, e.MacosUser.Disposition)
			}
			v, err := json5.Decode([]byte(c.value))
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			d := mockDeps(nil)
			d.IsMacOS = func() bool { return false } // a dry run works off-macOS
			d.Out = &buf
			opts := newOpts("/Users/Shared/yolo/proj")
			opts.DryRun = true
			opts.Config = jsonx.NewOrderedMap()
			opts.Config.Set(c.key, v)
			RunMacosUser(d, opts)
			if !strings.Contains(buf.String(), c.says) {
				t.Errorf("the census marks %s Warned on macos-user and the dry run printed no %q:\n%s",
					c.path, c.says, buf.String())
			}
		})
	}
}
