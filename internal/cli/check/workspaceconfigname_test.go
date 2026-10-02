package check

// workspaceconfigname_test.go pins that `yolo check` names the workspace config files the loader
// READ (config.ResolveWorkspaceConfigPath: `yolo-jail.jsonc`, or `yolo-jail.json` where only
// that exists). It parsed both `.json` files and then reported no workspace config at all, and
// its next step for a config finding sent the user to create a `yolo-jail.jsonc`, which would
// shadow the `yolo-jail.json` holding their config.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The parse line names the `.json` files a workspace keeps its config in, as it names the
// `.jsonc` ones.
func TestCheckNamesTheJSONWorkspaceConfigsItParsed(t *testing.T) {
	var out bytes.Buffer
	o := baseOptions(t, &out)
	o.PathExists = func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	ws := filepath.Join(o.Workspace, "yolo-jail.json")
	local := filepath.Join(o.Workspace, "yolo-jail.local.json")
	for _, p := range []string{ws, local} {
		if err := os.WriteFile(p, []byte(`{"network": {"mode": "bridge"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	Check(o)
	got := out.String()
	if want := "Parsed workspace config: " + ws + " + " + local; !strings.Contains(got, want) {
		t.Errorf("check does not name the .json workspace configs it parsed (want %q):\n%s", want, got)
	}
	if strings.Contains(got, "No workspace yolo-jail.jsonc found") {
		t.Errorf("check parsed the workspace's yolo-jail.json and reported no workspace config:\n%s", got)
	}
}

// The same-file preset/null finding, when no source location is recorded for it, falls back to
// the name of the workspace config the loader read.
func TestMergedConfigPresetNullFindingNamesTheJSONWorkspaceConfig(t *testing.T) {
	ws := t.TempDir()
	const body = `{"mcp_presets": ["chrome-devtools"], "mcp_servers": {"chrome-devtools": null}}`
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, bool) { return "", false },
	}
	o.sectionMergedConfig(r, capCfg(t, `{}`), ws, capCfg(t, `{}`), capCfg(t, body), nil, false)
	got := stripANSI(buf.String())
	if want := "yolo-jail.json: preset 'chrome-devtools' is enabled"; !strings.Contains(got, want) {
		t.Errorf("the finding does not name the workspace's yolo-jail.json (want %q):\n%s", want, got)
	}
}

// The next step for an unlocated config finding names the workspace config the loader reads.
func TestConfigNoteNamesTheWorkspaceConfigTheLoaderReads(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	note := configNote("config.definitely_not_a_key: unknown key", ws)
	if want := "Fix it in " + filepath.Join(ws, "yolo-jail.json") + " or "; !strings.HasPrefix(note, want) {
		t.Errorf("the next step does not name the workspace's yolo-jail.json (want prefix %q): %q",
			want, note)
	}
}
