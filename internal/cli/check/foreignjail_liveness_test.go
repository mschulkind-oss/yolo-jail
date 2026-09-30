package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// foreignjail_liveness_test.go: the per-jail liveness section grades ANOTHER workspace's jail
// only on what that jail published.
//
// The section builds its list of loopholes from THIS process's packs and THIS workspace's
// config, then walks every running yolo jail on the machine. A jail of another workspace,
// whose config never selected one of those loopholes, publishes nothing for it, and was
// graded "[FAIL] loophole <name> @ <jail>: no endpoint published" with the advice to restart
// a jail that would publish nothing after the restart either. Reproduced 2026-09-29 as a
// flaky TestGitHubBrokerRunsAReadAgainstTheHostGH whenever another suite's jail was up.
//
// The three halves below are the rule: an ABSENT file on a foreign jail is a [SKIP]; a
// PRESENT file on a foreign jail is still probed and still graded; this workspace's own jail
// is graded exactly as before.

// foreignJailName is the other workspace's jail in every fixture here.
const foreignJailName = "yolo-foreign-0badc0de"

// foreignJailWorkspace is what the fake runtime's `inspect` says that jail was launched from.
const foreignJailWorkspace = "/elsewhere/workspace-b"

// livenessFixture installs one loophole of each probe branch the section has — the Claude
// broker's front, a loopback-TLS service and an AF_UNIX socket — each enabled and each with a
// host daemon, so each is on the list the section walks.
func livenessFixture(t *testing.T) {
	t.Helper()
	moduleRoot := isolatedModuleDir(t)
	writeLoopholeManifest(t, moduleRoot, brokerLoopholeName,
		`"name":"`+brokerLoopholeName+`","description":"x","transport":"loopback-tls",`+
			`"default_enabled":true,"host_daemon":{"cmd":["true"],"publishes":"socket","scope":"host"}`)
	writeLoopholeManifest(t, moduleRoot, "svc",
		`"name":"svc","description":"x","transport":"loopback-tls","default_enabled":true,`+
			`"host_daemon":{"cmd":["true"],"publishes":"socket"}`)
	writeLoopholeManifest(t, moduleRoot, "unixsvc",
		`"name":"unixsvc","description":"x","transport":"none","default_enabled":true,`+
			`"host_daemon":{"cmd":["true"],"publishes":"socket"}`)
}

// servicesDirFor makes cname's host-services directory, empty, the way a launch leaves it
// before any service publishes, and removes it afterwards.
func servicesDirFor(t *testing.T, cname string) string {
	t.Helper()
	dir := paths.HostServicesDir(cname, false)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// runLiveness runs the section for workspace against a fake podman whose `ps` lists running
// and whose `inspect` of the foreign jail answers with its workspace when knowsForeign is set.
func runLiveness(t *testing.T, workspace string, knowsForeign bool, running ...string) (*reporter, string) {
	t.Helper()
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{
		Workspace: workspace,
		// Empty for every key, so inJail() is false: this suite runs inside a jail, where the
		// real YOLO_VERSION would send the section down its skip branch.
		Getenv:   func(string) string { return "" },
		LookPath: func(name string) (string, bool) { return "/bin/" + name, name == "podman" },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if len(argv) > 1 && argv[0] == "podman" && argv[1] == "ps" {
				return ExecResult{Ran: true, RC: 0, Stdout: strings.Join(running, "\n") + "\n"}
			}
			if knowsForeign && len(argv) > 2 && argv[0] == "podman" && argv[1] == "inspect" &&
				argv[2] == foreignJailName {
				return ExecResult{Ran: true, RC: 0, Stdout: "PATH=/bin\nYOLO_HOST_DIR=" + foreignJailWorkspace + "\n"}
			}
			return ExecResult{Ran: true, RC: 125, Stderr: "no such container"}
		},
	}
	fillDefaults(o)
	o.checkHostServiceLiveness(r)
	return r, buf.String()
}

