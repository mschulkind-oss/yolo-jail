package macosuser

// keepergrants_test.go pins the stage's two questions about the endpoint-file grants when a keeper
// holds the workspace's host services (docs/design/jail-lifetime-last-session-wins.md §9.9.5): every
// session of the workspace is told the keeper's one set of endpoint files, and what a second `chmod
// +a` of one ACE on one file does is unmeasured, so a session grants only a file no session granted
// yet (Options.SkipGrant), and records what its own stage named (Options.OnStaged).

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// A GRANT SkipGrant NAMES IS NOT RUN, every other one is, and OnStaged hears every path a grant
// named once the stage succeeded. Deleting the stage's SkipGrant check, or its OnStaged call, fails
// this.
func TestTheStageSkipsAGrantAnotherSessionMadeAndRecordsWhatItNamed(t *testing.T) {
	const file = "/private/tmp/yolo-host-services-x/claude-oauth-broker.endpoint"
	const dir = "/private/tmp/yolo-host-services-x"
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts(probeWS)
	o.PackEnv = brokerEndpointEnv()
	o.SkipGrant = func(p string) bool { return p == file }
	var staged []string
	o.OnStaged = func(paths []string) { staged = append(staged, paths...) }
	if rc := RunMacosUser(d, o); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	ran := strings.Join(rec, "\n")
	if strings.Contains(ran, "run:sudo "+chmodBin+" +a user:"+SandboxUser+" allow "+sandboxFileReadRights+" "+file) {
		t.Errorf("the stage granted %s, which SkipGrant said a session granted already:\n%s", file, ran)
	}
	if !strings.Contains(ran, "run:sudo "+chmodBin+" +a user:"+SandboxUser+" allow search "+dir) {
		t.Errorf("the stage skipped the grant on %s, which SkipGrant did not name:\n%s", dir, ran)
	}
	if !slices.Contains(staged, file) || !slices.Contains(staged, dir) {
		t.Errorf("OnStaged heard %v, want both %s and %s", staged, file, dir)
	}

	// With no SkipGrant, as on a launch with no keeper, every grant runs.
	rec = nil
	o.SkipGrant, o.OnStaged = nil, nil
	if rc := RunMacosUser(mockDeps(&rec), o); rc != 42 {
		t.Fatalf("rc = %d", rc)
	}
	if !strings.Contains(strings.Join(rec, "\n"), " allow "+sandboxFileReadRights+" "+file) {
		t.Errorf("a launch with no keeper did not grant %s:\n%s", file, strings.Join(rec, "\n"))
	}
}
