package run

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func cachedGoodEnv(value string) func(string) string {
	return func(key string) string {
		switch key {
		case "YOLO_RUNTIME":
			return "podman"
		case CachedGoodEnv:
			return value
		default:
			return ""
		}
	}
}

// Run validates the selected owner and passes only that owner to the real fresh-launch build-slot
// request; a helper-only selector test would not pin the launch delivery caller.
func TestFreshLaunchCarriesCachedGoodOwnerIntoBuildSlot(t *testing.T) {
	patchedLaunchHome(t)
	var got string
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.Getenv = cachedGoodEnv("forkpack/tool")
		o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			if req.Forks != nil {
				got = req.Forks.CachedGoodOwner
			}
			return map[string]entrypoint.ForkDelivery{"tool": {Key: "fixture-good-entry"}}, nil
		}
	})
	if got != "forkpack/tool" {
		t.Fatalf("fresh launch passed cached-good owner %q, want forkpack/tool:\n%s", got, printed)
	}
}

// An invalid owner refuses before the build slot or container launch; it cannot turn into a generic
// missing-program bypass or trigger an advance for some other key.
func TestInvalidCachedGoodOwnerRefusesBeforeBuildSlot(t *testing.T) {
	patchedLaunchHome(t)
	called := false
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.Getenv = cachedGoodEnv("unselected/tool")
		o.BuildSlot = func(BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			called = true
			return nil, nil
		}
	})
	if called || argv != nil || !strings.Contains(printed, "not exactly one selected patched program") {
		t.Errorf("invalid selector called the build slot (%v), ran the container (%v), or was not refused:\n%s",
			called, argv != nil, printed)
	}
}

// A selector never changes a running jail. The exact production attach path refuses before its
// environment inspection/exec and names stopping the jail before the fresh recovery invocation.
func TestCachedGoodOwnerRefusesAttach(t *testing.T) {
	patchedLaunchHome(t)
	called := false
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.Getenv = cachedGoodEnv("forkpack/tool")
		cname := yoloruntime.FromWorkspace(o.Workspace)
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			joined := strings.Join(argv, " ")
			switch {
			case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(joined, "name=^/"+cname+"$"):
				return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
			case len(argv) >= 2 && argv[1] == "inspect":
				return ExecResult{Ran: true, RC: 0, Stdout: "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"}
			}
			return ExecResult{Ran: true, RC: 0}
		}
		o.BuildSlot = func(BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			called = true
			return nil, nil
		}
	})
	if called || !strings.Contains(printed, "Refusing cached-good recovery on an attach") ||
		!strings.Contains(printed, "`yolo stop`") || !strings.Contains(printed, CachedGoodEnv+"=forkpack/tool") {
		t.Errorf("attach called the build slot (%v) or lacks the stop/fresh-launch instruction:\n%s", called, printed)
	}
}
