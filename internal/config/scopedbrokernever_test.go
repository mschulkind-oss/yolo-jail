package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// neverGrantKinds resolves user and project config the way a launch does and
// returns every grant the gate would hold as "<source>:<grant>".
func neverGrantKinds(t *testing.T, user, project string) []string {
	t.Helper()
	input, _ := scopedPolicyFixture(t, user, project)
	plan, err := ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	remote := "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n"
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"), []byte(remote), 0o644); err != nil {
		t.Fatal(err)
	}
	gate, err := ProduceReadableGateInput(ws, plan)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range gate.Sources {
		for _, g := range s.Grants {
			out = append(out, s.Source+":"+g.String())
		}
	}
	return out
}

const neverRepoProject = "[repos.one]\nrepo=\"github.com/acme/app\"\n[repos.one.requests.github]\ncreate_pr=true\n"

// Design 3.2/3.3: a narrower never refuses under a broader always. B2 of the review.
func TestNarrowerNeverRemovesGitHubWriteGrant(t *testing.T) {
	base := "packs=[\"github\"]\n[allow.github]\nalways=[\"read\",\"create_pr\"]\n[repos.one]\nrepo=\"github.com/acme/app\"\n"
	control := neverGrantKinds(t, base, neverRepoProject)
	if !slices.Contains(control, "github:create-pr github.com/acme/app") {
		t.Fatalf("control lost the create_pr grant: %v", control)
	}
	got := neverGrantKinds(t, base+"[repos.one.allow.github]\nnever=[\"create_pr\"]\n", neverRepoProject)
	for _, g := range got {
		if g == "github:create-pr github.com/acme/app" {
			t.Fatalf("never=create_pr still yields %q in %v", g, got)
		}
	}
	if !slices.Contains(got, "github:read github.com/acme/app") {
		t.Fatalf("never on create_pr must not remove read: %v", got)
	}
}

func TestNarrowerNeverRemovesGitHubReadGrant(t *testing.T) {
	user := "packs=[\"github\"]\n[allow.github]\nalways=[\"read\"]\n[repos.one]\nrepo=\"github.com/acme/app\"\n[repos.one.allow.github]\nnever=[\"read\"]\n"
	got := neverGrantKinds(t, user, "[repos.one]\nrepo=\"github.com/acme/app\"\n")
	for _, g := range got {
		if g == "github:read github.com/acme/app" {
			t.Fatalf("never=read still yields %q in %v", g, got)
		}
	}
}

func TestNarrowerNeverRemovesGitFetchGrant(t *testing.T) {
	user := "packs=[\"git\"]\n[allow.git]\nalways=[\"fetch\"]\n[repos.one]\nrepo=\"github.com/acme/app\"\n[repos.one.allow]\nnever=[\"fetch\"]\n"
	got := neverGrantKinds(t, user, "[repos.one]\nrepo=\"github.com/acme/app\"\n")
	for _, g := range got {
		if g == "git:git:read github.com/acme/app" || g == "git:read github.com/acme/app" {
			t.Fatalf("never=fetch still yields %q in %v", g, got)
		}
	}
}

// A narrower never on one repository leaves another repository's grant alone.
func TestNarrowerNeverIsScopedToItsRepository(t *testing.T) {
	user := "packs=[\"github\"]\n[allow.github]\nalways=[\"read\"]\n[repos.one]\nrepo=\"github.com/other/thing\"\n[repos.one.allow.github]\nnever=[\"read\"]\n"
	got := neverGrantKinds(t, user, "[repos.one]\nrepo=\"github.com/other/thing\"\n")
	if !slices.Contains(got, "github:read github.com/acme/app") {
		t.Fatalf("never on another repository removed this one: %v", got)
	}
}

