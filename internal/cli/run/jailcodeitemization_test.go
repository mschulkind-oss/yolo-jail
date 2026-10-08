package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestWrappedPluginCodeIsItemizedInTheLaunchLogOnly is OQ-TP10's detail-only half: the terminal
// gets one counted line per pack, and the launch's record (launch.log, through the stream's log
// half) gets each plugin by component and source. Driven through the spawn boundary, so deleting
// the itemization's call in notePackJailCode, or the boundary's call of notePackJailCode, fails
// here. The terminal must not get the itemization: that is the density the ruling bought.
func TestWrappedPluginCodeIsItemizedInTheLaunchLogOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)

	p := writePluginPack(t, "acme", map[string]string{
		"acme-tools": `{"name":"acme-tools","skills":["./"],` +
			`"hooks":{"PreToolUse":[]},"mcpServers":{"acme":{}}}`,
		"acme-docs": `{"name":"acme-docs","skills":["./"]}`,
	})

	cname := "yolo-jailcode-items-" + t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var term, log bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = teeLog{w: &term, log: &log}
	o.Stdout = discardBuf()
	o.PathExists = func(string) bool { return false }

	o.startLoopholesDisclosed(cname, "podman", newConfig(), []*packload.Pack{p}, nil)

	item := "  acme: acme-tools — hooks (.claude-plugin/plugin.json), mcpServers (.claude-plugin/plugin.json)"
	if !strings.Contains(log.String(), item+"\n") {
		t.Errorf("launch.log lacks the plugin's itemization %q:\n%s", item, log.String())
	}
	if !strings.Contains(log.String(), "runs code in the jail") {
		t.Errorf("launch.log lacks the counted line the terminal got:\n%s", log.String())
	}
	if strings.Contains(log.String(), "acme-docs") {
		t.Errorf("a plugin that runs no code was itemized as jail code:\n%s", log.String())
	}
	if strings.Contains(term.String(), "acme-tools") || strings.Contains(term.String(), "itemized") {
		t.Errorf("the itemization reached the terminal, which gets only the counted line:\n%s", term.String())
	}
	if !strings.Contains(term.String(), "runs code in the jail") {
		t.Errorf("the terminal lost the counted line:\n%s", term.String())
	}
}

// TestTheItemizationNamesTheCountedSet: the itemization and the counted line read one set
// (disclosedJailCodePlugins), so they agree on how many plugins run code.
func TestTheItemizationNamesTheCountedSet(t *testing.T) {
	p := writePluginPack(t, "acme", map[string]string{
		"one":  `{"name":"one","skills":["./"],"hooks":{"PreToolUse":[]}}`,
		"two":  `{"name":"two","skills":["./"],"mcpServers":{"x":{}}}`,
		"none": `{"name":"none","skills":["./"]}`,
	})
	items := pluginJailCodeItems(p)
	if !strings.HasPrefix(pluginJailCodeSummary(p), "2 wrapped plugins run code") || len(items) != 2 {
		t.Errorf("summary %q and items %v disagree on the set", pluginJailCodeSummary(p), items)
	}
}
