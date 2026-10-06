package config

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// The approval record's scope part (docs/design/boundary-broker.md BB-D30, BB-D31), tested
// on the gate itself. The launch's call site is pinned in internal/cli/run.

func githubScope(remotes ...brokerscope.Remote) *ScopeCheck {
	return &ScopeCheck{Sources: []ScopeSource{{Source: "github", Label: "github-broker",
		Read: brokerscope.Read{GitConfig: "/w/.git/config", Remotes: remotes}}}}
}

var origin = brokerscope.Remote{Name: "origin", Repo: "o/r"}

// reportPrompter records the report it was shown and answers with accept.
type reportPrompter struct {
	accept bool
	got    *ChangeReport
}

func (p *reportPrompter) Prompt([]string) bool {
	panic("a ReportPrompter must be asked through PromptReport")
}
func (p *reportPrompter) PromptReport(r ChangeReport) bool {
	p.got = &r
	return p.accept
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A first launch with a `{}` config and a GitHub remote asks once, with the scope block and
// no config diff, and a N leaves no record of either part (§12 done criterion 15).
func TestScopeFirstLaunchAsksOnceAndNLeavesNoRecord(t *testing.T) {
	ws := approvalWorkspace(t)
	p := &reportPrompter{accept: false}
	ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), githubScope(origin), true, false, p)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if p.got == nil || !p.got.ScopeChanged || p.got.ConfigChanged || len(p.got.DiffLines) != 0 {
		t.Fatalf("report %+v", p.got)
	}
	block := strings.Join(p.got.ScopeBlock, "\n")
	if !strings.Contains(block, "github-broker repository scope, read from /w/.git/config:") ||
		!strings.Contains(block, `+ o/r  remote "origin"  added`) {
		t.Fatalf("block:\n%s", block)
	}
	if exists(ApprovalSnapshotPath(ws)) || exists(ApprovalScopePath(ws)) {
		t.Fatal("a N wrote part of the approval record")
	}

	// y records both, and the next launch asks nothing.
	p = &reportPrompter{accept: true}
	if ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), githubScope(origin), true, false, p); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !exists(ApprovalSnapshotPath(ws)) || !exists(ApprovalScopePath(ws)) {
		t.Fatal("a y did not record both parts")
	}
	if got := ApprovedScope(ws, "github"); len(got) != 1 || got[0] != "o/r" {
		t.Fatalf("approved scope %v", got)
	}
	ok, err = CheckConfigAndScopeChanges(ws, decode(t, `{}`), githubScope(origin), false, false, &failPrompter{t: t})
	if err != nil || !ok {
		t.Fatalf("an approved scope re-asked: ok=%v err=%v", ok, err)
	}
}

// An empty scope with no record is nothing to approve: recorded silently, as `{}` is.
func TestAnEmptyScopeRecordsSilently(t *testing.T) {
	ws := approvalWorkspace(t)
	ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), githubScope(), false, false, &failPrompter{t: t})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !exists(ApprovalScopePath(ws)) || !exists(ApprovalSnapshotPath(ws)) {
		t.Fatal("the silent branch did not record both parts")
	}
}

// With no terminal the refusal names the scope alone when only the scope changed, prints
// the block, and names the git config and the scope part (BB-D31).
func TestScopeChangeWithNoTerminalRefusesNamingTheScope(t *testing.T) {
	ws := approvalWorkspace(t)
	approve(t, ws, decode(t, `{"packages": ["strace"]}`))
	_, err := CheckConfigAndScopeChanges(ws, decode(t, `{"packages": ["strace"]}`),
		githubScope(origin, brokerscope.Remote{Name: "upstream", Repo: "x/y"}), false, false, nil)
	e, ok := err.(*ChangedNonInteractiveError)
	if !ok {
		t.Fatalf("err %v", err)
	}
	if !e.ScopeChanged || e.ConfigChanged || len(e.DiffLines) != 0 {
		t.Fatalf("refusal %+v", e)
	}
	// It names the scope, and not the remotes alone: the workspace's entry is its other input.
	if !strings.Contains(e.Headline(), "The repository scope changed since the last approved launch") ||
		strings.Contains(e.Headline(), "remotes") || strings.Contains(e.Headline(), "Workspace config changed") {
		t.Fatalf("headline %q", e.Headline())
	}
	if !strings.Contains(e.Error(), "github-broker: 2 added, 0 removed, 0 source changed") {
		t.Fatalf("the refusal does not print the count line:\n%s", e.Error())
	}
	for _, want := range []string{"/w/.git/config", ApprovalScopePath(ws), "--accept-config-changes",
		"the new config and repository scope"} {
		if !strings.Contains(e.Advice(), want) {
			t.Errorf("advice does not name %q:\n%s", want, e.Advice())
		}
	}
	if !strings.Contains(e.Error(), `+ x/y  remote "upstream"  added`) {
		t.Fatalf("the refusal does not print the block:\n%s", e.Error())
	}
	if exists(ApprovalScopePath(ws)) {
		t.Fatal("a refusal recorded the scope part")
	}
}

