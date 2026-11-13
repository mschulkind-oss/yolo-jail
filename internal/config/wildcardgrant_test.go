package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// Wildcard branch grants and tag grants (docs/reference/git-broker.md GB-D25), at config load and
// at the config-change gate.

// A branch grant may name a pattern and "*" for its repository; a tag grant names its tag the
// same way. Each is accepted with its kind allowed, and the gate's read carries it.
func TestWildcardBranchAndTagGrantsPass(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"git": {"grant_kinds": ["branch", "tag"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"grants": [`+
		`{"kind": "branch", "repo": "*", "branch": "main"},`+
		`{"kind": "branch", "repo": "github.com/o/r", "branch": "patch/*"},`+
		`{"kind": "branch", "repo": "github.com/o/r", "branch": "patch*"},`+
		`{"kind": "tag", "repo": "*", "tag": "v*"},`+
		`{"kind": "tag", "repo": "github.com/o/r", "tag": "v1.0"}]}}}`)
	if errs, warns := h.validateWith(t, gitGrantResolver); errs != "" || warns != "" {
		t.Fatalf("wildcard and tag grants were not accepted:\nerrors:\n%s\nwarnings:\n%s", errs, warns)
	}
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	grants, _ := read.Grants("git", []string{"create-branch", "branch", "tag"})
	want := []brokerscope.Grant{
		{Kind: "branch", Repo: "*", Branch: "main"},
		{Kind: "branch", Repo: "github.com/o/r", Branch: "patch/*"},
		{Kind: "branch", Repo: "github.com/o/r", Branch: "patch*"},
		{Kind: "tag", Repo: "*", Tag: "v*"},
		{Kind: "tag", Repo: "github.com/o/r", Tag: "v1.0"},
	}
	if len(grants) != len(want) {
		t.Fatalf("the gate's read carries %+v, want %+v", grants, want)
	}
	for i := range want {
		if grants[i] != want[i] {
			t.Fatalf("the gate's read carries %+v, want %+v", grants, want)
		}
	}
}

// A tag grant the user config does not allow is refused, naming the user-config key.
func TestTagGrantKindNotAllowedNamesUserKey(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"git": {"grant_kinds": ["branch"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"grants": [{"kind": "tag", "repo": "github.com/o/r", "tag": "v*"}]}}}`)
	errs, _ := h.validateWith(t, gitGrantResolver)
	if !strings.Contains(errs, "brokered.git.grant_kinds") || !strings.Contains(errs, "'tag'") {
		t.Fatalf("no refusal naming brokered.git.grant_kinds and tag:\n%s", errs)
	}
}

