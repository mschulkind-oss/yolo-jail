package entrypoint

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// miseSessionEnv is a session's environment with its two mise session inputs.
func miseSessionEnv(t *testing.T, tools, disabled string) *Env {
	t.Helper()
	home := t.TempDir()
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_MISE_TOOLS": tools, "MISE_DISABLE_TOOLS": disabled})
	e.Home = home
	return e
}

// THE PROVISIONER RECORDS WHAT IT INSTALLED (live-config-apply.md LA-D20): the first session's
// stage ran the jail's mise install from its own session inputs, so a later session with the same
// inputs runs nothing. Deleting the record in provisionThisSession fails here, since the later
// session then installs again.
func TestALaterSessionWithTheProvisionersMiseToolsInstallsNothing(t *testing.T) {
	withJailMainDir(t)
	announceReady("true", &bytes.Buffer{})
	g := newSessionGate(false, &bytes.Buffer{})
	if p, err := g.claim(); err != nil || !p {
		t.Fatalf("claim: %v %v", p, err)
	}
	first := miseSessionEnv(t, `{"jq":"1.7.1"}`, "pnpm")
	if rc := provisionThisSession(first, g, func(*Env, string) int { return 0 }); rc != 0 {
		t.Fatalf("provisioning rc = %d", rc)
	}
	var ran []string
	run := func(_ *Env, stage string) int { ran = append(ran, stage); return 0 }
	if rc := reprovisionMise(miseSessionEnv(t, `{"jq":"1.7.1"}`, "pnpm"), run, &bytes.Buffer{}); rc != 0 || len(ran) != 0 {
		t.Errorf("a session with the provisioner's mise inputs ran %q (rc %d), want nothing", ran, rc)
	}
}

// A CHANGED SESSION INPUT IS INSTALLED ON THE SESSION PASS, ONCE (LA-S4a): the session that first
// carries it runs the jail's mise install on its terminal and records it, and the next session
// with the same inputs runs nothing.
func TestASessionWithChangedMiseToolsInstallsThemOnce(t *testing.T) {
	withJailMainDir(t)
	if err := writeMainState(miseToolsRecordFile, miseSessionKey(miseSessionEnv(t, `{"jq":"1.7.1"}`, "pnpm"))); err != nil {
		t.Fatal(err)
	}
	var ran []string
	run := func(_ *Env, stage string) int { ran = append(ran, stage); return 0 }
	var said bytes.Buffer
	if rc := reprovisionMise(miseSessionEnv(t, `{"jq":"1.8.0"}`, "pnpm"), run, &said); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	want := provision.SetupBypassingShims(provision.StepAnnounceMiseInstall, provision.StepMiseInstall)
	if len(ran) != 1 || ran[0] != want {
		t.Fatalf("ran %q, want the one mise install stage %q", ran, want)
	}
	if !strings.Contains(said.String(), "mise") {
		t.Errorf("the session was not told why it installs: %q", said.String())
	}
	if rc := reprovisionMise(miseSessionEnv(t, `{"jq":"1.8.0"}`, "pnpm"), run, &bytes.Buffer{}); rc != 0 || len(ran) != 1 {
		t.Errorf("the next session with the same inputs ran the install again: %q", ran)
	}
	// MISE_DISABLE_TOOLS is half of the inputs: lifting pnpm out of it installs too.
	if rc := reprovisionMise(miseSessionEnv(t, `{"jq":"1.8.0"}`, ""), run, &bytes.Buffer{}); rc != 0 || len(ran) != 2 {
		t.Errorf("a changed MISE_DISABLE_TOOLS did not install: %q", ran)
	}
}

// A FAILED INSTALL IS RECORDED AS NOTHING and names the next step: the next session tries again,
// and the session itself still enters (reprovisionMise's status is reported, never the session's).
func TestAFailedSessionMiseInstallIsRetriedByTheNextSession(t *testing.T) {
	withJailMainDir(t)
	var ran int
	fail := func(*Env, string) int { ran++; return 7 }
	var said bytes.Buffer
	if rc := reprovisionMise(miseSessionEnv(t, `{"jq":"1.8.0"}`, ""), fail, &said); rc != 7 {
		t.Fatalf("rc = %d, want the stage's 7", rc)
	}
	if !strings.Contains(said.String(), "mise install") || !strings.Contains(said.String(), "next") {
		t.Errorf("the failure does not name the next step: %q", said.String())
	}
	if _, ok := readMainState(miseToolsRecordFile); ok {
		t.Error("a failed install was recorded as installed")
	}
	if reprovisionMise(miseSessionEnv(t, `{"jq":"1.8.0"}`, ""), fail, &bytes.Buffer{}); ran != 2 {
		t.Errorf("the next session did not try again (ran %d)", ran)
	}
}

// TestMainRunsTheSessionMiseInstallForEveryOtherSession pins Main's call site, which no test can
// execute (the session ends in an exec): a session of a hold-main jail that did not provision runs
// reprovisionMise after its pass and before its exec, and its status never ends the session.
func TestMainRunsTheSessionMiseInstallForEveryOtherSession(t *testing.T) {
	src, err := os.ReadFile("boot.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	body = body[strings.Index(body, "func Main(args []string) error {"):]
	prov := strings.Index(body, "provisionThisSession(e, gate, runProvisionStage)")
	branch := strings.Index(body, "} else if gate != nil {")
	call := strings.Index(body, "\t\treprovisionMise(e, runProvisionStage, os.Stderr)\n")
	exec := strings.Index(body, "return execBash(e, command, mode != modeFirstSession)")
	if prov < 0 || branch < prov || call < branch || exec < call {
		t.Errorf("Main does not run reprovisionMise for a non-provisioning session between the stage and the exec (stage %d, branch %d, call %d, exec %d)", prov, branch, call, exec)
	}
}
