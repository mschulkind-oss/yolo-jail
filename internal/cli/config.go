// config.go implements `yolo config <subcommand>` — the runnable window into
// the generated-config composition pipeline (docs/reference/pack-system.md,
// "Config surfaces and the compose engine"). Today it provides `yolo config render`, which runs the SAME renderer the
// entrypoint boot render calls — render.Target.Compose, one implementation over a
// notch parameter, rather than the hand-copy that claim used to rest on — and prints
// what it would write, touching no live config. It runs host-side (the edit-before-launch
// loop) and in-jail (the operating agent's "what is my config, and why?" aid),
// and it is the CLI surface that makes the composition pipeline discoverable
// and operable by interrogation — the self-documenting-CLI gap
// (docs/reference/self-documenting-cli.md) this closes for the composed surfaces.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// configUsage is the `yolo config` help, printed on `--help`, `help`, or misuse.
const configUsage = `Usage: yolo config <subcommand>

Edit and inspect yolo's own configuration. Generated config surfaces are
managed separately by 'yolo surface'.

Subcommands:
  set <key> <value> [target] [--dry-run]
                           Set one typed value in a readable config file; may create
                           a missing standard Project/Local TOML target.
  add <key> <value> [target] [--dry-run]
                           Append one typed value to an existing list.
  unset <key> [target] [--dry-run]
                           Remove one key using the existing config removal policy.
  apply [--record-only] [--accept-config-changes]
                           HOST ONLY. Approve this workspace's config changes and
                           apply to its RUNNING jail what it can take without a
                           restart. It never restarts the jail.
  drift                    Show whether the WORKSPACE config on disk differs from
                           the config this jail has (what it started with, plus
                           what 'config apply' applied since).
  dump                     Print the full COMPUTED config as canonical JSON.
  why <key>                Show which file and line set a key, and what applies without it.
  secret <NAME> [--drop] [target]
                           Move an env name into the private set ([env.secrets]),
                           never printing its value; --dotenv <path> moves a file entry.
  unsecret <NAME> [--drop] [target]
                           Move an env name back to the public set ([env]).
  migrate [--dry-run] [--yes]
                           Convert the old-format config files to TOML, once,
                           proving the result says the same; on the host the
                           user config too, in a jail only this workspace's.

Edit targets:
  --user, --project, --local  Select the existing scope file.
  --file <path>              Select one consumed config file.
  --dry-run                  Show the edit without writing.

Flags:
  --help, -h               Show this help.

Examples:
  yolo config set machine.resources.cpus 4 --project
  yolo config add env.unset '"NEW"' --local
  yolo config unset mcp_servers.tavily --project
  yolo config apply
  yolo config drift
  yolo config dump`

const configEditUsage = `Usage:
  yolo config set <key> <value> [target] [--dry-run]
  yolo config add <key> <value> [target] [--dry-run]
  yolo config unset <key> [target] [--dry-run]

set and add require one typed value; unset physically removes an ordinary recognized scalar from the named file, so a lower layer or default may reappear. Table, list, native, and null removals keep their schema-specific forms; unsupported removals refuse without writing. Edits require an existing config file except for the bounded, standard Project/Local TOML target, which may be created when absent.
The default target is Project when it exists, even if User or Local currently wins. User and named-file targets must exist. A missing standard Project/Local TOML target may be created; --dry-run never creates it.

Flags:
  --user, --project, --local
                           Select an existing config target.
  --file PATH              Select an existing config file.
  --dry-run                Print the proposed path and changes without writing.
  --help, -h               Show this help.`

// runConfig dispatches `yolo config <subcommand>`. Registered in dispatch.go.
// Per the dispatch convention (see runBroker), args[0] is the command name
// ("config") itself; the payload is args[1:].
func runConfig(args []string) int {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	return configRunW(rest, os.Stdout, os.Stderr)
}

