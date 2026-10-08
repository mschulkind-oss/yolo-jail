package integration

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// THE HOUSEKEEPING SLOT ON A MAC (disk-levers-and-backfill.md, the macos-user slot): a macos-user
// launch reaps the retired loophole-state generations past the newest three while its session
// runs, and says so in <workspace>/.yolo/housekeeping.log.
//
// What the Linux gate cannot reach and this does: the real backend's timing. The slot runs on its
// own goroutine and dies at the launch's exit, so a pass that only started after the agent exited
// would be cut, and one that waited would hold the prompt. A session of a few seconds is long
// enough for a pass over a fresh home, and the one launch is the whole test.
func TestMacosUserHousekeepingReapsRetiredLoopholeStateDuringTheSession(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{}`)
	// The suite's isolated home the launch runs under (requireMacosUser): its machine store is the
	// one the slot reaps. It is shared with every earlier test, whose launches retire generations
	// of their own under today's timestamps, so the seeds are dated in the FUTURE: the keep is by
	// name order (prune.PruneRetiredLoopholeStateGuarded), which makes the newest three seeds the
	// newest three generations whatever else the suite left there.
	archive := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "state", ".retired")
	base := time.Date(2099, 9, 1, 12, 0, 0, 0, time.UTC)
	var seeded []string
	for i := 0; i < 5; i++ {
		name := base.Add(time.Duration(i) * time.Hour).Format("20060102-150405")
		dir := filepath.Join(archive, name, "yolo-it-loophole")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		seeded = append(seeded, name)
	}
	// Future-dated seeds would outrank every real generation for the rest of the suite.
	t.Cleanup(func() {
		for _, name := range seeded {
			_ = os.RemoveAll(filepath.Join(archive, name))
		}
	})

	r := runMacosUser(t, ws, "sleep 5\necho \"=== END ===\"", withAutoReapers())
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END ===") {
		t.Fatalf("the macos-user launch did not run its session (rc %d), so nothing below is a "+
			"statement about the slot.\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}

	entries, _ := os.ReadDir(archive)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	// Exactly the newest three seeds kept: anything older, seed or not, is past the keep. A
	// generation this very launch retires after the pass may also remain, and sorts before them.
	if len(left) < 3 || strings.Join(left[len(left)-3:], ",") != strings.Join(seeded[2:], ",") ||
		strings.Contains(strings.Join(left, ","), seeded[0]) || strings.Contains(strings.Join(left, ","), seeded[1]) {
		t.Errorf("after the session the retired generations are %v, want the newest three %v kept "+
			"and %v reaped: the launch ran no housekeeping slot, or its exit cut the pass",
			left, seeded[2:], seeded[:2])
	}
	note, err := os.ReadFile(filepath.Join(ws, ".yolo", "housekeeping.log"))
	if err != nil || !strings.Contains(string(note), "loophole state: reclaimed ") ||
		!strings.Contains(string(note), " retired generation(s), keeping the newest 3") {
		t.Errorf("housekeeping.log does not record the reap (err %v):\n%s", err, note)
	}
}
