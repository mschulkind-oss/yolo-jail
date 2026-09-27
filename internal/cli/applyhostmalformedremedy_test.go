package cli

// applyhostmalformedremedy_test.go pins the REMEDY `yolo host apply` prints for a pack refused
// over its problems (unresolvedPackGroups), which the dry run, the --assert refusal and the launch
// gate share.
//
// The bug: one group served every such pack, and it told the user to "remove it from `packs` in
// <config.jsonc>" and to fix each problem "in the pack's manifest". The conventional local pack
// (~/.config/yolo-jail/local) is in no `packs` list — config.LoadPacks includes it because the
// directory exists (PackEntry.Implicit) — and it is the pack most likely to be malformed, being the
// user's own. And a problem such as briefing/CLAUDE.md is a FILE in the pack, fixed by renaming it,
// not by editing the manifest.

import (
	"path/filepath"
	"strings"
	"testing"
)

// remedyLine is the report's `→` line under the group whose headline contains headline, or "".
func remedyLine(report, headline string) string {
	lines := strings.Split(report, "\n")
	for i, line := range lines {
		if !strings.Contains(line, headline) {
			continue
		}
		for _, next := range lines[i+1:] {
			if strings.Contains(next, "→") {
				return next
			}
		}
	}
	return ""
}

// THE LOCAL PACK HAS NO `packs` ENTRY TO REMOVE, so its remedy names its directory instead — in
// the dry run and in the --assert refusal alike. The control is the same malformed manifest as a
// configured pack, whose remedy still offers the `packs` edit.
func TestApplyHostRemedyForAMalformedLocalPackNamesItsDirectory(t *testing.T) {
	for _, implicit := range []bool{true, false} {
		var dir string
		if implicit {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("YOLO_PACK_ROOT", "")
			t.Chdir(t.TempDir())
			selectPacksWith(t, home, `"claude"`, "")
			dir = filepath.Join(home, ".config", "yolo-jail", "local")
			writeFile(t, filepath.Join(dir, "pack.json"), twoAutonomies("local", true))
		} else {
			_, dir = malformedPackHome(t, "", true, "")
		}
		for _, write := range []bool{false, true} {
			rc, report := applyWith(t, write, strings.NewReader("y\n"))
			if write && rc == 0 {
				t.Fatalf("implicit=%v: --assert must refuse\n%s", implicit, report)
			}
			line := remedyLine(report, "nothing can be applied")
			if line == "" {
				t.Fatalf("implicit=%v write=%v: no remedy line:\n%s", implicit, write, report)
			}
			offersPacksEdit := strings.Contains(line, "remove it from `packs`")
			if !implicit {
				if !offersPacksEdit {
					t.Errorf("fixture control: a configured pack's remedy offers the `packs` edit:\n%s", line)
				}
				continue
			}
			if offersPacksEdit {
				t.Errorf("write=%v: the local pack's remedy says to remove it from `packs`, where it "+
					"has no entry:\n%s", write, line)
			}
			for _, want := range []string{dir, "yolo pack lint", "no `packs` entry"} {
				if !strings.Contains(line, want) {
					t.Errorf("write=%v: the local pack's remedy must contain %q:\n%s", write, want, line)
				}
			}
		}
	}
}

// A PROBLEM IN A FILE IS FIXED IN THE PACK, NOT ITS MANIFEST: the group's words cover both
// classes, so a briefing/CLAUDE.md is not reported as a manifest to edit.
func TestApplyHostRemedyForAPackProblemDoesNotSendTheUserToTheManifest(t *testing.T) {
	filteredPackHome(t, cleanFltManifest, reservedBriefing, "", "")
	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("dry run rc=%d\n%s", rc, report)
	}
	line := remedyLine(report, "nothing can be applied")
	if line == "" || !strings.Contains(line, "yolo pack lint") {
		t.Fatalf("no lint remedy line:\n%s", report)
	}
	if strings.Contains(line, "manifest") {
		t.Errorf("the remedy for a reserved FILE sends the user to the manifest:\n%s", line)
	}
	if strings.Contains(report, "whose manifest has problems") {
		t.Errorf("the headline calls a file problem a manifest's:\n%s", report)
	}
}
