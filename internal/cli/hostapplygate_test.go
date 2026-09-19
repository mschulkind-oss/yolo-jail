package cli

// hostapplygate_test.go walks §4.3's table row by row, plus §4.4's two failure classes and
// §7's "in-jail is a hard no-op" (docs/reference/host-apply-staleness.md).
//
// THE CALL SITE IS PINNED SEPARATELY, at the bottom, and it has to be: every test that calls
// hostApplyGate directly would still pass with the one line in hostExec deleted, which is
// AGENTS.md's callee-pinned-call-site-unpinned shape — the class this repo has shipped five
// times. TestHostExecRefusesAStaleHomeWithNoTerminal drives `hostMain` instead and distinguishes
// the gate's refusal (rc 1, with its text) from what happens without it (rc 127, from the
// PATH lookup for a binary that does not exist).

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	// The gate drives a WRITING apply, and `claude` declares `program claude`: without the
	// stub these tests assert the launch gate on a machine that has the agent CLIs and assert
	// the DEPENDENCY refusal on one that does not.
	stubDeclaredBins(t)
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
	edited := strings.Replace(string(data), `"defaultMode": "default"`,
		`"defaultMode": "plan"`, 1)
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

// TestHostApplyGateAutoAppliesOnATTY asserts zero-prompt auto-apply on launch (OQ-2):
// on drift with a terminal attached, yolo applies the updates automatically without
// prompting, emits a concise stderr notice, and lets the launch proceed.
func TestHostApplyGateAutoAppliesOnATTY(t *testing.T) {
	home := gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	settings := driftTheHome(t, home)
	setGateTTY(t, true)

	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Fatalf("auto-apply must let the launch proceed:\n%s", errw.String())
	}
	report := errw.String()
	if !strings.Contains(report, "synchronized host configuration") {
		t.Errorf("the launch notice must announce synchronization:\n%s", report)
	}
	// AND IT APPLIED without prompting.
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"enabled"`) {
		t.Errorf("the auto-apply did not restore the managed value:\n%s", data)
	}
}

// TestHostApplyGateAutoAppliesWithNoTerminal asserts zero-prompt auto-apply off a TTY:
// scripted and CI launches no longer refuse over benign pack updates; they auto-apply and exec.
func TestHostApplyGateAutoAppliesWithNoTerminal(t *testing.T) {
	home := gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	settings := driftTheHome(t, home)

	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Fatalf("non-TTY auto-apply must let the launch proceed without refusing:\n%s", errw.String())
	}
	report := errw.String()
	if !strings.Contains(report, "synchronized host configuration") {
		t.Errorf("the launch notice must announce synchronization:\n%s", report)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"enabled"`) {
		t.Errorf("the auto-apply did not restore the managed value:\n%s", data)
	}
}

// TestHostApplyGateFirstApplyWithEntryLossesPromptsOnTTY asserts the one-way door exception:
// on a first-ever apply into an unmanaged home with pre-existing undeclared MCP servers,
// confirmHostLosses prompts interactively.
func TestHostApplyGateFirstApplyWithEntryLossesPromptsOnTTY(t *testing.T) {
	home := hostMCPFixture(t, mcpContributorPackJSON)
	t.Setenv("YOLO_VERSION", "")
	cfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(cfgData), `"packs":`, `"host_apply_on_launch":true,"packs":`, 1)
	writeFile(t, cfgPath, cfg)
	t.Setenv(acceptConfigChangesEnv, "")
	setGateTTY(t, true)

	path := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"tavily":{"type":"http","url":"https://x?k=SECRET"}}}`
	writeFile(t, path, original)

	// Decline:
	var errw bytes.Buffer
	if hostApplyGate(&errw, strings.NewReader("n\n"), "claude") {
		t.Fatalf("declining adoption must abort the launch:\n%s", errw.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("declined adoption destroyed user servers:\n%s", after)
	}

	// Accept:
	errw.Reset()
	if !hostApplyGate(&errw, strings.NewReader("y\n"), "claude") {
		t.Fatalf("accepting adoption must proceed:\n%s", errw.String())
	}
	afterAccept, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterAccept) == original {
		t.Errorf("accepted adoption did not apply:\n%s", afterAccept)
	}
}

// TestHostApplyGateFirstApplyWithEntryLossesRefusesWithoutTerminal asserts that a first apply
// with entry losses fails closed when no terminal is attached to prevent data loss.
func TestHostApplyGateFirstApplyWithEntryLossesRefusesWithoutTerminal(t *testing.T) {
	home := hostMCPFixture(t, mcpContributorPackJSON)
	t.Setenv("YOLO_VERSION", "")
	cfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(cfgData), `"packs":`, `"host_apply_on_launch":true,"packs":`, 1)
	writeFile(t, cfgPath, cfg)
	t.Setenv(acceptConfigChangesEnv, "")
	setGateTTY(t, false)

	path := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"tavily":{"type":"http","url":"https://x?k=SECRET"}}}`
	writeFile(t, path, original)

	var errw bytes.Buffer
	if hostApplyGate(&errw, nil, "claude") {
		t.Fatalf("first apply with entry losses and no terminal must refuse to launch:\n%s", errw.String())
	}
	if !strings.Contains(errw.String(), "refusing to launch") {
		t.Errorf("expected refusal message, got:\n%s", errw.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("refused launch destroyed user servers:\n%s", after)
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

	// DETERMINISM, not a shorter budget. This test used to set the budget to a nanosecond and
	// rely on the observe pass being slower than an almost-immediately-ready timer. Both select
	// cases then go ready and Go chooses at random, so the test was a coin flip weighted by how
	// warm the machine's page cache was — green here, RED ON CI 2026-09-12, where the survey won.
	// Blocking the survey outright makes the timeout the only selectable case.
	release := make(chan struct{})
	prevSurvey := hostApplyGateSurvey
	hostApplyGateSurvey = func(_, _ io.Writer, _, _ bool, _ io.Reader, _ *hostApplySurvey) int {
		<-release
		return 0
	}
	prev := hostApplyGateBudget
	hostApplyGateBudget = 10 * time.Millisecond
	t.Cleanup(func() {
		hostApplyGateSurvey = prevSurvey
		hostApplyGateBudget = prev
		close(release) // let the abandoned goroutine finish; the gate already moved on
	})

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

// TestHostExecAutoAppliesStaleHomeAtLaunch PINS THE CALL SITE.
//
// Every test above calls hostApplyGate directly, so deleting the one line in hostExec that
// calls it would leave all of them green while the feature was switched off wholesale — the
// exact shape AGENTS.md records as having shipped five times. This drives `hostMain` with the
// `--` grammar a wrapper uses: the gate auto-applies the stale home (restoring the key and
// emitting the synchronization notice), and then proceeds to launch (reaching resolveHostTarget
// and failing the PATH lookup for a nonexistent binary with rc 127).
func TestHostExecAutoAppliesStaleHomeAtLaunch(t *testing.T) {
	home := gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	settings := driftTheHome(t, home)

	var out, errw bytes.Buffer
	rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil)
	report := out.String() + errw.String()
	if rc != 127 {
		t.Fatalf("rc = %d, want 127 (the launch proceeds past gate to PATH lookup)\n%s", rc, report)
	}
	if !strings.Contains(report, "synchronized host configuration") {
		t.Errorf("the launch must output the synchronized notice:\n%s", report)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"enabled"`) {
		t.Errorf("the gate did not restore the managed value:\n%s", data)
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
