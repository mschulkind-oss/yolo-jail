package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// configrenderhost.go is `yolo config render --at host`: the preview of what `yolo host
// apply --assert` writes, byte for byte (docs/plans/notch-convergence.md item 23, row D5),
// from the pack store as it stands: the preview never fetches (NC-D28).
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

// hostRenderPrelude is the host apply's composition PRELUDE, resolved once for a verb that
// renders what `yolo host apply --assert` writes without being it: the configured pack set (the
// pack store as it stands — the read-only verbs never fetch), its destinations resolved, the
// doubly-owned surfaces host apply refuses, and the cross-pack overlays collected at the host
// notch's posture and profile table. Its two readers are `yolo config render --at host` (the
// preview) and `yolo config reset` under `host_management: own` (the re-render after a reset);
// one prelude, so the preview, the reset and the apply cannot fold different packs.
type hostRenderPrelude struct {
	packs      []*packload.Pack
	unresolved []unresolvedPack
	collisions []packload.Collision
	overlays   *packoverlay.OverlaySet
}

// composeHostPrelude resolves the prelude. The derive inputs are composeHostInputs' (called by
// the reader that needs them, over hostRenderPrelude.packs), so a caller that only previews can
// report a composition failure in its own words.
func composeHostPrelude() hostRenderPrelude {
	packs, unresolved := configuredPacksForInspection()
	packs, _ = packload.ResolveDestinations(packs)
	// The fallbacks `yolo host apply` takes (hostTreeFallbacks), so the preview is the write's bytes.
	listPacks, _ := hostTreeFallbacks(packs)
	return hostRenderPrelude{packs: packs, unresolved: unresolved,
		collisions: packload.ConfigSurfaceCollisions(packs),
		overlays: packoverlay.Collect(listPacks, render.ProfileFor(render.KindHost).AgentAutonomy,
			overlayGateProfiles(render.KindHost, packs))}
}

// inputs composes the derive inputs over the prelude's packs, as host apply does (HC-D11).
func (c hostRenderPrelude) inputs(home string) (hostInputComposition, error) {
	return composeHostInputs(config.UserScopeConfigOrEmpty(), c.packs, home)
}

// configRenderHost prints the host render of agent's surfaces (one surface when surface is
// non-empty). --explain prints the per-key record the write keeps instead of the file.
func configRenderHost(agent, surface string, explain bool, out, errw io.Writer, color bool) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(errw, "yolo config render: cannot resolve your home: %v\n", err)
		return 1
	}
	comp := composeHostPrelude()
	packs, unresolved := comp.packs, comp.unresolved
	// THE PREVIEW NEVER FETCHES, by the read-only rule refreshHostPacks states: `yolo host
	// apply` fetches a never-fetched git pack and refreshes a branch-following one before it
	// renders, and this verb reads the pack store as it stands. So "byte for byte" holds for
	// the store's current copy; a pack the store lacks is the one case the two resolve
	// differently, and the preview says so rather than predicting a refusal the apply would
	// not make (NC-D28).
	var unfetched, broken []unresolvedPack
	for _, u := range unresolved {
		if u.NeedsInstall {
			unfetched = append(unfetched, u)
		} else {
			broken = append(broken, u)
		}
	}
	if len(unfetched) > 0 {
		fmt.Fprintf(errw, "yolo config render: not rendered — not fetched yet: %s. This "+
			"preview never fetches; `yolo host apply` fetches a git pack first, so its "+
			"render includes it.\n",
			describeUnresolved(unfetched))
	}
	if len(broken) > 0 {
		// Host apply refuses an --assert over an incomplete set; a preview shows what the
		// resolvable part renders and says what it left out, like the jail preview.
		fmt.Fprintf(errw, "yolo config render: not rendered — could not be resolved: %s. "+
			"`yolo host apply --assert` refuses an incomplete pack set.\n",
			describeUnresolved(broken))
	}
	if cols := comp.collisions; len(cols) > 0 {
		for _, c := range cols {
			fmt.Fprintf(errw, "yolo config render: surface %s claimed by %s: %s\n",
				c.Target, strings.Join(c.Packs, ", "), c.Reason)
		}
		fmt.Fprintf(errw, "yolo config render: `yolo host apply` refuses a config surface with "+
			"more than one owner, so nothing is rendered at the host.\n")
		return 1
	}
	overlays := comp.overlays
	for _, prob := range overlays.Problems {
		fmt.Fprintf(errw, "yolo config render: not folded — %s (`yolo host apply` refuses this)\n", prob)
	}

	// THE SAME COMPOSITION host apply renders from (HC-D11), so the preview is the write's
	// bytes for a derived surface too: its computed layer over the host's inputs.
	inputs, cerr := comp.inputs(home)
	if cerr != nil {
		fmt.Fprintf(errw, "yolo config render: not rendered — %v (`yolo host apply` refuses "+
			"this too)\n", cerr)
		return 1
	}
	for _, line := range sortedOmitted(inputs.omitted) {
		fmt.Fprintf(errw, "yolo config render: %s\n", line)
	}

	rc := 0
	known := map[string]bool{}
	var matched []entrypoint.HostRenderResult
	ownership := hostOwnership()
	for _, p := range packs {
		results, rerr := entrypoint.RenderHostPack(p, home, ownership, true, overlays, inputs.inputs)
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
	// THE USER'S host_files ENTRIES, which host apply renders after the packs' surfaces through
	// the same loop (OQ-NC8), previewed the same way: agent "user", one surface per entry.
	userResults, uerr := entrypoint.RenderHostUserFiles(
		readHostUserFiles(config.UserScopeConfigOrEmpty()).render, home, ownership, true)
	if uerr != nil {
		fmt.Fprintf(errw, "yolo config render: %s: %v\n", hostUserFilesOwner, uerr)
		rc = 1
	}
	for _, r := range userResults {
		a, n, _ := strings.Cut(r.Surface, "/")
		known[a] = true
		if a == agent && (surface == "" || n == surface) {
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
