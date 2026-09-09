package cli

// outputformat_test.go is enforcement item 4 of
// docs/reference/self-documenting-cli.md: every state-reporting command accepts
// `--format json` and emits valid, ANSI-free JSON.
//
// It DISPATCHES THROUGH THE REGISTRY rather than calling the engines, because
// the engines were never the risk. Each of these commands has three links —
// parse the flag, carry it into the engine's Options/Deps, encode at the end —
// and a test that hands an engine a Format field it constructed itself passes
// with the middle link deleted. Measured: removing `deps.Format = format` from
// runLoopholes leaves every loopholes-package test green.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
)

// TestParseOutputFormatSpellings pins the accepted forms and the refusal.
func TestParseOutputFormatSpellings(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{"default is text", []string{"ps"}, "text", true},
		{"separate value", []string{"ps", "--format", "json"}, "json", true},
		{"glued value", []string{"ps", "--format=json"}, "json", true},
		{"explicit text", []string{"ps", "--format", "text"}, "text", true},
		{"bare --json shorthand", []string{"ps", "--json"}, "json", true},
		// The shorthand exists because `yolo stores --json` shipped it first; an
		// agent that learned it there must not be told it is wrong here.
		{"shorthand alongside other flags", []string{"prune", "--apply", "--json"}, "json", true},
		// Refused, not ignored: silently printing prose to something that asked
		// for JSON is the failure the value parse exists to prevent.
		{"unknown value", []string{"ps", "--format", "yaml"}, "yaml", false},
		{"unknown glued value", []string{"ps", "--format=xml"}, "xml", false},
		{"missing value", []string{"ps", "--format"}, "text", false},
		// After `--` belongs to an inner command.
		{"stops at dash-dash", []string{"ps", "--", "--format", "json"}, "text", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errw bytes.Buffer
			got, ok := parseOutputFormat("ps", tc.args, &errw)
			if got != tc.want || ok != tc.ok {
				t.Errorf("parseOutputFormat(%q) = (%q, %v), want (%q, %v)",
					tc.args, got, ok, tc.want, tc.ok)
			}
			// A refusal must SAY something — an exit 2 with a silent stderr is
			// indistinguishable from a crash.
			if !ok && strings.TrimSpace(errw.String()) == "" {
				t.Error("refused the format without writing a reason to stderr")
			}
			if ok && errw.Len() != 0 {
				t.Errorf("accepted the format but wrote to stderr: %q", errw.String())
			}
		})
	}
}

// formatFamily are the commands this file drives end-to-end. Each entry is the
// argv after the subcommand token, so a group's reporting verb is named.
//
// `prune` and `check` are deliberately absent from THIS list and covered in
// their own packages instead: prune walks the whole disk and check runs a nix
// image build, so dispatching either here would put minutes into
// `just test-fast`. Their documents are asserted over injected seams —
// internal/prune/jsonreport_test.go and internal/cli/check/jsonreport_test.go —
// and the flag plumbing they share with these three is what this file pins.
var formatFamily = []struct {
	name string
	argv []string
}{
	{"ps", []string{"ps"}},
	{"loopholes list", []string{"loopholes", "list"}},
	{"loopholes status", []string{"loopholes", "status"}},
	{"broker status", []string{"broker", "status"}},
}

// TestStateReportingCommandsEmitParseableJSON dispatches each command for real
// with `--format json` and requires the whole of stdout to decode.
//
// The probes run against empty temp trees for cwd and $HOME, so they find no
// config, no jails and no broker — and every one of them must still produce a
// DOCUMENT. That is the property worth pinning: "nothing to report" and
// "nothing was written" are the same bytes to a consumer, and the second is a
// bug it cannot diagnose.
func TestStateReportingCommandsEmitParseableJSON(t *testing.T) {
	for _, tc := range formatFamily {
		t.Run(tc.name, func(t *testing.T) {
			cwd, home := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home+"/.config")
			t.Chdir(cwd)

			stdout, _ := captureDispatch(t, append(tc.argv, "--format", "json"))

			if strings.TrimSpace(stdout) == "" {
				t.Fatal("emitted nothing on stdout; `--format json` must always " +
					"produce a document, including when there is nothing to report")
			}
			if strings.Contains(stdout, "\x1b[") {
				t.Error("emitted ANSI escapes; JSON output must be plain")
			}
			// PARSED, not matched. json.Valid over the whole stream also catches
			// a stray human line printed before or after the document.
			var v any
			if err := json.Unmarshal([]byte(stdout), &v); err != nil {
				t.Fatalf("output did not parse as JSON: %v\n--- got ---\n%s", err, stdout)
			}
			// The `--json` shorthand must reach the same place.
			shorthand, _ := captureDispatch(t, append(tc.argv, "--json"))
			if shorthand != stdout {
				t.Errorf("`--json` and `--format json` disagree\n--- --json ---\n%s\n"+
					"--- --format json ---\n%s", shorthand, stdout)
			}
		})
	}
}

// TestStateReportingCommandsRefuseAnUnknownFormat: misuse is machine-detectable
// (exit 2, stderr) and stdout stays EMPTY — the half that matters. A command
// that refused on stderr and then printed its human report anyway would hand a
// parser prose with a non-zero exit, which is the trap in a friendlier disguise.
func TestStateReportingCommandsRefuseAnUnknownFormat(t *testing.T) {
	for _, tc := range formatFamily {
		t.Run(tc.name, func(t *testing.T) {
			cwd, home := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home+"/.config")
			t.Chdir(cwd)

			rc, stdout, stderr := captureDispatchRC(t, append(tc.argv, "--format", "yaml"))
			if rc != 2 {
				t.Errorf("exit = %d, want 2 for an unknown --format", rc)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("printed a report despite refusing the format:\n%s", stdout)
			}
			if !strings.Contains(stderr, "yaml") {
				t.Errorf("the refusal does not name the rejected value:\n%s", stderr)
			}
		})
	}
}