// configRunW is the testable body: args is everything after `config`.
// Generic set/add/unset edits return through configedit before the composition target path below;
// they select existing readable config files, not generated surface composition targets.
//
// THIS IS THE ONE RESOLUTION POINT for the composition verbs
// (docs/reference/config-target-resolution.md#the-config-target,
// [P2](docs/reference/config-target-resolution.md#principles)). The target is resolved here — after argv is
// parsed, because the selector is an input, and before any composition verb's first read — DISCLOSED, and
// then handed to the verb. Nothing below resolves a second time: two predicates resolved independently is how
// one report came to describe two homes (F1, docs/reference/config-target-resolution.md#the-config-target), and
// a verb free to resolve its own could grow that defect back.
//
// The disclosure is printed for every VERB, and not above the usage text: help has no report
// whose subject there is to disclose, and neither has an unknown subcommand.
func configRunW(args []string, out, errw io.Writer) int {
	if len(args) == 0 || isHelpToken(args[0]) {
		// Bare `yolo config` and `yolo config --help` print help to stdout
		// (exit 0); this is a self-documenting request, not an error.
		writeConfigUsage(out, colorForWriter(out))
		return 0
	}
	verb := args[0]
	switch verb {
	case "ls", "render", "diff", "reset", "promote", "capture":
		fmt.Fprintf(errw, "yolo config %s has moved to yolo surface %s; use `yolo surface %s` instead.\n", verb, verb, verb)
		return 2
	case "set", "add", "unset":
		if helpRequested(args[1:], "--file") {
			writeCLIUsage(out, colorForWriter(out), configEditUsage)
			return 0
		}
		return runConfigEditWithPostWriteCheck(args, out, errw, configEditWorkspace())
	case "secret", "unsecret":
		return runConfigSecret(args, out, errw, configEditWorkspace())
	case "why":
		return configWhy(args[1:], out, errw)
	case "migrate":
		return configMigrateMain(args[1:], out, errw)
	}
	rest, setupName, rc := extractSetupFlag("config", verb, args[1:], errw)
	if rc != 0 {
		return rc
	}
	t, refusal := resolveConfigTarget(setupName)
	if refusal != "" {
		fmt.Fprintf(errw, "yolo config %s: %s\n", verb, refusal)
		return 1
	}
	fmt.Fprintln(errw, t.disclosure())
	if verb == "dump" {
		return configDump(rest, out, errw)
	}
	run, ok := configColorVerbs[verb]
	if !ok {
		fmt.Fprintf(errw, "yolo config: unknown subcommand %q\n\n%s\n", verb, configUsage)
		return 2
	}
	// Most config verbs color their stdout payload based on that destination. Apply writes
	// both launch diagnostics and a named stdout report, so pass color intent through and
	// let its stderr/stdout printers independently check their own TTY and NO_COLOR gates.
	color := verb == "apply" || colorForWriter(out)
	return run(t, rest, out, errw, color)
}

// configColorVerbs is every `yolo config` verb that prints in color, each handed the
// target and color intent from configRunW. `dump` is not here because it prints JSON, which never colors.
//
// Stdout-only verbs receive colorForWriter(out); apply receives an ungated request so the
// launch and payload printers can independently gate stderr and stdout. This table keeps every
// dispatch explicit; TestConfigAndSurfaceVerbTakesTheOneColorDecision pins it.
var configColorVerbs = map[string]func(t configTarget, args []string, out, errw io.Writer, color bool) int{
	// drift compares this workspace against its own boot baseline, so it takes no target.
	"drift": func(_ configTarget, args []string, out, errw io.Writer, color bool) int {
		return configDrift(args, out, errw, color)
	},
	// apply acts on the cwd's workspace and its running jail, as `yolo` does, so it takes no
	// target either (docs/design/live-config-apply.md §3.1).
	"apply": func(_ configTarget, args []string, out, errw io.Writer, color bool) int {
		return run.ConfigApply(args, out, errw, color)
	},
}

// notchlessVerbs are the `yolo config` verbs no notch selects, with the reason each one gives
// for refusing `--setup`.
//
// REFUSED RATHER THAN ACCEPTED AND IGNORED. A flag a verb takes and does nothing with is the
// declaration-parity defect (docs/design/declaration-parity.md): a surface that accepts a
// declaration it does not honor. `--setup host-self` on `promote` would be worse than inert — it
// would resolve a target with no workspace store, so the verb would report nothing to promote
// for a workspace full of captures.
var notchlessVerbs = map[string]string{
	"promote": "promote is host-only and its destination is user scope, which is not a notch. " +
		"It reads the cwd's workspace store by design — lifting a jail's captured keys into a " +
		"pack is the whole verb",
	"drift": "drift compares THIS workspace's config against the baseline its own launch froze, " +
		"so there is no notch to select",
	"dump": "dump prints the effective merged config, which no notch selects",
	"apply": "apply approves this workspace's config and applies it to the workspace's running " +
		"jail from the host, which no notch selects",
}

