package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// hometier_test.go pins the launcher's half of the macos-user workspace tier
// (docs/design/macos-user-home-tiers.md): the bootstrap lays the symlink layout only when
// it is TOLD where the workspace sidecar is, so this one env var is the whole channel.
//
// Its absence is silent by construction — a bootstrap without it generates into the shared
// account home exactly as it did before A′, and the launch looks healthy — which is why the
// plan invariant exists and why this asserts the invariant FIRES, not merely that the value
// is present today.

func runPlanFor(workspace string) RunPlan {
	return BuildRunPlan(workspace, jsonx.NewOrderedMap(), []string{"claude"}, []string{"claude"},
		"/usr/local/bin/yolo", "", "", jsonx.NewOrderedMap(), nil, nil)
}

func TestBuildRunPlanNamesTheWorkspaceSidecar(t *testing.T) {
	plan := runPlanFor("/Users/Shared/yolo/proj")
	want := entrypoint.DarwinHomeSidecarEnv + "=/Users/Shared/yolo/proj/.yolo/home"
	if !containsArg(plan.BootstrapArgv, want) {
		t.Fatalf("the bootstrap argv does not carry %s:\n%v", want, plan.BootstrapArgv)
	}
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("a plan carrying it must be clean: %v", problems)
	}

	// THE CALL-SITE PIN: strip the variable and the invariant must object. Without this
	// half the test above is satisfied by any plan that happens to contain the string,
	// including one produced by a launcher that stopped laying the tier.
	broken := plan
	broken.BootstrapArgv = nil
	for _, a := range plan.BootstrapArgv {
		if !strings.HasPrefix(a, entrypoint.DarwinHomeSidecarEnv+"=") {
			broken.BootstrapArgv = append(broken.BootstrapArgv, a)
		}
	}
	if !anyContains(PlanInvariants(broken), entrypoint.DarwinHomeSidecarEnv) {
		t.Errorf("a plan that never tells the bootstrap where the sidecar is passes the "+
			"invariants; every pack state dir would silently stay in /Users/_yolojail: %v",
			PlanInvariants(broken))
	}
}

// A sidecar pointing at ANOTHER workspace is the tier collapse with extra steps, so the
// invariant checks the value and not merely the key.
func TestPlanInvariantsRejectAForeignSidecar(t *testing.T) {
	plan := runPlanFor("/Users/Shared/yolo/proj")
	for i, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, entrypoint.DarwinHomeSidecarEnv+"=") {
			plan.BootstrapArgv[i] = entrypoint.DarwinHomeSidecarEnv + "=/Users/Shared/yolo/other/.yolo/home"
		}
	}
	if !anyContains(PlanInvariants(plan), entrypoint.DarwinHomeSidecarEnv) {
		t.Error("a layout pointed at another workspace's sidecar was accepted")
	}
}

// An install capture must lay NO layout: its home is a throwaway staging tree, and the
// delta walk that makes a capture an entry does not follow symlinks — a layout there would
// admit an empty install.
func TestCapturePlanNamesNoSidecar(t *testing.T) {
	plan := BuildCapturePlan(testCaptureOptions())
	for _, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, entrypoint.DarwinHomeSidecarEnv+"=") {
			t.Fatalf("the capture bootstrap carries %s; its staging home must stay flat "+
				"or the delta walk records nothing", a)
		}
	}
}
