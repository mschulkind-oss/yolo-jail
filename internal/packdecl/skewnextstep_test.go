package packdecl

import (
	"strings"
	"testing"
)

// The skills merge order names the layers that exist, lowest first: there is no user layer, and
// the workspace and the local pack were missing.
func TestSkillsFootprintNamesTheRealMergeOrder(t *testing.T) {
	fp, ok := FootprintOf(KindSkills)
	if !ok || !strings.Contains(fp.Claims, "(workspace < built-in < packs < your local pack)") {
		t.Errorf("skills footprint = %+v, found=%v", fp, ok)
	}
}

// A skew skip names its next step, as the sibling notes do: a newer yolo knows the kind or via.
func TestSkewSkipNotesSayUpdateYolo(t *testing.T) {
	for _, note := range []string{
		unknownKindNote(0, Kind("kind-from-a-newer-yolo")),
		unknownViaSkip(0, Contribution{Kind: KindProgram, Via: "via-from-a-newer-yolo", Bin: "x"}),
	} {
		if !strings.HasSuffix(note, ": update yolo)") {
			t.Errorf("the skip note names no next step: %s", note)
		}
	}
}
