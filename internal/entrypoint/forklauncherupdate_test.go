package entrypoint

// forklauncherupdate_test.go pins what a fork's source launcher says in update mode
// (forklauncher.go; docs/design/patched-forks.md §7): a plain fork's pin moves it, and a PATCHED
// fork's build moves at a fresh launch on the host, which never names a pin it does not have.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestASourceLauncherInUpdateModeNamesWhatMovesItsBuild(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	for name, tc := range map[string]struct {
		patches    string
		want, lack string
	}{
		"plain":   {"", "its pin moves it", "patched"},
		"patched": {"patches", "a fresh launch on the host checks its upstream and builds what moved", "its pin"},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			body := strings.Join(sourceAgentLauncherSegments(&packdecl.Install{Kind: packdecl.InstallKindSource,
				Bin: "tool", ForkedBy: "forkpack", Patches: tc.patches, Produces: []string{".local/bin/tool"}},
				ForkDelivery{Key: "k"}, filepath.Join(home, "stamps"), filepath.Join(home, "keys"),
				filepath.Join(home, "receipts.jsonl"), "", true, launcherServers{}, nil), "")
			cmd := exec.Command("bash", "-c", body, "tool")
			cmd.Env = append(os.Environ(), "HOME="+home, "YOLO_PACK_UPDATE=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the launcher in update mode: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), tc.want) || strings.Contains(string(out), tc.lack) {
				t.Errorf("the %s fork's launcher says %q, want %q and not %q", name, out, tc.want, tc.lack)
			}
		})
	}
}
