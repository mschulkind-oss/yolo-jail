package cli

import (
	"bytes"
	"strings"
	"testing"
)

// packlintlegacy_test.go pins `yolo pack lint`'s half of the legacy manifest window
// (docs/design/legacy-config-compat.md LC-D10): a legacy manifest's `default_enabled` and
// workspace-scope settings still read, with v0.15.1 meaning, so lint passes them, and says
// once per key what each reads as and what `yolo pack migrate` will do with it.
func TestLintPassesALegacyManifestWindowKeyWithANote(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		want           []string
	}{
		{"default_enabled true", `{"name": "acme-proxy", "transport": "none", "default_enabled": true}`,
			[]string{"default_enabled: true reads as nothing", "yolo pack migrate` drops the key"}},
		{"default_enabled false", `{"name": "acme-proxy", "transport": "none", "default_enabled": false}`,
			[]string{"default_enabled: false reads as", "[[grants]]"}},
		{"workspace scope", `{"name": "acme-proxy", "transport": "none",
		  "settings": {"greeting": {"type": "string", "scope": "workspace", "default": "hi"}}}`,
			[]string{`settings.greeting.scope = "workspace" reads as "request"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeLoopholeModulePack(t, tc.manifest)
			var out, errw bytes.Buffer
			rc := packMain([]string{"lint", dir}, &out, &errw, false)
			if rc != 0 {
				t.Fatalf("lint failed a manifest the legacy window reads (rc=%d):\n%s%s", rc, out.String(), errw.String())
			}
			for _, w := range tc.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("lint output does not say %q:\n%s", w, out.String())
				}
			}
		})
	}
}
