package check

// servedguest_test.go pins `yolo check` predicting what a macos-user launch SERVES since OQ-DP8
// and OQ-DP9 (docs/design/declaration-parity.md): its Seatbelt guest runs a loophole's jail
// daemon, so the prediction serves it and composes its pointer, exactly as the launch's
// servedDaemons does; and it declines a pack service's jail daemon (the launch runs that
// service's host half instead), so the prediction serves that one only on a container runtime.
// Both halves go through loopholes.ServedJailDaemonNames, the one split the launch reads, so
// deleting it from predictedServed fails this. A doorway's host argv passes the same admission
// the launch applies (launchservice.AdmitDoorways) before either split reads it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// doorwayPack writes a pack named name shipping one loophole module, acme-door, whose manifest
// body is body, registers the module as this process's pack loophole (as a launch's staging
// does), and returns the loaded pack, marked official when official is.
func doorwayPack(t *testing.T, name string, official bool, body string) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	mod := writeLoopholeManifest(t, filepath.Join(root, "loopholes"), "acme-door", body)
	manifest := `{"name": "` + name + `", "contributes": [{"kind": "loophole", "from": "loopholes/acme-door"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, name)
	if len(problems) > 0 {
		t.Fatalf("loading the %s pack: %v", name, problems)
	}
	p.Official = official
	loopholes.SetPackModuleResolver(func() []loopholes.PackModule {
		return []loopholes.PackModule{{Dir: mod, HostExecApproved: true}}
	})
	loopholes.ResetPackModules()
	t.Cleanup(func() {
		loopholes.SetPackModuleResolver(nil)
		loopholes.ResetPackModules()
	})
	t.Setenv("HOME", t.TempDir())
	retiredLoopholeDir(t)
	return p
}

// A DOORWAY THE LAUNCH OPENS OUTSIDE THE SANDBOX IS SERVED TOO (host-notch-services.md HS-D15): a
// loophole jail daemon of a pack yolo ships that declares `jail_daemon.host_cmd` is declined by
// the macos-user guest and opened by the launch as its own listener, so the prediction serves it
// at its declared address on macos-user as on a container runtime, and its clients' pointer
// composes. Deleting the doorways from loopholes.ServedJailDaemonNames fails this.
func TestCheckPredictsADoorwayServedOnMacosUser(t *testing.T) {
	p := doorwayPack(t, "acme", true,
		`"name":"acme-door","description":"d","transport":"none","default_enabled":true,`+
			`"jail_daemon":{"cmd":["yolo-jaild","acme","--listen","{listen}"],"listen":"127.0.0.1:1998",`+
			`"caller_token":true,"host_cmd":["yolo","internal","daemon","acme","--listen","{listen}"]}`)
	for _, rt := range []string{"podman", "macos-user"} {
		merged := useProfiles("claude", "cerebras")
		merged.Set("runtime", rt)
		served := (&Options{}).predictedServed(merged, []*packload.Pack{p})
		if !served.Serves("acme-door") || served.Listen("acme-door") != "127.0.0.1:1998" {
			t.Errorf("runtime %s: the doorway is not predicted served at its declared address "+
				"(serves %v, listen %q)", rt, served.Names(), served.Listen("acme-door"))
		}
	}
}

// A DOORWAY YOLO WILL NOT OPEN IS PREDICTED AS THE LAUNCH TREATS IT: a pack yolo does not ship
// may not run host code through host_cmd, so the launch clears the host argv
// (launchservice.AdmitDoorways) and judges the jail daemon like any other. This one's argv names
// the container's loophole mount, which the macos-user guest declines, so that launch serves it
// nowhere, and the prediction must not serve it either: a `served_by` pointer at it would compose
// in the prediction and be withheld at the launch. Deleting the admission from predictedServed
// fails this.
func TestCheckDoesNotPredictARefusedDoorwayServedOnMacosUser(t *testing.T) {
	p := doorwayPack(t, "local", false,
		`"name":"acme-door","description":"d","transport":"none","default_enabled":true,`+
			`"jail_daemon":{"cmd":["{jail_loophole_dir}/acme","--listen","{listen}"],"listen":"127.0.0.1:1998",`+
			`"caller_token":true,"host_cmd":["yolo","internal","daemon","acme","--listen","{listen}"]}`)
	for _, tc := range []struct {
		runtime string
		served  bool
	}{
		{"podman", true},
		{"macos-user", false},
	} {
		merged := useProfiles("claude", "cerebras")
		merged.Set("runtime", tc.runtime)
		served := (&Options{}).predictedServed(merged, []*packload.Pack{p})
		if served.Serves("acme-door") != tc.served {
			t.Errorf("runtime %s: predicted the refused doorway served = %v, want %v (the launch "+
				"opens no doorway for a pack yolo does not ship, and the guest declines this argv)",
				tc.runtime, served.Serves("acme-door"), tc.served)
		}
	}
}

func TestCheckPredictsTheGuestDaemonsServedOnMacosUser(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	writeLoopholeManifest(t, moduleRoot, "acme-adapter",
		`"name":"acme-adapter","description":"d","transport":"none","default_enabled":true,`+
			`"jail_daemon":{"cmd":["yolo-jaild","acme","--listen","{listen}"],"listen":"127.0.0.1:1999"}`)
	var bridge []*packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "wire-bridge" {
			bridge = append(bridge, p)
		}
	}
	if len(bridge) != 1 {
		t.Fatal("the shipped wire-bridge pack is gone; this test has lost its service subject")
	}
	for _, tc := range []struct {
		runtime       string
		serviceServed bool
	}{
		{"podman", true},
		{"macos-user", false},
	} {
		merged := useProfiles("claude", "cerebras")
		merged.Set("runtime", tc.runtime)
		served := (&Options{}).predictedServed(merged, bridge)
		if !served.Serves("acme-adapter") || served.Listen("acme-adapter") != "127.0.0.1:1999" {
			t.Errorf("runtime %s: the loophole's jail daemon is not predicted served at its "+
				"declared address (serves %v, listen %q)", tc.runtime, served.Names(),
				served.Listen("acme-adapter"))
		}
		if served.Serves("wire-bridge") != tc.serviceServed {
			t.Errorf("runtime %s: predicted the wire bridge's jail daemon served = %v, want %v",
				tc.runtime, served.Serves("wire-bridge"), tc.serviceServed)
		}
	}
}
