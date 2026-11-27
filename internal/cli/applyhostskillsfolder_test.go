package cli

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// A scoped import selecting one marked skill and one markerless folder: the host
// apply delivers both, counts and advertises only the skill. Fails with the
// folder label deleted from writeSourcedRegistrations or printSkillResult.
func TestHostApplyMarkerlessFolderIsDeliveredButNeverCountedOrAdvertisedAsASkill(t *testing.T) {
	user := testconfig.User(t, `packs = ["local/reader", {pack="local/modern", skills={only=["marked", "folder"]}}]
`)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Setenv(paths.VerboseEnv, "1")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(user.Root, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(user.Root, ".local/share"))
	root := filepath.Dir(user.Path)
	scopedCLIWrite(t, filepath.Join(root, "packs/reader/pack.toml"), "[[skills]]\nagent = \"fixture\"\ninto = \".fixture/skills\"\n")
	scopedCLIWrite(t, filepath.Join(root, "packs/modern/skills/marked/SKILL.md"), "MODERN-MARKER\n")
	scopedCLIWrite(t, filepath.Join(root, "packs/modern/skills/folder/notes.md"), "MODERN-FOLDER\n")
	stubDeclaredBins(t)

	var out, errw bytes.Buffer
	survey := &hostApplySurvey{}
	if rc := applyHostSurveyed(&out, &errw, false, true, strings.NewReader(""), survey); rc != 0 {
		t.Fatalf("apply rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	composed := survey.SkillNames(skillComposed)
	if slices.Contains(composed, "modern-folder") || !slices.Contains(composed, "modern-marked") {
		t.Fatalf("census counted %v, want the marked skill and not the folder\n%s", composed, report)
	}
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(line, "modern-folder") && strings.Contains(line, "invoke as") {
			t.Fatalf("a folder with no SKILL.md is advertised as invocable: %s", line)
		}
		if strings.Contains(line, "skills     modern-marked") && !strings.Contains(line, "invoke as /modern-marked") {
			t.Fatalf("a marked skill lost its invocation line: %s", line)
		}
	}
	if !strings.Contains(report, "folder, no SKILL.md") {
		t.Fatalf("folder row is not labeled:\n%s", report)
	}
}
