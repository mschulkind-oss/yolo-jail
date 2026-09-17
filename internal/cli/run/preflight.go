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
// then print+exit on errors, and last refuse a declared capability nothing
// satisfies. Returns (config, ok). ok=false means the caller
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
	// THE CAPABILITY GATE (agent-auth-modes.md OQ-CAP2), last in the config gate and
	// therefore below the shape errors above: `required_capabilities` is a string list
	// before it is a set of names, and reporting an unmet name for a value that is not
	// even a list would be reporting the second fault first.
	//
	// Here rather than beside checkProviderCredentials — the pre-flight this one most
	// resembles — because the two answer questions of different scope. That one asks
	// "does the environment this launch COMPOSES carry the credential", which needs the
	// channel, so it runs once per backend arm below the dispatch. This one asks "does
	// anything this config DECLARES satisfy the name", which is answerable from the
	// merged config alone; running it inside the config gate is what puts it above the
	// backend dispatch, so the refusal is the same on all four backends. DP-B30
	// (declaration-parity.md) recorded the alternative as a known defect in advance:
	// the delivery this replaces sits below the macos-user return, so a check written
	// there would silently not refuse on exactly one backend.
	if o.refuseUnmetCapabilities(cfg) {
		return nil, false
	}
	return cfg, true
}

// requiredCapabilityBaseline is the capability set every agent yolo can launch meets
// without anything declaring it: editing files and running commands are the floor of
// "a coding agent", so agent-auth-modes.md §6.2 makes them the default requirement and
// nothing has to declare them to satisfy them. Named here rather than left implicit
// because `"required_capabilities": ["code_editing"]` is the example the shipped
// config reference carries, and a gate that refused the floor would refuse the
// documented spelling.
var requiredCapabilityBaseline = []string{"code_editing", "command_execution"}

// AllowUnmetCapabilitiesEnv is the escape hatch out of the capability gate, in the style
// of AllowSourceSkewEnv and paths.AllowMissingProvidersEnv: a fatal refusal a user can
// overrule loudly, never quietly.
//
// It exists for the CENSUS BOUNDARY rather than for a yolo defect (AGENTS.md's rule: a
// hatch is for broken user config, never for a yolo bug). §6.1's first clause — agents
// and providers declaring their native capabilities in `pack.json` — is now BUILT
// (packdecl's `capabilities`, on the `program` and `provider` kinds), and the hatch's
// population has NOT shrunk by it, which is the part worth stating because the shape
// invites the opposite assumption: this gate reads the MERGED USER CONFIG, and pack
// declarations reach the launch through packload.ComposeProviders, which runs in
// assemble.go — BELOW this gate, by the same scope argument the function above makes for
// running here at all. So a capability an agent has natively, like Claude Code's own web
// search, is declared, consumed by capability-driven MCP delivery, and still invisible
// HERE. Closing that is a change of this gate's input, not of the declaration.
const AllowUnmetCapabilitiesEnv = "YOLO_ALLOW_UNMET_CAPABILITIES"

// capabilitySatisfiers maps every capability this config declares a source for to the
// phrase naming that source, for the refusal to quote back.
//
// TWO DECLARATION SURFACES, and they are the whole census OF THIS CONFIG:
// `providers.<name>.capabilities` (the agent has it natively when running on that
// provider) and an `mcp_servers.<name>` entry whose `provides` names it (§6.2's collision
// rule already refuses two servers claiming one name, so the mapping cannot be ambiguous
// by the time it gets here). `mcp_presets` contributes nothing on purpose: a preset is a
// baked command list (internal/entrypoint/mcp.go), and neither shipped preset declares a
// `provides`.
//
// A PACK'S OWN `capabilities` IS A THIRD SURFACE AND IS NOT IN THIS CENSUS — deliberately,
// and for the reason AllowUnmetCapabilitiesEnv states rather than by oversight: cfg is the
// merged user config, and a pack's provider row joins it in ComposeProviders, below this
// gate. A user who writes `providers.<name>.capabilities` over a pack's provider is read
// here; the pack's default alone is not.
//
// A provider satisfies REGARDLESS of whether a profile selects it, which is the one
// deliberate over-permission here. Narrowing to the ACTIVE provider needs the channel
// (profilechannel.go), which is composed below this gate, and erring toward launching is
// the right direction for a fatal refusal: the gap this closes is the config where
// NOTHING could satisfy the name — the agent discovering the hole at its first API call
// (setup-support-gaps.md G9) — not the config where the satisfier is present but
// unselected, which still fails loudly at the credential pre-flight.
func capabilitySatisfiers(cfg *jsonx.OrderedMap) map[string]string {
	out := map[string]string{}
	for _, name := range requiredCapabilityBaseline {
		out[name] = "the baseline every agent meets"
	}
	if providers := cfgMap(cfg, "providers"); providers != nil {
		for _, name := range providers.Keys() {
			p, _ := providers.Get(name)
			pm, ok := p.(*jsonx.OrderedMap)
			if !ok {
				continue // null drops the provider; it declares nothing
			}
			for _, capName := range cfgStrList(pm, "capabilities") {
				out[capName] = "provider '" + name + "'"
			}
		}
	}
	if servers := cfgMap(cfg, "mcp_servers"); servers != nil {
		for _, name := range servers.Keys() {
			s, _ := servers.Get(name)
			sm, ok := s.(*jsonx.OrderedMap)
			if !ok {
				// A null-removed server is a server this jail will not run — the null
				// crosses in YOLO_MCP_SERVERS and entrypoint's LoadMCPServers deletes
				// the entry rather than running it — so it provides nothing. This is
				// the one place the gate has to read the merged value rather than the
				// key set, or a `"tavily": null` in the workspace would keep satisfying
				// `web_search` off the user-level entry it just deleted.
				continue
			}
			if provides := mapStr(sm, "provides"); provides != "" {
				out[provides] = "mcp_servers." + name
			}
		}
	}
	return out
}

