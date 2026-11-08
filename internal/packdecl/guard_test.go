package packdecl

import (
	"strings"
	"testing"
)

func TestGuardDecodes(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"g","contributes":[
		{"kind":"blocked-tool","bin":"rm","guard":"workspace-state","message":"m"}]}`))
	if len(probs) != 0 {
		t.Fatalf("problems %v", probs)
	}
	if c := m.Contributions()[0]; c.Kind != KindBlockedTool || c.Guard != GuardWorkspaceState {
		t.Fatalf("contribution %+v", c)
	}
}

func TestGuardRefusesWhatNoGuardCouldRender(t *testing.T) {
	for name, c := range map[string]struct{ entry, want string }{
		"unknown path set": {`{"kind":"blocked-tool","bin":"rm","guard":"home"}`, "not a path set yolo knows"},
		"unknown tool":     {`{"kind":"blocked-tool","bin":"cp","guard":"workspace-state"}`, "knows those of mv and rm only"},
		"flags beside":     {`{"kind":"blocked-tool","bin":"rm","guard":"workspace-state","flags":["-r"]}`, `does not take "flags"`},
		"replacement":      {`{"kind":"blocked-tool","bin":"mv","guard":"workspace-state","replacement":"x"}`, `does not take "replacement"`},
		"other kind":       {`{"kind":"intercept","bin":"rm","forward":["yolo"],"guard":"workspace-state"}`, `does not take "guard"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"x","contributes":[` + c.entry + `]}`))
			if !strings.Contains(strings.Join(probs, "\n"), c.want) {
				t.Fatalf("problems %v, want one mentioning %q", probs, c.want)
			}
		})
	}
}
