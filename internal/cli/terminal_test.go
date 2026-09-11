package cli

import (
	"reflect"
	"strings"
	"testing"
)

// fakeTmux records every argv handed to the tmux seam and answers reads from a
// table keyed on the joined argv. An unlisted read answers empty, which is what a
// real `show-option -p` does for an option with no pane-local override.
type fakeTmux struct {
	reply map[string]string
	calls [][]string
}

func (f *fakeTmux) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	return []byte(f.reply[strings.Join(args, " ")]), nil
}

// restoreArgv returns the single argv the restore func handed to tmux. The restore
// is deliberately ONE invocation with ";" separators, so there is exactly one call
// after the setup calls are dropped.
func (f *fakeTmux) restoreArgv(t *testing.T, setupCalls int) []string {
	t.Helper()
	after := f.calls[setupCalls:]
	if len(after) != 1 {
		t.Fatalf("restore made %d tmux invocations, want 1: %q", len(after), after)
	}
	return after[0]
}

// withFakeTmux installs the seams tmuxSetupJailPane needs and returns the recorder.
func withFakeTmux(t *testing.T, reply map[string]string) *fakeTmux {
	t.Helper()
	f := &fakeTmux{reply: reply}
	oldCmd, oldAtty := tmuxCmd, isattyStdin
	tmuxCmd, isattyStdin = f.run, func() bool { return true }
	t.Cleanup(func() { tmuxCmd, isattyStdin = oldCmd, oldAtty })
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("YOLO_NO_TMUX", "")
	t.Setenv("SM_PROJECT", "yolo-jail")
	return f
}

// TestTmuxRestorePassesASpacedValueAsOneArgument is the regression for the LATCHED
// jail border. The restore used to build one flat argv by joining its commands with
// ";" and then re-splitting each on whitespace, so a saved `pane-border-format` of
// " 🔒 JAIL yolo-jail " arrived as seven arguments and tmux refused the whole list
// with `command set-option: too many arguments (need at most 2)`.
//
// Measured on tmux 3.7b, and the two facts that make it matter: tmux parses a
// ";"-separated list BEFORE executing any of it, so one malformed command discards
// the entire restore; and once a restore is discarded the next launch captures the
// JAIL values as "old", which contain spaces — so every restore after the first
// failure fails the same way and the pane never comes back.
//
// This drives the real tmuxSetupJailPane and asserts on the argv its restore func
// actually emits, rather than on a pure helper the closure could stop calling.
func TestTmuxRestorePassesASpacedValueAsOneArgument(t *testing.T) {
	// A value the USER owns, so the latch breaker leaves it alone and this test
	// keeps measuring the argv construction rather than the healing path. Several
	// spaces and a tmux format expression, which is what a real customized border
	// looks like.
	const userFormat = `#{pane_index} "#{pane_title}" on #{host_short}`
	f := withFakeTmux(t, map[string]string{
		// Both read forms are answered because the presence probe and the value
		// read are different tmux spellings.
		"show-option -pt %7 pane-border-format":  `pane-border-format "` + userFormat + `"`,
		"show-option -pvt %7 pane-border-format": userFormat,
		"display-message -p #{window_name}":      "my window",
		"show-window-option -v automatic-rename": "off",
	})

	restore := tmuxSetupJailPane()
	if restore == nil {
		t.Fatal("tmuxSetupJailPane returned nil restore func")
	}
	setupCalls := len(f.calls)
	restore()

	argv := f.restoreArgv(t, setupCalls)
	i := indexOf(argv, "pane-border-format")
	if i < 0 {
		t.Fatalf("restore argv never names pane-border-format: %q", argv)
	}
	if i+1 >= len(argv) {
		t.Fatalf("pane-border-format is the last argument, so no value follows: %q", argv)
	}
	if got := argv[i+1]; got != userFormat {
		t.Errorf("pane-border-format value arrived as %q, want the whole %q as ONE argument.\nfull argv: %q",
			got, userFormat, argv)
	}
}

// TestTmuxRestoreUnsetsWhatWasNotSet pins the other half: an option with no
// pane-local override is UNSET on the way out (-u), never set to an empty string,
// because the pane must fall back to the window/global value it had before.
func TestTmuxRestoreUnsetsWhatWasNotSet(t *testing.T) {
	f := withFakeTmux(t, map[string]string{
		// Every show-option answers empty => no pane-local override anywhere.
		"display-message -p #{window_name}": "zsh",
	})

	restore := tmuxSetupJailPane()
	if restore == nil {
		t.Fatal("tmuxSetupJailPane returned nil restore func")
	}
	setupCalls := len(f.calls)
	restore()

	argv := f.restoreArgv(t, setupCalls)
	for _, opt := range []string{
		"pane-border-style", "pane-active-border-style",
		"pane-border-status", "pane-border-format",
	} {
		i := indexOf(argv, opt)
		if i < 0 {
			t.Errorf("restore never mentions %s: %q", opt, argv)
			continue
		}
		// The unset spelling carries -u and no value, so the element before the
		// option name is the pane target and the one before that holds the flags.
		if !hasUnsetFlagBefore(argv, i) {
			t.Errorf("%s was restored by SETTING a value instead of unsetting it: %q", opt, argv)
		}
	}
}

