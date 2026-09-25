package cli

// configprovenance.go is `yolo config ls`'s per-key provenance report: for each composed
// surface, which pack contributed which key through `config-overlay`, whether that key WON,
// and any key yolo wrote for a layer that no longer claims it (ruling R3,
// docs/reference/pack-system.md §7).
//
// # It moved here out of `diff`, and the reason is the verb's SUBJECT
//
// `yolo config diff` printed it beside the captured divergence until
// docs/reference/config-target-resolution.md [OQ-CR7] ruled (a), against its own leaning:
// **`diff` reports the captured divergence and nothing else.**
//
// What `diff` answers is *"if I deleted all of these surfaces, discarded every capture and
// regenerated them, how would what I have now look different?"* A pure regeneration is
// exactly what the render produces, so the only thing that can survive that wipe is what was
// CAPTURED — which makes the capture store `diff`'s subject by definition. Provenance answers
// a different question, *"which layer did this key come from"*, and that is a property of the
// RENDER: it would read IDENTICALLY before and after the wipe `diff` measures against, so it
// cannot justify its place there.
//
// It was also the block that made §2.3 F1 possible. Two independently resolved readers in one
// report, only one of which ever named its home: host-side in a checkout, `diff` printed that
// jail's captured edits above the INVOKING USER'S real home's provenance. One resolved target
// closed that; moving the block closes it a second way, by leaving the verb one subject.
//
// `ls` is where the render is described — it already reports per-surface state, layers and
// mode — so this is the block's home rather than a new command. It normally prints nothing:
// only a surface another pack contributes to, or one carrying a retired key, reaches it.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// overlayContribution is one surface's config-overlay picture: who
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
	// Lists is the surface's config-list account
	// (docs/reference/pack-system.md#config-list-visibility): one row per array the packs
	// append entries to, sorted by path. Its own field rather than more Keys, because a
	// list is attributed per ENTRY and to several packs at once, which a key → last-pack
	// map cannot say.
	Lists []listContributionRow
}

// listContributionRow is one array a config-list appends to: the contributing packs in fold
// order with how many entries each declares there, and what — if anything — replaces the
// assembled result.
type listContributionRow struct {
	Path  string // the RFC 6901 pointer
	Packs []listPackEntries
	// ReplacedBy names a layer that replaces the assembled array wholesale: "managed" (the
	// owner DECLARES the path, or an ancestor as a non-object — always true, so it is read off
	// the declaration) or "overlay" (this target's capture store holds a captured deletion or
	// non-array edit there — MEASURED from the store). "" when neither. `computed` is not
	// judged: it is built per boot from jail paths, which is why `render` omits it too.
	ReplacedBy string
	// CapturedAdd / CapturedRemove count the per-entry in-jail edits recorded at this path in
	// the list-capture sidecar — what `yolo config diff` itemizes.
	CapturedAdd, CapturedRemove int
}

// listPackEntries is one contributing pack's share of a list row.
type listPackEntries struct {
	Pack    string
	Entries int
}

