package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"
)

// A JSONC project file is rewritten whole, so its unqualified `requests.github` lines change
// reach: before GW-D79 each reached every remote on the host, after it origin alone. The converted
// file keeps the second remote's writes as a table named for the remote (GW-D80), with no `repos`
// list needed to ask for it; a grant that named origin never reached the second remote and is not
// copied there.
func TestAConvertedJSONCFileKeepsItsUnqualifiedLinesOnTheOtherRemote(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git", "https://github.com/elsewhere/app.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{
  "brokered": {"github": {"grants": [
    {"kind": "create-pr", "repo": "github.com/acme/one"}
  ]}},
  "requests": {"github": {"runs": ["ci.yml"]}}
}
`)
	p := plan(t, Request{Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	text := targetText(p)
	for _, want := range []string{"[requests.github]", "[repos.rb.requests.github]", "create_pr = true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the converted file lacks %q:\n%s", want, text)
		}
	}
	if got := strings.Count(text, `runs = ["ci.yml"]`); got != 2 {
		t.Fatalf("want the line under [requests.github] and under the remote's table, got %d:\n%s", got, text)
	}
	if strings.Count(text, "create_pr = true") != 1 || strings.Contains(text, "repo = ") {
		t.Fatalf("the remote's table took origin's grant or a repo line:\n%s", text)
	}
	for _, want := range []string{`"runs github.com/acme/one ci.yml"`, `"runs github.com/elsewhere/app ci.yml"`, `"create-pr github.com/acme/one"`} {
		if !strings.Contains(p.canonical.grants, want) {
			t.Fatalf("the proof's grants lack %s: %s", want, p.canonical.grants)
		}
	}
	if strings.Contains(p.canonical.grants, "create-pr github.com/elsewhere/app") {
		t.Fatalf("the conversion widened origin's grant: %s", p.canonical.grants)
	}
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
