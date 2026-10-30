package ghbroker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// workingset_test.go pins the GitHub working set through the broker's real entry point
// (docs/design/boundary-broker.md §5.7, §5.8, §6.4, BB-D76, BB-D79, BB-D80, BB-D92, BB-D97):
// a creation under a create-pr grant runs unasked and its PR joins the working set, a write to
// a working-set PR runs unasked, a merge asks and ends the entry, a merged PR leaves the set,
// and a human's --add or --for answers cover the next write. The host gh is a fake.

// hostGHFake is a host gh that records every argv it runs and answers `pr create`, `issue create`
// and the broker's own issues/{n} read.
type hostGHFake struct {
	mu     sync.Mutex
	ran    [][]string
	merged bool
	create string // what `pr create` or `issue create` prints
}

func (f *hostGHFake) execute(_ context.Context, argv []string, _ []byte, out, _ func([]byte)) RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, append([]string(nil), argv...))
	switch {
	case len(argv) >= 2 && argv[0] == "api" && strings.HasPrefix(argv[1], "repos/o/r/issues/"):
		n := strings.TrimPrefix(argv[1], "repos/o/r/issues/")
		merged := "null"
		if f.merged {
			merged = `"2026-10-30T00:00:00Z"`
		}
		out([]byte(`{"number":` + n + `,"pull_request":{"merged_at":` + merged + `}}`))
	case len(argv) >= 2 && argv[1] == "create":
		out([]byte(f.create))
	}
	return RunResult{Exit: 0}
}

// writes is how many commands other than the broker's own issues/{n} read ran.
func (f *hostGHFake) writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.ran {
		if !(len(a) == 2 && a[0] == "api" && strings.HasPrefix(a[1], "repos/o/r/issues/")) {
			n++
		}
	}
	return n
}

func workingSetBroker(t *testing.T) (*Broker, *hostGHFake) {
	t.Helper()
	b, _ := windowFixture(t)
	gh := &hostGHFake{create: "Creating pull request for agent/x into main in o/r\n\nhttps://github.com/o/r/pull/41\n"}
	b.execute = gh.execute
	return b, gh
}

func serveGH(b *Broker, argv ...string) (int, string) {
	var errOut bytes.Buffer
	code := b.ServeContext(context.Background(), Request{Argv: argv, Repo: "o/r"}, paths.JailShortHash(b.authority.scope.Container),
		func([]byte) {}, func(p []byte) { errOut.Write(p) })
	return code, strings.Join(strings.Fields(errOut.String()), " ")
}

