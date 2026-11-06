package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// The `runs` grant kind (docs/design/github-write-scopes.md GW-D14) in a legacy JSONC
// workspace config: it names its workflow under `workflow`, is refused without one, and a
// workspace holds it only when the user's own config allows the kind.

// githubGrantResolver is a github broker declaring the kinds packs/github declares.
var githubGrantResolver = fenceResolver{known: map[string]LoopholeInfo{
	"github-broker": {Name: "github-broker", HasHostDaemon: true, Brokered: &loopholedecl.Brokered{
		Source: "github", RemoteHost: "github.com", Grants: []string{"create-pr", "create-issue", "runs"}}},
}}

func TestDispatchWorkflowGrantPassesAndReachesTheGate(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"github": {"grant_kinds": ["runs"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"grants": [`+
		`{"kind": "runs", "repo": "github.com/o/r", "workflow": "nightly-macos.yml"},`+
		`{"kind": "runs", "repo": "*", "workflow": "*"}]}}}`)
	if errs, warns := h.validateWith(t, githubGrantResolver); errs != "" || warns != "" {
		t.Fatalf("runs grants were not accepted:\nerrors:\n%s\nwarnings:\n%s", errs, warns)
	}
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	grants, _ := read.Grants("github", []string{"create-pr", "create-issue", "runs"})
	want := []brokerscope.Grant{
		{Kind: "runs", Repo: "github.com/o/r", Workflow: "nightly-macos.yml"},
		{Kind: "runs", Repo: "*", Workflow: "*"},
	}
	if len(grants) != len(want) || grants[0] != want[0] || grants[1] != want[1] {
		t.Fatalf("the gate's read carries %+v, want %+v", grants, want)
	}
}

// Each malformed runs grant is refused at config load with its next step, and
// reaches no gate.
func TestDispatchWorkflowGrantRefusals(t *testing.T) {
	for _, c := range []struct{ name, grant, want string }{
		{"workflow missing", `{"kind": "runs", "repo": "github.com/o/r"}`,
			`config.brokered.github.grants[0].workflow: a runs grant names its workflow, such as "workflow": "nightly.yml"`},
		{"a path", `{"kind": "runs", "repo": "github.com/o/r", "workflow": ".github/workflows/nightly.yml"}`,
			`config.brokered.github.grants[0].workflow: '.github/workflows/nightly.yml' is not a workflow a runs grant can name`},
		{"a numeric id", `{"kind": "runs", "repo": "github.com/o/r", "workflow": "12345"}`,
			`is not a workflow a runs grant can name`},
		{"a workflow on another kind", `{"kind": "create-pr", "repo": "github.com/o/r", "workflow": "nightly.yml"}`,
			`config.brokered.github.grants[0].workflow: only a runs grant names a workflow`},
		{"a branch on a dispatch grant", `{"kind": "runs", "repo": "github.com/o/r", "workflow": "nightly.yml", "branch": "main"}`,
			`config.brokered.github.grants[0].branch: only a branch grant names a branch`},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newBrokeredHost(t)
			write(t, h.userCfg, `{"brokered": {"github": {"grant_kinds": ["create-pr", "runs"]}}}`)
			write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"grants": [`+c.grant+`]}}}`)
			errs, _ := h.validateWith(t, githubGrantResolver)
			if !strings.Contains(errs, c.want) {
				t.Fatalf("errors do not contain %q:\n%s", c.want, errs)
			}
			read, err := ReadWorkspaceForGate(h.ws)
			if err != nil {
				t.Fatal(err)
			}
			if grants, _ := read.Grants("github", []string{"create-pr", "runs"}); len(grants) != 0 {
				t.Fatalf("a refused grant reached the gate's read: %+v", grants)
			}
		})
	}
}

// A runs grant the user config does not allow is refused, naming the user-config
// key that would allow it.
func TestDispatchWorkflowGrantKindNotAllowedNamesUserKey(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"github": {"grant_kinds": ["create-pr"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"grants": [`+
		`{"kind": "runs", "repo": "github.com/o/r", "workflow": "nightly.yml"}]}}}`)
	errs, _ := h.validateWith(t, githubGrantResolver)
	if !strings.Contains(errs, "brokered.github.grant_kinds") || !strings.Contains(errs, "'runs'") {
		t.Fatalf("no refusal naming brokered.github.grant_kinds and runs:\n%s", errs)
	}
}

// A legacy config written before GW-D78 names the kind `dispatch-workflow`, in the user's
// grant_kinds and the workspace's grant entries; both read as runs, so the grants reach the gate
// unchanged in meaning (docs/design/github-write-scopes.md GW-D78).
func TestFormerDispatchWorkflowKindReadsAsRuns(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"github": {"grant_kinds": ["dispatch-workflow"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"grants": [`+
		`{"kind": "dispatch-workflow", "repo": "github.com/o/r", "workflow": "nightly-macos.yml"}]}}}`)
	if errs, warns := h.validateWith(t, githubGrantResolver); errs != "" || warns != "" {
		t.Fatalf("the former spelling was not accepted:\nerrors:\n%s\nwarnings:\n%s", errs, warns)
	}
	if kinds := BrokeredGrantKinds("github"); len(kinds) != 1 || kinds[0] != brokerscope.KindRuns {
		t.Fatalf("grant_kinds read as %v, want [runs]", kinds)
	}
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	grants, _ := read.Grants("github", []string{"create-pr", "create-issue", "runs"})
	want := brokerscope.Grant{Kind: "runs", Repo: "github.com/o/r", Workflow: "nightly-macos.yml"}
	if len(grants) != 1 || grants[0] != want {
		t.Fatalf("the gate's read carries %+v, want %+v", grants, want)
	}
}
