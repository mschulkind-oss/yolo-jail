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

// dispatch_test.go pins the `dispatch-workflow` user grant through the broker's real entry
// point (docs/design/github-write-scopes.md GW-D14): a run of the granted workflow on the
// repository's default branch runs unasked and is audited under its own set; a run on another
// ref, of another workflow, on another repository, or under a kind the user's config no longer
// allows asks, as any repository write does. The host gh is a fake.

// dispatchGHFake is a host gh that answers the broker's own reads of a repository and its tags,
// and records every other command as a write.
type dispatchGHFake struct {
	mu        sync.Mutex
	ran       [][]string
	def       string // the default branch repos/o/r reports
	tagShadow bool   // whether a tag of the default branch's name exists
}

func (f *dispatchGHFake) execute(_ context.Context, argv []string, _ []byte, out, _ func([]byte)) RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, append([]string(nil), argv...))
	if len(argv) == 2 && argv[0] == "api" {
		switch {
		case argv[1] == "repos/o/r":
			out([]byte(`{"full_name":"o/r","default_branch":"` + f.def + `"}`))
		case argv[1] == "repos/o/r/git/matching-refs/tags/"+f.def:
			if f.tagShadow {
				out([]byte(`[{"ref":"refs/tags/` + f.def + `"}]`))
			} else {
				out([]byte(`[]`))
			}
		}
	}
	return RunResult{Exit: 0}
}

func (f *dispatchGHFake) isRead(a []string) bool {
	return len(a) == 2 && a[0] == "api" && (a[1] == "repos/o/r" || strings.HasPrefix(a[1], "repos/o/r/git/matching-refs/"))
}

// writes are the commands other than the broker's own reads.
func (f *dispatchGHFake) writes() [][]string {
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

func dispatchBroker(t *testing.T) (*Broker, *dispatchGHFake) {
	t.Helper()
	b, _ := windowFixture(t)
	gh := &dispatchGHFake{def: "main"}
	b.execute = gh.execute
	return b, gh
}

// grantDispatch gives the bound scope file the grants and the user config an allowance of
// dispatch_workflows (or not), as `yolo request github dispatch_workflows <file>` and `yolo
// config apply` would.
func grantDispatch(t *testing.T, b *Broker, allowed bool, grants ...brokerscope.Grant) {
	t.Helper()
	all := append(append([]brokerscope.Grant(nil), readAll...), grants...)
	if err := brokerscope.ReplaceScopeContext(context.Background(), b.authority.path, b.authority.scope, b.authority.scope.Repos, all); err != nil {
		t.Fatal(err)
	}
	allow := "always = ['read']\n"
	if allowed {
		allow += "on_request = ['dispatch_workflows']\n"
	}
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte("packs = ['github']\n\n[allow.github]\n"+allow), 0o600); err != nil {
		t.Fatal(err)
	}
}

func nightlyGrant(workflow string) brokerscope.Grant {
	return brokerscope.Grant{Kind: GrantDispatchWorkflow, Repo: "github.com/o/r", Workflow: workflow}
}

func TestDispatchOnTheDefaultBranchRunsUnaskedAndIsAudited(t *testing.T) {
	for _, argv := range [][]string{
		{"workflow", "run", "nightly-macos.yml", "--ref", "main"},
		{"workflow", "run", "nightly-macos.yml"},
		{"workflow", "run", "nightly-macos.yml", "--ref", "refs/heads/main", "-f", "suite=all"},
		{"api", "repos/o/r/actions/workflows/nightly-macos.yml/dispatches", "-f", "ref=main", "-f", "inputs[suite]=all"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			b, gh := dispatchBroker(t)
			grantDispatch(t, b, true, nightlyGrant("nightly-macos.yml"))
			code, msg := serveGH(b, argv...)
			if code != 0 || len(gh.writes()) != 1 {
				t.Fatalf("a granted dispatch on the default branch: code %d, writes %v: %s", code, gh.writes(), msg)
			}
			if len(pending(t, b)) != 0 {
				t.Fatal("a covered dispatch filed a request")
			}
			audit, err := os.ReadFile(paths.BrokerAuditLog())
			if err != nil || !strings.Contains(string(audit), `"set":"dispatch-workflow"`) {
				t.Fatalf("no audit line under the dispatch-workflow set (%v):\n%s", err, audit)
			}
		})
	}
}

func TestDispatchAnyWorkflowGrantCoversEveryFile(t *testing.T) {
	b, gh := dispatchBroker(t)
	grantDispatch(t, b, true, nightlyGrant(brokerscope.AnyWorkflow))
	for i, file := range []string{"nightly-macos.yml", "macos-user.yml"} {
		if code, msg := serveGH(b, "workflow", "run", file, "--ref", "main"); code != 0 || len(gh.writes()) != i+1 {
			t.Fatalf("%s under a * grant: code %d, writes %d: %s", file, code, len(gh.writes()), msg)
		}
	}
}

