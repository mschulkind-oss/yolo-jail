package ghbroker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// approverun_test.go pins the `approve-run` user grant through the broker's real entry point:
// approving a held run that a granted actor triggered from the repository itself runs unasked
// and is audited under its own set; a fork's run, another actor's, a run the broker cannot read,
// and every command that is not exactly the approve POST ask, as any repository write does. The
// host gh is a fake.

// runGHFake is a host gh that answers the broker's own read of a run, and records every other
// command as a write.
type runGHFake struct {
	mu     sync.Mutex
	ran    [][]string
	answer string // the body repos/o/r/actions/runs/<id> answers with
	exit   int    // the exit of that read
}

func (f *runGHFake) execute(_ context.Context, argv []string, _ []byte, out, _ func([]byte)) RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, append([]string(nil), argv...))
	if f.isRead(argv) {
		out([]byte(f.answer))
		return RunResult{Exit: f.exit}
	}
	return RunResult{Exit: 0}
}

func (f *runGHFake) isRead(a []string) bool {
	return len(a) == 2 && a[0] == "api" && strings.HasPrefix(a[1], "repos/o/r/actions/runs/") && !strings.HasSuffix(a[1], "/approve")
}

func (f *runGHFake) reads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.ran {
		if f.isRead(a) {
			n++
		}
	}
	return n
}

func (f *runGHFake) writes() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, a := range f.ran {
		if !f.isRead(a) {
			out = append(out, a)
		}
	}
	return out
}

// runJSON is a run as GitHub's REST API answers it, trimmed to what the broker reads.
func runJSON(id, actor, head string) string {
	return `{"id":` + id + `,"status":"completed","conclusion":"action_required",` +
		`"triggering_actor":{"login":"` + actor + `"},"head_repository":{"full_name":"` + head + `"}}`
}

func approveBroker(t *testing.T, answer string) (*Broker, *runGHFake) {
	t.Helper()
	b, _ := windowFixture(t)
	gh := &runGHFake{answer: answer}
	b.execute = gh.execute
	return b, gh
}

// grantApprove gives the bound scope file the grants and the user config an allowance of
// approve_runs (or not), as `yolo request github approve_runs <actor>` and `yolo config apply`
// would.
func grantApprove(t *testing.T, b *Broker, allowed bool, grants ...brokerscope.Grant) {
	t.Helper()
	all := append(append([]brokerscope.Grant(nil), readAll...), grants...)
	if err := brokerscope.ReplaceScopeContext(context.Background(), b.authority.path, b.authority.scope, b.authority.scope.Repos, all); err != nil {
		t.Fatal(err)
	}
	allow := "always = ['read']\n"
	if allowed {
		allow += "on_request = ['approve_runs']\n"
	}
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte("packs = ['github']\n\n[allow.github]\n"+allow), 0o600); err != nil {
		t.Fatal(err)
	}
}

func actorGrant(actor string) brokerscope.Grant {
	return brokerscope.Grant{Kind: GrantApproveRun, Repo: "github.com/o/r", Actor: actor}
}

var approveArgv = []string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/approve"}

func TestApproveRunOfAGrantedActorFromTheRepositoryRunsUnasked(t *testing.T) {
	for _, c := range []struct {
		name  string
		grant string
		actor string
		head  string
		argv  []string
	}{
		{"the named bot", "github-actions[bot]", "github-actions[bot]", "o/r", approveArgv},
		{"the login in another case", "GitHub-Actions[bot]", "github-actions[bot]", "O/R", approveArgv},
		{"any actor", brokerscope.AnyActor, "octocat", "o/r", approveArgv},
		{"--method spelled long", "octocat", "octocat", "o/r", []string{"api", "--method", "post", "repos/o/r/actions/runs/12345/approve"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, gh := approveBroker(t, runJSON("12345", c.actor, c.head))
			grantApprove(t, b, true, actorGrant(c.grant))
			code, msg := serveGH(b, c.argv...)
			if code != 0 || len(gh.writes()) != 1 || gh.reads() != 1 {
				t.Fatalf("a granted approval: code %d, reads %d, writes %v: %s", code, gh.reads(), gh.writes(), msg)
			}
			if ran := strings.Join(gh.writes()[0], " "); !strings.Contains(ran, "repos/o/r/actions/runs/12345/approve") {
				t.Fatalf("the approval ran %q", ran)
			}
			if len(pending(t, b)) != 0 {
				t.Fatal("a covered approval filed a request")
			}
			audit, err := os.ReadFile(paths.BrokerAuditLog())
			if err != nil || !strings.Contains(string(audit), `"set":"approve-run"`) {
				t.Fatalf("no audit line under the approve-run set (%v):\n%s", err, audit)
			}
		})
	}
}

