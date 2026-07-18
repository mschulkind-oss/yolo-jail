package buildercmd

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/builder"
)

// RunBuilder dispatches `yolo builder <sub> [args]` to the ported command
// bodies and returns the process exit code. Unknown subcommands return 2 (the
// front door validates the group, so this is a safety net). Mirrors the
// builder_app typer group. `args` is the post-subcommand argv.
func RunBuilder(deps Deps, sub string, args []string) int {
	switch sub {
	case "status":
		return BuilderStatusCmd(deps)
	case "start":
		return BuilderStartCmd(deps)
	case "stop":
		return BuilderStopCmd(deps)
	case "setup":
		return BuilderSetupCmd(deps, parseSetupFlags(args))
	default:
		return 2
	}
}

// requireMacos mirrors _require_macos: on non-macOS, print the notice and
// return (exitCode=0, handled=true). handled=false means proceed.
func requireMacos(deps Deps) (int, bool) {
	if !deps.IsMacOS() {
		printer{w: deps.Out}.print(
			"[yellow]The Linux builder is a macOS-only concept.[/yellow]  " +
				"On Linux the image builds natively — no builder VM needed.")
		return 0, true
	}
	return 0, false
}

func mark(ok bool) string {
	if ok {
		return "[green]yes[/green]"
	}
	return "[red]no[/red]"
}

// BuilderStatusCmd reports builder set-up state + reachability. Mirrors
// builder_status_cmd. Returns the exit code (1 when not set up, else 0).
func BuilderStatusCmd(deps Deps) int {
	if rc, done := requireMacos(deps); done {
		return rc
	}
	out := printer{w: deps.Out}
	st := BuilderStatus(deps)

	out.print("[bold]macOS Linux builder[/bold]")
	out.printf("  set up:       %s", mark(st.Done))
	out.printf("    nix.conf:   %s  (%s)", mark(st.NixBuilder), st.ConfPath)
	out.printf("    ssh config: %s", mark(st.SSHConfig))
	out.printf("    ssh key:    %s", mark(st.Key))
	out.printf("  reachable:    %s  (port %d)", mark(st.Reachable), builder.BuilderPort)
	out.print("")
	if !st.Done {
		out.print("[yellow]Not set up.[/yellow]  Run [cyan]yolo builder setup[/cyan] " +
			"once to wire the Nix daemon to a Linux builder VM.")
		return 1
	}
	if st.Reachable {
		out.print("[green]Builder set up and running.[/green]")
		return 0
	}
	out.print("[dim]Builder set up but not running — that's normal when idle; " +
		"yolo starts it automatically before a build.[/dim]")
	return 0
}

// BuilderStartCmd starts the builder VM now. Mirrors builder_start_cmd,
// including the interactive first-boot fork. Returns the exit code.
func BuilderStartCmd(deps Deps) int {
	if rc, done := requireMacos(deps); done {
		return rc
	}
	out := printer{w: deps.Out}
	if deps.Reachable() {
		out.print("[green]Builder already running.[/green]")
		return 0
	}
	if !BuilderSetupState(deps).Done {
		out.print("[yellow]Builder not set up.[/yellow]  Run " +
			"[cyan]yolo builder setup[/cyan] first.")
		return 1
	}

	// First boot: install the ssh key interactively (foreground, real TTY).
	if !BuilderSetupState(deps).Key {
		out.print("[bold]First boot:[/bold] installing the builder's ssh key (sudo) " +
			"and booting the VM.  When you see a [cyan]builder@…[/cyan] login " +
			"prompt, the builder is up — press [cyan]Ctrl-C[/cyan] to return.\n")
		ok, errMsg := FirstBootInteractive(deps)
		if !ok {
			out.printf("[red]First boot failed:[/red] %s", errMsg)
			return 1
		}
		if deps.Reachable() {
			out.print("[green]Builder is up.[/green]")
			return 0
		}
		// Key now installed; fall through to a normal detached start.
	}

	ok, errMsg := EnsureBuilder(deps, func(m string) { out.printf("[dim]%s[/dim]", m) })
	if ok {
		out.print("[green]Builder is up.[/green]")
		return 0
	}
	out.printf("[red]Could not start builder:[/red] %s", errMsg)
	return 1
}

