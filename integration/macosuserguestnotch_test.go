package integration

import (
	"os/user"
	"strings"
	"testing"
)

// A SEATBELT SETUP ON macOS, ASKED OF A REAL SANDBOX (docs/design/named-setups-plan.md slice 4a;
// formerly the guest-notch test, docs/plans/environment-manager-plan.md Phase 7.1).
//
// THE RULE. A launch resolves one named setup (config.SelectSetup), and `seatbelt-self` is the
// Seatbelt confinement run as you: the macos-user backend in its run-as-you mode, at the guest
// notch. The setup is named per launch with YOLO_SETUP, which outranks the user config, so a
// machine that sets a default cannot shadow it. Unit tests drive Run() with Options.IsMacOS set
// and an injected backend (internal/cli/run/setupgate_test.go); this is the Mac's own launch.
//
// WHAT THIS ASKS, of one launch with the claude pack and YOLO_SETUP=seatbelt-self:
//
//   - the command ran as YOU, not the sandbox account, so the setup's run_as reached the
//     backend (seatbelt-yolo-account would run it as the yolo account);
//   - the claude pack's surfaces arrived: ~/.claude/settings.json is in the sandbox home;
//   - the briefing states the guest notch: ~/.claude/CLAUDE.md carries the guest header line;
//   - the session is told its setup (YOLO_SETUP=seatbelt-self), and YOLO_CONFINEMENT=guest stays
//     beside it, as does the footer's `guest`, until slice 9 moves their readers to the setup.
func TestMacosUserSeatbeltSelfLaunchesTheSandbox(t *testing.T) {
	requireMacosUser(t)
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)
	probe := strings.Join([]string{
		`echo "=== SELF ==="`,
		`echo "user|$(id -un)"`,
		`if [ -f "$HOME/.claude/settings.json" ]; then echo "settings|PRESENT"; else echo "settings|ABSENT"; fi`,
		`if grep -Fxq '# YOLO Environment — guest' "$HOME/.claude/CLAUDE.md" 2>/dev/null; then echo "header|GUEST"; else echo "header|OTHER"; fi`,
		`echo "setup|${YOLO_SETUP:-unset}"`,
		`echo "marker|${YOLO_CONFINEMENT:-unset}"`,
		`echo "footer|$(yolo internal footer --template '{yolo.notch}' </dev/null 2>&1)"`,
		`echo "=== END SELF ==="`,
		`echo "=== END ==="`,
	}, "\n")
	args := append(jailRunArgs(), "--", "bash", "-lc", probe)
	r := runCommand(t, ws, args, withTimeout(macosUserTimeout()), withEnv("YOLO_SETUP=seatbelt-self"))
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END ===") {
		t.Fatalf("a YOLO_SETUP=seatbelt-self launch did not run its probe (rc %d): the setup was "+
			"refused, or no backend was selected.\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	got := macosUserHomeProbeFields(t, r.stdout, "SELF")
	if got["user"] != me.Username {
		t.Errorf("the seatbelt-self launch ran as %q, want you (%q) — the setup's run_as did not "+
			"reach the macos-user backend:\n%s", got["user"], me.Username, r.combined())
	}
	if got["settings"] != "PRESENT" {
		t.Errorf("the claude pack's ~/.claude/settings.json did not reach the sandbox home (%s):\n%s",
			got["settings"], r.combined())
	}
	if got["header"] != "GUEST" {
		t.Errorf("~/.claude/CLAUDE.md does not carry the guest header line, so the agent is told "+
			"some other confinement than the one it runs in (%s):\n%s", got["header"], r.combined())
	}
	if got["setup"] != "seatbelt-self" {
		t.Errorf("the session's YOLO_SETUP = %q, want seatbelt-self: the session is not told its "+
			"setup:\n%s", got["setup"], r.combined())
	}
	if got["marker"] != "guest" {
		t.Errorf("the session's YOLO_CONFINEMENT = %q, want guest:\n%s", got["marker"], r.combined())
	}
	if got["footer"] != "guest" {
		t.Errorf("the agent footer's notch in the session = %q, want guest:\n%s",
			got["footer"], r.combined())
	}
}
