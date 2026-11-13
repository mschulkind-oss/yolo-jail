package cli

// requestskipped_test.go pins, through the commands a user runs, the report of a project broker
// request outside the user's ceiling (docs/reference/config/user-config.md#what-a-jail-may-do-with-your-logins): `yolo check` names it with
// `yolo allow github on_request <capability>` alone on its line and `yolo config apply` after it,
// and that allow line is a command `yolo allow` accepts. Before the fix `yolo check` said
// nothing, and the owner of this repository approved the request at the gate unaware that it
// did nothing.

import (
	"strings"
	"testing"
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

func TestCheckNamesTheAllowCommandForABrokerRequestOutsideTheCeiling(t *testing.T) {
	newNamedVerbFixture(t, skippedGitHubRequestsTOML, "[allow.github]\nalways = [\"read\"]\n")
	_, out, errs := captureStd(t, func() int { return registry["check"]([]string{"check", "--no-build"}) })
	all := out + errs
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
			fx := newNamedVerbFixture(t, skippedGitHubRequestsTOML, "[allow.github]\nalways = [\"read\"]\n")
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
