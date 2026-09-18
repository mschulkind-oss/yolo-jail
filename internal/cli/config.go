// config.go implements `yolo config <subcommand>` — the runnable window into
// the generated-config composition pipeline (docs/plans/agent-settings-composition.md
// §6). Today it provides `yolo config render`, which runs the SAME renderer the
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

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// configUsage is the `yolo config` help, printed on `--help`, `help`, or misuse.
const configUsage = `Usage: yolo config <subcommand>

Inspect the generated-config composition pipeline (the layered regeneration of
agent settings/MCP/LSP/mise config). See 'yolo config-ref' and
docs/plans/agent-settings-composition.md.

Subcommands:
  ls [--all]               List every composed surface — path, codec, mode,
                           contributing layers, and whether captured in-jail
                           edits are outranking every layer but computed
                           and managed. Then, per key, which pack contributed it
                           via config-overlay and whether that contribution won.
  render <agent[/surface]> [flags]
                           Run the composition pipeline and print what it would
                           write, for every surface of <agent> (no writes).
  diff <agent[/surface]> [flags]
                           Show the captured in-jail edits (the capture overlay)
                           for <agent>, key by key, versus yolo's last render.
                           That is what survives deleting these surfaces and
                           regenerating them, and it is all this verb reports;
                           per-key layer provenance is 'config ls'.
  reset <agent[/surface]> [flags]
                           Discard those captured edits, so the surface returns
                           to what its layers produce on the next launch.
  promote <agent[/surface]> [flags]
                           Turn captured in-jail edits into DECLARED ones: write them
                           into a pack (the conventional local pack by default, which
                           renders in every jail and on the host) and clear them from
                           the capture overlay, so the value is declared in one place.
                           HOST-SIDE ONLY — the destinations are under the host's
                           ~/.config/yolo-jail/, which no jail may write.
  capture <agent[/surface]> [flags]
                           Record the CURRENT on-disk edits into the overlay now,
                           without waiting. Nothing is lost without it — a jail
                           captures on TERMINATE, and the next boot captures again
                           — so this is for reading 'diff' mid-session, while the
                           jail that made the edits is still running.
  drift                    Show whether the WORKSPACE config (yolo-jail.jsonc) on
                           disk differs from the one this jail was started with —
                           i.e. whether a restart is needed to apply an edit. Exit
                           0 in sync, 3 drifted (prints the diff), 4 no baseline.
                           WORKSPACE ONLY: the user-level config reaches a jail as a
                           generated snapshot taken at launch, so an edit to it is
                           not visible in here and not detectable as drift. In a jail
                           the output says so rather than implying both were checked.
  dump                     Print the full COMPUTED config as canonical JSON (sorted
                           keys) — the effective merged config this jail runs under,
                           the same form the startup config-change diff validates.

Surface selection:
  Use the canonical identity printed by 'yolo config ls', such as pi/settings.
  A bare agent name selects all of that agent's surfaces. The removed --surface
  flag is rejected with a migration hint.

render flags:
  --explain          Print, per config key, which layer set it
                     (defaults<host<workspace<overlay<managed),
                     instead of the rendered file.
  --help, -h         Show this help.

ls flags:
  --all              Include surfaces whose file does not exist (the manifest
                     declares every agent's surfaces; a jail composes only the
                     selected agents').

reset/capture also take:
  --force            reset and capture WRITE files; run host-side (outside the jail
                     that owns the workspace) they resolve against your REAL home and
                     could clobber your own config, so they refuse there unless --force.

promote flags:
  --keys a,b         Promote only these captured keys (default: all of them).
  --to <dest>        local (default unless promotion_target is set in user config),
                     pack:<name>, or host. "local" is
                     ~/.config/yolo-jail/local — no packs entry needed, folds after
                     every other pack, and renders at every notch.
  --plan             Classify and print; write nothing. Add --json for the document.
  --force <keys>     Promote these keys despite the credential-name refusal. Per key,
                     by name — there is no blanket form.
  --accept-promotion Write. Without it promote prints the plan and writes nothing.

A promotion is refused for a key that is redundant (the layers already produce it),
dead (an owning layer overrides it where it sits), environment-bound (it holds a jail
path), credential-named, or outranked by a later pack's config-overlay at the chosen
destination. Each refusal names the key and the reason.

Only a 'capture'-mode surface accumulates in-jail edits; 'readonly', 'once' and
'copy' surfaces write no sidecar, so diff/reset do not apply to them. Use
'user' as the agent for files declared via the host_files config key.

Every verb prints one line on stderr first naming what it is about: the
workspace-or-home, the notch, what selected it, and the capture store it reads.
The cwd selects that target — a directory that resolves a workspace means that
workspace's jail, one that resolves none means your real home — and the line is
not suppressible.

Examples:
  yolo config ls                      # every composed file, and what mode it is in
  yolo config render claude           # what a launch would write for claude
  yolo config diff claude/settings    # edits captured for one listed surface
  yolo config reset claude/settings   # discard that surface's captured edits`

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
//
// THIS IS THE ONE RESOLUTION POINT (docs/design/config-target-resolution.md §3,
// [P2](docs/design/config-target-resolution.md#1-the-verdict-and-the-principles-it-rests-on)).
// The target is resolved here — after argv is parsed, because the selector is an input, and
// before any verb's first read — DISCLOSED, and then handed to the verb. Nothing below
// resolves a second time: two predicates resolved independently is how one report came to
// describe two homes (§2.3 F1), and a verb free to resolve its own could grow that defect
// back.
//
// The disclosure is printed for every VERB, and not above the usage text: help has no report
// whose subject there is to disclose, and neither has an unknown subcommand.
func configRunW(args []string, out, errw io.Writer) int {
	if len(args) == 0 || isHelpToken(args[0]) {
		// Bare `yolo config` and `yolo config --help` print help to stdout
		// (exit 0); this is a self-documenting request, not an error.
		io.WriteString(out, configUsage+"\n")
		return 0
	}
	verb := args[0]
	t, refusal := resolveConfigTarget()
	if refusal != "" {
		fmt.Fprintf(errw, "yolo config %s: %s\n", verb, refusal)
		return 1
	}
	fmt.Fprintln(errw, t.disclosure())
	rest := args[1:]
	switch verb {
	case "render":
		return configRender(rest, out, errw, colorForWriter(out))
	case "ls":
		return configLs(t, rest, out, errw, colorForWriter(out))
	case "diff":
		return configDiff(t, rest, out, errw, colorForWriter(out))
	case "reset":
		return configReset(t, rest, out, errw, colorForWriter(out))
	case "capture":
		return configCapture(t, rest, out, errw, colorForWriter(out))
	case "promote":
		return configPromote(t, rest, out, errw, colorForWriter(out))
	case "drift":
		return configDrift(rest, out, errw, colorForWriter(out))
	case "dump":
		return configDump(rest, out, errw)
	default:
		fmt.Fprintf(errw, "yolo config: unknown subcommand %q\n\n%s\n", verb, configUsage)
		return 2
	}
}

