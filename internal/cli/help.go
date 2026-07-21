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
	{"builder", "Manage the macOS Linux-builder VM (status|setup|start|stop)"},
	{"loopholes", "List/enable/disable host-capability loopholes"},
	{"init", "Scaffold yolo-jail.jsonc in the current workspace"},
	{"init-user-config", "Write user-level defaults at ~/.config/yolo-jail/config.jsonc"},
	{"config", "Inspect generated config: 'config render <agent>' (composition pipeline)"},
	{"config-ref", "Print the full configuration reference"},
	{"macos-setup", "Provision the native macOS sandbox user (macos-user backend)"},
}

// usageText renders the top-level `yolo` usage string. Pure (no I/O) so it is
// unit-testable.
func usageText() string {
	var b strings.Builder
	b.WriteString("yolo — a sandboxed container jail for AI coding agents\n\n")
	b.WriteString("Usage:\n")
	b.WriteString("  yolo -- <command> [args...]     Run <command> inside the jail\n")
	b.WriteString("  yolo <subcommand> [args...]     Run a management subcommand\n")
	b.WriteString("  yolo --version                  Print the version\n")
	b.WriteString("  yolo --help                     Show this help\n\n")
	b.WriteString("Commands:\n")
	width := 0
	for _, c := range commandHelp {
		if len(c.name) > width {
			width = len(c.name)
		}
	}
	for _, c := range commandHelp {
		b.WriteString("  " + c.name + strings.Repeat(" ", width-len(c.name)+2) + c.blurb + "\n")
	}
	b.WriteString("\nRun 'yolo <subcommand> --help' where supported, or see 'yolo config-ref'.\n")
	return b.String()
}