// extractSetupFlag pulls `--setup <name>` / `--setup=<name>` out of a verb's argv and returns
// the rest unchanged, so the setup reaches the ONE resolution point above while each verb's own
// parser keeps refusing every flag it does not know. The removed `--at` is refused here, naming
// `--setup` (atRemovedFor).
//
// It is `yolo apply`'s token shape, deliberately: `--setup <name>` is the shipped answer to
// "which notch does this verb act on"
// ([P4](docs/reference/config-target-resolution.md#principles) — the SELECTOR rule, not report-tiers.md's own P4),
// and a second spelling for the read verbs would be a second vocabulary for one fact. The
// VALUE is validated once, in resolveConfigTarget. group is the verb's words for a refusal
// ("config" or "surface").
func extractSetupFlag(group, verb string, args []string, errw io.Writer) (rest []string, setupName string, rc int) {
	for i := 0; i < len(args); i++ {
		if fl, ok := readValueFlag(args, i, "--setup"); ok {
			if fl.err != nil {
				fmt.Fprintf(errw, "yolo %s %s: %v (a setup's name; `yolo setup list` names them)\n",
					group, verb, fl.err)
				return nil, "", 2
			}
			setupName, i = fl.value, fl.last
			continue
		}
		if fl, ok := readValueFlag(args, i, atFlag); ok {
			fmt.Fprintf(errw, "yolo %s %s: %v\n", group, verb, atRemovedFor(group+" "+verb, fl.value))
			return nil, "", 2
		}
		rest = append(rest, args[i])
	}
	if setupName != "" {
		if why, notchless := notchlessVerbs[verb]; notchless {
			fmt.Fprintf(errw, "yolo %s %s: --setup does not apply here — %s.\n", group, verb, why)
			return nil, "", 2
		}
	}
	return rest, setupName, 0
}

func writeConfigUsage(out io.Writer, color bool) {
	writeCLIUsage(out, color, configUsage)
}

func writeSurfaceUsage(out io.Writer, color bool) {
	writeCLIUsage(out, color, surfaceUsage)
}

func writeCLIUsage(out io.Writer, color bool, usage string) {
	lines := strings.Split(usage, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "Usage:") {
			lines[i] = "[bold]Usage:[/bold]" + strings.TrimPrefix(line, "Usage:")
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(line, " ") && strings.HasSuffix(trimmed, ":") {
			lines[i] = "[bold]" + line + "[/bold]"
			continue
		}
		start := strings.IndexFunc(line, func(r rune) bool { return r != ' ' && r != '\t' })
		// Entries are indented exactly two spaces; a deeper line is wrapped prose, even
		// when it happens to start with a flag.
		if start != 2 {
			continue
		}
		end := start
		for end < len(line) && line[end] != ' ' && line[end] != '\t' {
			end++
		}
		token := line[start:end]
		trimmedToken := strings.TrimSuffix(token, ",")
		if !configHelpToken(token) && !configHelpToken(trimmedToken) {
			continue
		}
		if trimmedToken != token {
			aliasStart := end
			for aliasStart < len(line) && (line[aliasStart] == ' ' || line[aliasStart] == '\t') {
				aliasStart++
			}
			aliasEnd := aliasStart
			for aliasEnd < len(line) && line[aliasEnd] != ' ' && line[aliasEnd] != '\t' {
				aliasEnd++
			}
			if aliasStart < aliasEnd && configHelpToken(line[aliasStart:aliasEnd]) {
				end = aliasEnd
			}
		}
		lines[i] = line[:start] + "[cyan]" + line[start:end] + "[/cyan]" + line[end:]
	}
	io.WriteString(out, richtext.Render(strings.Join(lines, "\n")+"\n", color))
}

func configHelpToken(token string) bool {
	switch token {
	case "ls", "render", "diff", "reset", "promote", "capture", "set", "add", "unset", "drift", "dump", "apply", "migrate", "why", "secret", "unsecret", "--drop", "--dotenv",
		"--user", "--project", "--local", "--file", "--dry-run",
		"--help", "-h", "--all", "--explain", "--setup", "--force", "--keys", "--to",
		"--plan", "--json", "--accept-promotion", "--record-only", "--accept-config-changes", "--yes":
		return true
	default:
		return false
	}
}

// isHelpToken reports whether tok requests help.
func isHelpToken(tok string) bool {
	return tok == "--help" || tok == "-h" || tok == "help"
}