// unmetCapabilities returns the declared requirements nothing in cfg satisfies, in
// declaration order and de-duplicated, so the refusal names them the way the config does.
func unmetCapabilities(cfg *jsonx.OrderedMap) []string {
	have := capabilitySatisfiers(cfg)
	seen := map[string]bool{}
	var missing []string
	for _, name := range cfgStrList(cfg, "required_capabilities") {
		if name == "" || have[name] != "" || seen[name] {
			continue
		}
		seen[name] = true
		missing = append(missing, name)
	}
	return missing
}

// refuseUnmetCapabilities is OQ-CAP2's fatal refusal: a config that DECLARES it needs a
// capability, and declares nothing that provides it, stops the launch instead of starting
// a jail that discovers the hole at its first request. Reports whether the caller must
// stop.
//
// WHY A REFUSAL AND NOT A WARNING: the key's whole content is "this environment does not
// work without X". A launch that prints that and proceeds has answered a declaration with
// a note, which is the shape the key already had — validated, exported, read by nothing —
// and the shape this gate exists to end.
//
// Printed to Stderr rather than through the config gate's own Stdout printer: this is a
// launch refusal, and it reads beside checkProviderCredentials' and the notch gate's,
// which are the two it will be compared with. The config ERRORS above keep Stdout; moving
// them is a separate change with its own callers.
func (o *Options) refuseUnmetCapabilities(cfg *jsonx.OrderedMap) bool {
	missing := unmetCapabilities(cfg)
	if len(missing) == 0 {
		return false
	}
	named := "'" + strings.Join(missing, "', '") + "'"
	out := o.pr(o.Stderr)
	// The hatch is consulted only where it suppresses something — a launch with no gap
	// never announces it (providerpreflight.go's rule), and when it DOES suppress, the
	// notice says what: nothing was repaired.
	if o.Getenv(AllowUnmetCapabilitiesEnv) != "" {
		out.printf("[yellow]Warning: %s is set — CONTINUING with required capability %s "+
			"that nothing in this config satisfies. Nothing was repaired: whatever needed "+
			"the capability still has to do without it.[/yellow]",
			AllowUnmetCapabilitiesEnv, named)
		return false
	}
	out.printf("[bold red]Refusing to launch: config.required_capabilities declares %s, "+
		"and nothing this config declares satisfies it.[/bold red]", named)
	out.print("  A capability is satisfied by a declaration: `providers.<name>.capabilities` " +
		"naming it (the agent has it natively there), or an `mcp_servers.<name>` entry with " +
		"\"provides\": \"<capability>\".")
	out.printf("[dim]Declare the satisfier, drop the name from required_capabilities, or "+
		"launch anyway with %s=1.[/dim]", AllowUnmetCapabilitiesEnv)
	return true
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
	out.print("\n[bold yellow]⚠  Workspace config changed since last run:[/bold yellow]\n")
	printConfigDiff(out, diffLines)
	out.print("")
	if _, err := p.o.Stdout.Write([]byte("Accept these workspace config changes? [y/N] ")); err != nil {
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
