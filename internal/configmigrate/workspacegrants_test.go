package configmigrate

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// The proof's grants the brokers resolve carries the workspace half (RC-D60): per source, the
// concrete grants the scope projection makes from the old brokered grants and from the converted
// requests. Fails if the proof stops projecting the workspace's grants.
func TestTheProofComparesAWorkspacesBrokerGrants(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "git@github.com:acme/one.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"git": {"grants": [
		{"kind": "branch", "repo": "github.com/acme/one", "branch": "main"},
		{"kind": "create-branch", "repo": "github.com/acme/one"}
	]}}}`+"\n")
	p := plan(t, Request{Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	for _, want := range []string{`"workspace_grants"`, `"branch github.com/acme/one main"`, `"create-branch github.com/acme/one"`} {
		if !strings.Contains(p.canonical.grants, want) {
			t.Fatalf("the proof does not carry the workspace's grants (%s): %s", want, p.canonical.grants)
		}
	}
}

// A conversion that changes what a broker would grant the workspace is a difference in the grants
// the brokers resolve, even where the files say the same lowered: a workflow grant whose name is
// no workflow file is one the launch never reads (it reaches no scope), and its request would ask
// the broker for it. Fails if the proof stops comparing the workspace's projected grants.
func TestTheProofNamesAWorkspaceGrantTheConversionChanges(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"grants": [{"kind": "runs", "repo": "github.com/acme/one", "workflow": "nightly"}]}}}`+"\n")
	g, err := Discover(Request{Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Plan(g)
	var ineq *InequalityError
	if !errors.As(err, &ineq) {
		t.Fatalf("Plan = %v; want the proof to refuse a conversion that changes the workspace's grants", err)
	}
	named := false
	for _, d := range p.Differences {
		if d.Layer == brokerGrantsLayer && strings.HasPrefix(d.Path, "github.workspace_") {
			named = true
			if !strings.Contains(d.Next, "run `yolo config migrate` again") {
				t.Errorf("the difference names no next step: %+v", d)
			}
		}
	}
	if !named {
		t.Fatalf("no difference names the workspace's github grants: %+v", p.Differences)
	}
}

// The layer itself: converted requests that drop a branch the old grants named are a difference
// at <source>.workspace_grants, compared as a set of kind, repository and name, and the same
// grants in either spelling are none.
func TestTheWorkspaceGrantLayerComparesKindRepoAndName(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git")
	declarations, err := shippedBrokers()
	if err != nil {
		t.Fatal(err)
	}
	project := func(text string) string {
		grants := mustJSON(t, "{}")
		addWorkspaceBrokerGrants(grants, mustJSON(t, text), ws, declarations)
		text, err := config.SnapshotJSON(grants)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	old := `{"brokered": {"git": {"grants": [
		{"kind": "branch", "repo": "github.com/acme/one", "branch": "patch/b"},
		{"kind": "branch", "repo": "github.com/acme/one", "branch": "main"},
		{"kind": "create-branch", "repo": "github.com/acme/one"}]}}}`
	same := `{"requests": {"git": {"push": ["main", "patch/b"], "new_branches": true}}}`
	if a, b := project(old), project(same); a != b {
		t.Fatalf("the same grants in either spelling project differently:\n  old: %s\n  new: %s", a, b)
	}
	declarationsGrants := mustJSON(t, "{}")
	addWorkspaceBrokerGrants(declarationsGrants, mustJSON(t, old), ws, declarations)
	dropped := mustJSON(t, "{}")
	addWorkspaceBrokerGrants(dropped, mustJSON(t, `{"requests": {"git": {"push": ["main"], "new_branches": true}}}`), ws, declarations)
	diffs := diffValues(brokerGrantsLayer, declarationsGrants, dropped)
	if len(diffs) != 1 || diffs[0].Path != "git.workspace_grants" {
		t.Fatalf("a dropped branch was not named at git.workspace_grants: %+v", diffs)
	}
}

// A converted pattern is noted once in the transcript, naming each one: a pattern is accepted in
// both spellings (GB-D25). Fails if the note goes, or is said once per pattern.
func TestTheTranscriptNotesOnceThatAPatternIsAccepted(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"git": {"grants": [
		{"kind": "branch", "repo": "github.com/acme/one", "branch": "main"},
		{"kind": "branch", "repo": "github.com/acme/one", "branch": "patch/*"},
		{"kind": "tag", "repo": "github.com/acme/one", "tag": "v*"}
	]}}}`+"\n")
	p := plan(t, Request{Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	out := p.Transcript()
	const lead = "A pattern converts as written"
	if n := strings.Count(out, lead); n != 1 {
		t.Fatalf("the pattern note appears %d times, want once:\n%s", n, out)
	}
	for _, want := range []string{`requests.git.push "patch/*"`, `"v*"`, "Names listed one by one stay listed one by one."} {
		if !strings.Contains(out, want) {
			t.Fatalf("the transcript lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `requests.git.push "main"`) {
		t.Fatalf("the note names a branch that is no pattern:\n%s", out)
	}
}

// Branch names with no pattern among them get no pattern note.
func TestTheTranscriptHasNoPatternNoteWithoutAPattern(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"git": {"grants": [{"kind": "branch", "repo": "github.com/acme/one", "branch": "main"}]}}}`+"\n")
	p := plan(t, Request{Workspace: ws})
	if out := p.Transcript(); strings.Contains(out, "A pattern converts as written") {
		t.Fatalf("a transcript with no pattern carries the pattern note:\n%s", out)
	}
}

// A grant whose repository is spelled with capitals is one the launch skips, and the conversion
// matches it to the remote without case: the request would grant what the old grant never did,
// and the proof names it.
func TestTheProofNamesACapitalizedGrantTheConversionWouldWiden(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"git": {"grants": [{"kind": "branch", "repo": "GitHub.com/Acme/One", "branch": "main"}]}}}`+"\n")
	g, err := Discover(Request{Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Plan(g)
	var ineq *InequalityError
	if !errors.As(err, &ineq) {
		t.Fatalf("Plan = %v; want the proof to refuse a conversion that widens the workspace's grants", err)
	}
	for _, d := range p.Differences {
		if d.Layer == brokerGrantsLayer && d.Path == "git.workspace_grants" && strings.Contains(d.After, "branch github.com/acme/one main") {
			return
		}
	}
	t.Fatalf("no difference names the widened branch grant: %+v", p.Differences)
}
