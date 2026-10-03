package ghbroker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The `gh api` endpoint rule for percent-encoding (docs/design/boundary-broker.md BB-D58).
// gh sends an endpoint's encoding as the jail spelled it (MEASURED against gh 2.101.0 and a
// local fake API: `repos/o/r/branches/MS%2Fmain` arrives as `/repos/o/r/branches/MS%2Fmain`),
// so the broker decodes the path once, checks the decoded path against the scope, and
// forwards the endpoint unchanged. An encoding is refused only where some server's reading
// of it could name a path outside the repository the broker checked; every such refusal
// names what to write instead.

// nextSteps are the words every refusal here offers its next step with.
var nextSteps = []string{"Spell", "Write", "Encode", "Remove", "Name", "read it with"}

func hasNextStep(reason string) bool {
	for _, s := range nextSteps {
		if strings.Contains(reason, s) {
			return true
		}
	}
	return false
}

func TestAPIEncodedEndpoints(t *testing.T) {
	for _, c := range []struct {
		endpoint string
		run      string // "" when refused
		reason   string // a substring of the refusal
	}{
		// The standard way to name a branch with a slash: decoding leaves repos/o/r alone.
		{endpoint: "repos/o/r/branches/MS%2Fmain", run: "repos/o/r/branches/MS%2Fmain"},
		{endpoint: "repos/o/r/branches/MS%2fmain", run: "repos/o/r/branches/MS%2fmain"},
		{endpoint: "/repos/o/r/branches/MS%2Fmain?per_page=1", run: "/repos/o/r/branches/MS%2Fmain?per_page=1"},
		{endpoint: "repos/o/r/contents/docs%2Fa%20b.md", run: "repos/o/r/contents/docs%2Fa%20b.md"},
		{endpoint: "repos/o/r/contents/a%23b%3F.md", run: "repos/o/r/contents/a%23b%3F.md"},
		{endpoint: "repos/o/r/git/ref/heads%2FMS%2Fmain", run: "repos/o/r/git/ref/heads%2FMS%2Fmain"},
		{endpoint: "repos/o/r/contents/%C3%A9t%C3%A9.md", run: "repos/o/r/contents/%C3%A9t%C3%A9.md"},
		{endpoint: "repos/o/r/pulls/", run: "repos/o/r/pulls/"},

		// Every way to move the call to another repository through an encoding.
		{endpoint: "repos/o%2Fother/r", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "repos/o/r%2F..%2F..%2Fother", reason: "dot segment"},
		{endpoint: "repos/o/r%2f..%2f..%2fother", reason: "dot segment"},
		{endpoint: "repos/%6F/r", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "%72epos/other/x", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "repos%2Fother%2Fx/pulls", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "repos/o%23x/r", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "repos/o/r%3F/x", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "repos/o/r/x%2F..%2F..%2F..%2Fother%2Fx", reason: "dot segment"},
		{endpoint: "repos/o/r/%2e%2e/%2e%2e/other/x", reason: "dot segment"},
		{endpoint: "repos/o/r/%2E%2E/%2e%2E/other/x", reason: "dot segment"},
		{endpoint: "repos/o/r/%2e/x", reason: "dot segment"},
		{endpoint: "repos/o/r/../../other/x", reason: "dot segment"},
		{endpoint: "repos/o/r/branches/MS%252Fmain", reason: "still encoded"},
		{endpoint: "repos/o/r/%252e%252e/%252e%252e/other/x", reason: "still encoded"},
		{endpoint: "repos/o/r/a%5C..%5C..%5Cother", reason: "backslash"},
		{endpoint: `repos/o/r/a\..\..\other`, reason: "backslash"},
		{endpoint: "repos/o/r/a%00b", reason: "control character"},
		{endpoint: "repos/o/r/a%0Ab", reason: "control character"},
		{endpoint: "repos/o/r/a%zz", reason: "two-digit hex escape"},
		{endpoint: "repos/o/r/a%2", reason: "two-digit hex escape"},
		{endpoint: "repos/o/r/x%2F%2Fy", reason: "empty segment"},
		{endpoint: "repos/o/r//x", reason: "empty segment"},
		{endpoint: "//repos/o/r", reason: "empty segment"},
		{endpoint: "repos/o/r/%c0%ae%c0%ae/x", reason: "not UTF-8"},
		{endpoint: "repos/o/r#/../../other/x", reason: "%23"},
		{endpoint: "repos/o/r/issues#x", reason: "%23"},
		{endpoint: "", reason: "empty"},
	} {
		t.Run(c.endpoint, func(t *testing.T) {
			d := Classify([]string{"api", c.endpoint}, "", testScope)
			if c.run != "" {
				if d.Outcome != OutcomeStanding || strings.Join(d.Argv, " ") != "api "+c.run {
					t.Fatalf("gh api %q: outcome %q (%s), runs %q; want a standing read of %q",
						c.endpoint, d.Outcome, d.Reason, d.Argv, c.run)
				}
				return
			}
			if d.Outcome != OutcomeRefused || d.Argv != nil {
				t.Fatalf("gh api %q: outcome %q argv %q (%s), want refused", c.endpoint, d.Outcome, d.Argv, d.Reason)
			}
			if !strings.Contains(d.Reason, c.reason) {
				t.Errorf("gh api %q: reason %q, want it to contain %q", c.endpoint, d.Reason, c.reason)
			}
			if !hasNextStep(d.Reason) {
				t.Errorf("gh api %q: the refusal names no next step: %q", c.endpoint, d.Reason)
			}
		})
	}
}

// The production path: a jail's `gh api` with an encoded branch reaches the host gh with
// the encoding it was given, and an encoding that names another repository runs nothing.
func TestServeForwardsAnEncodedEndpointAsGiven(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, out, errOut := f.serve(t, Request{Argv: []string{"api", "repos/{owner}/{repo}/branches/MS%2Fmain"}, Repo: "o/r"})
	if code != 0 || out != "ran api repos/o/r/branches/MS%2Fmain\n" {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	if got := oneCall(t, f.fakeDir)["argv"]; got != "api\nrepos/o/r/branches/MS%2Fmain\n" {
		t.Fatalf("the host gh received %q, want the endpoint's encoding unchanged", got)
	}
	if ev := lastAudit(t); ev.Outcome != "ran" || ev.Repo != "o/r" {
		t.Fatalf("audit %+v", ev)
	}

	if err := os.RemoveAll(filepath.Join(f.fakeDir, "calls")); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = f.serve(t, Request{Argv: []string{"api", "repos/o%2Fother/r"}})
	if code != ExitUsage || !strings.Contains(errOut, "encodes a character in its repos/OWNER/REPO") {
		t.Fatalf("an encoded owner: code %d err %q", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls")); len(entries) != 0 {
		t.Fatal("an encoded owner ran gh")
	}
}
