package packoverlay

// instructions_test.go pins the `instructions` kind's two config mechanisms through Collect
// (docs/design/workspace-instruction-files.md): the list lands appended after every other entry
// and anchored at the workspace, the set lands as an overlay, a keep or host selection places
// nothing, and an ownerless target is reported rather than rendered. Each test fails if
// placeInstructions' call site in Collect is deleted.

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func claudeInstructionsPack(sel *packdecl.InstructionsSelection) *packload.Pack {
	p := listPack("claude-instr", packdecl.Contribution{
		Kind: packdecl.KindInstructions, Agent: "claude", Bin: "claude",
		Reads: []string{"AGENTS.md"},
		Ignore: &packdecl.InstructionsIgnore{List: &packdecl.InstructionsList{
			Surface: "claude/settings", Path: "/claudeMdExcludes",
			Add: []string{"${workspace}/**/AGENTS.md", "${workspace}/**/CLAUDE.md"}}},
	})
	p.Instructions = sel
	return p
}

func TestInstructionsListIsAppendedAfterOtherEntriesAndAnchoredAtTheWorkspace(t *testing.T) {
	sel := &packdecl.InstructionsSelection{Mode: "ignore", Workspace: "/workspace", Home: "/home/agent"}
	packs := []*packload.Pack{
		ownerPack("claude"),
		listPack("mine", listContribution("claude/settings", "/claudeMdExcludes", `["/mine/**"]`)),
		claudeInstructionsPack(sel),
	}
	set := Collect(packs, true, nil)
	if len(set.Problems) != 0 || len(set.Orphans) != 0 {
		t.Fatalf("problems=%v orphans=%+v", set.Problems, set.Orphans)
	}
	var got []string
	for _, l := range set.ListsFor("claude", "settings") {
		for _, e := range l.Add {
			got = append(got, e.(string))
		}
	}
	want := []string{"/mine/**", "/workspace/**/AGENTS.md", "/workspace/**/CLAUDE.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %v, want the user's own first and the workspace-anchored ones after: %v", got, want)
	}
}

func TestInstructionsPlaceNothingForKeepNilAndHost(t *testing.T) {
	for name, sel := range map[string]*packdecl.InstructionsSelection{
		"nil":  nil,
		"keep": {Mode: "keep", Workspace: "/workspace"},
		"host": {Mode: "ignore", Workspace: "/w", Home: "/h", Host: true},
	} {
		set := Collect([]*packload.Pack{ownerPack("claude"), claudeInstructionsPack(sel)}, true, nil)
		if lists := set.ListsFor("claude", "settings"); len(lists) != 0 {
			t.Errorf("%s: placed %v, want nothing (host takes its flags through the launch)", name, lists)
		}
	}
}

func TestInstructionsSetFoldsAsAnOverlayOnTheOwnersSurface(t *testing.T) {
	sel := &packdecl.InstructionsSelection{Mode: "ignore", Workspace: "/workspace", Home: "/home/agent"}
	setPack := listPack("setter", packdecl.Contribution{
		Kind: packdecl.KindInstructions, Agent: "claude", Bin: "claude", Reads: []string{"AGENTS.md"},
		Ignore: &packdecl.InstructionsIgnore{Set: &packdecl.InstructionsSet{
			Surface: "claude/settings", Config: map[string]any{"project_doc_max_bytes": 0}}},
	})
	setPack.Instructions = sel
	set := Collect([]*packload.Pack{ownerPack("claude"), setPack}, true, nil)
	if len(set.Problems) != 0 || len(set.Orphans) != 0 {
		t.Fatalf("problems=%v orphans=%+v", set.Problems, set.Orphans)
	}
	overlays := set.For("claude", "settings")
	if len(overlays) != 1 || overlays[0].Pack != "setter" {
		t.Fatalf("overlays = %+v, want the one from the instructions pack", overlays)
	}
	data, _ := overlays[0].Data.(map[string]any)
	// An INTEGER, not the float a JSON round trip makes of it: the TOML codec would write 0.0, which
	// codex refuses for project_doc_max_bytes (seen in a nested jail).
	if got, ok := data["project_doc_max_bytes"].(int64); !ok || got != 0 {
		t.Errorf("overlay data = %#v, want the declared setting as an int64 in the managed layer", overlays[0].Data)
	}
}

func TestInstructionsOnAnUnownedSurfaceIsReportedNotRendered(t *testing.T) {
	sel := &packdecl.InstructionsSelection{Mode: "ignore", Workspace: "/workspace"}
	set := Collect([]*packload.Pack{claudeInstructionsPack(sel)}, true, nil)
	if len(set.ListsFor("claude", "settings")) != 0 {
		t.Errorf("a list landed on a surface no selected pack owns")
	}
	if len(set.Orphans) != 1 || set.Orphans[0].Kind != packdecl.KindInstructions {
		t.Errorf("orphans = %+v, want one reported as the instructions kind", set.Orphans)
	}
}
