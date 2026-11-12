package configmigrate

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// A workspace whose conversion rewrites its config proves the converted lines against the old
// reach: an unqualified `requests.github` line reached every remote, so the old side grants it on
// the second remote and the converted side (origin alone, GW-D79) does not. The proof names that
// as a difference and writes nothing; keeping the second remote's writes is the table migration's
// job, not this stage's.
func TestTheProofOfARewrittenWorkspaceHoldsTheUnqualifiedLinesOldReach(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git", "https://github.com/elsewhere/app.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{
  "brokered": {"github": {"grants": [
    {"kind": "dispatch-workflow", "repo": "github.com/acme/one", "workflow": "nightly.yml"}
  ]}},
  "requests": {"github": {"runs": ["ci.yml"]}}
}
`)
	g, err := Discover(Request{Workspace: ws})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	p, err := Plan(g)
	var inequality *InequalityError
	if !errors.As(err, &inequality) {
		t.Fatalf("Plan: %v, want the second remote's old reach named as an inequality", err)
	}
	for _, d := range inequality.Plan.Differences {
		if d.Path == "github.workspace_grants" && strings.Contains(d.Before, "github.com/elsewhere/app ci.yml") {
			return
		}
	}
	t.Fatalf("the differences do not name the old grant on elsewhere/app: %+v (plan %v)", p.Differences, p != nil)
}

// A workspace that holds no old spelling is not rewritten, so there is no old side to read and
// nothing to convert: its unqualified line stays as written, and the migration reports it equal.
func TestAWorkspaceWithNoOldSpellingIsNotRewritten(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git", "https://github.com/elsewhere/app.git")
	write(t, filepath.Join(ws, "yolo-jail.toml"), "[requests.github]\nruns = [\"ci.yml\"]\n")
	p := plan(t, Request{Workspace: ws})
	if !p.Equal {
		t.Fatalf("plan unequal: %+v", p.Differences)
	}
	if len(p.Targets) != 0 {
		t.Fatalf("a workspace with no old spelling is rewritten: %+v", p.Targets)
	}
}
