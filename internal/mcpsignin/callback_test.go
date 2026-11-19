package mcpsignin

import "testing"

func TestCallbackPlanOnlyVerifiedPointAndAcceptedBackend(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, want                            string
		linux, bridge, declared, callback, port, point bool
	}{
		{"Linux bridge", "podman", "publication pending", true, true, false, true, true, true},
		{"user publication", "podman", "port busy", true, true, true, true, true, true},
		{"callback default false", "podman", "not requested", true, true, false, false, true, true},
		{"no owned port", "podman", "not requested", true, true, false, true, false, true},
		{"no managed point", "podman", "not requested", true, true, false, true, true, false},
		{"Apple Container", "container", "not published on this backend", false, true, false, true, true, true},
		{"podman Mac", "podman", "not published on this backend", false, true, false, true, true, true},
		{"Linux host network", "podman", "not published on this backend", true, false, false, true, true, true},
		{"nested", "podman", "not published on this backend", true, false, false, true, true, true},
		{"host", "host", "not needed", true, false, false, true, true, true},
		{"macos-user", "macos-user", "not needed", false, false, false, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launch := &Launch{Version: LaunchVersion, Decisions: []Decision{{Server: "configured-alias", Agent: "fixture-agent"}}}
			if tc.point {
				launch.Decisions[0].Point = &Point{NativeName: "resolved-native", CallbackRoute: tc.callback}
				if tc.port {
					launch.Decisions[0].Point.CallbackPort = 3118
				}
			}
			var declared []string
			if tc.declared {
				declared = []string{"127.0.0.1:3118:9999/tcp"}
			}
			rows := PlanCallbacks(launch, tc.runtime, tc.linux, tc.bridge, declared)
			if len(rows) != 1 || rows[0].Disposition != tc.want {
				t.Fatal(rows)
			}
		})
	}
}
