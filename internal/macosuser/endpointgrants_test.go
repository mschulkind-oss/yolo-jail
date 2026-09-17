package macosuser

// endpointgrants_test.go pins the CROSS-UID GRANT half of the host-service lifecycle: every
// endpoint a launch carries is readable by the sandbox account, not just the one service this
// backend used to start.
//
// The run pipeline's macos-user arm goes through startLoopholesDisclosed now
// (internal/cli/run/run.go), so a launch can carry several `YOLO_SERVICE_*_ENDPOINT`
// variables. The plan used to name one by hand — which would have delivered the credential
// broker's endpoint and silently withheld every other one, leaving the sandbox told exactly
// where a service is and unable to open it.
//
// ⚠ `chmod +a` IS A macOS ACL EXTENSION and nothing here runs it. These tests assert the
// emitted argv, which is all a Linux unit test can reach; whether the sandbox account can then
// open the 0600 file is a question only the self-hosted arm64 runner can answer
// (docs/plans/runbooks/mac-actions-runner.md).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const grantServicesDir = "/private/tmp/yolo-host-services-abcd1234"

// planWithEndpoints builds a run plan whose launch env carries the given variables.
func planWithEndpoints(t *testing.T, pairs ...string) RunPlan {
	t.Helper()
	env := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		env.Set(pairs[i], pairs[i+1])
	}
	return BuildRunPlan("/Users/Shared/proj", jsonx.NewOrderedMap(), []string{"pi"},
		[]string{"pi"}, "/opt/yolo", "", "", HostContext{}, env, nil, nil)
}

// hasCommand reports whether the plan stages exactly this argv.
func hasCommand(plan RunPlan, want []string) bool {
	for _, cmd := range plan.StageCommands {
		if reflect.DeepEqual(cmd, want) {
			return true
		}
	}
	return false
}

// EVERY endpoint gets its grant, and the credential broker is not special.
//
// The expectation comes from EndpointGrantCommands itself rather than from a second copy of
// the ACE shape: what is under test is which endpoints are granted, and restating the argv
// here would turn a wording change into a failure about the wrong thing.
func TestRunPlanGrantsEveryPublishedEndpoint(t *testing.T) {
	broker := grantServicesDir + "/openai-auth-broker.endpoint"
	other := grantServicesDir + "/acme-proxy.endpoint"
	plan := planWithEndpoints(t,
		"YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT", broker,
		"YOLO_SERVICE_ACME_PROXY_ENDPOINT", other)

	for _, endpoint := range []string{broker, other} {
		if !hasCommand(plan, EndpointGrantCommands(endpoint, "")[0]) {
			t.Errorf("no stage command grants the sandbox account read on %s; it is 0600 under "+
				"the invoking user, so the sandbox would be told where its service is and be "+
				"unable to open it:\n%v", endpoint, plan.StageCommands)
		}
	}
}

// THE DIRECTORY ACE IS EMITTED ONCE. Every endpoint of one launch lives in the same per-jail
// services dir, and a stage command's failure is FATAL to the launch (orchestrator.go) — so a
// repeated `chmod +a` for one directory is not a cosmetic duplicate.
func TestRunPlanEmitsOneSearchAceForOneDirectory(t *testing.T) {
	plan := planWithEndpoints(t,
		"YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT", grantServicesDir+"/openai-auth-broker.endpoint",
		"YOLO_SERVICE_ACME_PROXY_ENDPOINT", grantServicesDir+"/acme-proxy.endpoint")

	searchAce := EndpointGrantCommands(grantServicesDir+"/acme-proxy.endpoint", "")[1]
	n := 0
	for _, cmd := range plan.StageCommands {
		if reflect.DeepEqual(cmd, searchAce) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the directory ACE is staged %d times for one directory, and a failed stage "+
			"command ends the launch:\n%v", n, plan.StageCommands)
	}
}

// THE `_SOCKET` SPELLING IS NOT GRANTED, and the exclusion is about the ACE rather than about
// tidiness: a Unix socket needs WRITE to connect(2) and this grant is READ (loophole-transport
// OQ-T5), so granting one would hand out an access that cannot be used and read as delivered.
func TestRunPlanDoesNotGrantASocketVariable(t *testing.T) {
	sock := grantServicesDir + "/cgroup-delegate.sock"
	plan := planWithEndpoints(t, "YOLO_SERVICE_CGROUP_DELEGATE_SOCKET", sock)
	for _, cmd := range plan.StageCommands {
		if strings.Contains(strings.Join(cmd, " "), sock) {
			t.Errorf("a socket-transport service was granted the endpoint-file ACE:\n%v", cmd)
		}
	}
}

// A variable that is not a service endpoint at all is left alone — the discriminator is the
// producer's own prefix/suffix pair, and a launch env is full of other variables.
func TestRunPlanGrantsNothingForAnOrdinaryVariable(t *testing.T) {
	plan := planWithEndpoints(t, "ANTHROPIC_BASE_URL", "https://example.test")
	for _, cmd := range plan.StageCommands {
		if strings.Contains(strings.Join(cmd, " "), "example.test") {
			t.Errorf("an ordinary env value reached the grant stage:\n%v", cmd)
		}
	}
}

// AND THE PLAN REFUSES ONE THAT IS CARRIED WITHOUT A GRANT — the mutation half, because every
// assertion above is about what BuildRunPlan emits and none of them fails if a later change
// carries a new endpoint some other way. PlanInvariants is a real gate: the orchestrator
// refuses a plan with problems.
func TestPlanInvariantsCatchAnUngrantedEndpoint(t *testing.T) {
	endpoint := grantServicesDir + "/acme-proxy.endpoint"
	plan := planWithEndpoints(t, "YOLO_SERVICE_ACME_PROXY_ENDPOINT", endpoint)
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("a well-formed plan has problems: %v", problems)
	}

	broken := plan
	broken.StageCommands = nil
	for _, cmd := range plan.StageCommands {
		if strings.Contains(strings.Join(cmd, " "), endpoint) {
			continue
		}
		broken.StageCommands = append(broken.StageCommands, cmd)
	}
	if !anyContains(PlanInvariants(broken), "YOLO_SERVICE_ACME_PROXY_ENDPOINT") {
		t.Errorf("PlanInvariants accepts a launch that names an endpoint the sandbox account "+
			"cannot open: %v", PlanInvariants(broken))
	}
}
