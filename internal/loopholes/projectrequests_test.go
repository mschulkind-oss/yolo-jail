package loopholes

import (
	"bytes"
	"fmt"
	"testing"
)

// TestLoopholesListReadsTheProjectsRequestsTable: `yolo loopholes list` reads a TOML project's
// `[requests.<pack>]` as the launch does: its `off = true` keeps an off-switch loophole off
// (docs/reference/config/rulings.md LC-D15), and its request-scope setting replaces the user's
// for this project (LG-D15, LC-D17). Either read deleted from loopholesWithConfigAndBlock fails it.
func TestLoopholesListReadsTheProjectsRequestsTable(t *testing.T) {
	isolateDirs(t)
	SetPackModules(shippedModules(t, "aws-auth", "serial"))
	var out, errBuf bytes.Buffer
	deps := cmdDeps(t, &out, &errBuf, `{"packs": ["aws-auth", "serial"], "allow": {"serial": {"default_baud": 115200}}}`,
		`{"requests": {"aws-auth": {"off": true}, "serial": {"default_baud": 9600}}}`)
	set, settings := loopholesWithConfigAndBlock(deps, true)
	listed := map[string]bool{}
	for _, lp := range set.All() {
		listed[lp.Name] = true
	}
	if !listed["serial"] || !listed["aws-auth"] {
		t.Fatalf("control: serial and aws-auth are not both listed: %v", listed)
	}
	for _, lp := range set.Enabled() {
		if lp.Name == "aws-auth" {
			t.Errorf("the project's requests.aws-auth.off did not keep aws-auth off in the list")
		}
	}
	got := SettingsFromConfig(settings, "serial")
	if got == nil {
		t.Fatalf("serial has no settings in the list's block: %v", settings)
	}
	if v, _ := got.Get("default_baud"); fmt.Sprint(v) != "9600" {
		t.Errorf("serial's default_baud = %v, want the project's 9600 over the user's 115200", v)
	}
}
