package cli

import (
	"os"
	"strings"
	"testing"
)

// TestACaptureJailHasNoControllingTerminal is the regression for an auto-capture
// that stopped and asked a question.
//
// On a fresh host, `yolo -- claude` auto-captures three programs before the launch
// the user asked for. The codex installer printed
// `Start Codex now? [y/N]` and WAITED — inside a jail nobody was driving, in the
// middle of a launch that was supposed to be automatic.
//
// ⚠ THE OBVIOUS FIX DOES NOT WORK, and the test says so because someone will try
// it: internal/capture's driver already leaves cmd.Stdin nil, so the installer's
// stdin is /dev/null. A vendor installer that has to survive `curl | sh` reads
// /dev/tty instead, and with -t on the container there is one. Removing the pty is
// what actually removes the question.
//
// Source-level, because the alternative is running a real installer in a real
// container to prove a flag is absent. What it pins is the DECISION — that the
// capture launch declares itself non-interactive — which is the thing a future
// edit would drop.
func TestACaptureJailHasNoControllingTerminal(t *testing.T) {
	b, err := os.ReadFile("capturehost.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	for _, want := range []string{
		"opts.IsTTYStdout = func() bool { return false }",
		"opts.IsTTYStdin = func() bool { return false }",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("runCaptureJail no longer sets %q.\n"+
				"Without it assemble.go adds podman's -t, the jail gets a pty, and any vendor "+
				"installer that reads /dev/tty can block a capture forever — auto-capture runs "+
				"three of them before the launch the user typed.", want)
		}
	}

	// Not vacuous: the strings above must be inside runCaptureJail, not anywhere in
	// the file. Bound the search to that function.
	i := strings.Index(src, "func runCaptureJail(")
	if i < 0 {
		t.Fatal("runCaptureJail is gone — this pin has lost its subject and is now vacuous")
	}
	rest := src[i:]
	if j := strings.Index(rest, "\nfunc "); j > 0 {
		rest = rest[:j]
	}
	if !strings.Contains(rest, "IsTTYStdout") || !strings.Contains(rest, "IsTTYStdin") {
		t.Error("the tty predicates are set somewhere in capturehost.go but NOT inside " +
			"runCaptureJail, so a capture launch does not get them")
	}
}
