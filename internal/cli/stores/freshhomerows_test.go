package stores

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// TestEachFreshHomeIsARowWithItsSizeAndRemoval is SD-D9's fresh-home half: `yolo stores` lists
// every fresh home of a known workspace, measured (its capture store included), with the `rm -r`
// that deletes it and its capture store, and offers no reclaimer, since yolo never deletes one.
// The setup's usual home gets no such row.
func TestEachFreshHomeIsARowWithItsSizeAndRemoval(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(base, "proj")
	if err := os.MkdirAll(filepath.Join(paths.SetupHomeState(ws, "podman-self"), "pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	fresh, err := paths.NewFreshHome(ws, "podman-self", time.Date(2026, 11, 8, 10, 15, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fresh, "pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fresh, "pi", "session"), make([]byte, 4000), 0o600); err != nil {
		t.Fatal(err)
	}
	prism := filepath.Join(paths.SetupStateDir(ws, "podman-self"), "prism.fresh-20261108T101500Z")
	if err := os.Mkdir(prism, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prism, "x.last_render"), make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}

	_, rep, o := durableRowsFor(t, ws)
	var rows []Store
	for _, s := range rep.Stores {
		if s.Section == SectionFreshHome {
			rows = append(rows, s)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("fresh home rows: %+v", rows)
	}
	s := rows[0]
	if s.Path != fresh || s.Sizing != SizingMeasured || s.Bytes != 4100 || s.Verdict != VerdictHuman || s.Reclaimer.Func != "" {
		t.Errorf("row: %+v", s)
	}
	if want := shquote.Join([]string{"rm", "-r", fresh, prism}); !strings.Contains(s.Note, want) {
		t.Errorf("the note lacks the removal %q:\n%s", want, s.Note)
	}
	renderText(rep, o)
	out := o.Out.(*strings.Builder).String()
	if !strings.Contains(out, SectionFreshHome) || !strings.Contains(out, "proj/.yolo/setups/podman-self/home.fresh-20261108T101500Z") {
		t.Errorf("the fresh home section is not rendered:\n%s", out)
	}
}

// TestTheRunWorkspacesFreshHomeIsListedWithNoJailRunning: a fresh home outlives its jail, so the
// workspace `yolo stores` runs in is listed whatever the runtime lists; a linked `.yolo` is not read.
func TestTheRunWorkspacesFreshHomeIsListedWithNoJailRunning(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := paths.NewFreshHome(ws, "podman-self", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, _ := testOptions(t)
	o.Workspace = ws
	rows := freshHomeStores(o, "podman")
	if len(rows) != 1 || rows[0].Path != fresh || !strings.Contains(rows[0].Note, shquote.Join([]string{"rm", "-r", fresh})) {
		t.Errorf("rows: %+v", rows)
	}
	o.Workspace = ""
	if rows := freshHomeStores(o, "podman"); len(rows) != 0 {
		t.Errorf("with no workspace and no jail, rows: %+v", rows)
	}

	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(paths.WorkspaceStateDir(ws), paths.WorkspaceStateDir(other)); err != nil {
		t.Fatal(err)
	}
	o.Workspace = other
	if rows := freshHomeStores(o, "podman"); len(rows) != 0 {
		t.Errorf("a linked .yolo was read: %+v", rows)
	}
}
