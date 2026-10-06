package packload

// useread_test.go pins the host's USE READ (LoadDirForUse, docs/design/patched-forks.md PF-D68):
// on the host, outside TolerateSkew, a contribution this build cannot read is skipped and named
// in Pack.SkewNotes, while LoadDir — the authoring read `yolo pack lint` runs — still refuses the
// same manifest. And the tree record carries the notes a launch printed, for the boot.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

const newerFieldManifest = `{"name":"acme","contributes":[
	{"kind":"skills","from":"skills","into":".acme/skills","field_from_a_newer_yolo":1},
	{"kind":"env","vars":{"ACME":"1"}}]}`

func TestLoadDirForUseSkipsWhatLoadDirRefuses(t *testing.T) {
	restore := OverrideSkewTolerance(false)
	defer restore()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName), []byte(newerFieldManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	p, problems := LoadDirForUse(dir, "acme")
	if len(problems) != 0 {
		t.Fatalf("the use read must not refuse a field from a newer yolo: %v", problems)
	}
	if len(p.SkewNotes) != 1 || !strings.HasPrefix(p.SkewNotes[0], "pack acme: contributes[0]: skipping") ||
		!strings.Contains(p.SkewNotes[0], `"field_from_a_newer_yolo"`) {
		t.Errorf("the skip must be named, by pack: %q", p.SkewNotes)
	}
	if env := p.Decl.EnvContributions(); env["ACME"] != "1" || len(p.Decl.Contributions()) != 1 {
		t.Errorf("only the readable contribution may remain: %+v", p.Decl.Contributions())
	}

	if _, problems := LoadDir(dir, "acme"); len(problems) == 0 {
		t.Error("the authoring read must still refuse the field")
	}
}

func TestThePackTreeRecordCarriesTheSkipsTheLaunchPrinted(t *testing.T) {
	restore := OverrideSkewTolerance(false)
	defer restore()
	root := t.TempDir()
	dir := filepath.Join(root, "acme-slug")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName), []byte(newerFieldManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := LoadDirForUse(dir, "acme")
	if err := WritePackTreeRecord(root, []*Pack{p}); err != nil {
		t.Fatal(err)
	}
	rec, recorded, err := ReadPackTreeRecord(root)
	if err != nil || !recorded || len(rec) != 1 {
		t.Fatalf("ReadPackTreeRecord = %+v, %v, %v", rec, recorded, err)
	}
	if len(rec[0].Skipped) != 1 || !strings.HasPrefix(rec[0].Skipped[0], "contributes[0]: skipping") {
		t.Errorf("the record must carry the note without its pack prefix: %q", rec[0].Skipped)
	}
}
