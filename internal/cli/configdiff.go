package cli

// configdiff.go implements `yolo config diff` and `yolo config reset` — the
// inspect-and-undo half of the capture overlay.
//
// `mode: capture` is only defensible if divergence is visible AND reversible: a
// captured edit outranks every layer but `computed` and `managed` forever — configls.go's
// header states the fold and what naming the host layer alone got wrong — so without
// these two commands the only cure is knowing to delete a file in
// <workspace>/.yolo/prism/ by hand (docs/reference/composed-file-permissions.md §5).
//
// `diff` carries a SECOND kind of divergence for the same reason: a pack's
// `config-overlay` contributions to a surface another pack owns (ruling R3,
// docs/reference/pack-system.md §7). Same shape of question — a key in the
// file that the file itself cannot account for — so it reads out of the same command.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// surfaceArgs parses the shared `<agent[/surface]>` argument shape used by diff,
// reset, and capture. Returns rc=-1 when parsing succeeded.
func surfaceArgs(cmd string, args []string, out, errw io.Writer) (agent, surface string, force bool, rc int) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case isHelpToken(a):
			io.WriteString(out, configUsage+"\n")
			return "", "", false, 0
		case a == "--surface" || strings.HasPrefix(a, "--surface="):
			fmt.Fprintf(errw, "yolo config %s: --surface was removed; use the canonical positional identity <agent>/<surface> (for example, %s/settings)\n", cmd, firstNonFlag(args))
			return "", "", false, 2
		case a == "--force":
			// The escape hatch for the host-side write guard (below). Only meaningful
			// when the surfaces are NOT local (host-side or another workspace's jail);
			// harmless otherwise.
			force = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo config %s: unknown flag %q\n\n%s\n", cmd, a, configUsage)
			return "", "", false, 2
		default:
			if agent != "" {
				fmt.Fprintf(errw, "yolo config %s: unexpected argument %q (agent already %q)\n", cmd, a, agent)
				return "", "", false, 2
			}
			agent = a
		}
	}
	if agent == "" {
		fmt.Fprintf(errw, "yolo config %s: needs an agent (e.g. 'yolo config %s claude')\n\n%s\n",
			cmd, cmd, configUsage)
		return "", "", false, 2
	}
	agent, surface, rc = parseSurfaceIdentity(cmd, agent, errw)
	if rc != 0 {
		return "", "", false, rc
	}
	return agent, surface, force, -1
}

func firstNonFlag(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "/") {
			return arg
		}
	}
	return "agent"
}

// parseSurfaceIdentity splits the canonical identity printed by `config ls`.
// A bare agent selects all of its surfaces; a single slash selects exactly one.
func parseSurfaceIdentity(cmd, identity string, errw io.Writer) (agent, surface string, rc int) {
	if strings.Count(identity, "/") == 0 {
		return identity, "", 0
	}
	parts := strings.Split(identity, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		fmt.Fprintf(errw, "yolo config %s: malformed surface identity %q; use <agent>/<surface> with exactly one slash and two non-empty names\n", cmd, identity)
		return "", "", 2
	}
	return parts[0], parts[1], 0
}

// refuseHostSideWrite is the Phase-0 data-loss guard. Host-side `config reset`/`capture`
// resolve `~` against the INVOKING human's real home (expandHome → paths.Home()) and
// write it — reset truncates a real dotfile to its (often empty) pure render; capture
// copies real host config into the workspace sidecar tree. Both are destructive on a
// file yolo does not own IN THAT CONTEXT. configTarget.local is true only in the jail that
// owns the resolved workspace; anywhere else (host-side, or a different workspace's
// surfaces) a write is refused unless --force. Returns true when the caller must abort.
//
// IT READS THE RESOLVED TARGET, and that is the only change this design makes to the guard:
// [OQ-CR2](docs/design/config-target-resolution.md#oq-cr2) is explicit that the resolution
// produces a TARGET, NOT A PERMISSION. Nothing here is loosened — in particular a directory
// that resolves no workspace gets the host target, where a write is refused exactly as it
// was.
//
// # `host_management: own` answers the premise for RESET, and that is why reset is exempt
//
// The guard's whole argument is the clause above — a file yolo does not own. Under `own` the
// user has declared that yolo DOES own these files: they are derived output, the host render
// composes them whole, and truncating one to its pure render is not data loss but the
// operation working (docs/design/config-ownership-and-promotion.md §6.1).
//
// It is LOAD-BEARING rather than parity. ComposeStateful's adoption is safe against reset
// ONLY because reset also truncates the surface to its pure render: without that, reset →
// no baseline → adopt would resurrect the very edits the user asked to discard, making reset
// a silent no-op (the engine's own comment says the two halves are one change). So an owned
// host whose reset refused would have an adoption path with nothing to discard against.
//
// CAPTURE IS NOT EXEMPT, and the asymmetry is deliberate rather than an oversight. Its
// premise is the G2 PRIVACY defect — a credential copied out of a real file into the
// workspace sidecar tree, which crosses into a jail and plausibly into git — and while `own`
// does relocate that destination to the state-dir store (§6.2), `yolo config capture`
// host-side buys only VISIBILITY: it folds edits early that the next host apply would fold
// anyway (see configCapture, "for observability, not correctness"). Nothing depends on it the
// way adoption depends on reset, and leaving it refused keeps the new store with exactly two
// writers — the render, and the reset that discards. --force still reaches it.
func refuseHostSideWrite(t configTarget, cmd string, force bool, errw io.Writer) bool {
	if t.local || force {
		return false
	}
	if cmd == "reset" && t.hostOwned() {
		return false
	}
	fmt.Fprintf(errw, "yolo config %s: refusing — these surfaces resolve against a real "+
		"home, not a jail's, so writing them could clobber your own config. This command "+
		"is meant to run inside the jail that owns the workspace. Re-run with --force if you "+
		"really mean to write the host's files.\n", cmd)
	return true
}

