package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE CALL-SITE PIN for the Environment block's backend arm. internal/jailcontent proves it
// RENDERS a native-backend Environment block when told the mechanism and the home; this
// proves the run path actually TELLS it, which a test of the renderer alone cannot see.
//
// Home is the half that is easy to lose: Mechanism has been threaded since OQ-DP2, but Home
// arrived later and is the only input the briefing cannot derive for itself — jailcontent
// cannot import internal/macosuser without routing through internal/entrypoint, whose tests
// import jailcontent back. Delete `Home: nativeHomeFor(rt)` from prepare.go and the briefing
// silently tells a macos-user agent its home is /home/agent.
func TestTheMacosUserBriefingNamesTheSandboxHome(t *testing.T) {
	// HOME is the workspace's own home in both modes (MMP-D5, MMP-D25), never the account's
	// machine root, which every workspace shares.
	for _, mode := range []macosuser.Mode{macosuser.ModeAccount, macosuser.ModeSelf} {
		var ws string
		got := macosUserBriefingWith(t, appliedTestConfig("packages", []any{}), func(o *Options) {
			o.MacosUserMode, o.MacosUserHostHome = mode, "/Users/you"
			ws = o.Workspace
		})
		if want := "- **Home**: `" + paths.SetupHomeState(ws, "seatbelt-self") + "`"; !strings.Contains(got, want) {
			t.Errorf("%s: the briefing does not name the sandbox home (%q).\n\nA macos-user agent is "+
				"told its home is somewhere it does not live, on the one surface it reads first.\n\n%s",
				mode, want, got)
		}
		if strings.Contains(got, "- **Home**: `"+macosuser.SandboxHome()+"`") {
			t.Errorf("%s: the briefing names the account's shared home as the agent's", mode)
		}
	}
	got := macosUserBriefing(t, appliedTestConfig("packages", []any{}))
	if strings.Contains(got, "- **Home**: `/home/agent`") {
		t.Errorf("the briefing still names the CONTAINER's home on a backend that has no "+
			"container:\n\n%s", got)
	}
	// The workspace half, from the same wiring: the alias paragraph must be gone and the
	// absence named, because the built-in skills keep saying `/workspace`.
	if strings.Contains(got, "`/workspace` is the host directory") {
		t.Errorf("the briefing still explains a bind mount this backend never makes:\n\n%s", got)
	}
	if !strings.Contains(got, "There is no `/workspace` on this backend") {
		t.Errorf("the briefing does not tell the agent that `/workspace` is absent — the one "+
			"place it learns that the built-in skills' 25 references mean its own path:\n\n%s", got)
	}
}