func awsCellPolicy(t *testing.T, user, project string) (CallBrokerTablePolicy, string) {
	t.Helper()
	input, _ := scopedPolicyFixture(t, user, project)
	plan, err := ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range plan.Sources {
		if s.Source == "aws" && s.CallBroker != nil {
			cells, err := EffectiveCallBrokerCells("aws", s.CallBroker, input.User, input.Workspace)
			if err != nil {
				t.Fatal(err)
			}
			return cells["put_objects"].Tables["all"], CallBrokerCurrentCeiling(input.User, "aws", "all", "put_objects")
		}
	}
	t.Fatal("aws call broker not selected")
	return CallBrokerTablePolicy{}, ""
}

const awsRoots = "[call_roots]\naws=[\"s3://b\"]\n[requests.aws.all]\nput_objects=true\n[requests.aws.working_set]\nput_objects=true\n"

// Design 3.2 rule 5.1: a never token naming the cell's own token refuses under a broader always.
func TestSourceNeverRefusesCallBrokerCell(t *testing.T) {
	user := "packs=[\"aws\"]\n[allow.aws]\nalways=[\"read\",\"all\"]\nnever=[\"all.put_objects\"]\n"
	policy, current := awsCellPolicy(t, user, awsRoots)
	if policy.Decision != "refuse" || current != "off" {
		t.Fatalf("source never ignored: policy=%+v current=%q", policy, current)
	}
	control, current := awsCellPolicy(t, "packs=[\"aws\"]\n[allow.aws]\nalways=[\"read\",\"all\"]\n", awsRoots)
	if control.Decision != "run" || current != "always" {
		t.Fatalf("control must still run: %+v %q", control, current)
	}
}

// A scoped never is enforced conservatively for the whole source until the scoped
// decision engine is wired: it may over-refuse, it may not run.
func TestScopedNeverRefusesCallBrokerCellConservatively(t *testing.T) {
	user := "packs=[\"aws\"]\n[allow.aws]\nalways=[\"read\",\"all.put_objects\"]\n[scopes.reports]\nsource=\"aws\"\nresource=\"s3://b/reports/\"\n[scopes.reports.allow]\nnever=[\"read\",\"all\"]\n"
	policy, current := awsCellPolicy(t, user, awsRoots)
	if policy.Decision != "refuse" || current != "off" {
		t.Fatalf("scoped never ignored: policy=%+v current=%q", policy, current)
	}
}

func TestScopedAskFalseRefusesCallBrokerCellConservatively(t *testing.T) {
	user := "packs=[\"aws\"]\n[allow.aws]\nalways=[\"read\",\"all\"]\n"
	project := awsRoots + "[scopes.reports]\nsource=\"aws\"\nresource=\"s3://b/reports/\"\n[scopes.reports.requests.all]\nput_objects=false\n[scopes.reports.requests.working_set]\nput_objects=false\n"
	policy, _ := awsCellPolicy(t, user, project)
	if policy.Decision != "refuse" {
		t.Fatalf("scoped ask false ignored: %+v", policy)
	}
}

// Design 3.7 row 3: the policy-model marker releases held writes only with a gate that lists them.
// A call-broker source has no listing, so an unrelated y must not write the marker (review M2).
func TestCallBrokerLegacyRecordDoesNotGainMarkerUnlisted(t *testing.T) {
	workspace, read, declaration := scopedBrokerGateFixture(t, true)
	check := scopedBrokerGateCheck(workspace, read, declaration)
	if err := RecordApproval(workspace, read.Config, check); err != nil {
		t.Fatal(err)
	}
	rec, _, _, _ := readScopeRecords(workspace)
	delete(rec, "local-tasks.policy-model")
	js, _ := scopeRecordJSON(rec)
	write(t, ApprovalScopePath(workspace), js+"\n")
	write(t, filepath.Join(workspace, "yolo-jail.toml"), "[call_roots]\nlocal-tasks = [\"team:alpha\"]\n[requests.local-tasks.all]\nupdate = true\n[network]\nmode = \"host\"\n")
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	check = scopedBrokerGateCheck(workspace, read, declaration)
	prompt := &scopedBrokerModelPrompter{accept: true}
	if _, err := CheckConfigAndScopeChanges(workspace, read.Config, check, true, false, prompt); err != nil {
		t.Fatal(err)
	}
	if PolicyModelApproved(workspace, "local-tasks") {
		t.Fatalf("marker written without listing the held writes; ScopeBlock=%q", prompt.report.ScopeBlock)
	}
}

