package run

// skippedcontribution_test.go pins a jail launch over a configured pack holding a contribution
// this yolo cannot read (docs/design/patched-forks.md PF-D68): a field a newer yolo reads. The
// launch used to refuse such a pack — and so every launch on the host, whichever pack it was. Now
// it stages the pack, skips the contribution, says so in one line, and records the line in the
// tree so the jail's boot logs it instead of printing it again.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestALaunchStagesAPackWithAFieldFromANewerYoloAndSaysWhatItSkips(t *testing.T) {
	home := packHome(t)
	src := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(filepath.Join(src, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "files", "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// contributes[0] holds a field no yolo knows; contributes[1] is readable and must still land.
	if err := os.WriteFile(filepath.Join(src, "pack.json"), []byte(`{"name":"acme","contributes":[
	  {"kind":"files","source":"git+https://example.com/x?ref=main","into":".acme/newer",
	   "patches_from_the_future":"patches"},
	  {"kind":"files","from":"files","into":".acme/files"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+src+`"]`)

	var stderr bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stderr: &stderr}
	tree, loaded, _, err := o.stagePacks("yolo-test-skipped-contribution")
	if err != nil {
		t.Fatalf("a pack holding a field from a newer yolo must not refuse the launch: %v", err)
	}
	var acme *packload.Pack
	for _, p := range loaded {
		if p.Name == "acme" {
			acme = p
		}
	}
	if acme == nil {
		t.Fatalf("the pack was not staged: %v", loaded)
	}
	var files []packdecl.Contribution
	for _, c := range acme.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles {
			files = append(files, c)
		}
	}
	if len(files) != 1 || files[0].Into != ".acme/files" {
		t.Errorf("want only the readable files contribution, got %+v", files)
	}

	// THE DISCLOSURE: one line, naming the pack, the contribution's kind, the field and a newer yolo.
	line := stderr.String()
	for _, want := range []string{"Warning: pack acme: contributes[0]: skipping the files contribution",
		`unknown field "patches_from_the_future"`, "a newer yolo may read the field"} {
		if !strings.Contains(line, want) {
			t.Errorf("the launch must say what it skipped (%q):\n%s", want, line)
		}
	}
	if n := strings.Count(line, "skipping"); n != 1 {
		t.Errorf("one skipped contribution, %d lines:\n%s", n, line)
	}

	// THE RECORD: the line the launch printed, for the boot to log rather than repeat.
	rec, recorded, err := packload.ReadPackTreeRecord(tree)
	if err != nil || !recorded {
		t.Fatalf("ReadPackTreeRecord: recorded=%v err=%v", recorded, err)
	}
	var skipped []string
	for _, e := range rec {
		if e.Name == "acme" {
			skipped = e.Skipped
		}
	}
	if len(skipped) != 1 || !strings.HasPrefix(skipped[0], "contributes[0]: skipping the files contribution") {
		t.Errorf("the tree must record the printed line without its pack prefix, got %q", skipped)
	}

	// AN ATTACH reads the running jail's tree the way the launch read it, so the same pack does not
	// fail the attach that its launch ran (loadPackTree, the host-side reader of a running tree).
	attached, err := loadPackTree(tree)
	if err != nil {
		t.Fatalf("an attach must read the tree its launch staged: %v", err)
	}
	for _, p := range attached {
		if p.Name == "acme" && len(p.SkewNotes) != 1 {
			t.Errorf("the attach's read must skip what the launch skipped: %q", p.SkewNotes)
		}
	}
}
