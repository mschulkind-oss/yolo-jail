package packs_test

import (
	"io/fs"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/packs"
)

// TestNoShippedPackHasAJSONManifest pins the conversion of the shipped packs to pack.toml: every
// shipped pack carries a pack.toml and no legacy manifest. There are no exceptions: the meanings
// TOML once could not say, "no package for this manager", "declared with no default" and "delete
// this managed key", are `false` and `unset` (docs/reference/config/rulings.md#RC-D25).
func TestNoShippedPackHasAJSONManifest(t *testing.T) {
	entries, err := fs.ReadDir(packs.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	converted := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		for _, legacy := range []string{"pack.json", "pack.jsonc"} {
			if _, err := fs.Stat(packs.FS, name+"/"+legacy); err == nil {
				t.Errorf("packs/%s ships a %s: convert it with `yolo pack migrate packs/%s --yes "+
					"--keep-install-hints`", name, legacy, name)
			}
		}
		if _, err := fs.Stat(packs.FS, name+"/pack.toml"); err != nil {
			t.Errorf("packs/%s ships no pack.toml", name)
			continue
		}
		converted++
	}
	if converted == 0 {
		t.Fatal("no shipped pack carries a pack.toml: the test is not reading the tree it exists for")
	}
}

// TestShippedProfilesAreNotProviderNames pins OQ-RC3: a provider is selectable by its own name
// (config.LoadSelectableProfiles), so a shipped name for the provider it selects is a second
// spelling of the same selection and is deleted. Since RC-D69 a shipped pack writes no legacy
// `[[profile]]`: the names it adds are its providers' aliases (claude, codex, copilot) and
// variant names (bedrock-bridge adds a via), read through SelectionProfiles.
func TestShippedProfilesAreNotProviderNames(t *testing.T) {
	entries, err := fs.ReadDir(packs.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	profiles := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest := shippedManifest(t, entry.Name())
		if legacy := manifest.Profiles(); len(legacy) != 0 {
			t.Errorf("packs/%s writes a legacy [[profile]] %v: run `yolo pack migrate packs/%s`", entry.Name(), legacy, entry.Name())
		}
		for _, profile := range manifest.SelectionProfiles() {
			profiles++
			if profile.Name == profile.Provider {
				t.Errorf("packs/%s ships profile %q over provider %q: delete it, the provider is "+
					"selectable by that name (OQ-RC3)", entry.Name(), profile.Name, profile.Provider)
			}
		}
	}
	if profiles == 0 {
		t.Fatal("no shipped pack declares a profile: the test is not reading the tree it exists for")
	}
}
