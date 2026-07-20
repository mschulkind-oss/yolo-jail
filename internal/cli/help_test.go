package cli

import (
	"strings"
	"testing"
)

// TestUsageTextListsCommands guards that the usage text enumerates the
// user-facing commands from the registry (so a new command shows up in help
// automatically) and omits the hidden `internal` namespace.
func TestUsageTextListsCommands(t *testing.T) {
	got := usageText()
	for _, want := range []string{"run", "check", "ps", "prune", "broker", "loopholes"} {
		if !strings.Contains(got, want) {
			t.Errorf("usageText() missing command %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "internal") {
		t.Errorf("usageText() must not advertise the hidden 'internal' namespace\n%s", got)
	}
	if !strings.Contains(got, "yolo") {
		t.Errorf("usageText() should mention the program name\n%s", got)
	}
}

// TestUsageListedCommandsAreRegistered guards that every command advertised in
// help is actually a dispatch registry key — a rename can't leave a stale help
// line pointing at a nonexistent command.
func TestUsageListedCommandsAreRegistered(t *testing.T) {
	for _, c := range commandHelp {
		if _, ok := registry[c.name]; !ok {
			t.Errorf("commandHelp advertises %q, which is not in the dispatch registry", c.name)
		}
	}
}

// TestMainHelpExitsZero pins the papercut fix: an EXPLICIT --help / -h / help
// prints usage and exits 0 (before the fix they hit the "unknown command"
// branch → exit 1).
func TestMainHelpExitsZero(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		if rc := Main([]string{"yolo", flag}); rc != 0 {
			t.Errorf("Main([yolo %s]) = %d, want 0", flag, rc)
		}
	}
}

// TestRouteDecision pins how Main routes, without executing anything. The
// load-bearing regression: a BARE `yolo` (and `yolo <flags>` with no `--`) must
// route to `run` (interactive jail shell), NOT to help — help is only for an
// explicit help token.
func TestRouteDecision(t *testing.T) {
	cases := map[string]string{
		"":                 "run", // bare yolo → shell, NOT help
		"--new":            "run", // flags-only, no subcommand → run
		"-h":               "help",
		"--help":           "help",
		"help":             "help",
		"check":            "dispatch:check",
		"ps":               "dispatch:ps",
		"-- echo hi":       "dispatch:run", // --→run rewrite
		"--new -- bash":    "dispatch:run",
		"definitely-bogus": "unknown", // a typo'd subcommand errors, not silent run
	}
	for in, want := range cases {
		var args []string
		if in != "" {
			args = strings.Fields(in)
		}
		if got := routeDecision(args); got != want {
			t.Errorf("routeDecision(%q) = %q, want %q", in, got, want)
		}
	}
}
