package cli

// configdiff.go implements `yolo config diff` and `yolo config reset` — the
// inspect-and-undo half of the capture overlay.
//
// `mode: capture` is only defensible if divergence is visible AND reversible: a
// captured edit outranks the host layer forever, so without these two commands the
// only cure is knowing to delete a file in <workspace>/.yolo/prism/ by hand
// (docs/design/composed-file-permissions.md §5).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// surfaceArgs parses the shared `<agent> [--surface <name>]` argument shape used
// by diff and reset. Returns rc=-1 when parsing succeeded.
func surfaceArgs(cmd string, args []string, out, errw io.Writer) (agent, surface string, rc int) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case isHelpToken(a):
			io.WriteString(out, configUsage+"\n")
			return "", "", 0
		case a == "--surface":
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo config %s: --surface needs a value\n", cmd)
				return "", "", 2
			}
			i++
			surface = args[i]
		case strings.HasPrefix(a, "--surface="):
			surface = strings.TrimPrefix(a, "--surface=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo config %s: unknown flag %q\n\n%s\n", cmd, a, configUsage)
			return "", "", 2
		default:
			if agent != "" {
				fmt.Fprintf(errw, "yolo config %s: unexpected argument %q (agent already %q)\n", cmd, a, agent)
				return "", "", 2
			}
			agent = a
		}
	}
	if agent == "" {
		fmt.Fprintf(errw, "yolo config %s: needs an agent (e.g. 'yolo config %s claude')\n\n%s\n",
			cmd, cmd, configUsage)
		return "", "", 2
	}
	return agent, surface, -1
}

