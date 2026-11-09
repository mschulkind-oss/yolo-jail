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
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// loadAndValidateConfig is run()'s config gate: load
// strict, validate, gather same-file preset/null conflicts, print warnings,
// then print+exit on errors, and last refuse a declared capability nothing
// satisfies. Returns (config, ok). ok=false means the caller
// must exit(1) — the messages were already printed.
func (o *Options) loadAndValidateConfig() (*jsonx.OrderedMap, bool) {
	out := o.pr(o.Stderr)

	// WithSources: every refusal below names the file and line its key was written at
	// (config's sources.go), since the config is composed from many files.
	cfg, src, err := config.LoadConfigWithSources(o.Workspace, true, func(string) {})
	o.configSources = src
	if err != nil {
		// ConfigError → print the message; any other load error also surfaces
		// (LoadConfig only returns ConfigError in strict mode for malformed
		// config).
		out.printf("[bold red]%s[/bold red]", configMessageText(err.Error()))
		return nil, false
	}

	resolver := loopholeResolver()
	configErrors, configWarnings := config.ValidateConfig(cfg, o.Workspace, resolver)
	configErrors = src.Annotate(configErrors)
	configWarnings = src.Annotate(configWarnings)
	// The pack switch: an `allow.<pack>` table naming a pack `packs` does not list selects
	// nothing, so it is refused here exactly as `yolo check` refuses it (config-user.md §7).
	configErrors = append(configErrors, config.AllowPackSwitchErrors()...)

	for _, msg := range configWarnings {
		out.printf("  [yellow]⚠ %s[/yellow]", configMessageText(msg))
	}
	if len(configErrors) > 0 {
		out.print("[bold red]Invalid jail config:[/bold red]")
		for _, msg := range configErrors {
			out.print("  • " + configMessageText(msg))
		}
		out.print("\n[dim]Run `yolo check` for a full preflight before restarting.[/dim]")
		return nil, false
	}
	// THE CAPABILITY GATE (mcp-configuration.md OQ-CAP2), last in the config gate and
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

// configMessageText is a config message as the launch prints it, through markup. It names the
// file its key was written in and can echo a key or a value, and the agent writes a workspace's
// includes and names them, so every rune a terminal acts on is escaped and so is any markup,
// while the newlines yolo wrote stay (docs/design/workspace-widening.md WW-D30).
func configMessageText(msg string) string {
	return richtext.Escape(termsafe.VisibleLines(msg))
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
// the config's `profile` key alone, and never every pack yolo ships. (A narrowed launch does not
// ask the gate at all: refuseUnmetCapabilities, PPX-D39.)
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
// declarations, then the selected packs' (mcp-configuration.md#the-capability-gate) — each installed
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
	if o.selectionNarrowed() {
		// A BUILD JAIL'S NARROWED SELECTION (seal.go, PPX-D39): the capability a config requires
		// is one its agents need, a build jail runs none, and the agent that provides it is often
		// a pack the narrowing dropped. The user's own launches count the whole selection.
		return false
	}
	missing, err := config.UnmetCapabilities(cfg, o.capabilityLaunch(cfg))
	// The words are config.UnmetCapabilityRefusal's, shared with `yolo host --` (the gate's host
	// notch), so the two notches cannot come to say different things about one gap; the markup is
	// this notch's own: a warning in yellow, a refusal's verdict in bold red and its next step dim.
	lines, refuse := config.UnmetCapabilityRefusal(missing, err,
		o.Getenv(AllowUnmetCapabilitiesEnv) != "", src.Locations("config.required_capabilities"))
	out := o.pr(o.Stderr)
	for i, line := range lines {
		switch {
		case !refuse:
			out.print("[yellow]" + line + "[/yellow]")
		case i == 0:
			out.print("[bold red]" + line + "[/bold red]")
		case i == len(lines)-1:
			out.print("[dim]" + line + "[/dim]")
		default:
			out.print(line)
		}
	}
	return refuse
}

// resolveRuntime returns this launch's backend ('podman', 'container' or 'macos-user'), the
// one its setup names (Setup.Backend; resolveSetup chose and gated the setup), or ("", false)
// when that runtime cannot take the launch (it prints the actionable message; the caller exits
// 1). There is no probing (design §2: one named default per OS): a setup names its technology,
// and a missing or stopped one is refused naming the setup.
//
// A caller that has not run resolveSetup (`yolo config apply`) gets it run here.
func (o *Options) resolveRuntime(cfg *jsonx.OrderedMap) (string, bool) {
	if o.Setup.Name == "" {
		if _, refused := o.resolveSetup(cfg); refused {
			return "", false
		}
	}
	return o.validateExplicitRuntime(o.Setup.Backend(), "setup "+o.Setup.Name)
}

// validateExplicitRuntime gates the setup's runtime (source names the setup). Native runtimes
// (macos-user) aren't on PATH — their availability is checked downstream — so they pass through.
// Container runtimes must be installed AND started: without this catch a podman-self launch
// with no podman (or a stopped `podman machine`) sails past into the image build and only
// surfaces as an opaque nix/builder failure three layers deep.
func (o *Options) validateExplicitRuntime(rt, source string) (string, bool) {
	if inStrSlice(paths.NativeRuntimes, rt) {
		return rt, true
	}
	out := o.pr(o.Stderr)
	if _, ok := o.LookPath(rt); !ok {
		out.printf("[bold red]The runtime '%s' (for %s) is not installed.[/bold red]", rt, source)
		out.printf("[dim]Install it, or choose another setup (`yolo setup list` shows them). Run `yolo check` to validate.[/dim]")
		return "", false
	}
	if ok, down := o.runtimeIsConnectable(rt); !ok {
		switch {
		case down.unanswered:
			out.printf("[bold red]The runtime '%s' (for %s) is installed but not answering.[/bold red]", rt, source)
		case o.IsMacOS && !down.scratch:
			out.printf("[bold red]The runtime '%s' (for %s) is installed but not started.[/bold red]", rt, source)
		default:
			out.printf("[bold red]Cannot query the runtime '%s' (for %s).[/bold red]", rt, source)
		}
		down.print(out)
		return "", false
	}
	return rt, true
}

// runtimeDown is why runtime selection could not use a runtime it found on PATH, in the words
// its refusal prints under the headline.
type runtimeDown struct {
	// reason is the probe's evidence: what it ran and what came back.
	reason string
	// hint is the next step, "" when reason already ends in it.
	hint string
	// unanswered: the runtime took the probe and did not answer, which a stopped Podman
	// machine never does (runtime.WaitForPodmanMachine, PR-D24). It is reported as not
	// answering, never as not started.
	unanswered bool
	// scratch: the probe ended on yolo's own scratch file (runtime.ReadyResult.EndedOnScratchError),
	// so its last attempt never ran podman. reason names the step that error calls for and hint
	// is "", and no headline calls the runtime stopped: a podman step would be the wrong one
	// (PR-D23).
	scratch bool
}

// print writes the refusal's body under its headline: the reason, then the next step when it
// is not already the reason's last line.
func (d runtimeDown) print(out printer) {
	out.print(d.reason)
	if d.hint != "" {
		out.printf("[dim]%s[/dim]", d.hint)
	}
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

// runtimeIsConnectable reports whether the runtime answers and, if not, why.
//
// Podman on Linux goes through the READINESS GATE (podmanready.go): up to a minute for
// `podman info` to answer, never killing a probe that may be doing podman's post-boot
// cleanup, and refusing at once only on an answer that cannot clear on its own
// (docs/reference/podman-readiness.md). Podman on macOS takes the gate's PATIENT ONE-SHOT
// (PR-D24): one attempt, the same minute to answer, no retry. It used to be killed at 10 s and
// called "not started", which is how a running machine that was busy got refused on the Intel
// macOS nightly of 2026-10-03. Apple Container keeps a one-shot probe bounded at 5 s
// (probeAppleContainer); a cold runtime can spend most of that, so it has a progress line too
// (silent when it answers promptly).
func (o *Options) runtimeIsConnectable(rt string) (ok bool, down runtimeDown) {
	if o.usesReadinessGate(rt) || o.usesPatientOneShot(rt) {
		return o.waitForPodman(rt)
	}
	o.withStderrProgress("Checking that "+rt+" is running", func() bool {
		ok, down.reason = o.probeAppleContainer()
		return ok
	})
	down.hint = runtimeStartHint(rt, o.IsMacOS)
	return ok, down
}

// probeAppleContainer is Apple Container's one-shot probe: `container system status`, bounded
// at 5 s, answering "running". Podman never comes here: waitForPodman is its probe on both
// hosts.
func (o *Options) probeAppleContainer() (bool, string) {
	res := o.Exec([]string{"container", "system", "status"}, "", nil, 5*time.Second)
	return res.Ran && !res.Timeout && res.RC == 0 &&
		strings.Contains(strings.ToLower(res.Stdout), "running"), runtimeProbeFailure("container system status", res)
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

// checkConfigChanges is the fresh launch's config-change gate, which both backends call: it
// reads the workspace config, delegates to config.ApproveConfigAndScope with the diff-printing
// prompter, and keeps what it approved for the spawn. Returns true to proceed, false to abort.
//
// THE READ IS HERE, STRICT AND ONCE (docs/design/workspace-widening.md WW-D18,
// config.ReadWorkspaceForGate). It feeds both halves of the approval record: the config part,
// with `brokered` projected out, and the repository scope's entry. A read this gate cannot make
// refuses the launch, naming the file: the non-strict read it replaced returned `{}` for an
// unparseable file, so a config broken after the launch's own read reached the gate as empty.
//
// merged and rt decide whether a brokered loophole's repository scope is in play — the approval
// record's second part (docs/design/boundary-broker.md BB-D30) — by the same predicate the spawn
// applies. The scope is READ HERE, at every fresh launch that starts such a loophole, and only
// here: an attach never reaches this function.
func (o *Options) checkConfigChanges(merged *jsonx.OrderedMap, rt string) bool {
	if !o.runConfigGate(merged, rt, nil) {
		return false
	}
	// The gate passed, so the ceiling lines the migration offer's conversion wrote stand.
	o.unapprovedCeiling = nil
	return true
}

// gateHooks is what `yolo config apply` and the attach-time gate add to the launch's gate
// (docs/design/live-config-apply.md §3.2, §3.3), and ALL they add: the read, the scope, the
// prompter, the record and the approved scope stay checkConfigChanges' own, because there is
// one gate (LA-P1).
type gateHooks struct {
	// plan is handed the gate's read, the config it approves, and its scope check, nil when no
	// broker is in play, once the read and the scope are made and before anything is asked or
	// recorded. The lines it returns, in markup, print after the gate's diff and directly above
	// its question.
	plan func(read *config.WorkspaceRead, scope *config.ScopeCheck) []string
	// attach is an attach's gate: an N does not stop the session, so the decline says that
	// the session starts and names `yolo config apply` instead of "Exiting." (LA-D14).
	attach bool
	// apply is `yolo config apply`'s own gate (not an attach's). Two things differ from a launch:
	//
	//   - A HUMAN IS THERE WHEN STDIN IS A TERMINAL, whatever stderr is. `yolo host-run -- yolo
	//     config apply` gives the command the host terminal as stdin and pipes for stdout and
	//     stderr, which host-run copies to that terminal as they arrive, so the diff and the
	//     question reach the person who answers them. A launch keeps asking for both, because its
	//     stderr is the launch stream and a redirected one is a log nobody is reading.
	//   - Its refusal names the command's own next step, not the launch's flag
	//     (config.ChangedNonInteractiveError.Apply).
	apply bool
}

// writableRequestResolver is the loophole resolver the gate's workspace-mount check fences with
// (config.CheckWorkspaceMounts), the one validation uses.
var writableRequestResolver = loopholeResolver

// readWorkspaceForGateSeam is the gate's one read (config.ReadWorkspaceForGate), a variable so a
// test can change the file between a caller's own load and this read.
var readWorkspaceForGateSeam = config.ReadWorkspaceForGate

// runConfigGate is checkConfigChanges' body, with h (nil for a fresh launch) for the apply
// command and the attach (gateHooks).
func (o *Options) runConfigGate(merged *jsonx.OrderedMap, rt string, h *gateHooks) bool {
	read, err := readWorkspaceForGateSeam(o.Workspace)
	if err != nil {
		// The file names come from the workspace, whose includes the agent names.
		out := o.pr(o.Stderr)
		out.printf("[bold red]Cannot read this workspace's config for the config-change gate: %s[/bold red]",
			richtext.Escape(termsafe.Visible(err.Error())))
		out.print("[dim]Fix the file it names, then launch again; `yolo check` lists every problem " +
			"in the config. Nothing was recorded.[/dim]")
		return false
	}
	// The mounts this read's workspace config declares (RC-D24's writable requests among them),
	// each resolved once, judged here on exactly what will be approved and bound (the launch
	// validated an earlier read), before anything is asked. The apply plan below reads them as
	// what a y would bind.
	o.workspaceMounts = nil
	requests := read.Mounts()
	if refusals := config.CheckWorkspaceMounts(o.Workspace, requests, writableRequestResolver()); len(refusals) > 0 {
		out := o.pr(o.Stderr)
		out.print("[bold red]The workspace config declares a mount the config-change gate refuses:[/bold red]")
		for _, r := range refusals {
			out.printf("  [red]%s[/red]", richtext.Escape(termsafe.VisibleLines(r)))
		}
		out.print("[dim]Fix the entry as each line says, then launch again; `yolo check` lists the same " +
			"problems. Nothing was recorded.[/dim]")
		return false
	}
	o.workspaceMounts = requests
	pr := &changePrompter{o: o}
	scope := o.brokeredScopeCheck(rt, merged, read)
	if h != nil {
		pr.attach = h.attach
		if h.plan != nil {
			pr.preQuestion = h.plan(read, scope)
		}
	}
	interactive := o.IsTTYStdin() && (o.IsTTYStderr() || (h != nil && h.apply))
	ok, approved, approvedMounts, err := config.ApproveWorkspaceRead(o.Workspace, read, scope,
		interactive, o.AcceptConfigChanges, pr)
	o.workspaceMounts = nil
	if ok && err == nil {
		cells, policyErr := freezeCallBrokerCells(merged, read.Config, merged)
		if policyErr != nil {
			o.pr(o.Stderr).printf("[bold red]%s[/bold red]", richtext.Escape(policyErr.Error()))
			return false
		}
		o.callBrokerCells = cells
		o.approvedHTTPBindings = scope.HTTPBindings()
		o.recordApprovedScopes(approved, scope.ApprovedGrants(), scope.ReadWithoutScope())
		o.recordApprovedLoopholeGrants(scope.ApprovedLoopholeGrants())
		o.workspaceMounts = approvedMounts
	}
	if err != nil {
		// The OQ-D2 refusal gets rendered rather than dumped: same diff, same
		// colours as the interactive prompt, so the two paths show the reader the
		// same change and differ only in how they end. Everything else is a
		// snapshot IO failure — surface it and abort, so the launch never proceeds
		// on an unwritten approval record.
		var changed *config.ChangedNonInteractiveError
		if errors.As(err, &changed) {
			changed.Apply = h != nil && h.apply
			o.printChangeRefusal(changed)
			return false
		}
		o.pr(o.Stderr).printf("[bold red]%s[/bold red]", err.Error())
		return false
	}
	return ok
}

// printChangeRefusal renders the non-interactive refusal: headline, the diff in
// the prompt's own colours, then the advice that names the flag.
func (o *Options) printChangeRefusal(e *config.ChangedNonInteractiveError) {
	out := o.pr(o.Stderr)
	out.printf("\n[bold red]⚠  %s[/bold red]\n", e.Headline())
	printWritableMounts(out, e.WritableMounts, e.MountSources)
	printScopeBlock(out, e.ScopeBlock)
	printScopeCounts(out, e.ScopeCounts)
	printScopeBlock(out, e.GrantBlock)
	printScopeCounts(out, e.GrantCounts)
	printConfigDiff(out, e.DiffLines)
	out.print("")
	// The advice names files, one of them the git config the scope was read from, which can
	// sit in a directory the agent named; it carries no markup of its own.
	out.print(richtext.Escape(e.Advice()))
}

// printWritableMounts renders the writable host mounts a changed workspace config holds
// (config.ChangeReport.WritableMounts, RC-D24) in bold red, FIRST, above the scope block and the
// diff: approving the diff approves them, and a long diff must not carry one past the reader.
// Each line carries config.WritableMountMarker in its words, so it stays loud with color off.
// The read-only mounts follow, plain (config.MountSourceBlock): each at the destination its source
// resolves to, which the approval records (oq-cx-symlink in docs/reference/context-mounts.md).
func printWritableMounts(out printer, lines, readOnly []string) {
	block := config.WritableMountBlock(lines)
	for _, line := range block {
		out.printf("[bold red]%s[/bold red]", richtext.Escape(line))
	}
	ro := config.MountSourceBlock(readOnly)
	for _, line := range ro {
		out.print(richtext.Escape(line))
	}
	if len(block)+len(ro) > 0 {
		out.print("")
	}
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

// printScopeCounts prints the per-source count lines (WW-D20), which sit directly above the
// question and in the refusal: a long config diff can scroll the block out of view, so the size
// of the change stays where the answer is given.
func printScopeCounts(out printer, counts []string) {
	for _, c := range counts {
		out.printf("[bold]%s[/bold]", richtext.Escape(c))
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
//
// UNDER THE SEAL the assembled copy is an empty object (assembledConfigFor, seal.go): the
// merged config carries the user's inline env_sources, and a sealed build's workspace is the
// one directory its jail writes. The baseline stays, being the workspace's own config.
//
// It returns the digest of the baseline it wrote, "" when it wrote none. The macos-user arm
// hands it to its session (config.BootBaselineDigestEnv says why that backend needs it); the
// container arm ignores it, since its baseline cannot be replaced while its jail runs.
func (o *Options) writeLaunchConfigArtifacts(cfg *jsonx.OrderedMap) (baselineDigest string) {
	out := o.pr(o.Stderr)
	assembled := o.assembledConfigFor(cfg)
	if err := config.WriteAssembledConfig(o.Workspace, assembled); err != nil {
		out.printf("[dim]Warning: could not write the assembled config for the jail: %s[/dim]", err.Error())
	} else if err := config.WriteAssembledOrigins(o.Workspace, assembled, o.assembledSourcesFor()); err != nil {
		// Beside it, which host file set each key, so a refusal in the jail names that file.
		out.printf("[dim]Warning: could not record where the jail's config keys were set: %s[/dim]", err.Error())
	}
	if wsCfg, wsErr := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {}); wsErr == nil {
		d, err := config.WriteWorkspaceBootBaseline(o.Workspace, wsCfg)
		if err != nil {
			out.printf("[dim]Warning: could not write config drift baseline: %s[/dim]", err.Error())
		}
		return d
	}
	return ""
}

// printConfigDiff renders a unified config diff in the launcher's colours. Shared
// by the interactive prompt and the non-interactive refusal so a reader who cannot
// be prompted sees the change in exactly the form the human at a terminal would.
func printConfigDiff(out printer, diffLines []string) {
	marked := writableDiffMarks(diffLines)
	for i, line := range diffLines {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			out.printf("[dim]%s[/dim]", line)
		case marked[i]:
			// RC-D24: the line that makes a mount writable is red and marked, not green.
			out.printf("[bold red]%s  ← %s[/bold red]", line, config.WritableMountMarker)
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

// writableDiffLine reports whether an added diff line is a mount's `"write": true`, the line
// that makes it writable (RC-D24).
func writableDiffLine(line string) bool {
	return strings.Contains(strings.Join(strings.Fields(line), ""), `"write":true`)
}

// writableDiffMarks says, per diff line, whether it is an ADDED line that makes or moves a
// writable mount: a `"write": true`, an entry of the approval record's resolved writable paths
// (which carry config.WritableMountMarker), or the `"host"` of an element whose current side
// holds `"write": true` further down, so a host changed alone is marked too. The snapshot sorts
// keys, so `write` always follows `host` within one element.
func writableDiffMarks(diffLines []string) []bool {
	marks := make([]bool, len(diffLines))
	for i, line := range diffLines {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			continue
		}
		body := strings.TrimSpace(line[1:])
		switch {
		case writableDiffLine(line), strings.Contains(line, config.WritableMountMarker):
			marks[i] = true
		case strings.HasPrefix(body, `"host":`):
			marks[i] = elementIsWritable(diffLines[i+1:])
		}
	}
	return marks
}

// elementIsWritable scans the current side of the rest of one element (context and added lines,
// up to its closing brace or the hunk's end) for `"write": true`.
func elementIsWritable(rest []string) bool {
	for _, l := range rest {
		if strings.HasPrefix(l, "@@") || strings.HasPrefix(l, "-") || l == "" {
			if strings.HasPrefix(l, "-") {
				continue
			}
			return false
		}
		if writableDiffLine(l) {
			return true
		}
		if strings.HasPrefix(strings.TrimSpace(l[1:]), "}") {
			return false
		}
	}
	return false
}

// changePrompter renders the config diff and reads the y/N answer.
//
// preQuestion is the apply's outcome block (docs/design/live-config-apply.md LA-P3), printed
// after the diff and the count lines and directly above the question, so the reader answers
// knowing what each change will do; nil for a fresh launch. attach selects the attach's
// decline lines (declineLines).
type changePrompter struct {
	o           *Options
	preQuestion []string
	attach      bool
}

func (p *changePrompter) Prompt(diffLines []string) bool {
	return p.PromptReport(config.ChangeReport{ConfigChanged: true, DiffLines: diffLines})
}

// PromptReport shows the whole change and asks once (BB-D31): the header and the question
// name the repository scope whenever it changed, and only the scope when the config did
// not, and the scope block comes first. One y approves the whole bundle, as ruled.
func (p *changePrompter) PromptReport(r config.ChangeReport) bool {
	out := p.o.pr(p.o.Stderr)
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
	if r.GrantsChanged {
		what := config.ChangedParts(r.ConfigChanged, r.ScopeChanged, true)
		header = what + " changed since last run:"
		question = "Accept these " + strings.ToLower(what) + " changes? [y/N] "
	}
	out.print("\n[bold yellow]⚠  " + header + "[/bold yellow]\n")
	printWritableMounts(out, r.WritableMounts, r.MountSources)
	printScopeBlock(out, r.ScopeBlock)
	printScopeBlock(out, r.GrantBlock)
	printConfigDiff(out, r.DiffLines)
	out.print("")
	printScopeCounts(out, r.ScopeCounts)
	printScopeCounts(out, r.GrantCounts)
	for _, line := range p.preQuestion {
		out.print(line)
	}
	if _, err := p.o.Stderr.Write([]byte(question)); err != nil {
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
	lines := declineLines(r, p.attach)
	out.print("[red]" + richtext.Escape(lines[0]) + "[/red]")
	for _, line := range lines[1:] {
		out.print(richtext.Escape(line))
	}
	return false
}

// ReportAccepted shows what --accept-config-changes approves on a launch with no terminal, before
// it is recorded (WW-D20): the repository scope block and its count lines, for remotes and
// entries alike, so a scripted launch's log says which repositories it let in; and the writable
// host mounts the approved config holds (RC-D24), which the flag approves as a `y` does but
// never unseen.
func (p *changePrompter) ReportAccepted(r config.ChangeReport) {
	out := p.o.pr(p.o.Stderr)
	what := "Repository scope changed since last run"
	mounts := "mounts"
	if len(r.WritableMounts) > 0 {
		mounts = "writable host mounts"
	}
	switch {
	case r.ScopeChanged && len(r.WritableMounts)+len(r.MountSources) > 0:
		what = "Repository scope and a config with " + mounts + " changed since last run"
	case !r.ScopeChanged:
		what = "Workspace config with " + mounts + " changed since last run"
	}
	if r.GrantsChanged {
		what = config.ChangedParts(r.ConfigChanged, r.ScopeChanged, true) + " changed since last run"
	}
	out.print("\n[bold yellow]⚠  " + what + "; " + config.AcceptConfigChangesFlag +
		" records it as approved:[/bold yellow]\n")
	printWritableMounts(out, r.WritableMounts, r.MountSources)
	printScopeBlock(out, r.ScopeBlock)
	printScopeCounts(out, r.ScopeCounts)
	printScopeBlock(out, r.GrantBlock)
	printScopeCounts(out, r.GrantCounts)
}

// declineLines is what a `N` at the config-change prompt prints: that nothing was recorded, then
// the next step for each part that changed (docs/reference/happy-path-principle.md rule 1). A
// changed config names the files it was read from, and that the next launch asks again; a changed
// repository scope names the command that launches this project without the loophole that reads
// it, since that is the one way past the question that answers no to it.
//
// AN ATTACH'S DECLINE does not exit (docs/design/live-config-apply.md LA-D14): the session starts
// in the jail as it is, so its lines say that instead of "Exiting.", and name `yolo config apply`
// as the way to approve the change later without restarting anything.
func declineLines(r config.ChangeReport, attach bool) []string {
	head := "Config changes rejected; nothing was recorded."
	switch {
	case r.ScopeChanged && r.ConfigChanged:
		head = "Workspace config and repository scope changes rejected; nothing was recorded."
	case r.ScopeChanged:
		head = "Repository scope changes rejected; nothing was recorded."
	}
	if r.GrantsChanged {
		head = config.ChangedParts(r.ConfigChanged, r.ScopeChanged, true) + " changes rejected; nothing was recorded."
	}
	if attach {
		return []string{head + " This session starts in the jail as it is.",
			"To approve the changes later, run `yolo config apply` on the host, in this workspace: " +
				"it asks this question again and applies what the running jail can take."}
	}
	lines := []string{head + " Exiting."}
	if r.ConfigChanged && len(r.ConfigFiles) > 0 {
		lines = append(lines, "The workspace config change is in "+strings.Join(r.ConfigFiles, " and ")+
			": undo it there, or answer y at the next launch, which asks again.")
	}
	// A changed entry row names its file, beside the disable step below: it is the agent-written
	// half of the scope, and editing it is the other way past the question.
	for _, ed := range r.EntryEdits {
		files := make([]string, len(ed.Files))
		for i, f := range ed.Files {
			files[i] = termsafe.Visible(f)
		}
		lines = append(lines, "The `"+ed.Key+"` change is in "+strings.Join(files, " and ")+
			": undo it there, or answer y at the next launch, which asks again.")
	}
	for i, l := range r.ScopeLabels {
		lines = append(lines, config.WithoutBrokerStep(l, config.ScopePackAt(r.ScopePacks, i))+" Otherwise the next launch asks about "+
			"its repositories again.")
	}
	if r.GrantsChanged {
		// A grant from the user's own ceiling has no file in this workspace to undo: it is the
		// user's config, and the next launch asks about it again until a y records it.
		lines = append(lines, "No loophole grant changed: this project holds only what was approved "+
			"before, and the next launch asks about the grants again.")
	}
	return lines
}
