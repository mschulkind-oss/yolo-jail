package ghbroker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// authorityGH is a host gh for the stored-authority pins. It answers the broker's reads (a
// repository's default branch, a run and PR 7) for o/r and o/other, both on main, and records every
// other command as a write.
type authorityGH struct {
	mu  sync.Mutex
	ran [][]string
}

func (f *authorityGH) execute(_ context.Context, argv []string, _ []byte, out, _ func([]byte)) RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, append([]string(nil), argv...))
	if f.isRead(argv) {
		switch path := argv[1]; {
		case strings.Contains(path, "/issues/7"):
			out([]byte(`{"number":7,"state":"open","pull_request":{"merged_at":null}}`))
		case strings.HasPrefix(path, "repos/") && strings.HasSuffix(path, "/actions/runs/55"):
			repo := strings.TrimSuffix(strings.TrimPrefix(path, "repos/"), "/actions/runs/55")
			out([]byte(runHeadJSON("55", ".github/workflows/ci.yml", "main", repo)))
		case strings.HasPrefix(path, "repos/"):
			repo := strings.TrimPrefix(path, "repos/")
			out([]byte(`{"full_name":"` + repo + `","default_branch":"main"}`))
		}
	}
	return RunResult{}
}

func (f *authorityGH) isRead(a []string) bool {
	return len(a) == 2 && a[0] == "api" && strings.HasPrefix(a[1], "repos/")
}

