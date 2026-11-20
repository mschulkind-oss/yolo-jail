package cli

// requestskipped_test.go pins, through the commands a user runs, the report of a project broker
// request outside the user's ceiling (docs/reference/config/user-config.md#what-a-jail-may-do-with-your-logins): `yolo check` names it with
// `yolo allow github on_request <capability>` alone on its line and `yolo config apply` after it,
// and that allow line is a command `yolo allow` accepts. Before the fix `yolo check` said
// nothing, and the owner of this repository approved the request at the gate unaware that it
// did nothing.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const skippedGitHubRequestsTOML = "[requests.github]\n" +
	"runs = [\"release-request.yml\", \"nightly-macos.yml\"]\n" +
	"approve_runs = [\"github-actions[bot]\"]\n"

// lineAlone reports whether some line of out is want and nothing else, its indent aside.
func lineAlone(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func newBrokerAdviceFixture(t *testing.T) namedVerbFixture {
	t.Helper()
	fx := newNamedVerbFixture(t, skippedGitHubRequestsTOML, "[allow.github]\nalways = [\"read\"]\n")
	t.Setenv("YOLO_SETUP", "podman-self")
	bin := filepath.Join(os.Getenv("HOME"), "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(bin, "queries")
	writeBrokerAdviceQueryAdapter(t, bin, log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		b, err := os.ReadFile(log)
		if err != nil {
			t.Errorf("owned broker advice query log: %v", err)
			return
		}
		const want = "version\ninfo\n"
		if string(b) != want {
			t.Errorf("owned broker advice queries = %q, want %q", b, want)
		}
		t.Logf("owned broker advice queries: %s", strings.TrimSpace(string(b)))
	})
	return fx
}

func writeBrokerAdviceQueryAdapter(t *testing.T, bin, log string) {
	t.Helper()
	// Only the check's version and readiness queries have synthetic answers;
	// every other doctor finding still comes from the normal guarded check.
	body := "#!/bin/sh\nlog=" + shquote.Quote(log) + `
if [ "$#" = 1 ] && [ "$1" = --version ]; then
  printf 'version\n' >> "$log"
  printf 'podman broker-advice fixture\n'
  exit 0
fi
if [ "$#" = 3 ] && [ "$1" = info ] && [ "$2" = --format ] && [ "$3" = json ]; then
  printf 'info\n' >> "$log"
  printf '{}\n'
  exit 0
fi
printf 'unexpected\n' >> "$log"
printf 'permission denied: broker advice fixture refuses unsupported runtime query\n' >&2
exit 97
`
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerAdviceQueryAdapterRefusesUnownedShapes(t *testing.T) {
	for _, args := range [][]string{{"--version", "extra"}, {"info"}, {"info", "--format", "yaml"}, {"run", "fixture"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			bin := t.TempDir()
			log := filepath.Join(bin, "queries")
			writeBrokerAdviceQueryAdapter(t, bin, log)
			out, err := exec.Command(filepath.Join(bin, "podman"), args...).CombinedOutput()
			failure, ok := err.(*exec.ExitError)
			if !ok || failure.ExitCode() != 97 || !strings.Contains(string(out), "fixture refuses unsupported runtime query") {
				t.Fatalf("unowned query %q: error=%v output=%q, want fixture refusal97", args, err, out)
			}
			if got := readFile(t, log); got != "unexpected\n" {
				t.Fatalf("unowned query log=%q, want one refusal", got)
			}
		})
	}
}

func TestBrokerAdviceFixtureOwnsRuntimeQueries(t *testing.T) {
	canary := t.TempDir()
	log := filepath.Join(canary, "escaped")
	body := "#!/bin/sh\nprintf 'escaped\\n' >> " + shquote.Quote(log) + "\nprintf 'permission denied: forbidden broker advice query\\n' >&2\nexit 97\n"
	if err := os.WriteFile(filepath.Join(canary, "podman"), []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", canary+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("YOLO_SETUP", "podman-self")
	newBrokerAdviceFixture(t)
	_, out, errs := captureStd(t, func() int { return registry["check"]([]string{"check", "--no-build"}) })
	assertSkippedBrokerRequestAdvice(t, out+errs)
	if b, err := os.ReadFile(log); !os.IsNotExist(err) {
		t.Fatalf("broker advice fixture escaped its owned runtime query adapter: %q (%v)", b, err)
	}
}

func TestCheckNamesTheAllowCommandForABrokerRequestOutsideTheCeiling(t *testing.T) {
	newBrokerAdviceFixture(t)
	_, out, errs := captureStd(t, func() int { return registry["check"]([]string{"check", "--no-build"}) })
	assertSkippedBrokerRequestAdvice(t, out+errs)
}

func assertSkippedBrokerRequestAdvice(t *testing.T, all string) {
	t.Helper()
	for _, want := range []string{
		"yolo allow github on_request runs",
		"yolo allow github on_request approve_runs",
		"yolo config apply",
	} {
		if !lineAlone(all, want) {
			t.Fatalf("yolo check lacks the line %q:\n%s", want, all)
		}
	}
	if !strings.Contains(all, "this project requests github runs release-request.yml, nightly-macos.yml.") {
		t.Fatalf("yolo check does not name the request:\n%s", all)
	}
}

// The command the report names is one `yolo allow` takes, for each capability with a value.
func TestTheNamedAllowCommandIsAccepted(t *testing.T) {
	for _, capability := range []string{"runs", "approve_runs"} {
		t.Run(capability, func(t *testing.T) {
			fx := newBrokerAdviceFixture(t)
			rec := recordAllowGate(t, false)
			rc, out, errs := captureStd(t, func() int {
				return registry["allow"]([]string{"allow", "github", "on_request", capability})
			})
			if rc != 0 {
				t.Fatalf("yolo allow github on_request %s rc=%d stdout=%q stderr=%q", capability, rc, out, errs)
			}
			if got := readFile(t, fx.fragment); !strings.Contains(got, "on_request = [\""+capability+"\"]") {
				t.Fatalf("on_request was not written:\n%s", got)
			}
			if rec.calls != 0 {
				t.Fatalf("a gate with no terminal was run")
			}
			_, out, errs = captureStd(t, func() int { return registry["check"]([]string{"check", "--no-build"}) })
			if lineAlone(out+errs, "yolo allow github on_request "+capability) {
				t.Fatalf("an allowed request is still reported skipped:\n%s", out+errs)
			}
		})
	}
}
