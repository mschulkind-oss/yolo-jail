package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostfloordeselected_test.go pins what `yolo host` does with a DESELECTED floor entry — one a
// launch installed while its pack was selected and the floor held it, and which stays in the
// prefix until `yolo host apply --assert` removes it: it is never run, whether the pack was
// deselected or `host_floor` now leaves it out, and a launch that finds no other copy says what
// removes it.

// deselectedFixture installs floorcli into the floor through a launch, then rewrites the user
// config to cfg. It returns the hand-installed copy's directory, which is NOT on PATH afterwards.
func deselectedFixture(t *testing.T, cfg func(pack string) string) (handInstalledDir string) {
	t.Helper()
	_, pack := floorHostFixture(t, "")
	handInstalledDir = filepath.Dir(filepath.Join(stubBins(t, "floorcli"), "floorcli"))
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("the installing launch: rc=%d\n%s", rc, errw.String())
	}
	if _, err := os.Stat(filepath.Join(paths.HostFloorDir(), "bin", "floorcli")); err != nil {
		t.Fatalf("the launch left no floor entry to be deselected: %v", err)
	}
	writeFile(t, paths.UserConfigPath(), cfg(pack))
	// A PATH with no other floorcli on it: a folder this test made and left empty, never the
	// machine's /usr/bin and /bin, whose contents are not the test's to know.
	t.Setenv("PATH", t.TempDir())
	return handInstalledDir
}

// TestADeselectedFloorEntryIsNeverRun: in both cases, from a PATH with no other floorcli the
// launch refuses with 127 and names the act that removes the entry; from a PATH that has one,
// that copy runs and the hand-over line says it came from the PATH — never the floor's copy
// under a "from your PATH" label.
func TestADeselectedFloorEntryIsNeverRun(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  func(pack string) string
	}{
		{"its pack deselected", func(string) string { return `{"packs":[]}` }},
		{"host_floor leaves its pack out", func(pack string) string {
			return `{"packs":[{"source":"file://` + pack + `","name":"floorpack"}],"host_floor":{"floorpack":false}}`
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			handDir := deselectedFixture(t, c.cfg)
			got := captureHostExec(t)
			var errw bytes.Buffer
			rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil)
			if rc != 127 || got.execed {
				t.Fatalf("rc=%d execed=%v (target %s), want 127 and no exec: the deselected entry ran\n%s",
					rc, got.execed, got.target, errw.String())
			}
			// Unset host_management is "none" (OQ-CO14), under which `yolo host apply --assert`
			// writes nothing: the step is the removal by hand, never that apply by itself.
			if !strings.Contains(errw.String(), "yolo host does not run it: remove it by hand with `rm -rf ") {
				t.Errorf("the refusal does not say what the entry is and what removes it:\n%s", errw.String())
			}
			if strings.Contains(errw.String(), "`yolo host apply --assert` removes it\n") {
				t.Errorf("under none the refusal names `yolo host apply --assert`, which writes nothing there:\n%s",
					errw.String())
			}

			t.Setenv("PATH", handDir+string(os.PathListSeparator)+t.TempDir())
			errw.Reset()
			*got = execCapture{}
			if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 {
				t.Fatalf("with a PATH copy: rc=%d\n%s", rc, errw.String())
			}
			want := filepath.Join(handDir, "floorcli")
			if got.target != want {
				t.Errorf("exec'd %s, want the PATH copy %s", got.target, want)
			}
			if !strings.Contains(errw.String(), "starting floorcli (from your PATH, "+want+")") {
				t.Errorf("the hand-over line does not name the PATH copy:\n%s", errw.String())
			}
		})
	}
}

// UNDER "own" THE DESELECTED ENTRY'S STEP IS THE APPLY, which runs Reconcile there.
func TestADeselectedFloorEntryNamesTheApplyUnderOwn(t *testing.T) {
	deselectedFixture(t, func(string) string { return `{"packs":[],"host_management":"own"}` })
	captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 127 {
		t.Fatalf("rc=%d, want 127\n%s", rc, errw.String())
	}
	if !strings.Contains(errw.String(), "yolo host does not run it: `yolo host apply --assert` removes it") ||
		strings.Contains(errw.String(), "by hand") {
		t.Errorf("under own the refusal does not name the apply:\n%s", errw.String())
	}
}
