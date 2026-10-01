package packdecl

import (
	"reflect"
	"strings"
	"testing"
)

// `project_dirs` is decoded, validated per entry, and read back through the one accessor
// (docs/reference/agent-briefings.md, the workspace layer).
func TestProjectDirsIsReadFromASkillsDestination(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"skills","agent":"acme","into":".acme/skills","project_dirs":[".acme/skills",".agents/skills"]},
	  {"kind":"skills","agent":"acme2","into":".acme2/skills","project_dirs":[".agents/skills",".acme2/skills"]}]}`))
	if len(probs) != 0 {
		t.Fatalf("a well-formed declaration was refused: %v", probs)
	}
	want := []string{".acme/skills", ".agents/skills", ".acme2/skills"}
	if got := m.ProjectSkillDirs(); !reflect.DeepEqual(got, want) {
		t.Errorf("ProjectSkillDirs = %v, want %v (declaration order, each spelled once)", got, want)
	}
}

func TestProjectDirsRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ entry, want string }{
		"absolute":      {`{"kind":"skills","agent":"a","into":".a/skills","project_dirs":["/etc/skills"]}`, "not absolute"},
		"climbs out":    {`{"kind":"skills","agent":"a","into":".a/skills","project_dirs":["../x/skills"]}`, "stay inside the workspace"},
		"climbs deeper": {`{"kind":"skills","agent":"a","into":".a/skills","project_dirs":["a/../../x"]}`, "stay inside the workspace"},
		"the root":      {`{"kind":"skills","agent":"a","into":".a/skills","project_dirs":["."]}`, "workspace root"},
		"unclean":       {`{"kind":"skills","agent":"a","into":".a/skills","project_dirs":["./.a/skills/"]}`, `write it as ".a/skills"`},
		"empty":         {`{"kind":"skills","agent":"a","into":".a/skills","project_dirs":[""]}`, "empty path"},
		"backslash":     {`{"kind":"skills","agent":"a","into":".a/skills","project_dirs":[".a\\skills"]}`, `"/" separators`},
		"git":           {`{"kind":"skills","agent":"a","into":".a/skills","project_dirs":[".git/skills"]}`, "git's metadata"},
		"yolo":          {`{"kind":"skills","agent":"a","into":".a/skills","project_dirs":[".yolo/skills"]}`, "yolo's own state"},
		"twice":         {`{"kind":"skills","agent":"a","into":".a/skills","project_dirs":[".a/skills",".a/skills"]}`, "declared twice"},
		"on content":    {`{"kind":"skills","agents":["a"],"project_dirs":[".a/skills"]}`, "skills DESTINATION"},
		"on briefing":   {`{"kind":"briefing","agent":"a","into":".a/AGENTS.md","project_dirs":[".a/skills"]}`, `does not take "project_dirs"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"acme","contributes":[` + tc.entry + `]}`))
			if !strings.Contains(strings.Join(probs, "; "), tc.want) {
				t.Errorf("want a problem containing %q, got %v", tc.want, probs)
			}
		})
	}
}
