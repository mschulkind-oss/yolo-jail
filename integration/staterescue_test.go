package integration

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAMovedStateIsRescuedAsTheJailStops is workspace-state-safety §3.4/§3.5 end to end: a
// session writes a file into an agent home, moves the workspace's `.yolo` away from inside the
// jail, and ends. The stop must copy the homes out of the still-running container into
// `<workspace>/.yolo/rescue/<stamp>/home/`, and the session's terminal must carry the line naming
// the rescue and its restore command.
//
// The wait is the session's own end: the stop line is logged only after the copy finished, and
// the session's teardown stream carries it, so its presence in the output is the event.
func TestAMovedStateIsRescuedAsTheJailStops(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)

	res := runYolo(t, dir, `set -e
echo kept-by-the-rescue > ~/.local/rescue-probe
mv /workspace/.yolo /workspace/.yolo-moved-away
echo MOVED`)
	out := res.combined()
	if !strings.Contains(out, "MOVED") {
		t.Fatalf("the session did not move the state (rc %d):\n%s", res.rc, out)
	}
	if !strings.Contains(out, "copied the agents' homes to") {
		t.Fatalf("the stop printed no rescue line (rc %d):\n%s", res.rc, out)
	}

	var found []string
	root := filepath.Join(dir, ".yolo", "rescue")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "rescue-probe" {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 1 {
		t.Fatalf("rescue-probe copies under %s: %v\n%s", root, found, out)
	}
	if rel, _ := filepath.Rel(root, found[0]); !strings.Contains(filepath.ToSlash(rel), "/home/local/rescue-probe") {
		t.Errorf("the probe is not under the rescued home's local dir: %s", rel)
	}
	b, err := os.ReadFile(found[0])
	if err != nil || strings.TrimSpace(string(b)) != "kept-by-the-rescue" {
		t.Errorf("the rescued probe reads %q (%v)", b, err)
	}
	if !strings.Contains(out, "cp -a") {
		t.Errorf("the stop line names no restore command:\n%s", out)
	}
}