// BuilderStopCmd stops the builder VM now. Mirrors builder_stop_cmd.
func BuilderStopCmd(deps Deps) int {
	if rc, done := requireMacos(deps); done {
		return rc
	}
	out := printer{w: deps.Out}
	if !deps.Reachable() {
		out.print("[dim]Builder not running.[/dim]")
		return 0
	}
	ok, errMsg := deps.StopVM()
	if ok {
		out.print("[green]Builder stopped.[/green]")
		return 0
	}
	out.printf("[red]Could not stop builder:[/red] %s", errMsg)
	return 1
}

// SetupFlags mirror builder_setup_cmd's options.
type SetupFlags struct {
	MaxJobs int
	Show    bool
	Yes     bool
}

// parseSetupFlags parses --max-jobs N/--max-jobs=N, --show, --yes/-y. Defaults
// max-jobs to 4 (the typer default).
func parseSetupFlags(args []string) SetupFlags {
	f := SetupFlags{MaxJobs: 4}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--show":
			f.Show = true
		case a == "--yes" || a == "-y":
			f.Yes = true
		case a == "--max-jobs":
			if i+1 < len(args) {
				i++
				if n, ok := atoi(args[i]); ok {
					f.MaxJobs = n
				}
			}
		case strings.HasPrefix(a, "--max-jobs="):
			if n, ok := atoi(a[len("--max-jobs="):]); ok {
				f.MaxJobs = n
			}
		}
	}
	return f
}

// BuilderSetupCmd does the one-time privileged wiring. Mirrors
// builder_setup_cmd, including the explanatory output, the exact-script
// preview, the --show early exit, and the confirmation prompt.
func BuilderSetupCmd(deps Deps, flags SetupFlags) int {
	if rc, done := requireMacos(deps); done {
		return rc
	}
	out := printer{w: deps.Out}
	st := BuilderSetupState(deps)
	if st.Done {
		out.print("[green]Builder already set up.[/green]")
		out.print("[dim]Run [cyan]yolo builder status[/cyan] to inspect, or " +
			"[cyan]yolo builder start[/cyan] to bring it up.[/dim]")
		return 0
	}

	me := deps.HostUser()
	conf := confPath(deps)
	label, _ := deps.DetectNixDaemonLabel()
	current := deps.CurrentTrustedUsers()
	script := builder.SetupRootScript(flags.MaxJobs, me, current, conf, label)

	out.print("[bold]Set up the on-demand macOS Linux builder[/bold]\n")
	out.print("macOS can't build the Linux image locally, so Nix offloads to a small " +
		"Linux VM.  This wires the Nix daemon to that VM so builds 'just work'; " +
		"afterward yolo starts/stops the VM on demand (no terminal to babysit, " +
		"no RAM held while idle).\n")
	out.print("[bold]It will, in one sudo:[/bold]")
	out.printf("  • add a [cyan]builders[/cyan] line to [cyan]%s[/cyan] (offload aarch64-linux)", conf)
	if _, ok := builder.TrustedUsersLine(current, me); ok {
		out.printf("  • add [cyan]%s[/cyan] to [cyan]trusted-users[/cyan] (merged — existing entries kept)", me)
	} else {
		out.print("  • [dim](you're already a trusted user — no trusted-users change)[/dim]")
	}
	out.printf("  • write the ssh host alias [cyan]%s[/cyan]", builder.SSHConfigPath())
	out.print("  • restart the Nix daemon to apply\n")

	out.print("[dim]The exact root script:[/dim]")
	out.printf("[dim]%s[/dim]", script)

	if flags.Show {
		return 0
	}

	out.print("[dim]After this, run [cyan]yolo builder start[/cyan] once for the " +
		"interactive first boot (one more sudo, to install the VM's ssh key). " +
		"From then on yolo starts/stops the builder for you on demand.[/dim]\n")

	if !flags.Yes {
		if !deps.Confirm("Run the privileged setup now (one sudo prompt)?") {
			out.print("[dim]Aborted. Re-run when ready, or `--show` to just print it.[/dim]")
			return 1
		}
	}

	ok, errMsg := RunSetup(deps, flags.MaxJobs, me)
	if !ok {
		out.printf("[red]Setup failed:[/red] %s", errMsg)
		return 1
	}
	out.print("[green]Builder wired up.[/green]")
	out.print("[dim]Next, run [cyan]yolo builder start[/cyan] once for the interactive " +
		"first boot (installs the VM ssh key via sudo).  After that yolo starts " +
		"and stops the builder for you on demand.[/dim]")
	return 0
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	neg := false
	i := 0
	if s[0] == '-' {
		neg = true
		i = 1
	}
	if i == len(s) {
		return 0, false
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}
