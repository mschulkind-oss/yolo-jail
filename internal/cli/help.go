package cli

import "strings"

// commandHelp is the ordered, user-facing command list for `yolo --help`. Order
// and blurbs are curated (a map has no stable order); every entry MUST be a key
// in the dispatch registry, and TestUsageListedCommandsAreRegistered enforces
// that so a rename can't leave a stale help line. The hidden `internal`
// namespace is deliberately absent.
//
// THE LIST IS EXHAUSTIVE OVER THE REGISTRY, minus hiddenFromCommandHelp — that
// is the standard's item 4 ("top-level help lists every registered command",
// docs/reference/self-documenting-cli.md) and it is now enforced in BOTH
// directions: TestEveryRegisteredCommandIsListedInHelp walks the registry and
// requires each key to be RENDERED here or explicitly hidden with a reason.
// Until 2026-09-09 only help→registry was checked, and four registered commands
// had drifted out of sight underneath it: `macos-teardown`, `macos-unshare`,
// `doctor` and — the one that mattered — `host`, the whole host-execution
// surface, unreachable from `yolo --help` since it shipped.
var commandHelp = []struct{ name, blurb string }{
	{"run", "Run a command (or an interactive shell) inside the jail"},
	// Directly under `run`, because the two are the same act at different notches
	// of the confinement dial and the contrast is what makes this one findable: a
	// reader looking for "how do I run my agent" reads both lines at once and
	// learns that the host notch exists. It was absent from this list for the
	// nine days between shipping and 2026-09-09, so the entire surface —
	// `yolo host -- <cmd>`, `yolo host apply`, `yolo host env` — could only be
	// found by reading dispatch.go.
	{"host", "Run agents on the HOST notch instead: 'host -- <cmd>', 'host apply'"},
	{"stop", "Stop this workspace's running jail (idempotent; the next launch is fresh)"},
	{"check", "Validate runtime, nix, config, image, and running jails (alias: doctor)"},
	{"ps", "List running yolo-* jails and their workspaces"},
	{"prune", "Reclaim disk: stale containers, images, caches (dry-run unless --apply)"},
	// Sits beside prune because the two answer different questions about the same
	// disk: prune prices what it WOULD delete, stores prices what EXISTS — including
	// the stores nothing reclaims, which prune cannot show by construction.
	{"stores", "Inventory every store: size, growth, what reclaims it (and what nothing does)"},
	{"broker", "Manage the Claude OAuth broker (status|stop|restart|logs)"},
	{"loopholes", "List and self-check host-capability loopholes"},
	{"init", "Scaffold yolo-jail.jsonc in the current workspace"},
	{"init-user-config", "Write user-level defaults at ~/.config/yolo-jail/config.jsonc"},
	{"config", "Inspect generated config: 'config ls' (every composed file), 'render', 'diff', 'reset'"},
	{"describe", "Print the resolved environment description (--json, --hash)"},
	{"apply", "Provision the environment without launching (--at jail|guest|host)"},
	{"check-deps", "Probe the host for binaries the packs need; write an install manifest"},
	// Leads with what a pack delivers rather than the authoring verbs: with the `agents`
	// config key gone, `pack` is the only channel content reaches a jail through, so this
	// line is what a new user scanning `yolo --help` needs to land on. It says "agent
	// config", not "add an agent" — a pack cannot install an agent yet (see packUsage),
	// and a blurb promising otherwise sends people down a path that does not exist.
	{"pack", "Add shared skills and agent config via packs: 'pack --help', 'ls', 'install'"},
	{"capture", "Record a vendor installer's output once per machine, for reuse by every jail"},
	// In-jail only, and the blurb says so: the programs it reports on live in a
	// jail's per-workspace home, and on the host every one of them would read as
	// undeclared. Listed anyway — an act a user cannot find in `yolo --help` is a
	// dead end with extra steps, the same argument macos-fix-permissions carries.
	{"programs", "In a jail: what is installed, what nothing declares, and remove the orphans"},
	{"config-ref", "Print the full configuration reference"},
	{"macos-setup", "Provision the native macOS sandbox user (macos-user backend)"},
	// The four macos-* commands are listed as TWO INVERSE PAIRS, in that order:
	// setup/teardown own the sandbox ACCOUNT, fix-permissions/unshare own a
	// WORKSPACE's ACLs. Ordering them by pair is what makes each one's undo
	// discoverable from the line above or below it, which is the only reason the
	// destructive halves need to be in this list at all.
	{"macos-teardown", "Remove that sandbox user and group again — the undo of macos-setup"},
	// Listed because it is the REMEDY a launch names: a workspace created before
	// macos-setup never inherited the sandbox group's ACL (macOS applies one at
	// creation time only), and the launch now refuses with this command. A remedy
	// a user cannot find in `yolo --help` is a dead end with extra steps.
	{"macos-fix-permissions", "Retrofit the sandbox-group ACL onto an existing workspace"},
	{"macos-unshare", "Strip those ACLs off a workspace — the inverse of macos-fix-permissions"},
}

