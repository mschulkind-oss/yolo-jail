package cli

// hostapplygate_test.go walks §4.3's table row by row, plus §4.4's two failure classes and
// §7's "in-jail is a hard no-op" (docs/design/host-apply-staleness.md).
//
// THE CALL SITE IS PINNED SEPARATELY, at the bottom, and it has to be: every test that calls
// hostApplyGate directly would still pass with the one line in hostExec deleted, which is
// AGENTS.md's callee-pinned-call-site-unpinned shape — the class this repo has shipped five
// times. TestHostExecRefusesAStaleHomeWithNoTerminal drives `hostMain` instead and distinguishes
// the gate's refusal (rc 1, with its text) from what happens without it (rc 127, from the
// PATH lookup for a binary that does not exist).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// gateFixture is a throwaway home with the opt-in ON, the shipped claude pack selected, and
// the environment neutralized: no TTY, no approval variable, not in a jail.
//
// YOLO_VERSION IS CLEARED DELIBERATELY. The suite itself runs inside a yolo jail, where it is
// set — so without this every gate test would exercise the in-jail no-op and assert nothing
// about the gate at all. TestHostApplyGateIsANoOpInAJail is the one test that puts it back.
func gateFixture(t *testing.T, keyOn bool) string {
	t.Helper()
	home := t.TempDir()
	cfg := `{"packs":["claude"],"host_apply_on_launch":true}`
	if !keyOn {
		cfg = `{"packs":["claude"]}`
	}
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(acceptConfigChangesEnv, "")
	setGateTTY(t, false)
	return home
}

// setGateTTY stands in for the terminal probe and restores it afterwards.
func setGateTTY(t *testing.T, tty bool) {
	t.Helper()
	prev := hostGateCanPrompt
	hostGateCanPrompt = func() bool { return tty }
	t.Cleanup(func() { hostGateCanPrompt = prev })
}

// driftTheHome hand-edits a managed key in a rendered surface and returns its path. This is
// §11's first scenario and OQ-HS9's whole argument: the CONFIG did not move, so only a
// comparison of the render can see it.
func driftTheHome(t *testing.T, home string) string {
	t.Helper()
	settings := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("fixture bug: %v", err)
	}
	edited := strings.Replace(string(data), `"autoUpdaterStatus": "disabled"`,
		`"autoUpdaterStatus": "enabled"`, 1)
	if edited == string(data) {
		t.Fatalf("fixture bug: no managed key to edit in %s:\n%s", settings, data)
	}
	if err := os.WriteFile(settings, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	return settings
}

// TestHostApplyGateIsSilentWithoutTheOptIn is the DEFAULT, and §11's fourth done-condition: no
// launch and no command mentions any of this.
//
// The home is left deliberately unapplied, so there is maximal drift for the gate to find. It
// must still say nothing: the key is what makes the mechanism exist.
func TestHostApplyGateIsSilentWithoutTheOptIn(t *testing.T) {
	gateFixture(t, false)
	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Error("the gate stopped a launch with the key off — it is opt-in")
	}
	if errw.Len() != 0 {
		t.Errorf("the gate printed something with the key off:\n%s", errw.String())
	}
}

// TestHostApplyGateIsSilentOnAFreshlyAppliedHome is R3 at the gate, the design's
// highest-consequence failure: *"launching prompts not at all, ever, until something actually
// changes."*
func TestHostApplyGateIsSilentOnAFreshlyAppliedHome(t *testing.T) {
	gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	// No TTY and no approval — the strictest row in the table. A settled home must sail
	// straight through it, or every scripted launch on the machine breaks.
	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Errorf("a freshly-applied home REFUSED a launch (R3):\n%s", errw.String())
	}
	if errw.Len() != 0 {
		t.Errorf("a settled home must be silent:\n%s", errw.String())
	}
}

