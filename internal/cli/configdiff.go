package cli

// configdiff.go implements `yolo config diff` and `yolo config reset` — the
// inspect-and-undo half of the capture overlay.
//
// `mode: capture` is only defensible if divergence is visible AND reversible: a
// captured edit outranks the host layer forever, so without these two commands the
// only cure is knowing to delete a file in <workspace>/.yolo/prism/ by hand
// (docs/design/composed-file-permissions.md §5).
//
// `diff` carries a SECOND kind of divergence for the same reason: a pack's
// `config-overlay` contributions to a surface another pack owns (ruling R3,
// docs/design/pack-config-collaboration.md §7). Same shape of question — a key in the
// file that the file itself cannot account for — so it reads out of the same command.

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
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// surfaceArgs parses the shared `<agent> [--surface <name>]` argument shape used
// by diff and reset. Returns rc=-1 when parsing succeeded.
func surfaceArgs(cmd string, args []string, out, errw io.Writer) (agent, surface string, force bool, rc int) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case isHelpToken(a):
			io.WriteString(out, configUsage+"\n")
			return "", "", false, 0
		case a == "--surface":
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo config %s: --surface needs a value\n", cmd)
				return "", "", false, 2
			}
			i++
			surface = args[i]
		case strings.HasPrefix(a, "--surface="):
			surface = strings.TrimPrefix(a, "--surface=")
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
	return agent, surface, force, -1
}

