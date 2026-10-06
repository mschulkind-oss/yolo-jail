package run

// doorwayrestart_test.go pins that a doorway's DECLARED restart policy (its loophole's
// `jail_daemon.restart`) reaches the launch-owned supervision at both notches that open one
// outside a sandbox (docs/design/host-notch-services.md HS-D28): `yolo host --`'s plan
// (PlanHostDoorways) and the macos-user arm's (planMacosUserDoorways). Both hand it to
// launchservice.AdmitDoorway, whose own rule TestAdmitDoorwayAppliesTheHostHalfRule pins; these
// cells pin the two call sites, so a doorway that declares "no" is never restarted because one of
// them stopped passing it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// noRestartDoorwayManifest is acmeDoorwayManifest declaring the restart policy "no".
var noRestartDoorwayManifest = strings.Replace(acmeDoorwayManifest, `"caller_token": true,`,
	`"caller_token": true, "restart": "no",`, 1)

// AT `yolo host --` a doorway is opened only for a selection that asks for it, so the pack gates
// its pointer on a profile and the selection names that profile for the agent the pack installs;
// the plan the launch would start carries the doorway's declared "no".
func TestPlanHostDoorwaysCarriesTheDoorwaysDeclaredRestartPolicy(t *testing.T) {
	if !strings.Contains(noRestartDoorwayManifest, `"restart": "no"`) {
		t.Fatal("fixture bug: the doorway manifest declares no restart policy")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	root := filepath.Join(t.TempDir(), "acme")
	mod := filepath.Join(root, "loopholes", "acme-proxy")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(mod, "manifest.jsonc"): noRestartDoorwayManifest,
		filepath.Join(root, "pack.json"): `{"name": "acme", "contributes": [
			{"kind": "program", "bin": "claude", "via": "npm", "package": "@acme/claude"},
			{"kind": "loophole", "from": "loopholes/acme-proxy"},
			{"kind": "env", "profile": "acmep", "served_by": "acme-proxy", "vars": {"ACME_URL": "http://{listen}/x"}}]}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pack, problems := packload.LoadDir(root, "acme")
	if len(problems) > 0 {
		t.Fatalf("loading the fixture pack: %v", problems)
	}
	pack.Local = true
	sel := packload.GateSelection{Profiles: map[string]string{"claude": "acmep"}}
	d, err := PlanHostDoorways(loopholesConfig(t, `{}`), []*packload.Pack{pack}, sel, true, "yolo host -- claude")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Release)
	plans := d.Plans()
	if len(plans) != 1 || plans[0].Service != "acme-proxy" {
		t.Fatalf("planned %d doorways, want the acme-proxy one (not opened: %v)", len(plans), d.notOpened)
	}
	if plans[0].Restart != "no" {
		t.Errorf("the doorway's plan carries restart %q, want the loophole's declared \"no\"", plans[0].Restart)
	}
}

// ON macos-user the arm opens every enabled doorway the payload declares; the plan it starts
// carries the doorway's declared "no".
func TestMacosUserHandsADoorwayItsDeclaredRestartPolicy(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", noRestartDoorwayManifest)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	doors := observeDoorways(t)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	plan, _ := doors.only(t, "acme-proxy")
	if plan.Restart != "no" {
		t.Errorf("the doorway's plan carries restart %q, want the loophole's declared \"no\"", plan.Restart)
	}
}