// TestActingVerbsRefuseJSONRatherThanIgnoreIt covers the verbs inside these
// groups that do NOT report state. `yolo broker restart` cycles a daemon and
// `yolo loopholes enable` prints a config block for a human to paste; neither
// has a document to give, and accepting the flag to ignore it would answer a
// request for machine-readable output with prose.
//
// These probes carry `--format json` on a verb that acts, so the refusal must
// come BEFORE the action — asserted by requiring exit 2 with empty stdout, which
// a restart-then-refuse would fail.
func TestActingVerbsRefuseJSONRatherThanIgnoreIt(t *testing.T) {
	for _, argv := range [][]string{
		{"loopholes", "enable", "journal"},
		{"loopholes", "disable", "journal"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			cwd, home := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home+"/.config")
			t.Chdir(cwd)

			rc, stdout, stderr := captureDispatchRC(t, append(argv, "--format", "json"))
			if rc != 2 {
				t.Errorf("exit = %d, want 2 — an acting verb must refuse the flag, "+
					"not ignore it", rc)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("wrote to stdout while refusing:\n%s", stdout)
			}
			if !strings.Contains(stderr, "--format json") {
				t.Errorf("the refusal does not name the flag it refused:\n%s", stderr)
			}
		})
	}
}

// captureDispatch runs args through dispatchNative with stdout/stderr redirected
// to temp files, returning both. args[0] is the subcommand.
//
// Temp FILES, not pipes, for the reason probeHelp gives: a command that writes
// more than a pipe buffer holds would block instead of returning.
func captureDispatch(t *testing.T, args []string) (stdout, stderr string) {
	t.Helper()
	_, stdout, stderr = captureDispatchRC(t, args)
	return stdout, stderr
}

func captureDispatchRC(t *testing.T, args []string) (rc int, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	outPath, errPath := dir+"/stdout", dir+"/stderr"
	outF, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	errF, err := os.Create(errPath)
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outF, errF
	func() {
		defer func() {
			os.Stdout, os.Stderr = oldOut, oldErr
			outF.Close()
			errF.Close()
		}()
		rc = dispatchNative(args[0], args)
	}()
	ob, _ := os.ReadFile(outPath)
	eb, _ := os.ReadFile(errPath)
	return rc, string(ob), string(eb)
}

// TestPruneOptionsWiring and TestCheckOptionsWiring pin the two commands the
// dispatch tests above cannot drive.
//
// `prune` walks the whole disk and `check` runs a nix image build, so neither
// can be invoked in `just test-fast`. Their `--format` plumbing is therefore one
// assignment inside an assembly function — pruneOptions / checkOptions — and
// these tests inspect the assembly, which is the same seam
// TestStoresOptionsWiring and TestPruneOptionsWireTheCaptureReader already use
// for the same reason.
//
// MUTATION: delete the `opts.Format = …` line in either function and the
// matching case here fails. That is the whole point — before the assignment
// moved onto these seams, deleting it left every suite in the repo green.
func TestPruneOptionsWiring(t *testing.T) {
	if got := pruneOptions([]string{"prune"}).Format; got != outfmt.Text {
		t.Errorf("default format = %q, want %q — a bare `yolo prune` must print "+
			"the human report", got, outfmt.Text)
	}
	for _, args := range [][]string{
		{"prune", "--format", "json"},
		{"prune", "--format=json"},
		{"prune", "--json"},
		{"prune", "--apply", "--json"},
	} {
		if got := pruneOptions(args).Format; got != outfmt.JSON {
			t.Errorf("pruneOptions(%q).Format = %q, want %q — the flag never reaches "+
				"the engine", args, got, outfmt.JSON)
		}
	}
}

func TestCheckOptionsWiring(t *testing.T) {
	opts, ok := checkOptions([]string{"check"}, io.Discard)
	if !ok {
		t.Fatal("a bare `yolo check` was refused")
	}
	if opts.Format != outfmt.Text {
		t.Errorf("default format = %q, want %q", opts.Format, outfmt.Text)
	}
	for _, args := range [][]string{
		{"check", "--format", "json"},
		{"check", "--format=json"},
		{"check", "--json"},
		// Alongside check's own flags, in either order: --no-build is the one an
		// agent actually pairs it with.
		{"check", "--no-build", "--format", "json"},
		{"doctor", "--json", "--no-build"},
	} {
		opts, ok := checkOptions(args, io.Discard)
		if !ok {
			t.Errorf("checkOptions(%q) refused a valid format", args)
			continue
		}
		if opts.Format != outfmt.JSON {
			t.Errorf("checkOptions(%q).Format = %q, want %q", args, opts.Format, outfmt.JSON)
		}
	}
	// check's own flags must still be parsed alongside the new one — a format
	// parse that swallowed the rest of argv would pass every assertion above.
	opts, _ = checkOptions([]string{"check", "--no-build", "--json"}, io.Discard)
	if opts.Build {
		t.Error("--no-build was lost when --json was present")
	}
	// And misuse is refused rather than silently ignored.
	if _, ok := checkOptions([]string{"check", "--format", "yaml"}, io.Discard); ok {
		t.Error("checkOptions accepted --format yaml")
	}
}