// colorForWriter is THE color decision for every command in this package, and it
// is made through the one gate, tty.Color: ANSI only when out is a file or a
// transparent writer that exposes its terminal destination, that destination is
// a real terminal, AND NO_COLOR is unset or empty (https://no-color.org). A
// bytes.Buffer or ordinary pipe/redirect yields false, so output stays plain.
//
// Every entry point that writes to os.Stdout asks colorForWriter(os.Stdout) —
// never isTTYStdout(), which answers "is this interactive?" and knows nothing of
// NO_COLOR. TestEveryCommandHonorsNoColor drives each of those entry points.
func colorForWriter(out io.Writer) bool {
	f := tty.FileForTTY(out)
	return tty.Color(nil, true, f != nil && fileIsTerminal(f))
}

// fileIsTerminal is the terminal probe behind colorForWriter. A var so a test can
// stand a terminal in for the pipe it captures a command's stdout through; it is
// NOT the probe interactive decisions use (isTTY), so overriding it changes what
// is colored and nothing else.
var fileIsTerminal = tty.IsTerminalFile

// configRender implements `yolo config render <agent[/surface]> [--explain]`.
func configRender(t configTarget, args []string, out, errw io.Writer, color bool) int {
	var identity string
	var explain bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case isHelpToken(a):
			writeSurfaceUsage(out, color)
			return 0
		case a == "--explain":
			explain = true
		case a == "--surface" || strings.HasPrefix(a, "--surface="):
			fmt.Fprintln(errw, "yolo config render: --surface was removed; use the canonical positional identity <agent>/<surface> (for example, pi/settings)")
			return 2
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo config render: unknown flag %q\n\n%s\n", a, surfaceUsage)
			return 2
		default:
			if identity != "" {
				fmt.Fprintf(errw, "yolo config render: unexpected argument %q (target already %q)\n", a, identity)
				return 2
			}
			identity = a
		}
	}
	if identity == "" {
		fmt.Fprintf(errw, "yolo config render: needs an agent (e.g. 'yolo config render pi')\n\n%s\n", surfaceUsage)
		return 2
	}
	agent, surface, rc := parseSurfaceIdentity("render", identity, errw)
	if rc != 0 {
		return rc
	}
	// THE HOST NOTCH PREVIEWS THE HOST APPLY'S OWN RENDER (configrenderhost.go), never the loop
	// below: that one composes the JAIL's surfaces, at the jail's posture, which is not what
	// `yolo host apply` writes (docs/plans/notch-convergence.md item 23).
	if t.notch == render.KindHost {
		return configRenderHost(agent, surface, explain, out, errw, color)
	}

	m := surfaceManifest()
	surfaces := m.ForAgent(agent)
	if len(surfaces) == 0 {
		known := map[string]bool{}
		var names []string
		for _, s := range m.Surfaces() {
			if !known[s.Agent] {
				known[s.Agent] = true
				names = append(names, s.Agent)
			}
		}
		fmt.Fprintf(errw, "yolo config render: no surfaces for agent %q (known: %s)\n", agent, strings.Join(names, ", "))
		return 1
	}
	if surface != "" {
		found := false
		for _, s := range surfaces {
			if s.Name == surface {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(errw, "yolo config render: no surface %q for agent %q\n", surface, agent)
			return 1
		}
	}

	// THE PACK CONTRIBUTIONS, collected once for every surface below: the config-overlay
	// and config-list contributions the configured packs make, resolved the way `config ls`
	// resolves them (same packs, same notch). Anything that could not be folded is SAID, on
	// stderr beside the refusals, because a preview missing a contributor reads exactly like
	// a complete one.
	contribs := renderContributions(t, errw)

	rc = 0
	for _, s := range surfaces {
		if surface != "" && s.Name != surface {
			continue
		}
		// A3: skip surfaces the JAIL never composes. claude/config (~/.claude.json)
		// is written by writeClaudeJSON's read-modify-write, not by the engine —
		// the manifest marks it unrendered and ls/diff/reset already skip it,
		// but render composed it anyway and printed LIVE AGENT STATE (machineID, the
		// whole mcpServers table, onboarding timestamps, userID) as though it were a
		// preview of what yolo writes. That breaks §6's promise that what render
		// prints is what the jail gets, and it is the one place the promise is a lie
		// rather than a drift.
		// Skip surfaces with no composition to preview: `unrendered` (the agent owns
		// the file outright) and `rmw` (yolo asserts keys into an agent-owned file —
		// there is no layer fold to show).
		if mode := surfaceMode(s); mode == surfaceModeUnrendered || mode == surfaceModeRMW {
			if surface != "" {
				// Explicitly asked for by name: say why nothing came out, rather than
				// printing silence.
				fmt.Fprintf(errw, "yolo config render: %s/%s is not composed by yolo "+
					"(%s is owned by the agent; yolo only asserts individual keys into "+
					"it at boot), so there is nothing to render\n", s.Agent, s.Name, s.Path)
			}
			continue
		}
		if err := renderSurface(t, s, contribs, explain, out, color); err != nil {
			fmt.Fprintf(errw, "yolo config render: %s/%s: %v\n", s.Agent, s.Name, err)
			rc = 1
		}
	}
	return rc
}

