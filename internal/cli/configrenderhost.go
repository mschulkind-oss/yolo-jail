package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// configrenderhost.go is `yolo config render --at host`: the preview of what `yolo host
// apply --assert` writes, byte for byte (docs/plans/notch-convergence.md item 23, row D5).
//
// IT RUNS THE HOST APPLY'S OWN RENDER, in observe, rather than composing the surfaces a second
// way. The preview used to go through the jail preview's loop — the EMBEDDED packs' surfaces at
// the autonomous posture, composed against the process home — and so showed keys the host never
// gets: MEASURED 2026-09-27, `additionalDirectories ["/"]` in the preview while host apply wrote
// `[]`. entrypoint.RenderHostPack in observe hands back each surface's Content, which is the
// write's own compose stopped before the write, so the two cannot disagree about a byte.
//
// The inputs are host apply's inputs, in host apply's order (applyHostSurveyed): the configured
// pack set from config.LoadPacks, destinations resolved, a doubly-owned surface refused, the
// cross-pack overlays collected at the host notch's posture and profile table, and the declared
// `host_management` contract.

// configRenderHost prints the host render of agent's surfaces (one surface when surface is
// non-empty). --explain prints the per-key record the write keeps instead of the file.
func configRenderHost(agent, surface string, explain bool, out, errw io.Writer, color bool) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(errw, "yolo config render: cannot resolve your home: %v\n", err)
		return 1
	}
	packs, unresolved := configuredPacksForInspection()
	if len(unresolved) > 0 {
		// Host apply refuses an --assert over an incomplete set; a preview shows what the
		// resolvable part renders and says what it left out, like the jail preview.
		fmt.Fprintf(errw, "yolo config render: not rendered — could not be resolved: %s. "+
			"`yolo host apply --assert` refuses an incomplete pack set.\n",
			describeUnresolved(unresolved))
	}
	packs, _ = packload.ResolveDestinations(packs)
	if cols := packload.ConfigSurfaceCollisions(packs); len(cols) > 0 {
		for _, c := range cols {
			fmt.Fprintf(errw, "yolo config render: surface %s claimed by %s: %s\n",
				c.Target, strings.Join(c.Packs, ", "), c.Reason)
		}
		fmt.Fprintf(errw, "yolo config render: `yolo host apply` refuses a config surface with "+
			"more than one owner, so nothing is rendered at the host.\n")
		return 1
	}
	overlays := packoverlay.Collect(packs, render.ProfileFor(render.KindHost).AgentAutonomy,
		overlayGateProfiles(render.KindHost))
	for _, prob := range overlays.Problems {
		fmt.Fprintf(errw, "yolo config render: not folded — %s (`yolo host apply` refuses this)\n", prob)
	}

	rc := 0
	known := map[string]bool{}
	var matched []entrypoint.HostRenderResult
	ownership := hostOwnership()
	for _, p := range packs {
		results, rerr := entrypoint.RenderHostPack(p, home, ownership, true, overlays)
		if rerr != nil {
			fmt.Fprintf(errw, "yolo config render: %s: %v\n", p.Name, rerr)
			rc = 1
			continue
		}
		for _, r := range results {
			a, n, _ := strings.Cut(r.Surface, "/")
			known[a] = true
			if a != agent || (surface != "" && n != surface) {
				continue
			}
			matched = append(matched, r)
		}
	}
	if len(matched) == 0 {
		if !known[agent] {
			names := make([]string, 0, len(known))
			for a := range known {
				names = append(names, a)
			}
			sort.Strings(names)
			fmt.Fprintf(errw, "yolo config render: no surfaces for agent %q at the host notch "+
				"(known: %s)\n", agent, strings.Join(names, ", "))
		} else {
			fmt.Fprintf(errw, "yolo config render: no surface %q for agent %q at the host notch\n",
				surface, agent)
		}
		return 1
	}

	pr := richtext.Printer{W: out, Color: color}
	for _, r := range matched {
		if r.Content == "" {
			// Skipped or refused: the render will not happen, so there is no file to show.
			// The Action is host apply's own sentence, so the preview says what the apply says.
			fmt.Fprintf(errw, "yolo config render: %s → %s: %s\n", r.Surface, r.Path, r.Action)
			if strings.HasPrefix(r.Action, "refused") {
				rc = 1
			}
			continue
		}
		header := fmt.Sprintf("[bold]# %s → %s[/bold]", r.Surface, r.Path)
		if explain {
			pr.Printf("%s [dim](layer that set each key, as `yolo host apply --assert` "+
				"records it)[/dim]", header)
			keys := make([]string, 0, len(r.Provenance))
			for k := range r.Provenance {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				pr.Printf("  [cyan]%s[/cyan]\t%s", k, colorLayer(r.Provenance[k]))
			}
			continue
		}
		pr.Print(header)
		// The file body verbatim: exactly the bytes the write puts at r.Path.
		io.WriteString(out, r.Content)
	}
	return rc
}