// capturedSurfaces returns the (agent, name) pairs that can carry a capture
// overlay for the given agent, honoring an optional surface filter. It works for
// the pseudo-agent "user" too, whose surfaces are host_files slugs rather than
// manifest entries — those are discovered from the sidecar files on disk, since
// the CLI cannot know which entries a past boot staged.
func capturedSurfaces(agent, surface string) []manifest.Surface {
	if agent == "user" {
		return userSidecarSurfaces(surface)
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
func userSidecarSurfaces(surface string) []manifest.Surface {
	entries, err := os.ReadDir(prismSidecarDir())
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

// configDiff implements `yolo config diff <agent> [--surface s]`: what the capture
// overlay is contributing, key by key, versus the layers beneath it.
//
// It reports the overlay's own content rather than re-composing, deliberately: the
// overlay IS the divergence, and showing it directly cannot drift from what the
// engine will apply. Each key is annotated with what the layers underneath say, so
// a redundant capture (same value as the host layer — the common case) is
// distinguishable from a real edit.
func configDiff(args []string, out, errw io.Writer, color bool) int {
	agent, surface, rc := surfaceArgs("diff", args, out, errw)
	if rc >= 0 {
		return rc
	}
	surfaces := capturedSurfaces(agent, surface)
	if len(surfaces) == 0 {
		fmt.Fprintf(errw, "yolo config diff: no capture surfaces for agent %q%s\n",
			agent, surfaceSuffix(surface))
		return 1
	}

	pr := richtext.Printer{W: out, Color: color}
	found := false
	for _, s := range surfaces {
		overlay := readOverlayValue(s.Agent, s.Name)
		if overlayIsEmpty(overlay) {
			continue
		}
		found = true
		pr.Printf("[bold]# %s/%s → %s[/bold]", s.Agent, s.Name, surfacePathOrSidecar(s))
		baseline := readLastRenderKeys(s)
		for _, line := range overlayDiffLines(overlay, baseline) {
			pr.Print(line)
		}
		pr.Printf("")
	}
	if !found {
		pr.Printf("[dim]No captured in-jail edits for %s%s.[/dim]", agent, surfaceSuffix(surface))
		return 0
	}
	pr.Printf("[dim]These values were captured from in-jail edits and outrank the host layer.[/dim]")
	pr.Printf("[dim]Discard them with: yolo config reset %s[/dim]", agent)
	return 0
}

// overlayDiffLines renders one line per captured key: the key, the captured value,
// and how it compares to the last render (the bytes yolo itself wrote).
func overlayDiffLines(overlay any, baseline map[string]string) []string {
	m, ok := overlay.(*jsonx.OrderedMap)
	if !ok {
		// A keyless surface (raw/lines): the whole file is the captured value.
		return []string{"  [magenta]<file>[/magenta]  " + oneLineJSON(overlay)}
	}
	var lines []string
	for _, k := range sortedKeys(m) {
		v, _ := m.Get(k)
		got := oneLineJSON(v)
		switch {
		case v == nil:
			lines = append(lines, fmt.Sprintf("  [magenta]%s[/magenta]  [red]deleted in-jail[/red]", k))
		case baseline[k] == got:
			lines = append(lines, fmt.Sprintf("  [magenta]%s[/magenta]  %s [dim](same as yolo's last render — redundant capture)[/dim]", k, got))
		case baseline[k] != "":
			lines = append(lines, fmt.Sprintf("  [magenta]%s[/magenta]  %s [dim](was %s)[/dim]", k, got, baseline[k]))
		default:
			lines = append(lines, fmt.Sprintf("  [magenta]%s[/magenta]  %s [dim](added in-jail)[/dim]", k, got))
		}
	}
	return lines
}

// readLastRenderKeys decodes the last_render sidecar into per-key one-line JSON,
// so a captured value can be compared against what yolo last wrote. An absent or
// undecodable sidecar yields an empty map (everything reads as "added in-jail").
func readLastRenderKeys(s manifest.Surface) map[string]string {
	baseline := map[string]string{}
	data, err := os.ReadFile(prismLastRenderPath(s.Agent, s.Name))
	if err != nil {
		return baseline
	}
	// The sidecar is in the SURFACE's codec, so only decode the object codecs; a
	// keyless surface has no keys to compare and falls back to the whole-file line.
	decoded, derr := jsonx.Decode(data)
	if derr != nil {
		return baseline
	}
	if m, ok := decoded.(*jsonx.OrderedMap); ok {
		for _, k := range m.Keys() {
			v, _ := m.Get(k)
			baseline[k] = oneLineJSON(v)
		}
	}
	return baseline
}

// configReset implements `yolo config reset <agent> [--surface s]`: discard the
// capture overlay so the surface returns to what its layers produce.
//
// It removes the overlay sidecar AND the last_render sidecar. Removing last_render
// too is not incidental: it makes the next boot take the §3.2 first-migration path,
// which re-seeds a truthful baseline with an empty overlay. Deleting only the
// overlay would leave the next boot diffing the (still-edited) file against a stale
// baseline and immediately re-capturing the very edits just discarded.
func configReset(args []string, out, errw io.Writer, color bool) int {
	agent, surface, rc := surfaceArgs("reset", args, out, errw)
	if rc >= 0 {
		return rc
	}
	surfaces := capturedSurfaces(agent, surface)
	if len(surfaces) == 0 {
		fmt.Fprintf(errw, "yolo config reset: no capture surfaces for agent %q%s\n",
			agent, surfaceSuffix(surface))
		return 1
	}

	pr := richtext.Printer{W: out, Color: color}
	cleared := 0
	for _, s := range surfaces {
		overlayPath := prismOverlayPath(s.Agent, s.Name)
		had := overlayKeyCount(s.Agent, s.Name)
		removedAny := false
		for _, p := range []string{overlayPath, prismLastRenderPath(s.Agent, s.Name)} {
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
		if err := truncateSurfaceToPureRender(s); err != nil {
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
	pr.Printf("[dim]The next jail launch re-renders these surfaces from their layers.[/dim]")
	return 0
}

// readOverlayValue decodes a surface's overlay sidecar, or nil.
func readOverlayValue(agent, name string) any {
	data, err := os.ReadFile(prismOverlayPath(agent, name))
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
// An ABSENT surface file is left absent: reset discards edits, it does not create
// files the jail has not written yet.
//
// The computed layer is deliberately not supplied — for the same reason
// `config render` omits it (jail-absolute paths built from $HOME; see
// renderSurface). The next boot recomputes it, so the only cost is that a
// computed-layer key is briefly missing from the file between reset and restart,
// which is strictly better than leaving a discarded edit in place.
func truncateSurfaceToPureRender(s manifest.Surface) error {
	path := expandHome(s.Path)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	sub := agentcfg.SubstituteWorkspace(s, containerWorkspace)
	var hostBytes []byte
	if surfaceHasHostLayer[sub.Agent+"/"+sub.Name] {
		hostBytes, _ = os.ReadFile(path)
	}
	res, err := agentcfg.Compose(agentcfg.Inputs{Surface: sub, HostBytes: hostBytes})
	if err != nil {
		return err
	}
	text := string(res.Encoded)
	if sub.Kind() == codec.KindObject {
		text += "\n"
	}
	// Truncate in place: the file may be a bind-mount target whose inode matters.
	return os.WriteFile(path, []byte(text), 0o644)
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
func configCapture(args []string, out, errw io.Writer, color bool) int {
	agent, surface, rc := surfaceArgs("capture", args, out, errw)
	if rc >= 0 {
		return rc
	}
	surfaces := capturedSurfaces(agent, surface)
	if len(surfaces) == 0 {
		fmt.Fprintf(errw, "yolo config capture: no capture surfaces for agent %q%s\n",
			agent, surfaceSuffix(surface))
		return 1
	}
	pr := richtext.Printer{W: out, Color: color}
	captured := 0
	for _, s := range surfaces {
		n, err := captureSurface(s)
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

// captureSurface folds one surface's on-disk state into its overlay, returning the
// resulting overlay key count, or -1 when there is no baseline to diff against.
func captureSurface(s manifest.Surface) (int, error) {
	path := expandHome(s.Path)
	current, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return -1, nil
		}
		return 0, err
	}
	lastRender, err := os.ReadFile(prismLastRenderPath(s.Agent, s.Name))
	if err != nil {
		return -1, nil // no baseline: cannot tell an edit from yolo's own output
	}
	overlayJSON, _ := os.ReadFile(prismOverlayPath(s.Agent, s.Name))

	// Reuse the ENGINE's capture path rather than reimplementing the diff: a second
	// implementation would be free to disagree with the boot render, which is the one
	// thing this must not do.
	out, err := agentcfg.ComposeStateful(agentcfg.StatefulInputs{
		Base:              agentcfg.Inputs{Surface: agentcfg.SubstituteWorkspace(s, containerWorkspace)},
		CurrentBytes:      current,
		LastRenderPresent: true,
		LastRenderBytes:   lastRender,
		OverlayJSON:       overlayJSON,
	})
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(prismOverlayPath(s.Agent, s.Name),
		append(out.OverlayJSON, '\n'), 0o644); err != nil {
		return 0, err
	}
	return overlayKeyCount(s.Agent, s.Name), nil
}
