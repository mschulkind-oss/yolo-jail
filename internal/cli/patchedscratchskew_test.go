package cli

// patchedscratchskew_test.go pins that the scratch acts, `yolo pack series check` and `yolo pack
// rebase --pack`, read a pack as a launch does, a USE READ (docs/design/patched-forks.md PF-D68),
// and say each contribution it skips as a launch says it (PF-D72): a contribution this yolo cannot
// read leaves the rest of the pack checked, and a patched fork skipped for a field this yolo does not
// know is named rather than read as a pack that declares none.

import (
	"path/filepath"
	"strings"
	"testing"
)

// A SIBLING THIS YOLO CANNOT READ is skipped and said, and the series beside it is still checked.
func TestTheScratchActsSayASkippedSiblingAndCheckTheSeries(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	writeFile(t, f.manifest, `{"name":"forkpack","contributes":[{"kind":"kind-from-a-newer-yolo"},`+
		`{"kind":"program","bin":"tool","via":"source","fork_of":"basepack","source":"git+file://`+f.repo+
		`?ref=main","patches":"patches","build":"sh build.sh","produces":[".local/bin/tool"]}]}`)
	rc, out, errw := seriesVerb(t, "check", f.forkDir)
	if rc != 0 || !strings.Contains(errw, `Warning: pack forkpack: contributes[0]: skipping unknown kind "kind-from-a-newer-yolo"`) ||
		!strings.Contains(out, "takes the series") {
		t.Errorf("the series check beside a skipped kind: rc=%d\n%s\n%s", rc, out, errw)
	}
}

// A PATCHED FORK THIS YOLO CANNOT READ is named, with its field and `update yolo`, before the
// refusal that finds no series: both scratch acts.
func TestTheScratchActsNameAPatchedForkTheyCannotRead(t *testing.T) {
	f := newPatchedFixture(t, "")
	writeFile(t, f.manifest, `{"name":"forkpack","contributes":[{"kind":"program","bin":"tool","via":"source",`+
		`"fork_of":"basepack","source":"git+file://`+f.repo+`?ref=main","patches":"patches",`+
		`"field_from_a_newer_yolo":true,"build":"sh build.sh","produces":[".local/bin/tool"]}]}`)
	skip := `Warning: pack forkpack: contributes[0]: skipping the program contribution "tool" — unknown field ` +
		`"field_from_a_newer_yolo"`
	rc, out, errw := seriesVerb(t, "check", f.forkDir)
	if rc != 1 || !strings.Contains(errw, skip) || !strings.Contains(errw, "update yolo") ||
		!strings.Contains(errw, "declares no patch series") {
		t.Errorf("the series check of an unreadable patched fork: rc=%d\n%s\n%s", rc, out, errw)
	}
	rc, out, errw = rebaseVerb(t, "forkpack/tool", "--pack", f.forkDir, "--into", filepath.Join(t.TempDir(), "clone"))
	if rc != 1 || !strings.Contains(errw, skip) || !strings.Contains(errw, "declares no patched fork or extension") {
		t.Errorf("the scratch rebase of an unreadable patched fork: rc=%d\n%s\n%s", rc, out, errw)
	}
}