// TestHostServiceLivenessSkipsWhatAnotherWorkspacesJailDidNotPublish is the defect itself: a
// foreign jail with its services directory in place and nothing in it.
func TestHostServiceLivenessSkipsWhatAnotherWorkspacesJailDidNotPublish(t *testing.T) {
	for _, tc := range []struct {
		name         string
		knowsForeign bool
		// want is the parenthetical each row ends with.
		want string
	}{
		{"workspace known", true, "(another workspace's jail: " + foreignJailWorkspace + ")"},
		{"workspace unknown", false, "(another workspace's jail)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			livenessFixture(t)
			servicesDirFor(t, foreignJailName)

			r, out := runLiveness(t, t.TempDir(), tc.knowsForeign, foreignJailName)

			if r.failed != 0 {
				t.Errorf("failed = %d: a jail of another workspace was graded against this "+
					"workspace's loopholes, and told to restart over services its own config "+
					"never selected:\n%s", r.failed, out)
			}
			for _, name := range []string{brokerLoopholeName, "svc", "unixsvc"} {
				want := "[SKIP] loophole " + name + " @ " + foreignJailName +
					": not graded from this workspace " + tc.want
				if !strings.Contains(out, want) {
					t.Errorf("no row %q:\n%s", want, out)
				}
			}
			if r.skipped != 3 {
				t.Errorf("skipped = %d, want one row per loophole: %s", r.skipped, out)
			}
			if r.passed != 0 {
				t.Errorf("passed = %d: a probe that did not look must not be counted as a pass:\n%s",
					r.passed, out)
			}
			if !strings.Contains(out, "yolo check") || !strings.Contains(out, "that workspace") {
				t.Errorf("the skip does not say where the jail can be graded instead:\n%s", out)
			}
			if strings.Contains(out, "unknown") {
				t.Errorf("the row printed the lookup's placeholder rather than leaving it out:\n%s", out)
			}
		})
	}
}

// TestHostServiceLivenessStillProbesWhatAnotherWorkspacesJailPublished: the skip is for an
// ABSENCE only. A file a foreign jail did publish is that jail's own service, and a broken one
// is a finding whichever workspace the check runs in.
func TestHostServiceLivenessStillProbesWhatAnotherWorkspacesJailPublished(t *testing.T) {
	livenessFixture(t)
	dir := servicesDirFor(t, foreignJailName)
	for _, name := range []string{brokerLoopholeName, "svc"} {
		if err := os.WriteFile(filepath.Join(dir, name+paths.ServiceEndpointExt),
			[]byte("not an endpoint\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A regular file where the socket goes: published, and not accepting.
	if err := os.WriteFile(filepath.Join(dir, "unixsvc.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	r, out := runLiveness(t, t.TempDir(), true, foreignJailName)

	for _, want := range []string{
		"[FAIL] loophole " + brokerLoopholeName + " @ " + foreignJailName + ": broker endpoint incomplete",
		"[FAIL] loophole svc @ " + foreignJailName + ": endpoint file incomplete",
		"[FAIL] loophole unixsvc @ " + foreignJailName + ": socket dead",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a published file of another workspace's jail was not probed, want %q:\n%s", want, out)
		}
	}
	if r.failed != 3 || r.skipped != 0 {
		t.Errorf("failed=%d skipped=%d, want every published file graded:\n%s", r.failed, r.skipped, out)
	}
}

// TestHostServiceLivenessStillFailsThisWorkspacesOwnMissingEndpoint: this workspace's own
// jail selected these loopholes, so an absence there is exactly the fault the section exists
// to report — graded as before, beside a foreign jail's skip in the same listing.
func TestHostServiceLivenessStillFailsThisWorkspacesOwnMissingEndpoint(t *testing.T) {
	livenessFixture(t)
	ws := t.TempDir()
	own := runtime.FromWorkspace(ws)
	ownDir := servicesDirFor(t, own)
	servicesDirFor(t, foreignJailName)

	r, out := runLiveness(t, ws, true, own, foreignJailName)

	for _, want := range []string{
		"[FAIL] loophole " + brokerLoopholeName + " @ " + own + ": broker endpoint missing",
		"[FAIL] loophole svc @ " + own + ": no endpoint published",
		"[FAIL] loophole unixsvc @ " + own + ": no socket",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("this workspace's own jail was not graded, want %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, ownDir) {
		t.Errorf("the own jail's rows do not name the file they looked for:\n%s", out)
	}
	if strings.Contains(out, "@ "+own+": not graded") {
		t.Errorf("this workspace's own jail was skipped as foreign:\n%s", out)
	}
	if r.failed != 3 || r.skipped != 3 {
		t.Errorf("failed=%d skipped=%d, want the own jail's three FAILs and the foreign jail's "+
			"three SKIPs:\n%s", r.failed, r.skipped, out)
	}
}
