package macosuser

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE FORK DECISIONS REACH THE BOOTSTRAP (docs/design/forked-programs-as-packs.md FP-D3,
// docs/design/patched-forks.md §9): the bootstrap generates the sandbox's launcher for a forked
// program, which bakes its bin's decision, so the reason the launch composed for each fork — this
// backend delivers none, and a container backend does — is relayed from the launch env, and nothing
// is when the launch carried no fork.
func TestTheForkDecisionsReachTheBootstrap(t *testing.T) {
	const ws = "/Users/Shared/yolo/proj"
	wire := `{"tool":{"reason":"tool is not delivered on macos-user: run it on a container backend"}}`
	plan := func(env *jsonx.OrderedMap) RunPlan {
		return BuildRunPlan(ws, jsonx.NewOrderedMap(), []string{"claude"}, []string{"/bin/zsh", "-l"},
			"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, env, nil, nil)
	}
	env := jsonx.NewOrderedMap()
	env.Set(entrypoint.ForkBuildsEnv, wire)
	if p := plan(env); !slices.Contains(p.BootstrapArgv, entrypoint.ForkBuildsEnv+"="+wire) {
		t.Errorf("the bootstrap is not told the fork decisions: %q", p.BootstrapArgv)
	}
	for _, a := range plan(jsonx.NewOrderedMap()).BootstrapArgv {
		if strings.HasPrefix(a, entrypoint.ForkBuildsEnv+"=") {
			t.Errorf("a launch with no fork told the bootstrap %q", a)
		}
	}
}
