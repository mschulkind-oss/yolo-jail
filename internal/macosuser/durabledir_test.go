package macosuser

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE DURABLE DIR REACHES BOTH HALVES OF A macos-user LAUNCH: the agent through the session
// env file (the launch env the run pipeline composed), and the bootstrap, whose launch line
// reports on the directory, through the relay in buildBootstrapEnv — and neither when the
// launch did not make it (docs/design/durable-scratch-space.md §5.2, §5.4).
func TestTheDurableDirReachesTheAgentAndTheBootstrap(t *testing.T) {
	const ws = "/Users/Shared/yolo/proj"
	dir := ws + "/.yolo/durable"
	plan := func(env *jsonx.OrderedMap) RunPlan {
		return BuildRunPlan(ws, jsonx.NewOrderedMap(), []string{"claude"}, []string{"/bin/zsh", "-l"},
			"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, env, nil, nil)
	}
	env := jsonx.NewOrderedMap()
	env.Set(durable.EnvVar, dir)
	p := plan(env)
	pair := durable.EnvVar + "=" + dir
	if !slices.Contains(p.BootstrapArgv, pair) {
		t.Errorf("the bootstrap is not told the durable dir (%s): %q", pair, p.BootstrapArgv)
	}
	if !strings.Contains(p.EnvFileContent, durable.EnvVar) || !strings.Contains(p.EnvFileContent, dir) {
		t.Errorf("the agent's session env does not carry the durable dir:\n%s", p.EnvFileContent)
	}

	p = plan(jsonx.NewOrderedMap())
	for _, a := range p.BootstrapArgv {
		if strings.HasPrefix(a, durable.EnvVar+"=") {
			t.Errorf("a launch with no durable dir told the bootstrap %q", a)
		}
	}
}
