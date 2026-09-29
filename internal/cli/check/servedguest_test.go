package check

// servedguest_test.go pins `yolo check` predicting what a macos-user launch SERVES since OQ-DP8
// and OQ-DP9 (docs/design/declaration-parity.md): its Seatbelt guest runs a loophole's jail
// daemon, so the prediction serves it and composes its pointer, exactly as the launch's
// servedDaemons does; and it declines a pack service's jail daemon (the launch runs that
// service's host half instead), so the prediction serves that one only on a container runtime.
// Both halves go through loopholes.ServedJailDaemonNames, the one split the launch reads, so
// deleting it from predictedServed fails this.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A DOORWAY THE LAUNCH OPENS OUTSIDE THE SANDBOX IS SERVED TOO (host-notch-services.md HS-D15): a
// loophole jail daemon declaring `jail_daemon.host_cmd` is declined by the macos-user guest and
// opened by the launch as its own listener, so the prediction serves it at its declared address on
// macos-user as on a container runtime, and its clients' pointer composes. Deleting the doorways
// from loopholes.ServedJailDaemonNames fails this.
func TestCheckPredictsADoorwayServedOnMacosUser(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	writeLoopholeManifest(t, moduleRoot, "acme-door",
		`"name":"acme-door","description":"d","transport":"none","default_enabled":true,`+
			`"jail_daemon":{"cmd":["yolo-jaild","acme","--listen","{listen}"],"listen":"127.0.0.1:1998",`+
			`"caller_token":true,"host_cmd":["yolo","internal","daemon","acme","--listen","{listen}"]}`)
	for _, rt := range []string{"podman", "macos-user"} {
		merged := useProfiles("claude", "cerebras")
		merged.Set("runtime", rt)
		served := (&Options{}).predictedServed(merged, nil)
		if !served.Serves("acme-door") || served.Listen("acme-door") != "127.0.0.1:1998" {
			t.Errorf("runtime %s: the doorway is not predicted served at its declared address "+
				"(serves %v, listen %q)", rt, served.Names(), served.Listen("acme-door"))
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
