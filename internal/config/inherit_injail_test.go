package config

import (
	"path/filepath"
	"testing"
)

// LC-D8 (docs/design/legacy-config-compat.md): in a jail, for its own workspace, the assembled
// delivery copy is the host's merged config with the per-workspace file applied, so it answers
// only a caller that wants that file. The read a nested launch's crossing composes from
// (LoadConfigWithoutWorkspaceFileWithSources, the user scope it hands the inner jail) composes
// without it, from the files, the same computation as the host's crossing; BB-D56 forbids a
// per-workspace switch from reaching a jail's user scope. Fails if the short-circuit answers
// withWorkspaceFile=false callers again.
func TestInJailReadWithoutTheWorkspaceFileSkipsTheAssembledCopy(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(UserLayerEnv, "")
	t.Setenv("YOLO_WORKSPACE", ws)
	t.Setenv("YOLO_SETUP", "podman-self")
	mustWrite(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"), "packs = [\"pi\"]\n")
	mustWrite(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"packages": ["ripgrep"]}`)
	// What the host assembled for this launch: the workspace config plus the per-workspace file's
	// switch, which is for the host's workspace alone.
	mustWrite(t, WorkspaceAssembledConfigPath(ws, "podman-self"), `{"packages": ["ripgrep"], "packs": ["pi"],
  "loopholes": {"github-broker": {"enabled": true}}}`)
	t.Setenv("YOLO_VERSION", "9.9.9-test")

	with, err := LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := with.Get("loopholes"); !ok {
		t.Fatalf("fixture: the jail's own read did not take the assembled copy, so this proves nothing: %v", with.Keys())
	}
	for name, read := range map[string]func() (any, error){
		"LoadConfigWithoutWorkspaceFile": func() (any, error) { return LoadConfigWithoutWorkspaceFile(ws, true, func(string) {}) },
		"LoadConfigWithoutWorkspaceFileWithSources": func() (any, error) {
			m, _, err := LoadConfigWithoutWorkspaceFileWithSources(ws, true, func(string) {})
			return m, err
		},
	} {
		got, err := read()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		m := got.(interface{ Get(string) (any, bool) })
		if _, ok := m.Get("loopholes"); ok {
			t.Errorf("%s returned the assembled copy, per-workspace switch and all (BB-D56)", name)
		}
		if _, ok := m.Get("packs"); !ok {
			t.Errorf("%s did not compose the jail's own user scope", name)
		}
	}
}
