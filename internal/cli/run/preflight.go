package run

import (
	"bufio"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// loadAndValidateConfig is run()'s config gate: load
// strict, validate, gather same-file preset/null conflicts, print warnings,
// then print+exit on errors. Returns (config, ok). ok=false means the caller
// must exit(1) — the messages were already printed.
func (o *Options) loadAndValidateConfig() (*jsonx.OrderedMap, bool) {
	out := o.pr(o.Stdout)

	cfg, err := config.LoadConfig(o.Workspace, true, func(string) {})
	if err != nil {
		// ConfigError → print the message; any other load error also surfaces
		// (LoadConfig only returns ConfigError in strict mode for malformed
		// config).
		out.printf("[bold red]%s[/bold red]", err.Error())
		return nil, false
	}

	resolver := loopholeResolver()
	configErrors, configWarnings := config.ValidateConfig(cfg, o.Workspace, resolver)

	// Cross-hierarchy overrides are valid, but same-file contradictions are not.
	userPath := paths.UserConfigPath()
	userRaw, err := config.LoadJSONCFile(userPath, userPath, false, func(string) {})
	if err != nil || userRaw == nil {
		userRaw = jsonx.NewOrderedMap()
	}
	wsPath := o.Workspace + "/yolo-jail.jsonc"
	wsRaw, err := config.LoadJSONCFile(wsPath, "yolo-jail.jsonc", false, func(string) {})
	if err != nil || wsRaw == nil {
		wsRaw = jsonx.NewOrderedMap()
	}
	configErrors = append(configErrors, checkPresetNullConflicts(userRaw, userPath)...)
	configErrors = append(configErrors, checkPresetNullConflicts(wsRaw, "yolo-jail.jsonc")...)

	for _, msg := range configWarnings {
		out.printf("  [yellow]⚠ %s[/yellow]", msg)
	}
	if len(configErrors) > 0 {
		out.print("[bold red]Invalid jail config:[/bold red]")
		for _, msg := range configErrors {
			out.print("  • " + msg)
		}
		out.print("\n[dim]Run `yolo check` for a full preflight before restarting.[/dim]")
		return nil, false
	}
	return cfg, true
}

// checkPresetNullConflicts detects a same-file
// preset/null contradiction (a preset enabled in mcp_presets but null-removed in
// mcp_servers within the same file).
func checkPresetNullConflicts(cfg *jsonx.OrderedMap, label string) []string {
	var errs []string
	presetsV, _ := cfg.Get("mcp_presets")
	serversV, _ := cfg.Get("mcp_servers")
	presets, okP := presetsV.([]any)
	servers, okS := serversV.(*jsonx.OrderedMap)
	if !okP || !okS {
		return errs
	}
	for _, nameV := range presets {
		name, ok := nameV.(string)
		if !ok {
			continue
		}
		if v, present := servers.Get(name); present && v == nil {
			errs = append(errs, label+": preset '"+name+"' is enabled in mcp_presets but "+
				"null-removed in mcp_servers within the same config file")
		}
	}
	return errs
}

// resolveRuntime returns the resolved container runtime
// ('podman' or 'container'), or ("", false) when none is reachable (prints the
// actionable message; the caller exits 1). YOLO_RUNTIME / config.runtime win
// (validated against ALL_RUNTIMES) before platform auto-detection.
func (o *Options) resolveRuntime(cfg *jsonx.OrderedMap) (string, bool) {
	if env := o.Getenv("YOLO_RUNTIME"); env != "" && inStrSlice(paths.AllRuntimes, env) {
		return o.validateExplicitRuntime(env, "YOLO_RUNTIME")
	}
	if rt := configRuntime(cfg); rt != "" && inStrSlice(paths.AllRuntimes, rt) {
		return o.validateExplicitRuntime(rt, "yolo-jail.jsonc")
	}
	var candidates []string
	if o.IsMacOS {
		candidates = []string{"container", "podman"}
	} else {
		candidates = []string{"podman"}
	}
	var offline []string // installed but daemon/VM not up
	for _, rt := range candidates {
		path, ok := o.LookPath(rt)
		if !ok {
			continue
		}
		if rt == "container" && !o.isAppleContainer(path) {
			continue
		}
		if !o.runtimeIsConnectable(rt) {
			offline = append(offline, rt)
			continue
		}
		return rt, true
	}
	// A runtime that is installed but not started is a distinct, actionable case
	// from nothing installed — mirror `yolo check` rather than the misleading
	// "install podman" (it IS installed; it just needs starting).
	if len(offline) > 0 {
		out := o.pr(o.Stdout)
		out.printf("[bold red]Container runtime installed but not started (%s).[/bold red]",
			strings.Join(offline, ", "))
		for _, rt := range offline {
			out.printf("[dim]%s[/dim]", runtimeStartHint(rt))
		}
		return "", false
	}
	o.pr(o.Stdout).print(
		"[bold red]No container runtime found. Install podman, or on macOS, Apple's container CLI.[/bold red]")
	return "", false
}

// validateExplicitRuntime gates an explicitly-selected runtime (YOLO_RUNTIME or
// config.runtime, source names the origin). Native runtimes (macos-user) aren't
// on PATH — their availability is checked downstream — so they pass through.
// Container runtimes must be installed AND started: without this catch a
// `YOLO_RUNTIME=podman` with no podman (or a stopped `podman machine`) sails
// past into the image build and only surfaces as an opaque nix/builder failure
// three layers deep.
func (o *Options) validateExplicitRuntime(rt, source string) (string, bool) {
	if inStrSlice(paths.NativeRuntimes, rt) {
		return rt, true
	}
	out := o.pr(o.Stdout)
	if _, ok := o.LookPath(rt); !ok {
		out.printf("[bold red]Configured runtime '%s' (from %s) is not installed.[/bold red]", rt, source)
		out.printf("[dim]Install it, or unset %s to auto-detect. Run `yolo check` to validate.[/dim]", source)
		return "", false
	}
	if !o.runtimeIsConnectable(rt) {
		out.printf("[bold red]Configured runtime '%s' (from %s) is installed but not started.[/bold red]", rt, source)
		out.printf("[dim]%s[/dim]", runtimeStartHint(rt))
		return "", false
	}
	return rt, true
}

// runtimeStartHint is the "it's installed, just start it" one-liner for a
// container runtime, kept in step with `yolo check`'s liveness hints.
func runtimeStartHint(rt string) string {
	if rt == "container" {
		return "Start it: `container system start`"
	}
	return "Start it: `podman machine start` " +
		"(first time: `podman machine init && podman machine start`)"
}

// isAppleContainer reports whether the runtime at path is Apple's container CLI.
func (o *Options) isAppleContainer(path string) bool {
	res := o.Exec([]string{path, "--version"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return false
	}
	out := res.Stdout + res.Stderr
	return strings.Contains(out, "Apple") || strings.Contains(out, "container CLI version")
}

// runtimeIsConnectable reports whether the runtime's daemon is reachable.
func (o *Options) runtimeIsConnectable(rt string) bool {
	if rt == "container" {
		res := o.Exec([]string{"container", "system", "status"}, "", nil, 5*time.Second)
		if !res.Ran || res.Timeout {
			return false
		}
		return res.RC == 0 && strings.Contains(strings.ToLower(res.Stdout), "running")
	}
	res := o.Exec([]string{rt, "info"}, "", nil, 10*time.Second)
	if !res.Ran || res.Timeout {
		return false
	}
	return res.RC == 0
}

// checkConfigChanges delegates to config.CheckConfigChanges,
// wiring the diff-printing prompter. Returns true to proceed, false to abort.
func (o *Options) checkConfigChanges(cfg *jsonx.OrderedMap) bool {
	pr := &changePrompter{o: o}
	ok, err := config.CheckConfigChanges(o.Workspace, cfg, o.IsTTYStdin(), o.AcceptConfigChanges, pr)
	if err != nil {
		// The OQ-D2 refusal gets rendered rather than dumped: same diff, same
		// colours as the interactive prompt, so the two paths show the reader the
		// same change and differ only in how they end. Everything else is a
		// snapshot IO failure — surface it and abort, so the launch never proceeds
		// on an unwritten approval record.
		var changed *config.ChangedNonInteractiveError
		if errors.As(err, &changed) {
			o.printChangeRefusal(changed)
			return false
		}
		o.pr(o.Stdout).printf("[bold red]%s[/bold red]", err.Error())
		return false
	}
	return ok
}

// printChangeRefusal renders the non-interactive refusal: headline, the diff in
// the prompt's own colours, then the advice that names the flag.
func (o *Options) printChangeRefusal(e *config.ChangedNonInteractiveError) {
	out := o.pr(o.Stdout)
	out.printf("\n[bold red]⚠  %s[/bold red]\n", e.Headline())
	printConfigDiff(out, e.DiffLines)
	out.print("")
	out.print(e.Advice())
}

// writeLaunchConfigArtifacts writes the two workspace-side config files a fresh
// launch owes the jail it is about to start. Both live under <workspace>/.yolo,
// both are written by the HOST, and neither is the approval record — that moved
// out of the workspace entirely under OQ-D1 (config.ApprovalSnapshotPath).
//
//   - config-assembled.json: the MERGED config this launch is using. The in-jail
//     LoadConfig reads it back verbatim for the jail's own workspace, because the
//     user-level `include_if_found` overrides it carries are host-side files the
//     jail never sees — re-assembling in there silently yields a reduced config.
//   - config-boot.json: the WORKSPACE-ONLY config, frozen for the jail's life so an
//     in-jail `yolo config drift` has an immutable thing to diff the live file
//     against. Loaded through the same loader the in-jail diff uses, so the two
//     sides compare exactly.
//
// Both are best-effort. A jail must not fail to launch because one of these
// hiccuped: without the assembled copy the in-jail read degrades to the documented
// re-assemble, and without the baseline `drift` reports "cannot determine" rather
// than a false "no drift". Neither degradation is worth refusing a launch over.
func (o *Options) writeLaunchConfigArtifacts(cfg *jsonx.OrderedMap) {
	out := o.pr(o.Stdout)
	if err := config.WriteAssembledConfig(o.Workspace, cfg); err != nil {
		out.printf("[dim]Warning: could not write the assembled config for the jail: %s[/dim]", err.Error())
	}
	if wsCfg, wsErr := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {}); wsErr == nil {
		if err := config.WriteWorkspaceBootBaseline(o.Workspace, wsCfg); err != nil {
			out.printf("[dim]Warning: could not write config drift baseline: %s[/dim]", err.Error())
		}
	}
}

// printConfigDiff renders a unified config diff in the launcher's colours. Shared
// by the interactive prompt and the non-interactive refusal so a reader who cannot
// be prompted sees the change in exactly the form the human at a terminal would.
func printConfigDiff(out printer, diffLines []string) {
	for _, line := range diffLines {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			out.printf("[dim]%s[/dim]", line)
		case strings.HasPrefix(line, "+"):
			out.printf("[green]%s[/green]", line)
		case strings.HasPrefix(line, "-"):
			out.printf("[red]%s[/red]", line)
		case strings.HasPrefix(line, "@@"):
			out.printf("[cyan]%s[/cyan]", line)
		default:
			out.print(line)
		}
	}
}

// changePrompter renders the config diff and reads the y/N answer.
type changePrompter struct{ o *Options }

func (p *changePrompter) Prompt(diffLines []string) bool {
	out := p.o.pr(p.o.Stdout)
	out.print("\n[bold yellow]⚠  Jail config changed since last run:[/bold yellow]\n")
	printConfigDiff(out, diffLines)
	out.print("")
	// input("Accept these config changes? [y/N] ")
	if _, err := p.o.Stdout.Write([]byte("Accept these config changes? [y/N] ")); err != nil {
		return false
	}
	scanner := bufio.NewScanner(p.o.Stdin)
	if !scanner.Scan() {
		out.print("\n[red]Aborted.[/red]")
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if answer == "y" || answer == "yes" {
		return true
	}
	out.print("[red]Config changes rejected. Exiting.[/red]")
	return false
}
