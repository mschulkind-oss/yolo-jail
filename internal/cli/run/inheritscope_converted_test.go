package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// convertedUserConfigTOML is a host user config as `yolo config migrate` leaves it, with every
// readable-only shape the legacy reader misjudges: a bare named pack source, handles qualified
// by it, a bare git pack table, and a provider's own `model`.
const convertedUserConfigTOML = `packs = ['claude', 'mine/tools', {git = 'https://example.invalid/acme/craft', ref = 'main', bare = true}]

[pack_sources.mine]
path = '~/mine-packs'
bare = true

[providers.bedrock]
region = 'us-east-1'
model = 'global.example.model-1'
`

// writeConvertedPackTree gives a home the pack the converted config's qualified handle names.
func writeConvertedPackTree(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "mine-packs", "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.toml"), []byte("description = 'tools'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// podmanConfigDir is where a podman launch keeps the jail's ~/.config/yolo-jail in wsState.
func podmanConfigDir(t *testing.T, wsState string) string {
	t.Helper()
	for _, bind := range podmanEarlyHomeBinds() {
		if bind.HomeRel == ".config" {
			return filepath.Join(wsState, bind.Subtree, "yolo-jail")
		}
	}
	t.Fatal("Podman no longer declares its .config home bind")
	return ""
}

// THE RELAUNCH AFTER `yolo config migrate`. Every earlier podman launch delivered the inherited
// user scope as JSONC through single-file binds, and podman left an empty file at each bind's
// target in the jail's home (config.jsonc, inherited-launch.jsonc). The converted host config
// must still cross as TOML: an empty placeholder holds no owner data, so it is moved aside
// rather than read as a legacy sibling that pins the scope to JSONC. And what crosses must read
// back cleanly in the jail, through the same validator `yolo check` runs — a JSONC copy of
// these values made the jail's legacy reader report the qualified handle, `bare` and the
// provider's `model` as broken.
func TestConvertedHostConfigRelaunchDeliversTOMLOverPodmanPlaceholders(t *testing.T) {
	home, wsState := inheritHome(t, "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(config.UserLayerEnv, "")
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), []byte(convertedUserConfigTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConvertedPackTree(t, home)
	configDir := podmanConfigDir(t, wsState)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	placeholders := []string{paths.UserConfigLegacyJSONCName, "inherited-launch.jsonc"}
	for _, name := range placeholders {
		if err := os.WriteFile(filepath.Join(configDir, name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	o := goldenOptions(t.TempDir(), home)
	var stderr strings.Builder
	o.Stderr = &stderr
	in := relocationInput(t, "podman", wsState, nil)
	argv := o.assembleRunCmd(in)
	if in.assemblyErr != nil {
		t.Fatalf("the relaunch was refused: %v", in.assemblyErr)
	}
	if strings.Contains(stderr.String(), "generated JSONC") {
		t.Fatalf("the converted config fell back to JSONC delivery:\n%s", stderr.String())
	}
	preflight, _, ok := mountedBody(t, argv, "/home/agent/"+inheritPreflightRel)
	if !ok {
		t.Fatalf("no TOML preflight scope was mounted: %v", inheritedScopeMounts(argv))
	}
	if _, _, ok := mountedBody(t, argv, "/home/agent/"+inheritNestedRel); !ok {
		t.Fatalf("no TOML nested scope was mounted: %v", inheritedScopeMounts(argv))
	}
	for _, spec := range inheritedScopeMounts(argv) {
		if strings.Contains(spec, ".jsonc:") {
			t.Fatalf("a JSONC scope was mounted beside the TOML one: %s", spec)
		}
	}
	for _, name := range placeholders {
		if _, err := os.Lstat(filepath.Join(configDir, name)); !os.IsNotExist(err) {
			t.Errorf("the empty placeholder %s is still where the jail's user config goes: %v", name, err)
		}
		if _, err := os.Lstat(filepath.Join(configDir, name+podmanPlaceholderSuffix)); err != nil {
			t.Errorf("the empty placeholder %s was not moved aside: %v", name, err)
		}
	}

	// The next relaunch meets the TOML placeholders this launch's binds leave. They are the binds'
	// own targets, so they stay where they are: nothing is moved, and nothing piles up.
	for _, rel := range []string{inheritPreflightRel, inheritNestedRel} {
		if err := os.WriteFile(filepath.Join(configDir, filepath.Base(rel)), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stderr.Reset()
	again := relocationInput(t, "podman", wsState, nil)
	argv = o.assembleRunCmd(again)
	if again.assemblyErr != nil {
		t.Fatalf("the second relaunch was refused: %v", again.assemblyErr)
	}
	if _, _, ok := mountedBody(t, argv, "/home/agent/"+inheritPreflightRel); !ok {
		t.Fatalf("the second relaunch mounted no TOML preflight scope: %v", inheritedScopeMounts(argv))
	}
	if strings.Contains(stderr.String(), "Moved the empty file") {
		t.Errorf("the second relaunch moved the placeholder its own bind targets:\n%s", stderr.String())
	}

	// The jail's side: its own HOME, the delivered file at the path the bind targets, and the
	// validator `yolo check` and every config verb run.
	jail := t.TempDir()
	t.Setenv("HOME", jail)
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	writeConvertedPackTree(t, jail)
	jailConfig := filepath.Join(jail, filepath.FromSlash(inheritPreflightRel))
	if err := os.MkdirAll(filepath.Dir(jailConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jailConfig, []byte(preflight), 0o444); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	merged, err := config.LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatalf("the jail could not load its delivered user config: %v\n%s", err, preflight)
	}
	if errs, _ := config.ValidateConfig(merged, ws, nil); len(errs) != 0 {
		t.Errorf("the jail misreads its delivered user config: %v", errs)
	}
}

// AN OWNER'S OWN legacy file at the destination is not a placeholder: it keeps its place, and
// before this fix it pinned the scope to JSONC with the converted values inside, which the jail
// then misread. A converted config has no JSONC route, so the launch refuses, naming the file
// and the step that clears it.
func TestConvertedHostConfigRefusesAnOwnerLegacyFileAtTheDestination(t *testing.T) {
	home, wsState := inheritHome(t, "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(config.UserLayerEnv, "")
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), []byte(convertedUserConfigTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConvertedPackTree(t, home)
	configDir := podmanConfigDir(t, wsState)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(configDir, paths.UserConfigLegacyJSONCName)
	ownerBytes := []byte(`{"packs": ["owner"]}` + "\n")
	if err := os.WriteFile(owner, ownerBytes, 0o640); err != nil {
		t.Fatal(err)
	}

	o := goldenOptions(t.TempDir(), home)
	in := relocationInput(t, "podman", wsState, nil)
	argv := o.assembleRunCmd(in)
	if in.assemblyErr == nil {
		t.Fatalf("converted values crossed beside an owner's legacy file: %v", inheritedScopeMounts(argv))
	}
	for _, want := range []string{"~/.config/yolo-jail/config.jsonc", paths.UserConfigTOMLName, "move it aside"} {
		if !strings.Contains(in.assemblyErr.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, in.assemblyErr)
		}
	}
	if got, err := os.ReadFile(owner); err != nil || string(got) != string(ownerBytes) {
		t.Fatalf("the owner's file changed: %q %v", got, err)
	}
}
