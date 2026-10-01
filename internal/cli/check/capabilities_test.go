package check

// capabilities_test.go pins the launch-gate prediction two ways: the four outcomes of the
// section, and the call site — because a predictor nobody calls is this feature's own defect
// wearing a passing test suite. The census itself is config.UnmetCapabilities, and its rows are
// pinned in internal/config's capabilities_test.go.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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

// TestCapabilityGapOutcomes pins the ruling, and the middle rows are the ones that are a
// judgement rather than a mechanism.
func TestCapabilityGapOutcomes(t *testing.T) {
	gap := `{"required_capabilities": ["web_search"]}`

	t.Run("no gap says nothing at all", func(t *testing.T) {
		errs, warns := capabilityGap(capCfg(t, `{}`), "", nil)
		if len(errs) != 0 || len(warns) != 0 {
			t.Errorf("a satisfied gate must add no line — it is what keeps the byte-pinned "+
				"golden and its counts still: errs=%v warns=%v", errs, warns)
		}
	})

	t.Run("a gap with no hatch is a FAIL that names the remedies", func(t *testing.T) {
		errs, warns := capabilityGap(capCfg(t, gap), "", nil)
		if len(errs) != 1 || len(warns) != 0 {
			t.Fatalf("want one error and no warning, got errs=%v warns=%v", errs, warns)
		}
		for _, want := range []string{"web_search", "REFUSED", "provides", "selected pack",
			allowUnmetCapabilitiesEnv} {
			if !strings.Contains(errs[0], want) {
				t.Errorf("the prediction should name %q: %s", want, errs[0])
			}
		}
	})

	t.Run("a gap with the hatch set is a WARN, not silence and not a FAIL", func(t *testing.T) {
		errs, warns := capabilityGap(capCfg(t, gap), "1", nil)
		if len(errs) != 0 || len(warns) != 1 {
			t.Fatalf("with the hatch set the launch PROCEEDS, so a FAIL would be a false "+
				"prediction and silence would hide a real gap: errs=%v warns=%v", errs, warns)
		}
		if !strings.Contains(warns[0], "CONTINUE") || !strings.Contains(warns[0], allowUnmetCapabilitiesEnv) {
			t.Errorf("the warning must say the launch continues and why: %s", warns[0])
		}
	})

	t.Run("a gap the census could not prove is a WARN, never a FAIL", func(t *testing.T) {
		unread := func() ([]*packload.Pack, bool) { return nil, false }
		errs, warns := capabilityGap(capCfg(t, gap), "", unread)
		if len(errs) != 0 || len(warns) != 1 {
			t.Fatalf("a selected pack the census could not read may be the satisfier, and the "+
				"launch does not refuse over it, so a FAIL would be a false prediction and "+
				"silence would hide what went unchecked: errs=%v warns=%v", errs, warns)
		}
		if !strings.Contains(warns[0], "Could not predict") || !strings.Contains(warns[0], "web_search") {
			t.Errorf("the warning must say what it could not check: %s", warns[0])
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
	// An empty user scope, so no selected pack satisfies the name: the census reads the user
	// config's `packs`, and the jail running this test selects claude, which would.
	capabilityHome(t, `{}`)

	t.Run("no hatch", func(t *testing.T) {
		var buf bytes.Buffer
		r := newReporter(&buf, false)
		// LookPath finds nothing, so the runtime probe takes the same no-runtime path
		// the golden test uses — this is a section test, not a runtime test.
		o := &Options{
			Getenv:   func(string) string { return "" },
			LookPath: func(string) (string, bool) { return "", false },
		}
		o.sectionMergedConfig(r, cfg, t.TempDir(), empty, empty, false)
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
		o.sectionMergedConfig(r, cfg, t.TempDir(), empty, empty, false)
		got := stripANSI(buf.String())
		if !strings.Contains(got, "[WARN]") || !strings.Contains(got, "required_capabilities") {
			t.Errorf("with the hatch set the gap must still be reported, as a WARN:\n%s", got)
		}
		if strings.Contains(got, "[FAIL] config.required_capabilities") {
			t.Errorf("the hatch means the launch proceeds, so this must not be a FAIL:\n%s", got)
		}
	})
}

// capabilityHome points HOME at a fresh directory whose user config is body, so the pack
// selection the census resolves is exactly the one the test wrote.
func capabilityHome(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMergedConfigSectionCountsASelectedPacksCapability is the prediction's half of the pack
// census, at the call site: claude is selected, its pack declares web_search for claude's own
// login, and the launch lets this config through, so `check` must not predict a refusal. Its
// control is the section test above, whose user scope selects nothing and still FAILs.
func TestMergedConfigSectionCountsASelectedPacksCapability(t *testing.T) {
	capabilityHome(t, `{"packs": ["claude"]}`)
	cfg := capCfg(t, `{"required_capabilities": ["web_search"]}`)
	empty := jsonx.NewOrderedMap()
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, bool) { return "", false },
	}
	o.sectionMergedConfig(r, cfg, t.TempDir(), empty, empty, false)
	got := stripANSI(buf.String())
	if strings.Contains(got, "required_capabilities") {
		t.Errorf("claude is selected and declares web_search, so the launch's gate passes and "+
			"check must say nothing about it:\n%s", got)
	}
}
