package run

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestFinishStopCapturesFromTheHomeTheJailBound: `yolo stop` reaping a jail whose keeper died
// captures its config edits from the home its keeper recorded (keeperRecord.Home), a setup's home
// after the move, not the legacy one (named-setups.md §9). Red if FinishStop stops reading the
// record's Home, or the keeper plan stops carrying it into the record.
func TestFinishStopCapturesFromTheHomeTheJailBound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// A runtime client that knows no container, so nothing outside the test is stopped and every
	// question about the jail's container is answered at once.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	emptyLoopholeDirs(t)
	ws := t.TempDir()
	home := paths.SetupHomeState(ws, "podman-self")
	cname := yoloruntime.FromWorkspace(ws)
	if err := writeKeeperRecord(cname, keeperRecord{PID: 999999, Workspace: ws, Runtime: "podman", Home: home}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownerPIDFile(cname), []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var homes []string
	FinishStop(io.Discard, io.Discard, ws, cname, "podman", 0, func(w, h, _ string) {
		if w == ws {
			homes = append(homes, h)
		}
	}, false)
	if len(homes) == 0 {
		t.Fatal("the reap of the unkept jail captured nothing")
	}
	for _, h := range homes {
		if h != home {
			t.Errorf("the reap captured from %q, want the recorded home %q", h, home)
		}
	}
}
