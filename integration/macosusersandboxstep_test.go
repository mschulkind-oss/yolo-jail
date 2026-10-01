package integration

import (
	"regexp"
	"slices"
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

// TestMacosUserQ3CacheMissStepRebuildsAFloorPackage pins Q3's CACHE-MISS step the same way, from
// Linux under -short. The floor step above has never built a floor package (every run
// substituted them), so this one forces one with `nix build --rebuild`, and the ways it could
// stop measuring that are visible from here:
//
//   - the package could stop being a floor entry, and the step would then measure a builder no
//     macos-user launch runs;
//   - it could resolve the package from a nixpkgs other than this flake's locked input, or drop
//     the sandbox flag, the rebuild, the VOID checks, its `continue-on-error` or its cap.
//
// Its position is checked too, though a --rebuild builds whether or not the launches realized
// the package first: before the tests is where its summary sits beside the floor step's.
func TestMacosUserQ3CacheMissStepRebuildsAFloorPackage(t *testing.T) {
	wf := uncommentedYAML(readWorkflow(t, "macos-user.yml"))
	const needle = "--rebuild"
	step, ok := stepContaining(wf, needle)
	if !ok {
		t.Fatalf("macos-user.yml has no step running `nix build %s`, so Q3's cache-miss "+
			"measurement is gone (docs/design/macos-user-build-step-threat-model.md#Q3)", needle)
	}
	m := regexp.MustCompile(`(?m)^\s*pkg=([A-Za-z0-9_.+-]+)\s*$`).FindStringSubmatch(step)
	if m == nil {
		t.Fatalf("the Q3 cache-miss step no longer names its package as `pkg=<name>`, so this "+
			"test cannot check it is a floor entry:\n%s", step)
	}
	if !slices.Contains(darwinpkg.FloorNames(), m[1]) {
		t.Errorf("the Q3 cache-miss step rebuilds %q, which is not in darwinpkg.FloorNames(): it "+
			"would measure a builder no macos-user launch runs. Pick a floor entry", m[1])
	}
	for _, want := range []struct{ text, why string }{
		{"--inputs-from .", "the package must resolve from this flake's locked nixpkgs, the set the floor reads"},
		{`inst="nixpkgs#${pkg}^*"`, "every output must be realized, or --rebuild refuses to check the derivation"},
		{"--option sandbox true", "without it the rebuild says nothing about the sandbox"},
		{"aarch64-darwin", "the step must say VOID on a system whose floor reads another nixpkgs input"},
		{"ignoring the client-specified setting 'sandbox'", "an unsandboxed rebuild must read as VOID, not as a result"},
		{"continue-on-error: true", "a refusal is the answer, and must not turn the macos-user verdict red"},
		{"timeout-minutes:", "a wedged compile must not spend the job's whole budget"},
	} {
		if !strings.Contains(step, want.text) {
			t.Errorf("the Q3 cache-miss step lacks %q: %s.\nstep:\n%s", want.text, want.why, step)
		}
	}
	if i, tests := strings.Index(wf, needle), strings.Index(wf, "-run '^TestMacosUser'"); tests < 0 || i > tests {
		t.Errorf("the Q3 cache-miss step does not run before the macos-user tests (rebuild at %d, "+
			"tests at %d)", i, tests)
	}
}
