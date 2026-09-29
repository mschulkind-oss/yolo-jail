package check

// servedguest_test.go pins `yolo check` predicting what a macos-user launch SERVES since OQ-DP8
// and OQ-DP9 (docs/design/declaration-parity.md): its Seatbelt guest runs a loophole's jail
// daemon, so the prediction serves it and composes its pointer, exactly as the launch's
// servedDaemons does; and it declines a pack service's jail daemon (the launch runs that
// service's host half instead), so the prediction serves that one only on a container runtime.
// Both halves go through loopholes.JailDaemonNamesRunIn, the one split the launch reads, so
// deleting it from predictedServed fails this.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

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
