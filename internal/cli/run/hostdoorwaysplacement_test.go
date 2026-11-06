package run

// hostdoorwaysplacement_test.go pins the placement rule at the `yolo host` notch
// (docs/reference/loophole-system.md#the-placement-rule; the Mac lab report of 2026-10-19, §4):
// the notch mounts nothing, so the cwd a launch runs in is no tree it hands an agent, and a
// doorway whose host service the rule does refuse is not opened behind the refusal.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostDoorwayFixture is a planned doorway for a pack whose loophole module lives under root, a
// loophole with no host daemon of its own, so the module dir is the one face the rule judges.
func hostDoorwayFixture(t *testing.T, root string) *HostDoorways {
	t.Helper()
	module := filepath.Join(root, "loopholes", "doorway-placed")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"),
		[]byte(`{"name": "doorway-placed", "description": "fixture", "transport": "none"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"name":"fixture-pack",`+
		`"contributes":[{"kind":"loophole","from":"loopholes/doorway-placed"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDir(root, "fixture-pack")
	if len(problems) != 0 {
		t.Fatalf("fixture pack: %v", problems)
	}
	packs := []*packload.Pack{pack}
	set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: packLoopholeModules(packs)})
	if _, ok := set.Lookup("doorway-placed"); !ok {
		t.Fatal("fixture loophole was not discovered")
	}
	plan := &launchservice.Plan{Declared: launchservice.Declared{Service: "doorway-placed", Pack: "fixture-pack"}}
	return &HostDoorways{plans: []*launchservice.Plan{plan}, set: set, packs: packs}
}

// startHostDoorwayFixture runs Start over d with workspace as the launch's cwd, and reports
// whether the doorway's start was reached, what the launch printed and its error.
func startHostDoorwayFixture(t *testing.T, d *HostDoorways, workspace string) (bool, string, error) {
	t.Helper()
	var output bytes.Buffer
	reached := false
	_, stop, _, err := d.Start(newConfig("loopholes", newConfig()), workspace, "pi", &output,
		func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
			reached = true
			return nil, errors.New("fixture stops at the doorway's start")
		})
	if stop != nil {
		stop()
	}
	return reached, output.String(), err
}

// THE CWD IS NOT A MOUNTED WORKSPACE AT THE HOST. Run from $HOME, the launch's cwd contains
// ~/.local/share/yolo-jail, where an installed pack's module dir lives; judged as the workspace a
// jail bind-mounts, the rule refused aws-auth there. With no workspace in hand it judges the jail
// home tree alone, which this module dir is outside, so the doorway's start is reached.
func TestHostDoorwaysStartDoesNotJudgeItsCwdAsAMountedWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	d := hostDoorwayFixture(t, filepath.Join(home, ".local", "share", "yolo-jail", "embedded-packs", "h", "fixture-pack"))
	reached, output, err := startHostDoorwayFixture(t, d, home)
	if strings.Contains(output, "placement rule") || strings.Contains(output, "Refusing to start loophole") {
		t.Errorf("a host launch from $HOME judged its cwd as a mounted workspace:\n%s", output)
	}
	if !reached {
		t.Fatalf("the doorway's start was not reached: err=%v\n%s", err, output)
	}
}

// A REFUSAL THAT LETS ITS DOORWAY OPEN REFUSES NOTHING. A module dir inside the jail home tree,
// which the rule judges at every notch, is refused; the launch then refuses before the doorway
// opens, naming the doorway, the rule and the step that fixes it, where it used to print the
// refusal and open the doorway anyway, pointed at a service that never started.
func TestHostDoorwaysStartRefusesWhenThePlacementRuleRefusesTheDoorwaysService(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	d := hostDoorwayFixture(t, filepath.Join(paths.GlobalHome(), "fixture-pack"))
	reached, output, err := startHostDoorwayFixture(t, d, t.TempDir())
	if reached {
		t.Fatalf("the doorway opened behind its service's placement refusal:\n%s", output)
	}
	if err == nil {
		t.Fatalf("Start returned no refusal for a doorway whose service the placement rule refuses:\n%s", output)
	}
	for _, want := range []string{"Refusing to start loophole doorway-placed", "the placement rule",
		paths.GlobalHome()} {
		if !strings.Contains(output, want) {
			t.Errorf("the launch's output must say %q:\n%s", want, output)
		}
	}
	for _, want := range []string{`"doorway-placed" doorway`, "placement rule", "not opened",
		"Move the pack's files out of that tree"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must say %q: %v", want, err)
		}
	}
	if strings.Contains(output, "This launch runs pack code on your machine") {
		t.Errorf("the refused launch disclosed host code it will not run:\n%s", output)
	}
}
