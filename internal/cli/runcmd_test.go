package cli

import (
	"bytes"
	"go/ast"
	"go/token"
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

		// -p and --profile are value flags like --network: the next token is the name
		// whatever it spells, so a help token directly after the flag is a profile
		// called "-h", not a help request. With a name present, the flag owns it and
		// the scan continues past both.
		"run -p -h":                   false,
		"run -p --help":               false,
		"run --profile -h":            false,
		"run -p dev -h":               true,
		"run --profile dev -h":        true,
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
	parseRunArgs(strings.Fields("--new run --timing --dry-run --network host -- true"), &opts)
	if !opts.New || !opts.Timing || !opts.DryRun {
		t.Errorf("flags not parsed: %+v", opts)
	}
	if opts.Network != "host" {
		t.Errorf("Network = %q, want host", opts.Network)
	}
	if opts.ProfileName != "" {
		t.Errorf("ProfileName = %q, want none — --timing is not a profile flag", opts.ProfileName)
	}
	if !reflect.DeepEqual(opts.Args, []string{"true"}) {
		t.Errorf("Args = %q, want [true]", opts.Args)
	}

	opts = run.Options{}
	parseRunArgs(strings.Fields("run --network=none -- true"), &opts)
	if opts.Network != "none" {
		t.Errorf("--network=none → %q, want none", opts.Network)
	}

	// --timing names nothing and selects no profile: it is the renamed startup-timing
	// report (OQ-PT5), the flag the old bare --profile used to be.
	opts = run.Options{}
	parseRunArgs(strings.Fields("run --timing -- true"), &opts)
	if !opts.Timing || opts.ProfileName != "" {
		t.Errorf("--timing → %+v, want the timing flag and no name", opts)
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

// TestRunRunParsesArgvIntoTheOptionsItLaunches pins parseRunArgs's CALL SITE,
// which nothing else in this package pins.
//
// Every other test here drives the pure parser directly, so the whole run flag
// surface — --new, --timing, --profile/-p, --pack-profile, --dry-run,
// --accept-config-changes, --network and the post-`--` command argv — could be
// switched off wholesale with `just test-fast` green if runRun simply stopped
// consulting it. That is the callee-pinned / call-site-unpinned shape AGENTS.md
// documents this repo shipping five times, and it is not hypothetical: deleting
// `parseRunArgs(args, &opts)` from runRun left this package's short suite green
// at `5afbb592`. The Main-driven help test does not fill the gap — it pins
// runHelp's edge, which subhelp_test's registry probe pins independently.
//
// No behavioral unit pin is reachable instead. Every field parseRunArgs sets is
// first observable inside run.Run, past the config gate, the runtime probe and
// pack staging, and the one pre-launch refusal the parse used to make
// (errProfileNameMissing) is gone with the heuristic that needed it. So the
// cheapest test that fails on the deletion reads the call out of the source,
// exactly as parseCLISource reads the registry out of dispatch.go — and as a
// side effect it is what keeps TestUsageListsEveryParsedFlag's one hop of
// delegation (runRun → parseRunArgs) from going vacuous: the hop is where run's
// documented flag inventory comes from, so a handler that no longer delegates
// documents nothing.
func TestRunRunParsesArgvIntoTheOptionsItLaunches(t *testing.T) {
	_, funcs := parseCLISource(t)
	runRun, ok := funcs["runRun"]
	if !ok {
		t.Fatal("runRun is gone — run's flags have no consumer")
	}
	parsed, launched := optionsFlow(runRun)
	// "_" counts as missing: an Options handed to the parser and thrown away is
	// the subtler version of not consulting it at all.
	if parsed == "" || parsed == "_" {
		t.Errorf("runRun never passes a plain Options variable to parseRunArgs (got %q) — "+
			"run's flags are read by nobody", parsed)
	}
	if launched == "" || launched == "_" {
		t.Errorf("runRun never passes a plain Options variable to run.Run (got %q)", launched)
	}
	if parsed != "" && launched != "" && parsed != launched {
		t.Errorf("runRun parses argv into %s but launches %s — the parse never reaches "+
			"the launch, so no run flag has any effect", parsed, launched)
	}
}

// optionsFlow reports the Options variable fn parses argv into (the second
// argument of a call to parseRunArgs) and the one it launches (the first argument
// of a call to run.Run), unwrapping the `&` the parser's pointer parameter puts
// on the first. "" where a call is absent or names anything but a plain
// identifier — a composite literal, a selector, `_` — so the caller can tell
// "pins a variable it never launched" from "pins nothing".
func optionsFlow(fn *ast.FuncDecl) (parsed, launched string) {
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch pkg, name := calleeName(call); {
		case pkg == "" && name == "parseRunArgs" && len(call.Args) >= 2:
			parsed = identArg(call.Args[1])
		case pkg == "run" && name == "Run" && len(call.Args) >= 1:
			launched = identArg(call.Args[0])
		}
		return true
	})
	return parsed, launched
}

// calleeName names a call's callee as a (package, function) pair, "" for the
// package of an unqualified — therefore same-package — call. That is the same
// distinction parsedFlagLiterals draws when it decides what counts as a delegated
// parser, and it is why run.Run needs the qualifier while parseRunArgs does not.
func calleeName(call *ast.CallExpr) (pkg, name string) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return "", fn.Name
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			return x.Name, fn.Sel.Name
		}
	}
	return "", ""
}

