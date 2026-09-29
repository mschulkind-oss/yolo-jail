package run

// sharednetwork_test.go pins OQ-NC3's ruling (docs/plans/notch-convergence.md, 2026-09-28, A):
// a jail on a shared network namespace keeps the autonomous posture AND SAYS SO, at launch and
// in its briefing, from the network primitive in render.Profile. Every row drives a production
// call site — Run for the launch line, refreshJailBriefings for the written briefing — so
// deleting either reader fails here, and the bridge rows fail if the fact leaks onto a jail
// whose loopback is its own.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// sharedNetworkMark is a fragment of the one sentence both readers print
// (render.SharedNetworkFact), so the two rows below cannot pass on different wordings.
const sharedNetworkMark = "shares the host's network, so the host's loopback services are reachable"

func TestTheBriefingStatesASharedNetwork(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rt, mode   string
		nested     bool
		wantShared bool
	}{
		{"podman network.mode host", "podman", "host", false, true},
		{"nested podman, forced onto the launcher's namespace", "podman", "bridge", true, true},
		{"podman bridge", "podman", "bridge", false, false},
		{"Apple Container never shares, however the key is set", "container", "host", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := appliedOptions(t, t.TempDir(), home, tc.nested)
			netSec := jsonx.NewOrderedMap()
			netSec.Set("mode", tc.mode)
			got := appliedBriefing(t, o, tc.rt, appliedTestConfig("network", netSec))
			if has := strings.Contains(got, sharedNetworkMark); has != tc.wantShared {
				t.Errorf("shared-network fact present = %v, want %v:\n%s", has, tc.wantShared, got)
			}
			if tc.wantShared && !strings.Contains(got, "autonomy stays on") {
				t.Errorf("a shared-namespace briefing must say autonomy stays on:\n%s", got)
			}
		})
	}
	t.Run("every macos-user jail", func(t *testing.T) {
		got := macosUserBriefing(t, appliedTestConfig())
		if !strings.Contains(got, sharedNetworkMark) {
			t.Errorf("a macos-user briefing must state the shared network:\n%s", got)
		}
	})
}

// sharedNetworkLaunch runs one launch through Run and returns everything it printed. The
// container arm is expected to stop later (no image, no runtime): the line is printed above the
// backend dispatch, so what matters is that it was printed, not how the launch ended.
func sharedNetworkLaunch(t *testing.T, rt, network string, nested bool) string {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), rt, &stdout, &stderr, nil)
	o.Network = network
	if nested {
		o.PathExists = func(p string) bool { return p == "/run/.containerenv" }
	}
	if rt == "macos-user" {
		o.IsMacOS, o.IsLinux = true, false
		o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string,
			macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap,
			[]packload.BlockedTool, macosuser.JailDaemons) int {
			return 0
		}
	}
	_ = Run(*o)
	return stdout.String() + stderr.String()
}

func TestTheLaunchLineStatesASharedNetwork(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rt, net    string
		nested     bool
		wantShared bool
	}{
		{"podman --network host", "podman", "host", false, true},
		{"nested podman", "podman", "bridge", true, true},
		{"macos-user", "macos-user", "bridge", false, true},
		{"podman bridge", "podman", "bridge", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sharedNetworkLaunch(t, tc.rt, tc.net, tc.nested)
			if has := strings.Contains(got, sharedNetworkMark); has != tc.wantShared {
				t.Errorf("launch line present = %v, want %v:\n%s", has, tc.wantShared, got)
			}
		})
	}
}