// containerWorkspace is the workspace root inside a container-backed jail, and the
// value `yolo config render` previews against. Env.WorkspaceDir() resolves the same
// default in-jail. The macos-user backend uses the real host path instead, so a
// render preview on that backend is approximate for ${workspace}-bearing keys.
const containerWorkspace = "/workspace"

// renderSurface composes one surface and writes either the rendered file or the
// --explain provenance to out.
//
// SCOPE, stated because A7 half-closed and half-documented this: render composes
// defaults < host < config-overlay < config-list < managed — the packs' contributions
// included, because docs/reference/pack-system.md#config-list-visibility has this command
// show an array ASSEMBLED from its inputs plus named contributions, and a preview that
// folded neither could only ever show the owner's own list. It does NOT supply the
// `computed` layer,
// and that is a real limitation rather than an oversight — the computed builders
// bake JAIL-ABSOLUTE, $HOME-derived paths (Env.McpWrappersBin() =
// $HOME/.local/bin/mcp-wrappers, Env.GoBin() = $GOPATH/bin), so composing them
// host-side would emit HOST paths into the preview and be wrong in a more
// misleading way than omitting them. `yolo config ls` names which surfaces carry a
// computed layer (surfaceHasComputedLayer, derived from the packs' derive
// registrations), so the gap is visible rather than silent. Nor does it supply the
// captured `overlay`: that is per-workspace state
// under <workspace>/.yolo/prism/, and `yolo config diff` is the command for it.
func renderSurface(t configTarget, s manifest.Surface, contribs *packoverlay.OverlaySet,
	explain bool, out io.Writer, color bool) error {
	// A11 (${workspace}) and "~" are both the TARGET's now, resolved by the same
	// render.Target.Compose the boot path calls (host-render-target.md §8 step 3). This
	// command previews the JAIL's file, so the compose target carries the container
	// workspace rather than the host checkout path — see localTarget for the other half.
	ct := t.composeTarget()

	// A7: read a `host` layer ONLY for the surfaces that actually get one at boot — and,
	// since [OQ-CR6], out of the bytes the boot render actually composes. See hostLayerFor.
	hostBytes, note, err := hostLayerFor(t, s)
	if err != nil {
		return err
	}

	s, res, err := ct.Compose(s, render.Layers{
		HostBytes: hostBytes,
		Overlays:  contribs.For(s.Agent, s.Name),
		Lists:     contribs.ListsFor(s.Agent, s.Name),
	})
	if err != nil {
		return err
	}

	pr := richtext.Printer{W: out, Color: color}
	header := fmt.Sprintf("[bold]# %s/%s → %s[/bold]", s.Agent, s.Name, s.Path)
	if explain {
		pr.Printf("%s [dim](layer that set each key)[/dim]", header)
		if note != "" {
			pr.Printf("  [dim](%s)[/dim]", note)
		}
		// A7: say what this preview leaves out, on the surfaces where it matters.
		// Silently omitting the computed layer is how the old output managed to
		// attribute mise's computed [tools] table to `host` without anyone noticing.
		if surfaceHasComputedLayer(s) {
			pr.Printf("  [dim](this surface also has a `computed` layer, not shown: it is " +
				"built per-boot from jail paths — see `yolo config ls`)[/dim]")
		}
		// A gated surface is previewed as if its condition held, and says so: the boot writes
		// nothing at all while it does not (manifest.Surface.WhenListed).
		if c := s.WhenListed; c != nil {
			pr.Printf("  [dim](written only while %s %s has an entry matching %q; this "+
				"preview shows it as if one did)[/dim]", c.Surface, c.Path, c.Matches)
		}
		// ProvenanceLines is sorted "key\tlayer"; color the key cyan and the
		// layer by its distinct hue.
		//
		// A key a config-list assembled an array under is MARKED here, because its layer
		// alone would misreport it (pack-system.md#config-list-visibility):
		// `packages  config-overlay:personal` reads as that overlay's array while several
		// packs' entries survive in it. The mark points at the per-entry account printed
		// below.
		underList := listTopKeys(res.Lists)
		for _, line := range res.ProvenanceLines() {
			key, layer, _ := strings.Cut(line, "\t")
			mark := ""
			if underList[key] && layer != agentcfg.LayerConfigList {
				mark = "  [dim](+ config-list entries, below)[/dim]"
			}
			pr.Printf("  [cyan]%s[/cyan]\t%s%s", key, colorLayer(layer), mark)
		}
		writeListExplain(pr, res.Lists)
		return nil
	}
	pr.Print(header)
	if note != "" {
		// A DISCLOSURE, not a decoration: [OQ-CR6] rules that an unreachable host layer is
		// REPORTED rather than silently substituted, so the preview says which of the two
		// compositions it just performed. It rides in the header block, above the file body,
		// where the `#` line already tells a reader this is a report and not a pasteable file.
		pr.Printf("[dim]# %s[/dim]", note)
	}
	fmt.Fprintf(out, "%s\n", res.Encoded)
	return nil
}

