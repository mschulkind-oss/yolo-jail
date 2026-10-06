package check

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestAMDCDISpecCheckUsesTheLaunchProbe: `yolo check`'s AMD CDI verdict comes from
// run.FindAMDCDISpec, the probe the launch also gates passthrough on (G28,
// docs/plans/setup-support-gaps.md). A spec at either of its paths is found, and a missing one
// fails with the command that writes it and what a launch does meanwhile.
func TestAMDCDISpecCheckUsesTheLaunchProbe(t *testing.T) {
	m, err := jsonx.Decode([]byte(`{"gpu": {"enabled": true, "vendor": "amd", "mode": "cdi"}}`))
	if err != nil {
		t.Fatal(err)
	}
	merged := m.(*jsonx.OrderedMap)

	for _, have := range append([]string{""}, run.AMDCDISpecPaths...) {
		t.Run("spec="+have, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var out bytes.Buffer
			opts := baseOptions(t, &out)
			opts.PathExists = func(p string) bool { return have != "" && p == have }
			r := newReporter(&out, false)
			opts.sectionGPUAmd(r, merged)

			var found, missing bool
			for _, f := range r.findings {
				if f.Status == "pass" && f.Message == "AMD CDI spec found: "+have {
					found = true
				}
				if f.Status == "fail" && strings.HasPrefix(f.Message, "No AMD CDI spec found") {
					missing = true
					if !strings.Contains(f.Note, run.AMDCDISpecGenerate) ||
						!strings.Contains(f.Note, "without GPU passthrough") {
						t.Errorf("the missing-spec note must name %q and what a launch does "+
							"meanwhile; got %q", run.AMDCDISpecGenerate, f.Note)
					}
				}
			}
			if have == "" && (!missing || found) {
				t.Errorf("no spec on the host, want one missing-spec FAIL:\n%s", out.String())
			}
			if have != "" && (!found || missing) {
				t.Errorf("spec at %s, want it reported found:\n%s", have, out.String())
			}
		})
	}
}
