package clipimage

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func workspace(t *testing.T) string {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestSaveWritesANewFileUnderTheStore(t *testing.T) {
	ws := workspace(t)
	now := time.Date(2026, 11, 6, 15, 30, 45, 0, time.UTC)
	name, err := Save(ws, []byte("pngdata"), now, bytes.NewReader([]byte{1, 2, 3, 4}))
	if err != nil {
		t.Fatal(err)
	}
	if name != "20261106T153045Z-01020304.png" {
		t.Fatalf("name = %q", name)
	}
	got, err := os.ReadFile(filepath.Join(ws, ".yolo", "paste", name))
	if err != nil || string(got) != "pngdata" {
		t.Fatalf("read %q %v", got, err)
	}
	// No temporary name is left behind.
	ents, _ := os.ReadDir(filepath.Join(ws, ".yolo", "paste"))
	if len(ents) != 1 {
		t.Fatalf("store holds %d entries, want 1", len(ents))
	}
	// .yolo is made by the creation chokepoint, so its ignore file is there.
	if _, err := os.Stat(filepath.Join(ws, ".yolo", paths.WorkspaceStateIgnoreName)); err != nil {
		t.Fatalf(".yolo has no ignore file: %v", err)
	}
	if InJailPath("/workspace", name) != "/workspace/.yolo/paste/"+name {
		t.Fatalf("InJailPath = %q", InJailPath("/workspace", name))
	}
}

func TestSaveRefusesALinkedStore(t *testing.T) {
	ws := workspace(t)
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(ws, ".yolo", "paste")); err != nil {
		t.Fatal(err)
	}
	_, err := Save(ws, []byte("x"), time.Now(), bytes.NewReader([]byte{1, 2, 3, 4}))
	var linked *paths.LinkedStateDirError
	if !errors.As(err, &linked) {
		t.Fatalf("err = %v, want a LinkedStateDirError", err)
	}
	if ents, _ := os.ReadDir(elsewhere); len(ents) != 0 {
		t.Fatal("Save wrote through the link")
	}
}

func TestSweepAgesOutAndCapsTheStore(t *testing.T) {
	ws := workspace(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	dir := filepath.Join(ws, ".yolo", "paste")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	put := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stamp := func(d time.Duration) string { return now.Add(-d).UTC().Format(nameTimeLayout) }
	put(stamp(8*24*time.Hour) + "-aaaaaaaa.png")           // past the week
	put("." + stamp(8*24*time.Hour) + "-bbbbbbbb.png.tmp") // an abandoned temporary
	put("." + stamp(time.Minute) + "-cccccccc.png.tmp")    // an in-flight one: kept
	put("notes.txt")                                       // not the store's: kept
	for i := 0; i < MaxFiles+5; i++ {
		put(fmt.Sprintf("%s-%08x.png", stamp(time.Duration(i+1)*time.Minute), i))
	}
	removed, err := Sweep(ws, now)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1+1+5 {
		t.Fatalf("removed %d, want 7", removed)
	}
	ents, _ := os.ReadDir(dir)
	var pngs []string
	re := regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{8}\.png$`)
	for _, e := range ents {
		if re.MatchString(e.Name()) {
			pngs = append(pngs, e.Name())
		}
	}
	if len(pngs) != MaxFiles {
		t.Fatalf("%d images left, want %d", len(pngs), MaxFiles)
	}
	sort.Strings(pngs)
	// The oldest went first: the newest (i=0, one minute old) is still there.
	if pngs[len(pngs)-1] != stamp(time.Minute)+"-00000000.png" {
		t.Fatalf("newest left = %s", pngs[len(pngs)-1])
	}
	for _, keep := range []string{"notes.txt", "." + stamp(time.Minute) + "-cccccccc.png.tmp"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Fatalf("%s was removed", keep)
		}
	}
}

func TestSweepWithNoStoreCreatesNothing(t *testing.T) {
	ws := workspace(t)
	if n, err := Sweep(ws, time.Now()); err != nil || n != 0 {
		t.Fatalf("n %d err %v", n, err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".yolo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Sweep created .yolo")
	}
}
