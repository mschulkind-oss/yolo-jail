package check

// orphanstopreason_test.go pins the stop record for the one stop `yolo check` makes: its
// orphan cleanup removes a RUNNING jail (`rm -f`), so every session still attached to it is cut
// short, and each of those sessions prints why its jail ended from the record whatever ended it
// wrote first (docs/design/jail-lifetime-last-session-wins.md JL-D53, run.RecordJailStop).
// Without the record they said that nothing recorded why, and named a stop from outside yolo.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestTheOrphanCleanupRecordsWhyBeforeItRemovesAJail: a running jail whose workspace is gone,
// cleaned up at a terminal's yes, has `yolo check`'s reason on disk when the runtime is asked to
// remove it.
func TestTheOrphanCleanupRecordsWhyBeforeItRemovesAJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-001-deadbeef"
	record := filepath.Join(paths.GlobalStorage(), "owners", cname+".stopped")
	gone := filepath.Join(t.TempDir(), "gone")
	var atRemove []byte
	removed := false
	o := &Options{
		IsTTYStdout: func() bool { return true },
		Stdin:       strings.NewReader("y\n"),
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			switch {
			case len(argv) > 1 && argv[1] == "ps":
				return ExecResult{Ran: true, Stdout: cname + "\t2 hours ago\n"}
			case len(argv) > 1 && argv[1] == "inspect":
				return ExecResult{Ran: true, Stdout: "YOLO_HOST_DIR=" + gone + "\n"}
			case len(argv) > 2 && argv[1] == "rm" && argv[2] == "-f":
				removed = true
				atRemove, _ = os.ReadFile(record)
				return ExecResult{Ran: true}
			}
			return ExecResult{Ran: true}
		},
	}
	var out bytes.Buffer
	o.sectionRunningJails(newReporter(&out, false), "podman")
	if !removed {
		t.Fatalf("the cleanup did not remove the orphan:\n%s", out.String())
	}
	if !strings.Contains(string(atRemove), "`yolo check` (pid ") ||
		!strings.Contains(string(atRemove), "workspace gone") {
		t.Errorf("at the removal the stop record held %q, want yolo check's reason", atRemove)
	}
}
