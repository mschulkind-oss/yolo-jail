package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// The `approve-run` grant kind in a legacy JSONC workspace config: it names the run's
// triggering actor under `actor`, is refused without one or with a name that is not a GitHub
// login, and reaches the gate's read as written.

var githubApproveResolver = fenceResolver{known: map[string]LoopholeInfo{
	"github-broker": {Name: "github-broker", HasHostDaemon: true, Brokered: &loopholedecl.Brokered{
		Source: "github", RemoteHost: "github.com", Grants: []string{"create-pr", "create-issue", "dispatch-workflow", "approve-run"}}},
}}

func TestApproveRunGrantPassesAndReachesTheGate(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"github": {"grant_kinds": ["approve-run"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"grants": [`+
		`{"kind": "approve-run", "repo": "github.com/o/r", "actor": "github-actions[bot]"},`+
		`{"kind": "approve-run", "repo": "*", "actor": "*"}]}}}`)
	if errs, warns := h.validateWith(t, githubApproveResolver); errs != "" || warns != "" {
		t.Fatalf("approve-run grants were not accepted:\nerrors:\n%s\nwarnings:\n%s", errs, warns)
	}
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	grants, _ := read.Grants("github", []string{"approve-run"})
	want := []brokerscope.Grant{
		{Kind: "approve-run", Repo: "github.com/o/r", Actor: "github-actions[bot]"},
		{Kind: "approve-run", Repo: "*", Actor: "*"},
	}
	if len(grants) != len(want) || grants[0] != want[0] || grants[1] != want[1] {
		t.Fatalf("the gate's read carries %+v, want %+v", grants, want)
	}
}

// Each malformed approve-run grant is refused at config load with its next step, and reaches no
// gate.
func TestApproveRunGrantRefusals(t *testing.T) {
	for _, c := range []struct{ name, grant, want string }{
		{"actor missing", `{"kind": "approve-run", "repo": "github.com/o/r"}`,
			`config.brokered.github.grants[0].actor: a approve-run grant names its actor, such as "actor": "github-actions[bot]"`},
		{"not a login", `{"kind": "approve-run", "repo": "github.com/o/r", "actor": "some one"}`,
			`config.brokered.github.grants[0].actor: 'some one' is not an actor an approve-run grant can name`},
		{"a pattern", `{"kind": "approve-run", "repo": "github.com/o/r", "actor": "dependabot*"}`,
			`is not an actor an approve-run grant can name`},
		{"an actor on another kind", `{"kind": "create-pr", "repo": "github.com/o/r", "actor": "octocat"}`,
			`config.brokered.github.grants[0].actor: only a approve-run grant names a actor`},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newBrokeredHost(t)
			write(t, h.userCfg, `{"brokered": {"github": {"grant_kinds": ["create-pr", "approve-run"]}}}`)
			write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"grants": [`+c.grant+`]}}}`)
			errs, _ := h.validateWith(t, githubApproveResolver)
			if !strings.Contains(errs, c.want) {
				t.Fatalf("errors do not contain %q:\n%s", c.want, errs)
			}
			read, err := ReadWorkspaceForGate(h.ws)
			if err != nil {
				t.Fatal(err)
			}
			if grants, _ := read.Grants("github", []string{"create-pr", "approve-run"}); len(grants) != 0 {
				t.Fatalf("a refused grant reached the gate's read: %+v", grants)
			}
		})
	}
}