func TestBothChangedNamesBoth(t *testing.T) {
	ws := approvalWorkspace(t)
	approve(t, ws, decode(t, `{}`))
	_, err := CheckConfigAndScopeChanges(ws, decode(t, `{"packages": ["jq"]}`), githubScope(origin), false, false, nil)
	e := err.(*ChangedNonInteractiveError)
	if !e.ConfigChanged || !e.ScopeChanged || !strings.Contains(e.Headline(), "config and repository scope") {
		t.Fatalf("refusal %+v: %s", e, e.Headline())
	}
}

// A removal is a change too, and an unreadable config reads as an empty scope that says why
// (§3.5); against an approved scope that asks, with every repository removed.
func TestARemovedRepositoryAndAnUnreadableConfigAreChanges(t *testing.T) {
	ws := approvalWorkspace(t)
	accept := func(s *ScopeCheck) {
		t.Helper()
		if ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), s, false, true, nil); err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	}
	accept(githubScope(origin, brokerscope.Remote{Name: "old", Repo: "o/old"}))
	p := &reportPrompter{accept: false}
	CheckConfigAndScopeChanges(ws, decode(t, `{}`), githubScope(origin), true, false, p)
	if block := strings.Join(p.got.ScopeBlock, "\n"); !strings.Contains(block, "- o/old") ||
		!strings.Contains(block, "removed") || !strings.Contains(block, "  o/r") {
		t.Fatalf("block:\n%s", block)
	}
	broken := &ScopeCheck{Sources: []ScopeSource{{Source: "github", Label: "github-broker",
		Read: brokerscope.Read{Problem: "/w/.git is a worktree pointer of the wrong shape"}}}}
	p = &reportPrompter{accept: false}
	CheckConfigAndScopeChanges(ws, decode(t, `{}`), broken, true, false, p)
	block := strings.Join(p.got.ScopeBlock, "\n")
	if !strings.Contains(block, "- o/old") || !strings.Contains(block, "- o/r") ||
		!strings.Contains(block, "wrong shape") {
		t.Fatalf("block:\n%s", block)
	}
}

// GitHub names are case-insensitive, so a remote respelled in another case is no change.
func TestACaseOnlyChangeIsNoChange(t *testing.T) {
	ws := approvalWorkspace(t)
	CheckConfigAndScopeChanges(ws, decode(t, `{}`), githubScope(origin), false, true, nil)
	ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`),
		githubScope(brokerscope.Remote{Name: "origin", Repo: "O/R"}), false, false, &failPrompter{t: t})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

// With no scope in play the scope part is neither read nor written, whatever it holds.
func TestNoScopeInPlayLeavesTheScopePartAlone(t *testing.T) {
	ws := approvalWorkspace(t)
	CheckConfigAndScopeChanges(ws, decode(t, `{}`), githubScope(origin), false, true, nil)
	before, _ := os.ReadFile(ApprovalScopePath(ws))
	ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), nil, false, false, &failPrompter{t: t})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	after, _ := os.ReadFile(ApprovalScopePath(ws))
	if string(before) != string(after) {
		t.Fatal("a gate with no scope in play touched the scope part")
	}
	// And a workspace that never starts a broker never gains the file.
	ws2 := approvalWorkspace(t)
	CheckConfigAndScopeChanges(ws2, decode(t, `{"packages": ["jq"]}`), nil, false, true, nil)
	if exists(ApprovalScopePath(ws2)) {
		t.Fatal("a gate with no scope in play created a scope part")
	}
}

// A prompter that predates ReportPrompter is still shown the block, above the diff.
func TestAPlainPrompterSeesTheBlockFirst(t *testing.T) {
	ws := approvalWorkspace(t)
	approve(t, ws, decode(t, `{}`))
	p := &recordingPrompter{accept: true}
	CheckConfigAndScopeChanges(ws, decode(t, `{"packages": ["jq"]}`), githubScope(origin), true, false, p)
	if len(p.diff) == 0 || !strings.HasPrefix(p.diff[0], "github-broker repository scope") {
		t.Fatalf("shown %q", p.diff)
	}
}

// RecordApproval is `yolo check --accept-config-changes`'s writer: both parts, together.
func TestRecordApprovalWritesBothParts(t *testing.T) {
	ws := approvalWorkspace(t)
	if err := RecordApproval(ws, decode(t, `{"packages": ["jq"]}`), githubScope(origin)); err != nil {
		t.Fatal(err)
	}
	if got := ApprovedScope(ws, "github"); len(got) != 1 || got[0] != "o/r" {
		t.Fatalf("scope %v", got)
	}
	if ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{"packages": ["jq"]}`), githubScope(origin),
		false, false, &failPrompter{t: t}); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	ws2 := approvalWorkspace(t)
	if err := RecordApproval(ws2, decode(t, `{}`), nil); err != nil {
		t.Fatal(err)
	}
	if exists(ApprovalScopePath(ws2)) {
		t.Fatal("RecordApproval with no scope in play wrote a scope part")
	}
}
