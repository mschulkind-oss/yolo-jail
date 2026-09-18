package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE AGENT LAUNCH IS CONFINED, and this file is what fails when it stops being.
//
// docs/research/agent-safehouse.md §8.4. `PlanInvariants` has pinned
// `sandbox-exec -f <profile>` on the PROVISIONING stage since that stage was written,
// and `CapturePlanInvariants` pins it on the capture driver — both on the argument
// that they run vendor code. The argv that runs THE AGENT carried the same three
// words and nothing checked them, so deleting them from `LaunchArgv` (macosuser.go)
// left the whole suite green on the backend where the Seatbelt profile is the entire
// trust boundary: there is no container here, no uid boundary between the agent and
// its own tools, and no second mechanism that denies it /Users or the keychains.
//
// TestLaunchArgvIsConfined is the CALL-SITE pin and the one that matters: it
// runs the real BuildRunPlan and asks the real gate. The rest pin the guard itself —
// a guard that only ever sees correct input is a guard nobody has seen work.

// launchConfinementProblem is the substring identifying this check's message, so a
// test can tell "the launch is unconfined" from the dozen other problems
// PlanInvariants can report about the same plan.
const launchConfinementProblem = "the agent launch argv does not run under"

// testRunPlan builds the plan every test here starts from: the ordinary shape, with
// nothing about confinement said out loud.
func testRunPlan(t *testing.T) RunPlan {
	t.Helper()
	return BuildRunPlan("/Users/Shared/proj", jsonx.NewOrderedMap(), nil, []string{"claude"},
		"/usr/local/bin/yolo", "", "", HostContext{}, jsonx.NewOrderedMap(), nil, nil)
}

// TestLaunchArgvIsConfined is the CALL-SITE pin: delete
// `"/usr/bin/sandbox-exec", "-f", profilePath` from LaunchArgv and this fails.
//
// It asserts through PlanInvariants rather than by grepping the argv itself,
// deliberately: a test that looked at the argv directly would pin the builder and
// leave the GATE unpinned, which is the same hole one level down — the gate is what
// a launch consults (orchestrator.go refuses a plan with any problem), so the gate
// is what has to notice.
func TestLaunchArgvIsConfined(t *testing.T) {
	plan := testRunPlan(t)
	if problems := PlanInvariants(plan); hasProblem(problems, launchConfinementProblem) {
		t.Errorf("a plan built by BuildRunPlan is reported unconfined:\n  %s\nlaunch argv: %v",
			strings.Join(problems, "\n  "), plan.LaunchArgv)
	}
	// And the words really are consecutive on the argv the builder emits — the shape
	// the gate above tests for. Asserted here so a failure distinguishes "the builder
	// changed" from "the gate changed".
	if !containsArgPair(plan.LaunchArgv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
		t.Errorf("launch argv does not carry `sandbox-exec -f %s` consecutively: %v",
			plan.ProfilePath, plan.LaunchArgv)
	}
}

// TestPlanInvariantsRefusesAnUnconfinedLaunch is the mutation the call-site
// pin above is protecting against, applied to the plan instead of to the source: strip
// the three words and the gate must report it.
func TestPlanInvariantsRefusesAnUnconfinedLaunch(t *testing.T) {
	plan := testRunPlan(t)
	plan.LaunchArgv = withoutArgTriple(plan.LaunchArgv,
		"/usr/bin/sandbox-exec", "-f", plan.ProfilePath)
	problems := PlanInvariants(plan)
	if !hasProblem(problems, launchConfinementProblem) {
		t.Errorf("an unconfined launch argv was reported viable; problems were:\n  %s",
			strings.Join(problems, "\n  "))
	}
}

// TestPlanInvariantsRefusesAnotherSessionsProfile: the profile is generated
// FRESH PER SESSION and installed root-owned at SessionProfilePath, so an argv naming
// some other session's file is confined by a policy this launch did not compose — the
// same failure the provisioning stage's own check has always guarded.
func TestPlanInvariantsRefusesAnotherSessionsProfile(t *testing.T) {
	plan := testRunPlan(t)
	for i, a := range plan.LaunchArgv {
		if a == plan.ProfilePath {
			plan.LaunchArgv[i] = "/var/run/yolo-jail/some-other-session.sb"
		}
	}
	if problems := PlanInvariants(plan); !hasProblem(problems, launchConfinementProblem) {
		t.Errorf("a launch confined by another session's profile was reported viable; "+
			"problems were:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestPlanInvariantsWantsTheWordsConsecutive is why the check uses
// containsArgPair and not three membership tests. Every word is present in this argv
// and nothing is confined by it.
func TestPlanInvariantsWantsTheWordsConsecutive(t *testing.T) {
	plan := testRunPlan(t)
	plan.LaunchArgv = append(
		withoutArgTriple(plan.LaunchArgv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath),
		"/usr/bin/sandbox-exec", "--", "-f", "--", plan.ProfilePath)
	if problems := PlanInvariants(plan); !hasProblem(problems, launchConfinementProblem) {
		t.Errorf("three unrelated mentions of the words passed for confinement; "+
			"problems were:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestPlanInvariantsIgnoresAnEmptyLaunchArgv: a plan that launches nothing is
// a legitimate state (a fixture, a builder used for its other halves), and reporting it
// unconfined would be a problem message about a process that does not exist.
func TestPlanInvariantsIgnoresAnEmptyLaunchArgv(t *testing.T) {
	plan := testRunPlan(t)
	plan.LaunchArgv = nil
	if problems := PlanInvariants(plan); hasProblem(problems, launchConfinementProblem) {
		t.Errorf("an empty launch argv was reported unconfined:\n  %s",
			strings.Join(problems, "\n  "))
	}
}

// withoutArgTriple returns argv with the first consecutive run of a, b, c removed —
// the exact mutation "somebody deleted the sandbox-exec words" makes.
func withoutArgTriple(argv []string, a, b, c string) []string {
	for i := 0; i+2 < len(argv); i++ {
		if argv[i] == a && argv[i+1] == b && argv[i+2] == c {
			out := append([]string(nil), argv[:i]...)
			return append(out, argv[i+3:]...)
		}
	}
	return append([]string(nil), argv...)
}
