package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// TestRunHelpRequested pins BOTH halves of the papercut at once.
//
// The fix half: `yolo run --help` / `-h` is run's own flag.
//
// The invariant half (the one a future "fix" must not trade away): anything
// after `--` belongs to the inner command. wantsTopLevelHelp counts only the
// first token precisely so `yolo -- cmd --help` reaches cmd; if run then
// claimed that --help, the papercut would simply have moved.
func TestRunHelpRequested(t *testing.T) {
	cases := map[string]bool{
		"run --help": true,
		"run -h":     true,
		// The `--`→run rewrite puts pre-`--` flags BEFORE the injected token.
		"--help run":     true,
		"--new run -h":   true,
		"run --new -h":   true,
		"run --help -- ": true, // help precedes the separator

		// R2: --help after `--` is the INNER command's, always.
		"run -- cmd --help":       false,
		"run -- claude -h":        false,
		"--new run -- cmd --help": false,
		"run --":                  false,
		// The cases that isolate the `--` STOP itself, rather than the
		// implicit-command-start branch that also happens to reject the three above:
		// after an explicit separator even a bare --help is payload. `yolo -- --help`
		// is literally the argv the original bug produced, and it must stay the inner
		// command's problem, not run's.
		"run -- --help":       false,
		"run --new -- -h":     false,
		"run -- --help --new": false,

		// An implicit command start (runRun treats the first bare token as the
		// command) also ends run's own flag region.
		"run foo --help": false,

		// --network eats its value, so this -h is a network mode, not help.
		"run --network -h": false,

		// -p applies --profile's guard instead of eating anything: a token that
		// cannot be a name leaves -h reachable, so `yolo -p -h` reads as the mistyped
		// help request it almost certainly is. With a name present, -p still owns it
		// and the scan continues past both.
		"run -p -h":                   true,
		"run -p --help":               true,
		"run -p dev -h":               true,
		"run -p dev -- claude --help": false,

		// No help anywhere.
		"run":              false,
		"run --new":        false,
		"":                 false,
		"run -- bash -l":   false,
		"--profile run --": false,
	}
	for in, want := range cases {
		args := strings.Fields(in)
		if got := runHelpRequested(args); got != want {
			t.Errorf("runHelpRequested(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestRunHelpWritesUsage: a help request writes run's usage and reports handled;
// a non-help argv writes nothing.
func TestRunHelpWritesUsage(t *testing.T) {
	var buf bytes.Buffer
	if !runHelp([]string{"run", "--help"}, &buf) {
		t.Fatal("runHelp([run --help]) = false, want true")
	}
	if !strings.Contains(buf.String(), "yolo run") {
		t.Errorf("run usage does not name the command:\n%s", buf.String())
	}
	buf.Reset()
	if runHelp([]string{"run", "--", "cmd", "--help"}, &buf) {
		t.Error("runHelp claimed an inner command's --help")
	}
	if buf.Len() != 0 {
		t.Errorf("runHelp wrote %q on a non-help argv", buf.String())
	}
}

// TestRunPassesHelpThroughToInnerCommand is the OTHER half of R2: not merely
// that run declines to claim `-- cmd --help`, but that `--help` actually lands in
// the inner command's argv. runHelpRequested returning false is necessary and not
// sufficient — a future refactor could stop claiming it and still swallow it.
func TestRunPassesHelpThroughToInnerCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		// The `--`→run rewrite of `yolo -- claude --help`.
		{"run -- claude --help", []string{"claude", "--help"}},
		{"run -- bash -h", []string{"bash", "-h"}},
		{"--new run -- claude --help", []string{"claude", "--help"}},
		// An implicit command start keeps its own flags too.
		{"run claude --help", []string{"claude", "--help"}},
	}
	for _, tc := range cases {
		var opts run.Options
		parseRunArgs(strings.Fields(tc.in), &opts)
		if !reflect.DeepEqual(opts.Args, tc.want) {
			t.Errorf("parseRunArgs(%q).Args = %q, want %q", tc.in, opts.Args, tc.want)
		}
	}
}

// TestParseRunArgsFlags pins the flag half of the extracted parser, including the
// two `--network` spellings and the pre-`run` flag position the argv rewrite
// creates.
func TestParseRunArgsFlags(t *testing.T) {
	var opts run.Options
	parseRunArgs(strings.Fields("--new run --profile --dry-run --network host -- true"), &opts)
	if !opts.New || !opts.Profile || !opts.DryRun {
		t.Errorf("flags not parsed: %+v", opts)
	}
	if opts.Network != "host" {
		t.Errorf("Network = %q, want host", opts.Network)
	}
	if !reflect.DeepEqual(opts.Args, []string{"true"}) {
		t.Errorf("Args = %q, want [true]", opts.Args)
	}

	opts = run.Options{}
	parseRunArgs(strings.Fields("run --network=none -- true"), &opts)
	if opts.Network != "none" {
		t.Errorf("--network=none → %q, want none", opts.Network)
	}
}

// THE REFUSAL MESSAGE AND THE PARSER MUST NAME THE SAME FLAG (OQ-D2). A launch with
// no terminal to prompt on is refused, and the only reader of that refusal is
// someone who cannot be asked anything — so the flag it tells them to pass has to
// be the flag this parser accepts. The two are spelled separately on purpose
// (parseRunArgs needs a source-visible literal for the usage/parser drift guard),
// which is exactly why the equality needs pinning rather than assuming.
func TestAcceptConfigChangesFlagMatchesTheRefusalMessage(t *testing.T) {
	var opts run.Options
	parseRunArgs([]string{"run", config.AcceptConfigChangesFlag, "--", "true"}, &opts)
	if !opts.AcceptConfigChanges {
		t.Fatalf("parseRunArgs did not accept %q — the refusal message names a flag "+
			"run does not parse", config.AcceptConfigChangesFlag)
	}
	if !strings.Contains(runUsage, config.AcceptConfigChangesFlag) {
		t.Errorf("runUsage does not document %q\n%s", config.AcceptConfigChangesFlag, runUsage)
	}
	// It must not be on by default: an approval that arrives without being asked
	// for is the auto-accept this ruling removed.
	opts = run.Options{}
	parseRunArgs(strings.Fields("run -- true"), &opts)
	if opts.AcceptConfigChanges {
		t.Error("AcceptConfigChanges must default to false")
	}
}

// TestRunUsageListsEveryRunFlag guards the usage text against parser drift: every
// flag runRun consumes must be documented, or `run --help` teaches a flag surface
// that is not the real one.
//
// runFlags is a HAND-WRITTEN inventory — parseRunArgs spells its own literals and
// runHelpRequested skips anything that starts with `-`, so neither reads this list
// and neither breaks when it goes stale. Iterating it therefore proved only that a
// remembered flag is documented, never that a NEW one is; the parser is the
// authority and is checked directly by TestUsageListsEveryParsedFlag, which follows
// runRun's delegation into parseRunArgs.
//
// The list still earns its place as the one written-down answer to "what does run
// consume", which is why the second assertion here pins it AGAINST the parser: an
// inventory nothing keeps honest is worse than no inventory, because a reader
// (runHelpRequested's doc comment among them) trusts it.
func TestRunUsageListsEveryRunFlag(t *testing.T) {
	for _, f := range runFlags {
		if !strings.Contains(runUsage, f) {
			t.Errorf("runUsage does not document %q\n%s", f, runUsage)
		}
	}
	for _, f := range []string{"--help", "-h"} {
		if !strings.Contains(runUsage, f) {
			t.Errorf("runUsage does not document %q\n%s", f, runUsage)
		}
	}

	_, funcs := parseCLISource(t)
	parser, ok := funcs["parseRunArgs"]
	if !ok {
		t.Fatal("parseRunArgs is gone — runFlags has nothing left to be an inventory of")
	}
	// parseRunArgs also matches `--` (excluded by length) and the `--network=`
	// form, which longFlagLiterals folds onto `--network`.
	got, want := longFlagLiterals(parser), append([]string(nil), runFlags...)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("runFlags is stale: parseRunArgs consumes %v, runFlags says %v", got, want)
	}
}

// THE -p HALF OF THE PROFILE SELECTOR. `--profile` refuses a following token that
// cannot be a name ('-', the injected "run", '--') and falls back to the startup
// timing report. `-p` had no such guard: it took the next token WHATEVER it was, and
// RewriteArgv inserts "run" at the `--` position, so `yolo -p -- claude` reached the
// parser as [-p, run, --, claude] and silently selected a profile literally named
// "run" — a name OQ-3 rules free-form, so no downstream check could call it a typo —
// while a trailing bare `yolo -p` silently selected no profile at all.
//
// Unlike --profile, -p does NOT fall back to the timing report: a -p that named
// nothing is a mistake about the profile, and a mistake must not be read as "the
// user asked for timings".

// TestParseRunArgsRefusesAnUnreadablePName pins the guard, not just the refusal: the
// tokens -p must NOT read as a name are exactly the ones --profile refuses.
func TestParseRunArgsRefusesAnUnreadablePName(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		// `yolo run -p -- claude`, typed as the subcommand spelling.
		{"the separator directly after -p", "run -p -- claude"},
		// `yolo -p -- claude` as the front door delivers it: the injected "run" sits
		// between -p and the separator, and is what -p used to swallow.
		{"the injected run token", "-p run -- claude"},
		{"the injected run token after run", "run -p run -- claude"},
		{"a following flag", "run -p --new -- true"},
		{"a trailing bare -p", "run -p"},
		{"a trailing bare -p, no subcommand", "-p"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts run.Options
			err := parseRunArgs(strings.Fields(tc.in), &opts)
			if !errors.Is(err, errProfileNameMissing) {
				t.Fatalf("parseRunArgs(%q) error = %v, want errProfileNameMissing", tc.in, err)
			}
			if opts.ProfileName != "" {
				t.Errorf("ProfileName = %q, want none — -p must never guess a name", opts.ProfileName)
			}
			if opts.Profile {
				t.Error("Profile = true — the timing report is --profile's fallback, not -p's")
			}
		})
	}
}

