package runcmd

import (
	"bufio"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// loadAndValidateConfig ports run()'s config gate (lines 1205-1237): load
// strict, validate, gather same-file preset/null conflicts, print warnings,
// then print+exit on errors. Returns (config, ok). ok=false means the caller
// must exit(1) — the messages were already printed.
func (o *Options) loadAndValidateConfig() (*jsonx.OrderedMap, bool) {
	out := o.pr(o.Stdout)

	cfg, err := config.LoadConfig(o.Workspace, true, func(string) {})
	if err != nil {
		// ConfigError → print the message; any other load error also surfaces
		// (Python only catches ConfigError, but LoadConfig only returns
		// ConfigError in strict mode for malformed config).
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

// checkPresetNullConflicts ports _check_preset_null_conflicts: same-file
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

// resolveRuntime ports _runtime(config): the resolved container runtime
// ('podman' or 'container'), or ("", false) when none is reachable (the Python
// prints the actionable message and exits(1)). YOLO_RUNTIME / config.runtime win
// (validated against ALL_RUNTIMES) before platform auto-detection.
func (o *Options) resolveRuntime(cfg *jsonx.OrderedMap) (string, bool) {
	if env := o.Getenv("YOLO_RUNTIME"); env != "" && inStrSlice(paths.AllRuntimes, env) {
		return env, true
	}
	if rt := configRuntime(cfg); rt != "" && inStrSlice(paths.AllRuntimes, rt) {
		return rt, true
	}
	var candidates []string
	if o.IsMacOS {
		candidates = []string{"container", "podman"}
	} else {
		candidates = []string{"podman"}
	}
	for _, rt := range candidates {
		path, ok := o.LookPath(rt)
		if !ok {
			continue
		}
		if rt == "container" && !o.isAppleContainer(path) {
			continue
		}
		if !o.runtimeIsConnectable(rt) {
			continue
		}
		return rt, true
	}
	o.pr(o.Stdout).print(
		"[bold red]No container runtime found. Install podman, or on macOS, Apple's container CLI.[/bold red]")
	return "", false
}

// isAppleContainer ports _is_apple_container.
func (o *Options) isAppleContainer(path string) bool {
	res := o.Exec([]string{path, "--version"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return false
	}
	out := res.Stdout + res.Stderr
	return strings.Contains(out, "Apple") || strings.Contains(out, "container CLI version")
}

// runtimeIsConnectable ports _runtime_is_connectable.
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

// checkConfigChanges ports _check_config_changes via config.CheckConfigChanges,
// wiring the diff-printing prompter. Returns true to proceed, false to abort.
func (o *Options) checkConfigChanges(cfg *jsonx.OrderedMap) bool {
	pr := &changePrompter{o: o}
	ok, err := config.CheckConfigChanges(o.Workspace, cfg, o.IsTTYStdin(), pr)
	if err != nil {
		// A snapshot IO error is non-fatal in spirit; treat as proceed=false
		// only when the write genuinely failed. Python raises; we surface and
		// abort so the launch doesn't proceed on an unwritten snapshot.
		o.pr(o.Stdout).printf("[bold red]%s[/bold red]", err.Error())
		return false
	}
	return ok
}

// changePrompter renders the config diff and reads the y/N answer, mirroring the
// _check_config_changes console block.
type changePrompter struct{ o *Options }

func (p *changePrompter) Prompt(diffLines []string) bool {
	out := p.o.pr(p.o.Stdout)
	out.print("\n[bold yellow]⚠  Jail config changed since last run:[/bold yellow]\n")
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
