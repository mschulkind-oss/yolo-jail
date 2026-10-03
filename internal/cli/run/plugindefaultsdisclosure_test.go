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