// TestParseRunArgsStillReadsAProfileName pins the spellings the guard must not break,
// in the two argv shapes the front door actually produces (an explicit `run` keeps its
// leading token; the rewrite moves "run" next to the `--` instead).
func TestParseRunArgsStillReadsAProfileName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"run -p dev -- claude", "dev"},
		{"-p dev run -- claude", "dev"},
		{"run -p=dev -- claude", "dev"},
		// The glued form is the only way to name a profile "run": positionally, that
		// token is indistinguishable from the injected one.
		{"-p=run -- claude", "run"},
		{"--profile dev -- true", "dev"},
	}
	for _, tc := range cases {
		var opts run.Options
		if err := parseRunArgs(strings.Fields(tc.in), &opts); err != nil {
			t.Errorf("parseRunArgs(%q) error = %v, want a name", tc.in, err)
			continue
		}
		if opts.ProfileName != tc.want {
			t.Errorf("parseRunArgs(%q).ProfileName = %q, want %q", tc.in, opts.ProfileName, tc.want)
		}
	}
	opts := run.Options{}
	if err := parseRunArgs(strings.Fields("-p dev run -- claude"), &opts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opts.Args, []string{"claude"}) {
		t.Errorf("Args = %q, want [claude] — the guard must not eat the command", opts.Args)
	}
	// --profile's own fallback is untouched: no argument at all still means timings,
	// which is exactly the fallback -p must NOT share.
	opts = run.Options{}
	if err := parseRunArgs(strings.Fields("--profile run -- claude"), &opts); err != nil {
		t.Fatal(err)
	}
	if !opts.Profile || opts.ProfileName != "" {
		t.Errorf("--profile run → %+v, want the timing flag and no name", opts)
	}
}

