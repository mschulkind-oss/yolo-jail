package macosuser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// floor_test.go pins half one of docs/design/macos-user-provisioning.md at the
// only level this jail can reach: the plan and the call site. Nothing here runs
// on macOS, and nothing here claims the resulting closure behaves on hardware.

// TestALaunchWithNoPackagesStillBuildsTheFloor is THE call-site test for the
// change, and it is written to fail on the exact edit that would undo it.
//
// `if len(pkgs) > 0` guarded this build until 2026-09-12 and was correct while
// the closure held nothing but the user's declarations: an empty `packages:`
// meant an empty profile. With a floor it means the opposite — the launch that
// declares nothing is the one that depends entirely on the floor — so restoring
// the guard produces a sandbox with no mise, no node and no git while every other
// test in this package stays green.
func TestALaunchWithNoPackagesStillBuildsTheFloor(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	called := false
	d.MaterializeDarwin = func(_ string, pkgs []any) (*Darwin, bool, error) {
		called = true
		if len(pkgs) != 0 {
			t.Errorf("materialize saw %v, want no declared packages", pkgs)
		}
		return mockDarwin(), true, nil
	}

	// newOpts carries an empty config: no `packages:` at all.
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d, want 42 (the mock proxy's exit code)\n%s", rc, buf.String())
	}
	if !called {
		t.Error("a launch with an empty `packages:` never materialized the native closure — " +
			"the floor (mise, node, git, ripgrep, …) would be absent, which is the state " +
			"docs/design/macos-user-provisioning.md §1 is about")
	}
}

// TestTheFloorReachesBothPATHsThePlanCarries. There are TWO, they are read by
// different processes at different times, and only one of them was ever checked.
//
// The launch argv's PATH is what the AGENT gets. $YOLO_DARWIN_LOGIN_PATH in the
// bootstrap argv is what the GENERATORS get — entrypoint.agentPath returns it,
// and three separate generators ask it "will the agent have this binary?" before
// deciding what to write. A plan can satisfy the first and not the second, and
// the result is a jail whose tools are all present and whose generated
// environment was composed as if they were not.
func TestTheFloorReachesBothPATHsThePlanCarries(t *testing.T) {
	storeBin := "/nix/store/000mock-yolo-noncontainer-profile/bin"
	plan := BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(),
		[]string{"claude"}, []string{"claude"}, "/usr/local/bin/yolo", "", "",
		jsonx.NewOrderedMap(), mockDarwin(), nil)

	if !strings.Contains(strings.Join(plan.LaunchArgv, " "), storeBin) {
		t.Errorf("the floor's bin dir never reached the launch PATH:\n%v", plan.LaunchArgv)
	}
	var loginPath string
	for _, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, entrypoint.DarwinLoginPathEnv+"=") {
			loginPath = strings.TrimPrefix(a, entrypoint.DarwinLoginPathEnv+"=")
		}
	}
	if loginPath == "" {
		t.Fatalf("no %s in the bootstrap env:\n%v", entrypoint.DarwinLoginPathEnv, plan.BootstrapArgv)
	}
	if !strings.Contains(loginPath, storeBin) {
		t.Errorf("%s = %q does not carry the floor's bin dir — GenerateShims, "+
			"launchercollision and AssertRequiredBins would all decide against a PATH "+
			"without mise, node, git or ripgrep on it",
			entrypoint.DarwinLoginPathEnv, loginPath)
	}

	// And the invariants agree, which is what makes the two checks above a gate
	// rather than an observation.
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Errorf("a plan carrying the floor is reported unviable: %v", problems)
	}
}

// TestPlanInvariantsCatchAFloorThatNeverReachedTheBootstrap is the mutation half:
// delete the DarwinLoginPathEnv line from buildBootstrapEnv and the plan is still
// perfectly launchable by every other check. This is the one that notices.
func TestPlanInvariantsCatchAFloorThatNeverReachedTheBootstrap(t *testing.T) {
	plan := BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(),
		[]string{"claude"}, []string{"claude"}, "/usr/local/bin/yolo", "", "",
		jsonx.NewOrderedMap(), mockDarwin(), nil)

	// Strip the store bin out of the bootstrap env, leaving the launch PATH intact.
	for i, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, entrypoint.DarwinLoginPathEnv+"=") {
			plan.BootstrapArgv[i] = entrypoint.DarwinLoginPathEnv + "=/usr/bin:/bin"
		}
	}
	problems := PlanInvariants(plan)
	if len(problems) == 0 {
		t.Fatal("a plan whose bootstrap env lost the floor's bin dir was reported viable")
	}
	if !strings.Contains(strings.Join(problems, "\n"), entrypoint.DarwinLoginPathEnv) {
		t.Errorf("the problem does not name %s:\n%v", entrypoint.DarwinLoginPathEnv, problems)
	}
}

// TestARefusalWhenTheNativeBuildContributesNothing. A materialize that succeeds
// and returns no bin dir is a yolo bug, not a config error, and it must not reach
// a launch: the sandbox would come up with neither the floor nor the declared
// packages, and everything about it would look healthy.
//
// It is reachable only since the build became unconditional — before that an
// empty closure was the correct answer to an empty `packages:`.
func TestARefusalWhenTheNativeBuildContributesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		ret  *Darwin
	}{
		{"nil result", nil},
		{"no bin dir", &Darwin{System: "aarch64-darwin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec []string
			var buf bytes.Buffer
			d := mockDeps(&rec)
			d.Out = &buf
			d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
				return tc.ret, true, nil
			}
			if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 1 {
				t.Errorf("rc = %d, want 1", rc)
			}
			if strings.Contains(strings.Join(rec, "\n"), "proxy:") {
				t.Error("launched a sandbox with no tool directory at all")
			}
			if !strings.Contains(buf.String(), "FLOOR") && !strings.Contains(buf.String(), "floor") {
				t.Errorf("the refusal never mentions the floor:\n%s", buf.String())
			}
		})
	}
}