// identArg names the variable an argument refers to, looking through the `&` of
// an address-of. "" for anything else.
func identArg(e ast.Expr) string {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = u.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// THE PROFILE SELECTOR, AFTER OQ-PT5 (provider-table-fidelity.md §5.2). The
// startup-timing report moved to --timing, so --profile and -p have ONE meaning
// left: take the next token as the name. profileValueAt — which looked ahead and
// refused to read a `-`-prefixed token, the injected "run", or the separator as a
// name — is gone rather than more careful, and with it errProfileNameMissing, the
// only refusal the fold made. Both spellings are ordinary value flags now, which is
// exactly how --network and --pack-profile have always read theirs: a missing value
// silently selects nothing, and a value that looks like a flag IS the value.
//
// The heuristic existed because the flags had a second reading to protect. Its two
// fix commits were correct about the ambiguity; the ruling removed the ambiguity.

// TestParseRunArgsProfileTakesTheNextToken pins the ruled reading, including the
// cases the old guard refused: those tokens are names now, and the tests say so
// rather than leaving them implied, because they are the ones a future reader will
// want to "fix" back.
func TestParseRunArgsProfileTakesTheNextToken(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// The ordinary spellings, in the two argv shapes the front door produces
		// (an explicit `run` keeps its leading token; the rewrite moves "run" next
		// to the `--` instead).
		{"run -p dev -- claude", "dev"},
		{"-p dev run -- claude", "dev"},
		{"run --profile dev -- claude", "dev"},
		{"--profile dev run -- claude", "dev"},
		// The glued forms, which are also the only way to name a profile "run":
		// positionally that token is indistinguishable from the injected one.
		{"run -p=dev -- claude", "dev"},
		{"--profile=dev -- true", "dev"},
		{"-p=run -- claude", "run"},
		{"--profile=run -- true", "run"},
		// What the deleted guard used to refuse, now read literally as names —
		// the ruling's whole point is that there is no second reading to protect.
		{"run -p -- claude", "--"},
		{"-p run -- claude", "run"},
		{"run -p --new -- true", "--new"},
		{"--profile --new -- true", "--new"},
	}
	for _, tc := range cases {
		var opts run.Options
		parseRunArgs(strings.Fields(tc.in), &opts)
		if opts.ProfileName != tc.want {
			t.Errorf("parseRunArgs(%q).ProfileName = %q, want %q", tc.in, opts.ProfileName, tc.want)
		}
		if opts.Timing {
			t.Errorf("parseRunArgs(%q) set Timing — the timing report is --timing's, "+
				"not something a profile flag falls back to", tc.in)
		}
	}

	// A name in the value position must not eat the command with it.
	opts := run.Options{}
	parseRunArgs(strings.Fields("-p dev run -- claude"), &opts)
	if !reflect.DeepEqual(opts.Args, []string{"claude"}) {
		t.Errorf("Args = %q, want [claude] — the value flag must not eat the command", opts.Args)
	}
}

// TestParseRunArgsBareProfileSelectsNothing pins the silent swallow a value flag
// gets when its value is missing — the documented behavior --network and
// --pack-profile already had, which --profile and -p now share instead of the old
// timing fallback and the old parse error.
func TestParseRunArgsBareProfileSelectsNothing(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"a trailing bare -p", "run -p"},
		{"a trailing bare --profile", "run --profile"},
		{"a trailing bare -p, no subcommand", "-p"},
		{"a trailing bare --profile, no subcommand", "--profile"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts run.Options
			parseRunArgs(strings.Fields(tc.in), &opts)
			if opts.ProfileName != "" {
				t.Errorf("parseRunArgs(%q).ProfileName = %q, want none", tc.in, opts.ProfileName)
			}
			if opts.Timing {
				t.Errorf("parseRunArgs(%q) set Timing — a nameless --profile no longer "+
					"reports timings; that is --timing's job", tc.in)
			}
			if opts.Args != nil {
				t.Errorf("parseRunArgs(%q).Args = %q, want nothing to start a command", tc.in, opts.Args)
			}
		})
	}
}

// TestRunHelpNeverClaimsAHelpTokenAfterProfileName drives the REAL entry point,
// because the pure scanner's verdict is worthless if runRun never consults it — the
// exact callee-pinned/call-site-unpinned shape this repo keeps shipping. A help
// token directly after -p is a profile named "-h" (see TestParseRunArgsProfileTakesTheNextToken),
// so run must NOT answer with its own usage; the fixture config is unparseable so
// that a runRun which DID answer help (exit 0, usage printed) is distinguishable
// from the run.Run failure (non-zero, the config error on stderr, no container).
func TestRunHelpNeverClaimsAHelpTokenAfterProfileName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yolo-jail.jsonc"),
		[]byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	out, errOut := captureBoth(t, func() {
		if rc := Main([]string{"yolo", "-p", "-h"}); rc == 0 {
			t.Error("Main([yolo -p -h]) = 0, want non-zero — -h is a profile name here")
		}
	})
	joined := out + errOut
	if strings.Contains(joined, "Usage: yolo run") {
		t.Errorf("run answered its own help for a -p whose value is \"-h\":\nstdout: %s\nstderr: %s",
			out, errOut)
	}
	// run.Run's config load is the proof the launch pipeline was reached (and the
	// json5 error never quotes the fixture, so "not json" cannot appear).
	if !strings.Contains(joined, "Failed to parse yolo-jail.jsonc") {
		t.Errorf("run.Run never ran — runRun stopped consulting the scanner or stopped "+
			"parsing before launch:\n%s", joined)
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
