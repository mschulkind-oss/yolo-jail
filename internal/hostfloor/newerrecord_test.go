package hostfloor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeNewerRecord puts a record for bin that a newer yolo wrote (schema 99) in the floor, with
// the launcher that yolo generated beside it, and returns both paths.
func writeNewerRecord(t *testing.T, f *Floor, bin string) (rec, launcher string) {
	t.Helper()
	rec, launcher = f.recordPath(bin), f.Launcher(bin)
	must(t, os.MkdirAll(filepath.Dir(rec), 0o755))
	must(t, os.MkdirAll(filepath.Dir(launcher), 0o755))
	must(t, os.WriteFile(rec, []byte(`{"schema": 99, "bin": "`+bin+`"}`), 0o600))
	must(t, os.WriteFile(launcher, []byte("#!/bin/sh\necho the newer yolo's launcher\n"), 0o700))
	return rec, launcher
}

// TestARecordANewerYoloWroteNamesTheUpdate: a floor record whose schema this yolo does not know
// was written by a newer yolo, and the reason Status gives (which `yolo check` prints) used to
// end at "record schema 2 is newer than this yolo's 1", leaving the reader to work out how to
// get that yolo. It now names `yolo update`, in updatehint's sentence, the one every refusal of
// a newer yolo's file prints (docs/reference/happy-path-principle.md, rule 7).
func TestARecordANewerYoloWroteNamesTheUpdate(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	w := newWorld(t)
	p := npmProgram("opencode", "opencode", "opencode-ai")
	rec, _ := writeNewerRecord(t, w.floor, p.Bin())

	st := w.floor.Status(p)
	if st.Disposition != Missing || !st.Newer {
		t.Fatalf("disposition = %v, newer = %v, want Missing and newer for a record this yolo cannot read",
			st.Disposition, st.Newer)
	}
	for _, want := range []string{rec, "schema 99", "a newer yolo wrote it", "run `yolo update`"} {
		if !strings.Contains(st.Reason, want) {
			t.Errorf("the reason lacks %q:\n%s", want, st.Reason)
		}
	}
}

// TestEnsureRefusesToInstallOverANewerYolosRecord: Ensure read the newer record's error as
// "missing" and installed over it, replacing the newer yolo's launcher and record with this
// one's, though `yolo check` was saying to run `yolo update`. It now refuses, with that step, and
// leaves both files as the newer yolo wrote them, as a pack lockfile of a newer schema is refused
// rather than rewritten (docs/design/host-tool-provisioning.md, HP-D8).
func TestEnsureRefusesToInstallOverANewerYolosRecord(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	w := newWorld(t)
	w.publish("opencode-ai", "1.2.3", "bin=opencode")
	p := npmProgram("opencode", "opencode", "opencode-ai")
	rec, launcher := writeNewerRecord(t, w.floor, p.Bin())
	recBefore, _ := os.ReadFile(rec)
	launcherBefore, _ := os.ReadFile(launcher)

	_, outcome, err := w.floor.Ensure(context.Background(), p)
	if !errors.Is(err, ErrNewerRecord) {
		t.Fatalf("Ensure returned %v (outcome %q), want ErrNewerRecord's refusal:\n%s", err, outcome,
			w.out.String())
	}
	for _, want := range []string{rec, "a newer yolo wrote it", "run `yolo update`"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q:\n%v", want, err)
		}
	}
	if n := len(w.npmCalls("install")); n != 0 {
		t.Errorf("npm install ran %d time(s) for a refused install", n)
	}
	if got, _ := os.ReadFile(rec); string(got) != string(recBefore) {
		t.Errorf("the newer yolo's record was rewritten:\n%s", got)
	}
	if got, _ := os.ReadFile(launcher); string(got) != string(launcherBefore) {
		t.Errorf("the newer yolo's launcher was replaced:\n%s", got)
	}
}
