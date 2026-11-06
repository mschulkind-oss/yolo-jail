package brokerscope

import (
	"os"
	"strings"
	"testing"
)

// A runs grant names one workflow file or `*` in its own field, and spells back through String
// and ParseGrant (docs/design/github-write-scopes.md GW-D14, GW-D78).
func TestRunsGrantShape(t *testing.T) {
	for _, c := range []struct {
		g  Grant
		ok bool
	}{
		{Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: "nightly-macos.yml"}, true},
		{Grant{Kind: KindRuns, Repo: "*", Workflow: "*"}, true},
		{Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: "ci.yaml"}, true},
		{Grant{Kind: KindRuns, Repo: "github.com/o/r"}, false},
		{Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: "12345"}, false},
		{Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: ".github/workflows/ci.yml"}, false},
		{Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: "nightly*.yml"}, false},
		{Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: "ci.yml", Branch: "main"}, false},
		{Grant{Kind: KindBranch, Repo: "github.com/o/r", Branch: "main", Workflow: "ci.yml"}, false},
		{Grant{Kind: "create-pr", Repo: "github.com/o/r", Workflow: "ci.yml"}, false},
	} {
		if got := ValidGrant(c.g); got != c.ok {
			t.Errorf("ValidGrant(%+v) = %v, want %v", c.g, got, c.ok)
			continue
		}
		if !c.ok {
			continue
		}
		back, ok := ParseGrant(c.g.String())
		if !ok || back != c.g {
			t.Errorf("ParseGrant(%q) = %+v %v, want %+v", c.g.String(), back, ok, c.g)
		}
	}
	if !GrantTakesName(KindRuns) || GrantTakesRef(KindRuns) || GrantNameKey(KindRuns) != "workflow" {
		t.Fatal("runs names a workflow, not a ref")
	}
}

// A grant written under the kind's former name, `dispatch-workflow`, by an older yolo's scope
// file or approval record reads as a runs grant (GW-D78).
func TestFormerRunsKindReadsAsRuns(t *testing.T) {
	g, ok := ParseGrant("dispatch-workflow github.com/o/r nightly.yml")
	if !ok || g != (Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: "nightly.yml"}) {
		t.Fatalf("ParseGrant of the former spelling = %+v %v", g, ok)
	}
	if CanonicalGrantKind("dispatch-workflow") != KindRuns || CanonicalGrantKind("approve-run") != "approve-run" {
		t.Fatal("CanonicalGrantKind")
	}
}

// A scope file an older yolo wrote with a `dispatch-workflow` grant reads back as a runs grant.
func TestScopeFileReadsTheFormerRunsKind(t *testing.T) {
	t.Setenv("HOME", resolvedTempDir(t))
	path, err := Write(File{Source: "github", LaunchID: "former", PID: os.Getpid(),
		Grants: []Grant{{Kind: KindRuns, Repo: "github.com/o/r", Workflow: "nightly.yml"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(data), `"kind": "runs"`, `"kind": "dispatch-workflow"`, 1)
	if old == string(data) {
		t.Fatalf("the written file names no runs grant to respell: %s", data)
	}
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(path)
	if err != nil || len(got.Grants) != 1 || got.Grants[0] != (Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: "nightly.yml"}) {
		t.Fatalf("read back %+v, %v", got.Grants, err)
	}
}
