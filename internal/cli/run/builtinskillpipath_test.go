package run

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE BUILT-IN configuring-the-jail SKILL NAMES WHERE PI'S JAIL STATE LIVES ON THE HOST, and it
// named `<workspace>/.yolo/state/pi`, a directory no launch makes
// (docs/design/agent-directory-map.md Appendix B). The skill is staged into every jail, so that
// path is one an agent repeats to its human.
//
// The expected path is read off the launch itself rather than retyped: the podman bind whose
// destination is pi's home directory, from the wsState production hands the assembler
// (paths.WorkspaceHomeState, prepareWsState), and Apple Container's spelling of the same
// directory through wsStateHomeRel, the function that backend's whole-home bind is laid out by.
// Moving either bind fails this test until the skill follows.
func TestConfiguringTheJailSkillNamesPisRealOverlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	ws := "/ws"
	wsState := paths.WorkspaceHomeState(ws)
	o := goldenOptions(ws, home)
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	argv := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("security", sec),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		packs:        packsFixture(t, "pi"),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      wsState,
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
	var podmanRel string
	for i := 0; i+1 < len(argv); i++ {
		if src, ok := strings.CutSuffix(argv[i+1], ":/home/agent/.pi"); argv[i] == "-v" && ok {
			rel, err := filepath.Rel(ws, src)
			if err != nil {
				t.Fatal(err)
			}
			podmanRel = filepath.ToSlash(rel)
		}
	}
	if podmanRel == "" {
		t.Fatalf("no podman bind for /home/agent/.pi in the argv: %v", argv)
	}
	acRel := filepath.ToSlash(filepath.Join(paths.WorkspaceHomeState("."), wsStateHomeRel("container", ".pi")))

	b, err := builtinskills.FS.ReadFile("configuring-the-jail/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	skill := string(b)
	for _, want := range []string{"<workspace>/" + podmanRel, acRel} {
		if !strings.Contains(skill, want) {
			t.Errorf("configuring-the-jail does not name pi's overlay as the launch lays it out (%q)", want)
		}
	}
	if strings.Contains(skill, ".yolo/state/") {
		t.Errorf("configuring-the-jail still names .yolo/state/, a directory no launch makes")
	}
}
