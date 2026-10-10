package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// TestMacosUserPasteboardProbe RECORDS whether a process in the macos-user sandbox can read the
// pasteboard of the user who launched it, and asserts nothing about the answer. It is measurement
// M1 of docs/design/image-paste.md, the input OQ-PA1 waits on: the session Seatbelt profile is
// `(allow default)` with no deny of the pasteboard's mach service, so whether the sandbox account
// (`_yolojail`, entered through sudo from the user's own session) reaches the user's pasteboard
// depends on which bootstrap namespace it inherits, which nobody had measured. If it can, every
// password the user copies is readable from every macos-user jail.
//
// What it does:
//
//   - on the host, as the test's own user and outside any sandbox, it puts a fresh TEXT marker and
//     a fresh 1x1 PNG on the general pasteboard as ONE item carrying both types, so one launch can
//     ask for both. It sets them through NSPasteboard (an `osascript -l JavaScript` script), since
//     `pbcopy` and `set the clipboard to` each replace the whole pasteboard and cannot leave both;
//   - THE CONTROL: still on the host, it reads both back with the three methods the sandbox will
//     use. A control that does not get its own marker back makes the measurement INCONCLUSIVE and
//     fails the test, because a "no" from the sandbox then says nothing. ⚠ A CI runner may have no
//     window server or pasteboard session at all (a runner started as a daemon rather than in a
//     logged-in Aqua session); the control is what tells that apart from a sandbox that is denied;
//   - inside one macos-user session it runs `pbpaste`, `osascript -e 'the clipboard as text'` and
//     `osascript -e 'the clipboard as «class PNGf»'`, each bounded, and logs one MEASUREMENT line
//     per method: whether the marker came back, the exit code, its stdout and its stderr. Only
//     STDOUT is judged, here and in the control: macOS prints harmless warnings on stderr (a
//     runner VM's `IOServiceMatchingfailed for: AppleM2ScalerParavirtDriver`, for one), and a
//     comparison over merged output read a successful read as a failed one;
//   - it restores the text the pasteboard held before it started. Only TEXT is restored: an image
//     or file the runner's pasteboard held is not, and an empty or non-text pasteboard is left
//     holding an empty string so the marker does not linger.
//
// Neither outcome fails the test. It never runs an agent.
//
// ⚠ A GitHub-hosted runner is not a Terminal launch by a person at the keyboard, and whether the
// two inherit the same bootstrap namespace is not known. An answer here is an input for OQ-PA1,
// not the whole measurement.
func TestMacosUserPasteboardProbe(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{}`)

	marker := "yolo-pasteboard-probe-" + randomHex(t, 12)
	pngBytes := probePNG(t)
	pngHex := strings.ToUpper(hex.EncodeToString(pngBytes))
	pngPath := filepath.Join(resolvedTempDir(t), "probe.png")
	if err := os.WriteFile(pngPath, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	original, _, origErr := hostPasteboard(t, "", "pbpaste")
	t.Cleanup(func() {
		restore := original
		if origErr != nil {
			restore = ""
		}
		if _, _, err := hostPasteboard(t, restore, "pbcopy"); err != nil {
			t.Logf("could not restore the pasteboard's original text: %v", err)
		}
	})

	jxa := filepath.Join(resolvedTempDir(t), "set-pasteboard.js")
	if err := os.WriteFile(jxa, []byte(pasteboardSetJXA), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, errOut, err := hostPasteboard(t, "", "osascript", "-l", "JavaScript", jxa, marker, pngPath); err != nil {
		t.Fatalf("INCONCLUSIVE: the host could not set its own pasteboard (%v), so the sandbox's "+
			"answer would mean nothing. This runner probably has no window server or pasteboard "+
			"session; run this test from a logged-in GUI session (a Terminal on a Mac).\n"+
			"stdout: %q\nstderr: %q", err, out, errOut)
	}
	var control []string
	for _, m := range pasteboardMethods {
		out, errOut, err := hostPasteboard(t, "", m.argv[0], m.argv[1:]...)
		if !m.matches(out, marker, pngHex) {
			control = append(control, fmt.Sprintf("%s: err %v, stdout %q, stderr %q",
				m.name, err, clip(out), clip(errOut)))
			continue
		}
		t.Logf("control: the host read its own pasteboard via %s (stderr %q)", m.what, clip(errOut))
	}
	if len(control) > 0 {
		t.Fatalf("INCONCLUSIVE: the host, unsandboxed, could not read back what it put on its own "+
			"pasteboard, so a \"no\" from the sandbox would be false. This runner probably has no "+
			"window server or pasteboard session; run this test from a logged-in GUI session.\n%s",
			strings.Join(control, "\n"))
	}

	r := macosUserRunProbe(t, "pasteboard", ws, pasteboardProbeScript(pasteboardProbeStepSeconds))
	probe := section(r.stdout, "=== PASTEBOARD ===", "=== END ===")
	lines, steps := pasteboardVerdicts(probe, marker, pngHex)
	if steps == 0 {
		t.Fatalf("the probe printed none of its steps, so nothing was measured:\nstdout:\n%s\nstderr:\n%s",
			r.stdout, r.stderr)
	}
	for _, l := range lines {
		t.Logf("MEASUREMENT (image-paste.md M1, OQ-PA1), as %s inside the sandbox: %s",
			macosuser.SandboxUser, l)
	}
	t.Logf("the probe's raw output:\n%s", probe)
}

// pasteboardMethod is one way to read the pasteboard, run identically by the host's control and
// inside the sandbox, and the test of whether its output carries the probe's marker.
type pasteboardMethod struct {
	name string // the step's name in the probe's output
	what string // how the MEASUREMENT line names it
	argv []string
	png  bool // the marker is the PNG's bytes, not the text
}

// matches judges a method's STDOUT alone; stderr must never reach it. The text marker counts
// when it is a whole line of stdout, once trimmed, so a warning a tool prints on stdout before
// it cannot hide a successful read; the marker is random, so no other line can equal it. The
// PNG counts when its bytes, hex-encoded as osascript prints «data PNGf…», appear in stdout.
func (m pasteboardMethod) matches(stdout, marker, pngHex string) bool {
	if m.png {
		return strings.Contains(strings.ToUpper(stdout), pngHex)
	}
	for _, l := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(l) == marker {
			return true
		}
	}
	return false
}

var pasteboardMethods = []pasteboardMethod{
	{name: "pbpaste", what: "pbpaste", argv: []string{"pbpaste"}},
	{name: "osascript-text", what: "osascript `the clipboard as text`",
		argv: []string{"osascript", "-e", "the clipboard as text"}},
	{name: "osascript-png", what: "osascript `the clipboard as «class PNGf»`",
		argv: []string{"osascript", "-e", "the clipboard as «class PNGf»"}, png: true},
}

// pasteboardVerdicts reads the probe's output: one line per method saying whether the sandbox got
// the marker back, with its exit code, stdout and stderr, and how many steps the probe printed at
// all. A step's stdout lines are printed as "  | " and its stderr lines as "  ! ".
func pasteboardVerdicts(probe, marker, pngHex string) (lines []string, steps int) {
	steps = strings.Count("\n"+probe, "\nSTEP ")
	for _, m := range pasteboardMethods {
		head := "STEP " + m.name + " rc="
		i := strings.Index(probe, head)
		if i < 0 {
			lines = append(lines, fmt.Sprintf("sandbox read the host pasteboard via %s: not run "+
				"(the probe printed no %s step)", m.what, m.name))
			continue
		}
		rest := probe[i+len(head):]
		rc, body, _ := strings.Cut(rest, "\n")
		if j := strings.Index(body, "\nSTEP "); j >= 0 {
			body = body[:j]
		} else if strings.HasPrefix(body, "STEP ") {
			body = ""
		}
		var out, errOut strings.Builder
		for _, l := range strings.Split(body, "\n") {
			if v, ok := strings.CutPrefix(l, "  | "); ok {
				out.WriteString(v + "\n")
			} else if v, ok := strings.CutPrefix(l, "  ! "); ok {
				errOut.WriteString(v + "\n")
			}
		}
		got := out.String()
		verdict := "no"
		if m.matches(got, marker, pngHex) {
			verdict = "yes"
		}
		lines = append(lines, fmt.Sprintf("sandbox read the host pasteboard via %s: %s (rc %s, stdout %q, stderr %q)",
			m.what, verdict, strings.TrimSpace(rc), clip(got), clip(errOut.String())))
	}
	return lines, steps
}

// pasteboardProbeStepSeconds bounds each read inside the sandbox. A pasteboard read that is
// allowed answers at once; one waiting on a service it cannot reach must end well inside the
// launch's deadline.
const pasteboardProbeStepSeconds = 20

// pasteboardProbeScript is the probe's shell, run by the sandbox's login bash. Each step runs
// with an empty-pipe stdin (an isatty(0) on /dev/null is a denied ioctl under the session
// profile: macosuserseatbelt_test.go's header) and under bash's own watchdog, a stock macOS
// having no timeout(1); its stdout and its stderr go to separate files and are copied with awk,
// stdout lines prefixed "  | " and stderr lines "  ! ", so every STEP marker begins a line and
// only stdout is judged. The marker is NOT in the script: a step's output can only carry it by reading
// the pasteboard.
func pasteboardProbeScript(stepSeconds int) string {
	var b strings.Builder
	b.WriteString(strings.ReplaceAll(pasteboardProbeHead, "__BOUND__", strconv.Itoa(stepSeconds)))
	for _, m := range pasteboardMethods {
		b.WriteString("step " + m.name)
		for _, a := range m.argv {
			b.WriteString(" '" + strings.ReplaceAll(a, "'", `'\''`) + "'")
		}
		b.WriteString("\n")
	}
	b.WriteString("echo \"=== END ===\"\n")
	return b.String()
}

const pasteboardProbeHead = `
echo "=== PASTEBOARD ==="
echo "WHOAMI=$(id -un)"
pbdir=$(mktemp -d "${TMPDIR:-/tmp}/yolo-pb-probe.XXXXXX")
trap 'rm -rf "$pbdir"' EXIT
step() {
    name=$1 secs=__BOUND__
    shift
    out="$pbdir/$name.out" err="$pbdir/$name.err"
    # No EXIT trap for the forked helpers to inherit: only this shell removes $pbdir
    # (macosuserkeychain_test.go's probe states the race).
    trap - EXIT
    printf '' | "$@" >"$out" 2>"$err" &
    pid=$!
    ( sleep "$secs"; kill -TERM "$pid" 2>/dev/null; sleep 2; kill -KILL "$pid" 2>/dev/null ) >/dev/null 2>&1 &
    dog=$!
    trap 'rm -rf "$pbdir"' EXIT
    rc=0
    wait "$pid" || rc=$?
    kill "$dog" 2>/dev/null
    wait "$dog" 2>/dev/null
    echo "STEP $name rc=$rc"
    awk '{ print "  | " $0 }' "$out"
    awk '{ print "  ! " $0 }' "$err"
}
`

// pasteboardSetJXA puts argv[0] as text and the PNG file argv[1] on the general pasteboard as
// one item carrying both types (public.utf8-plain-text and public.png, which AppleScript reads as
// «class PNGf»), and fails when either write is refused.
const pasteboardSetJXA = `ObjC.import('AppKit');
function run(argv) {
    var pb = $.NSPasteboard.generalPasteboard;
    pb.clearContents;
    var data = $.NSData.dataWithContentsOfFile(argv[1]);
    if (!data || data.isNil()) { throw new Error('cannot read ' + argv[1]); }
    if (!pb.setStringForType($(argv[0]), $.NSPasteboardTypeString)) { throw new Error('setString refused'); }
    if (!pb.setDataForType(data, $.NSPasteboardTypePNG)) { throw new Error('setData refused'); }
    return 'ok';
}
`

// hostPasteboard runs one pasteboard command on the host, outside any sandbox, with stdin as
// its input, bounded so a pasteboard with no session behind it cannot hang the job. It returns
// stdout and stderr apart: only stdout is the pasteboard's content (and what the cleanup restores).
func hostPasteboard(t *testing.T, stdin, name string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

// probePNG is a 1x1 PNG of a random color, so its bytes are this run's own.
func probePNG(t *testing.T) []byte {
	t.Helper()
	var c [3]byte
	if _, err := rand.Read(c[:]); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: c[0], G: c[1], B: c[2], A: 0xff})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// clip shortens a step's output for a MEASUREMENT line; the raw output is logged whole after.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// TestMacosUserPasteboardProbeShellRunsAgainstAStandIn runs the probe's shell on any machine,
// against stand-in `pbpaste` and `osascript` first on PATH, and reads it with the parser the Mac
// test uses: pbpaste answers the marker after a warning line on stderr (the shape macOS printed
// on a CI runner, which a merged-output comparison misread as a failed read), the text read
// fails on stderr, and the PNG read hangs past a one-second bound so the watchdog must end it. Not behind requireMacosUser's skip, so it runs
// under -short on Linux.
func TestMacosUserPasteboardProbeShellRunsAgainstAStandIn(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	marker, pngHex := "yolo-pasteboard-probe-standin", "89504E470D0A1A0A"
	bin := resolvedTempDir(t)
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("pbpaste", "echo 'IOServiceMatchingfailed for: AppleM2ScalerParavirtDriver' >&2\nprintf '%s' \"$PB_MARKER\"\n")
	write("osascript", `case "$2" in
  *PNGf*) exec sleep 30 ;;
  *) echo "execution error: Can't make some data into the expected type. (-1700)" >&2; exit 1 ;;
