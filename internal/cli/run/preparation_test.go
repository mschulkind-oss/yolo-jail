package run

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestPreparationFreshFlagReachesRealBuildSlot(t *testing.T) {
	slotLaunchHome(t)
	calls := 0
	rc, out := fakePodmanLaunch(t, func(o *Options) {
		o.Update = true
		o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			calls++
			if req.Forks == nil || req.Trees == nil || !req.Forks.FreshUpdate || !req.Trees.FreshUpdate {
				t.Fatalf("fresh launch lost request: %+v", req)
			}
			return nil, map[string]TreeDelivery{req.Trees.Trees[0].Key(): {FreshFailure: "fresh check fixture failed"}}
		}
	})
	if calls != 1 || len(rc) != 0 || !strings.Contains(out, "fresh check fixture failed") {
		t.Fatalf("failed freshness did not stop real launch: argv %v calls %d output %s", rc, calls, out)
	}
}
func TestPreparationFreshOrdinaryForwardingIsLaunchScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, _ := pastaHostOptions(t, "/ws", home, false)
	o.Update = true
	argv := o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil))
	if got, ok := envValue(argv, paths.LaunchUpdateEnv); !ok || got != "1" {
		t.Fatal("fresh authorization never reached readiness")
	}
	o.Update = false
	o.Getenv = func(k string) string {
		if k == paths.LaunchUpdateEnv {
			return "1"
		}
		return ""
	}
	argv = o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil))
	if _, ok := envValue(argv, paths.LaunchUpdateEnv); ok {
		t.Fatal("ambient authorization leaked into ordinary launch")
	}
}

func TestPreparationUpdateAttachRefusesBeforeAdvance(t *testing.T) {
	packHome(t)
	calls := 0
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.Update = true
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			// The setup's jail runs; a jail under the legacy name does not (SUP-D10).
			if len(argv) > 1 && argv[1] == "ps" && !slices.Contains(argv, "name=^/"+o.legacyJailName()+"$") {
				return ExecResult{Ran: true, RC: 0, Stdout: "running\n"}
			}
			return ExecResult{Ran: true, RC: 0}
		}
		o.BuildSlot = func(BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			calls++
			return nil, nil
		}
	})
	if calls != 0 || !strings.Contains(printed, "cannot refresh a running jail on attach") || !strings.Contains(printed, "fresh") {
		t.Fatalf("attach flag advanced or lacked repair: %d %s", calls, printed)
	}
}
