package loopholedecl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// defaultenabled_test.go pins the retirement of both manifest enablement keys
// (docs/reference/loophole-grants.md §4.5, LG-D3): a loophole runs whenever its pack is
// selected, and the pack author's one remaining declaration is what can be granted, `[[grants]]`.
// `enabled` (the key OQ-A9 renamed) and `default_enabled` (the rename) are both refused BY NAME in
// the structural walk, so both decoders refuse them and neither is reported as an unknown key.

// bodyWith wraps a manifest fragment in the minimum a decode needs: a `name` matching
// the directory this file always decodes against.
func bodyWith(fragment string) []byte {
	body := `{"name": "probe", "transport": "none"`
	if fragment != "" {
		body += ", " + fragment
	}
	return []byte(body + "}")
}

const probeDir = "/loopholes/probe"

// decodeErr runs one decoder over a fragment and returns only its error.
func decodeErr(t *testing.T, fragment string, tolerant bool) error {
	t.Helper()
	if tolerant {
		_, _, err := loopholedecl.DecodeTolerant(bodyWith(fragment), probeDir)
		return err
	}
	_, err := loopholedecl.Decode(bodyWith(fragment), probeDir)
	return err
}

// TestDefaultEnabledIsRefusedOnEveryManifest is the plan's T13 test: the decoder refuses the key
// with §5's grant-naming text whatever its value, and no manifest yolo ships still declares it.
func TestDefaultEnabledIsRefusedOnEveryManifest(t *testing.T) {
	const dir = "packs/journal/loopholes/journal"
	want := "packs/journal/loopholes/journal/loophole.toml: default_enabled is gone. A loophole runs whenever its pack\n" +
		"  is selected and grants nothing until a grant covers the project; declare what can be granted under\n" +
		"  [[grants]] instead, and remove the key."
	for _, value := range []string{"true", "false"} {
		body := []byte("name = \"journal\"\ntransport = \"none\"\ndefault_enabled = " + value + "\n")
		_, err := loopholedecl.Decode(body, dir)
		if err == nil || err.Error() != want {
			t.Errorf("default_enabled = %s: Decode error\n%v\nwant\n%s", value, err, want)
		}
		// The tolerant decoder (discovery's) refuses it too: no reader consults the key, so a
		// manifest that declares it says something nothing honors.
		if _, _, err := loopholedecl.DecodeTolerant(body, dir); err == nil || err.Error() != want {
			t.Errorf("default_enabled = %s: DecodeTolerant error\n%v\nwant\n%s", value, err, want)
		}
	}
	for _, k := range loopholedecl.KnownKeys() {
		if k == "default_enabled" {
			t.Errorf("KnownKeys() still advertises default_enabled; authoring tools suggest spellings from it")
		}
	}

	manifests, err := filepath.Glob(filepath.Join("..", "..", "packs", "*", "loopholes", "*", loopholedecl.ManifestTOMLName))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) == 0 {
		t.Fatal("no shipped loophole manifests found: the walk is not reading the tree it exists for")
	}
	for _, path := range manifests {
		if _, err := loopholedecl.LoadDir(filepath.Dir(path)); err != nil {
			t.Errorf("%s does not decode strictly: %v", path, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "default_enabled") {
				t.Errorf("%s still declares default_enabled: %q", path, line)
			}
		}
	}
}

// TestRetiredEnabledKeyIsRefusedAndNamesTheGrants: the older manifest key is refused by name in
// both decoders, and its message now names the grant declaration rather than the rename it once
// pointed at, which is itself refused.
func TestRetiredEnabledKeyIsRefusedAndNamesTheGrants(t *testing.T) {
	for _, fragment := range []string{`"enabled": true`, `"enabled": false`} {
		for name, err := range map[string]error{"strict": decodeErr(t, fragment, false), "tolerant": decodeErr(t, fragment, true)} {
			if err == nil {
				t.Fatalf("%s (%s): decoded without error; the retired key must be refused, not ignored", fragment, name)
			}
			msg := err.Error()
			if !strings.Contains(msg, "[[grants]]") {
				t.Errorf("%s (%s): message does not name [[grants]]:\n%s", fragment, name, msg)
			}
			if strings.Contains(msg, "write 'default_enabled'") {
				t.Errorf("%s (%s): message still points at the retired default_enabled:\n%s", fragment, name, msg)
			}
			if strings.Contains(msg, "unknown key") {
				t.Errorf("%s (%s): reported as an unknown key rather than a retired one:\n%s", fragment, name, msg)
			}
		}
	}
	for _, k := range loopholedecl.KnownKeys() {
		if k == loopholedecl.RetiredKeyEnabled {
			t.Errorf("KnownKeys() still advertises the retired %q", k)
		}
	}
}

// TestAWorkspaceScopeSettingIsRefusedAtLoad: `scope = "workspace"` on a declared setting let an
// agent-writable project file widen what a loophole grants. It has no shipped user and is refused
// at load in both decoders, naming the valued grant that replaces it (LG-D5).
//
// A loophole.toml only: a legacy manifest's is read through the legacy window
// (TestALegacyManifestsWorkspaceScopeSettingIsARequestScopeSetting, LC-D10).
func TestAWorkspaceScopeSettingIsRefusedAtLoad(t *testing.T) {
	const dir = "packs/probe/loopholes/probe"
	body := []byte("name = \"probe\"\ntransport = \"none\"\n[settings.visible]\ntype = \"string_list\"\nscope = \"workspace\"\n")
	_, strictErr := loopholedecl.Decode(body, dir)
	_, _, tolerantErr := loopholedecl.DecodeTolerant(body, dir)
	for name, err := range map[string]error{"strict": strictErr, "tolerant": tolerantErr} {
		if err == nil {
			t.Fatalf("%s: a workspace-scope setting decoded without error", name)
		}
		msg := err.Error()
		for _, want := range []string{"settings.visible.scope", "workspace", "[[grants]]", "yolo request"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: refusal does not name %q:\n%s", name, want, msg)
			}
		}
	}
	// The control: the user scope, spelled or defaulted, still loads.
	for _, ok := range []string{
		`"settings": {"visible": {"type": "string_list", "scope": "user"}}`,
		`"settings": {"visible": {"type": "string_list"}}`,
	} {
		if err := decodeErr(t, ok, false); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
}