// TestHostApplyGatePromptsAndAppliesOnATTY is §4.3 row 2, accept half — §11's first scenario
// end to end: hand-edit, launch, the change appears, accept, the file is restored.
func TestHostApplyGatePromptsAndAppliesOnATTY(t *testing.T) {
	home := gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	settings := driftTheHome(t, home)
	setGateTTY(t, true)

	var errw bytes.Buffer
	if !hostApplyGate(&errw, strings.NewReader("y\n"), "claude") {
		t.Fatalf("an accepted prompt must let the launch proceed:\n%s", errw.String())
	}
	report := errw.String()
	if !strings.Contains(report, settings) {
		t.Errorf("the prompt must name the destination that would change:\n%s", report)
	}
	if !strings.Contains(report, "out of date") {
		t.Errorf("the prompt must say what is wrong:\n%s", report)
	}
	if !strings.Contains(report, "--dry-run") {
		t.Errorf("the prompt must name where the per-key detail is:\n%s", report)
	}
	// AND IT APPLIED. Accepting is not an acknowledgement: the whole point is that the agent
	// starts against the render its packs describe.
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"enabled"`) {
		t.Errorf("the accepted apply did not restore the managed value:\n%s", data)
	}
}

// TestHostApplyGateDeclineAbortsTheLaunch is §4.3 row 2, decline half (OQ-HS5). Declining
// aborts, as it does in the jail — and writes nothing, or "no" would mean nothing.
func TestHostApplyGateDeclineAbortsTheLaunch(t *testing.T) {
	home := gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	settings := driftTheHome(t, home)
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	setGateTTY(t, true)

	var errw bytes.Buffer
	if hostApplyGate(&errw, strings.NewReader("n\n"), "claude") {
		t.Fatalf("a declined prompt must ABORT the launch, as a jail launch does:\n%s",
			errw.String())
	}
	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("a declined gate wrote to the home:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if !strings.Contains(errw.String(), "not launched") {
		t.Errorf("the abort must say the agent did not start:\n%s", errw.String())
	}
}

// TestHostApplyGateRefusesWithNoTerminalAndNoApproval is §4.3 row 3 (OQ-HS6), and its message
// is P5: its reader typed `claude`, so it has to name the remedy in a spelling they can use.
func TestHostApplyGateRefusesWithNoTerminalAndNoApproval(t *testing.T) {
	home := gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	settings := driftTheHome(t, home)
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}

	var errw bytes.Buffer
	if hostApplyGate(&errw, nil, "claude") {
		t.Fatal("with drift, no terminal and no approval the launch must be REFUSED")
	}
	report := errw.String()
	for _, want := range []string{
		"refusing to launch claude",   // what stopped, and what the reader typed
		settings,                      // which destination
		"host_apply_on_launch",        // whose setting asked for this
		"yolo host apply --assert",    // remedy 1: the two-step apply
		acceptConfigChangesEnv + "=1", // remedy 2: the only channel a wrapper leaves open
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the refusal must contain %q — its reader typed `claude`, not `yolo`, so an "+
				"unexplained failure reads as \"claude is broken\" (P5/R2):\n%s", want, report)
		}
	}
	// AND EVERY COMMAND IT NAMES MUST RUN. The design writes step 1 as
	// `yolo host apply --assert --accept-config-changes` (§4.3) and hostApply exits 2 on that
	// flag, so a refusal quoting it would hand its reader a second failure. Asserting the
	// absence keeps a future "fix" from putting it back without teaching the parser first.
	if strings.Contains(report, config.AcceptConfigChangesFlag) {
		t.Errorf("the refusal offers %s, which `yolo host apply` rejects with rc 2 — every "+
			"remedy a refusal names has to be runnable (P5):\n%s",
			config.AcceptConfigChangesFlag, report)
	}
	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("a refused launch applied something — nothing partial may be written (OQ-HS6)")
	}
}

// TestHostApplyGateAppliesWithTheApprovalInTheEnvironment is §4.3 row 4 (OQ-HS10), including
// the PRESENCE-not-truth-parsing rule: `0` grants, matching YOLO_ALLOW_STALE_IMAGE's probe.
func TestHostApplyGateAppliesWithTheApprovalInTheEnvironment(t *testing.T) {
	for _, value := range []string{"1", "0", "anything"} {
		t.Run("value="+value, func(t *testing.T) {
			home := gateFixture(t, true)
			if rc, report := applyWith(t, true, nil); rc != 0 {
				t.Fatalf("assert apply rc=%d\n%s", rc, report)
			}
			settings := driftTheHome(t, home)
			t.Setenv(acceptConfigChangesEnv, value)

			var errw bytes.Buffer
			if !hostApplyGate(&errw, nil, "claude") {
				t.Fatalf("%s=%q must let a non-interactive launch proceed:\n%s",
					acceptConfigChangesEnv, value, errw.String())
			}
			data, err := os.ReadFile(settings)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), `"enabled"`) {
				t.Errorf("the approved launch did not apply:\n%s", data)
			}
		})
	}
}

// TestHostApplyGateIsANoOpInAJail is §7's "it does not check the jail". The gate re-renders the
// INVOKING USER'S REAL HOME, and in a jail paths.Home() is /home/agent — a different object
// that no host render is about.
//
// The fixture is deliberately maximal: the key is on, the home is unapplied, and there is
// neither a terminal nor an approval. Every other row of the table would refuse. This one execs.
func TestHostApplyGateIsANoOpInAJail(t *testing.T) {
	gateFixture(t, true)
	t.Setenv("YOLO_VERSION", "9.9.9")

	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Errorf("the gate stopped an IN-JAIL launch:\n%s", errw.String())
	}
	if errw.Len() != 0 {
		t.Errorf("in-jail must be a hard no-op, not a quiet one:\n%s", errw.String())
	}
}

// TestHostApplyGateExecsWhenTheBudgetExpires is §4.4's first class arriving through the
// stuck-detector: a check that does not finish is a check with no answer, and a launch must
// never hang on one.
func TestHostApplyGateExecsWhenTheBudgetExpires(t *testing.T) {
	gateFixture(t, true)
	prev := hostApplyGateBudget
	hostApplyGateBudget = time.Nanosecond
	t.Cleanup(func() { hostApplyGateBudget = prev })

	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Fatalf("a budget overrun must EXEC, not refuse:\n%s", errw.String())
	}
	report := errw.String()
	if !strings.Contains(report, "could not check") {
		t.Errorf("cannot-determine must say so, in one line:\n%s", report)
	}
	if n := strings.Count(strings.TrimRight(report, "\n"), "\n"); n != 0 {
		t.Errorf("cannot-determine is AT MOST ONE LINE to stderr (§4.4); got %d newlines:\n%s",
			n+1, report)
	}
}

// TestHostApplyGateExecsWhenTheApplyItselfCannotAnswer is §4.4's first class arriving the other
// way: a pack the observe pass refuses outright. `yolo check` owns that problem; a launch may
// not be stopped by it.
func TestHostApplyGateExecsWhenTheApplyItselfCannotAnswer(t *testing.T) {
	// TWO PACKS CLAIMING ONE CONFIG SURFACE — a real pack-authoring fault, and the one whose
	// consequence is exactly right here: `yolo host apply` REFUSES the whole apply as a
	// pre-flight and writes nothing, so it has no verdict about the home at all.
	home := t.TempDir()
	packRoot := t.TempDir()
	second := `{"name":"acme-fzf","contributes":[
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	    "path":"~/.acme/settings.json","mode":"rmw","managed":{"fileSuggestion":"run-fzf"}}]}]}`
	var entries []string
	for name, body := range map[string]string{"acme": acmeOwnerPackJSON, "acme-fzf": second} {
		dir := filepath.Join(packRoot, name)
		writeFile(t, filepath.Join(dir, "pack.json"), body)
		entries = append(entries, `{"source":"file://`+dir+`","name":"`+name+`"}`)
	}
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[`+strings.Join(entries, ",")+`],"host_apply_on_launch":true}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(acceptConfigChangesEnv, "")
	setGateTTY(t, false)

	// The fault is real: the same apply, run explicitly, refuses.
	if rc, _ := applyWith(t, false, nil); rc == 0 {
		t.Fatal("fixture bug: the observe pass must fail on a doubly-owned config surface")
	}

	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Fatalf("an apply that cannot answer must EXEC, not refuse — a pack-authoring fault "+
			"is `yolo check`'s problem and may not stop a launch (§4.4):\n%s", errw.String())
	}
	if !strings.Contains(errw.String(), "could not check") {
		t.Errorf("cannot-determine must say so:\n%s", errw.String())
	}
}

// TestAcceptConfigChangesEnvIsScopedToTheWrapperPath is OQ-HS10's containment, asserted as
// BEHAVIOR rather than as a claim in a comment.
//
// If `yolo host apply` honored the variable, one line in a shell rc would pre-approve the
// destruction of a hand-added MCP server on every apply that machine ever runs — and the same
// leniency extended to `yolo run` would hand that rc line every jail launch too, which is the
// blast radius config.AcceptConfigChangesFlag was written to prevent. So: the variable set, no
// stdin, a first apply that would destroy something — and the apply must still fail closed.
func TestAcceptConfigChangesEnvIsScopedToTheWrapperPath(t *testing.T) {
	home := hostMCPFixture(t, mcpContributorPackJSON)
	t.Setenv(acceptConfigChangesEnv, "1")
	path := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"tavily":{"type":"http","url":"https://x?k=SECRET"}}}`
	writeFile(t, path, original)

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc == 0 {
		t.Fatalf("%s made `yolo host apply --assert` skip its own confirmation — that variable "+
			"is honored on the wrapped-launch path and nowhere else (OQ-HS10)\n%s%s",
			acceptConfigChangesEnv, out.String(), errw.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("the user's entry was destroyed:\n%s", after)
	}
}

