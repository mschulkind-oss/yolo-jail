package macosuser

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE HOME THE LAUNCH BOUND REACHES THE BOOTSTRAP (named-setups-plan.md SUP-D34, SUP-D35): the
// bootstrap's render keys its capture store to paths.HomeStateEnv (entrypoint's renderTarget,
// render.Target.WithHomeState), and the host-side fold at the session's end reads the store beside
// the setup's home. The session env file cannot carry it: the bootstrap's hydrator takes no YOLO_
// key from that file (hydrateEnvFromSessionEnvFile). Unrelayed, the bootstrap rendered its
// baseline into the legacy .yolo/prism while the fold looked in .yolo/setups/<setup>/prism, and
// TestMacosUserCapturesConfigEditsWhenTheSessionEnds found no overlay sidecar (CI run 42525693335).
func TestTheHomeTheLaunchBoundReachesTheBootstrap(t *testing.T) {
	const ws = "/Users/Shared/yolo/proj"
	const rel = "setups/seatbelt-yolo-account/home"
	plan := func(env *jsonx.OrderedMap) RunPlan {
		return BuildRunPlan(ws, jsonx.NewOrderedMap(), []string{"claude"}, []string{"/bin/zsh", "-l"},
			"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, env, nil, nil)
	}
	env := jsonx.NewOrderedMap()
	env.Set(paths.HomeStateEnv, rel)
	if p := plan(env); !slices.Contains(p.BootstrapArgv, paths.HomeStateEnv+"="+rel) {
		t.Errorf("the bootstrap is not told the home the launch bound: %q", p.BootstrapArgv)
	}
	for _, a := range plan(jsonx.NewOrderedMap()).BootstrapArgv {
		if strings.HasPrefix(a, paths.HomeStateEnv+"=") {
			t.Errorf("a launch that named no home told the bootstrap %q", a)
		}
	}
	if !slices.Contains(LauncherOwnedEnvNames(), paths.HomeStateEnv) {
		t.Errorf("%s is relayed to the bootstrap but not launcher-owned, so an env_sources entry "+
			"could choose the capture store", paths.HomeStateEnv)
	}
}
