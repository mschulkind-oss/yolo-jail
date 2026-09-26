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

// jailColorOptions are the tmux options the jail border sets that are COLOR and nothing
// else. The border's status line and its "🔒 JAIL <project>" text are the indicator's
// words, and stay under NO_COLOR.
var jailColorOptions = []string{"pane-border-style", "pane-active-border-style"}

// setsOption reports whether any recorded tmux call SETS opt to a value (the
// `set-option -pt <pane> <opt> <value>` spelling; the restore's unset is `-put`).
func setsOption(calls [][]string, opt string) bool {
	for _, c := range calls {
		if len(c) >= 5 && c[0] == "set-option" && c[1] == "-pt" && c[3] == opt {
			return true
		}
	}
	return false
}

// TestTmuxJailBorderHonorsNoColor: the jail border's red is color yolo adds, so it goes
// through the NO_COLOR half of the gate (https://no-color.org) — with NO_COLOR set the
// pane gets the border's status line and text but no red. The unset run is the control.
// Driven through SetupJailIndicator, the entry point runRun calls.
func TestTmuxJailBorderHonorsNoColor(t *testing.T) {
	for _, tc := range []struct {
		noColor   string
		wantColor bool
	}{{"", true}, {"1", false}} {
		f := withFakeTmux(t, map[string]string{"display-message -p #{window_name}": "zsh"})
		t.Setenv("KITTY_PID", "")
		t.Setenv("NO_COLOR", tc.noColor)
		if SetupJailIndicator() == nil {
			t.Fatal("SetupJailIndicator returned no restore func for a tmux pane")
		}
		for _, opt := range jailColorOptions {
			if got := setsOption(f.calls, opt); got != tc.wantColor {
				t.Errorf("NO_COLOR=%q: the jail border sets %s = %v, want %v; calls: %q",
					tc.noColor, opt, got, tc.wantColor, f.calls)
			}
		}
		if !setsOption(f.calls, "pane-border-format") {
			t.Errorf("NO_COLOR=%q: the jail border lost its text; calls: %q", tc.noColor, f.calls)
		}
	}
}

// TestKittyJailTabHonorsNoColor is the kitty half: the tab's red is withheld under
// NO_COLOR, in the setup AND the restore (which resets the color it set, so it has
// nothing to reset), and the "🔒 JAIL <project>" title is set either way.
func TestKittyJailTabHonorsNoColor(t *testing.T) {
	for _, tc := range []struct {
		noColor   string
		wantColor bool
	}{{"", true}, {"1", false}} {
		var calls [][]string
		oldCmd, oldAtty := kittenCmd, isattyStdin
		kittenCmd = func(args ...string) ([]byte, error) {
			calls = append(calls, args)
			return nil, nil
		}
		isattyStdin = func() bool { return true }
		t.Cleanup(func() { kittenCmd, isattyStdin = oldCmd, oldAtty })
		t.Setenv("KITTY_PID", "4242")
		t.Setenv("KITTY_WINDOW_ID", "3")
		t.Setenv("TMUX", "")
		t.Setenv("SM_PROJECT", "yolo-jail")
		t.Setenv("NO_COLOR", tc.noColor)

		restore := SetupJailIndicator()
		if restore == nil {
			t.Fatal("SetupJailIndicator returned no restore func for a kitty tab")
		}
		restore()
		var colors, titles int
		for _, c := range calls {
			switch {
			case len(c) > 0 && c[0] == "set-tab-color":
				colors++
			case len(c) > 0 && c[0] == "set-tab-title":
				titles++
			}
		}
		if want := map[bool]int{true: 2, false: 0}[tc.wantColor]; colors != want {
			t.Errorf("NO_COLOR=%q: kitty got %d set-tab-color calls, want %d; calls: %q",
				tc.noColor, colors, want, calls)
		}
		if titles != 2 {
			t.Errorf("NO_COLOR=%q: kitty got %d set-tab-title calls, want 2 (the jail "+
				"title, then the restore); calls: %q", tc.noColor, titles, calls)
		}
	}
}
