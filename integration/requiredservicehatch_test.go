package integration

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestARequiredServiceThatDidNotStartRefusesAndTheHatchGetsAShell is OQ-R8 end to end
// (docs/reference/loopback-tls-reachability.md, ruled 2026-10-05: (a)): a real boot whose
// required in-jail service, the wire bridge, cannot start refuses with a refusal that names the
// service, the pack it came from, the cause and the hatch, and never calls the relay a config
// generator; the same launch with YOLO_ALLOW_UNREACHABLE_SERVICES=1 boots and says what it let
// through.
//
// The cause is a MISSING KEY, the one a test can stage without a port: claude is profiled at
// cerebras, which the bridge serves, and no CEREBRAS_API_KEY reaches the launch. The launcher's
// own credential pre-flight would refuse that first, so its hatch (YOLO_ALLOW_MISSING_PROVIDERS)
// is set on both launches: the boot is what is under test. No agent runs and nothing is dialled;
// the bridge reports `failed` at boot, before any request.
func TestARequiredServiceThatDidNotStartRefusesAndTheHatchGetsAShell(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{
		"packs": ["claude", "cerebras"],
		"profile": {"claude": "cerebras"}
	}`)
	noKey := []string{"CEREBRAS_API_KEY=", paths.AllowMissingProvidersEnv + "=1"}

	refused := runYolo(t, dir, "echo JAIL-BOOTED", withEnv(noKey...))
	got := refused.combined()
	if refused.rc == 0 || strings.Contains(refused.stdout, "JAIL-BOOTED") {
		t.Fatalf("a boot whose required wire bridge did not start must refuse: rc %d\n%s", refused.rc, got)
	}
	for _, want := range []string{
		"required in-jail service 'wire-bridge'", // the service
		`from pack "wire-bridge"`,                // the pack it came from
		"CEREBRAS_API_KEY",                       // the cause: the key that never arrived
		"env_sources",                            // its next step
		paths.AllowUnreachableServicesEnv + "=1", // the way to a shell
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "config generator") {
		t.Errorf("the refusal still calls the relay a config generator:\n%s", got)
	}

	hatched := runYolo(t, dir, "echo JAIL-BOOTED",
		withEnv(append(noKey, paths.AllowUnreachableServicesEnv+"=1")...))
	if hatched.rc != 0 || !strings.Contains(hatched.stdout, "JAIL-BOOTED") {
		t.Fatalf("%s=1 must get the launch past a required service that did not start: rc %d\n%s",
			paths.AllowUnreachableServicesEnv, hatched.rc, hatched.combined())
	}
	if !strings.Contains(hatched.stderr, "CONTINUING although required in-jail service 'wire-bridge'") {
		t.Errorf("the hatched boot does not say what it let through:\n%s", hatched.stderr)
	}
}
