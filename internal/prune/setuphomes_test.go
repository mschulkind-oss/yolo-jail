package prune

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A WORKSPACE'S SETUP HOMES ARE ITS HOMES TOO (docs/design/named-setups.md §9): once a launch has
// moved the legacy home to .yolo/setups/<setup>/home, the agent-log purge and the dedup walk find
// what the jail left there, as well as in a legacy home still in place. Red if either stops
// iterating paths.WorkspaceHomeStates.
func TestPruneSweepsEverySetupsHome(t *testing.T) {
	ws := t.TempDir()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	old := now.Add(-60 * 24 * time.Hour)
	var logs []string
	for _, home := range []string{paths.LegacyHomeState(ws), paths.SetupHomeState(ws, "podman-self"), paths.SetupHomeState(ws, "lab")} {
		logs = append(logs, mkLog(t, filepath.Join(home, "copilot", "logs"), "process.log", 100, old))
		bin := filepath.Join(home, "local", "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "tool"), []byte("identical bytes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	walked := map[string]bool{}
	for _, e := range WalkDedupableWorkspaces([]string{ws}) {
		walked[e.Path] = true
		// The entry's root opens to the home it was found in.
		root, rel, err := e.beneath()
		if err != nil {
			t.Fatalf("%s: %v", e.Path, err)
		}
		r, err := root.open()
		if err != nil {
			t.Fatalf("%s: opening its home: %v", e.Path, err)
		}
		if _, err := r.Lstat(rel); err != nil {
			t.Errorf("%s is not %s beneath the home it was found in: %v", e.Path, rel, err)
		}
		r.Close()
	}
	for _, home := range []string{paths.LegacyHomeState(ws), paths.SetupHomeState(ws, "podman-self"), paths.SetupHomeState(ws, "lab")} {
		if p := filepath.Join(home, "local", "bin", "tool"); !walked[p] {
			t.Errorf("dedup did not walk %s", p)
		}
	}

	if b, f := PurgeAgentLogs([]string{ws}, t.TempDir(), 30, true, now); b != 300 || f != 3 {
		t.Errorf("purge = (%d, %d), want (300, 3): one old log in each home", b, f)
	}
	for _, p := range logs {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("the purge left %s", p)
		}
	}
}
