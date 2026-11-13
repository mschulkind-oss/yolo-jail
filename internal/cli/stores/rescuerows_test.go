package stores

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/stateguard"
)

// TestEachRescueIsARowWithItsSizeAndRestore is SD-D9's rescue half: `yolo stores` lists every
// rescue of a known workspace, measured, with the command that restores it, and offers no
// reclaimer, since yolo never deletes one.
func TestEachRescueIsARowWithItsSizeAndRestore(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(base, "proj")
	home := paths.SetupHomeState(ws, "podman-self")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := stateguard.NewRescue(ws, time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: ".pi/s", Typeflag: tar.TypeReg, Mode: 0o600, Size: 4000})
	_, _ = tw.Write(make([]byte, 4000))
	_ = tw.Close()
	if _, err := r.Extract(&buf, ".pi", "pi"); err != nil {
		t.Fatal(err)
	}
	rel, _ := paths.HomeStateRel(ws, home)
	if err := r.WriteRecord(stateguard.RescueRecord{HomeRel: rel}); err != nil {
		t.Fatal(err)
	}
	r.Close()

	_, rep, o := durableRowsFor(t, ws)
	var rows []Store
	for _, s := range rep.Stores {
		if s.Section == SectionRescue {
			rows = append(rows, s)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("rescue rows: %+v", rows)
	}
	s := rows[0]
	if s.Sizing != SizingMeasured || s.Bytes < 4000 || s.Verdict != VerdictHuman || s.Reclaimer.Func != "" {
		t.Errorf("row: %+v", s)
	}
	if want := stateguard.RestoreCommand(r.Dir, home); !strings.Contains(s.Note, want) {
		t.Errorf("the note lacks the restore %q:\n%s", want, s.Note)
	}
	renderText(rep, o)
	out := o.Out.(*strings.Builder).String()
	if !strings.Contains(out, SectionRescue) || !strings.Contains(out, "proj/.yolo/rescue/20261107T120000Z") {
		t.Errorf("the rescue section is not rendered:\n%s", out)
	}
}

// TestTheRunWorkspacesRescueIsListedWithNoJailRunning: by the time a rescue exists its jail is
// gone, so the workspace `yolo stores` runs in is listed whatever the runtime lists.
func TestTheRunWorkspacesRescueIsListedWithNoJailRunning(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := stateguard.NewRescue(ws, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	o, _ := testOptions(t)
	o.Workspace = ws
	rows := rescueStores(o)
	if len(rows) != 1 || rows[0].Path != r.Dir {
		t.Errorf("rows: %+v", rows)
	}
	o.Workspace = ""
	if rows := rescueStores(o); len(rows) != 0 {
		t.Errorf("with no workspace and no jail, rows: %+v", rows)
	}
}
