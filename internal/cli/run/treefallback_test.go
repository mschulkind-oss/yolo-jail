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

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
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

// A NESTED LAUNCH names an unmodified extension's reason without a series it does not have, in the
// fallback's line (XB-D49). Red if treeDeliveriesFor's in-jail arm stops reading f.Unmodified.
func TestANestedLaunchNamesAnUnmodifiedExtensionWithNoSeries(t *testing.T) {
	fallbackLaunchHome(t)
	t.Setenv("YOLO_VERSION", "test")
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
			t.Error("a nested launch ran the tree arm")
			return nil
		}
	})
	want := "is an unmodified extension, whose upstream is checked and built on the host — a launch from " +
		"the host delivers it; the agent installs " + fallbackRaw + " itself, in this workspace"
	if !strings.Contains(printed, want) || strings.Contains(printed, "series is replayed") {
		t.Errorf("a nested launch does not name the unmodified extension's reason (%q):\n%s", want, printed)
	}
}

// A FALLBACK IS NOT A MISSING BUILD (missingbuilds.go): an extension whose build left nothing, with a
// fallback declared, never reaches the launch's refusal of a missing patched build — the agent
// installs its raw entry — and the build's cause, which its act left to the launch, is said once,
// under the fallback's line. Red if missingBuilds stops passing over a key with a fallback (the
// launch refuses), or if noteTreeDeliveries stops printing the cause.
func TestAFailedBuildWithAFallbackIsNotRefusedAndSaysItsCauseOnce(t *testing.T) {
	fallbackLaunchHome(t)
	t.Setenv("YOLO_ALLOW_MISSING_PROGRAMS", "")
	cause := "writing tool's settings file failed: ~/.tool is mounted read-only in that jail"
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
			return map[string]TreeDelivery{treeKey: {Reason: "its build jail refused to start on the host",
				Unsaid: true, Cause: &entrypoint.BuildCause{Lines: []string{cause}}}}
		}
	})
	if strings.Contains(printed, "Refusing to launch") || strings.Contains(printed, "no build on this machine") {
		t.Errorf("a key with a fallback reached the missing-build refusal:\n%s", printed)
	}
	if d := patchedTreesInArgv(t, argv)[treeKey]; d.Stop {
		t.Errorf("the jail is handed %+v, want no stop", d)
	}
	if n := strings.Count(printed, cause); n != 1 {
		t.Errorf("the launch says the build's cause %d times, want once under the fallback's line:\n%s", n, printed)
	}
	if !strings.Contains(printed, "the agent installs "+fallbackRaw+" itself") {
		t.Errorf("the launch does not say the fallback:\n%s", printed)
	}
}
