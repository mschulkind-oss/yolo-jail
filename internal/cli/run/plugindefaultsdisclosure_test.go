package run

// plugindefaultsdisclosure_test.go pins the jail-code disclosure for a wrapped plugin whose code
// sits at Claude Code's DEFAULT locations with no manifest entry: hooks/hooks.json, a root
// .mcp.json, monitors/monitors.json and bin/. Claude Code loads each of those whatever the
// manifest says (https://code.claude.com/docs/en/plugins-reference, "Standard layout"), so a
// launch that read only the manifest delivered that code with nothing on the banner.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack/pluginpacktest"
)

// Driven through startLoopholesDisclosed, the real spawn boundary, for the reason
// TestWrappedPluginCodeIsDisclosedAtTheSpawnBoundary gives: a test of the printer alone would
// pass with the call deleted.
func TestDefaultLocationPluginCodeIsDisclosedAtTheSpawnBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)

	root := t.TempDir()
	pluginpacktest.WriteDefaultLocationPlugin(t, filepath.Join(root, "skills", "acme-tools"), "acme-tools")
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, "acme")
	if len(probs) > 0 {
		t.Fatalf("the plugin pack fixture does not load: %v", probs)
	}

	cname := "yolo-jailcode-" + t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var errBuf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.PathExists = func(string) bool { return false }

	o.startLoopholesDisclosed(cname, "podman", newConfig(), []*packload.Pack{p}, nil)

	got := errBuf.String()
	if !strings.Contains(got, "acme: 1 wrapped plugin runs code in the jail") {
		t.Fatalf("a plugin whose hooks, MCP server, monitor and executable sit at Claude Code's "+
			"default locations reached the launch with no jail-code line. Its manifest names "+
			"none of them, and Claude Code loads every one. The launch said:\n%s", got)
	}
	// Each component is counted ONCE, by the name the line uses for it.
	for _, want := range []string{"hooks (1)", "mcpServers (1)", "monitors (1)", "bin (1)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the jail-code line does not count %q:\n%s", want, got)
		}
	}
}

// The same boundary for code yolo used to read in no manifest at all: inline hooks in the
// `.claude-plugin/plugin.json` Claude Code runs, hidden by a second plugin.json yolo found first
// or by a byte order mark encoding/json refuses, and a root settings.json's subagentStatusLine,
// a shell command Claude Code runs for every subagent row it draws.
func TestPluginCodeInEveryManifestAndSettingIsDisclosedAtTheSpawnBoundary(t *testing.T) {
	const hooks = `"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo hi"}]}]}`
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"a root plugin.json beside the claude manifest", map[string]string{
			"plugin.json":                `{"name":"acme-tools"}`,
			".claude-plugin/plugin.json": `{"name":"acme-tools",` + hooks + `}`,
		}, "hooks (1)"},
		{"a byte order mark", map[string]string{
			".claude-plugin/plugin.json": "\ufeff" + `{"name":"acme-tools",` + hooks + `}`,
		}, "hooks (1)"},
		{"a subagentStatusLine in settings.json", map[string]string{
			".claude-plugin/plugin.json": `{"name":"acme-tools"}`,
			"settings.json":              `{"subagentStatusLine":{"type":"command","command":"./row.sh"}}`,
		}, "subagentStatusLine (1)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			emptyLoopholeDirs(t)

			root := t.TempDir()
			for rel, body := range tc.files {
				p := filepath.Join(root, "skills", "acme-tools", filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "pack.json"),
				[]byte(`{"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`),
				0o644); err != nil {
				t.Fatal(err)
			}
			p, probs := packload.LoadDir(root, "acme")
			if len(probs) > 0 {
				t.Fatalf("the plugin pack fixture does not load: %v", probs)
			}

			cname := "yolo-jailcode-" + strings.ReplaceAll(t.Name(), "/", "-")
			t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
			var errBuf bytes.Buffer
			o := &Options{}
			fillDefaults(o)
			o.Stderr = &errBuf
			o.Stdout = discardBuf()
			o.PathExists = func(string) bool { return false }

			o.startLoopholesDisclosed(cname, "podman", newConfig(), []*packload.Pack{p}, nil)

			got := errBuf.String()
			if !strings.Contains(got, "acme: 1 wrapped plugin runs code in the jail") ||
				!strings.Contains(got, tc.want) {
				t.Fatalf("a plugin carrying %s reached the launch without a jail-code line "+
					"counting it. The launch said:\n%s", tc.want, got)
			}
		})
	}
}
