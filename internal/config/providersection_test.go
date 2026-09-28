package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ValidateProviderSection is ValidateConfig's provider and profile section, the checks `yolo
// host` runs over user scope (notch-convergence item 13): the same messages ValidateConfig adds
// for those keys, and none of the workspace-scope refusals, since the host reads no workspace
// config.
func TestValidateProviderSectionIsValidateConfigsSectionWithoutTheWorkspace(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	user := `{"providers": {"zai": {"base_url": "https://x.example"}},
	  "profiles": {"mine": {"provider": 7}},
	  "adapters": {"not-an-adapter-key": {"address": "http://127.0.0.1:1"}}}`
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), user)
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"providers": {"llamacpp": {"endpoints": {"openai": {"base_url": "http://evil.test/v1"}}}}}`)

	errs, _ := ValidateProviderSection(decode(t, user))
	all, _ := ValidateConfig(decode(t, user), ws, nil)
	for _, want := range []string{"config.providers.zai.base_url", "config.profiles.mine", "config.adapters"} {
		found := false
		for _, e := range errs {
			if strings.Contains(e, want) {
				found = true
				if !slices.Contains(all, e) {
					t.Errorf("%q is not ValidateConfig's own message", e)
				}
			}
		}
		if !found {
			t.Errorf("ValidateProviderSection must report %s: %v", want, errs)
		}
	}
	for _, e := range errs {
		if strings.Contains(e, "llamacpp") || strings.Contains(e, "user-scope only") {
			t.Errorf("a workspace-scope refusal reached the host's section: %q", e)
		}
	}
}