// refuseHostSideWrite is the Phase-0 data-loss guard. Host-side `config reset`/`capture`
// resolve `~` against the INVOKING human's real home (expandHome → paths.Home()) and
// write it — reset truncates a real dotfile to its (often empty) pure render; capture
// copies real host config into the workspace sidecar tree. Both are destructive on a
// file yolo does not own in that context. surfacesAreLocal() is true only in the jail
// that owns /workspace; anywhere else (host-side, or a different workspace's surfaces)
// a write is refused unless --force. Returns true when the caller must abort.
func refuseHostSideWrite(cmd string, force bool, errw io.Writer) bool {
	if surfacesAreLocal() || force {
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
//
// It also reports config-overlay PROVENANCE — which pack contributed which key to a
// surface another pack owns, and whether that key won (ruling R3,
// docs/design/pack-config-collaboration.md §7). That belongs here rather than in a new
// command because it answers the same question the capture diff does — "why does this
// file say that, and who is responsible?" — and because an overlay folds in BELOW the
// owner's managed layer, so it leaves no trace in the surface file at all. Provenance
// nobody can read does not make an override legible, which was the entire justification
// for the kind.
func configDiff(args []string, out, errw io.Writer, color bool) int {
	agent, surface, _, rc := surfaceArgs("diff", args, out, errw)
	if rc >= 0 {
		return rc
	}
	pr := richtext.Printer{W: out, Color: color}
	overlaid, unresolved := overlayContributionRows(agent, surface)

	surfaces := capturedSurfaces(agent, surface)
	if len(surfaces) == 0 && len(overlaid) == 0 && len(unresolved) == 0 {
		fmt.Fprintf(errw, "yolo config diff: no capture surfaces or pack config-overlays "+
			"for agent %q%s\n", agent, surfaceSuffix(surface))
		return 1
	}
	// A pack this command could not read might be the one contributing the key the user
	// is asking about, so an incomplete answer says so rather than reading as complete.
	if len(unresolved) > 0 {
		pr.Printf("[yellow]⚠ not inspected (fetched packs need `yolo pack install`): %s "+
			"— any config-overlay they declare is not listed below.[/yellow]",
			strings.Join(unresolved, ", "))
	}

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
	writeOverlayContributions(pr, overlaid)
	if !found {
		pr.Printf("[dim]No captured in-jail edits for %s%s.[/dim]", agent, surfaceSuffix(surface))
		return 0
	}
	pr.Printf("[dim]These values were captured from in-jail edits and outrank the host layer.[/dim]")
	pr.Printf("[dim]Discard them with: yolo config reset %s[/dim]", agent)
	return 0
}

// overlayContribution is one surface's config-overlay picture for the diff: who
// contributed, which keys, and — where a render recorded it — which layer actually won each
// key.
type overlayContribution struct {
	Surface string // "agent/name"
	Path    string
	// Packs are the contributing packs in fold order (later wins).
	Packs []string
	// Keys maps a contributed top-level key to the pack that contributed it LAST (the one
	// whose value the fold would use).
	Keys map[string]string
	// Winners is the RECORDED provenance, key → winning layer, read from whichever notch's
	// record describes these surfaces. nil when no render has written one.
	Winners map[string]string
	// Notch is the LABEL of the level where Winners was measured (render.Kind.String()), so a
	// reported winner is attributed as well as measured. The notches render into different
	// homes from different postures (the host renders the guarded autonomy posture, pure RMW),
	// so "managed won" without a notch is an incomplete fact. A display string rather than a
	// render.Kind because nothing downstream DECIDES on it — the writer interpolates it into a
	// sentence, and the decision it came from was made once in surfaceProvenance.
	Notch string
	// NoRecordReason explains an ABSENT record, in the words of the specific state it is.
	// Empty when Winners is non-nil. Three states, and collapsing them is a misreport in
	// its own right: a mode that keeps no record by design is expected, an unrendered
	// surface is worth investigating, and a host notch nobody has asserted yet has an
	// obvious remedy.
	NoRecordReason string
	// Retired maps a RETIRED key to the layer that last claimed it — the record's own answer
	// for a key yolo wrote for a pack that is no longer active. Read out of Winners rather
	// than from any declaration, because by definition nothing declares these keys any more:
	// the contributing pack has left `packs`, so it appears in neither Packs nor Keys and
	// there is no other source for the fact.
	//
	// It is the reader half of the anti-laundering record (agentcfg.RetiredLayer). Without it
	// the fix would be invisible: the orphaned key sits in the user's file attributed to a
	// pack they dropped, and the only place that says so is a state-dir file they have never
	// heard of.
	Retired map[string]string
}

// overlayContributionRows resolves the config-overlay contributions landing on the given
// agent's surfaces, honoring an optional surface filter, plus the names of any configured
// pack it could not read (see configuredPacksForInspection).
//
// It reads the PACK DECLARATIONS rather than the record for "who contributed what",
// because the record holds only the WINNER of each key — a contribution the owner's
// managed layer beat leaves no entry, and "your overlay lost" is exactly the case a user
// needs told. The record then supplies the winner where a render has measured one.
//
// WHICH notch's record, and it is the load-bearing choice here: the surfaces `config diff`
// describes are the ones THIS invocation's home would carry, so an in-jail run reads the
// jail's sidecar tree and a host-side run reads the host provenance record. Reading the
// jail's record host-side (or inferring, which is what this used to do when it found
// nothing) reports one notch's outcome as the other's — and since the host renders a
// different posture into a different home, that answer can be the exact opposite of what
// landed. Measured-or-silent, never guessed.
//
// The surfaces come from the LOADED PACKS rather than surfaceManifest(), which is embedded
// packs only (see internal/cli/surfaces.go). That limitation is exactly wrong here: a
// third-party pack is the likely OWNER in the Layout C story, so keying on the embedded set
// would leave the overlay case this command exists for unreportable.
func overlayContributionRows(agent, surface string) ([]overlayContribution, []string) {
	packs, unresolved := configuredPacksForInspection()
	if len(packs) == 0 {
		return nil, unresolved
	}
	// WHICH NOTCH this invocation describes, resolved ONCE into a render.Kind rather than
	// carried as a bool and re-labelled twice (plan §6c step 3). It was `host :=
	// !surfacesAreLocal()`, and every downstream fact — the posture, the record to read, the
	// name printed beside a winner — was re-derived from that bool with its own literal.
	notch := render.KindJail
	if !surfacesAreLocal() {
		notch = render.KindHost
	}
	// The posture matches the notch whose surfaces we are describing: the jail renders
	// autonomy ON, the host renders the guarded posture (§4.2). It only patches the owner's
	// managed layer, so it changes which keys an overlay LOSES, not which surfaces exist —
	// but that is exactly the thing being reported, so it must match. Read off the notch's
	// PROFILE, so this report cannot disagree with the render it is describing.
	set := packoverlay.Collect(packs, render.ProfileFor(notch).AgentAutonomy)
	var out []overlayContribution
	for _, s := range packSurfacesForAgent(packs, agent, surface) {
		overlays := set.For(s.Agent, s.Name)
		row := overlayContribution{
			Surface: s.Agent + "/" + s.Name,
			Path:    s.Path,
			Keys:    map[string]string{},
		}
		row.Winners, row.Notch, row.NoRecordReason = surfaceProvenance(s, notch)
		row.Retired = retiredKeys(row.Winners)
		// A surface with NEITHER a live overlay NOR a retired key has nothing to report here.
		// The retired half is why this is not the old `len(overlays) == 0` skip: an orphaned
		// key's whole defining property is that no pack declares it any more, so a surface
		// filtered on live contributions is exactly the one where the report is needed and
		// exactly the one it would never reach.
		if len(overlays) == 0 && len(row.Retired) == 0 {
			continue
		}
		for _, ov := range overlays {
			row.Packs = append(row.Packs, ov.Pack)
			if layer, isMap := ov.Data.(map[string]any); isMap {
				for k := range layer {
					row.Keys[k] = ov.Pack // later pack wins the attribution, as it wins the fold
				}
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Surface < out[j].Surface })
	return out, unresolved
}

// surfaceProvenance reads the recorded per-key winners for one surface at the notch this
// invocation describes, returning (winners, notch-label, reason-it-is-absent). Exactly one of
// winners / reason is meaningful: a non-nil map means measured, and a non-empty reason
// names WHICH absence this is.
//
// The host notch is the simple case and the reason this function exists: `yolo host apply` is
// pure RMW at every mode, and it records a winner for every surface it writes, so there is
// exactly one question — has an apply asserted yet? The jail notch has the mode split,
// because an `rmw`/`computed` surface in a jail keeps no record by design (§8) and that
// must not read as a loss.
//
// It switches on the render.Kind and LABELS with notch.String(), rather than taking a bool and
// spelling "host"/"jail" at five returns (plan §6c step 3). What the notch decides here is a
// FILE LOCATION and a remedy — two things that genuinely differ per notch, which is why this
// stays a switch; what it no longer decides is how the answer is spelled. A notch with no
// stated record location gets the fail-closed answer rather than the jail's tree: `guest`
// cannot reach here today (the caller resolves jail-or-host), and inheriting a location if it
// ever does is the D2 bug in the reader.
func surfaceProvenance(s manifest.Surface, notch render.Kind) (winners map[string]string, notchLabel, reason string) {
	switch notch {
	case render.KindHost:
		if w := readProvenance(hostProvenancePath(s.Agent, s.Name)); w != nil {
			return w, notch.String(), ""
		}
		// No mode split here: the host render is pure RMW and records every surface it
		// writes, so an absent record means no apply has asserted this surface — which has
		// a remedy, unlike the by-design absences below.
		return nil, notch.String(), "no `yolo host apply --assert` has rendered it yet"
	case render.KindJail:
		if w := readProvenance(prismProvenancePath(s.Agent, s.Name)); w != nil {
			return w, notch.String(), ""
		}
		if s.ResolvedMode() == manifest.ModeRMW || s.ResolvedMode() == manifest.ModeComputed {
			return nil, notch.String(), "this surface's mode keeps no provenance sidecar"
		}
		return nil, notch.String(), "not rendered in this workspace yet"
	default:
		return nil, notch.String(), "no provenance record location is stated for this confinement level"
	}
}

// packSurfacesForAgent returns the loaded packs' surfaces owned by one agent, honoring an
// optional name filter. Deduped by identity, last declaration winning — matching
// manifest.Merge's rule, so this reports the surface the boot render would actually use.
func packSurfacesForAgent(packs []*packload.Pack, agent, name string) []manifest.Surface {
	byKey := map[manifest.SurfaceKey]manifest.Surface{}
	var order []manifest.SurfaceKey
	for _, p := range packs {
		surfaces, _ := p.Surfaces()
		for _, s := range surfaces {
			if s.Agent != agent || (name != "" && s.Name != name) {
				continue
			}
			if _, seen := byKey[s.Key()]; !seen {
				order = append(order, s.Key())
			}
			byKey[s.Key()] = s
		}
	}
	out := make([]manifest.Surface, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}

// retiredKeys extracts the RETIRED entries from a recorded winner map: key → the layer that
// last claimed it. nil when the record has none (the overwhelmingly common case) or when
// there is no record at all.
//
// The record is the only source for this. A retired key is by construction one no live layer
// declares, so there is nothing to cross-check it against — which is also why the label had
// to carry the previous layer's name rather than a bare "retired" (agentcfg.RetiredLayer).
func retiredKeys(winners map[string]string) map[string]string {
	var out map[string]string
	for k, layer := range winners {
		last, retired := agentcfg.RetiredOf(layer)
		if !retired {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = last
	}
	return out
}

// writeOverlayContributions prints the R3 provenance section: per surface, which packs
// contribute and what became of each contributed key, plus any key yolo wrote for a pack
// that is no longer configured.
func writeOverlayContributions(pr richtext.Printer, rows []overlayContribution) {
	if len(rows) == 0 {
		return
	}
	for _, row := range rows {
		if len(row.Packs) > 0 {
			pr.Printf("[bold]# %s → %s[/bold]  [dim]config-overlay from %s[/dim]",
				row.Surface, row.Path, strings.Join(row.Packs, ", "))
		} else {
			// RETIRED-ONLY surface: no pack contributes to it any more, so naming contributors
			// would print an empty list. The heading still has to appear, because the retired
			// lines below it are the whole point of reaching this surface.
			pr.Printf("[bold]# %s → %s[/bold]  [dim]no live config-overlay[/dim]",
				row.Surface, row.Path)
		}
		for _, k := range sortedStrings(row.Retired) {
			// A KEY YOLO WROTE FOR A LAYER THAT NO LONGER CLAIMS IT. Reported as yolo's own
			// output with the layer named, which is precisely what the record refuses to launder
			// into `host`. The wording covers both ways a layer stops claiming a key (its pack
			// left `packs`, or its pack stopped declaring the key) because the record cannot
			// distinguish them — and does not need to: the fact and the remedy are the same.
			// The remedy is the user's to take, since yolo left the value working rather than
			// reaching into a file it does not own.
			pr.Printf("  [magenta]%s[/magenta]  [yellow]written by a past apply for %s, which no "+
				"longer asserts it[/yellow] [dim](%s notch — the value is still in the file and "+
				"still in effect; nothing re-asserts it, so delete the key to drop it)[/dim]",
				k, row.Retired[k], row.Notch)
		}
		for _, k := range sortedStrings(row.Keys) {
			pack := row.Keys[k]
			winner, recorded := row.Winners[k]
			switch {
			case row.Winners == nil:
				// NO RECORD AT ALL. Say which absence this is rather than inferring a winner
				// from the declarations — that inference is what made this command print
				// "contributed by X but managed won" for a key no `managed` layer even
				// declared, which reads as a confident wrong answer rather than an unknown.
				pr.Printf("  [magenta]%s[/magenta]  [dim]contributed by %s (winner not measured at "+
					"the %s notch — %s)[/dim]", k, pack, row.Notch, row.NoRecordReason)
			case !recorded:
				// The surface DID render and the record does not mention this key. Measured,
				// and it means the key never made it into the file: the only way a
				// contributed key is unattributed is a tombstone deleting it, or a transform
				// dropping it. Reported as a measurement, not as a loss to some layer.
				pr.Printf("  [magenta]%s[/magenta]  [yellow]contributed by %s but the key is not in "+
					"the rendered file[/yellow] [dim](%s notch — deleted by a tombstone or a "+
					"transform)[/dim]", k, pack, row.Notch)
			case winner == agentcfg.OverlayLayer(pack):
				pr.Printf("  [magenta]%s[/magenta]  [green]set by %s[/green] [dim](won the key at the "+
					"%s notch)[/dim]", k, pack, row.Notch)
			default:
				// The load-bearing line: the overlay folded in and LOST. Naming the layer
				// that beat it is what turns "my key did nothing" into an actionable fact —
				// and it is only worth printing because the layer is now MEASURED from the
				// render's own record rather than guessed from what the packs declare.
				pr.Printf("  [magenta]%s[/magenta]  [yellow]contributed by %s but %s won[/yellow] "+
					"[dim](measured at the %s notch)[/dim]", k, pack, winner, row.Notch)
			}
		}
		pr.Printf("")
	}
	// The precedence footer only applies to LIVE contributions. Printing it for a
	// retired-only surface would end on "drop the contributing pack to remove them" — advice
	// the user has already taken, and the reason these keys exist.
	if anyLiveOverlay(rows) {
		pr.Printf("[dim]config-overlay keys fold in BELOW the owning pack's managed layer, so the " +
			"owner still wins a genuine conflict. Drop the contributing pack from `packs` to " +
			"remove them.[/dim]")
	}
	if anyRetired(rows) {
		// Said separately, because the remedy is the OPPOSITE of the live-overlay footer's:
		// dropping the pack is what PRODUCED these keys, so "drop the pack" is advice the user
		// has already taken.
		pr.Printf("[dim]`written by a past apply` marks a key yolo asserted for a layer that has " +
			"since stopped claiming it (its pack left `packs`, or stopped declaring the key). " +
			"yolo does not remove it — the file is yours — so it keeps working until you delete " +
			"it. The record remembers whose it was rather than relabelling it as yours.[/dim]")
	}
}

// anyRetired reports whether any row carries a retired key, so the retirement footer is
// printed only when there is something to explain.
func anyRetired(rows []overlayContribution) bool {
	for _, row := range rows {
		if len(row.Retired) > 0 {
			return true
		}
	}
	return false
}

// anyLiveOverlay reports whether any row has a pack still contributing to it. False for a
// report that reached these surfaces ONLY through retired keys, where the precedence footer
// would describe a mechanism nothing in the output uses.
func anyLiveOverlay(rows []overlayContribution) bool {
	for _, row := range rows {
		if len(row.Packs) > 0 {
			return true
		}
	}
	return false
}

// readProvenance decodes a provenance record at path into key → winning layer. An absent
// or malformed file yields nil, which callers read as "no render recorded here" rather than
// "nothing won" — the two are different and conflating them would report an unrendered
// surface as one where every overlay lost.
//
// Path-taking rather than (agent, name)-taking, because there are now two records to read
// — the jail's per-workspace sidecar and the host's per-home one — and the CHOICE of which
// belongs to the caller that knows which notch it is describing (surfaceProvenance). A
// function that resolved the path itself would have to re-derive that decision, which is
// how a reader ends up reporting one notch's outcome as the other's.
func readProvenance(path string) map[string]string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// PRESENT-BUT-EMPTY returns an empty non-nil map, not nil, and the distinction is the
	// reason the writer emits an empty file rather than skipping: "this surface rendered and
	// attributed no keys" is a measurement, while nil means "no render recorded here". A
	// reader that collapsed them would answer a question it had actually measured with "we
	// do not know", which is the mirror image of the confident-wrong-answer this record
	// exists to remove. agentcfg.ParseProvenanceRecord guarantees the non-nil, and is shared
	// with the writer's own re-read so the format has exactly one definition.
	return agentcfg.ParseProvenanceRecord(data)
}

// configuredPacksForInspection loads the packs this workspace's config selects, for the
// read-only inspection commands, plus the names of any that could not be resolved offline.
//
// EMBEDDED AND LOCAL packs resolve; a git-sourced one needs `yolo pack install` and comes
// back nil, so its name is returned for the caller to report. That limitation is the same
// one surfaceManifest() carries and is stated for the same reason: a `config diff` that
// failed on an unreachable remote would be worse than one that names what it could not
// read.
func configuredPacksForInspection() ([]*packload.Pack, []string) {
	entries, err := config.LoadPacks(nil)
	if err != nil {
		return nil, nil
	}
	var packs []*packload.Pack
	var unresolved []string
	for _, e := range entries {
		if p := packForCheckDeps(e); p != nil {
			packs = append(packs, p)
			continue
		}
		unresolved = append(unresolved, e.Name)
	}
	return packs, unresolved
}

// sortedStrings returns a string-keyed map's keys sorted, for deterministic output.
func sortedStrings(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
	agent, surface, force, rc := surfaceArgs("reset", args, out, errw)
	if rc >= 0 {
		return rc
	}
	if refuseHostSideWrite("reset", force, errw) {
		return 1
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
	agent, surface, force, rc := surfaceArgs("capture", args, out, errw)
	if rc >= 0 {
		return rc
	}
	if refuseHostSideWrite("capture", force, errw) {
		return 1
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
// resulting overlay key count, or -1 when there is no baseline to diff against.
// LOCAL resolution: the process home and the cwd's workspace.
func captureSurface(s manifest.Surface) (int, error) {
	return captureSurfaceAt(s, captureLocation{
		surface:    expandHome(s.Path),
		lastRender: prismLastRenderPath(s.Agent, s.Name),
		overlay:    prismOverlayPath(s.Agent, s.Name),
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
	if err := os.WriteFile(at.overlay, append(out.OverlayJSON, '\n'), 0o644); err != nil {
		return 0, err
	}
	return overlayKeyCountAt(at.overlay), nil
}