// renderContributions collects the configured packs' config-overlay and config-list
// contributions for a preview, reporting on errw whatever could not be folded: a pack that
// could not be resolved, and a malformed contribution (fatal at boot, where A12 applies; a
// preview shows what it can and says what it left out). Never nil — an empty set answers
// "no contribution" for every surface, which composes exactly as before the fold existed.
//
// The SAME packs and notch as `config ls`'s provenance report (overlayContributionRows), so
// the preview and the per-key account cannot disagree about who contributes.
func renderContributions(t configTarget, errw io.Writer) *packoverlay.OverlaySet {
	packs, unresolved := configuredPacksForInspection()
	if len(unresolved) > 0 {
		fmt.Fprintf(errw, "yolo config render: not folded — could not be resolved: %s. Any "+
			"config-overlay or config-list they declare is missing from this preview.\n",
			describeUnresolved(unresolved))
	}
	set := packoverlay.Collect(packs, render.ProfileFor(t.notch).AgentAutonomy,
		overlayGateProfiles(t.notch, packs))
	for _, prob := range set.Problems {
		fmt.Fprintf(errw, "yolo config render: not folded — %s (a launch refuses this)\n", prob)
	}
	return set
}

// listTopKeys is the set of top-level keys the config-list paths of one render live under:
// the keys whose one-word provenance label cannot tell the whole story.
func listTopKeys(lists []agentcfg.ListProvenance) map[string]bool {
	out := map[string]bool{}
	for _, l := range lists {
		if tokens, err := jsonptr.Parse(l.Path); err == nil && len(tokens) > 0 {
			out[tokens[0]] = true
		}
	}
	return out
}

// writeListExplain prints the per-entry account pack-system.md#config-list-visibility
// describes, for every array a config-list touched: the entries in final order with the
// source of each, headed by WHICH of the two things the array is — assembled from the lower
// layers' entries plus named pack contributions, or replaced wholesale by a higher layer, in
// which case the entries below it are what the contributions assembled and not what the file
// holds.
//
// Sources are agentcfg's: `base` (the array the ordinary layers — defaults, host, workspace,
// config-overlay — already held), `config-list:<pack>`, and `captured` (an in-jail edit).
func writeListExplain(pr richtext.Printer, lists []agentcfg.ListProvenance) {
	for _, l := range lists {
		base, captured := 0, 0
		var packs []string
		seen := map[string]bool{}
		for _, e := range l.Entries {
			switch {
			case e.Source == agentcfg.ListSourceBase:
				base++
			case e.Source == agentcfg.ListSourceCaptured:
				captured++
			case !seen[e.Source]:
				seen[e.Source] = true
				packs = append(packs, e.Source)
			}
		}
		var how string
		switch {
		case len(packs) == 0:
			how = fmt.Sprintf("assembled — the lower layers' %d %s; every contributed entry was "+
				"already there", base, plural(base, "entry", "entries"))
		default:
			how = fmt.Sprintf("assembled — the lower layers' %d %s, then entries from %s",
				base, plural(base, "entry", "entries"), strings.Join(packs, ", "))
		}
		if captured > 0 {
			how += fmt.Sprintf(", then %d captured in-jail", captured)
		}
		if l.ReplacedBy != "" {
			pr.Printf("  [bold]list %s[/bold]  [yellow]replaced by %s[/yellow] — the file holds "+
				"that layer's value here, not the list assembled below [dim](%s)[/dim]",
				l.Path, listReplacer(l.ReplacedBy), how)
		} else {
			pr.Printf("  [bold]list %s[/bold]  %s", l.Path, how)
		}
		for _, e := range l.Entries {
			pr.Printf("    %s\t%s", oneLineJSON(e.Value), colorLayer(e.Source))
		}
	}
}