esac
`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, "-c", pasteboardProbeScript(1))
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PB_MARKER="+marker, "TMPDIR="+resolvedTempDir(t))
	begun := time.Now()
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the probe's shell failed: %v\n%s", err, raw)
	}
	if took := time.Since(begun); took > 15*time.Second {
		t.Errorf("the probe took %s against a one-second bound: a step was not bounded\n%s", took, raw)
	}
	if strings.Contains(pasteboardProbeScript(1), marker) {
		t.Fatal("the probe's script carries the marker, so its output could echo it without reading the pasteboard")
	}
	probe := section(string(raw), "=== PASTEBOARD ===", "=== END ===")
	lines, steps := pasteboardVerdicts(probe, marker, pngHex)
	if steps != len(pasteboardMethods) {
		t.Errorf("the probe printed %d steps, want %d:\n%s", steps, len(pasteboardMethods), raw)
	}
	want := []string{
		"via pbpaste: yes (rc 0, stdout \"" + marker + "\", stderr \"IOServiceMatchingfailed",
		"via osascript `the clipboard as text`: no (rc 1, stdout \"\", stderr \"execution error",
		"via osascript `the clipboard as «class PNGf»`: no (rc 14", // 143 or 137, the watchdog's
	}
	for i, w := range want {
		if i >= len(lines) || !strings.Contains(lines[i], w) {
			t.Errorf("verdict %d does not contain %q:\n%s\nraw:\n%s", i, w, strings.Join(lines, "\n"), raw)
		}
	}
	if len(lines) > 2 && !watchdogEnded(probe, "osascript-png") {
		t.Errorf("the hanging PNG read was not ended by the watchdog (want rc 143 or 137):\n%s", probe)
	}
}

// TestPasteboardMethodMatchesJudgesTheMarkerLine pins the text comparison the host control and
// the sandbox verdicts share: the marker counts as a whole line of stdout, so a warning line
// before it (the merged output CI printed, which an exact comparison called a failed read) does
// not hide it, while a line merely containing the marker does not count.
func TestPasteboardMethodMatchesJudgesTheMarkerLine(t *testing.T) {
	marker := "yolo-pasteboard-probe-0ac899290e89bfdfbd1126c9"
	text := pasteboardMethods[1]
	for _, c := range []struct {
		stdout string
		want   bool
	}{
		{marker, true},
		{marker + "\n", true},
		{"IOServiceMatchingfailed for: AppleM2ScalerParavirtDriver\n" + marker, true},
		{"  " + marker + "  \nwarning after\n", true},
		{"", false},
		{"IOServiceMatchingfailed for: AppleM2ScalerParavirtDriver", false},
		{"prefix " + marker, false},
		{marker[:len(marker)-1], false},
	} {
		if got := text.matches(c.stdout, marker, ""); got != c.want {
			t.Errorf("matches(%q) = %v, want %v", c.stdout, got, c.want)
		}
	}
	pngHex := "89504E470D0A1A0A"
	if !pasteboardMethods[2].matches("warning\n«data PNGf89504e470d0a1a0a»\n", "", pngHex) {
		t.Error("the PNG read does not match its own bytes after a warning line")
	}
}
