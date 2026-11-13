package check

// pathcolor_test.go pins the Group C polish of docs/plans/cli-visual-polish.md for `yolo check`:
// a path that is a finding's value is cyan, and a running jail's ` -> workspace` tail is dim, so
// the label and the jail name are what the eye lands on. Color is additive: stripped, every line
// is the plain report byte for byte, and the recorded (JSON) message never carries an escape.

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestOkPathColorsThePathAndKeepsThePlainBytes(t *testing.T) {
	var plain, colored bytes.Buffer
	pr, cr := newReporter(&plain, false), newReporter(&colored, true)
	pr.okPath("Parsed user config: ", "/h/.config/yolo-jail/config.toml", " (x)")
	cr.okPath("Parsed user config: ", "/h/.config/yolo-jail/config.toml", " (x)")

	if want := "  [PASS] Parsed user config: /h/.config/yolo-jail/config.toml (x)\n"; plain.String() != want {
		t.Errorf("plain okPath = %q, want %q", plain.String(), want)
	}
	if !strings.Contains(colored.String(), ansiCyan+"/h/.config/yolo-jail/config.toml"+ansiReset) {
		t.Errorf("colored okPath does not cyan the path: %q", colored.String())
	}
	if got := stripANSI(colored.String()); got != plain.String() {
		t.Errorf("stripped colored okPath = %q, want the plain %q", got, plain.String())
	}
	if cr.passed != 1 || len(cr.findings) != 1 ||
		cr.findings[0].Message != "Parsed user config: /h/.config/yolo-jail/config.toml (x)" {
		t.Errorf("okPath recorded %+v (passed %d), want one plain pass", cr.findings, cr.passed)
	}
}

// The call site, not only the helper: the user-config finding of a real section goes through
// okPath, so deleting that routing fails here.
func TestTheUserConfigFindingColorsItsPath(t *testing.T) {
	var out bytes.Buffer
	o := baseOptions(t, &out)
	o.Color = true
	o.IsTTYStdout = func() bool { return true }
	t.Setenv("NO_COLOR", "")
	Check(o)
	if !strings.Contains(out.String(), "No user config found: "+ansiCyan) {
		t.Errorf("the user-config finding's path is not cyan:\n%s", out.String())
	}
}

func TestARunningJailsWorkspaceTailIsDim(t *testing.T) {
	ws := t.TempDir()
	exec := func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		switch {
		case len(argv) > 1 && argv[1] == "ps":
			return ExecResult{Ran: true, Stdout: "yolo-001-feedface\t2 hours ago\n"}
		case len(argv) > 1 && argv[1] == "inspect":
			return ExecResult{Ran: true, Stdout: "YOLO_HOST_DIR=" + ws + "\n"}
		}
		return ExecResult{Ran: true}
	}
	run := func(color bool) string {
		var out bytes.Buffer
		o := &Options{IsTTYStdout: func() bool { return false }, Stdin: strings.NewReader(""), Exec: exec}
		o.sectionRunningJails(newReporter(&out, color), "podman")
		return out.String()
	}
	plain, colored := run(false), run(true)
	if !strings.Contains(plain, "    yolo-001-feedface -> "+ws) {
		t.Fatalf("plain running-jail row changed:\n%s", plain)
	}
	if !strings.Contains(colored, "    yolo-001-feedface"+ansiDim+" -> "+ws+ansiReset) {
		t.Errorf("the workspace tail is not dim:\n%q", colored)
	}
	if stripANSI(colored) != plain {
		t.Errorf("stripped colored section differs from the plain one:\n%q\n%q", stripANSI(colored), plain)
	}
}
