package loopholes

// statuscolor_test.go pins `yolo loopholes status`'s color (docs/plans/cli-visual-polish.md, Group
// A's first item): with Deps.Color set, each state word is wrapped in its convention's color, the
// loophole's name bold and its rc and doctor output dim; with it unset no escape is written, and
// the colored report with its escapes removed is the plain report byte for byte, which is the
// plan's additive-color invariant. Driven through Status over user-config loopholes whose
// doctor_cmd really runs (`true`, /bin/sh), so the states come from RunDoctorChecks.

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// statusColorConfig is the user config the report is drawn from. `true` is found on PATH, not
// spelled /bin/true: macOS has only /usr/bin/true, and this jail only /bin/true.
func statusColorConfig(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("no `true` on PATH for the doctor_cmd to run: %v", err)
	}
	q, err := json.Marshal(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(`{"loopholes": {
  "good": {"description": "d", "command": [TRUE], "doctor_cmd": [TRUE]},
  "bad": {"description": "d", "command": [TRUE], "doctor_cmd": ["/bin/sh", "-c", "echo 'broken [red] here'; exit 3"]},
  "quiet": {"description": "d", "command": [TRUE]},
  "off": {"description": "d", "command": [TRUE], "doctor_cmd": [TRUE], "enabled": false}
}}`, "TRUE", string(q))
}

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

func runStatus(t *testing.T, color bool) string {
	t.Helper()
	isolateDirs(t)
	unsetJail(t)
	var out, errBuf bytes.Buffer
	deps := cmdDeps(t, &out, &errBuf, statusColorConfig(t), "")
	deps.Color = color
	if rc := Status(deps); rc != 0 {
		t.Fatalf("Status rc = %d, err=%q", rc, errBuf.String())
	}
	return out.String()
}

func TestStatusColorsItsStateVocabulary(t *testing.T) {
	plain, colored := runStatus(t, false), runStatus(t, true)

	if strings.Contains(plain, "\x1b[") || strings.Contains(plain, "[bold]") || strings.Contains(plain, "[/dim]") {
		t.Errorf("the plain report carries an escape or a style tag:\n%s", plain)
	}
	for _, want := range []string{"  [ok] good  rc=0\n", "  [fail] bad  rc=3\n", "  [no-check] quiet  rc=None\n",
		"  [disabled] off  rc=0\n", "      broken [\u2060red] here\n"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the plain report lacks %q:\n%s", want, plain)
		}
	}
	if got := ansiEscape.ReplaceAllString(colored, ""); got != plain {
		t.Errorf("color is not additive: the colored report without its escapes differs from the "+
			"plain one\n--- plain\n%s\n--- colored, stripped\n%s", plain, got)
	}
	for _, want := range []string{
		"\x1b[32m[ok]\x1b[0m \x1b[1mgood\x1b[0m  \x1b[2mrc=0\x1b[0m",
		"\x1b[31m[fail]\x1b[0m \x1b[1mbad\x1b[0m",
		"\x1b[2m[no-check]\x1b[0m \x1b[1mquiet\x1b[0m",
		"\x1b[2m[disabled]\x1b[0m \x1b[1moff\x1b[0m",
		// A style tag in a doctor's output is the doctor's text, not yolo's markup.
		"      \x1b[2mbroken [\u2060red] here\x1b[0m",
	} {
		if !strings.Contains(colored, want) {
			t.Errorf("the colored report lacks %q:\n%q", want, colored)
		}
	}
}

// Every state doctorState can return has a color, so a new state cannot print as `[][state][/]`.
func TestEveryDoctorStateHasAStyle(t *testing.T) {
	for _, state := range []string{"ok", "fail", "inactive", "unapproved", "disabled", "superseded", "no-check"} {
		if doctorStateStyle[state] == "" {
			t.Errorf("doctor state %q has no style", state)
		}
	}
}

// The in-jail short-circuit names the command to run on the host in the identifier color.
func TestStatusInJailColorsTheHostCommand(t *testing.T) {
	isolateDirs(t)
	for _, color := range []bool{false, true} {
		var out, errBuf bytes.Buffer
		deps := cmdDeps(t, &out, &errBuf, "", "")
		deps.InJail, deps.Color = true, color
		if rc := Status(deps); rc != 0 {
			t.Fatalf("Status rc = %d", rc)
		}
		want := "From the host: yolo loopholes status\n"
		if color {
			want = "From the host: \x1b[36myolo loopholes status\x1b[0m\n"
		}
		if !strings.HasSuffix(out.String(), want) {
			t.Errorf("color=%v: in-jail line = %q, want it to end %q", color, out.String(), want)
		}
	}
}
