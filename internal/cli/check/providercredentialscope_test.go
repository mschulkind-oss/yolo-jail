package check

// providercredentialscope_test.go pins docs/plans/notch-convergence.md OQ-NC6 at `yolo check`:
// a workspace config that decides where a provider's credential goes (here, the variable a
// provider claims) FAILs the Merged Configuration section, naming the field and the user
// config to move it to, as every launch refuses it. The call-site pin for
// config.validateProviderCredentialScope, whose own cells live in internal/config.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestCheckRefusesAWorkspaceProviderCredentialVariable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	ws := t.TempDir()
	const wsConfig = `{"providers": {"zai": {"api_key_env_name": "GH_TOKEN"}}}`
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(wsConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	merged := capCfg(t, wsConfig)
	empty := jsonx.NewOrderedMap()

	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, bool) { return "", false },
	}
	o.sectionMergedConfig(r, merged, ws, empty, empty, nil, false)
	got := stripANSI(buf.String())
	for _, want := range []string{"[FAIL]", "config.providers.zai.api_key_env_name: user-scope only",
		"move it to " + paths.UserConfigPath()} {
		if !strings.Contains(got, want) {
			t.Errorf("yolo check must refuse a workspace api_key_env_name (%q missing):\n%s", want, got)
		}
	}
}