// Each malformed ref grant is refused at config load with its next step, and reaches no gate.
func TestRefGrantPatternRefusals(t *testing.T) {
	for _, c := range []struct{ name, grant, want string }{
		{"two stars", `{"kind": "branch", "repo": "github.com/o/r", "branch": "*/*"}`,
			"config.brokered.git.grants[0].branch: '*/*' has more than one `*`"},
		{"two stars in a tag", `{"kind": "tag", "repo": "*", "tag": "v*.*"}`,
			"config.brokered.git.grants[0].tag: 'v*.*' has more than one `*`"},
		{"tag missing", `{"kind": "tag", "repo": "github.com/o/r"}`,
			`config.brokered.git.grants[0].tag: a tag grant names its tag`},
		{"full tag ref", `{"kind": "tag", "repo": "github.com/o/r", "tag": "refs/tags/v1"}`,
			`config.brokered.git.grants[0].tag: 'refs/tags/v1' is not a tag name`},
		{"tag grant with a branch", `{"kind": "tag", "repo": "github.com/o/r", "tag": "v1", "branch": "main"}`,
			`config.brokered.git.grants[0].branch: only a branch grant names a branch`},
		{"branch grant with a tag", `{"kind": "branch", "repo": "github.com/o/r", "branch": "main", "tag": "v1"}`,
			`config.brokered.git.grants[0].tag: only a tag grant names a tag`},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newBrokeredHost(t)
			write(t, h.userCfg, `{"brokered": {"git": {"grant_kinds": ["branch", "tag"]}}}`)
			write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"grants": [`+c.grant+`]}}}`)
			errs, _ := h.validateWith(t, gitGrantResolver)
			if !strings.Contains(errs, c.want) {
				t.Fatalf("errors do not contain %q:\n%s", c.want, errs)
			}
			read, err := ReadWorkspaceForGate(h.ws)
			if err != nil {
				t.Fatal(err)
			}
			if grants, _ := read.Grants("git", []string{"branch", "tag"}); len(grants) != 0 {
				t.Fatalf("a refused grant reached the gate's read: %+v", grants)
			}
		})
	}
}

func gitWildcardScope(remotes []brokerscope.Remote, grants ...brokerscope.Grant) *ScopeCheck {
	return &ScopeCheck{Sources: []ScopeSource{{Source: "git", Label: "git-broker", RemoteHost: "*",
		Form: brokerscope.FormRemoteURL, Grants: grants,
		Read: brokerscope.Read{GitConfig: "/w/.git/config", Remotes: remotes}}}}
}

// A wildcard grant's line at the gate says in plain words what it admits; a grant naming one
// branch on one repository reads as it is.
func TestWildcardGrantLineSaysWhatItAdmits(t *testing.T) {
	ws := approvalWorkspace(t)
	remotes := []brokerscope.Remote{{Name: "origin", Repo: "git@github.com:o/r.git"}}
	if err := RecordApproval(ws, decode(t, `{}`), gitWildcardScope(remotes)); err != nil {
		t.Fatal(err)
	}
	r, err := ScopeChanges(ws, gitWildcardScope(remotes,
		brokerscope.Grant{Kind: "branch", Repo: "*", Branch: "main"},
		brokerscope.Grant{Kind: "branch", Repo: "github.com/o/r", Branch: "patch/*"},
		brokerscope.Grant{Kind: "tag", Repo: "*", Tag: "v*"},
		brokerscope.Grant{Kind: "branch", Repo: "github.com/o/r", Branch: "dev"}))
	if err != nil || !r.ScopeChanged {
		t.Fatalf("no change (%v): %+v", err, r)
	}
	block := strings.Join(r.ScopeBlock, "\n")
	for grant, want := range map[string]string{
		"branch * main":                 "added (may create, update, force-push and delete `main` on every remote in scope now and any approved later)",
		"branch github.com/o/r patch/*": "added (may create, update, force-push and delete branches matching `patch/*` on github.com/o/r)",
		"tag * v*":                      "added (may create, update, force-push and delete tags matching `v*` on every remote in scope now and any approved later)",
		"branch github.com/o/r dev":     "added",
	} {
		found := false
		for _, l := range r.ScopeBlock {
			if strings.HasPrefix(l, "  + grant "+grant+" ") {
				found = true
				if !strings.HasSuffix(l, " "+want) {
					t.Errorf("the line %q does not end %q", l, want)
				}
			}
		}
		if !found {
			t.Errorf("no added line for %q:\n%s", grant, block)
		}
	}
}

// Approving a new remote later names every standing wildcard grant that now covers it, on the
// remote's own added line, so the widening is seen where the remote is approved.
func TestNewRemoteNamesTheWildcardGrantsCoveringIt(t *testing.T) {
	ws := approvalWorkspace(t)
	grants := []brokerscope.Grant{
		{Kind: "branch", Repo: "*", Branch: "main"},
		{Kind: "tag", Repo: "*", Tag: "v*"},
		{Kind: "branch", Repo: "github.com/o/r", Branch: "patch/*"},
	}
	origin := brokerscope.Remote{Name: "origin", Repo: "git@github.com:o/r.git"}
	if err := RecordApproval(ws, decode(t, `{}`), gitWildcardScope([]brokerscope.Remote{origin}, grants...)); err != nil {
		t.Fatal(err)
	}
	upstream := brokerscope.Remote{Name: "upstream", Repo: "git@github.com:org/r.git"}
	r, err := ScopeChanges(ws, gitWildcardScope([]brokerscope.Remote{origin, upstream}, grants...))
	if err != nil || !r.ScopeChanged {
		t.Fatalf("no change (%v): %+v", err, r)
	}
	var line string
	for _, l := range r.ScopeBlock {
		if strings.Contains(l, "git@github.com:org/r.git") {
			line = l
		}
	}
	want := "added (now covered by the standing grants branch * main, tag * v*)"
	if !strings.HasPrefix(line, "  + ") || !strings.HasSuffix(line, want) {
		t.Fatalf("the new remote's line %q does not end %q:\n%s", line, want, strings.Join(r.ScopeBlock, "\n"))
	}
	for _, l := range r.ScopeBlock {
		if strings.Contains(l, "git@github.com:o/r.git") && strings.Contains(l, "covered by") {
			t.Fatalf("an unchanged remote names the grants: %q", l)
		}
	}
}
