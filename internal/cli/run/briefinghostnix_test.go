package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
)

// The "your nix links are not GC roots" line is gated on the SAME predicate that emits
// the nix daemon and store mounts, asserted on the briefing refreshJailBriefings actually
// writes — so it fails if the call site stops passing HostNix, not only if the renderer
// changes (docs/design/in-jail-nix-roots.md §8).
func TestBriefingNixLineFollowsTheHostNixMount(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rt         string
		daemonHere bool
		want       bool
	}{
		{"podman with the host daemon", "podman", true, true},
		{"podman without it", "podman", false, false},
		{"macos-user shares the host's paths", "macos-user", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			ws := t.TempDir()
			emptyLoopholeDirs(t)
			o := goldenOptions(ws, home)
			o.PathExists = func(p string) bool {
				return tc.daemonHere && (p == hostNixSocket || p == hostNixStore)
			}
			if got := o.hostNixMounted(tc.rt); tc.rt == "podman" && got != tc.daemonHere {
				t.Fatalf("fixture: hostNixMounted(%q) = %v, want %v", tc.rt, got, tc.daemonHere)
			}
			staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", newConfig(), tc.rt,
				stagedPacks{packs: claudePackFixture(t)}, ioprio.Normal)
			if err != nil {
				t.Fatalf("refreshJailBriefings: %v", err)
			}
			body, err := os.ReadFile(filepath.Join(staging, briefingStagingName(claudeBriefingDest)))
			if err != nil {
				t.Fatalf("no briefing was written for the claude pack: %v", err)
			}
			if got := strings.Contains(string(body), "yolo nix-roots list"); got != tc.want {
				t.Errorf("nix line present = %v, want %v:\n%s", got, tc.want, body)
			}
		})
	}
}
