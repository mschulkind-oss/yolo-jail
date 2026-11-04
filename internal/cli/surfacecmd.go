package cli

import (
	"fmt"
	"io"
	"os"
)

// surfaceUsage is the existing composition help, transferred to the surface namespace.
const surfaceUsage = `Usage: yolo surface <subcommand>

Inspect the generated-config composition pipeline (the layered regeneration of
agent settings/MCP/LSP/mise config). See 'yolo config-ref' and
docs/reference/pack-system.md.

Subcommands:
  ls [--all]               List every composed surface — path, codec, mode,
                           contributing layers, and whether captured in-jail
                           edits are outranking every layer but computed
                           and managed. Then, per key, which pack contributed it
                           via config-overlay and whether that contribution won,
                           and per array which packs appended entries to it via
                           config-list and whether a higher layer replaces it.
  render <agent[/surface]> [flags]
                           Run the composition pipeline and print what it would
                           write, for every surface of <agent> (no writes). The
                           'host' layer is the copy a LAUNCH stages under /ctx —
                           the bytes the boot render composes — so a preview run
                           outside the jail that staged it reports that layer as
                           unavailable rather than substituting your own file,
                           and a copy the launch labelled yolo's own render is a
                           baseline rather than a layer. At the HOST notch it
                           prints exactly the file 'yolo host apply --assert'
                           writes into your real home, from your configured
                           packs, and --explain the per-key record it keeps.
  diff <agent[/surface]> [flags]
                           Show the captured in-jail edits (the capture overlay)
                           for <agent>, key by key, versus yolo's last render.
                           That is what survives deleting these surfaces and
                           regenerating them, and it is all this verb reports;
                           per-key layer provenance is 'surface ls'.
  reset <agent[/surface]> [flags]
                           Discard those captured edits, so the surface returns
                           to what its layers produce on the next launch. It
                           runs HOST-SIDE for a workspace too — it acts on that
                           jail's own home overlay, never on your real home —
                           and refuses while a jail for that workspace is
                           running, naming the in-jail command, which is
                           available exactly then. At the host under
                           host_management "own" it re-renders the surface as
                           'yolo host apply --assert' would, so your profile,
                           servers and the packs' overlays stay in it.
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
                           jail that made the edits is still running. At the host
                           under host_management "own" it records the edits in
                           your real home as 'yolo host apply --assert' would
                           record them, into the host's capture store, so
                           'diff --setup host-self' shows them before the next apply.
Surface selection:
  Use the canonical identity printed by 'yolo surface ls', such as pi/settings.
  A bare agent name selects all of that agent's surfaces. The removed --surface
  flag is rejected with a migration hint.

render flags:
  --explain          Print, per config key, which layer set it
                     (defaults<host<workspace<config-overlay<config-list
                     <overlay<managed), instead of the rendered file — and,
                     per array a config-list appends to, its entries in
                     order with the pack each came from, or the layer that
                     replaced the assembled array.
  --help, -h         Show this help.

ls flags:
  --all              Include surfaces whose file does not exist (the manifest
                     declares every agent's surfaces; a jail composes only the
                     selected agents').

ls, render, diff, reset and capture also take:
  --setup <name>     Ask about a different notch than the cwd selected: a setup
                     with walls (the workspace you are standing in) or host-self
                     (your real home). The same flag, the same names and the same
                     refusals as 'yolo apply --setup'. A Seatbelt setup is refused
                     by name — its notch is unbuilt here — and a setup with walls
                     outside a workspace is refused rather than silently answered
                     about the host. There is no --workspace flag: cd into the
                     project you meant. (--at is removed.)

reset/capture also take:
  --force            reset and capture WRITE files. At the HOST notch they resolve
                     against your REAL home and could clobber your own config, so
                     they refuse there unless --force. 'own' exempts both: yolo
                     composes those files and keeps their capture store. Under
                     'none' and 'assert' there is no store, so capture refuses
                     even with --force. A host-side reset of a WORKSPACE needs no
                     --force — those files are that jail's, not yours — but it
                     refuses while that jail is running, or while the container
                     runtime cannot be asked whether it is; --force reaches both.

promote flags:
  --keys a,b         Promote only these captured keys (default: all of them).
  --to <dest>        local (default unless promotion_target is set in user config),
                     or pack:<name>. "local" is
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
  yolo surface ls                      # every composed file, and what mode it is in
  yolo surface render claude           # what a launch would write for claude
  yolo surface diff claude/settings    # edits captured for one listed surface
  yolo surface reset claude/settings   # discard that surface's captured edits`

// runSurface is the registry handler for `yolo surface <subcommand>`.
func runSurface(args []string) int {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	return surfaceRunW(rest, os.Stdout, os.Stderr)
}

// surfaceRunW routes composition verbs through their existing target and handler pipeline.
func surfaceRunW(args []string, out, errw io.Writer) int {
	if len(args) == 0 || isHelpToken(args[0]) {
		writeSurfaceUsage(out, colorForWriter(out))
		return 0
	}
	verb := args[0]
	rest, setupName, rc := extractSetupFlag("surface", verb, args[1:], errw)
	if rc != 0 {
		return rc
	}
	t, refusal := resolveConfigTarget(setupName)
	if refusal != "" {
		fmt.Fprintf(errw, "yolo surface %s: %s\n", verb, refusal)
		return 1
	}
	fmt.Fprintln(errw, t.disclosure())
	run, ok := surfaceColorVerbs[verb]
	if !ok {
		fmt.Fprintf(errw, "yolo surface: unknown subcommand %q\n\n%s\n", verb, surfaceUsage)
		return 2
	}
	return run(t, rest, out, errw, colorForWriter(out))
}

// surfaceColorVerbs is the explicit registration of existing color-aware composition handlers.
var surfaceColorVerbs = map[string]func(t configTarget, args []string, out, errw io.Writer, color bool) int{
	"render":  configRender,
	"ls":      configLs,
	"diff":    configDiff,
	"reset":   configReset,
	"capture": configCapture,
	"promote": configPromote,
}
