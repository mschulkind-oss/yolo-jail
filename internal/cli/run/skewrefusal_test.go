package run

// skewrefusal_test.go pins two readings of a pack holding a contribution this yolo cannot read
// (docs/design/patched-forks.md PF-D68, PF-D75): a pack the use read still refuses names what it
// skipped before the refusal, since the problem may be one only the skip left; and an attach reads a
// tree with no record (one an older launch staged) as the launch reads a pack, so a skip there is a
// note, not a refused attach.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A RESTRICTION KEPT WITHOUT ITS FIELD THAT NO LONGER VALIDATES is refused, failing closed, and the
// launch says the field it could not read and `update yolo` before the refusal.
func TestARefusedPackNamesWhatItsUseReadSkipped(t *testing.T) {
	home := packHome(t)
	src := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "pack.json"), []byte(`{"name":"acme","contributes":[`+
		`{"kind":"autonomy","guarded_by_a_newer_yolo":{"launch":[{"bin":"pi"}]}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+src+`"]`)
	var stderr bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stderr: &stderr}
	_, _, _, err := o.stagePacks("yolo-test-skew-refusal")
	if err == nil || !strings.Contains(err.Error(), "autonomy needs at least one of") {
		t.Fatalf("a restriction that no longer validates must refuse the launch: %v", err)
	}
	for _, want := range []string{`"guarded_by_a_newer_yolo"`, "update yolo"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the refused launch does not say %q:\n%s\nerr: %v", want, stderr.String(), err)
		}
	}
}

// A TREE WITH NO RECORD, as an older launch staged, holding a field from a newer yolo, is read as
// the launch read it: the pack loads, and its skip is one note.
func TestAnUnrecordedTreeIsReadAsTheLaunchReadsIt(t *testing.T) {
	packHome(t)
	root := t.TempDir()
	dir := filepath.Join(root, "acme")
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"name":"acme","contributes":[`+
		`{"kind":"env","vars":{"A":"1"},"field_from_a_newer_yolo":1},{"kind":"env","vars":{"B":"2"}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packs, err := loadUnrecordedPackTree(root)
	if err != nil {
		t.Fatalf("an unrecorded tree holding a skip must load: %v", err)
	}
	if len(packs) != 1 || len(packs[0].SkewNotes) != 1 || !strings.Contains(packs[0].SkewNotes[0], "field_from_a_newer_yolo") {
		t.Errorf("the unrecorded tree's packs: %+v", packs)
	}
}
