package stores

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// EVERY KNOWN WORKSPACE'S DURABLE DIR IS A ROW, verdict human and reclaimer none (OQ-DS2):
// the user's work, sized, with its worktree count from git's admin files. Driven through
// Inventory, so the row is pinned at its call site, and rendered, so its section prints.
func TestEachKnownWorkspacesDurableDirIsARow(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws, bare := filepath.Join(base, "proj"), filepath.Join(base, "never-launched")
	for _, d := range []string{filepath.Join(ws, ".git"), bare} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := durable.Ensure(ws)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(dir, "worktrees", "land")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	for p, body := range map[string]string{
		filepath.Join(wt, ".git"):                                "gitdir: x\n",
		filepath.Join(wt, "big"):                                 strings.Repeat("x", 4000),
		filepath.Join(ws, ".git", "worktrees", "land", "gitdir"): "/workspace/.yolo/durable/worktrees/land/.git\n",
		filepath.Join(ws, ".git", "worktrees", "land", "HEAD"):   "ref: refs/heads/land\n",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	o, _ := testOptions(t)
	o.Exec = func(argv []string, _ time.Duration) prune.ProbeResult {
		switch k := strings.Join(argv, " "); {
		case strings.HasSuffix(k, "ps -a --format {{.Names}}"):
			return prune.ProbeResult{Ran: true, Stdout: "yolo-proj-1\nyolo-bare-2\n"}
		case strings.HasSuffix(k, "yolo-proj-1"):
			return prune.ProbeResult{Ran: true, Stdout: `[{"Destination":"/workspace","Source":"` + ws + `"}]`}
		case strings.HasSuffix(k, "yolo-bare-2"):
			return prune.ProbeResult{Ran: true, Stdout: `[{"Destination":"/workspace","Source":"` + bare + `"}]`}
		}
		return prune.ProbeResult{Ran: false}
	}
	rep := Inventory(o)
	var rows []Store
	for _, s := range rep.Stores {
		if s.Section == SectionDurable {
			rows = append(rows, s)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("durable rows = %+v, want one (the workspace with no durable dir gets none)", rows)
	}
	s := rows[0]
	if s.Path != dir || s.Count != 1 || s.CountLabel != "worktrees" || s.Verdict != VerdictHuman ||
		s.Reclaimer.Func != "" || !strings.Contains(s.Reclaimer.Detail, "yolo never reclaims it") ||
		s.Sizing != SizingMeasured || s.Bytes < 4000 {
		t.Errorf("row = %+v", s)
	}

	var out strings.Builder
	o.Out = &out
	renderText(rep, o)
	if !strings.Contains(out.String(), SectionDurable) {
		t.Errorf("the report does not render the %q section:\n%s", SectionDurable, out.String())
	}
}
