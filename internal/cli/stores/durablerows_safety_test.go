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

// durableRowsFor drives Inventory with one yolo container whose workspace is ws.
func durableRowsFor(t *testing.T, ws string) ([]Store, Report, Options) {
	t.Helper()
	o, _ := testOptions(t)
	o.Exec = func(argv []string, _ time.Duration) prune.ProbeResult {
		switch k := strings.Join(argv, " "); {
		case strings.HasSuffix(k, "ps -a --format {{.Names}}"):
			return prune.ProbeResult{Ran: true, Stdout: "yolo-proj-1\n"}
		case strings.HasSuffix(k, "yolo-proj-1"):
			return prune.ProbeResult{Ran: true, Stdout: `[{"Destination":"/workspace","Source":"` + ws + `"}]`}
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
	return rows, rep, o
}

// A LINKED `.yolo` IS NOT READ BY `yolo stores` (DS-D3): the jail can point it at another host
// directory. The row says why it has no figures, and nothing is measured through the link.
func TestADurableRowDoesNotFollowALinkedYolo(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws, other := filepath.Join(base, "proj"), filepath.Join(base, "other")
	for _, d := range []string{ws, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := durable.Ensure(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private-name"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, ".yolo"), filepath.Join(ws, ".yolo")); err != nil {
		t.Fatal(err)
	}
	rows, _, _ := durableRowsFor(t, ws)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one saying why", rows)
	}
	s := rows[0]
	if s.Sizing != SizingUnknown || s.Bytes != 0 || s.Count != 0 || !strings.Contains(s.Reason, "`.yolo` is a symbolic link") {
		t.Errorf("row read through a linked .yolo: %+v", s)
	}
}

// The oldest worktree's name is the jail's choice and the note is rich markup: a name that is
// a style tag stays text, and a control character never reaches the terminal.
func TestADurableRowNoteEscapesTheJailsNames(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(base, "proj")
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := durable.Ensure(ws)
	if err != nil {
		t.Fatal(err)
	}
	name := "[bold red]x\x1b[2J"
	wt := filepath.Join(dir, "worktrees", name)
	admin := filepath.Join(ws, ".git", "worktrees", "e")
	for p, body := range map[string]string{
		filepath.Join(wt, ".git"):      "gitdir: x\n",
		filepath.Join(admin, "gitdir"): filepath.Join(wt, ".git") + "\n",
		filepath.Join(admin, "HEAD"):   "ref: refs/heads/e\n",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rows, rep, o := durableRowsFor(t, ws)
	if len(rows) != 1 || rows[0].Count != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	var out strings.Builder
	o.Out = &out
	o.Color = true
	o.IsTTYStdout = func() bool { return true }
	renderText(rep, o)
	text := out.String()
	if strings.Contains(text, "\x1b[1m\x1b[31mx") || strings.Contains(text, "\x1b[2J") {
		t.Errorf("a jail-chosen name restyled or drove the terminal:\n%q", text)
	}
	if !strings.Contains(text, "bold red]x[2J") {
		t.Errorf("the name is not shown as text:\n%q", text)
	}
}
