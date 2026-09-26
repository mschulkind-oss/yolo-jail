package entrypoint

// opencodemenu_test.go pins OQ-CN4's menu half for opencode
// (docs/design/provider-credential-scope.md, "both, named separately"), through the boot
// render of the real pi, opencode and zai packs (pioencodeRender), over the fixture
// credentialderive_test.go declares.

import "testing"

// opencode's own hard key, enabled_providers, names the provider the profile selected —
// ONLY that provider is enabled, so opencode's menu stops offering the providers whose
// credentials the gate now withholds from it. It rides the selection beside `model`, so a
// deselect clears it with the model, and nothing selected writes none.
func TestOpencodeMenuFollowsTheSelectedProvider(t *testing.T) {
	r := newPioencodeRender(t, credentialListJSON)
	r.wireProfiles(`{"zai": {"provider": "zai", "model": "glm-5.3"}}`)
	r.render(t, `{"opencode":"zai"}`)
	got, _ := r.ocConfig(t)["enabled_providers"].([]any)
	if len(got) != 1 || got[0] != "zai" {
		t.Errorf("opencode.json enabled_providers = %#v, want [\"zai\"] — the provider the "+
			"profile selected, and nothing else", r.ocConfig(t)["enabled_providers"])
	}

	r.render(t, ``)
	if v, present := r.ocConfig(t)["enabled_providers"]; present {
		t.Errorf("after deselection opencode.json still narrows its menu: %#v", v)
	}
}
