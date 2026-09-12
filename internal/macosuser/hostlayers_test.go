package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE macos-user CARVE-OUT, DECLARED RATHER THAN INFERRED (OQ-CO10).
//
// A `readsHost` surface's bytes cross on a /ctx mount and this backend has no mounts, so
// every host layer is missing here by construction. The jail's read fails CLOSED, and
// without this line it would fail closed HERE TOO — refusing every launch that selects the
// claude or pi pack on a Mac with a ~/.claude/settings.json, which is the default
// configuration of the backend.
//
// THE RULING: that refusal would be wrong, and this is a carve-out rather than a bug.
// Severity belongs to the DISPOSITION, exactly as it does for the only other fatal in-jail
// witness — the reachability witness escalates on `requested`/`shared` and never on
// `unsupported`, because "a host yolo could not ask is never refused for what it cannot
// help" (OQ-R3). "This backend has no delivery mechanism" is that same `unsupported`: the
// launcher knows it before the launch starts, and it is not a delivery fault. What the
// fail-closed read is for is the launch that SAID it delivered and did not.
//
// It is also the treatment this backend already gives the sibling mechanism: a
// source-bearing `host_files` entry is filtered out of the wire a few lines above, as a
// recorded deficiency (runplan.go), never a refused launch. A pack's grant refusing where
// the user's key degrades would be a new asymmetry introduced in the same commit as one is
// removed.
//
// The deficiency stays SAID, which is what keeps this from being the backend
// feature-detection P5 forbids: the launch names each grant that did not cross
// (run.noteMacosUserHostByteGaps), the agent's briefing says its config came from DEFAULTS
// rather than the human's (run.backendLimits), and this variable tells the jail itself.
func TestRunPlanDeclaresHostLayersUnsupported(t *testing.T) {
	plan := BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", hostStaged, "",
		jsonx.NewOrderedMap(), nil, nil)

	want := packload.HostLayerEnvVar + "=" + packload.HostLayersUnsupportedWire()
	if !containsArg(plan.BootstrapArgv, want) {
		t.Fatalf("the bootstrap argv does not carry %q:\n%v\nWithout it the jail's "+
			"fail-closed host-layer read has no disposition to read, and a Mac with a "+
			"~/.claude/settings.json cannot launch at all", want, plan.BootstrapArgv)
	}
	// The value is a REPORT, not a bare token: the jail parses one shape from both
	// backends, so a hand-written string here would be read as UNKNOWN and silently
	// restore the old fail-open behaviour on this backend alone.
	if !strings.Contains(want, `"delivery":"unsupported"`) {
		t.Errorf("the wire %q is not the report shape the jail parses", want)
	}
}