// hiddenFromCommandHelp names the registry keys deliberately absent from
// commandHelp, each mapped to the reason it is absent. It is the ONLY sanctioned
// exception to item 4, and it lives here rather than in the test on purpose:
// "why is this command not in `yolo --help`?" is a question about the CLI
// surface, so the answer belongs beside the surface, where the next person to
// add a command reads it. A test-local list would let the code look complete
// while the exception hid in a file nobody opens.
//
// It is not a mute button. TestEveryRegisteredCommandIsListedInHelp requires
// every entry here to (a) still be a registry key, (b) carry a non-empty reason,
// and (c) have its NAME appear somewhere in the rendered `yolo --help` text
// anyway — so hiding a command from the LIST can never cost it discoverability,
// and silencing a genuine omission by adding a line here fails the test instead.
var hiddenFromCommandHelp = map[string]string{
	// Same handler, same body, same flags as `check`. A second line for one
	// command is a duplicate rather than a discovery, so the alias is carried by
	// check's own blurb ("alias: doctor") and by checkUsage's second Usage line —
	// which is what clause (c) above checks for.
	"doctor": "alias for `check`; named in check's blurb and in `yolo check --help`, so a second list line would be a duplicate",
}

// usageText renders the top-level `yolo` usage string. Pure (no I/O) so it is
// unit-testable.
// usageText renders the top-level `yolo` usage string with rich markup
// ([bold] section headers, [cyan] command names). It is PURE (no I/O, no TTY
// probe) so it stays unit-testable; the caller renders it to ANSI on a TTY or
// strips the tags off a pipe (usageStripped mirrors the strip for tests). The
// literal command names + blurbs are unchanged text, so stripping is byte-stable.
func usageText() string {
	var b strings.Builder
	b.WriteString("[bold]yolo[/bold] — a sandboxed container jail for AI coding agents\n\n")
	b.WriteString("[bold]Usage:[/bold]\n")
	b.WriteString("  [cyan]yolo -- <command>[/cyan] [args...]     Run <command> inside the jail\n")
	b.WriteString("  [cyan]yolo <subcommand>[/cyan] [args...]     Run a management subcommand\n")
	b.WriteString("  [cyan]yolo --version[/cyan]                  Print the version\n")
	b.WriteString("  [cyan]yolo --help[/cyan]                     Show this help\n\n")
	b.WriteString("[bold]Commands:[/bold]\n")
	width := 0
	for _, c := range commandHelp {
		if len(c.name) > width {
			width = len(c.name)
		}
	}
	for _, c := range commandHelp {
		b.WriteString("  [cyan]" + c.name + "[/cyan]" + strings.Repeat(" ", width-len(c.name)+2) + c.blurb + "\n")
	}
	// GLOBAL flags, listed after the commands because they apply to all of them. Only
	// --user-layer is here: it is the one flag consumed before subcommand routing, so it is
	// the one a reader cannot discover from any single subcommand's help.
	b.WriteString("\n[bold]Global options:[/bold]\n")
	b.WriteString("  [cyan]--user-layer <file>[/cyan]  Layer a JSONC config in at USER-LEVEL " +
		"precedence for this\n")
	b.WriteString("                       invocation only. Inert unless passed; a workspace " +
		"config still\n")
	b.WriteString("                       wins over it. Reaches every command that reads user " +
		"scope, so\n")
	b.WriteString("                       `pack`/`loopholes`/`check` agree with what a launch " +
		"would compose.\n")
	b.WriteString("                       Inside a jail this is how you install a loophole for " +
		"a nested\n")
	b.WriteString("                       jail: write the layer in your own home and pass it.\n")
	// The banner is not a flag, so it is not in the Global options list — but its
	// hatch is the one thing about it a user needs a name for, and `yolo --help` is
	// where they will look for it.
	b.WriteString("\n[bold]Startup banner:[/bold]\n")
	b.WriteString("  Every subcommand writes `yolo-jail <version> | <platform> | host|in-jail` to\n")
	b.WriteString("  stderr first, so a pasted bug report carries them. Never on stdout, and not\n")
	b.WriteString("  gated on a terminal. Set [cyan]YOLO_NO_BANNER[/cyan] to any non-empty value to turn it off.\n")
	// "where supported" is gone: EVERY registered subcommand now answers --help to
	// stdout with exit 0 and no side effect (subhelp.go, and
	// TestEveryRegisteredCommandAnswersHelp which walks the registry to keep it
	// true). The hedge existed because the promise was false — following it landed
	// you in a full check, a disk scan, or a scaffolded yolo-jail.jsonc — and a
	// hedge in the footer is not a fix for a footer that lies.
	b.WriteString("\nRun '[cyan]yolo <subcommand> --help[/cyan]' for any command's own help, or see '[cyan]yolo config-ref[/cyan]'.\n")
	return b.String()
}
