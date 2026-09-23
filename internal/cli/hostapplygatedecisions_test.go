package cli

// hostapplygatedecisions_test.go pins the launch hook's rule for an apply that would ASK
// something: the hook never runs it. Its auto-apply's report is buffered, so a question asked
// there is one the user cannot see — measured at HEAD 790a814f as a launch that hung after the
// banner on a TTY (Enter meant no; a blind `y` moved a skill into the local pack), a silent "no"
// off one followed by "synchronized host configuration (~/.claude/skills/myskill)" on every
// launch, and a declined install of ANOTHER pack's missing binary refusing claude's launch.
//
// Each test drives the hook with a stdin that records any read and would answer `y`, so "the
// hook asked nothing" is asserted rather than inferred from the outcome.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// yesTrap is a stdin that answers `y` and records that it was read. A hook that reads it has
// put a question to the user through a buffer they cannot see.
type yesTrap struct{ read bool }

func (r *yesTrap) Read(p []byte) (int, error) {
	r.read = true
	return copy(p, "y\n"), nil
}

// adoptionHome is a settled home (one --assert applied) into which the user has since put a
// skill of their own, under the directory the claude pack composes. An --assert would ask to
// move it into the local pack.
func adoptionHome(t *testing.T) (home, skill string) {
	t.Helper()
	home = gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	skill = filepath.Join(home, ".claude", "skills", "myskill", "SKILL.md")
	writeFile(t, skill, "---\nname: myskill\ndescription: mine\n---\nMy own skill.\n")
	if lock := tryHostApplyLock(home); lock != nil {
		lock.Close() // the hook's lock is not a render; take it once so the hash ignores it
	}
	// The fixture is only a fixture if the apply really would ask.
	survey := &hostApplySurvey{}
	var sink bytes.Buffer
	applyHostSurveyed(&sink, &sink, false, false, nil, survey)
	if len(survey.PendingDecisions()) == 0 || !survey.Changes() {
		t.Fatalf("fixture bug: the user's skill should make an --assert ask about adoption:\n%s",
			sink.String())
	}
	return home, skill
}

// THE MEASURED HANG AND THE MEASURED BLIND `y`, on a TTY and off one: nothing is rendered,
// nothing is read from stdin, the skill stays where the user put it, and the question is printed
// with the command that asks it properly.
func TestHostApplyGateNeverAsksThroughItsBuffer(t *testing.T) {
	for _, tty := range []bool{true, false} {
		name := "no-tty"
		if tty {
			name = "tty"
		}
		t.Run(name, func(t *testing.T) {
			home, skill := adoptionHome(t)
			setGateTTY(t, tty)
			before := hashTree(t, home)

			stdin := &yesTrap{}
			var errw bytes.Buffer
			if !hostApplyGate(&errw, stdin, "claude") {
				t.Fatalf("an apply needing a decision must not stop the launch:\n%s", errw.String())
			}
			report := errw.String()
			if stdin.read {
				t.Errorf("the hook read stdin — a question the user cannot see:\n%s", report)
			}
			if _, err := os.Stat(skill); err != nil {
				t.Errorf("the user's skill was moved: %v\n%s", err, report)
			}
			if hashTree(t, home) != before {
				t.Errorf("the hook rendered an apply that needed a decision:\n%s", report)
			}
			for _, want := range []string{"did not render", "local pack", "yolo host apply --assert"} {
				if !strings.Contains(report, want) {
					t.Errorf("the hook must say %q:\n%s", want, report)
				}
			}
			if strings.Contains(report, "synchronized") {
				t.Errorf("the hook reported a synchronization that did not happen:\n%s", report)
			}
		})
	}
}

// ANOTHER PACK'S MISSING DEPENDENCY DOES NOT REFUSE THIS LAUNCH. A pack alongside claude
// declares a binary this host lacks; the launch of claude proceeds (rc 127 from the PATH lookup
// for the nonexistent binary below, not rc 1 from the hook), nothing is rendered, and the report
// names the binary. Driven through hostMain, so the call site is pinned too.
func TestHostExecLaunchesOverAnotherPacksMissingDependency(t *testing.T) {
	home := gateFixture(t, true)
	needy := filepath.Join(t.TempDir(), "needy")
	writeFile(t, filepath.Join(needy, "pack.json"), `{"name":"needy","contributes":[
	  {"kind":"requires","bin":"yolo-test-absent-bin","install_hints":{"brew":"x","apt":"x"}}]}`)
	selectPacksWith(t, home, `"claude",{"source":"file://`+needy+`","name":"needy"}`,
		`,"host_apply_on_launch":true`)
	if lock := tryHostApplyLock(home); lock != nil {
		lock.Close()
	}
	before := hashTree(t, home)

	stdin := &yesTrap{}
	var out, errw bytes.Buffer
	rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, stdin)
	report := out.String() + errw.String()
	if rc != 127 {
		t.Fatalf("rc = %d, want 127 — another pack's missing dependency refused this launch\n%s",
			rc, report)
	}
	if stdin.read {
		t.Errorf("the hook asked about an install through a buffer:\n%s", report)
	}
	if hashTree(t, home) != before {
		t.Errorf("the hook rendered past a missing dependency:\n%s", report)
	}
	for _, want := range []string{"did not render", "yolo-test-absent-bin", "yolo host apply --assert"} {
		if !strings.Contains(report, want) {
			t.Errorf("the hook must say %q:\n%s", want, report)
		}
	}
}

// THE DEFENSE BEHIND THE PRE-CHECK. Should the observe pass ever miss a question, the buffered
// apply reads noPromptStdin — never the user's terminal — which answers no; and the hook says so
// instead of claiming a synchronization. Called directly, bypassing the pre-check that would
// otherwise stop this fixture first.
func TestHostApplyGateApplyNeverClaimsDeclinedWork(t *testing.T) {
	home, skill := adoptionHome(t)
	var errw bytes.Buffer
	if !hostApplyGateApply(&errw, "claude", home) {
		t.Fatalf("a declined question is not a failed apply:\n%s", errw.String())
	}
	report := errw.String()
	if strings.Contains(report, "synchronized") {
		t.Errorf("reported a synchronization for work that was declined:\n%s", report)
	}
	if !strings.Contains(report, "asked a question") {
		t.Errorf("the missed question must be reported:\n%s", report)
	}
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("the user's skill was moved on an unanswered question: %v", err)
	}
}

// "synchronized" names what the apply CHANGED: one hand-edited managed key, one file.
func TestHostApplyGateSynchronizedNamesTheChangedFile(t *testing.T) {
	home := gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	driftTheHome(t, home)
	var errw bytes.Buffer
	if !hostApplyGate(&errw, &yesTrap{}, "claude") {
		t.Fatalf("drift must auto-apply and launch:\n%s", errw.String())
	}
	if want := "synchronized host configuration (~/.claude/settings.json)"; !strings.Contains(errw.String(), want) {
		t.Errorf("want %q:\n%s", want, errw.String())
	}
}
