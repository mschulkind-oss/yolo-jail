package check

// capabilities_test.go pins the launch-gate prediction three ways: the census itself, the
// three outcomes of the section, and the call site — because a predictor nobody calls is
// this feature's own defect wearing a passing test suite.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func capCfg(t *testing.T, src string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(src))
	if err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("fixture is not an object: %T", v)
	}
	return m
}

func TestUnmetCapabilitiesCensus(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want []string
	}{
		{"nothing declared", `{}`, nil},
		{"a requirement nothing satisfies", `{"required_capabilities": ["web_search"]}`,
			[]string{"web_search"}},
		{"satisfied by an mcp server's provides", `{
			"required_capabilities": ["web_search"],
			"mcp_servers": {"tavily": {"command": "npx", "provides": "web_search"}}}`, nil},
		{"satisfied by a provider's capabilities", `{
			"required_capabilities": ["web_search"],
			"providers": {"zai": {"capabilities": ["web_search"]}}}`, nil},
		{"the baseline is always met", `{
			"required_capabilities": ["code_editing", "command_execution"]}`, nil},
		// The null cases are the whole reason the census reads merged VALUES rather than
		// key sets: a null removes the entry instead of running it, so it satisfies
		// nothing — otherwise a workspace `"tavily": null` would keep satisfying the
		// capability off the user-level entry it just deleted.
		{"a null-removed server satisfies nothing", `{
			"required_capabilities": ["web_search"],
			"mcp_servers": {"tavily": null}}`, []string{"web_search"}},
		{"a null-removed provider satisfies nothing", `{
			"required_capabilities": ["web_search"],
			"providers": {"zai": null}}`, []string{"web_search"}},
		{"declaration order and de-duplication", `{
			"required_capabilities": ["b", "a", "b"]}`, []string{"b", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := unmetCapabilities(capCfg(t, tc.cfg))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("unmetCapabilities = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCapabilityGapOutcomes pins the ruling, and the middle row is the one that is a
// judgement rather than a mechanism.
func TestCapabilityGapOutcomes(t *testing.T) {
	gap := `{"required_capabilities": ["web_search"]}`

	t.Run("no gap says nothing at all", func(t *testing.T) {
		errs, warns := capabilityGap(capCfg(t, `{}`), "")
		if len(errs) != 0 || len(warns) != 0 {
			t.Errorf("a satisfied gate must add no line — it is what keeps the byte-pinned "+
				"golden and its counts still: errs=%v warns=%v", errs, warns)
		}
	})

	t.Run("a gap with no hatch is a FAIL that names the remedies", func(t *testing.T) {
		errs, warns := capabilityGap(capCfg(t, gap), "")
		if len(errs) != 1 || len(warns) != 0 {
			t.Fatalf("want one error and no warning, got errs=%v warns=%v", errs, warns)
		}
		for _, want := range []string{"web_search", "REFUSED", "provides", allowUnmetCapabilitiesEnv} {
			if !strings.Contains(errs[0], want) {
				t.Errorf("the prediction should name %q: %s", want, errs[0])
			}
		}
	})

	t.Run("a gap with the hatch set is a WARN, not silence and not a FAIL", func(t *testing.T) {
		errs, warns := capabilityGap(capCfg(t, gap), "1")
		if len(errs) != 0 || len(warns) != 1 {
			t.Fatalf("with the hatch set the launch PROCEEDS, so a FAIL would be a false "+
				"prediction and silence would hide a real gap: errs=%v warns=%v", errs, warns)
		}
		if !strings.Contains(warns[0], "CONTINUE") || !strings.Contains(warns[0], allowUnmetCapabilitiesEnv) {
			t.Errorf("the warning must say the launch continues and why: %s", warns[0])
		}
	})
}

// TestMergedConfigSectionReportsTheCapabilityGap is the call-site pin: the section test,
// in the style warningchannel_test.go established for a section that end-to-end cannot
// reach deterministically. Without it, capabilityGap could be perfect and never called —
// which is the defect this whole file closes, one layer up.
func TestMergedConfigSectionReportsTheCapabilityGap(t *testing.T) {
	cfg := capCfg(t, `{"required_capabilities": ["web_search"]}`)
	empty := jsonx.NewOrderedMap()

	t.Run("no hatch", func(t *testing.T) {
		var buf bytes.Buffer
		r := newReporter(&buf, false)
		// LookPath finds nothing, so the runtime probe takes the same no-runtime path
		// the golden test uses — this is a section test, not a runtime test.
		o := &Options{
			Getenv:   func(string) string { return "" },
			LookPath: func(string) (string, bool) { return "", false },
		}
		o.sectionMergedConfig(r, cfg, t.TempDir(), empty, empty)
		got := stripANSI(buf.String())
		if !strings.Contains(got, "required_capabilities") || !strings.Contains(got, "[FAIL]") {
			t.Errorf("the section must FAIL on a gap the launch would refuse:\n%s", got)
		}
	})

	t.Run("hatch set", func(t *testing.T) {
		var buf bytes.Buffer
		r := newReporter(&buf, false)
		o := &Options{
			Getenv: func(k string) string {
				if k == allowUnmetCapabilitiesEnv {
					return "1"
				}
				return ""
			},
			LookPath: func(string) (string, bool) { return "", false },
		}
		o.sectionMergedConfig(r, cfg, t.TempDir(), empty, empty)
		got := stripANSI(buf.String())
		if !strings.Contains(got, "[WARN]") || !strings.Contains(got, "required_capabilities") {
			t.Errorf("with the hatch set the gap must still be reported, as a WARN:\n%s", got)
		}
		if strings.Contains(got, "[FAIL] config.required_capabilities") {
			t.Errorf("the hatch means the launch proceeds, so this must not be a FAIL:\n%s", got)
		}
	})
}
