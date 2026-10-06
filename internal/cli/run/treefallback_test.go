package run

// treefallback_test.go pins the launch's half of an UNMODIFIED EXTENSION's FALLBACK
// (docs/design/pi-extension-store-builds.md §4.3, XB-D7) from Run's own entry point: a launch that
// hands no tree for an extension declaring a fallback never stops its owner, and says, once, that the
// agent installs the raw entry itself; a macos-user launch, which builds none, says the same in
// place of "starts without it".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fallbackRaw = "npm:tree-ext"

// fallbackLaunchHome is treeLaunchHome with the extension unmodified, from an npm source, and a
// fallback declared; its list entry is the package inside the tree's npm prefix.
func fallbackLaunchHome(t *testing.T) {
	t.Helper()
	home := treeLaunchHome(t, true)
	cfg, err := os.ReadFile(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	// The tree pack's directory, read back off the user config treeLaunchHome wrote.
	_, after, _ := strings.Cut(string(cfg), `{"source":"file://`)
	_, after, _ = strings.Cut(after, `{"source":"file://`)
	treeDir, _, _ := strings.Cut(after, `"`)
	if err := os.WriteFile(filepath.Join(treeDir, "pack.json"), []byte(`{"contributes":[{"kind":"files",`+
		`"into":"`+treeInto+`","source":"npm:tree-ext","fallback":"`+fallbackRaw+`"},`+
		`{"kind":"config-list","surface":"tool/settings","path":"/packages","add":["~/`+treeInto+
		`/node_modules/tree-ext"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestALaunchWithNoTreeForAnExtensionWithAFallbackStopsNothingAndSaysSo(t *testing.T) {
	fallbackLaunchHome(t)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
			if len(r.Trees) != 1 || r.Trees[0].Fallback != fallbackRaw || r.Trees[0].Owner != "agentpack" {
				t.Errorf("the tree arm was handed %+v", r.Trees)
			}
			return map[string]TreeDelivery{treeKey: {Reason: "extension " + treeKey + "'s build failed on the host"}}
		}
	})
	d := patchedTreesInArgv(t, argv)[treeKey]
	if d.Stop || d.Build != "" || d.Owner != "agentpack" {
		t.Errorf("the jail is handed %+v, want no build and no stop", d)
	}
	want := "extension " + treeKey + ": no tree is mounted in this jail — extension " + treeKey +
		"'s build failed on the host; the agent installs " + fallbackRaw + " itself, in this workspace"
	if n := strings.Count(printed, want); n != 1 {
		t.Errorf("the launch says the fallback %d times, want once (%q):\n%s", n, want, printed)
	}
	if !strings.Contains(printed, "an unmodified extension of npm:tree-ext") {
		t.Errorf("the launch's block does not name an unmodified extension:\n%s", printed)
	}
	// The disclosure: an unmodified extension's claim prints with the launch's, as a patched one's
	// does, whether or not a tree was handed.
	if !strings.Contains(printed, "DELIVERS a tree built from source at ~/"+treeInto) ||
		!strings.Contains(printed, "its install scripts included") {
		t.Errorf("the launch does not disclose its unmodified extension's claim:\n%s", printed)
	}
}

func TestAMacosUserLaunchSaysTheAgentInstallsTheFallback(t *testing.T) {
	fallbackLaunchHome(t)
	out := launchToDispatch(t)
	if !strings.Contains(out, "the agent installs "+fallbackRaw+" itself, in this workspace") ||
		strings.Contains(out, "is not delivered on macos-user") {
		t.Errorf("the macos-user launch does not say the agent installs the fallback:\n%s", out)
	}
}
