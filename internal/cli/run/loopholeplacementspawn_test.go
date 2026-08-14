package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// TestManifestPlacementRefusesTheSpawn pins landing item 1a's LAST face.
//
// §4.3a's placement rule was landed for a CONFIG entry's `command` (refused during
// validation) but a MANIFEST's own host_daemon.cmd could not be refused there: two of
// its three targets are runtime resolutions — the module dir after symlinks, the argv
// after {loophole_dir} substitution — so a resolved record is the first place they
// exist. loopholes.(*Loophole).PlacementProblems supplies the judgement, and it had
// ZERO production callers until this gate: built, tested, and reaching nothing.
//
// Without the gate a pack (or a hand-placed user dir) can name a daemon inside the
// live-mounted workspace, and an agent rewrites the file between launches with nothing
// noticing — the exact defect the rule exists to close, one face short of closed.
func TestManifestPlacementRefusesTheSpawn(t *testing.T) {
	ws := t.TempDir()
	inside := filepath.Join(ws, "tool.py")
	if err := os.WriteFile(inside, []byte("#!/usr/bin/env python3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	lp := &loopholes.Loophole{
		Name:    "acme",
		Source:  loopholes.SourcePack,
		Enabled: true,
		HostDaemon: &loopholes.HostDaemon{
			Cmd: []string{"python3", inside},
		},
	}

	// The judgement itself must fire, or the gate below is asserting nothing.
	problems := lp.PlacementProblems(ws)
	if len(problems) == 0 {
		t.Fatalf("PlacementProblems saw no problem in a daemon at %s inside workspace %s "+
			"— the gate cannot refuse what the rule does not report", inside, ws)
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "acme") {
		t.Errorf("the problem does not name the loophole, so a user cannot act on it:\n%s", joined)
	}

	// And the same record OUTSIDE the workspace must be clean — otherwise the gate
	// would refuse every loophole and the test above would pass for the wrong reason.
	outside := filepath.Join(t.TempDir(), "tool.py")
	if err := os.WriteFile(outside, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	clean := &loopholes.Loophole{
		Name:       "acme",
		Source:     loopholes.SourcePack,
		Enabled:    true,
		HostDaemon: &loopholes.HostDaemon{Cmd: []string{"python3", outside}},
	}
	if got := clean.PlacementProblems(ws); len(got) != 0 {
		t.Errorf("a daemon outside the workspace was refused, so the gate is not "+
			"discriminating: %v", got)
	}
}