// overlayContributionRows resolves the config-overlay contributions landing on the given
// agent's surfaces, honoring an optional surface filter, plus the names of any configured
// pack it could not read (see configuredPacksForInspection). An EMPTY agent means every one,
// which is what `ls` asks: it lists the whole manifest rather than one agent's surfaces.
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
func overlayContributionRows(t configTarget, agent, surface string) ([]overlayContribution, []unresolvedPack) {
	packs, unresolved := configuredPacksForInspection()
	if len(packs) == 0 {
		return nil, unresolved
	}
	// WHICH NOTCH this invocation describes comes off the RESOLVED TARGET, which is the one
	// place it is decided (docs/reference/config-target-resolution.md#the-config-target). It was
	// `notch := KindJail; if !surfacesAreLocal() { notch = KindHost }` — a second predicate,
	// and the one that made a host-side report in a workspace read the invoking user's real
	// home's provenance beside that jail's captures (§2.3 F1).
	notch := t.notch
	// The posture matches the notch whose surfaces we are describing: the jail renders
	// autonomy ON, the host renders the guarded posture (§4.2). It only patches the owner's
	// managed layer, so it changes which keys an overlay LOSES, not which surfaces exist —
	// but that is exactly the thing being reported, so it must match. Read off the notch's
	// PROFILE, so this report cannot disagree with the render it is describing. The profile
	// table matches the same notch for the same reason: a `profile`-gated overlay is not a
	// contribution this report may list when the selection that gates it is off.
	set := packoverlay.Collect(packs, render.ProfileFor(notch).AgentAutonomy,
		overlayGateProfiles(notch))
	var out []overlayContribution
	for _, s := range packSurfacesForAgent(packs, agent, surface) {
		overlays := set.For(s.Agent, s.Name)
		row := overlayContribution{
			Surface: s.Agent + "/" + s.Name,
			Path:    s.Path,
			Keys:    map[string]string{},
		}
		row.Winners, row.Notch, row.NoRecordReason = surfaceProvenance(t, s)
		row.Retired = retiredKeys(row.Winners)
		row.Lists = listContributionRows(t, s, set.ListsFor(s.Agent, s.Name))
		// A surface with NEITHER a live overlay NOR a retired key has nothing to report here.
		// The retired half is why this is not the old `len(overlays) == 0` skip: an orphaned
		// key's whole defining property is that no pack declares it any more, so a surface
		// filtered on live contributions is exactly the one where the report is needed and
		// exactly the one it would never reach.
		if len(overlays) == 0 && len(row.Retired) == 0 && len(row.Lists) == 0 {
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

// listContributionRows builds one surface's config-list rows: its contributions grouped by
// path (sorted), each pack named once in fold order with the entries it declares there, plus
// the replacement and the in-jail edits this target's stores record at the path. nil for a
// surface no list targets — the overwhelmingly common answer.
func listContributionRows(t configTarget, s manifest.Surface, lists []agentcfg.ListContribution) []listContributionRow {
	if len(lists) == 0 {
		return nil
	}
	overlay := jsonx.Plain(readOverlayValue(t.overlayFile(s.Agent, s.Name)))
	var records map[string]agentcfg.ListRecord
	if f := t.listCaptureFile(s.Agent, s.Name); f.name != "" {
		if data, err := f.read(); err == nil {
			_, records = agentcfg.ListCaptureRecords(data)
		}
	}
	var out []listContributionRow
	for _, path := range agentcfg.ListPaths(lists) {
		row := listContributionRow{Path: path}
		at := map[string]int{}
		for _, l := range lists {
			if l.Path != path {
				continue
			}
			i, seen := at[l.Pack]
			if !seen {
				i = len(row.Packs)
				at[l.Pack] = i
				row.Packs = append(row.Packs, listPackEntries{Pack: l.Pack})
			}
			row.Packs[i].Entries += len(l.Add)
		}
		switch {
		case agentcfg.ListPathReplacedBy(s.ManagedMap(), path):
			row.ReplacedBy = "managed"
		case agentcfg.ListPathReplacedBy(overlay, path):
			row.ReplacedBy = "overlay"
		}
		if rec, ok := records[path]; ok {
			row.CapturedAdd, row.CapturedRemove = len(rec.Add), len(rec.Remove)
		}
		out = append(out, row)
	}
	return out
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
// It reads the TARGET's notch and LABELS with notch.String(), rather than taking a bool and
// spelling "host"/"jail" at five returns (plan §6c step 3). What the notch decides here is a
// FILE LOCATION and a remedy — two things that genuinely differ per notch, which is why this
// stays a switch; what it no longer decides is how the answer is spelled. A notch with no
// stated record location gets the fail-closed answer rather than the jail's tree: `guest`
// cannot reach here today (the caller resolves jail-or-host), and inheriting a location if it
// ever does is the D2 bug in the reader.
func surfaceProvenance(t configTarget, s manifest.Surface) (winners map[string]string, notchLabel, reason string) {
	notch := t.notch
	switch notch {
	case render.KindHost:
		if w := readProvenance(t.provenanceFile(s.Agent, s.Name)); w != nil {
			return w, notch.String(), ""
		}
		// No mode split here: the host render is pure RMW and records every surface it
		// writes, so an absent record means no apply has asserted this surface — which has
		// a remedy, unlike the by-design absences below.
		return nil, notch.String(), "no `yolo host apply --assert` has rendered it yet"
	case render.KindJail:
		if w := readProvenance(t.provenanceFile(s.Agent, s.Name)); w != nil {
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

// packSurfacesForAgent returns the loaded packs' surfaces owned by one agent — or by every
// agent, when agent is empty — honoring an optional name filter. Deduped by identity, last declaration winning — matching
// manifest.Merge's rule, so this reports the surface the boot render would actually use.
func packSurfacesForAgent(packs []*packload.Pack, agent, name string) []manifest.Surface {
	byKey := map[manifest.SurfaceKey]manifest.Surface{}
	var order []manifest.SurfaceKey
	for _, p := range packs {
		surfaces, _ := p.Surfaces()
		for _, s := range surfaces {
			if (agent != "" && s.Agent != agent) || (name != "" && s.Name != name) {
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
		if len(row.Packs) > 0 || len(row.Lists) > 0 {
			var from []string
			if len(row.Packs) > 0 {
				from = append(from, "config-overlay from "+strings.Join(row.Packs, ", "))
			}
			if len(row.Lists) > 0 {
				from = append(from, "config-list from "+strings.Join(listRowPacks(row.Lists), ", "))
			}
			pr.Printf("[bold]# %s → %s[/bold]  [dim]%s[/dim]",
				row.Surface, row.Path, strings.Join(from, "; "))
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
				// contributed key is unattributed is a tombstone deleting it. Reported as a
				// measurement, not as a loss to some layer.
				pr.Printf("  [magenta]%s[/magenta]  [yellow]contributed by %s but the key is not in "+
					"the rendered file[/yellow] [dim](%s notch — deleted by a tombstone)[/dim]",
					k, pack, row.Notch)
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
		for _, l := range row.Lists {
			writeListContribution(pr, row.Surface, l)
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
	if anyLiveList(rows) {
		// The list twin of the precedence footer, and its remedy differs in the half that
		// matters: dropping the pack removes ITS entries, never the array or an entry the user
		// added themselves (per-entry capture, OQ-AL1).
		pr.Printf("[dim]config-list entries append after every config-overlay, in `packs` order, " +
			"first occurrence winning; a captured edit, a computed value or the owner's managed " +
			"layer can still replace the whole array. Drop the contributing pack from `packs` to " +
			"remove its entries — an entry you added yourself stays.[/dim]")
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

// writeListContribution prints one array's config-list line: the contributing packs in the
// order their entries append, and either what the assembled array is or which layer replaces
// it — the distinction pack-system.md#config-list-visibility exists for — plus the in-jail
// edits recorded there.
func writeListContribution(pr richtext.Printer, surface string, l listContributionRow) {
	parts := make([]string, 0, len(l.Packs))
	for _, p := range l.Packs {
		parts = append(parts, fmt.Sprintf("%s (%d %s)", p.Pack, p.Entries, plural(p.Entries, "entry", "entries")))
	}
	who := strings.Join(parts, ", ")
	switch l.ReplacedBy {
	case "managed":
		pr.Printf("  [magenta]%s[/magenta]  [yellow]entries from %s, but the owner's managed layer "+
			"replaces this array — they do not reach the file[/yellow]", l.Path, who)
	case "overlay":
		pr.Printf("  [magenta]%s[/magenta]  [yellow]entries from %s, but a captured in-jail edit "+
			"replaces this array — they do not reach the file[/yellow] [dim](yolo config reset %s "+
			"restores the assembled list)[/dim]", l.Path, who, surface)
	default:
		pr.Printf("  [magenta]%s[/magenta]  [green]assembled: the lower layers' entries, then "+
			"entries from %s[/green]", l.Path, who)
	}
	if l.CapturedAdd+l.CapturedRemove > 0 {
		pr.Printf("    [dim]%d added and %d removed in-jail, per entry (yolo config diff %s)[/dim]",
			l.CapturedAdd, l.CapturedRemove, surface)
	}
}

// listRowPacks is every pack contributing to any of a surface's arrays, in first-seen order.
func listRowPacks(rows []listContributionRow) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rows {
		for _, p := range r.Packs {
			if !seen[p.Pack] {
				seen[p.Pack] = true
				out = append(out, p.Pack)
			}
		}
	}
	return out
}

// anyLiveList reports whether any row carries a config-list contribution, so the list
// precedence footer prints only when a list line precedes it.
func anyLiveList(rows []overlayContribution) bool {
	for _, row := range rows {
		if len(row.Lists) > 0 {
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

// readProvenance decodes the given provenance record into key → winning layer. An absent
// or malformed file yields nil, which callers read as "no render recorded here" rather than
// "nothing won" — the two are different and conflating them would report an unrendered
// surface as one where every overlay lost.
//
// File-taking rather than (agent, name)-taking, because there are now two records to read
// — the jail's per-workspace sidecar and the host's per-home one — and the CHOICE of which
// belongs to the caller that knows which notch it is describing (surfaceProvenance). A
// function that resolved the path itself would have to re-derive that decision, which is
// how a reader ends up reporting one notch's outcome as the other's.
func readProvenance(f captureFile) map[string]string {
	if f.name == "" {
		return nil
	}
	data, err := f.read()
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
// read-only inspection commands, plus every one that could not be resolved and why.
//
// It resolves the way a launch does (resolveConfiguredPack): embedded, local, and a git pack
// from the pack store, offline. One the store does not have is returned for the caller to
// report — a `config diff` that failed over it would be worse than one that names what it
// could not read.
func configuredPacksForInspection() ([]*packload.Pack, []unresolvedPack) {
	entries, err := config.LoadPacks(nil)
	if err != nil {
		return nil, nil
	}
	var packs []*packload.Pack
	var unresolved []unresolvedPack
	for _, e := range entries {
		p, rerr := resolveConfiguredPack(e)
		if rerr != nil {
			unresolved = append(unresolved, newUnresolvedPack(e.Name, rerr))
			continue
		}
		packs = append(packs, p)
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
