package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// macosUserStorePackagesOutput drives Run down the macos-user arm with the store-delivery
// dial set to val, and returns the launch's output plus the handler's reach.
func macosUserStorePackagesOutput(t *testing.T, val string) (string, bool) {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Getenv = func(k string) string {
		switch k {
		case "YOLO_RUNTIME":
			return "macos-user"
		case StorePackagesOptInEnv:
			return val
		}
		return ""
	}
	reached := false
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, string, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool) int {
		reached = true
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	return stdout.String() + stderr.String(), reached
}

// YOLO_STORE_PACKAGES=1 on macos-user used to vanish without a line: the macos-user arm
// returns before planStorePackages, the dial's only reader, so the one ineligible setup
// that said nothing was this one (docs/plans/setup-support-gaps.md G10,
// docs/reference/settings-per-setup.md's "n/a — silent" cell).
//
// A NOTICE, NOT A REFUSAL. The ruling that governs this dial is planStorePackages' own
// (docs/reference/image-staging-vs-baking.md, "falls back ... and says so"): a requested
// but ineligible launch is never refused over where bytes live, only never silent. And
// here there is nothing to refuse: macos-user has no image, and its `packages:` already
// come from the nix store — the outcome the dial asks for is this backend's only mode.
func TestMacosUserSaysStorePackagesDialIsIgnored(t *testing.T) {
	got, reached := macosUserStorePackagesOutput(t, "1")
	if !reached {
		t.Fatalf("the macos-user handler was not reached — the dial must not refuse the "+
			"launch:\n%s", got)
	}
	var notice string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, StorePackagesOptInEnv+"=1 ignored") {
			notice = line
			break
		}
	}
	if notice == "" {
		t.Fatalf("a macos-user launch with %s=1 said nothing about the dial, which this "+
			"backend never reads:\n%s", StorePackagesOptInEnv, got)
	}
	// On the notice LINE itself: the rest of a macos-user launch's output names the
	// backend anyway, so a whole-output match would pass a notice that never says it.
	if !strings.Contains(notice, "macos-user") {
		t.Errorf("the notice does not name the backend it is about:\n%s", notice)
	}
}

// And nothing for a launch that never set it: a notice about a dial nobody turned is the
// warning people learn to skip.
func TestMacosUserSilentWhenStorePackagesDialUnset(t *testing.T) {
	got, _ := macosUserStorePackagesOutput(t, "")
	if strings.Contains(got, StorePackagesOptInEnv) {
		t.Errorf("a macos-user launch that never set %s mentioned it:\n%s",
			StorePackagesOptInEnv, got)
	}
}