// capturedSurfaces returns the (agent, name) pairs that can carry a capture
// overlay for the given agent, honoring an optional surface filter. It works for
// the pseudo-agent "user" too, whose surfaces are host_files slugs rather than
// manifest entries — those are discovered from the sidecar files on disk, since
// the CLI cannot know which entries a past boot staged.
func capturedSurfaces(t configTarget, agent, surface string) []manifest.Surface {
	if agent == "user" {
		return userSidecarSurfaces(t, surface)
	}
	var out []manifest.Surface
	for _, s := range surfaceManifest().ForAgent(agent) {
		if surface != "" && s.Name != surface {
			continue
		}
		// Only CAPTURE surfaces have sidecars; rmw/copy/unrendered have none, so
		// diff and reset have nothing to operate on.
		if surfaceMode(s) != "capture" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// userSidecarSurfaces discovers host_files capture surfaces from the sidecar dir.
// The slug is opaque here (it is a percent-escaped destination path), so the
// surfaces are synthesized from the file names rather than the config — which also
// means `reset user` can clean up after an entry the user has since removed.
func userSidecarSurfaces(t configTarget, surface string) []manifest.Surface {
	dir := t.sidecarDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []manifest.Surface
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "user-") || !strings.HasSuffix(name, ".overlay.json") {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(name, "user-"), ".overlay.json")
		if surface != "" && slug != surface {
			continue
		}
		// Path is filled in from the slug so the diff header names the FILE rather
		// than the escaped slug — the slug is a reversible percent-escape of the
		// destination (config.HostFileEntry.Slug), so this needs no config read and
		// still works for an entry the user has since removed.
		out = append(out, manifest.Surface{
			Agent: "user", Name: slug, Path: "~/" + unslugHostFilePath(slug),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// configDiff implements `yolo config diff <agent[/surface]>`: what the capture
// overlay is contributing, key by key, versus the layers beneath it.
//
// It reports the overlay's own content rather than re-composing, deliberately: the
// overlay IS the divergence, and showing it directly cannot drift from what the
// engine will apply. Each key is annotated with what the layers underneath say, so
// a redundant capture (same value as the host layer — the common case) is
// distinguishable from a real edit.
//
// IT REPORTS THE CAPTURED DIVERGENCE AND NOTHING ELSE
// ([OQ-CR7](docs/design/config-target-resolution.md#oq-cr7), ruled (a) against its own
// leaning). It used to print a per-key config-overlay PROVENANCE block beside the capture;
// that block now lives in `yolo config ls`, where the render is described — see
// configprovenance.go's header for the argument. In short: provenance answers *"which layer
// did this key come from"*, a property of the RENDER, so it would read identically before and
// after the wipe this verb measures against — and it was the second reader that let one
// report describe two homes (§2.3 F1).
//
// The name was never the problem. *"What survives deleting every surface and regenerating
// them"* IS a diff — current state against a regenerated baseline — so the word names this
// exactly; it was only the second block that made the verb look mis-named.
func configDiff(t configTarget, args []string, out, errw io.Writer, color bool) int {
	agent, surface, _, rc := surfaceArgs("diff", args, out, errw)
	if rc >= 0 {
		return rc
	}
	// THE STORE'S OWN STATE FIRST, because an unreadable store cannot be reported as an
	// absence of edits (§4.2, [P3](docs/design/config-target-resolution.md#1-the-verdict-and-the-principles-it-rests-on)).
	// It read as empty until this design: os.ReadDir's error became nil and the verb printed
	// the same confident negative it prints for a store it really did read.
	state, serr := t.storeState()
	if state == storeUnreadable {
		fmt.Fprintf(errw, "yolo config diff: cannot read the capture store %s: %v\n",
			t.sidecarDir(), serr)
		return 1
	}
	pr := richtext.Printer{W: out, Color: color}
	surfaces := capturedSurfaces(t, agent, surface)
	if len(surfaces) == 0 {
		fmt.Fprintf(errw, "yolo config diff: no capture surfaces for agent %q%s\n",
			agent, surfaceSuffix(surface))
		return 1
	}

	found := false
	for _, s := range surfaces {
		overlay := readOverlayValue(t.overlayPath(s.Agent, s.Name))
		if overlayIsEmpty(overlay) {
			continue
		}
		found = true
		pr.Printf("[bold]# %s/%s → %s[/bold]", s.Agent, s.Name, surfacePathOrSidecar(s))
		baseline := readLastRenderKeys(t.lastRenderPath(s.Agent, s.Name), s)
		for _, line := range overlayDiffLines(overlay, baseline) {
			pr.Print(line)
		}
		pr.Printf("")
	}
	if !found {
		// WHICH negative this is. "No captured edits" is a measurement and may only be
		// printed for a store that was actually read; the other two answers name themselves.
		if reason := t.noCaptureReason(state); reason != "" {
			pr.Printf("[dim]%s%s: %s.[/dim]", agent, surfaceSuffix(surface), reason)
			return 0
		}
		pr.Printf("[dim]No captured in-jail edits for %s%s.[/dim]", agent, surfaceSuffix(surface))
		return 0
	}
	pr.Printf("[dim]These values were captured from in-jail edits and outrank every layer but `computed` and `managed`.[/dim]")
	pr.Printf("[dim]Discard them with: yolo config reset %s[/dim]", surfaceIdentity(agent, surface))
	return 0
}

func surfaceIdentity(agent, surface string) string {
	if surface == "" {
		return agent
	}
	return agent + "/" + surface
}

// overlayKeyState is one captured key's relationship to yolo's own last render — the
// comparison `config diff` prints and `config promote` acts on.
//
// IT IS DATA BECAUSE IT HAS TWO CONSUMERS NOW. The comparison used to exist only as
// formatted lines, and promote needs the same answer as a decision: §5.2 step 2 of
// docs/design/config-ownership-and-promotion.md drops every REDUNDANT key before
// classifying anything, and a second implementation of "is this capture identical to what
// yolo wrote?" is precisely the shape that made this comparison wrong for TOML surfaces
// until readLastRenderKeys' codec fix — one reader corrected, another left to drift. So
// the two commands read one function and can only be wrong together.
type overlayKeyState struct {
	// Key is the captured top-level key.
	Key string
	// Value is the captured value as one-line JSON, for DISPLAY ONLY. `config diff` prints
	// it; promote must not, and does not — see promotePlanDoc's header on why a plan names
	// keys and never values.
	Value string
	// Was is the last render's value for the key as one-line JSON, empty when the last
	// render had no such key.
	Was string
	// Redundant reports a capture identical to yolo's own last render — the common case,
	// and the one promote drops rather than declaring a value the layers already produce.
	Redundant bool
	// Deleted reports a null tombstone: the key was deleted in-jail, and the capture is the
	// record of that deletion rather than of a value.
	Deleted bool
}

// overlayKeyStates compares a decoded capture overlay against the last-render baseline,
// key by key, in sorted order. ok=false for a KEYLESS surface (raw/lines), whose overlay
// is the whole file and has no keys to compare — the caller decides what to say about it.
func overlayKeyStates(overlay any, baseline map[string]string) ([]overlayKeyState, bool) {
	m, isObject := overlay.(*jsonx.OrderedMap)
	if !isObject {
		return nil, false
	}
	states := make([]overlayKeyState, 0, m.Len())
	for _, k := range sortedKeys(m) {
		v, _ := m.Get(k)
		got := oneLineJSON(v)
		states = append(states, overlayKeyState{
			Key: k, Value: got, Was: baseline[k],
			Redundant: v != nil && baseline[k] == got,
			Deleted:   v == nil,
		})
	}
	return states, true
}

// overlayDiffLines renders one line per captured key: the key, the captured value,
// and how it compares to the last render (the bytes yolo itself wrote).
func overlayDiffLines(overlay any, baseline map[string]string) []string {
	states, ok := overlayKeyStates(overlay, baseline)
	if !ok {
		// A keyless surface (raw/lines): the whole file is the captured value.
		return []string{"  [magenta]<file>[/magenta]  " + oneLineJSON(overlay)}
	}
	var lines []string
	for _, s := range states {
		switch {
		case s.Deleted:
			lines = append(lines, fmt.Sprintf("  [magenta]%s[/magenta]  [red]deleted in-jail[/red]", s.Key))
		case s.Redundant:
			lines = append(lines, fmt.Sprintf("  [magenta]%s[/magenta]  %s [dim](same as yolo's last render — redundant capture)[/dim]", s.Key, s.Value))
		case s.Was != "":
			lines = append(lines, fmt.Sprintf("  [magenta]%s[/magenta]  %s [dim](was %s)[/dim]", s.Key, s.Value, s.Was))
		default:
			lines = append(lines, fmt.Sprintf("  [magenta]%s[/magenta]  %s [dim](added in-jail)[/dim]", s.Key, s.Value))
		}
	}
	return lines
}

// readLastRenderKeys decodes the last_render sidecar AT THE GIVEN PATH into per-key one-line
// JSON — path-taking for readProvenance's reason, one function over: there are two stores to
// read, and the CHOICE of which belongs to the caller holding the resolved target. A reader
// that resolved its own is how `diff` and `reset` came to describe different stores on an
// owned host (docs/design/config-target-resolution.md §2.3 F3).
//
// so a captured value can be compared against what yolo last wrote. An absent or
// undecodable sidecar yields an empty map (everything reads as "added in-jail").
//
// It decodes through the SURFACE'S OWN CODEC, and that is the whole correctness
// argument. The sidecar holds the exact bytes of the last render, so its format is
// the surface's — TOML for codex/config and mise/config, JSON for the rest. Reading
// every sidecar as JSON made both TOML surfaces fail to decode, yielding an empty
// baseline, so a capture that was byte-for-byte the last render was reported as
// "(added in-jail)" — a fully redundant capture presented as a new in-jail edit.
//
// The two-step re-encode below is the other half. The overlay sidecar is
// agentcfg.marshalOverlay's encoding/json over CODEC-DECODED values, so running the
// baseline through the same transform (JSON codec encode, jsonx decode) lands both
// sides in one value model and makes them comparable by construction. Skipping it
// would trade the TOML bug for a JSON one: an integer reaches the overlay side as a
// jsonx integer literal ("5") and the codec side as float64 ("5.0"), so every
// integer-valued key in a JSON surface would start misreporting instead.
func readLastRenderKeys(lastRenderPath string, s manifest.Surface) map[string]string {
	baseline := map[string]string{}
	if lastRenderPath == "" {
		return baseline
	}
	data, err := os.ReadFile(lastRenderPath)
	if err != nil {
		return baseline
	}
	c, ok := codec.LookupCodec(s.Codec)
	if !ok {
		return baseline
	}
	decoded, derr := c.Decode(data)
	if derr != nil {
		return baseline
	}
	// Only the object codecs have keys; a keyless surface (lines/raw) decodes to a
	// slice or a string and falls back to the whole-file line.
	m, isObject := decoded.(map[string]any)
	if !isObject {
		return baseline
	}
	encoded, eerr := codec.JSON{}.Encode(m)
	if eerr != nil {
		return baseline
	}
	rt, rerr := jsonx.Decode(encoded)
	if rerr != nil {
		return baseline
	}
	om, isMap := rt.(*jsonx.OrderedMap)
	if !isMap {
		return baseline
	}
	for _, k := range om.Keys() {
		v, _ := om.Get(k)
		baseline[k] = oneLineJSON(v)
	}
	return baseline
}

// configReset implements `yolo config reset <agent[/surface]>`: discard the
// capture overlay so the surface returns to what its layers produce.
//
// It removes the overlay sidecar AND the last_render sidecar, then truncates the surface to
// its pure render and WRITES THE BASELINE BACK for what it just wrote. Removing last_render
// first is not incidental — a baseline left pointing at the pre-reset render would have the
// next boot diff the truncated file against it and capture the discard as an edit — and
// neither is writing the new one: see reseedResetBaseline for what deleting it cost.
func configReset(t configTarget, args []string, out, errw io.Writer, color bool) int {
	agent, surface, force, rc := surfaceArgs("reset", args, out, errw)
	if rc >= 0 {
		return rc
	}
	if refuseHostSideWrite(t, "reset", force, errw) {
		return 1
	}
	surfaces := capturedSurfaces(t, agent, surface)
	if len(surfaces) == 0 {
		fmt.Fprintf(errw, "yolo config reset: no capture surfaces for agent %q%s\n",
			agent, surfaceSuffix(surface))
		return 1
	}

	pr := richtext.Printer{W: out, Color: color}
	cleared := 0
	for _, s := range surfaces {
		overlayPath, lastRenderPath := t.overlayPath(s.Agent, s.Name), t.lastRenderPath(s.Agent, s.Name)
		if overlayPath == "" {
			// A target that keeps no capture store has no sidecars to discard, and a bare
			// os.Remove("") would report a confusing ENOENT for a path nobody named.
			continue
		}
		had := overlayKeyCountAt(overlayPath)
		removedAny := false
		for _, p := range []string{overlayPath, lastRenderPath} {
			if err := os.Remove(p); err == nil {
				removedAny = true
			} else if !os.IsNotExist(err) {
				fmt.Fprintf(errw, "yolo config reset: %s: %v\n", filepath.Base(p), err)
				return 1
			}
		}
		if !removedAny {
			continue
		}
		// Ruling 1 / B1: also TRUNCATE the surface to its pure render.
		//
		// This is what makes reset survive adopt-on-first-migration. Deleting the two
		// sidecars is how the discard used to take effect: no baseline meant the next
		// boot re-seeded from scratch. But B1 changed that path to ADOPT the on-disk
		// file (so a first migration stops wiping agent state), and "no baseline" is
		// indistinguishable from "the user asked to discard" — so without this,
		// reset → no baseline → adopt would bring back the very edits the user just
		// discarded, making reset a silent no-op. The two halves are one change.
		//
		// Truncating also makes reset VISIBLE immediately rather than only after the
		// next boot, which is what a user means by "reset".
		baseline, err := truncateSurfaceToPureRender(t, s)
		if err != nil {
			fmt.Fprintf(errw, "yolo config reset: %s/%s: %v\n", s.Agent, s.Name, err)
			return 1
		}
		// AND RE-SEED THE BASELINE THE TRUNCATION JUST MADE TRUE, rather than leaving the
		// next render to infer one (OQ-CO7 D1). See reseedResetBaseline.
		if err := reseedResetBaseline(t, lastRenderPath, baseline); err != nil {
			fmt.Fprintf(errw, "yolo config reset: %s/%s: %v\n", s.Agent, s.Name, err)
			return 1
		}
		cleared++
		if had > 0 {
			pr.Printf("Cleared [cyan]%s/%s[/cyan] — discarded %d captured %s.",
				s.Agent, s.Name, had, plural(had, "key", "keys"))
		} else {
			pr.Printf("Cleared [cyan]%s/%s[/cyan] — no captured edits (baseline re-seeded).", s.Agent, s.Name)
		}
	}
	if cleared == 0 {
		pr.Printf("[dim]Nothing to reset for %s%s.[/dim]", agent, surfaceSuffix(surface))
		return 0
	}
	// WHICH COMMAND RE-RENDERS depends on the notch this reset ran at, and naming the wrong
	// one sends a host-side user to relaunch a jail that has nothing to do with the file they
	// just truncated.
	if t.hostOwned() {
		pr.Printf("[dim]The next `yolo host apply --assert` re-renders these surfaces from " +
			"their layers.[/dim]")
	} else {
		pr.Printf("[dim]The next jail launch re-renders these surfaces from their layers.[/dim]")
	}
	return 0
}

// reseedResetBaseline writes the last_render sidecar for the bytes `reset` has just truncated
// the surface to, or leaves it removed when the truncation wrote nothing.
//
// # Why reset writes a baseline at all, when its whole first half is deleting sidecars
//
// The deletion's STATED purpose is already this — configReset's own output says "baseline
// re-seeded", and the ruling that deletes last_render says it does so because that "makes the
// next boot take the §3.2 first-migration path, which re-seeds a truthful baseline". Reset
// then writes the file itself (ruling 1's truncation), so the truthful baseline is in its
// hands at that moment and deferring it to a boot is what costs something:
//
//   - THE NEXT RENDER MISREADS RESET'S OWN OUTPUT AS THE USER'S FILE. No trusted last_render
//     over a non-empty file is a first migration, and a first migration over bytes is an
//     ADOPTION — so it spent OQ-CO7's one-per-surface adoption archive on a copy of yolo's
//     own render, announced it as "the file as yolo found it", and left the next genuine
//     adoption with no net at all. Measured 2026-09-12 (§6.3.3, D1).
//   - "Cannot tell an edit from yolo's own output" is the reason captureSurfaceAt declines to
//     act with no baseline. Reset used to create exactly that state and then hand it to the
//     boot render, which has no such guard.
//
// Ruling 1's "the two halves are one change" is untouched by this — the truncation and the
// discard both stay, and this is the same re-seed the deletion was reaching for, performed by
// the half that knows the bytes instead of by the one that has to guess them. What it costs
// is that the next boot is no longer a first migration for this surface, so the §4.7 orphan
// retirement does not fire on it; that sweep is keyed to a surface's FIRST render into a home
// (RetireOnFirstRender), which a reset is not.
//
// An ABSENT surface file leaves the sidecar deleted, and that is not an omission: last_render
// means "the exact bytes yolo wrote last", so a baseline for a file that does not exist is a
// lie the next render would act on — it would read the absent file as an empty one, diff it
// against the baseline, and capture a tombstone for every key.
func reseedResetBaseline(t configTarget, lastRenderPath string, baseline []byte) error {
	if len(baseline) == 0 {
		return nil
	}
	// THE STORE'S OWN MODE, off the same target that decided where the store is — 0600 in a
	// real home, 0644 in a workspace. Read through the Target rather than spelled here: a
	// hand-copied 0644 would put a host user's own config bytes, credentials included, in a
	// world-readable file.
	return os.WriteFile(lastRenderPath, baseline, t.sidecarFileMode())
}

// readOverlayValue decodes the overlay sidecar AT path, or nil. Path-taking for
// readLastRenderKeys' reason: which store is the resolved target's answer, not this
// reader's.
func readOverlayValue(path string) any {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	v, err := jsonx.Decode(data)
	if err != nil {
		return nil
	}
	return v
}

// overlayIsEmpty reports whether an overlay contributes nothing.
func overlayIsEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case *jsonx.OrderedMap:
		return t.Len() == 0
	case []any:
		return len(t) == 0
	case string:
		return t == ""
	default:
		return false
	}
}

// unslugHostFilePath reverses config.HostFileEntry.Slug: "_hh" is a two-hex-digit
// escape and every other byte passed through unchanged. A malformed tail is
// returned as-is rather than dropped — this is display text, so being readable
// matters more than being strict.
func unslugHostFilePath(slug string) string {
	var b strings.Builder
	for i := 0; i < len(slug); i++ {
		if slug[i] != '_' || i+2 >= len(slug) {
			b.WriteByte(slug[i])
			continue
		}
		var v int
		if _, err := fmt.Sscanf(slug[i+1:i+3], "%02x", &v); err != nil {
			b.WriteByte(slug[i])
			continue
		}
		b.WriteByte(byte(v))
		i += 2
	}
	return b.String()
}

// surfacePathOrSidecar names a surface's destination, falling back to the sidecar
// identity for a user surface (whose path lives in config, not the manifest).
func surfacePathOrSidecar(s manifest.Surface) string {
	if s.Path != "" {
		return s.Path
	}
	if built, ok := surfaceManifest().Lookup(s.Agent, s.Name); ok && built.Path != "" {
		return built.Path
	}
	return "(host_files entry " + s.Name + ")"
}

func surfaceSuffix(surface string) string {
	if surface == "" {
		return ""
	}
	return " surface " + surface
}

// oneLineJSON renders a decoded value as compact single-line JSON for a diff line.
func oneLineJSON(v any) string {
	s, err := jsonx.DumpsCompact(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return s
}

// sortedKeys returns an ordered map's keys sorted, for deterministic output.
func sortedKeys(m *jsonx.OrderedMap) []string {
	keys := append([]string(nil), m.Keys()...)
	sort.Strings(keys)
	return keys
}

// truncateSurfaceToPureRender rewrites a surface file with the composition yolo
// would produce from its declared layers alone — no captured overlay. It is the
// second half of `reset` (see configReset): discarding the sidecars is what stops
// the edits being re-applied, and this is what removes them from the file the agent
// reads right now.
//
// It RETURNS the bytes it wrote, in the shape the last_render sidecar takes (the encoded
// surface plus the codec's terminator, no generated header — entrypoint.surfaceText's shape,
// which is what the boot writer persists). That return is what lets reset re-seed the baseline
// for its own write instead of deleting it and leaving the next render to guess; see
// reseedResetBaseline. nil means nothing was written.
//
// An ABSENT surface file is left absent: reset discards edits, it does not create
// files the jail has not written yet.
//
// The computed layer is deliberately not supplied — for the same reason
// `config render` omits it (jail-absolute paths built from $HOME; see
// renderSurface). The next boot recomputes it, so the only cost is that a
// computed-layer key is briefly missing from the file between reset and restart,
// which is strictly better than leaving a discarded edit in place.
//
// # It has two notches, because "the pure render" is the NOTCH's, not the surface's
//
// In a jail it is the jail's: ${workspace} resolves to the container workspace, and a
// surface with a host layer re-reads its own file for those bytes. Host-side under
// `host_management: own` BOTH of those are wrong, and each in a way that would defeat the
// reset it is performing:
//
//   - ${workspace} HAS NO HOST REFERENT. Substituting the container's literal would write
//     "/workspace"-keyed entries into the user's real home — keys their agent never looks at.
//     The host render prunes those branches instead (entrypoint.PruneWorkspaceKeyed), and this
//     calls the SAME function so the truncation lands on the bytes the next apply would write.
//   - THERE IS NO SEPARATE HOST LAYER TO RE-READ. At this notch the surface's own file IS
//     what a `host` layer carries elsewhere, and it reaches the render through ADOPTION, not
//     as a layer (see the `stateful` arm of entrypoint.RenderHostPack). Feeding it back here
//     would preserve exactly the keys the user asked to discard — reset as a no-op, which is
//     the failure the truncation exists to prevent.
func truncateSurfaceToPureRender(t configTarget, s manifest.Surface) ([]byte, error) {
	path, ok := t.surfaceFile(s.Path)
	if !ok {
		// NOT RESOLVABLE AT THIS NOTCH (§4.1's last row). Never a fallback to the process
		// home: host-side that is the invoking human's own dotfile, and truncating it is the
		// class that put a jail's autonomy posture into a real home once.
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if t.hostOwned() {
		return truncateHostSurfaceToPureRender(t, s, path)
	}
	// Surface.HasHostLayer, not a table beside this file: the surfaces the boot render
	// hands host bytes to are the ones whose HostSource is set, and re-rendering WITHOUT
	// the host layer for a surface that has one would write yolo's defaults over the
	// user's own keys. This was the fourth reader of the retired surfaceHasHostLayer map
	// (docs/design/host-render-target.md §3.4).
	var hostBytes []byte
	if s.HasHostLayer() {
		hostBytes, _ = os.ReadFile(path)
	}
	// The jail notch's own Target does the ${workspace} substitution and the fold — the
	// same render.Target.Compose the boot render calls, which is what makes "the pure
	// render" one definition rather than this file's opinion of one.
	sub, res, err := t.composeTarget().Compose(s, render.Layers{HostBytes: hostBytes})
	if err != nil {
		return nil, err
	}
	text := pureRenderText(sub, res.Encoded)
	// Truncate in place: the file may be a bind-mount target whose inode matters.
	if err := os.WriteFile(path, text, 0o644); err != nil {
		return nil, err
	}
	return text, nil
}

// pureRenderText is the file body for a composed surface at this notch: render.SurfaceText,
// as []byte for the writers below. It RESTATED entrypoint.surfaceText's rule until the two
// render paths were collapsed — the docstring said so, which is what made it findable — and
// now calls the one definition, so "reset's bytes, reset's baseline, and the boot render's
// bytes are the same bytes" is a fact rather than three blocks agreeing by hand.
//
// Deliberately the BODY and not render.FileText: what this returns is written to the file AND
// returned to reseed the §5 baseline (see the caller), and the baseline must hold what the
// engine produced, without the generated header. A toml surface therefore comes back from a
// reset without its banner and regains it on the next boot render, which the capture diff
// cannot see either way — it runs on decoded values.
func pureRenderText(s manifest.Surface, encoded []byte) []byte {
	return []byte(render.SurfaceText(s, encoded))
}

// truncateHostSurfaceToPureRender is truncateSurfaceToPureRender at the host notch under
// `host_management: own`: compose from the DECLARED layers alone — no ${workspace} referent,
// no host layer, no overlay — and write that. See its caller for why each of those three is
// a deliberate difference rather than an omission.
//
// # A FOURTH difference, and the one that is not about layers: the AUTONOMY POSTURE
//
// The surface handed in came from surfaceManifest(), which folds each pack's AUTONOMOUS
// posture because its callers are reporting commands (see hostSurfaceManifest for the whole
// argument). Writing that composition into a real home puts the jail's permission bypass into
// the user's own config — the 2026-08-01 leak. So the surface is RE-RESOLVED here at the host
// notch, by its own (agent, name), through the manifest built with the host Target's posture.
//
// Re-resolving rather than taking a second parameter keeps the choice at the one place that
// writes: a caller that forgot to pass the host surface would be a silent leak, while a
// surface this lookup cannot find falls back to the one it was given — a pseudo-agent
// "user" host_files slug, which declares no autonomy posture and so cannot carry the keys
// this guards against.
func truncateHostSurfaceToPureRender(t configTarget, s manifest.Surface, path string) ([]byte, error) {
	if hs, ok := hostSurfaceManifest().Lookup(s.Agent, s.Name); ok {
		s = hs
	}
	// PRUNE, then compose at the host Target — which substitutes nothing, because a host
	// notch has no ${workspace} referent to substitute against (render.Target.Prepare).
	// The two steps are the same pair entrypoint.RenderHostPack performs, in the same
	// order, which is the whole reason PruneWorkspaceKeyed is exported.
	sub, _ := entrypoint.PruneWorkspaceKeyed(s)
	sub, res, err := t.store.Compose(sub, render.Layers{})
	if err != nil {
		return nil, err
	}
	text := pureRenderText(sub, res.Encoded)
	// 0644 and truncate-in-place, as the jail twin does: this is the agent's own file, not a
	// sidecar, and its inode may be one something else is holding.
	if err := os.WriteFile(path, text, 0o644); err != nil {
		return nil, err
	}
	return text, nil
}

// configCapture folds the current on-disk edits into the overlay sidecar NOW, instead
// of waiting for the next boot (E3).
//
// It exists for observability, not correctness. Nothing is lost without it: every
// composed surface lives under a host-backed bind, so an edit and its baseline both
// survive `--rm`, and the next boot captures normally. What lags is VISIBILITY — until
// that boot, `yolo config diff` cannot show an edit made this session, so a user
// checking their own divergence sees a stale answer with no indication it is stale.
//
// It performs exactly the capture half of a boot render: diff the file against the
// last_render baseline, accumulate into the overlay, persist. It deliberately does NOT
// re-render the surface, because re-rendering needs the computed layer, which is built
// from jail paths (see renderSurface) — so a host-side re-render would write host paths
// into the file. Capture needs none of that: it only compares what is there.
func configCapture(t configTarget, args []string, out, errw io.Writer, color bool) int {
	agent, surface, force, rc := surfaceArgs("capture", args, out, errw)
	if rc >= 0 {
		return rc
	}
	if refuseHostSideWrite(t, "capture", force, errw) {
		return 1
	}
	surfaces := capturedSurfaces(t, agent, surface)
	if len(surfaces) == 0 {
		fmt.Fprintf(errw, "yolo config capture: no capture surfaces for agent %q%s\n",
			agent, surfaceSuffix(surface))
		return 1
	}
	pr := richtext.Printer{W: out, Color: color}
	captured := 0
	for _, s := range surfaces {
		n, err := captureSurface(t, s)
		if err != nil {
			fmt.Fprintf(errw, "yolo config capture: %s/%s: %v\n", s.Agent, s.Name, err)
			return 1
		}
		if n < 0 {
			// No baseline yet: the surface has never been rendered in this workspace,
			// so there is nothing to diff against. Skipping is right, but say so —
			// silence would read as "captured, nothing to do".
			pr.Printf("[dim]%s/%s: never rendered here — nothing to capture[/dim]", s.Agent, s.Name)
			continue
		}
		captured++
		pr.Printf("Captured [cyan]%s/%s[/cyan] — %d %s now recorded.",
			s.Agent, s.Name, n, plural(n, "key", "keys"))
	}
	if captured > 0 {
		pr.Printf("[dim]`yolo config diff` now reflects the current files.[/dim]")
	}
	return 0
}

// captureLocation is where ONE capture reads and writes: the surface file itself
// plus its two sidecars, as absolute paths.
//
// It exists because capture has two callers that resolve those three paths
// differently, and only differently. `yolo config capture` runs inside the jail
// that owns the workspace, so `~` is the jail's home and the sidecar dir is the
// cwd's — the local case. Capture-on-terminate runs on the HOST after the
// container is gone, where `~` is the invoking human's real home and reading it
// would be the G2 privacy defect refuseHostSideWrite exists to stop; it resolves
// the same three files against the workspace's host-side backing dirs instead.
// Making the paths a parameter is what lets the second caller reuse the engine
// call below rather than grow a second capture implementation that would be free
// to disagree with it.
type captureLocation struct {
	surface    string // the composed file the agent reads and may have edited
	lastRender string // yolo's own last output, the baseline an edit is measured against
	overlay    string // the accumulated captured edits (written)
}

// captureSurface folds one surface's on-disk state into its overlay, returning the
// resulting overlay key count, or -1 when there is no baseline to diff against. Every path
// comes off the RESOLVED TARGET, which is what makes this and the diff that reports it read
// one store.
func captureSurface(t configTarget, s manifest.Surface) (int, error) {
	path, ok := t.surfaceFile(s.Path)
	if !ok {
		return -1, nil // not resolvable at this notch: the same answer as "no baseline"
	}
	return captureSurfaceAt(s, captureLocation{
		surface:    path,
		lastRender: t.lastRenderPath(s.Agent, s.Name),
		overlay:    t.overlayPath(s.Agent, s.Name),
	})
}

// captureSurfaceAt is the capture itself, over explicit paths. Both callers land
// here, so there is exactly one definition of what capturing means.
func captureSurfaceAt(s manifest.Surface, at captureLocation) (int, error) {
	current, err := os.ReadFile(at.surface)
	if err != nil {
		if os.IsNotExist(err) {
			return -1, nil
		}
		return 0, err
	}
	lastRender, err := os.ReadFile(at.lastRender)
	if err != nil {
		return -1, nil // no baseline: cannot tell an edit from yolo's own output
	}
	overlayJSON, _ := os.ReadFile(at.overlay)

	// Reuse the RENDERER's capture path rather than reimplementing the diff: a second
	// implementation would be free to disagree with the boot render, which is the one
	// thing this must not do. Same entry the boot render calls, at the jail notch's
	// Target — no layers, because capture only compares what is on disk (see above).
	_, out, err := localTarget().ComposeStateful(s, render.Layers{}, render.State{
		CurrentBytes:      current,
		LastRenderPresent: true,
		LastRenderBytes:   lastRender,
		OverlayJSON:       overlayJSON,
	})
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(at.overlay, append(out.OverlayJSON, '\n'), 0o644); err != nil {
		return 0, err
	}
	return overlayKeyCountAt(at.overlay), nil
}
