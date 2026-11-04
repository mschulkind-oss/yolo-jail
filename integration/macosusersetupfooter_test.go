package integration

import (
	"os/user"
	"strings"
	"testing"
)

// A SEATBELT SETUP ON macOS, ASKED OF A REAL SANDBOX (docs/design/named-setups-plan.md slice 9).
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
//   - the briefing names the selected setup: ~/.claude/CLAUDE.md carries the setup header line;
//   - the session is told its setup (YOLO_SETUP=seatbelt-self), exports no YOLO_CONFINEMENT marker,
//     and its footer prints seatbelt-self.
func TestMacosUserSeatbeltSelfLaunchNamesTheSetupInItsBriefingAndFooter(t *testing.T) {
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
		`if grep -Fxq '# YOLO Environment — setup seatbelt-self' "$HOME/.claude/CLAUDE.md" 2>/dev/null; then echo "header|SETUP"; else echo "header|OTHER"; fi`,
		`echo "setup|${YOLO_SETUP:-unset}"`,
		`echo "marker|${YOLO_CONFINEMENT:-unset}"`,
		`echo "footer|$(yolo internal footer --template '{yolo.setup}' </dev/null 2>&1)"`,
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
	if got["header"] != "SETUP" {
		t.Errorf("~/.claude/CLAUDE.md does not carry the selected setup header (%s):\n%s", got["header"], r.combined())
	}
	if got["setup"] != "seatbelt-self" {
		t.Errorf("the session's YOLO_SETUP = %q, want seatbelt-self: the session is not told its "+
			"setup:\n%s", got["setup"], r.combined())
	}
	if got["marker"] != "unset" {
		t.Errorf("the session still exports YOLO_CONFINEMENT=%q, want unset; YOLO_SETUP is the marker:\n%s", got["marker"], r.combined())
	}
	if got["footer"] != "seatbelt-self" {
		t.Errorf("the agent footer's setup in the session = %q, want seatbelt-self:\n%s",
			got["footer"], r.combined())
	}
}
