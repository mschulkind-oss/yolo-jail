package ghbroker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// identityGH answers the broker's own host reads for any repository it is given, and records every
// other command as a write, so a test can tell which identity a cancel was let through for.
type identityGH struct {
	mu   sync.Mutex
	runs map[string]string // "owner/repo run-id" -> the run's JSON
	ran  [][]string
}

func (f *identityGH) execute(_ context.Context, argv []string, _ []byte, out, _ func([]byte)) RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(argv) == 2 && argv[0] == "api" && strings.HasPrefix(argv[1], "repos/") {
		rest := strings.TrimPrefix(argv[1], "repos/")
		parts := strings.Split(rest, "/")
		if len(parts) == 2 {
			out([]byte(`{"full_name":"` + rest + `","default_branch":"main"}`))
			return RunResult{}
		}
		if len(parts) == 5 && parts[2] == "actions" && parts[3] == "runs" {
			if body, ok := f.runs[parts[0]+"/"+parts[1]+" "+parts[4]]; ok {
				out([]byte(body))
				return RunResult{}
			}
		}
		return RunResult{Exit: 1}
	}
	f.ran = append(f.ran, append([]string(nil), argv...))
	return RunResult{}
}

func (f *identityGH) writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ran)
}

// writeScopedCeilingUser writes the user config a broker reads at every call: the broad allow.github
// table, then the repos tables as written (GW-D64).
func writeScopedCeilingUser(t *testing.T, broad, repos string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "packs = ['github']\n\n[allow.github]\nalways = ['read']\n" + broad + "\n" + repos
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// scopedCeilingBroker is a broker whose scope file grants runs on both o/r and o/other, and whose
// gh has a run on each, so only the user's ceiling decides which identity a cancel may reach.
func scopedCeilingBroker(t *testing.T) (*Broker, *identityGH) {
	t.Helper()
	b, _ := windowFixture(t)
	gh := &identityGH{runs: map[string]string{
		"o/r run 42228358413":     runHeadJSON("42228358413", ".github/workflows/nightly-macos.yml", "main", "o/r"),
		"o/other run 42228358414": runHeadJSON("42228358414", ".github/workflows/nightly-macos.yml", "main", "o/other"),
	}}
	b.execute = gh.execute
	grants := append(append([]brokerscope.Grant(nil), readAll...),
		brokerscope.Grant{Kind: GrantRuns, Repo: "github.com/o/r", Workflow: "*"},
		brokerscope.Grant{Kind: GrantRuns, Repo: "github.com/o/other", Workflow: "*"})
	if err := brokerscope.ReplaceScopeContext(context.Background(), b.authority.path, b.authority.scope, []string{"o/r", "o/other"}, grants); err != nil {
		t.Fatal(err)
	}
	return b, gh
}

// serveRepo runs argv for the identity repo, the way the jail's gh shim reaches the broker.
func serveRepo(b *Broker, repo string, argv ...string) (int, string) {
	var errOut bytes.Buffer
	code := b.ServeContext(context.Background(), Request{Argv: argv, Repo: repo},
		paths.JailShortHash(b.authority.scope.Container), func([]byte) {}, func(p []byte) { errOut.Write(p) })
	return code, strings.Join(strings.Fields(errOut.String()), " ")
}

// A repos table's scoped ceiling reaches the broker per identity: a cancel on the repository its
// repo line admits goes through; the same alias on another identity, whose scope grant is held,
// does not (GW-D64). The broad allowance is untouched by the table.
func TestAScopedCeilingCoversItsIdentityAtTheBroker(t *testing.T) {
	const scoped = "\n[repos.jail]\nrepo = 'github.com/o/r'\n\n[repos.jail.allow.github]\non_request = ['runs']\n"
	t.Run("the admitted identity", func(t *testing.T) {
		b, gh := scopedCeilingBroker(t)
		writeScopedCeilingUser(t, "", scoped)
		if code, msg := serveRepo(b, "o/r", "run", "cancel", "42228358413"); code != 0 || gh.writes() != 1 {
			t.Fatalf("code %d, writes %d: %s", code, gh.writes(), msg)
		}
	})
	t.Run("another identity the scope also grants", func(t *testing.T) {
		b, gh := scopedCeilingBroker(t)
		writeScopedCeilingUser(t, "", scoped)
		if code, msg := serveRepo(b, "o/other", "run", "cancel", "42228358414"); code == 0 || gh.writes() != 0 {
			t.Fatalf("code %d, writes %d: %s", code, gh.writes(), msg)
		}
	})
	t.Run("the broad allowance covers every identity without a table", func(t *testing.T) {
		b, gh := scopedCeilingBroker(t)
		writeScopedCeilingUser(t, "on_request = ['runs']\n", "")
		if code, msg := serveRepo(b, "o/other", "run", "cancel", "42228358414"); code != 0 || gh.writes() != 1 {
			t.Fatalf("code %d, writes %d: %s", code, gh.writes(), msg)
		}
	})
}

// The user's table is read at every call: withdrawing it between the look at the grant and the
// transaction refuses the cancel, as it does for a broad allowance (workingset.go afterGrantLook).
func TestAScopedCeilingWithdrawnBetweenLookAndTransactionRefuses(t *testing.T) {
	b, gh := scopedCeilingBroker(t)
	writeScopedCeilingUser(t, "", "\n[repos.jail]\nrepo = 'github.com/o/r'\n\n[repos.jail.allow.github]\non_request = ['runs']\n")
	afterGrantLook = func() { writeScopedCeilingUser(t, "", "") }
	t.Cleanup(func() { afterGrantLook = nil })
	if code, msg := serveRepo(b, "o/r", "run", "cancel", "42228358413"); code == 0 || gh.writes() != 0 {
		t.Fatalf("code %d, writes %d: %s", code, gh.writes(), msg)
	}
}
