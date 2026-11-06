package ghbroker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// runs_test.go pins the `runs` user grant's re-run and cancel halves through the broker's real
// entry point (docs/design/github-write-scopes.md GW-D78; dispatch_test.go pins the dispatch
// half): a cancel of any run is covered by any runs grant on the repository; a re-run is covered
// only when the broker's own read of the run shows a run of a granted workflow whose head is the
// default branch of the repository itself. Everything else asks. The host gh is a fake.

// runsGHFake is a host gh that answers the broker's reads of the repository, a run and a job,
// and records every other command as a write.
type runsGHFake struct {
	mu   sync.Mutex
	ran  [][]string
	runs map[string]string // run id -> the run's JSON
	jobs map[string]string // job id -> the job's JSON
}

func (f *runsGHFake) execute(_ context.Context, argv []string, _ []byte, out, _ func([]byte)) RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, append([]string(nil), argv...))
	if len(argv) == 2 && argv[0] == "api" {
		switch path := argv[1]; {
		case path == "repos/o/r":
			out([]byte(`{"full_name":"o/r","default_branch":"main"}`))
		case strings.HasPrefix(path, "repos/o/r/actions/runs/"):
			if body, ok := f.runs[strings.TrimPrefix(path, "repos/o/r/actions/runs/")]; ok {
				out([]byte(body))
				return RunResult{}
			}
			return RunResult{Exit: 1}
		case strings.HasPrefix(path, "repos/o/r/actions/jobs/"):
			if body, ok := f.jobs[strings.TrimPrefix(path, "repos/o/r/actions/jobs/")]; ok {
				out([]byte(body))
				return RunResult{}
			}
			return RunResult{Exit: 1}
		}
	}
	return RunResult{}
}

func (f *runsGHFake) writes() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, a := range f.ran {
		if !(len(a) == 2 && a[0] == "api" && strings.HasPrefix(a[1], "repos/o/r")) {
			out = append(out, a)
		}
	}
	return out
}

func runHeadJSON(id, path, branch, headRepo string) string {
	return `{"id":` + id + `,"path":"` + path + `","head_branch":"` + branch + `","head_repository":{"full_name":"` + headRepo + `"}}`
}

func runsBroker(t *testing.T) (*Broker, *runsGHFake) {
	t.Helper()
	b, _ := windowFixture(t)
	gh := &runsGHFake{
		runs: map[string]string{
			"42228358413": runHeadJSON("42228358413", ".github/workflows/nightly-macos.yml", "main", "o/r"),
			"100":         runHeadJSON("100", ".github/workflows/nightly-macos.yml", "agent/evil", "o/r"),
			"101":         runHeadJSON("101", ".github/workflows/nightly-macos.yml", "main", "fork/r"),
			"102":         runHeadJSON("102", ".github/workflows/release.yml", "main", "o/r"),
			"103":         runHeadJSON("103", "dynamic/pages/pages-build-deployment", "main", "o/r"),
		},
		jobs: map[string]string{"777": `{"id":777,"run_id":42228358413}`},
	}
	b.execute = gh.execute
	return b, gh
}

