package config

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// brokeredskipped_test.go pins the report of a project's broker request that the user's ceiling
// leaves out (docs/design/config-user.md §6, docs/design/github-write-scopes.md §5): the
// `requests.<source>.<capability>` a selected broker pack declares, with no `allow.<source>`
// list naming the capability. Before the fix `yolo check`, every launch and `yolo config apply`
// said nothing about it, and the jail ran without the grant: the owner of this repository
// approved `requests.github.runs` at the gate and a later dispatch still asked.

// skippedBrokerFixture is a user config selecting the git and github packs with the ceiling
// given, and a project whose yolo-jail.toml holds the requests given. HOME is the fixture's.
func skippedBrokerFixture(t *testing.T, allow, requests string) string {
	t.Helper()
	user := testconfig.User(t, "packs = [\"git\", \"github\"]\n\n"+allow)
	ws := testconfig.Workspace(t, requests)
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(UserLayerEnv, "")
	return ws.Root
}

const skippedGitHubRequests = "[requests.github]\n" +
	"runs = [\"release-request.yml\", \"nightly-macos.yml\"]\n" +
	"approve_runs = [\"github-actions[bot]\"]\n"

func TestABrokerRequestOutsideTheCeilingWarnsNamingTheAllowCommand(t *testing.T) {
	ws := skippedBrokerFixture(t, "[allow.github]\nalways = [\"read\"]\n", skippedGitHubRequests)
	merged, err := LoadConfig(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	errs, warns := ValidateConfig(merged, ws, nil)
	if len(errs) != 0 {
		t.Fatalf("a skipped request is not an error: %q", errs)
	}
	for _, want := range []string{
		paths.WorkspaceConfigTOMLName + ":2: this project requests github runs release-request.yml, nightly-macos.yml.\n" +
			"  Your config does not allow runs on request, so this project runs without it.\n" +
			"  To allow it for every project you work on (in your config, not this file), run on the host:\n" +
			"    yolo allow github on_request runs\n" +
			"  then approve it for this workspace, if that command did not already ask:\n" +
			"    yolo config apply",
		paths.WorkspaceConfigTOMLName + ":3: this project requests github approve_runs github-actions[bot].\n" +
			"  Your config does not allow approve_runs on request, so this project runs without it.\n" +
			"  To allow it for every project you work on (in your config, not this file), run on the host:\n" +
			"    yolo allow github on_request approve_runs\n" +
			"  then approve it for this workspace, if that command did not already ask:\n" +
			"    yolo config apply",
	} {
		if !slices.Contains(warns, want) {
			t.Fatalf("warnings = %q\nwant one to be exactly:\n%s", warns, want)
		}
	}
}

func TestABrokerRequestWithinTheCeilingIsNotReportedSkipped(t *testing.T) {
	ws := skippedBrokerFixture(t, "[allow.github]\nalways = [\"read\"]\non_request = [\"runs\", \"approve_runs\"]\n\n"+
		"[allow.git]\nalways = [\"fetch\"]\n", skippedGitHubRequests+"\n[requests.git]\npush = [\"main\"]\n")
	merged, err := LoadConfig(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	_, warns := ValidateConfig(merged, ws, nil)
	var skipped []string
	for _, w := range warns {
		if strings.Contains(w, "runs without it") {
			skipped = append(skipped, w)
		}
	}
	// git push is outside this ceiling; the two github requests are within it.
	if len(skipped) != 1 || !strings.Contains(skipped[0], "this project requests git push main.") ||
		!strings.Contains(skipped[0], "\n    yolo allow git on_request push\n") {
		t.Fatalf("skipped reports = %q, want exactly the git push one", skipped)
	}
}

// In a jail the user config carries no `allow` (inherit.go never inherits it), so a check there
// cannot judge the ceiling and says nothing rather than calling every request skipped.
func TestABrokerRequestIsNotJudgedInAJail(t *testing.T) {
	ws := skippedBrokerFixture(t, "", skippedGitHubRequests)
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	merged, err := LoadConfig(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	_, warns := ValidateConfig(merged, ws, nil)
	for _, w := range warns {
		if strings.Contains(w, "runs without it") {
			t.Fatalf("a jail judged a ceiling it cannot see: %q", w)
		}
	}
}

// skippedGateScope is the gate's scope for the fixture workspace, with a GitHub remote, through
// the same collector a launch's gate reads (NewScopeCheck) and the github broker's declaration.
func skippedGateScope(t *testing.T, ws string) (*WorkspaceRead, *ScopeCheck) {
	t.Helper()
	writeReadableScopeRemote(t, ws, "[remote \"origin\"]\nurl = https://github.com/acme/repo.git\n")
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read",
		[]string{"create-pr", "runs", "approve-run"},
		map[string]string{"create-pr": "create_pr", "runs": "runs", "approve-run": "approve_runs"})
	return read, NewScopeCheck(ws, []ScopeBroker{broker}, read)
}

// The approval gate a launch and `yolo config apply` ask shows each skipped request with the
// allow command alone on its line, then the approval step: the owner answered y to a diff of
// these request lines and was never told they did nothing.
func TestTheGateShowsABrokerRequestOutsideTheCeilingWithItsAllowCommand(t *testing.T) {
	ws := skippedBrokerFixture(t, "[allow.github]\nalways = [\"read\"]\n", skippedGitHubRequests)
	read, scope := skippedGateScope(t, ws)
	p := &reportPrompter{accept: true}
	if _, err := CheckConfigAndScopeChanges(ws, read.Config, scope, true, false, p); err != nil {
		t.Fatal(err)
	}
	if p.got == nil {
		t.Fatal("the gate did not ask about a changed workspace config")
	}
	block := p.got.ScopeBlock
	for _, want := range []string{
		brokeredSkippedHeading,
		"      yolo allow github on_request runs",
		"      yolo allow github on_request approve_runs",
		"      yolo config apply",
	} {
		if !slices.Contains(block, want) {
			t.Fatalf("the gate's block lacks the line %q:\n%s", want, strings.Join(block, "\n"))
		}
	}
	if !strings.Contains(strings.Join(block, "\n"), "– github runs release-request.yml, nightly-macos.yml (not allowed on request by your config") {
		t.Fatalf("the gate's block does not name the request:\n%s", strings.Join(block, "\n"))
	}

	// With no terminal the refusal shows the same lines.
	write(t, ws+"/"+paths.WorkspaceConfigTOMLName, skippedGitHubRequests+"create_pr = true\n")
	read, scope = skippedGateScope(t, ws)
	_, err := CheckConfigAndScopeChanges(ws, read.Config, scope, false, false, nil)
	var changed *ChangedNonInteractiveError
	if !errors.As(err, &changed) {
		t.Fatalf("err = %v, want the non-interactive refusal", err)
	}
	if !slices.Contains(changed.ScopeBlock, "      yolo allow github on_request runs") {
		t.Fatalf("the refusal's block lacks the allow line:\n%s", strings.Join(changed.ScopeBlock, "\n"))
	}
}

// Within the ceiling the gate shows no skipped block.
func TestTheGateShowsNoSkippedBlockWithinTheCeiling(t *testing.T) {
	ws := skippedBrokerFixture(t, "[allow.github]\nalways = [\"read\"]\non_request = [\"runs\", \"approve_runs\"]\n", skippedGitHubRequests)
	read, scope := skippedGateScope(t, ws)
	p := &reportPrompter{accept: true}
	if _, err := CheckConfigAndScopeChanges(ws, read.Config, scope, true, false, p); err != nil {
		t.Fatal(err)
	}
	if p.got != nil && slices.Contains(p.got.ScopeBlock, brokeredSkippedHeading) {
		t.Fatalf("a request within the ceiling was shown skipped:\n%s", strings.Join(p.got.ScopeBlock, "\n"))
	}
}
