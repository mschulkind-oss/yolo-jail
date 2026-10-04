package cli

// hostcapabilities_test.go pins OQ-CAP2's gate at the HOST notch (hostcapabilities.go): `yolo host
// -- <cmd>` refuses a user config whose `required_capabilities` nothing in the launch satisfies, as
// a jail launch does, and in the same words. Every cell drives hostMain to the exec, with the exec
// replaced (hostGateRun), so deleting the gate's call in hostLaunch fails the refusing cells.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestHostLaunchAsksTheCapabilityGate(t *testing.T) {
	cases := []struct {
		name, cfg, agent string
		hatch            bool
		wantRC           int
		wantExec         bool
		want             []string
	}{
		{name: "nothing satisfies it", cfg: `{"required_capabilities": ["web_search"]}`, agent: "true",
			wantRC: 1, want: []string{
				"yolo host: Refusing to launch: config.required_capabilities declares 'web_search', " +
					"and nothing this config or its selected packs declare satisfies it.",
				"  config.required_capabilities is written at ",
				"or launch anyway with " + config.AllowUnmetCapabilitiesEnv + "=1."}},
		{name: "claude's own login satisfies it", cfg: `{"packs": ["claude"], "required_capabilities": ["web_search"]}`,
			agent: "claude", wantExec: true},
		{name: "pi on no profile does not", cfg: `{"packs": ["pi"], "required_capabilities": ["web_search"]}`,
			agent: "pi", wantRC: 1, want: []string{"declares 'web_search'"}},
		{name: "an mcp server satisfies it", cfg: `{"required_capabilities": ["web_search"], "mcp_servers": ` +
			`{"t": {"command": "x", "provides": "web_search"}}}`, agent: "true", wantExec: true},
		{name: "the hatch continues loudly", cfg: `{"required_capabilities": ["web_search"]}`, agent: "true",
			hatch: true, wantExec: true, want: []string{
				"yolo host: Warning: " + config.AllowUnmetCapabilitiesEnv + " is set — CONTINUING with " +
					"required capability 'web_search'"}},
		{name: "the baseline needs nothing", cfg: `{"required_capabilities": ["code_editing"]}`, agent: "true",
			wantExec: true},
		{name: "a malformed value is refused, not read as nothing", cfg: `{"required_capabilities": "web_search"}`,
			agent: "true", wantRC: 1, want: []string{
				"yolo host: refusing to launch: config.required_capabilities: expected a list",
				"`yolo check` reports the same problem."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.hatch {
				t.Setenv(config.AllowUnmetCapabilitiesEnv, "1")
			} else {
				t.Setenv(config.AllowUnmetCapabilitiesEnv, "")
			}
			rc, env, errs := hostGateRun(t, tc.cfg, nil, nil, tc.agent)
			if rc != tc.wantRC || (env != nil) != tc.wantExec {
				t.Fatalf("rc=%d exec=%v, want rc=%d exec=%v\n%s", rc, env != nil, tc.wantRC, tc.wantExec, errs)
			}
			for _, w := range tc.want {
				if !strings.Contains(errs, w) {
					t.Errorf("stderr lacks %q:\n%s", w, errs)
				}
			}
			if len(tc.want) == 0 && strings.Contains(errs, "required capabilit") {
				t.Errorf("a satisfied launch mentions the gate:\n%s", errs)
			}
		})
	}
}

// THE GATE ASKS BEFORE THE RENDER GATE: with `host_apply_on_launch` on, a launch the capability
// gate refuses leaves the home exactly as it found it, rather than auto-applying a render of the
// config it is about to refuse (hostapplygate.go's "a config the launch refuses is not rendered
// first"). The control launch, with the requirement met, does render: the hook is live, so the
// first assertion is about the order and not about a hook that never runs.
func TestARefusedCapabilityLaunchRendersNothingFirst(t *testing.T) {
	for _, tc := range []struct {
		name, require string
		refused       bool
	}{
		{"refused", `["web_search"]`, true},
		{"control: met", `["code_editing"]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.AllowUnmetCapabilitiesEnv, "")
			cfg := `{"packs": ["pi"], "host_apply_on_launch": true, "required_capabilities": ` + tc.require + `}`
			var before string
			rc, env, errs := hostGateRunIn(t, cfg, nil, nil, "pi", func(home string) {
				stubDeclaredBins(t)
				// The machine logs every launch writes (hostLaunchTrace) are no render, and
				// hashTree skips them; made here so their parents are not news either.
				if err := os.MkdirAll(filepath.Join(paths.GlobalStorageUnder(home), "logs"), 0o755); err != nil {
					t.Fatal(err)
				}
				before = hashTree(t, home)
			})
			home := os.Getenv("HOME")
			after := hashTree(t, home)
			if tc.refused {
				if rc != 1 || env != nil {
					t.Fatalf("rc=%d exec=%v, want the capability refusal\n%s", rc, env != nil, errs)
				}
				if after != before {
					t.Errorf("a launch the capability gate refused rendered into the home first:\n%s", errs)
				}
				if _, err := os.Stat(filepath.Join(home, ".pi")); err == nil {
					t.Errorf("the refused launch wrote pi's config:\n%s", errs)
				}
				return
			}
			if rc != 0 || env == nil {
				t.Fatalf("control launch: rc=%d exec=%v\n%s", rc, env != nil, errs)
			}
			if after == before {
				t.Fatalf("control launch: the apply hook rendered nothing, so the refused cell proves "+
					"nothing about the order:\n%s", errs)
			}
		})
	}
}
