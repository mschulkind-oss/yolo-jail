package cli

import "strings"

// commandHelp is the ordered, user-facing command list for `yolo --help`. Order
// and blurbs are curated (a map has no stable order); every entry MUST be a key
// in the dispatch registry, and TestUsageListedCommandsAreRegistered enforces
// that so a rename can't leave a stale help line. The hidden `internal`
// namespace is deliberately absent.
var commandHelp = []struct{ name, blurb string }{
	{"run", "Run a command (or an interactive shell) inside the jail"},
	{"check", "Validate runtime, nix, config, image, and running jails (alias: doctor)"},
	{"ps", "List running yolo-* jails and their workspaces"},
	{"prune", "Reclaim disk: stale containers, images, caches (dry-run unless --apply)"},
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
	{"config-ref", "Print the full configuration reference"},
	{"macos-setup", "Provision the native macOS sandbox user (macos-user backend)"},
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
	b.WriteString("\nRun '[cyan]yolo <subcommand> --help[/cyan]' where supported, or see '[cyan]yolo config-ref[/cyan]'.\n")
	return b.String()
}
