package cli

// skewedpackreview_test.go pins what the first skew tests left open (docs/design/patched-forks.md
// PF-D68 to PF-D71, PF-D75): a host refusal of a pack the use read still refuses names what it
// skipped; `yolo host apply`'s closing verdict over a skipped contribution says the pack could not be
// read whole, as the report above it does; `--format json` carries the skips; and `yolo features`
// through its front door prints the list and refuses an argument.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// A HOST REFUSAL names the field the use read could not read and `update yolo` beside the problem
// only the skip left: a restriction kept without it that no longer validates.
func TestAHostRefusalNamesWhatTheUseReadSkipped(t *testing.T) {
	_, packDir := skewedPackHome(t, "")
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"name":"bad","description":"d","contributes":[`+
		`{"kind":"autonomy","guarded_by_a_newer_yolo":{"launch":[{"bin":"claude"}]}}]}`)
	rc, reached, errw := hostExecRun(t, "claude")
	if rc == 0 || reached {
		t.Fatalf("a restriction that no longer validates must refuse the launch; rc=%d reached=%v\n%s", rc, reached, errw)
	}
	for _, want := range []string{"autonomy needs at least one of", `"guarded_by_a_newer_yolo"`, "update yolo"} {
		if !strings.Contains(errw, want) {
			t.Errorf("the host refusal does not say %q:\n%s", want, errw)
		}
	}
}

// THE CLOSING VERDICT over a pack that resolved but holds a contribution this yolo cannot read says
// it could not be read whole, as the report's group above it does, in the dry run's sentence.
func TestTheApplyVerdictSaysAPackCouldNotBeReadWhole(t *testing.T) {
	skewedPackHome(t, "")
	_, report := applyWith(t, false, nil)
	if w := "An --assert would REFUSE: 1 configured pack could not be resolved or read whole (bad)"; !strings.Contains(report, w) {
		t.Errorf("the dry run's verdict lacks %q:\n%s", w, report)
	}
}

// `yolo host apply --format json` CARRIES THE SKIPS per pack as skipped_contributions, without the
// "pack <name>: " prefix the record's name already says.
func TestApplyHostJSONCarriesTheSkippedContributions(t *testing.T) {
	skewedPackHome(t, "")
	var out, errw bytes.Buffer
	if rc := applyHostFormatted(&out, &errw, false, false, nil, "json", nil); rc != 0 {
		t.Fatalf("rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	var doc struct {
		Outcome    string `json:"outcome"`
		Unresolved []struct {
			Name    string   `json:"name"`
			Skipped []string `json:"skipped_contributions"`
		} `json:"unresolved_packs"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if doc.Outcome != outcomeRefused || len(doc.Unresolved) != 1 || doc.Unresolved[0].Name != "bad" ||
		len(doc.Unresolved[0].Skipped) != 1 || !strings.Contains(doc.Unresolved[0].Skipped[0], `"fieldFromANewerYolo"`) ||
		strings.HasPrefix(doc.Unresolved[0].Skipped[0], "pack bad: ") {
		t.Errorf("want outcome refused and bad's one skipped contribution: %+v", doc)
	}
}

// `yolo features` THROUGH ITS FRONT DOOR prints the list and exits 0, and an argument is refused,
// exit 2, rather than answered with the list.
func TestYoloFeaturesFrontDoor(t *testing.T) {
	var rc int
	out := captureStdout(t, func() { rc = registry["features"]([]string{"features"}) })
	if rc != 0 || !strings.Contains("\n"+out, "\npatch-series\n") {
		t.Errorf("`yolo features` must print patch-series and exit 0; rc=%d out=%q", rc, out)
	}
	out = captureStdout(t, func() { rc = registry["features"]([]string{"features", "patch-series"}) })
	if rc != 2 || out != "" {
		t.Errorf("`yolo features patch-series` must refuse with exit 2 and print no list; rc=%d out=%q", rc, out)
	}
}