// TestHostExecRefusesAStaleHomeWithNoTerminal PINS THE CALL SITE.
//
// Every test above calls hostApplyGate directly, so deleting the one line in hostExec that
// calls it would leave all of them green while the feature was switched off wholesale — the
// exact shape AGENTS.md records as having shipped five times. This drives `hostMain` with the
// `--` grammar a wrapper uses, and the two outcomes are distinguishable: the gate refuses with
// rc 1 before anything is composed, while without it the launch reaches resolveHostTarget and
// fails the PATH lookup with rc 127.
func TestHostExecRefusesAStaleHomeWithNoTerminal(t *testing.T) {
	home := gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	driftTheHome(t, home)

	var out, errw bytes.Buffer
	rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil)
	report := out.String() + errw.String()
	if rc == 127 {
		t.Fatalf("the launch reached the PATH lookup, so hostExec never consulted the gate — "+
			"the call site is gone\n%s", report)
	}
	if rc != 1 {
		t.Fatalf("rc = %d, want 1 (the gate's refusal)\n%s", rc, report)
	}
	if !strings.Contains(report, "refusing to launch no-such-agent-binary") {
		t.Errorf("the refusal must come from the host-render gate:\n%s", report)
	}
}

// TestHostExecPassesAFreshHomeThroughToTheLaunch is the call site's other half: the gate must
// not stand between a settled home and its launch. Without it, the rc-1 assertion above could
// be satisfied by a gate that refuses everything.
func TestHostExecPassesAFreshHomeThroughToTheLaunch(t *testing.T) {
	gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}

	var out, errw bytes.Buffer
	rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil)
	report := out.String() + errw.String()
	if rc != 127 {
		t.Fatalf("a settled home must reach the PATH lookup (rc 127); got %d\n%s", rc, report)
	}
	if strings.Contains(report, "refusing to launch") {
		t.Errorf("the gate refused a settled home:\n%s", report)
	}
}
