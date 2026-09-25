package integration

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
)

// Q3'S MEASUREMENT STEP, pinned from Linux under -short
// (docs/design/macos-user-build-step-threat-model.md, Q3).
//
// macos-user.yml builds the macOS floor with `--option sandbox true` and lists what the
// sandbox refuses. That step is shell in YAML, and three ways it could stop measuring anything
// are visible from here, on every push, without a Mac:
//
//   - it could build the wrong attribute. The attribute is `darwinpkg.FloorProfileAttr`, the one
//     every macos-user launch realizes; a rename there (which floor_drift_test.go would carry
//     into flake.nix) would leave the step evaluating a name that no longer exists. The step
//     evaluates `<attr>.name` first and fails loudly, but only on the Mac, a night later.
//   - it could run AFTER the launches. They realize the same floor unsandboxed, so a later step
//     would find nothing to build and report a clean result that measured nothing.
//   - it could fail the job. The answer is a list of refusals, so a refusal is data.
//
// Carries the TestMacosUser prefix for the reason the gate's own tests do: the job that depends
// on the step also verifies it. It is not behind requireMacosUser and needs no Mac.
func TestMacosUserQ3SandboxStepBuildsTheFloorFirst(t *testing.T) {
	wf := uncommentedYAML(readWorkflow(t, "macos-user.yml"))
	const needle = "--option sandbox true"
	step, ok := stepContaining(wf, needle)
	if !ok {
		t.Fatalf("macos-user.yml has no step running `%s`, so Q3's measurement is gone "+
			"(docs/design/macos-user-build-step-threat-model.md)", needle)
	}

	wantAttr := ".#packages.${sys}." + darwinpkg.FloorProfileAttr
	if !strings.Contains(step, wantAttr) {
		t.Errorf("the Q3 step does not build %s — darwinpkg.FloorProfileAttr is %q, the "+
			"attribute every macos-user launch realizes (darwinpkg.BuildFloorProfileArgv). "+
			"A step building anything else measures a closure no launch uses.\nstep:\n%s",
			wantAttr, darwinpkg.FloorProfileAttr, step)
	}
	// THE FLOOR ALONE: a `packages:` list leaking in from the job env would add derivations
	// no default launch builds, and they would read as the floor's refusals.
	if !strings.Contains(step, "env -u YOLO_EXTRA_PACKAGES nix build") {
		t.Errorf("the Q3 build does not clear YOLO_EXTRA_PACKAGES, so the closure it measures " +
			"is not guaranteed to be the floor alone")
	}
	if !strings.Contains(step, "continue-on-error: true") {
		t.Errorf("the Q3 step is not `continue-on-error: true`: a sandbox refusal is the " +
			"ANSWER to Q3, and without the key it turns the whole macos-user verdict red")
	}
	if !strings.Contains(step, "timeout-minutes:") {
		t.Errorf("the Q3 step has no `timeout-minutes`, so a wedged sandboxed build would " +
			"spend the job's whole budget and take the launch tests' results with it")
	}
	// VOID, never a clean result: the setting is restricted and an untrusted user's is ignored.
	if !strings.Contains(step, "ignoring the client-specified setting 'sandbox'") {
		t.Errorf("the Q3 step no longer checks for nix IGNORING `sandbox` (a restricted " +
			"setting), so an unsandboxed build would be reported as a sandboxed one")
	}

	q3 := strings.Index(wf, needle)
	tests := strings.Index(wf, "-run '^TestMacosUser'")
	if tests < 0 {
		t.Fatal("macos-user.yml no longer runs `-run '^TestMacosUser'`; this test's ordering " +
			"check has lost its reference point")
	}
	if q3 > tests {
		t.Errorf("the Q3 step runs AFTER the macos-user launches. They realize the same floor " +
			"unsandboxed, so the sandboxed build would find nothing left to build and report " +
			"a clean result that measured nothing. Move it before the tests.")
	}
}