// Each run the grant does not cover asks: nothing runs and a request is filed.
func TestDispatchTheGrantDoesNotCoverAsks(t *testing.T) {
	for _, c := range []struct {
		name    string
		grant   brokerscope.Grant
		allowed bool
		argv    []string
		shadow  bool
		note    string
	}{
		{"another ref", nightlyGrant("nightly-macos.yml"), true,
			[]string{"workflow", "run", "nightly-macos.yml", "--ref", "agent/evil"}, false, "default branch (main) only"},
		{"another workflow", nightlyGrant("nightly-macos.yml"), true,
			[]string{"workflow", "run", "release.yml", "--ref", "main"}, false, ""},
		{"a workflow by display name", nightlyGrant(brokerscope.AnyWorkflow), true,
			[]string{"workflow", "run", "Nightly macOS", "--ref", "main"}, false, ""},
		{"a workflow by id", nightlyGrant(brokerscope.AnyWorkflow), true,
			[]string{"workflow", "run", "123456", "--ref", "main"}, false, ""},
		{"another repository", brokerscope.Grant{Kind: GrantDispatchWorkflow, Repo: "github.com/o/other", Workflow: "*"}, true,
			[]string{"workflow", "run", "nightly-macos.yml", "--ref", "main"}, false, ""},
		{"not allowed by the user", nightlyGrant("nightly-macos.yml"), false,
			[]string{"workflow", "run", "nightly-macos.yml", "--ref", "main"}, false, ""},
		{"a tag shadows the default branch", nightlyGrant("nightly-macos.yml"), true,
			[]string{"workflow", "run", "nightly-macos.yml", "--ref", "main"}, true, "a tag named main exists"},
		{"api on another ref", nightlyGrant("nightly-macos.yml"), true,
			[]string{"api", "repos/o/r/actions/workflows/nightly-macos.yml/dispatches", "-f", "ref=agent/evil"}, false, "default branch (main) only"},
		{"api with its body from input", nightlyGrant("nightly-macos.yml"), true,
			[]string{"api", "repos/o/r/actions/workflows/nightly-macos.yml/dispatches", "--input", "-"}, false, ""},
		{"a rerun", nightlyGrant(brokerscope.AnyWorkflow), true,
			[]string{"run", "rerun", "12345"}, false, ""},
		{"a workflow disable", nightlyGrant(brokerscope.AnyWorkflow), true,
			[]string{"workflow", "disable", "nightly-macos.yml"}, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, gh := dispatchBroker(t)
			gh.tagShadow = c.shadow
			grantDispatch(t, b, c.allowed, c.grant)
			code, msg := serveGH(b, c.argv...)
			if code != ExitTempFail || len(gh.writes()) != 0 {
				t.Fatalf("an uncovered run: code %d, writes %v: %s", code, gh.writes(), msg)
			}
			if len(pending(t, b)) != 1 {
				t.Fatal("the uncovered run filed no request")
			}
			if c.note != "" && !strings.Contains(msg, c.note) {
				t.Fatalf("no note %q: %s", c.note, msg)
			}
		})
	}
}

// A pending request for a workflow run names the grant that would let it run unasked.
func TestDispatchPendingRequestNamesTheGrant(t *testing.T) {
	b, _ := dispatchBroker(t)
	code, msg := serveGH(b, "workflow", "run", "nightly-macos.yml", "--ref", "main")
	if code != ExitTempFail || !strings.Contains(msg, "yolo request github dispatch_workflows nightly-macos.yml") {
		t.Fatalf("code %d: %s", code, msg)
	}
}

// The grant is re-read inside the transaction: withdrawn between the look and the run, nothing
// runs.
func TestDispatchGrantWithdrawnBeforeTheTransactionAsks(t *testing.T) {
	b, gh := dispatchBroker(t)
	grantDispatch(t, b, true, nightlyGrant("nightly-macos.yml"))
	afterGrantLook = func() { grantDispatch(t, b, false, nightlyGrant("nightly-macos.yml")) }
	t.Cleanup(func() { afterGrantLook = nil })
	code, msg := serveGH(b, "workflow", "run", "nightly-macos.yml", "--ref", "main")
	if len(gh.writes()) != 0 || code == 0 || !strings.Contains(msg, "dispatch-workflow grant no longer holds") {
		t.Fatalf("a withdrawn grant ran: code %d, writes %v: %s", code, gh.writes(), msg)
	}
}

func TestDispatchOfReadsOnlyAFileNamedRun(t *testing.T) {
	t0 := writeTarget{Kind: targetRepo, Repo: "o/r"}
	for _, c := range []struct {
		argv     []string
		ok       bool
		workflow string
		ref      string
	}{
		{[]string{"workflow", "run", "a.yml", "--repo", "o/r"}, true, "a.yml", ""},
		{[]string{"workflow", "run", "a.yaml", "-r", "dev", "--repo", "o/r"}, true, "a.yaml", "dev"},
		{[]string{"workflow", "run", "a.yml", "--ref", "x", "--ref", "y", "--repo", "o/r"}, false, "", ""},
		{[]string{"workflow", "run", "*", "--repo", "o/r"}, false, "", ""},
		{[]string{"api", "repos/o/r/actions/workflows/a.yml/dispatches", "-f", "ref=main"}, true, "a.yml", "main"},
		{[]string{"api", "repos/o/r/actions/workflows/a.yml/dispatches", "-f", "ref=main", "-f", "ref=dev"}, false, "", ""},
		{[]string{"api", "repos/o/r/actions/workflows/a.yml/dispatches", "-f", "ref[x]=main"}, false, "", ""},
		{[]string{"api", "repos/o/r/actions/workflows/a.yml/dispatches", "-X", "GET", "-f", "ref=main"}, false, "", ""},
		{[]string{"api", "repos/o/other/actions/workflows/a.yml/dispatches", "-f", "ref=main"}, false, "", ""},
		{[]string{"api", "repos/o/r/actions/workflows/a%2Eyml/dispatches", "-f", "ref=main"}, false, "", ""},
	} {
		w, ok := dispatchOf(c.argv, t0)
		if ok != c.ok || w.Workflow != c.workflow || w.Ref != c.ref {
			t.Errorf("%v: got %+v %v, want %q %q %v", c.argv, w, ok, c.workflow, c.ref, c.ok)
		}
	}
}