// listReplacer names the layer that replaced an assembled array, in a reader's words.
func listReplacer(layer string) string {
	switch layer {
	case "managed":
		return "the owner's managed layer"
	case "computed":
		return "the computed layer"
	case "overlay":
		return "a captured in-jail edit (yolo config reset discards it)"
	default:
		return layer
	}
}

// hostLayerFor is the `host` layer a PREVIEW composes: the bytes, and the one-line note
// explaining the answer whenever it is not simply "the user's own staged file"
// ([OQ-CR6](docs/reference/config-target-resolution.md#oq-cr6), ruled (a)).
//
// # The staged copy, never the destination
//
// It read `expandHome(s.Path)` — the DESTINATION — until this design, and three different
// files answer to "the host layer": the copy the LAUNCH staged under /ctx (which is what the
// boot render reads, Surface.HostSource), the destination inside the jail (after one boot,
// yolo's own composed output), and the destination on the host (on a managed home, likewise
// yolo's own output). So the preview fed yolo's previous output back in as if it were the
// user's input, while the bytes the boot render uses came from neither (§2.3 F4). This repo
// deleted SkillTarget.HostSource for that exact circularity.
//
// # Where there is no staged copy, the layer is UNAVAILABLE, never substituted
//
// The staged tree belongs to the jail this process is in. Host-side there is no /ctx at all,
// and inside a jail asked about ANOTHER workspace (a nested jail, every integration test)
// the /ctx here is this jail's rather than that workspace's. Both say so and compose without
// the layer, which is the judgement `render` already makes about the `computed` layer:
// decline to invent one and name it, rather than read a different file and call it the
// user's input.
//
// # And a staged copy is not always a layer
//
// A delivery labelled a RENDER is yolo's own output (entrypoint.HostLayerRender): it is the
// baseline the jail reports divergence against and never a layer, or a key yolo wrote comes
// back indistinguishable from the user's ([P6]). The preview composes what the boot render
// composes, so it drops that layer too — and says so, because the difference is otherwise
// invisible in output that looks exactly as correct either way.
func hostLayerFor(t configTarget, s manifest.Surface) ([]byte, string, error) {
	if s.Agent == userSurfaceAgent {
		b, note := userHostLayerFor(t, s)
		return b, note, nil
	}
	if !s.HasHostLayer() {
		return nil, "", nil
	}
	staged, disposition := entrypoint.StagedHostLayer(s)
	if staged == "" {
		return nil, "no `host` layer: this surface declares one and no /ctx source was " +
			"derived for it", nil
	}
	// THE STAGED TREE IS THIS PROCESS'S JAIL'S. A host-side invocation has none, and a jail
	// asked about a DIFFERENT workspace has one that belongs to itself — neither is the
	// target's, and reading either would be the substitution the ruling forbids.
	if t.notch != render.KindJail || !t.local {
		return nil, "the `host` layer is unavailable here: a launch stages the user's own " +
			"copy at " + s.HostSource + ", which exists only inside the jail it launches — " +
			"this preview composes without it", nil
	}
	if disposition == entrypoint.HostLayerRender {
		return nil, "the host's copy of " + s.Path + " is yolo's own render, so it is a " +
			"baseline and not a layer — this surface composes from its packs and its " +
			"capture alone", nil
	}
	pointers := entrypoint.HostImportExclusionsIn(os.Getenv(packload.HostLayerEnvVar), s)
	data, err := os.ReadFile(staged)
	if err != nil {
		if len(pointers) > 0 {
			return nil, "", fmt.Errorf("surface %s/%s: read delivered host layer for active host import exclusions: %w",
				s.Agent, s.Name, err)
		}
		if os.IsNotExist(err) {
			// The overwhelmingly common case: the user has no such file, so the surface
			// composes from its lower layers and nothing is wrong. Silent, as the boot
			// render is.
			return nil, "", nil
		}
		return nil, "the `host` layer could not be read at " + staged + ": " + err.Error(), nil
	}
	if len(pointers) > 0 {
		filtered, filterErr := agentcfg.FilterHostImportExclusions(s.Codec, data, pointers)
		if filterErr != nil {
			return nil, "", fmt.Errorf("surface %s/%s: apply staged host import exclusions: %w",
				s.Agent, s.Name, filterErr)
		}
		data = filtered
	}
	return data, "", nil
}

