package cli

// packexpects_test.go pins the authoring half of a REGISTERING files slot's `expects`
// (docs/design/pack-pi-resources.md PR-D4): `yolo pack lint` and `yolo pack footprint` of a pack
// whose pi folder holds none of the names the shipped pi pack's slot expects warn, naming the folder
// and the next step, and say nothing about a folder laid out as a pi package. A warning, never a
// failure: both still exit 0.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePiFolderPack writes a pack addressing pi with files/pi, holding `entries` (directories).
func writePiFolderPack(t *testing.T, entries ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "matt")
	for _, e := range entries {
		if err := os.MkdirAll(filepath.Join(dir, "files", "pi", e), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "files", "pi", e, "a.ts"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"name":"matt","contributes":[{"kind":"files","agents":["pi"],"from":"files/pi"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPackLintAndFootprintWarnAboutAPiFolderPiCannotLoad(t *testing.T) {
	for _, verb := range []string{"lint", "footprint"} {
		t.Run(verb, func(t *testing.T) {
			var out, errw bytes.Buffer
			if rc := packMain([]string{verb, writePiFolderPack(t, "exts")}, &out, &errw, false); rc != 0 {
				t.Fatalf("%s rc = %d, want 0 (a warning, not a failure)\n%s%s", verb, rc, out.String(), errw.String())
			}
			report := out.String()
			for _, want := range []string{`"files/pi"`, "extensions, themes", "~/.pi/agent/yolo-packs/matt",
				"files/pi/extensions/"} {
				if !strings.Contains(report, want) {
					t.Errorf("`pack %s` does not say %q:\n%s", verb, want, report)
				}
			}

			out.Reset()
			if rc := packMain([]string{verb, writePiFolderPack(t, "extensions", "themes")}, &out, &errw, false); rc != 0 {
				t.Fatalf("%s rc = %d on a well-formed folder\n%s", verb, rc, out.String())
			}
			if strings.Contains(out.String(), "unlikely to load") {
				t.Errorf("`pack %s` warned about a folder laid out as a pi package:\n%s", verb, out.String())
			}
		})
	}
}

// A PI FOLDER SHIPPING skills/ (docs/design/pack-pi-resources.md §3.4): pi loads those skills for
// pi alone, so `yolo pack lint` notes that the `skills` kind reaches every agent, and how to move
// them there. A note, never a failure; and nothing is said of a folder with no skills/.
func TestPackLintNotesThatAPiFoldersSkillsReachPiAlone(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", writePiFolderPack(t, "extensions", "skills/review")}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint rc = %d, want 0 (a note, not a failure)\n%s%s", rc, out.String(), errw.String())
	}
	if !hasLine(out.String(), "pack matt", `"files/pi"`, "skills/", "every agent") {
		t.Errorf("`pack lint` does not note that the folder's skills reach pi alone:\n%s", out.String())
	}
	if !hasLine(out.String(), "files/pi/skills/", "skills/ at the pack's root") {
		t.Errorf("`pack lint` does not say where to move the skills:\n%s", out.String())
	}

	out.Reset()
	if rc := packMain([]string{"lint", writePiFolderPack(t, "extensions", "themes")}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint rc = %d on a folder with no skills/\n%s", rc, out.String())
	}
	if strings.Contains(out.String(), "every agent") {
		t.Errorf("`pack lint` noted skills for a folder with none:\n%s", out.String())
	}
}
