package macosuser

import (
	"strings"
)

// MacosSetup creates the dedicated sandbox account (one-time, needs admin).
// Idempotent. Returns the exit code (0 success; 1 on failure/refusal). Mirrors
// macos_setup (which raises typer.Exit; here the exit code is returned so the
// front door maps it to a process exit).
func MacosSetup(deps Deps) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !deps.IsMacOS() {
		out.print("[bold red]yolo macos-setup requires macOS.[/bold red]")
		return 1
	}
	if rc := refuseIfRoot(deps); rc != 0 {
		return rc
	}

	// 1. Account — create if missing, otherwise reuse (idempotent).
	if deps.SandboxUserExists() {
		out.printf("• Sandbox user [bold]%s[/bold] already exists.", SandboxUser)
	} else {
		hostUser := deps.HostUser()
		uid := NextFreeID(deps.TakenIDs(), sandboxMinID)
		out.printf("• Creating sandbox user [bold]%s[/bold] (uid %d); "+
			"you may be prompted for your admin password by sudo.", SandboxUser, uid)
		for _, cmd := range CreateUserCommands(uid, uid, hostUser) {
			if deps.Run(append([]string{"sudo"}, cmd...)) != 0 {
				out.printf("[bold red]✗ setup step failed:[/bold red] %s", strings.Join(cmd, " "))
				return 1
			}
		}
		// Random password, piped via stdin (never argv).
		deps.SetRandomPassword()
		out.printf("  [green]created[/green] %s.", SandboxUser)
	}

	// 1b. Provision the neutral shared root (idempotent).
	out.printf("• Provisioning shared root [bold]%s[/bold] "+
		"(setgid + inheriting ACL, group _yolojail, no other-access) — "+
		"projects created under it are shared automatically, no per-run walk.", SharedRootDefault())
	for _, cmd := range SharedRootProvisionCommands("", deps.HostUser()) {
		if deps.Run(append([]string{"sudo"}, cmd...)) != 0 {
			out.printf("[bold red]✗ setup step failed:[/bold red] %s", strings.Join(cmd, " "))
			return 1
		}
	}

	// 2. Readiness checks — report each; neither fatal to setup.
	var warnings []string
	interp, ok := deps.ResolvePython()
	if !ok {
		warnings = append(warnings,
			"No Homebrew/Nix python3 found — the run path would fall back to "+
				"/usr/bin/python3, which is the xcode-select stub unless the "+
				"Command Line Tools are installed. Fix: `brew install python` or "+
				"`xcode-select --install`.")
		out.print("• python3 for the sandbox: [yellow]not found[/yellow]")
	} else {
		out.printf("• python3 for the sandbox: [green]%s[/green]", interp)
	}

	if !deps.Which("sandbox-exec") {
		warnings = append(warnings,
			"sandbox-exec not found on PATH — it ships with macOS, so this is "+
				"unusual; the run path needs it for the Seatbelt profile.")
		out.print("• Apple Seatbelt (sandbox-exec): [yellow]not found[/yellow]")
	} else {
		out.print("• Apple Seatbelt (sandbox-exec): [green]available[/green]")
	}

	if !deps.Which("nix") {
		warnings = append(warnings,
			"nix not found on PATH — the backend materializes `packages:` via "+
				"native nix; install it (https://nixos.org/download) or configs "+
				"with packages get no declared tools.")
		out.print("• nix (native darwin packages): [yellow]not found[/yellow]")
	} else {
		out.print("• nix (native darwin packages): [green]available[/green]")
	}

	// 3. One clear verdict + next steps.
	out.print("")
	if len(warnings) > 0 {
		out.print("[bold yellow]⚠ Setup done, but the macos-user backend is not " +
			"ready to run yet:[/bold yellow]")
		for _, w := range warnings {
			out.printf("  • %s", w)
		}
		out.print("\nResolve the above, then verify with " +
			"[bold]yolo run --dry-run[/bold] (prints the full plan; needs no " +
			"further setup).")
	} else {
		out.printf("[bold green]✓ macos-user backend ready.[/bold green] "+
			"Sandbox user '%s' is provisioned and preconditions "+
			"pass.", SandboxUser)
		out.printf("Next: put your project under [bold]%s/"+
			"<name>[/bold] (the agent can only share neutral ground, never a "+
			"path inside your home), set `runtime: \"macos-user\"` in "+
			"yolo-jail.jsonc (or YOLO_RUNTIME=macos-user), then from that "+
			"directory run [bold]yolo run --dry-run[/bold] to preview, or "+
			"[bold]yolo[/bold] to launch.\n"+
			"[dim]sudo will prompt per run — that's expected (we don't change "+
			"your sudo policy).[/dim]", SharedRootDefault())
	}
	return 0
}

