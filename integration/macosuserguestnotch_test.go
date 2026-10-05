package integration

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// THE GUEST NOTCH ON macOS, ASKED OF A REAL SANDBOX (docs/plans/environment-manager-plan.md
// Phase 7.1, EMP-D1 and EMP-D2).
//
// THE RULE. On macOS `confinement: "guest"` IS the macos-user backend: with no `runtime` key and
// no YOLO_RUNTIME the notch alone selects it (config.NotchRuntime), and it renders exactly what
// that backend renders at the jail notch. Unit tests drive Run() with Options.IsMacOS set and an
// injected backend (internal/cli/run/notchgate_test.go); no Mac has launched one.
//
// WHAT THIS ASKS, of one launch with the claude pack, a workspace config naming only the notch,
// and YOLO_RUNTIME CLEARED (runMacosUser sets it, which would select the backend by name and
// prove nothing about the notch):
//
//   - the command ran as the sandbox account, so the launch reached the macos-user backend
//     rather than a container or a refusal;
//   - the claude pack's surfaces arrived: ~/.claude/settings.json is in the sandbox home;
//   - the briefing states the guest notch: ~/.claude/CLAUDE.md carries the guest header line;
//   - so does the agent footer: the session carries YOLO_CONFINEMENT=guest (EMP-D4), and the
//     staged `yolo internal footer` prints `guest` for `{yolo.notch}`, where a jail-notch
//     macos-user session prints `jail` (macosuserfooter_test.go).
func TestMacosUserGuestNotchLaunchesTheSandbox(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{"confinement": "guest"}`)
	probe := strings.Join([]string{
		`echo "=== GUEST ==="`,
		`echo "user|$(id -un)"`,
		`if [ -f "$HOME/.claude/settings.json" ]; then echo "settings|PRESENT"; else echo "settings|ABSENT"; fi`,
		`if grep -Fxq '# YOLO Environment — guest' "$HOME/.claude/CLAUDE.md" 2>/dev/null; then echo "header|GUEST"; else echo "header|OTHER"; fi`,
		`echo "marker|${YOLO_CONFINEMENT:-unset}"`,
		`echo "footer|$(yolo internal footer --template '{yolo.notch}' </dev/null 2>&1)"`,
		`echo "=== END GUEST ==="`,
		`echo "=== END ==="`,
	}, "\n")
	args := append(jailRunArgs(), "--", "bash", "-lc", probe)
	r := runCommand(t, ws, args, withTimeout(macosUserTimeout()), withEnv("YOLO_RUNTIME="))
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END ===") {
		t.Fatalf("a `confinement: \"guest\"` launch with no runtime named did not run its probe "+
			"(rc %d): the notch gate refused it, or no backend was selected.\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	got := macosUserHomeProbeFields(t, r.stdout, "GUEST")
	if got["user"] != macosuser.SandboxUser {
		t.Errorf("the guest launch ran as %q, want the sandbox account %q — it did not reach "+
			"the macos-user backend:\n%s", got["user"], macosuser.SandboxUser, r.combined())
	}
	if got["settings"] != "PRESENT" {
		t.Errorf("the claude pack's ~/.claude/settings.json did not reach the guest's home "+
			"(%s), so the notch launched without rendering the surfaces macos-user renders:\n%s",
			got["settings"], r.combined())
	}
	if got["header"] != "GUEST" {
		t.Errorf("~/.claude/CLAUDE.md does not carry the guest header line, so the agent is "+
			"told some other notch than the one it runs at (%s):\n%s", got["header"], r.combined())
	}
	if got["marker"] != "guest" {
		t.Errorf("the guest session's YOLO_CONFINEMENT = %q, want guest: the launch env did not "+
			"reach the session env file:\n%s", got["marker"], r.combined())
	}
	if got["footer"] != "guest" {
		t.Errorf("the agent footer's notch in the guest session = %q, want guest:\n%s",
			got["footer"], r.combined())
	}
}
