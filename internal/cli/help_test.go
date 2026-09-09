package cli

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// TestUsageTextListsCommands guards that the usage text enumerates the
// user-facing commands from the registry (so a new command shows up in help
// automatically) and omits the hidden `internal` namespace.
func TestUsageTextListsCommands(t *testing.T) {
	// Assert on the STRIPPED form: usageText carries rich markup ([bold]/[cyan]),
	// rendered on a TTY and stripped off a pipe. The command names/blurbs are
	// unchanged text, so stripping is byte-stable.
	got := richtext.Strip(usageText())
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

// TestUsageTextStripsToStableText: the rendered-off-TTY form contains no ANSI
// and no leftover style tags (a parity guard for the help output).
func TestUsageTextStripsToStableText(t *testing.T) {
	stripped := richtext.Strip(usageText())
	if strings.Contains(stripped, "\x1b[") {
		t.Error("stripped usage should contain no ANSI escapes")
	}
	if strings.Contains(stripped, "[bold]") || strings.Contains(stripped, "[cyan]") {
		t.Errorf("stripped usage leaked a style tag:\n%s", stripped)
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

// listedCommandNames returns the command names `yolo --help` actually RENDERS,
// parsed out of usageText()'s own output.
//
// Parsing the render rather than reading commandHelp is the whole point. The
// slice is data; the rendered text is the surface an operator sees, and the loop
// in usageText that turns one into the other is a CALL SITE that can be deleted
// or narrowed with a slice-reading test still green. Asserting on the output
// pins both halves at once.
func listedCommandNames(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	inCommands := false
	for _, ln := range strings.Split(richtext.Strip(usageText()), "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "Commands:" {
			inCommands = true
			continue
		}
		if !inCommands {
			continue
		}
		// The block is a run of two-space-indented "name  blurb" lines and ends
		// at the first blank line (the one before "Global options:").
		if trimmed == "" {
			break
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		names[fields[0]] = true
	}
	// Without this, every assertion built on the returned set passes vacuously
	// the moment the render or this parse stops agreeing about the block's shape.
	if len(names) == 0 {
		t.Fatalf("parsed no command lines out of the rendered usage — the "+
			"`Commands:` block moved or this scan is wrong:\n%s", richtext.Strip(usageText()))
	}
	return names
}

// TestEveryRegisteredCommandIsListedInHelp is the REVERSE direction of
// TestUsageListedCommandsAreRegistered, and the standard's enforcement item 2
// (docs/design/self-documenting-cli.md): registry → help, so a command that
// exists and is not advertised fails the build.
//
// This direction is the one that was missing, and its absence is why four
// registered commands sat unlisted — `macos-teardown`, `macos-unshare`,
// `doctor`, `host` — for as long as they did. The forward test could not see
// them: it only ever asked whether each help line named something real, which is
// true of a list that names nothing at all.
//
// The hidden-set escape is deliberately expensive to use. hiddenFromCommandHelp
// lives in help.go (production, beside the surface it describes) rather than
// here, and three clauses below make an entry cost something: it must still be
// registered, it must carry a reason, and the command's name must appear
// SOMEWHERE in the rendered help anyway. So adding a name here to quiet the test
// does not quiet it — the third clause then demands the discoverability the
// list line would have provided.
func TestEveryRegisteredCommandIsListedInHelp(t *testing.T) {
	listed := listedCommandNames(t)
	rendered := richtext.Strip(usageText())

	for _, sub := range slices.Sorted(maps.Keys(registry)) {
		if listed[sub] {
			continue
		}
		reason, hidden := hiddenFromCommandHelp[sub]
		if !hidden {
			t.Errorf("`yolo %s` is a dispatch registry key but `yolo --help` never lists "+
				"it — add it to commandHelp, or to hiddenFromCommandHelp with the reason "+
				"it is deliberately unlisted", sub)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("hiddenFromCommandHelp[%q] has no reason; an unlisted command needs "+
				"one on the record", sub)
		}
		// Hiding a command from the LIST must never hide it from the PAGE.
		if !strings.Contains(rendered, sub) {
			t.Errorf("%q is hidden from the command list, but its name appears nowhere in "+
				"`yolo --help` at all — hiding may not cost discoverability (reason on "+
				"file: %s)", sub, reason)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(hiddenFromCommandHelp)) {
		if _, ok := registry[name]; !ok {
			t.Errorf("hiddenFromCommandHelp names %q, which is not a dispatch registry "+
				"key — a rename left the exception behind", name)
		}
		if listed[name] {
			t.Errorf("%q is BOTH listed in `yolo --help` and marked hidden; one of the two "+
				"is stale", name)
		}
	}

	// The forward direction again, but asserted on the RENDER rather than on the
	// slice — so a hand-written line smuggled into usageText's own body (not via
	// commandHelp) is caught too.
	for _, name := range slices.Sorted(maps.Keys(listed)) {
		if _, ok := registry[name]; !ok {
			t.Errorf("`yolo --help` lists %q, which is not a dispatch registry key", name)
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
