package run

// packworkingtree_test.go pins the LAUNCH's side of the working-tree staging (docs/design/local-pack-clones.md
// §6.4-§6.5, slice 6): stagePackEntry stages a `local` pack from its clone's working tree, and says on stderr,
// as a disclosure, when the clone was to run and origin's held copy runs instead.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func localPackEntry(t *testing.T) (config.PackEntry, string) {
	t.Helper()
	packHome(t)
	origin := t.TempDir()
	if err := os.WriteFile(filepath.Join(origin, "pack.json"), []byte(`{"name":"loc"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "init", "-q", "-b", "main")
	runGit(t, origin, "add", "-A")
	runGit(t, origin, "commit", "-qm", "pack")
	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	if err := os.WriteFile(filepath.Join(clone, "note.md"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := config.PackEntry{Source: "git+file://" + origin + "?ref=main", Name: "loc", Local: clone}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	outs, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{})
	if err != nil || outs[0].Local == nil || outs[0].Local.Copy != packsrc.CopyWorkingTree {
		t.Fatalf("refresh = %+v, %v: want a decision that runs the working tree", outs, err)
	}
	return entry, clone
}

func TestStagePackEntryStagesTheCloneAndWarnsWhenItCannot(t *testing.T) {
	entry, clone := localPackEntry(t)
	var stderr bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &stderr}
	dest := filepath.Join(t.TempDir(), "loc")
	if _, err := o.stagePackEntry(entry, dest); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "note.md")); err != nil || string(b) != "uncommitted" {
		t.Fatalf("the clone's untracked file did not reach the launch's staged tree: %q, %v", b, err)
	}
	if strings.Contains(stderr.String(), "was not staged") {
		t.Errorf("a staged clone warned: %q", stderr.String())
	}

	if err := os.Symlink("/etc/hostname", filepath.Join(clone, "leak")); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	dest = filepath.Join(t.TempDir(), "loc")
	if _, err := o.stagePackEntry(entry, dest); err != nil {
		t.Fatal(err)
	}
	if out := stderr.String(); !strings.Contains(out, "Warning: pack loc: the working tree of "+clone+" was not staged") ||
		!strings.Contains(out, "leak") {
		t.Errorf("stderr = %q, want the fallback warning naming the clone and the link", out)
	}
	if _, err := os.Stat(filepath.Join(dest, "note.md")); err == nil {
		t.Errorf("the clone's file staged although origin's copy runs")
	}
}
