package run

import (
	"bufio"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// loadAndValidateConfig is run()'s config gate: load
// strict, validate, gather same-file preset/null conflicts, print warnings,
// then print+exit on errors, and last refuse a declared capability nothing
// satisfies. Returns (config, ok). ok=false means the caller
// must exit(1) — the messages were already printed.
func (o *Options) loadAndValidateConfig() (*jsonx.OrderedMap, bool) {
	out := o.pr(o.Stdout)

	// WithSources: every refusal below names the file and line its key was written at
	// (config's sources.go), since the config is composed from many files.
	cfg, src, err := config.LoadConfigWithSources(o.Workspace, true, func(string) {})
	o.configSources = src
	if err != nil {
		// ConfigError → print the message; any other load error also surfaces
		// (LoadConfig only returns ConfigError in strict mode for malformed
		// config).
		out.printf("[bold red]%s[/bold red]", err.Error())
		return nil, false
	}

	resolver := loopholeResolver()
	configErrors, configWarnings := config.ValidateConfig(cfg, o.Workspace, resolver)
	configErrors = src.Annotate(configErrors)
	configWarnings = src.Annotate(configWarnings)

	// Cross-hierarchy overrides are valid, but same-file contradictions are not.
	userPath := paths.UserConfigPath()
	userRaw, userSrc, err := config.LoadJSONCFileWithSources(userPath, userPath, false, func(string) {})
	if err != nil || userRaw == nil {
		userRaw = jsonx.NewOrderedMap()
	}
	wsPath := o.Workspace + "/yolo-jail.jsonc"
	wsRaw, wsSrc, err := config.LoadJSONCFileWithSources(wsPath, "yolo-jail.jsonc", false, func(string) {})
	if err != nil || wsRaw == nil {
		wsRaw = jsonx.NewOrderedMap()
	}
	configErrors = append(configErrors, config.PresetNullConflicts(userRaw, userPath, userSrc)...)
	configErrors = append(configErrors, config.PresetNullConflicts(wsRaw, "yolo-jail.jsonc", wsSrc)...)

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
	// anything this config or its selected packs DECLARE satisfy the name", which is
	// answerable from the merged config and the selection's declarations, both readable
	// before staging; running it inside the config gate is what puts it above the
	// backend dispatch, so the refusal is the same on all four backends. DP-B30
	// (declaration-parity.md) recorded the alternative as a known defect in advance:
	// the delivery this replaces sits below the macos-user return, so a check written
	// there would silently not refuse on exactly one backend.
	if o.refuseUnmetCapabilities(cfg, src) {
		return nil, false
	}
	return cfg, true
}

// AllowUnmetCapabilitiesEnv is the escape hatch out of the capability gate, in the style of
// AllowSourceSkewEnv and paths.AllowMissingProvidersEnv: a fatal refusal a user can overrule
// loudly, never quietly. config.AllowUnmetCapabilitiesEnv is the one spelling, which `yolo
// check` reads too, and it says what the hatch is for.
const AllowUnmetCapabilitiesEnv = config.AllowUnmetCapabilitiesEnv

// capabilityLaunch is the launch the capability gate counts: this launch's own pack selection,
// resolved read-only because the gate runs before staging, under this launch's own profile table.
// The entries are narrowed as stagePacks narrows them (a fork build, seal.go), and the selection
// closure and the profile sets both read the config's `profile` key with `-p` folded over it
// (launchSelectionFor, effectiveUseProfiles), so the census counts the agents this launch will
// stage, on the sources this launch will run them on: not the user scope's selection alone, not
// the config's `profile` key alone, and never every pack yolo ships.
func (o *Options) capabilityLaunch(cfg *jsonx.OrderedMap) *config.CapabilityLaunch {
	return &config.CapabilityLaunch{
		Packs: func() ([]*packload.Pack, bool) {
			entries, err := config.LoadPacks(func(string) {})
			if err != nil {
				return nil, false
			}
			return config.SelectedPackDeclarations(o.narrowedPackEntries(entries),
				o.launchSelectionFor(cfg), o.Getenv)
		},
		ProfileSets: func(packs []*packload.Pack) map[string][]string {
			return packload.ProfileSets(o.effectiveUseProfiles(cfg, packs))
		},
	}
}

// refuseUnmetCapabilities is OQ-CAP2's fatal refusal: a config that DECLARES it needs a
// capability, and has nothing that provides it, stops the launch instead of starting a jail that
// discovers the hole at its first request. Reports whether the caller must stop.
//
// The census is config.UnmetCapabilities, the one `yolo check` predicts with: the user's own
// declarations, then the selected packs' (agent-auth-modes.md §6.1 clause 1) — each installed
// agent's ACTIVE source, the provider its profile selects (composed under the user's overrides
// as the launch composes it) or, with no profile, its built-in login. Until 2026-09-30 it read
// the merged user config alone, so requiring `web_search` with claude selected was refused
// although claude searches natively.
//
// WHY A REFUSAL AND NOT A WARNING: the key's whole content is "this environment does not work
// without X". A launch that prints that and proceeds has answered a declaration with a note,
// which is the shape the key already had — validated, exported, read by nothing — and the shape
// this gate exists to end.
//
// A CENSUS THAT COULD NOT LOOK DOES NOT REFUSE. When a selected pack could not be read, the
// provider table did not compose, or the profiles did not resolve, the unread half may hold the
// satisfier, and each is a fault this launch refuses on its own further down (pack staging is
// fail-closed, and the channel composition refuses the table and the profiles) with its own
// message. Refusing here would name the second fault first, so the gate says what it could not
// check and lets that step speak.
//
// Printed to Stderr rather than through the config gate's own Stdout printer: this is a launch
// refusal, and it reads beside checkProviderCredentials' and the notch gate's, which are the two
// it will be compared with. The config ERRORS above keep Stdout; moving them is a separate change
// with its own callers.
//
// src locates the key in the files that wrote it (config's sources.go); nil locates nothing.
func (o *Options) refuseUnmetCapabilities(cfg *jsonx.OrderedMap, src *config.Sources) bool {
	missing, err := config.UnmetCapabilities(cfg, o.capabilityLaunch(cfg))
	if len(missing) == 0 {
		return false
	}
	named := "'" + strings.Join(missing, "', '") + "'"
	out := o.pr(o.Stderr)
	if err != nil {
		out.printf("[yellow]Warning: cannot tell whether anything satisfies required capability "+
			"%s: %s. Continuing: this launch reports that problem itself further on.[/yellow]",
			named, err.Error())
		return false
	}
	// The hatch is consulted only where it suppresses something — a launch with no gap never
	// announces it (providerpreflight.go's rule), and when it DOES suppress, the notice says
	// what: nothing was repaired.
	if o.Getenv(AllowUnmetCapabilitiesEnv) != "" {
		out.printf("[yellow]Warning: %s is set — CONTINUING with required capability %s "+
			"that nothing in this launch satisfies. Nothing was repaired: whatever needed "+
			"the capability still has to do without it.[/yellow]",
			AllowUnmetCapabilitiesEnv, named)
		return false
	}
	out.printf("[bold red]Refusing to launch: config.required_capabilities declares %s, "+
		"and nothing this config or its selected packs declare satisfies it.[/bold red]", named)
	if where := src.Locations("config.required_capabilities"); len(where) > 0 {
		out.print("  config.required_capabilities is written at " + strings.Join(where, " and at ") + ".")
	}
	out.print("  A capability is satisfied by a declaration: `providers.<name>.capabilities` " +
		"naming it (the agent has it natively there), an `mcp_servers.<name>` entry with " +
		"\"provides\": \"<capability>\", or a selected agent whose pack declares it for the " +
		"source the agent runs on: its built-in login, or the provider its profile selects.")
	out.printf("[dim]Declare the satisfier, drop the name from required_capabilities, or "+
		"launch anyway with %s=1.[/dim]", AllowUnmetCapabilitiesEnv)
	return true
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
	var offline []string
	var failures []string
	for _, rt := range candidates {
		path, ok := o.LookPath(rt)
		if !ok {
			continue
		}
		if rt == "container" && !o.isAppleContainer(path) {
			continue
		}
		if ok, reason := o.runtimeIsConnectable(rt); !ok {
			offline = append(offline, rt)
			failures = append(failures, reason)
			continue
		}
		return rt, true
	}
	// PATH presence does not tell us whether a VM is stopped: on Linux there
	// is no VM, and a failed info probe may instead be a transient error.
	if len(offline) > 0 {
		out := o.pr(o.Stdout)
		if o.IsMacOS {
			out.printf("[bold red]Container runtime installed but not started (%s).[/bold red]",
				strings.Join(offline, ", "))
		} else {
			out.printf("[bold red]Cannot query container runtime (%s).[/bold red]",
				strings.Join(offline, ", "))
		}
		for i, rt := range offline {
			out.print(failures[i])
			out.printf("[dim]%s[/dim]", runtimeStartHint(rt, o.IsMacOS))
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
	if ok, reason := o.runtimeIsConnectable(rt); !ok {
		if o.IsMacOS {
			out.printf("[bold red]Configured runtime '%s' (from %s) is installed but not started.[/bold red]", rt, source)
		} else {
			out.printf("[bold red]Cannot query configured runtime '%s' (from %s).[/bold red]", rt, source)
		}
		out.print(reason)
		out.printf("[dim]%s[/dim]", runtimeStartHint(rt, o.IsMacOS))
		return "", false
	}
	return rt, true
}

// runtimeStartHint gives a platform-specific next step for a failed probe.
func runtimeStartHint(rt string, isMacOS bool) string {
	if rt == "container" {
		return "Start it: `container system start`"
	}
	if !isMacOS {
		return "Run `podman info` to diagnose the failure."
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

// runtimeIsConnectable reports whether the runtime answers and, if not, why.
//
// Podman on Linux goes through the READINESS GATE (podmanready.go): up to a minute for
// `podman info` to answer, never killing a probe that may be doing podman's post-boot
// cleanup, and refusing at once only on an answer that cannot clear on its own
// (docs/design/podman-reboot-readiness.md). Everything else keeps the one-shot probe,
// bounded at 5 s (Apple Container) and 10 s (podman on macOS, where a stopped VM and a
// starting one look alike to `podman info`); a cold runtime can spend most of that, so it
// has a progress line too (silent when it answers promptly).
func (o *Options) runtimeIsConnectable(rt string) (ok bool, reason string) {
	if o.usesReadinessGate(rt) {
		return o.waitForPodman(rt)
	}
	o.withStderrProgress("Checking that "+rt+" is running", func() bool {
		ok, reason = o.probeRuntime(rt)
		return ok
	})
	return ok, reason
}

func (o *Options) probeRuntime(rt string) (bool, string) {
	if rt == "container" {
		res := o.Exec([]string{"container", "system", "status"}, "", nil, 5*time.Second)
		return res.Ran && !res.Timeout && res.RC == 0 &&
			strings.Contains(strings.ToLower(res.Stdout), "running"), runtimeProbeFailure("container system status", res)
	}
	res := o.Exec([]string{rt, "info"}, "", nil, 10*time.Second)
	return res.Ran && !res.Timeout && res.RC == 0, runtimeProbeFailure(rt+" info", res)
}

func runtimeProbeFailure(command string, res ExecResult) string {
	prefix := command + " "
	switch {
	case !res.Ran:
		return prefix + "could not run."
	case res.Timeout:
		prefix += "timed out."
	case res.RC != 0:
		prefix += fmt.Sprintf("failed (exit %d).", res.RC)
	default:
		prefix += "did not report running."
	}
	if detail := strings.TrimSpace(res.Stderr); detail != "" {
		return prefix + " " + detail
	}
	return prefix
}

// checkConfigChanges delegates to config.CheckConfigAndScopeChanges, wiring the
// diff-printing prompter. Returns true to proceed, false to abort.
//
// wsCfg is the workspace config the approval record's config part holds; merged and rt
// decide whether a brokered loophole's repository scope is in play — the approval record's
// second part (docs/design/boundary-broker.md BB-D30) — by the same predicate the spawn
// applies. The scope is READ HERE, at every fresh launch that starts such a loophole, and
// only here: an attach never reaches this function.
func (o *Options) checkConfigChanges(wsCfg, merged *jsonx.OrderedMap, rt string) bool {
	pr := &changePrompter{o: o}
	scope := o.brokeredScopeCheck(rt, merged)
	ok, err := config.CheckConfigAndScopeChanges(o.Workspace, wsCfg, scope, o.IsTTYStdin(), o.AcceptConfigChanges, pr)
	if ok && err == nil {
		o.recordApprovedScopes(scope)
	}
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
	printScopeBlock(out, e.ScopeBlock)
	printConfigDiff(out, e.DiffLines)
	out.print("")
	// The advice names files, one of them the git config the scope was read from, which can
	// sit in a directory the agent named; it carries no markup of its own.
	out.print(richtext.Escape(e.Advice()))
}

// printScopeBlock renders the labeled repository-scope block (BB-D31) in the launcher's
// colours: a repository added in green, one removed in red. It is printed FIRST, above the
// config diff, because it is the part a routine package change could otherwise carry past
// a reader.
func printScopeBlock(out printer, block []string) {
	for _, line := range block {
		switch {
		case strings.HasPrefix(line, "  + "):
			out.printf("[bold green]%s[/bold green]", richtext.Escape(line))
		case strings.HasPrefix(line, "  - "):
			out.printf("[bold red]%s[/bold red]", richtext.Escape(line))
		case !strings.HasPrefix(line, "  "):
			out.printf("[bold]%s[/bold]", richtext.Escape(line))
		default:
			out.print(richtext.Escape(line))
		}
	}
	if len(block) > 0 {
		out.print("")
	}
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
	return p.PromptReport(config.ChangeReport{ConfigChanged: true, DiffLines: diffLines})
}

// PromptReport shows the whole change and asks once (BB-D31): the header and the question
// name the repository scope whenever it changed, and only the scope when the config did
// not, and the scope block comes first. One y approves the whole bundle, as ruled.
func (p *changePrompter) PromptReport(r config.ChangeReport) bool {
	out := p.o.pr(p.o.Stdout)
	header, question := "Workspace config changed since last run:",
		"Accept these workspace config changes? [y/N] "
	switch {
	case r.ScopeChanged && r.ConfigChanged:
		header, question = "Workspace config and repository scope changed since last run:",
			"Accept these workspace config and repository scope changes? [y/N] "
	case r.ScopeChanged:
		header, question = "Repository scope changed since last run:",
			"Accept these repository scope changes? [y/N] "
	}
	out.print("\n[bold yellow]⚠  " + header + "[/bold yellow]\n")
	printScopeBlock(out, r.ScopeBlock)
	printConfigDiff(out, r.DiffLines)
	out.print("")
	if _, err := p.o.Stdout.Write([]byte(question)); err != nil {
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
	lines := declineLines(r)
	out.print("[red]" + richtext.Escape(lines[0]) + "[/red]")
	for _, line := range lines[1:] {
		out.print(richtext.Escape(line))
	}
	return false
}

// declineLines is what a `N` at the config-change prompt prints: that nothing was recorded, then
// the next step for each part that changed (docs/reference/happy-path-principle.md rule 1). A
// changed config names the files it was read from, and that the next launch asks again; a changed
// repository scope names the command that launches this project without the loophole that reads
// it, since that is the one way past the question that answers no to it.
func declineLines(r config.ChangeReport) []string {
	head := "Config changes rejected; nothing was recorded. Exiting."
	switch {
	case r.ScopeChanged && r.ConfigChanged:
		head = "Workspace config and repository scope changes rejected; nothing was recorded. Exiting."
	case r.ScopeChanged:
		head = "Repository scope changes rejected; nothing was recorded. Exiting."
	}
	lines := []string{head}
	if r.ConfigChanged && len(r.ConfigFiles) > 0 {
		lines = append(lines, "The workspace config change is in "+strings.Join(r.ConfigFiles, " and ")+
			": undo it there, or answer y at the next launch, which asks again.")
	}
	for _, l := range r.ScopeLabels {
		lines = append(lines, "To launch this project without "+l+", run `"+
			config.DisableLoopholeCommand(l, r.Workspace)+"`; otherwise the next launch asks about "+
			"its repositories again.")
	}
	return lines
}
