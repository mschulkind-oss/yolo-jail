package hostfloor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestARecordANewerYoloWroteNamesTheUpdate: a floor record whose schema this yolo does not know
// was written by a newer yolo, and the reason Status gives (which `yolo check` prints) used to
// end at "record schema 2 is newer than this yolo's 1", leaving the reader to work out how to
// get that yolo. It now names `yolo update`, in updatehint's sentence, the one every refusal of
// a newer yolo's file prints (docs/reference/happy-path-principle.md, rule 7).
func TestARecordANewerYoloWroteNamesTheUpdate(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	w := newWorld(t)
	p := npmProgram("opencode", "opencode", "opencode-ai")
	rec := w.floor.recordPath(p.Bin())
	must(t, os.MkdirAll(filepath.Dir(rec), 0o755))
	must(t, os.WriteFile(rec, []byte(`{"schema": 99, "bin": "opencode"}`), 0o600))

	st := w.floor.Status(p)
	if st.Disposition != Missing {
		t.Fatalf("disposition = %v, want Missing for a record this yolo cannot read", st.Disposition)
	}
	for _, want := range []string{rec, "schema 99", "a newer yolo wrote it", "run `yolo update`"} {
		if !strings.Contains(st.Reason, want) {
			t.Errorf("the reason lacks %q:\n%s", want, st.Reason)
		}
	}
}
