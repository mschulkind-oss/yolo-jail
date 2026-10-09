package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// macoslog_test.go pins the macos-user half of the macos-log loophole (packs/macos-log): the
// unified log is read on the host by the bridge and reached from the sandbox with the staged
// `yolo-log` client. The in-sandbox wrapper the retired `macos_log` key dialled is gone, and
// with it the bootstrap variable that carried the key.

// A launch whose session env carries the macos-log endpoint stages `yolo-log` into the guest,
// through the plan a real launch builds; one without it names no such client.
func TestAMacosLogEndpointStagesTheYoloLogClient(t *testing.T) {
	src := "/opt/homebrew/Cellar/yolo-jail/1.0/share/yolo-jail/bin/darwin-arm64"
	build := func(env *jsonx.OrderedMap) RunPlan {
		return BuildRunPlanWithDaemons(probeWS, jsonx.NewOrderedMap(), []string{"claude"},
			[]string{"claude"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, env,
			mockDarwin(), nil, JailDaemons{GuestBinSource: src}, FloorStage{}, PlanSession{})
	}
	env := jsonx.NewOrderedMap()
	env.Set(paths.MacosLogEndpointEnv, "/private/tmp/yolo-host-services-x/macos-log.endpoint")
	plan := build(env)
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("a macos-log plan fails its invariants:\n%s", strings.Join(problems, "\n"))
	}
	if got := strings.Join(plan.GuestClients, ","); got != "yolo-log" {
		t.Errorf("GuestClients = %q, want yolo-log", got)
	}
	staged := false
	for _, c := range plan.StageCommands {
		if len(c) == 4 && c[0] == mvBin && c[3] == GuestBinaryPath("yolo-log", "") {
			staged = true
		}
	}
	if !staged {
		t.Errorf("yolo-log is not renamed into the guest prefix:\n%v", plan.StageCommands)
	}
	if got := build(jsonx.NewOrderedMap()).GuestClients; len(got) != 0 {
		t.Errorf("a launch with no client endpoint names clients %v", got)
	}
}

// The bootstrap no longer hears about the retired key: nothing reads YOLO_DARWIN_MACOS_LOG, and a
// config still carrying `macos_log` (a jail snapshot from an older launcher) must not revive it.
func TestTheDarwinBootstrapEnvCarriesNoMacosLogMode(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set("macos_log", "full")
	p := BuildRunPlan("/Users/Shared/yolo/proj", cfg, []string{"claude"}, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), nil, nil)
	for _, a := range p.BootstrapArgv {
		if strings.HasPrefix(a, "YOLO_DARWIN_MACOS_LOG=") {
			t.Errorf("the bootstrap argv still carries %s", a)
		}
	}
}
