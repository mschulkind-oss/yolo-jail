package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// profiledeclarations_test.go pins `yolo check`'s prediction of the launch's undeclared-profile
// refusal (profiledeclarations.go), at the call site: sectionMergedConfig. A config selecting a
// profile no selected pack declares FAILs with the launch's own next step; the same config with
// the declaring pack selected passes. Deleting the call in sectionMergedConfig fails the first.

func runMergedConfigSection(t *testing.T, cfgJSON string) string {
	t.Helper()
	cfg := capCfg(t, cfgJSON)
	empty := jsonx.NewOrderedMap()
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, bool) { return "", false },
	}
	o.sectionMergedConfig(r, cfg, t.TempDir(), empty, empty, nil, false)
	return stripANSI(buf.String())
}

func TestCheckFailsAProfileNoSelectedPackDeclares(t *testing.T) {
	capabilityHome(t, `{"packs": ["pi"]}`)
	got := runMergedConfigSection(t, `{"profile": {"pi": "copilot"}}`)
	for _, want := range []string{"[FAIL]", `profile "copilot" selected for pi`,
		`no profile named "copilot" is declared`, "run `yolo pack add copilot-auth`"} {
		if !strings.Contains(got, want) {
			t.Errorf("check must FAIL with the launch's refusal, lacking %q:\n%s", want, got)
		}
	}
}

func TestCheckTypesTheDeclarationForABuiltInName(t *testing.T) {
	capabilityHome(t, `{"packs": ["pi"]}`)
	got := runMergedConfigSection(t, `{"profile": {"pi": "groq"}}`)
	if !strings.Contains(got, "[FAIL]") || !strings.Contains(got, "pi has groq built in") {
		t.Errorf("check must FAIL typing out the declaration:\n%s", got)
	}
}

// THE ONE SELECTION NAMESPACE (config.SelectableProfiles, OQ-RC3), at the merged-config
// section: a provider the selected local pack declares, selected by its own name with no profile
// over it, is what the launch resolves pi onto. So check neither FAILs the name as undeclared
// (profileDeclarationGap) nor predicts an unmet capability the provider declares
// (capabilityGap's census). Fails if either reader resolves over the declared profiles alone.
func TestCheckResolvesAProviderSelectedByItsOwnName(t *testing.T) {
	capabilityHome(t, `{"packs": ["pi"]}`)
	local := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "local")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "pack.json"), []byte(`{"name":"local","contributes":[
		{"kind":"provider","name":"acmecloud","platform":"acmecloud","capabilities":["web_search"]}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	got := runMergedConfigSection(t, `{"profile": {"pi": "acmecloud"}, "required_capabilities": ["web_search"]}`)
	for _, bad := range []string{`no profile named "acmecloud"`, "web_search"} {
		if strings.Contains(got, bad) {
			t.Errorf("pi selected acmecloud by its provider name, so check must not report %q:\n%s", bad, got)
		}
	}
}

func TestCheckPassesAProfileTheSelectedPackDeclares(t *testing.T) {
	capabilityHome(t, `{"packs": ["pi", "copilot-auth"]}`)
	got := runMergedConfigSection(t, `{"profile": {"pi": "copilot"}}`)
	if strings.Contains(got, "no profile named") {
		t.Errorf("copilot-auth is selected and declares copilot, so check must pass:\n%s", got)
	}
}
