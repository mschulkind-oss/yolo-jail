package integration

// skewedpack_test.go is request 1's verification end to end in a real container
// (docs/design/patched-forks.md PF-D60): a pack holding a contribution with a field this yolo does
// not know — the shape a patched extension's `patches` had for a yolo older than the mode —
// launches, the readable part of the pack reaches the jail, the skipped part does not, and the
// launch says so in ONE line: the host prints it, and the boot, which skips the same contribution,
// logs it instead of printing it again. Only a started container proves the second half, since the
// boot's read and the record it consults are the entrypoint's.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestALaunchRunsAPackWrittenForANewerYoloAndSaysWhatItLeftOut(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	for _, sub := range []string{"files", "newer"} {
		if err := os.MkdirAll(filepath.Join(pack, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pack, sub, "marker.txt"),
			[]byte("SKEWMARKER-"+sub+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"skewpack","contributes":[`+
		`{"kind":"files","from":"newer","into":".skewpack-newer","field_from_a_newer_yolo":"x"},`+
		`{"kind":"files","from":"files","into":".skewpack"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["file://`+pack+`"]}`)

	r := runYolo(t, dir, `cat /home/agent/.skewpack/marker.txt && `+
		`(cat /home/agent/.skewpack-newer/marker.txt 2>/dev/null || echo SKIPPED-ABSENT)`)
	if r.rc != 0 {
		t.Fatalf("a pack written for a newer yolo must not fail the launch: rc %d\nstdout: %s\nstderr: %s",
			r.rc, r.stdout, r.stderr)
	}
	for _, want := range []string{"SKEWMARKER-files", "SKIPPED-ABSENT"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q: the readable contribution reaches the jail and the skipped one "+
				"does not:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "SKEWMARKER-newer") {
		t.Errorf("the skipped contribution was delivered:\n%s", r.stdout)
	}
	out := r.combined()
	if n := strings.Count(out, `unknown field "field_from_a_newer_yolo"`); n != 1 {
		t.Errorf("one skipped contribution is one line at launch, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "skipping the files contribution") || !strings.Contains(out, "update yolo") {
		t.Errorf("the line must name the contribution and the next step:\n%s", out)
	}
}
