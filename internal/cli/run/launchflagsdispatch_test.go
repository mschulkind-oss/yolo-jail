package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// LAUNCH FLAGS REACH macos-user. packload.InjectLaunchFlags used to be called inside
// runContainer, which this arm returns before reaching, so a `launch` contribution did
// nothing at all natively. copilot's `--yolo --no-auto-update` is a plain launch
// contribution with no autonomy config half to fall back on — a 100% drop — and the
// comment at the old call site said the in-jail launcher would reapply them, which is
// false: both templates end `exec "$REAL_BIN" "$@"` and never read the contributions.
//
// Asserted at the SEAM the backend actually receives, not on the injector: the argv
// handed to MacosUserRun is what the sandbox execs, so this fails if the hoist is
// reverted and passes only if the flags genuinely arrive.
func TestLaunchFlagsReachTheMacosUserBackend(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["copilot"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = []string{"copilot"}

	var got []string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _ []string, agentArgv []string, _, _, _ string, _ bool, _ *jsonx.OrderedMap) int {
		got = agentArgv
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}

	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--yolo") {
		t.Errorf("the copilot pack's launch flags never reached the macos-user backend.\n"+
			"got argv: %v\n"+
			"Nothing downstream recovers this: the in-jail launcher templates end\n"+
			"exec \"$REAL_BIN\" \"$@\" and never read LaunchFlagContributions.", got)
	}
}

// The empty case must still reach each arm's OWN default rather than being injected
// into — a bare `yolo` is an interactive zsh natively and bash in a container, and
// injecting into an empty argv would invent a binary neither arm asked for.
func TestBareLaunchStillGetsTheBackendDefault(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["copilot"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = nil

	var got []string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _ []string, agentArgv []string, _, _, _ string, _ bool, _ *jsonx.OrderedMap) int {
		got = agentArgv
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	if len(got) == 0 || got[0] != "/bin/zsh" {
		t.Errorf("a bare launch no longer opens the native default shell: %v", got)
	}
}