// colorLayer wraps a provenance layer name in its distinct hue so --explain
// reads like syntax-highlighted provenance: one hue per composition layer
// (docs/plans/cli-visual-polish.md). A compound value ("config-overlay:pack")
// keys its hue on the leading word.
func colorLayer(layer string) string {
	tag := map[string]string{
		"defaults":  "dim",     // lowest precedence — muted
		"host":      "blue",    // the host mirror
		"workspace": "cyan",    // workspace layer
		"overlay":   "magenta", // capture-diff overlay (in-jail edits)
		"managed":   "green",   // yolo-enforced, wins
		// Other packs' contributions, keyed on the word before `:<pack>`.
		"config-overlay": "yellow",
		"config-list":    "yellow",
	}
	word := layer
	if i := strings.IndexAny(word, " :"); i >= 0 {
		word = word[:i]
	}
	if t, ok := tag[word]; ok {
		return "[" + t + "]" + layer + "[/" + t + "]"
	}
	return layer
}

// localTarget is the render.Target the `yolo config` verbs compose at: the notch these
// commands are ABOUT, which is the jail's.
//
// Its two halves answer to different things, and that is the point rather than an
// inconsistency. "~" resolves against the PROCESS home, because that is the home whose
// files these verbs read — in-jail the jail's, host-side the invoking human's, which is
// exactly why the writers among them refuse host-side (refuseHostSideWrite) instead of
// pretending the two are the same. "${workspace}" resolves against the CONTAINER
// workspace, because a preview of a jail file must show the keys the jail will get, not
// keys under the host checkout path the jail never sees.
//
// A function rather than a package var: paths.Home() is read per call, so a test that
// moves $HOME moves the target with it.
//
// ⚠ COMPOSE ONLY. The two paths above are right; this target's SIDECAR paths would not
// be, because the §5 sidecars live under the workspace the invocation is actually in
// (workspaceRoot(), deliberately not /workspace — see its docstring for the nested-jail
// case that rule exists for), and this one carries the container workspace instead. The
// readers that need them resolve that one directly: prismSidecarDir joins workspaceRoot(),
// and configdiff.go builds its own render.Jail from it for the store's file mode. Nothing
// here may reach for Target.SidecarDir and friends.
func localTarget() render.Target {
	return render.Jail(paths.Home(), containerWorkspace, nil)
}

// expandHome expands a leading "~/" in a manifest path to the resolved home dir. One line
// over localTarget, so the CLI and the boot render resolve "~" through the same code
// (render.Target.ExpandHome) instead of through two implementations that agreed by hand.
func expandHome(p string) string {
	return localTarget().ExpandHome(p)
}

// userSurfaceAgent is the fixed pseudo-agent every `host_files` entry lowers to. No real
// agent is named that, which is what keeps a user surface distinct from every pack's.
const userSurfaceAgent = "user"

// userHostLayerFor is hostLayerFor for a `host_files` surface, whose host layer is declared
// rather than pack-derived: an inline `content` literal, or the copy a launch staged at
// /ctx/host-user/<slug>. Same rule either way — the STAGED copy, never the destination,
// which for these is the very file a reset is truncating.
//
// An entry the user has SINCE REMOVED has no layer to resolve and no declaration to
// re-render from; the truncation declines on the missing codec before it reaches here, and
// this says the same thing for a reader who gets here another way.
func userHostLayerFor(t configTarget, s manifest.Surface) ([]byte, string) {
	entry, ok := userHostFileEntry(s.Name)
	if !ok {
		return nil, "no `host_files` entry declares " + s.Path + " any more, so there is no " +
			"`host` layer to compose"
	}
	if entry.HasContent {
		// Declared inline, so it is reachable from either side and needs no /ctx at all.
		return []byte(entry.Content), ""
	}
	if !entry.SourceBearing() {
		return nil, "" // layers-only: defaults and managed are the whole surface
	}
	if t.notch != render.KindJail || !t.local {
		return nil, "the `host` layer is unavailable here: a launch stages this entry's " +
			"source at " + entrypoint.HostUserPath(entry.Slug()) + ", which exists only " +
			"inside the jail it launches — this composes without it"
	}
	data, err := os.ReadFile(entrypoint.HostUserPath(entry.Slug()))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ""
		}
		return nil, "the `host` layer could not be read at " +
			entrypoint.HostUserPath(entry.Slug()) + ": " + err.Error()
	}
	return data, ""
}