// isHelpToken reports whether tok requests help.
func isHelpToken(tok string) bool {
	return tok == "--help" || tok == "-h" || tok == "help"
}

// colorForWriter reports whether to emit ANSI: only when out is os.Stdout AND a
// real terminal. A bytes.Buffer (tests) or a pipe/redirect yields false, so the
// rendered/explain output stays plain and byte-stable off a TTY.
func colorForWriter(out io.Writer) bool {
	f, ok := out.(*os.File)
	return ok && isTTY(f)
}

// configRender implements `yolo config render <agent[/surface]> [--explain]`.
func configRender(args []string, out, errw io.Writer, color bool) int {
	var identity string
	var explain bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case isHelpToken(a):
			io.WriteString(out, configUsage+"\n")
			return 0
		case a == "--explain":
			explain = true
		case a == "--surface" || strings.HasPrefix(a, "--surface="):
			fmt.Fprintln(errw, "yolo config render: --surface was removed; use the canonical positional identity <agent>/<surface> (for example, pi/settings)")
			return 2
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo config render: unknown flag %q\n\n%s\n", a, configUsage)
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
		fmt.Fprintf(errw, "yolo config render: needs an agent (e.g. 'yolo config render pi')\n\n%s\n", configUsage)
		return 2
	}
	agent, surface, rc := parseSurfaceIdentity("render", identity, errw)
	if rc != 0 {
		return rc
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
		if err := renderSurface(s, explain, out, color); err != nil {
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
// defaults < host < managed. It does NOT supply the `computed` layer,
// and that is a real limitation rather than an oversight — the computed builders
// bake JAIL-ABSOLUTE, $HOME-derived paths (Env.McpWrappersBin() =
// $HOME/.local/bin/mcp-wrappers, Env.GoBin() = $GOPATH/bin), so composing them
// host-side would emit HOST paths into the preview and be wrong in a more
// misleading way than omitting them. `yolo config ls` names which surfaces carry a
// computed layer (surfaceHasComputedLayer, derived from the packs' derive
// registrations), so the gap is visible rather than silent. Nor does it supply the
// captured `overlay`: that is per-workspace state
// under <workspace>/.yolo/prism/, and `yolo config diff` is the command for it.
func renderSurface(s manifest.Surface, explain bool, out io.Writer, color bool) error {
	// A11 (${workspace}) and "~" are both the TARGET's now, resolved by the same
	// render.Target.Compose the boot path calls (host-render-target.md §8 step 3). This
	// command previews the JAIL's file, so localTarget carries the container workspace
	// rather than the host checkout path — see that function for the other half.
	t := localTarget()

	// A7: read a `host` layer ONLY for the surfaces that actually get one at boot.
	//
	// This used to read the surface's own DESTINATION unconditionally. For a
	// yolo-OWNED surface that destination is yolo's previous output, so every key
	// yolo had written came back labelled `host` — mise's computed [tools] table
	// reported as host-provided, and a claude `model` present in no boot layer
	// printed as if composed. Which surfaces the jail hands host bytes to is
	// Surface.HasHostLayer, the same predicate the boot render reads
	// (entrypoint.hostSurfaceBytes), so matching it is what makes render a faithful
	// preview (§6) rather than a re-read of its own output. It was a hand-maintained
	// two-entry map here until docs/design/host-render-target.md §3.4's payoff landed.
	var hostBytes []byte
	if s.HasHostLayer() {
		hostBytes, _ = os.ReadFile(t.SurfacePath(s)) // absent host file => empty layer
	}

	s, res, err := t.Compose(s, render.Layers{HostBytes: hostBytes})
	if err != nil {
		return err
	}

	pr := richtext.Printer{W: out, Color: color}
	header := fmt.Sprintf("[bold]# %s/%s → %s[/bold]", s.Agent, s.Name, s.Path)
	if explain {
		pr.Printf("%s [dim](layer that set each key)[/dim]", header)
		// A7: say what this preview leaves out, on the surfaces where it matters.
		// Silently omitting the computed layer is how the old output managed to
		// attribute mise's computed [tools] table to `host` without anyone noticing.
		if surfaceHasComputedLayer(s) {
			pr.Printf("  [dim](this surface also has a `computed` layer, not shown: it is " +
				"built per-boot from jail paths — see `yolo config ls`)[/dim]")
		}
		// ProvenanceLines is sorted "key\tlayer"; color the key cyan and the
		// layer by its distinct hue.
		for _, line := range res.ProvenanceLines() {
			key, layer, _ := strings.Cut(line, "\t")
			pr.Printf("  [cyan]%s[/cyan]\t%s", key, colorLayer(layer))
		}
		return nil
	}
	pr.Print(header)
	fmt.Fprintf(out, "%s\n", res.Encoded)
	return nil
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
	}
	word := layer
	if i := strings.IndexByte(word, ' '); i >= 0 {
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
