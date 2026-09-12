package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// RUNBOOK ITEM 8 — a failing provisioning stage must not kill the launch.
// docs/plans/runbooks/macos-user-manual-checks.md §8, written 2026-09-12 and never run.
//
// THE RULE, and why it needed a test on hardware. §4 of
// docs/design/macos-user-provisioning.md says in bold that a failing stage must not abort
// the launch, and the code INVERTED that rule until 2026-09-12 (§10.7): a workspace that
// merely DECLARED mise_tools could not launch at all when the stage returned non-zero,
// and the message blamed the user for it.
//
// ⚠ THIS IS A NEGATIVE ASSERTION — "the launch survived" — and those rot silently,
// because everything about a launch that never happened also fails to happen. Two things
// keep it honest, and both must stay:
//
//   - THE STAGE REALLY RAN AND REALLY FAILED. The launch is given a `mise_tools` version
//     that cannot resolve, and the test asserts BOTH halves of the evidence the stage
//     leaves behind — the failure marker in the startup log and the red line on the
//     console. Without those, a stage that silently succeeded (or never ran at all) would
//     satisfy "the launch survived" while testing nothing.
//   - THE AGENT REALLY RAN. Not merely `rc == 0`: the probe echoes a marker, so a launch
//     that returned 0 without ever reaching the agent cannot pass.
//
// DOES IT FAIL IF THE CALL SITE IS DELETED? Yes, and that is the check AGENTS.md asks for.
// Remove the orchestrator's `runProvisionStage(deps, out, plan)` — the whole stage — and
// nothing writes the marker, so the first assertion below is fatal. Make provision.Script
// propagate the body's status unconditionally instead of only on an explicit `n`, and the
// launch dies before the agent, so the marker assertions pass and the survival ones fail.
// The one edit it does NOT catch is reverting runProvisionStage to `if deps.Run(…) != 0 {
// return 1 }`, because a non-interactive stage never returns non-zero for it to see —
// that branch is the unit suite's, as stated at the foot of this comment.
//
// WHAT THIS TEST DOES NOT COVER, stated here because the runbook item is wider than the
// test and must stay in the runbook for the rest:
//
//   - THE EXEC-LAYER HALF — the runbook's own fault injection, `chmod 000
//     /usr/bin/sandbox-exec`. That is a global, SIP-adjacent mutation with a window in
//     which the machine is broken for every other process, and no test may make it. There
//     is no process-local substitute: the stage argv names /usr/bin/sandbox-exec
//     ABSOLUTELY (internal/macosuser/provision.go ProvisionArgv), so a PATH shim cannot
//     reach it, and the Seatbelt profile is installed root-owned by the same launch that
//     consumes it. Closing this needs a seam in internal/macosuser, and until one exists
//     the runbook keeps the item.
//   - THE VETO HALF — answering `n` at the prompt. It requires a controlling terminal
//     (provision.Script gates the prompt on `[ -t 0 ]`) and this harness never gives a
//     child one: runCommand leaves cmd.Stdin nil, which os/exec wires to /dev/null. That
//     is deliberate and shared by the whole suite, so the veto stays a human's check.
//
// WHAT IS COVERED ELSEWHERE, so nobody writes it twice: runProvisionStage's three-way
// decision — veto / never-ran / unreadable-log — is pinned at its real call site by
// internal/macosuser/provision_test.go, which drives RunMacosUser with mocked Deps. Those
// branches all need a NON-ZERO stage status, which is exactly what a non-interactive
// failing stage does not produce; this test is the complementary half, and it is the only
// thing anywhere that runs the real script under a real Seatbelt profile.
func TestMacosUserAFailingProvisioningStageDoesNotAbortTheLaunch(t *testing.T) {
	requireMacosUser(t)

	ws := macosUserWorkspace(t, `{"mise_tools": {"jq": "`+macosUserUnresolvableVersion+`"}}`)
	r := runMacosUser(t, ws, `echo "=== AGENT ==="`)

	// EVIDENCE FIRST, verdict second. If the stage did not fail, everything below is
	// vacuous, so the two proofs that it did are checked before the negative assertion
	// they qualify.
	logPath := provision.StartupLog(ws)
	log := ""
	if body, err := os.ReadFile(logPath); err == nil {
		log = string(body)
	}
	if !strings.Contains(log, provision.FailedMarker) {
		t.Fatalf("runbook item 8: the startup log at %s does not record %q, so THE STAGE "+
			"DID NOT FAIL and this test proves nothing about a launch surviving one.\n\n"+
			"Expected `mise install` to reject jq@%s. If mise now resolves that version, "+
			"pick another impossible one; if the log is empty or absent, the stage never "+
			"ran and runbook item 7 is the item to read first.\nlog:\n%s\nstdout:\n%s\nstderr:\n%s",
			logPath, provision.FailedMarker, macosUserUnresolvableVersion, log, r.stdout, r.stderr)
	}
	if !strings.Contains(r.combined(), macosUserStageFailedConsoleLine) {
		t.Errorf("runbook item 8: the stage failed (the log says so) but the launch's "+
			"console never printed %q. The failure is recorded where only a later briefing "+
			"reads it, so the human running this launch is told nothing at all.\n"+
			"stdout:\n%s\nstderr:\n%s",
			macosUserStageFailedConsoleLine, r.stdout, r.stderr)
	}

	// THE RULE ITSELF (§4). The stage failed; the launch must have continued to the agent.
	if !strings.Contains(r.stdout, "=== AGENT ===") {
		t.Errorf("runbook item 8: THE FAILING STAGE KILLED THE LAUNCH — the agent never "+
			"printed its marker. §4 of docs/design/macos-user-provisioning.md says in bold "+
			"that a failing stage must not abort the launch: a workspace whose declared "+
			"tools cannot install is still a workspace the user asked to open, and the "+
			"record in %s is the whole remedy. This is the exact inversion §10.7 "+
			"describes.\nstdout:\n%s\nstderr:\n%s", logPath, r.stdout, r.stderr)
	}
	if r.rc != 0 {
		t.Errorf("runbook item 8: the launch exited %d. The agent is `echo`, so the only "+
			"thing that can have failed is the launch itself — and rc 1 with the message "+
			"below is the veto branch (runProvisionStage) firing on a launch nobody "+
			"vetoed.\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	if strings.Contains(r.combined(), macosUserStageAbortedConsoleLine) {
		t.Errorf("runbook item 8: the launch printed %q. That message means a human "+
			"answered `n` — but nothing here has a terminal to answer on, so it was "+
			"reached by mistake. Either provision.Script propagated the body's status "+
			"without a veto, or runProvisionStage classified a zero status as one.\n"+
			"stdout:\n%s\nstderr:\n%s",
			macosUserStageAbortedConsoleLine, r.stdout, r.stderr)
	}

	// THE UNANSWERABLE PROMPT. provision.Script asks "continue anyway?" only when stdin is
	// a terminal, and this launch's is /dev/null: a prompt here would be read from EOF and
	// answered by nobody, which is how a scripted launch acquires a question in its log
	// that no reader can act on.
	if strings.Contains(r.combined(), macosUserStagePromptLine) {
		t.Errorf("runbook item 8: the stage printed its %q prompt on a launch with no "+
			"terminal. The `[ -t 0 ]` guard in provision.Script is what keeps a scripted "+
			"launch from asking a question nothing can answer.\nstdout:\n%s\nstderr:\n%s",
			macosUserStagePromptLine, r.stdout, r.stderr)
	}
}

// macosUserUnresolvableVersion is a version of a REAL tool that mise cannot resolve — the
// runbook's own recipe for the reversible half of item 8 ("a `mise_tools` version that
// does not exist").
//
// A real tool with an impossible version, rather than an impossible tool NAME, on purpose:
// mise resolves the backend from the name, so `jq` routes to aqua and fails at asset
// resolution with a clear message, while an unknown NAME can also make every later `mise`
// invocation in the sandbox noisy — including the agent's own login shell, which this
// test needs quiet in order to read its marker off stdout. Measured with mise 2026.8.6 in
// the development jail: `mise install --quiet` exits 1 with "no asset released". That
// measurement is a Linux one and the assertion it supports is not — what carries across is
// that no platform has an asset for a version that was never released, and the test says
// so in its own failure message rather than assuming it.
const macosUserUnresolvableVersion = "0.0.0-yolo-integration-never-resolves"

// The two console lines item 8 reads that the STAGE SCRIPT writes. They are
// transcribed rather than derived because each is a fragment of a printf FORMAT string
// with its own escapes and substitutions — and
// TestMacosUserStageFailureConsoleLinesAreWhatTheScriptPrints checks every one of them
// against the production script on Linux, in `just test-fast`, so a reword cannot reach a
// Mac as an unexplained red.
const (
	macosUserStageFailedConsoleLine = "✗ Provisioning failed"
	macosUserStagePromptLine        = "continue anyway?"
)

// macosUserStageAbortedConsoleLine is the launcher's, not the script's: runProvisionStage
// prints it when it reads a non-zero stage status together with the failure marker, which
// it classifies as a human's explicit `n`.
const macosUserStageAbortedConsoleLine = "Provisioning was aborted."

// Pins the needles above against the code that prints them. See the item 7 file's pair for
// why a Mac-only assertion never spells its expected value by hand.
func TestMacosUserStageFailureConsoleLinesAreWhatTheScriptPrints(t *testing.T) {
	script := provision.Script("/Users/Shared/yolo/ws/.yolo/startup.log", "false")
	for _, want := range []string{
		macosUserStageFailedConsoleLine,
		macosUserStagePromptLine,
		provision.FailedMarker,
		// The guard that makes the prompt conditional. Item 8 asserts the prompt is
		// ABSENT on a launch with no terminal, which is only meaningful while the
		// script asks that question of stdin.
		"[ -t 0 ]",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the provisioning script no longer contains %q, so runbook item 8 is "+
				"looking for a line the stage does not print any more — and every "+
				"assertion that reads it would pass vacuously on the next Mac that runs "+
				"the suite.\nscript:\n%s", want, script)
		}
	}
}
