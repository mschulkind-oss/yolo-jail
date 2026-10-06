package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestAMDCDISpecCheckUsesTheLaunchProbe: `yolo check`'s AMD CDI verdict comes from
// run.FindAMDCDISpec, the probe the launch also gates passthrough on (G28,
// docs/plans/setup-support-gaps.md). A spec is found by kind under any name podman's CDI
// registry would load, an NVIDIA spec is not mistaken for one, and a missing one fails with
// where it looked, the command that writes it and what a launch does meanwhile.
func TestAMDCDISpecCheckUsesTheLaunchProbe(t *testing.T) {
	m, err := jsonx.Decode([]byte(`{"gpu": {"enabled": true, "vendor": "amd", "mode": "cdi"}}`))
	if err != nil {
		t.Fatal(err)
	}
	merged := m.(*jsonx.OrderedMap)
	const amd = "kind: amd.com/gpu\ncdiVersion: 0.6.0\n"

	for _, tc := range []struct {
		name, path, body, want string
	}{
		{"none", "", "", ""},
		{"nvidia only", "/etc/cdi/nvidia.yaml", "kind: nvidia.com/gpu\n", ""},
		{"amd.json", "/etc/cdi/amd.json", `{"kind": "amd.com/gpu"}`, "/etc/cdi/amd.json"},
		{"amd.yaml in /var/run/cdi", "/var/run/cdi/amd.yaml", amd, "/var/run/cdi/amd.yaml"},
		{"another name", "/etc/cdi/rocm-gpus.yaml", amd, "/etc/cdi/rocm-gpus.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			root := t.TempDir()
			if tc.path != "" {
				full := filepath.Join(root, tc.path)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(tc.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			opts := baseOptions(t, &out)
			opts.cdiHostRoot = root
			r := newReporter(&out, false)
			opts.sectionGPUAmd(r, merged)

			var found, missing bool
			for _, f := range r.findings {
				if f.Status == "pass" && f.Message == "AMD CDI spec found: "+tc.want {
					found = true
				}
				if f.Status == "fail" && strings.HasPrefix(f.Message, "No AMD CDI spec found") {
					missing = true
					for _, want := range []string{run.AMDCDISpecGenerate, "without GPU passthrough",
						"/etc/cdi", "/var/run/cdi", run.AMDCDIKind} {
						if !strings.Contains(f.Note, want) {
							t.Errorf("the missing-spec note must name %q; got %q", want, f.Note)
						}
					}
					if strings.Contains(f.Note, root) {
						t.Errorf("the note names the probe's test root, not host paths: %q", f.Note)
					}
				}
			}
			if tc.want == "" && (!missing || found) {
				t.Errorf("no AMD spec on the host, want one missing-spec FAIL:\n%s", out.String())
			}
			if tc.want != "" && (!found || missing) {
				t.Errorf("spec at %s, want it reported found:\n%s", tc.want, out.String())
			}
		})
	}
}
