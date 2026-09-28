package packdecl

import (
	"strings"
	"testing"
)

// `served_by` (docs/plans/notch-convergence.md §2.4) names the jail daemon an env
// contribution's variables point at. It is env's alone, like `profile`, so a declaration that
// would silently do nothing on another kind is refused, and the accessors carry it per variable.
func TestServedByIsAnEnvDeclaration(t *testing.T) {
	m, problems := Decode([]byte(`{"name": "p", "contributes": [
	  {"kind": "env", "vars": {"PLAIN": "1"}},
	  {"kind": "env", "served_by": "adapter-a", "vars": {"POINTER": "http://127.0.0.1:1/x"}},
	  {"kind": "env", "profile": "gate", "served_by": "adapter-b", "vars": {"GATED": "http://127.0.0.1:2/y"}}
	]}`))
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	servedBy := m.EnvServedBy()
	if servedBy["POINTER"] != "adapter-a" || servedBy["PLAIN"] != "" || servedBy["GATED"] != "" {
		t.Errorf("EnvServedBy = %v, want only the unconditional POINTER, naming adapter-a", servedBy)
	}
	gated := m.ProfiledEnvContributions()
	if len(gated) != 1 || gated[0].ServedBy != "adapter-b" {
		t.Errorf("the gated contribution lost its served_by: %+v", gated)
	}

	_, problems = Decode([]byte(`{"name": "p", "contributes": [
	  {"kind": "skills", "from": "skills", "into": ".claude/skills", "served_by": "x"}]}`))
	if !strings.Contains(strings.Join(problems, "\n"), `does not take "served_by"`) {
		t.Errorf("served_by on a skills contribution was not refused: %v", problems)
	}
}