// TestTmuxRestoreUnlatchesAPaneLeftDirtyByAPriorRun is the healing half. A launch
// whose restore never ran (signalled, or discarded by the argv bug) leaves yolo's own
// marks on the pane. The next launch must NOT save those as the user's values —
// doing so makes it "restore" the jail border on purpose, and pins it forever.
//
// This is the mechanism that actually unsticks a pane in the wild, so it is asserted
// on the emitted argv rather than on the capture: the border options must be UNSET,
// not re-set to the jail values that were found.
func TestTmuxRestoreUnlatchesAPaneLeftDirtyByAPriorRun(t *testing.T) {
	f := withFakeTmux(t, map[string]string{
		// Exactly the latched state measured on the maintainer's Mac.
		"show-option -pt %7 pane-border-style":         `pane-border-style fg=red,bold`,
		"show-option -pvt %7 pane-border-style":        "fg=red,bold",
		"show-option -pt %7 pane-active-border-style":  `pane-active-border-style fg=red,bold`,
		"show-option -pvt %7 pane-active-border-style": "fg=red,bold",
		"show-option -pt %7 pane-border-status":        `pane-border-status bottom`,
		"show-option -pvt %7 pane-border-status":       "bottom",
		"show-option -pt %7 pane-border-format":        `pane-border-format " 🔒 JAIL yolo-jail "`,
		"show-option -pvt %7 pane-border-format":       " 🔒 JAIL yolo-jail ",
		"display-message -p #{window_name}":            "JAIL",
	})

	restore := tmuxSetupJailPane()
	if restore == nil {
		t.Fatal("tmuxSetupJailPane returned nil restore func")
	}
	setupCalls := len(f.calls)
	restore()

	argv := f.restoreArgv(t, setupCalls)
	for _, opt := range []string{
		"pane-border-style", "pane-active-border-style",
		"pane-border-status", "pane-border-format",
	} {
		i := indexOf(argv, opt)
		if i < 0 {
			t.Errorf("restore never mentions %s: %q", opt, argv)
			continue
		}
		if !hasUnsetFlagBefore(argv, i) {
			t.Errorf("%s was RE-SET to the jail value it found instead of unset — the pane stays latched: %q",
				opt, argv)
		}
	}
	// The window NAME latches the same way: the fixture's window is already called
	// JAIL, so saving it would rename the window to JAIL on every exit forever.
	if i := indexOf(argv, "rename-window"); i >= 0 {
		t.Errorf("restore renames the window back to the JAIL name it found, pinning it: %q", argv)
	}
}

// TestTmuxRestoreKeepsAUsersOwnBorderFormat is the guard on the latch breaker: it
// must recognise only yolo's OWN mark, so a user who set a real pane-border-format
// still gets it back. Without this the healing above would quietly eat customization.
func TestTmuxRestoreKeepsAUsersOwnBorderFormat(t *testing.T) {
	const mine = " 🔒 my own format "
	f := withFakeTmux(t, map[string]string{
		"show-option -pt %7 pane-border-format":  `pane-border-format "` + mine + `"`,
		"show-option -pvt %7 pane-border-format": mine,
		"display-message -p #{window_name}":      "zsh",
	})
	restore := tmuxSetupJailPane()
	if restore == nil {
		t.Fatal("tmuxSetupJailPane returned nil restore func")
	}
	setupCalls := len(f.calls)
	restore()

	argv := f.restoreArgv(t, setupCalls)
	i := indexOf(argv, "pane-border-format")
	if i < 0 || i+1 >= len(argv) {
		t.Fatalf("restore does not restore pane-border-format: %q", argv)
	}
	if argv[i+1] != mine {
		t.Errorf("user's own format was not restored: got %q, want %q\nfull argv: %q", argv[i+1], mine, argv)
	}
}

// TestTmuxRestoreIsOneInvocationWithSemicolonSeparators pins the batching, because
// the whole reason a malformed value was catastrophic is that tmux discards a
// ";"-list wholesale. If this ever becomes one invocation per option, the blast
// radius of a bad value shrinks and the test above stops describing the risk.
func TestTmuxRestoreIsOneInvocationWithSemicolonSeparators(t *testing.T) {
	f := withFakeTmux(t, map[string]string{
		"display-message -p #{window_name}": "zsh",
	})
	restore := tmuxSetupJailPane()
	if restore == nil {
		t.Fatal("tmuxSetupJailPane returned nil restore func")
	}
	setupCalls := len(f.calls)
	restore()

	argv := f.restoreArgv(t, setupCalls)
	if n := countOf(argv, ";"); n < 3 {
		t.Errorf("restore argv has %d %q separators, want >=3 (four border options): %q", n, ";", argv)
	}
	if reflect.DeepEqual(argv, []string{}) {
		t.Error("restore argv is empty")
	}
}

// indexOf is the package's own (dispatch.go) — reused deliberately.

func countOf(ss []string, want string) int {
	n := 0
	for _, s := range ss {
		if s == want {
			n++
		}
	}
	return n
}

// hasUnsetFlagBefore reports whether the tmux flag group preceding argv[i] carries
// the -u (unset) flag. The flags sit two positions back: `set-option <flags> <pane> <opt>`.
func hasUnsetFlagBefore(argv []string, i int) bool {
	for j := i - 1; j >= 0 && j >= i-3; j-- {
		if strings.HasPrefix(argv[j], "-") && strings.Contains(argv[j], "u") {
			return true
		}
	}
	return false
}
