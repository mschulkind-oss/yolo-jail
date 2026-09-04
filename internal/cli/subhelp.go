package cli

// subhelp.go is the registration point for per-subcommand `--help`.
//
// It exists because "what does this command do?" was, for most of the CLI, a
// question you could only ask by RUNNING the command. `yolo` is a hand-rolled
// dispatcher (dispatch.go's `registry`), so nothing parses flags before a
// handler is entered; an unrecognized `--help` therefore fell through each
// handler's own flag scan and the command simply did its work. That is worse
// than a missing feature — `yolo init --help` scaffolded a yolo-jail.jsonc and
// appended to .gitignore, `yolo init-user-config --help` wrote
// ~/.config/yolo-jail/config.jsonc, `yolo check --help` ran a full check
// including a nix build, and `yolo prune --help` walked the disk. Interrogating
// a tool must never change the machine (docs/design/self-documenting-cli.md,
// item 1: help is a REQUEST — stdout, exit 0, no side effect).
//
// The shape is the one `run`/`config`/`pack`/`apply`/`describe`/`check-deps`
// already use, not a second convention: a plain-text `<cmd>Usage` const next to
// the handler, recognized via isHelpToken, answered at the TOP of the handler
// before any config load or any work. What is new here is only the REGISTRY of
// those texts — subcommandUsage — and it is new for one reason: a table keyed by
// the same names dispatch.go's `registry` is keyed by can be walked in a test,
// so "every command answers --help" is checkable by construction rather than by
// a list someone must remember to extend. See subhelp_test.go.
//
// Deliberately NOT done here: intercepting `--help` centrally in dispatchNative.
// `run` must not be intercepted — `yolo -- claude --help` has to reach the inner
// command, which is exactly why wantsTopLevelHelp counts only the first token
// (cli.go) and why runHelpRequested re-implements the scan with run's stricter
// rule (runcmd.go). A central interception would either break that invariant or
// need a per-command predicate anyway, which is what the valueFlags field is.

import (
	"io"
	"slices"
)

// subUsage is one command's help registration.
type subUsage struct {
	// text is the full help, plain (no rich markup) so it is byte-stable off a
	// TTY and directly assertable in tests — the property runUsage documents.
	text string
	// valueFlags are this command's flags that consume the NEXT argv token as
	// their value. They are listed so that token is not mistaken for a help
	// request: `yolo init -m --help` asks to mount a path called `--help`, and
	// `yolo broker logs -n --help` asks for `--help` lines. This is the same
	// rule runHelpRequested applies to run's `--network`.
	valueFlags []string
}

// subcommandUsage maps every dispatch-registry key to its help text. EVERY key
// — a command missing from here has no discoverable help, which is the defect
// this table exists to make impossible to reintroduce silently
// (TestEveryRegisteredCommandAnswersHelp walks `registry`, not this map).
//
// Commands that already answered help before this table existed keep answering
// it their own way; their entry here points at the SAME const they print, so the
// table stays the one complete inventory without becoming a second source of
// truth for the text.
var subcommandUsage = map[string]subUsage{
	// Answers via runHelp/runHelpRequested (runcmd.go), whose scan is stricter
	// than helpRequested's: a bare token before `--` starts the inner command, so
	// `yolo run help` runs `help` IN the jail. valueFlags is left empty because
	// nothing here drives that path; run's `--network` skip lives in
	// runHelpRequested, next to the parse it mirrors.
	"run": {text: runUsage},
	// Answer inside their own testable bodies (case isHelpToken(a): ...).
	"config": {text: configUsage},
	"pack":   {text: packUsage},
	"apply":  {text: applyUsage},
	// `host` answers inside hostMain, like pack/config/apply. valueFlags carries the
	// exec half's -p/--profile so `yolo host -p bedrock --help` is read as help rather
	// than as a profile named "--help".
	"host":       {text: hostUsage, valueFlags: []string{"-p", "--profile", "--format", "--agent"}},
	"capture":    {text: captureUsage},
	"describe":   {text: describeUsage},
	"check-deps": {text: checkDepsUsage},
	// Answered via answerHelp, at the top of each handler.
	"check": {text: checkUsage},
	// doctor is an alias for check (same handler, same body), so it registers the
	// same text — which names the alias — rather than a near-duplicate that would
	// drift.
	"doctor":                {text: checkUsage},
	"ps":                    {text: psUsage},
	"prune":                 {text: pruneUsage, valueFlags: []string{"--keep-images", "--image-cache-keep", "--cache-age", "--nix-gc-max"}},
	"loopholes":             {text: loopholesUsage},
	"broker":                {text: brokerUsage, valueFlags: []string{"-n", "--lines"}},
	"init":                  {text: initUsage, valueFlags: []string{"--mount", "-m"}},
	"init-user-config":      {text: initUserConfigUsage},
	"config-ref":            {text: configRefUsage},
	"macos-setup":           {text: macosSetupUsage},
	"macos-teardown":        {text: macosTeardownUsage},
	"macos-unshare":         {text: macosUnshareUsage},
	"macos-fix-permissions": {text: macosFixPermissionsUsage},
}

// helpRequested reports whether args (the rewritten argv[1:], so args[0] is
// normally the subcommand token) asks for this command's help.
//
// It stops at `--`: everything after it belongs to an inner command, the
// invariant the first-token-only top-level rule (cli.go) exists to protect. It
// skips the value of each flag in valueFlags for the reason that field
// documents.
//
// The bare word `help` counts, matching isHelpToken and what `yolo config help`
// / `yolo pack help` already do. The one ambiguity that buys: a positional
// literally named `help` (`yolo macos-unshare help`) reads as a help request —
// write `./help` to mean the directory. Uniformity is worth more than that case.
func helpRequested(args []string, valueFlags ...string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return false
		case isHelpToken(a):
			return true
		case slices.Contains(valueFlags, a):
			i++ // its value, whatever it looks like
		}
	}
	return false
}

// answerHelp answers a help request for sub: it writes sub's registered usage to
// out and reports true, so the caller returns 0 having loaded no config, probed
// no runtime, and written no file. False means args was not a help request and
// the caller proceeds with its real work.
//
// Callers pass the literal command name rather than reading args[0], because
// args[0] is only the subcommand token when no global flag precedes it
// (Subcommand skips leading flags), and an alias dispatches under two names.
func answerHelp(sub string, args []string, out io.Writer) bool {
	spec, ok := subcommandUsage[sub]
	if !ok {
		return false
	}
	if !helpRequested(args, spec.valueFlags...) {
		return false
	}
	io.WriteString(out, spec.text+"\n")
	return true
}