// Each approval the grant does not cover asks: nothing runs and a request is filed, with a note
// saying why where the broker read the run.
func TestApproveRunTheGrantDoesNotCoverAsks(t *testing.T) {
	bot := runJSON("12345", "github-actions[bot]", "o/r")
	for _, c := range []struct {
		name    string
		grant   brokerscope.Grant
		allowed bool
		answer  string
		exit    int
		argv    []string
		note    string
	}{
		{"a fork's run", actorGrant(brokerscope.AnyActor), true, runJSON("12345", "github-actions[bot]", "evil/r"), 0,
			approveArgv, "never covers a fork's run"},
		{"another actor", actorGrant("github-actions[bot]"), true, runJSON("12345", "octocat", "o/r"), 0,
			approveArgv, "triggered by octocat, whom no approve-run grant"},
		{"an unreadable run", actorGrant("github-actions[bot]"), true, "not json", 0,
			approveArgv, "could not read run 12345"},
		{"a failed read", actorGrant("github-actions[bot]"), true, bot, 1,
			approveArgv, "could not read run 12345"},
		{"an answer naming another run", actorGrant("github-actions[bot]"), true, runJSON("999", "github-actions[bot]", "o/r"), 0,
			approveArgv, "could not read run 12345"},
		{"an actor that is not a login", actorGrant(brokerscope.AnyActor), true, runJSON("12345", "*", "o/r"), 0,
			approveArgv, "is not a GitHub login"},
		{"no head repository", actorGrant("github-actions[bot]"), true, `{"id":12345,"triggering_actor":{"login":"github-actions[bot]"},"head_repository":null}`, 0,
			approveArgv, "names no head repository"},
		{"another repository's grant", brokerscope.Grant{Kind: GrantApproveRun, Repo: "github.com/o/other", Actor: "*"}, true, bot, 0,
			approveArgv, ""},
		{"not allowed by the user", actorGrant("github-actions[bot]"), false, bot, 0, approveArgv, ""},
		{"a non-numeric id", actorGrant("*"), true, bot, 0,
			[]string{"api", "-X", "POST", "repos/o/r/actions/runs/latest/approve"}, ""},
		{"a field", actorGrant("*"), true, bot, 0,
			[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/approve", "-f", "x=y"}, ""},
		{"a body from input", actorGrant("*"), true, bot, 0,
			[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/approve", "--input", "-"}, ""},
		{"another method", actorGrant("*"), true, bot, 0,
			[]string{"api", "-X", "DELETE", "repos/o/r/actions/runs/12345/approve"}, ""},
		{"a rerun", actorGrant("*"), true, bot, 0,
			[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/rerun"}, ""},
		{"a deployment approval", actorGrant("*"), true, bot, 0,
			[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/pending_deployments", "-f", "state=approved"}, ""},
		{"gh run rerun", actorGrant("*"), true, bot, 0, []string{"run", "rerun", "12345"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, gh := approveBroker(t, c.answer)
			gh.exit = c.exit
			grantApprove(t, b, c.allowed, c.grant)
			code, msg := serveGH(b, c.argv...)
			if code != ExitTempFail || len(gh.writes()) != 0 {
				t.Fatalf("an uncovered approval: code %d, writes %v: %s", code, gh.writes(), msg)
			}
			if len(pending(t, b)) != 1 {
				t.Fatal("the uncovered approval filed no request")
			}
			if c.note != "" && !strings.Contains(msg, c.note) {
				t.Fatalf("no note %q: %s", c.note, msg)
			}
		})
	}
}

// A pending request for a run approval names the grant that would let it run unasked.
func TestApproveRunPendingRequestNamesTheGrant(t *testing.T) {
	b, _ := approveBroker(t, runJSON("12345", "github-actions[bot]", "o/r"))
	code, msg := serveGH(b, approveArgv...)
	if code != ExitTempFail || !strings.Contains(msg, "yolo request github approve_runs") || !strings.Contains(msg, "yolo config apply") {
		t.Fatalf("code %d: %s", code, msg)
	}
}

// The grant is re-read, for the actor the broker read, inside the transaction: narrowed to
// another actor between the look and the run, nothing runs.
func TestApproveRunGrantNarrowedBeforeTheTransactionAsks(t *testing.T) {
	b, gh := approveBroker(t, runJSON("12345", "github-actions[bot]", "o/r"))
	grantApprove(t, b, true, actorGrant("github-actions[bot]"))
	afterGrantLook = func() { grantApprove(t, b, true, actorGrant("octocat")) }
	t.Cleanup(func() { afterGrantLook = nil })
	code, msg := serveGH(b, approveArgv...)
	if len(gh.writes()) != 0 || code == 0 || !strings.Contains(msg, "approve-run grant no longer holds") {
		t.Fatalf("a narrowed grant ran: code %d, writes %v: %s", code, gh.writes(), msg)
	}
}

func TestApproveRunOfReadsOnlyThePlainApprovePost(t *testing.T) {
	t0 := writeTarget{Kind: targetRepo, Repo: "o/r"}
	for _, c := range []struct {
		argv []string
		ok   bool
		id   string
	}{
		{[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/approve"}, true, "12345"},
		{[]string{"api", "--method", "POST", "/repos/O/R/actions/runs/7/approve"}, true, "7"},
		{[]string{"api", "repos/o/r/actions/runs/12345/approve"}, false, ""},
		{[]string{"api", "-X", "GET", "repos/o/r/actions/runs/12345/approve"}, false, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/actions/runs/0123/approve"}, false, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/actions/runs/1e3/approve"}, false, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/approve?x=1"}, false, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/approve#x"}, false, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/%61pprove"}, false, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/approve/x"}, false, ""},
		{[]string{"api", "-X", "POST", "repos/o/other/actions/runs/12345/approve"}, false, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/approve", "-F", "a=b"}, false, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/actions/runs/12345/approve", "-f", "a=b"}, false, ""},
		{[]string{"run", "rerun", "12345"}, false, ""},
	} {
		a, ok := approveRunOf(c.argv, t0)
		if ok != c.ok || a.RunID != c.id {
			t.Errorf("%v: got %+v %v, want %q %v", c.argv, a, ok, c.id, c.ok)
		}
	}
}
