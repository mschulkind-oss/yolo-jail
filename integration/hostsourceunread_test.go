package integration

// hostsourceunread_test.go is docs/design/agent-directory-map.md §6.5 item 2 in a real launch:
// on a home where a dotfiles manager left ~/.pi/agent/settings.json as a link into a deleted
// ~/.dotfiles/pi, a jail launch prints one line naming the link and its target, and the jail
// still starts. The unit tests (internal/cli/run/hostsourceunread_test.go) drive the assembler
// that prints the line; only a launch shows the line reaching the user's stderr and the
// container starting with the source left unmounted.
//
// The same launch carries pi's PI_TELEMETRY, which moved from core's argv into the pi pack's
// `env` (agent-directory-map.md Appendix B): a non-login `sh -c` is the process that never
// sources .bashrc, so it sees the variable only through the env file the entrypoint exports.
//
// The briefing half (`after: "host:.pi/agent/AGENTS.md"`) is not asserted here: in a jail the
// launcher never reads a briefing destination back (mayPrependHostBriefing), so whether that
// line prints would depend on where the suite runs.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The one shell command both launches run. Its markers are read from STDOUT, because the launch
// echoes the command itself to stderr.
const piTelemetryProbe = `echo JAIL_STARTED; echo "PI_TELEMETRY_IS=${PI_TELEMETRY-unset}"`

func TestAJailLaunchNamesADanglingHostSettingsLinkAndStarts(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, `{}`, "pi")

	home := os.Getenv("HOME")
	agent := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".dotfiles", "pi", "settings.json")
	if err := os.Symlink(target, filepath.Join(agent, "settings.json")); err != nil {
		t.Fatal(err)
	}

	r := runYoloDirect(t, dir, "sh", "-c", piTelemetryProbe)

	if r.rc != 0 || !strings.Contains(r.stdout, "JAIL_STARTED") {
		t.Fatalf("a dangling host settings link stopped the jail (rc %d)\nstdout: %s\nstderr: %s",
			r.rc, r.stdout, r.stderr)
	}
	var lines []string
	for _, l := range strings.Split(r.stderr, "\n") {
		if strings.Contains(l, "~/.pi/agent/settings.json") && strings.Contains(l, "was not read") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("want exactly one launch line naming ~/.pi/agent/settings.json, got %d\nstderr: %s",
			len(lines), r.stderr)
	}
	for _, want := range []string{target, "does not exist", "without it"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the launch line does not say %q:\n%s", want, lines[0])
		}
	}
	if !strings.Contains(r.stdout, "PI_TELEMETRY_IS=0") {
		t.Errorf("a jail with the pi pack selected did not give a non-login process PI_TELEMETRY=0\n"+
			"stdout: %s", r.stdout)
	}
}

// And a jail without the pi pack no longer carries pi's variable at all: core set it on every
// container launch until the pi pack took it over. No pack is selected, which is the jail core
// alone makes.
func TestAJailWithoutThePiPackCarriesNoPiTelemetry(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)

	r := runYoloDirect(t, dir, "sh", "-c", piTelemetryProbe)

	if r.rc != 0 || !strings.Contains(r.stdout, "JAIL_STARTED") {
		t.Fatalf("the jail did not start (rc %d)\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "PI_TELEMETRY_IS=unset") {
		t.Errorf("a jail without the pi pack still sets PI_TELEMETRY\nstdout: %s", r.stdout)
	}
}