// grantRuns gives the bound scope file the grants and the user config the allowance list
// allow (`on_request = [...]`), as `yolo request github runs <file>` and `yolo config apply`
// would.
func grantRuns(t *testing.T, b *Broker, onRequest string, grants ...brokerscope.Grant) {
	t.Helper()
	all := append(append([]brokerscope.Grant(nil), readAll...), grants...)
	if err := brokerscope.ReplaceScopeContext(context.Background(), b.authority.path, b.authority.scope, b.authority.scope.Repos, all); err != nil {
		t.Fatal(err)
	}
	allow := "always = ['read']\n"
	if onRequest != "" {
		allow += "on_request = [" + onRequest + "]\n"
	}
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte("packs = ['github']\n\n[allow.github]\n"+allow), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runsGrant(workflow string) brokerscope.Grant {
	return brokerscope.Grant{Kind: GrantRuns, Repo: "github.com/o/r", Workflow: workflow}
}

// The command of 2026-11-06 that asked, and any other cancel and covered re-run, runs unasked
// under a runs grant and is audited under the runs set; no request is filed.
func TestRunsGrantCoversCancelsAndDefaultBranchReruns(t *testing.T) {
	for _, c := range []struct {
		name  string
		grant brokerscope.Grant
		argv  []string
	}{
		{"cancel, under a grant naming another workflow", runsGrant("release.yml"), []string{"run", "cancel", "42228358413"}},
		{"cancel of a run on another branch", runsGrant("nightly-macos.yml"), []string{"run", "cancel", "100"}},
		{"cancel, forced", runsGrant("*"), []string{"run", "cancel", "42228358413", "--force"}},
		{"rerun", runsGrant("nightly-macos.yml"), []string{"run", "rerun", "42228358413"}},
		{"rerun of failed jobs", runsGrant("*"), []string{"run", "rerun", "42228358413", "--failed"}},
		{"rerun of one job", runsGrant("nightly-macos.yml"), []string{"run", "rerun", "--job", "777"}},
		{"rerun under a grant on every repository", brokerscope.Grant{Kind: GrantRuns, Repo: brokerscope.AnyRepo, Workflow: "*"},
			[]string{"run", "rerun", "42228358413", "--debug"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, gh := runsBroker(t)
			grantRuns(t, b, "'runs'", c.grant)
			code, msg := serveGH(b, c.argv...)
			if code != 0 || len(gh.writes()) != 1 || gh.writes()[0][1] != c.argv[1] {
				t.Fatalf("a covered %v: code %d, writes %v: %s", c.argv, code, gh.writes(), msg)
			}
			if len(pending(t, b)) != 0 {
				t.Fatal("a covered command filed a request")
			}
			audit, err := os.ReadFile(paths.BrokerAuditLog())
			if err != nil || !strings.Contains(string(audit), `"set":"runs"`) {
				t.Fatalf("no audit line under the runs set (%v):\n%s", err, audit)
			}
		})
	}
}

// Each command the grant does not cover asks: nothing runs, a request is filed, and the refusal
// says why and what would grant it.
func TestRunsGrantDoesNotCoverAsks(t *testing.T) {
	for _, c := range []struct {
		name      string
		onRequest string
		grant     brokerscope.Grant
		argv      []string
		note      string
	}{
		{"cancel with no runs grant", "'runs'", brokerscope.Grant{Kind: GrantCreatePR, Repo: "github.com/o/r"},
			[]string{"run", "cancel", "42228358413"}, "Any runs grant on o/r (`yolo request github runs <workflow file>`"},
		{"cancel the user's config does not allow", "", runsGrant("*"),
			[]string{"run", "cancel", "42228358413"}, ""},
		{"cancel on another repository's grant", "'runs'", brokerscope.Grant{Kind: GrantRuns, Repo: "github.com/o/other", Workflow: "*"},
			[]string{"run", "cancel", "42228358413"}, ""},
		{"rerun of another branch's run", "'runs'", runsGrant("*"),
			[]string{"run", "rerun", "100"}, `run 100 ran on "agent/evil", not o/r's default branch (main)`},
		{"rerun of a fork's run", "'runs'", runsGrant("*"),
			[]string{"run", "rerun", "101"}, `run 101's head is in "fork/r"`},
		{"rerun of a workflow no grant names", "'runs'", runsGrant("nightly-macos.yml"),
			[]string{"run", "rerun", "102"}, "`yolo request github runs release.yml`"},
		{"rerun of a run outside .github/workflows", "'runs'", runsGrant("*"),
			[]string{"run", "rerun", "103"}, "is not a file in .github/workflows"},
		{"rerun of a run the broker cannot read", "'runs'", runsGrant("*"),
			[]string{"run", "rerun", "999"}, "could not read run 999 on o/r"},
		{"rerun with no run named", "'runs'", runsGrant("*"),
			[]string{"run", "rerun"}, ""},
		{"rerun naming both a run and a job", "'runs'", runsGrant("*"),
			[]string{"run", "rerun", "42228358413", "--job", "777"}, ""},
		{"workflow enable", "'runs'", runsGrant("*"),
			[]string{"workflow", "enable", "nightly-macos.yml"}, ""},
		{"run delete", "'runs'", runsGrant("*"),
			[]string{"run", "delete", "42228358413"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, gh := runsBroker(t)
			grantRuns(t, b, c.onRequest, c.grant)
			code, msg := serveGH(b, c.argv...)
			if code != ExitTempFail || len(gh.writes()) != 0 {
				t.Fatalf("an uncovered %v: code %d, writes %v: %s", c.argv, code, gh.writes(), msg)
			}
			if len(pending(t, b)) != 1 {
				t.Fatal("the uncovered command filed no request")
			}
			if c.note != "" && !strings.Contains(msg, c.note) {
				t.Fatalf("no note %q: %s", c.note, msg)
			}
		})
	}
}

// A user config written before GW-D78 allows the capability by its former spelling,
// `dispatch_workflows`, and an older scope file names the kind `dispatch-workflow`; both read as
// runs, so the grant keeps working, now for cancels too.
func TestRunsGrantUnderTheFormerSpellingStillCovers(t *testing.T) {
	b, gh := runsBroker(t)
	grantRuns(t, b, "'dispatch_workflows'", runsGrant("nightly-macos.yml"))
	if code, msg := serveGH(b, "run", "cancel", "42228358413"); code != 0 || len(gh.writes()) != 1 {
		t.Fatalf("a cancel under the former capability spelling: code %d, writes %v: %s", code, gh.writes(), msg)
	}
}

// Evaluate reports a covered re-run as one that runs, and sends nothing.
func TestEvaluateRerunUnderARunsGrant(t *testing.T) {
	b, gh := runsBroker(t)
	grantRuns(t, b, "'runs'", runsGrant("nightly-macos.yml"))
	var got evaluation
	code := b.ServeContext(context.Background(), Request{Mode: "evaluate", Argv: []string{"run", "rerun", "42228358413"}, Repo: "o/r"},
		paths.JailShortHash(b.authority.scope.Container), func(p []byte) { _ = json.Unmarshal(p, &got) }, func([]byte) {})
	if code != 0 || got.Verdict != "runs" || got.Rule != GrantRuns {
		t.Fatalf("evaluate: code=%d result=%+v", code, got)
	}
	if len(gh.writes()) != 0 {
		t.Fatalf("evaluate sent the re-run: %v", gh.writes())
	}
}

// The grant is re-read inside the transaction: withdrawn between the look and the run, a re-run
// does not run.
func TestRerunGrantWithdrawnBeforeTheTransactionAsks(t *testing.T) {
	b, gh := runsBroker(t)
	grantRuns(t, b, "'runs'", runsGrant("nightly-macos.yml"))
	afterGrantLook = func() { grantRuns(t, b, "'runs'", runsGrant("release.yml")) }
	t.Cleanup(func() { afterGrantLook = nil })
	code, msg := serveGH(b, "run", "rerun", "42228358413")
	if len(gh.writes()) != 0 || code == 0 || !strings.Contains(msg, "runs grant no longer holds") {
		t.Fatalf("a withdrawn grant ran: code %d, writes %v: %s", code, gh.writes(), msg)
	}
}

func TestRunsOfReadsOnlyANamedRun(t *testing.T) {
	t0 := writeTarget{Kind: targetRepo, Repo: "o/r"}
	for _, c := range []struct {
		argv []string
		ok   bool
		want runsAction
	}{
		{[]string{"run", "cancel", "12", "--repo", "o/r"}, true, runsAction{Op: runsCancel, RunID: "12"}},
		{[]string{"run", "rerun", "12", "--failed"}, true, runsAction{Op: runsRerun, RunID: "12"}},
		{[]string{"run", "rerun", "--job", "34"}, true, runsAction{Op: runsRerun, JobID: "34"}},
		{[]string{"run", "rerun", "-j", "34"}, true, runsAction{Op: runsRerun, JobID: "34"}},
		{[]string{"run", "cancel"}, false, runsAction{}},
		{[]string{"run", "cancel", "012"}, false, runsAction{}},
		{[]string{"run", "cancel", "latest"}, false, runsAction{}},
		{[]string{"run", "rerun", "12", "--job", "34"}, false, runsAction{}},
		{[]string{"run", "rerun", "--job", "34", "--job", "35"}, false, runsAction{}},
		{[]string{"run", "delete", "12"}, false, runsAction{}},
		{[]string{"workflow", "run", "a.yml"}, true, runsAction{Op: runsDispatch, Dispatch: workflowDispatch{Workflow: "a.yml"}}},
	} {
		got, ok := runsOf(c.argv, t0)
		if ok != c.ok || got != c.want {
			t.Errorf("%v: got %+v %v, want %+v %v", c.argv, got, ok, c.want, c.ok)
		}
	}
}
