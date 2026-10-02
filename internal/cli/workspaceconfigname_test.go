package cli

// workspaceconfigname_test.go pins that `yolo init` and `yolo apply --sealed` read the workspace
// config files the loader reads (config.ResolveWorkspaceConfigPath: the `.jsonc` name, or its
// `.json` fallback where only that exists). Each joined the `.jsonc` name itself: init wrote a
// `yolo-jail.jsonc` beside an existing `yolo-jail.json`, which then shadowed it at every launch,
// and --sealed passed while a `yolo-jail.local.json` merged into the config.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// init finds the `yolo-jail.json` a workspace already keeps its config in, and writes no
// `yolo-jail.jsonc` to shadow it.
func TestInitFindsTheJSONConfigTheLoaderReads(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "yolo-jail.json")
	must(t, os.WriteFile(existing, []byte("{ existing }"), 0o644))
	var buf bytes.Buffer
	Init(dir, nil, &buf, false)
	if !strings.Contains(buf.String(), "yolo-jail.json already exists.") {
		t.Errorf("init did not find the workspace's yolo-jail.json, got %q", buf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "yolo-jail.jsonc")); !os.IsNotExist(err) {
		t.Errorf("init wrote a yolo-jail.jsonc beside yolo-jail.json (stat err %v): the loader "+
			"reads the .jsonc first, so it replaces the user's config at the next launch", err)
	}
	if data, _ := os.ReadFile(existing); string(data) != "{ existing }" {
		t.Error("existing config was clobbered")
	}
}

// --sealed refuses a `yolo-jail.local.json` as it refuses a `yolo-jail.local.jsonc`: both merge
// into the config, and nothing declares either.
func TestApplySealedRefusesTheJSONLocalConfig(t *testing.T) {
	home, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.json"), `{"packs":["claude"]}`)
	writeFile(t, filepath.Join(repo, ".yolo", "keep"), "x")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"host_management":"assert"}`)
	writeFile(t, filepath.Join(repo, "yolo-jail.local.json"), `{"packages":["ripgrep"]}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("a yolo-jail.local.json present should refuse the seal (rc 1), got %d:\n%s%s",
			rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "yolo-jail.local.json is present") {
		t.Errorf("the refusal does not name the yolo-jail.local.json it refused:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Fold its keys into yolo-jail.json ") {
		t.Errorf("the refusal does not send the keys to the workspace's yolo-jail.json:\n%s", out.String())
	}
}