// TestRunRejectsAPWithoutAName drives the REAL entry point, because the pure parser
// returning an error is worthless if runRun never looks at it — the exact
// callee-pinned/call-site-unpinned shape this repo keeps shipping. The fixture config
// is unparseable so that a runRun which LOST the check falls through to run.Run and
// fails on the config (non-zero, no container) instead of launching one: the
// assertion that stderr NAMES -p is what distinguishes the two failures.
func TestRunRejectsAPWithoutAName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yolo-jail.jsonc"),
		[]byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	out, errOut := captureBoth(t, func() {
		if rc := Main([]string{"yolo", "-p", "--", "claude"}); rc == 0 {
			t.Error("Main([yolo -p -- claude]) = 0, want non-zero")
		}
	})
	joined := out + errOut
	if !strings.Contains(joined, "-p") {
		t.Errorf("the refusal never names the flag, so the reader cannot tell which "+
			"spelling was wrong:\nstdout: %s\nstderr: %s", out, errOut)
	}
	if strings.Contains(joined, "not json") {
		t.Errorf("run.Run ran — the -p check is not wired into runRun:\n%s", joined)
	}
}

// captureBoth redirects stdout AND stderr for the duration of body and returns what
// each caught. runRun answers help on stdout and refuses argv on stderr, so a test
// that wants "the user was told" has to read both.
func captureBoth(t *testing.T, body func()) (stdout, stderr string) {
	t.Helper()
	stdout = captureStdout(t, func() { stderr = captureStderr(t, body) })
	return stdout, stderr
}

// captureStderr is captureStdout's twin for os.Stderr. Both redirects have to nest
// (captureBoth), which is why neither captures both streams itself.
func captureStderr(t *testing.T, body func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()
	body()
	os.Stderr = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestRunHelpAnsweredWithoutConfig is R3: help must be reachable exactly when it
// is most needed — a workspace whose yolo-jail.jsonc does not parse.
//
// It drives the real runRun, in a temp workspace holding a broken config. With
// the help branch in place it returns 0 having printed usage; without it, runRun
// falls through to run.Run, which loads config and fails (1) — or, with a VALID
// config, would launch a container. That is why the fixture is deliberately
// unparseable: the regression's failure mode stays a non-zero exit, never a
// container start.
func TestRunHelpAnsweredWithoutConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yolo-jail.jsonc"),
		[]byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	out := captureStdout(t, func() {
		if rc := runRun([]string{"run", "--help"}); rc != 0 {
			t.Errorf("runRun([run --help]) = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "yolo run") {
		t.Errorf("runRun([run --help]) printed no usage:\n%s", out)
	}
}