// writes are the commands other than the broker's own reads.
func (f *authorityGH) writes() [][]string {
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

// bindAuthorityScope replaces the bound scope file with exactly repos and grants (no read-anything
// grant), and writes the user config's allowance: read always, and onRequest for the kinds named.
func bindAuthorityScope(t *testing.T, b *Broker, repos []string, onRequest string, grants ...brokerscope.Grant) {
	t.Helper()
	if err := brokerscope.ReplaceScopeContext(context.Background(), b.authority.path, b.authority.scope, repos, grants); err != nil {
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

// readAuthorityOn is the read grant on one repository, as the gate approves it.
func readAuthorityOn(repo string) brokerscope.Grant {
	return brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/" + repo}
}

// serveAuthorityOn serves argv as a command run in a checkout whose origin is origin. A -R in argv
// names the repository the command is for; origin is only the checkout's.
func serveAuthorityOn(b *Broker, origin string, argv ...string) (int, string) {
	var errOut strings.Builder
	code := b.ServeContext(context.Background(), Request{Argv: argv, Repo: origin}, paths.JailShortHash(b.authority.scope.Container),
		func([]byte) {}, func(p []byte) { errOut.Write(p) })
	return code, strings.Join(strings.Fields(errOut.String()), " ")
}

// GW-D79: origin holds every workflow, approve-run and create-pr; a second repository in scope is
// readable and nothing more. Each dispatch, cancel and re-run is unasked on origin and asks on the
// second repository, and a grant the second repository holds covers its own commands.
func TestOriginsGrantsDoNotReachASecondRepositoryInScope(t *testing.T) {
	for _, c := range []struct {
		name string
		argv func(repo string) []string
	}{
		{"dispatch", func(r string) []string { return []string{"workflow", "run", "ci.yml", "-R", r} }},
		{"cancel", func(r string) []string { return []string{"run", "cancel", "55", "-R", r} }},
		{"rerun", func(r string) []string { return []string{"run", "rerun", "55", "-R", r} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, _ := windowFixture(t)
			gh := &authorityGH{}
			b.execute = gh.execute
			bindAuthorityScope(t, b, []string{"o/r", "o/other"}, "'runs'",
				readAuthorityOn("o/r"), readAuthorityOn("o/other"),
				brokerscope.Grant{Kind: GrantRuns, Repo: "github.com/o/r", Workflow: brokerscope.AnyWorkflow})
			if code, msg := serveAuthorityOn(b, "o/r", c.argv("o/other")...); code != ExitTempFail || len(gh.writes()) != 0 {
				t.Fatalf("origin's grant reached o/other: code %d, writes %v: %s", code, gh.writes(), msg)
			}
			if got := pending(t, b); len(got) != 1 || got[0].Repo != "o/other" {
				t.Fatalf("the uncovered write filed %+v, want one request on o/other", got)
			}
			if code, msg := serveAuthorityOn(b, "o/r", c.argv("o/r")...); code != 0 || len(gh.writes()) != 1 {
				t.Fatalf("origin's own grant did not cover: code %d, writes %v: %s", code, gh.writes(), msg)
			}
		})
	}
}

// A lasting working-set entry on a repository the scope holds keeps covering its own resource
// while the repository is readable in scope. It stops when the read is withdrawn or the repository
// leaves the scope, is never ended by either change, and covers again when the scope is restored.
func TestStoredEntriesFollowTheLiveScopeAndTheReadFloor(t *testing.T) {
	b, _ := windowFixture(t)
	gh := &authorityGH{}
	b.execute = gh.execute
	b.store.Entries = RequestEntries
	both := []string{"o/r", "o/other"}
	bindAuthorityScope(t, b, both, "", readAuthorityOn("o/r"), readAuthorityOn("o/other"))
	lasting, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace,
		Repo: "github.com/o/other", Kind: brokerstore.WorkingPR, Resource: "#7", By: GrantCreatePR})
	if err != nil {
		t.Fatal(err)
	}
	comment := []string{"pr", "comment", "7", "--body", "x", "-R", "o/other"}
	if code, msg := serveAuthorityOn(b, "o/r", comment...); code != 0 || len(gh.writes()) != 1 {
		t.Fatalf("a readable in-scope entry did not cover: code %d, writes %v: %s", code, gh.writes(), msg)
	}
	// The same number on origin is another resource.
	if code, _ := serveAuthorityOn(b, "o/r", "pr", "comment", "7", "--body", "x", "-R", "o/r"); code != ExitTempFail || len(gh.writes()) != 1 {
		t.Fatalf("the entry crossed repositories: code %d, writes %v", code, gh.writes())
	}
	before := len(pending(t, b))

	// Read withdrawn for o/other only: in scope, not readable.
	bindAuthorityScope(t, b, both, "", readAuthorityOn("o/r"))
	if code, msg := serveAuthorityOn(b, "o/r", comment...); code == 0 || len(gh.writes()) != 1 || len(pending(t, b)) != before {
		t.Fatalf("an entry outranked the read floor: code %d, writes %v: %s", code, gh.writes(), msg)
	}
	// Out of scope entirely.
	bindAuthorityScope(t, b, []string{"o/r"}, "", readAuthorityOn("o/r"))
	if code, msg := serveAuthorityOn(b, "o/r", comment...); code == 0 || len(gh.writes()) != 1 {
		t.Fatalf("an entry outranked the scope: code %d, writes %v: %s", code, gh.writes(), msg)
	}
	if e, live, err := b.store.LiveWorkingKind(b.workspace, "github.com/o/other", "#7", brokerstore.WorkingPR); err != nil || !live || e.ID != lasting.ID {
		t.Fatalf("a scope change ended the entry: %+v live=%v err=%v", e, live, err)
	}
	// Back in scope with its read: the entry covers again.
	bindAuthorityScope(t, b, both, "", readAuthorityOn("o/r"), readAuthorityOn("o/other"))
	if code, msg := serveAuthorityOn(b, "o/r", comment...); code != 0 || len(gh.writes()) != 2 {
		t.Fatalf("a restored scope did not restore coverage: code %d, writes %v: %s", code, gh.writes(), msg)
	}
}

// GW-D80: a runs grant the gate approved as a pattern covers the dispatch and the re-run of every
// repository its owner holds, each against that repository's own default branch, and nothing
// another owner holds. Only a marked grant does; the unmarked one an older build recorded stays
// inert through the commands, as the matcher pins it.
func TestAPatternRunsGrantCoversDispatchAndRerunOfItsOwnersRepositories(t *testing.T) {
	pattern := brokerscope.Grant{Kind: GrantRuns, Repo: "github.com/o/*", Workflow: brokerscope.AnyWorkflow, RepoPattern: true}
	legacy := brokerscope.Grant{Kind: GrantRuns, Repo: "github.com/o/*", Workflow: brokerscope.AnyWorkflow}
	for _, c := range []struct {
		name    string
		repos   []string
		reads   []brokerscope.Grant
		grant   brokerscope.Grant
		argv    []string
		covered bool
	}{
		{"dispatch on another repository of the owner", []string{"o/r", "o/other"},
			[]brokerscope.Grant{readAuthorityOn("o/r"), readAuthorityOn("o/other")}, pattern,
			[]string{"workflow", "run", "ci.yml", "-R", "o/other"}, true},
		{"re-run on another repository of the owner", []string{"o/r", "o/other"},
			[]brokerscope.Grant{readAuthorityOn("o/r"), readAuthorityOn("o/other")}, pattern,
			[]string{"run", "rerun", "55", "-R", "o/other"}, true},
		{"dispatch on a repository of another owner", []string{"o/r", "elsewhere/x"},
			[]brokerscope.Grant{readAuthorityOn("o/r"), readAuthorityOn("elsewhere/x")}, pattern,
			[]string{"workflow", "run", "ci.yml", "-R", "elsewhere/x"}, false},
		{"dispatch without a read grant on the repository", []string{"o/r", "o/other"},
			[]brokerscope.Grant{readAuthorityOn("o/r")}, pattern,
			[]string{"workflow", "run", "ci.yml", "-R", "o/other"}, false},
		{"dispatch under the unmarked grant an older build recorded", []string{"o/r", "o/other"},
			[]brokerscope.Grant{readAuthorityOn("o/r"), readAuthorityOn("o/other")}, legacy,
			[]string{"workflow", "run", "ci.yml", "-R", "o/other"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, _ := windowFixture(t)
			gh := &authorityGH{}
			b.execute = gh.execute
			bindAuthorityScope(t, b, c.repos, "'runs'", append(append([]brokerscope.Grant(nil), c.reads...), c.grant)...)
			code, msg := serveAuthorityOn(b, "o/r", c.argv...)
			if c.covered && (code != 0 || len(gh.writes()) != 1) {
				t.Fatalf("not covered: code %d, writes %v: %s", code, gh.writes(), msg)
			}
			if !c.covered && (code == 0 || len(gh.writes()) != 0) {
				t.Fatalf("covered: code %d, writes %v: %s", code, gh.writes(), msg)
			}
			if c.covered && c.argv[0] == "workflow" && !slicesContains(gh.writes()[0], "--ref=refs/heads/main") {
				t.Fatalf("the dispatch did not run on the repository's own default branch: %v", gh.writes()[0])
			}
		})
	}
}