// Call-site pin: RecordApproval (--accept-config-changes and a host `yolo check`) publishes the
// policy-model marker for the writes it approves, as the interactive y does. Deleting
// publishPolicyApproval from recordApproval fails this (review M3).
func TestRecordApprovalPublishesThePolicyModelMarker(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, ".git", "config"), "[remote \"origin\"]\n url = git@github.com:o/r.git\n")
	if err := os.Remove(h.userCfg); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	write(t, filepath.Join(filepath.Dir(h.userCfg), "config.toml"), "[allow.git]\non_request = [\"push\"]\n")
	write(t, filepath.Join(h.ws, "yolo-jail.toml"), "[requests.git]\npush = [\"main\"]\n")
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordApproval(h.ws, read.Config, nil); err != nil {
		t.Fatal(err)
	}
	legacy, err := scopeRecordJSON(scopeRecord{"git": {"git@github.com:o/r.git"}})
	if err != nil {
		t.Fatal(err)
	}
	legacy += "\n"
	write(t, ApprovalScopePath(h.ws), legacy)
	metadata := loopholedecl.Brokered{Source: "git", RemoteHost: "*", Grants: []string{"branch"}, ReadCapability: "fetch", KindCapabilities: map[string]string{"branch": "push"}}
	declaration := readableBrokerDeclaration("git-pack", "git-broker", metadata, nil)
	broker := ScopeBroker{Source: "git", Label: "git-broker", Pack: "git-pack", RemoteHost: "*", GrantKinds: metadata.Grants, Brokered: metadata, Readable: &declaration}
	check := NewScopeCheck(h.ws, []ScopeBroker{broker}, read)
	if PolicyModelApproved(h.ws, "git") {
		t.Fatal("setup: marker already present")
	}
	if err := RecordApproval(h.ws, read.Config, check); err != nil {
		t.Fatal(err)
	}
	if !PolicyModelApproved(h.ws, "git") || len(ApprovedGrants(h.ws, "git")) != 1 {
		record, _, _, _ := readScopeRecords(h.ws)
		t.Fatalf("approval recorded without the policy-model marker: %+v", record)
	}
}

// The projection pairs with the check by identity: a source at the same position under another
// name, or past the end, is no pair (review minor: the guard in readablescope.go was unpinned).
func TestAProjectionPairsWithTheCheckByIdentity(t *testing.T) {
	projection := []ScopeSource{{Source: "git"}, {Source: "github"}}
	if got, ok := pairedProjection(projection, 1, "github"); !ok || got.Source != "github" {
		t.Fatalf("matching identity not paired: %+v %v", got, ok)
	}
	if _, ok := pairedProjection(projection, 0, "github"); ok {
		t.Fatal("a source of another name was paired by position")
	}
	if _, ok := pairedProjection(projection, 2, "github"); ok {
		t.Fatal("a position past the projection was paired")
	}
}

// B2's skip reason names the real verb and path: the table the refusing line is in, not a placeholder.
func TestANarrowerNeverSkipReasonNamesTheRealLine(t *testing.T) {
	user := "packs=[\"github\"]\n[allow.github]\nalways=[\"read\",\"create_pr\"]\n[repos.one]\nrepo=\"github.com/acme/app\"\n[repos.one.allow.github]\nnever=[\"create_pr\"]\n"
	input, _ := scopedPolicyFixture(t, user, neverRepoProject)
	plan, err := ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"), []byte("[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate, err := ProduceReadableGateInput(ws, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range gate.Requests {
		if r.Capability == "create_pr" {
			if r.Status != ReadableRequestSkipped || !strings.Contains(r.Reason, "`yolo config unset repos.one.allow.github.never`") || strings.Contains(r.Reason, "<name>") {
				t.Fatalf("status %v reason %q", r.Status, r.Reason)
			}
			return
		}
	}
	t.Fatal("no create_pr request in the gate")
}