// grantCreatePR gives the bound scope file a create-pr grant and the user config an allowance
// of create_pr, as `yolo request github create_pr` and `yolo config apply` would.
func grantCreatePR(t *testing.T, b *Broker, allowed bool) {
	t.Helper()
	grants := append(append([]brokerscope.Grant(nil), readAll...), brokerscope.Grant{Kind: GrantCreatePR, Repo: "github.com/o/r"})
	if err := brokerscope.ReplaceScopeContext(context.Background(), b.authority.path, b.authority.scope, b.authority.scope.Repos, grants); err != nil {
		t.Fatal(err)
	}
	allow := "always = ['read']\n"
	if allowed {
		allow += "on_request = ['create_pr']\n"
	}
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte("packs = ['github']\n\n[allow.github]\n"+allow), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pending(t *testing.T, b *Broker) []brokerstore.Request {
	t.Helper()
	requests, _, err := b.store.List()
	if err != nil {
		t.Fatal(err)
	}
	var out []brokerstore.Request
	for _, q := range requests {
		if q.State == "pending" {
			out = append(out, q)
		}
	}
	return out
}

func TestCreatePRUnderAGrantRunsUnaskedAndJoinsTheWorkingSet(t *testing.T) {
	b, gh := workingSetBroker(t)
	grantCreatePR(t, b, true)
	code, msg := serveGH(b, "pr", "create", "--title", "t", "--body", "b", "--head", "agent/x")
	if code != 0 || gh.writes() != 1 {
		t.Fatalf("pr create under a create-pr grant: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	if !strings.Contains(msg, "PR o/r#41 joined this workspace's working set") {
		t.Fatalf("no working-set note: %s", msg)
	}
	e, ok, err := b.store.LiveWorkingKind(b.workspace, "github.com/o/r", "#41", brokerstore.WorkingPR)
	if err != nil || !ok || e.By != GrantCreatePR {
		t.Fatalf("PR #41 entry %+v %v %v", e, ok, err)
	}
	if len(pending(t, b)) != 0 {
		t.Fatal("a covered creation filed a request")
	}

	// Its later writes run unasked; the broker reads once that it is not merged.
	if code, msg := serveGH(b, "pr", "comment", "41", "--body", "done"); code != 0 || gh.writes() != 2 {
		t.Fatalf("comment on a working-set PR: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	// Another PR asks.
	if code, _ := serveGH(b, "pr", "comment", "40", "--body", "x"); code != ExitTempFail || gh.writes() != 2 {
		t.Fatalf("comment on a PR outside the working set: code %d, writes %d", code, gh.writes())
	}
}

func TestCreatePRGrantNeedsTheUsersAllowance(t *testing.T) {
	b, gh := workingSetBroker(t)
	grantCreatePR(t, b, false)
	if code, _ := serveGH(b, "pr", "create", "--title", "t", "--body", "b", "--head", "agent/x"); code != ExitTempFail || gh.writes() != 0 {
		t.Fatalf("a create-pr grant the user config no longer allows ran: code %d, writes %d", code, gh.writes())
	}
	if len(pending(t, b)) != 1 {
		t.Fatal("the uncovered creation filed no request")
	}
}

func TestWorkingSetMergeAsksAndEndsTheEntry(t *testing.T) {
	b, gh := workingSetBroker(t)
	if _, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace, Repo: "github.com/o/r",
		Kind: brokerstore.WorkingPR, Resource: "#7", By: GrantCreatePR}); err != nil {
		t.Fatal(err)
	}
	code, msg := serveGH(b, "pr", "merge", "7", "--squash")
	if code != ExitTempFail || gh.writes() != 0 {
		t.Fatalf("a merge of a working-set PR ran unasked: code %d: %s", code, msg)
	}
	if !strings.Contains(msg, "A merge asks per PR") {
		t.Fatalf("no merge hint: %s", msg)
	}
	q := pending(t, b)
	if len(q) != 1 {
		t.Fatalf("requests %+v", q)
	}
	if _, err := b.store.Decide(q[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	if code, msg := serveGH(b, "pr", "merge", "7", "--squash"); code != 0 || gh.writes() != 1 {
		t.Fatalf("an allowed merge: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	if _, ok, _ := b.store.LiveWorkingKind(b.workspace, "github.com/o/r", "#7", brokerstore.WorkingPR); ok {
		t.Fatal("a merged PR stayed in the working set")
	}
	all, _ := b.store.Working()
	if len(all) != 1 || all[0].EndReason != brokerstore.EndMerged {
		t.Fatalf("working set %+v", all)
	}
}

func TestAMergedPRLeavesTheWorkingSetAndItsWriteAsks(t *testing.T) {
	b, gh := workingSetBroker(t)
	if _, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace, Repo: "github.com/o/r",
		Kind: brokerstore.WorkingPR, Resource: "#7", By: GrantCreatePR}); err != nil {
		t.Fatal(err)
	}
	gh.merged = true
	code, msg := serveGH(b, "pr", "comment", "7", "--body", "late")
	if code != ExitTempFail || gh.writes() != 0 || !strings.Contains(msg, "is merged") {
		t.Fatalf("a comment on a merged PR: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	if _, ok, _ := b.store.LiveWorkingKind(b.workspace, "github.com/o/r", "#7", brokerstore.WorkingPR); ok {
		t.Fatal("the merged PR stayed in the working set")
	}
}

// TestApproveAddAndForCoverTheNextWrite answers a filed request the way `yolo approve` does,
// through the GitHub store's Entries hook, and runs the write again.
func TestApproveAddAndForCoverTheNextWrite(t *testing.T) {
	b, gh := workingSetBroker(t)
	b.store.Entries = RequestEntries
	if code, msg := serveGH(b, "pr", "comment", "12", "--body", "x"); code != ExitTempFail || !strings.Contains(msg, "--add") {
		t.Fatalf("first write: code %d: %s", code, msg)
	}
	q := pending(t, b)[0]
	entries, err := b.store.DecideWorking(q.ID, brokerstore.Decision{Add: true})
	if err != nil || len(entries) != 1 || entries[0].Kind != brokerstore.WorkingPR || entries[0].Resource != "#12" || entries[0].Timed() {
		t.Fatalf("--add: %+v %v", entries, err)
	}
	if code, msg := serveGH(b, "pr", "edit", "12", "--title", "y"); code != 0 || gh.writes() != 1 {
		t.Fatalf("a write to an added PR: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	if _, err := b.store.RevokeIn(entries[0].ID, ""); err != nil {
		t.Fatal(err)
	}
	if code, _ := serveGH(b, "pr", "edit", "12", "--title", "z"); code != ExitTempFail || gh.writes() != 1 {
		t.Fatalf("a revoked entry still covered: code %d, writes %d", code, gh.writes())
	}

	// --for: a merge for fifteen minutes.
	if code, _ := serveGH(b, "pr", "merge", "12", "--rebase"); code != ExitTempFail {
		t.Fatalf("merge: code %d", code)
	}
	var merge brokerstore.Request
	for _, r := range pending(t, b) {
		if r.Argv[1] == "merge" {
			merge = r
		}
	}
	if _, err := b.store.DecideWorking(merge.ID, brokerstore.Decision{Add: true}); err == nil {
		t.Fatal("--add was accepted for a merge")
	}
	entries, err = b.store.DecideWorking(merge.ID, brokerstore.Decision{For: 15 * time.Minute})
	if err != nil || entries[0].Kind != brokerstore.WorkingMerge || !entries[0].Timed() {
		t.Fatalf("--for on a merge: %+v %v", entries, err)
	}
	if code, msg := serveGH(b, "pr", "merge", "12", "--rebase"); code != 0 || gh.writes() != 2 {
		t.Fatalf("a merge under --for: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	if _, ok, _ := b.store.LiveWorkingKind(b.workspace, "github.com/o/r", "#12", brokerstore.WorkingMerge); ok {
		t.Fatal("the merge grant outlived the merge")
	}
}

func TestTargetOfNamesKindAndResource(t *testing.T) {
	a, _ := authorityFixture(t)
	for _, c := range []struct {
		argv []string
		kind string
		n    int
	}{
		{[]string{"pr", "comment", "32", "--body", "x"}, targetPR, 32},
		{[]string{"pr", "comment", "#32", "--body", "x"}, targetPR, 32},
		{[]string{"pr", "review", "https://github.com/o/r/pull/32", "--approve"}, targetPR, 32},
		{[]string{"pr", "close", "32", "--delete-branch"}, targetRepo, 0},
		{[]string{"pr", "comment", "agent/x", "--body", "x"}, targetUnresolved, 0},
		{[]string{"pr", "merge", "32", "--squash"}, targetMerge, 32},
		{[]string{"pr", "create", "--title", "t", "--body", "b"}, targetCreatePR, 0},
		{[]string{"issue", "create", "--title", "t", "--body", "b"}, targetCreateIssue, 0},
		{[]string{"issue", "close", "5"}, targetIssue, 5},
		{[]string{"api", "-X", "POST", "repos/o/r/issues/5/comments", "-f", "body=x"}, targetIssueOrPR, 5},
		{[]string{"api", "-X", "PATCH", "repos/o/r/pulls/5", "-f", "title=x"}, targetPR, 5},
		{[]string{"api", "-X", "PUT", "repos/o/r/pulls/5/merge"}, targetMerge, 5},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls", "-f", "title=x"}, targetCreatePR, 0},
		{[]string{"api", "-X", "POST", "repos/o/r/labels", "-f", "name=x"}, targetRepo, 0},
	} {
		d, _, err := a.Classify(c.argv, "o/r")
		if err != nil {
			t.Errorf("%v: %v", c.argv, err)
			continue
		}
		if got := targetOf(d.Argv, d.Repos); got.Kind != c.kind || got.Number != c.n {
			t.Errorf("%v: target %+v, want %s #%d", c.argv, got, c.kind, c.n)
		}
	}
}

func TestCreatedNumberReadsOnlyGhsOwnReport(t *testing.T) {
	a, _ := authorityFixture(t)
	canon := func(argv ...string) ([]string, writeTarget) {
		d, _, err := a.Classify(argv, "o/r")
		if err != nil {
			t.Fatal(err)
		}
		return d.Argv, targetOf(d.Argv, d.Repos)
	}
	argv, tg := canon("pr", "create", "--title", "t", "--body", "b")
	for out, want := range map[string]int{
		"https://github.com/o/r/pull/41\n":            41,
		"https://github.com/O/R/pull/41\n":            41,
		"https://github.com/o/other/pull/41\n":        0,
		"https://github.com/o/r/issues/41\n":          0,
		"https://github.com/o/r/pull/41 trailing\n":   0,
		"https://github.com/o/r/pull/041\n":           0,
		"warning\nhttps://github.com/o/r/pull/9\n":    9,
		"https://github.com/o/r/pull/9\nlater line\n": 0,
	} {
		if got := createdNumber(tg, argv, []byte(out)); got != want {
			t.Errorf("pr create printing %q: %d, want %d", out, got, want)
		}
	}
	argv, tg = canon("pr", "create", "--title", "t", "--body", "b", "--dry-run")
	if got := createdNumber(tg, argv, []byte("https://github.com/o/r/pull/41\n")); got != 0 {
		t.Errorf("a dry run joined: %d", got)
	}
	argv, tg = canon("api", "-X", "POST", "repos/o/r/issues", "-f", "title=x")
	if got := createdNumber(tg, argv, []byte(`{"number":8,"html_url":"https://github.com/o/r/issues/8"}`)); got != 8 {
		t.Errorf("api issue creation: %d", got)
	}
	if got := createdNumber(tg, argv, []byte(`{"number":9,"html_url":"https://github.com/o/r/issues/8"}`)); got != 0 {
		t.Errorf("a number its URL does not match joined: %d", got)
	}
	// Output the agent shapes cannot name what joins.
	argv, tg = canon("api", "-X", "POST", "repos/o/r/issues", "-f", "title=x", "--jq", ".")
	if got := createdNumber(tg, argv, []byte(`{"number":8,"html_url":"https://github.com/o/r/issues/8"}`)); got != 0 {
		t.Errorf("--jq output joined: %d", got)
	}
}