// MacosTeardown deletes the sandbox account + home (needs admin). macOS only.
func MacosTeardown(deps Deps) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !deps.IsMacOS() {
		out.print("[bold red]yolo macos-teardown requires macOS.[/bold red]")
		return 1
	}
	if rc := refuseIfRoot(deps); rc != 0 {
		return rc
	}
	if !deps.SandboxUserExists() {
		out.printf("Sandbox user '%s' does not exist — nothing to do.", SandboxUser)
		return 0
	}
	for _, cmd := range DeleteUserCommands(deps.HostUser()) {
		deps.Run(append([]string{"sudo"}, cmd...))
	}
	out.printf("[green]✓ Removed sandbox user '%s'.[/green]", SandboxUser)
	return 0
}

// MacosUnshare strips the yolo-jail ACLs from a shared workspace (chmod -h -N).
// macOS only.
// resolvePathAbs to match Path(workspace).resolve().
func MacosUnshare(deps Deps, workspace string) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !deps.IsMacOS() {
		out.print("[bold red]yolo macos-unshare requires macOS.[/bold red]")
		return 1
	}
	if rc := refuseIfRoot(deps); rc != 0 {
		return rc
	}
	ws := resolvePathAbs(workspace)
	if !deps.PathIsDir(ws) {
		out.printf("[bold red]Not a directory:[/bold red] %s", ws)
		return 1
	}
	if deps.RunBash(WorkspaceACLStripScript(ws)) != 0 {
		out.printf("[yellow]ACL strip reported an error on %s.[/yellow]", ws)
		return 1
	}
	out.printf("[green]✓ Stripped yolo-jail ACLs from %s.[/green]", ws)
	return 0
}

// MacosFixPermissions retrofits the shared-group ACL onto pre-existing files in
// the shared area. macOS only.
// the whole shared root.
func MacosFixPermissions(deps Deps, path string) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !deps.IsMacOS() {
		out.print("[bold red]yolo macos-fix-permissions requires macOS.[/bold red]")
		return 1
	}
	if rc := refuseIfRoot(deps); rc != 0 {
		return rc
	}
	target := SharedRootDefault()
	if path != "" {
		target = resolvePathAbs(path)
	}
	if !deps.PathIsDir(target) {
		out.printf("[bold red]Not a directory:[/bold red] %s", target)
		return 1
	}
	if _, inHome := HomeContaining(target, ""); inHome {
		out.printf("[bold red]%s is inside a user home[/bold red] — the "+
			"macos-user backend only manages ACLs on neutral ground "+
			"(under %s or another non-home root).", target, SharedRootDefault())
		return 1
	}
	if deps.RunBash(FixPermissionsScript(target, "")) != 0 {
		out.printf("[yellow]Some ACLs could not be applied under %s "+
			"(e.g. a file whose ACL is locked). The rest were applied.[/yellow]", target)
		return 1
	}
	out.printf("[green]✓ Applied shared-group ACLs under %s.[/green]", target)
	return 0
}

// refuseIfRoot fails fast if invoked as root (under sudo). The macos-*
// commands self-escalate; running the whole command under sudo misassigns the
// shared-group ACL to root. Returns 1 (with the message printed) when euid==0,
// else 0.
func refuseIfRoot(deps Deps) int {
	if deps.Geteuid() == 0 {
		printer{w: deps.Out, color: deps.Color}.print(
			"[bold red]Don't run this under sudo.[/bold red]  Run it as your " +
				"normal admin user — it escalates each privileged step itself " +
				"(prompting for your password).  Running the whole command as root " +
				"would grant the shared-workspace ACL to 'root' instead of you and " +
				"silently break host↔sandbox file sharing.")
		return 1
	}
	return 0
}
