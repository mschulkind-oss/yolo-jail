package cli

// configls.go implements `yolo config ls` — the one-screen answer to "how is this
// file constructed, and is anything hidden winning over it?"
//
// It exists because the composition engine grew enough moving parts (five layers,
// four host_files modes, per-surface codecs, an optional capture overlay) that
// file construction stopped being answerable by reading docs. More pointedly: the
// §5 capture overlay outranks every layer below it PERMANENTLY and had no user-facing
// view at all, so a surface could silently diverge from what its layers would
// produce with the divergence recorded only in a sidecar the user has never heard
// of (docs/reference/composed-file-permissions.md §5). This is the missing half of a
// mechanism already in production, not new-feature polish.
//
// ⚠ THE CEILING IS PART OF THE FACT, and this file said "outranks the host layer"
// — here and in the ⚠ footer below — until 2026-09-14. The fold is ascending
// `defaults < host < workspace < config-overlay:<pack> < overlay (capture) < computed
// < managed` (internal/agentcfg/compose.go builds `preLayers` in that order, then
// enforceManaged applies the floor), so a capture outranks `defaults`, `host`, a
// host_files `content` layer (same slot as `host`), `workspace` and every
// `config-overlay`, and LOSES to `computed` and `managed`. Naming the host layer alone
// was wrong twice over for mise/config — the surface the launch banner flags most
// often — which declares NO host layer at all and whose only yolo-owned layer is the
// `computed` [tools] table that BEATS the capture (internal/agentcfg/builtin.go,
// miseConfig + CoreComputedSurfaces). Which layers a listed surface actually has is
// the LAYERS column, which is why the footer names the two exceptions by their column
// spelling rather than restating the stack.

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// surfaceRow is one line of the listing: a surface and how it is constructed.
type surfaceRow struct {
	Surface  string // "<owner>/<name>"
	Path     string // the ~-relative destination
	Codec    string
	Mode     string   // the posture: readonly | once | copy | capture
	Layers   []string // contributing layers, lowest precedence first
	Overlay  int      // captured overlay keys (-1 = surface writes no sidecar)
	HasFile  bool     // the destination exists in the target's home
	Reserved bool     // declared in the manifest but never rendered at boot
	// Unreachable marks a surface whose destination THIS TARGET cannot resolve — §4.1's
	// "not resolvable at this notch". It is not the same fact as HasFile==false and must not
	// print as one: an absence sends the reader looking for a render, and this says the
	// render was never going to land in the home they are asking about.
	Unreachable bool
	// ListEntries counts the per-entry captures at the surface's config-list paths (adds plus
	// removes in the list-capture sidecar). Captured edits exactly as the overlay's keys are —
	// kept in their own file only so no overlay reader mistakes them for a key — so a surface
	// holding only these is as diverged as one holding keys, and must not print "–".
	ListEntries int
}

// The MODE strings `config ls` prints. They are the user-facing vocabulary, kept
// distinct from the engine's manifest.Mode* constants so the display can stay stable
// if the engine's naming changes.
const (
	// surfaceModeUnrendered marks a file yolo does not compose: the agent owns it and
	// yolo only asserts individual keys (claude/config via writeClaudeJSON).
	surfaceModeUnrendered = "unrendered"
	// surfaceModeRMW marks a read-modify-write surface (B2): yolo asserts managed keys
	// and fills defaults into an agent-owned file, preserving everything else and
	// writing no capture sidecars. Used where a secret must stay off the capture path.
	//
	// These ARE listed by `config ls` — de-composing a surface must not make it
	// invisible, which was the known regression of moving copilot/config off capture.
	surfaceModeRMW = "rmw"
)

// surfaceMode reports the posture of a builtin surface, READ FROM THE MANIFEST (D2).
//
// This replaced a hand-maintained map keyed "agent/name" — a FOURTH surface table
// that had to be kept in sync with which render helper the boot path happened to
// call, pinned only by a drift test. Reading the declared mode removes the
// possibility of that drift, and it is a prerequisite for surfaces becoming pack
// data: a data-defined surface cannot appear in a Go-side table.
func surfaceMode(s manifest.Surface) string {
	switch s.ResolvedMode() {
	case manifest.ModeComputed:
		return "copy"
	case manifest.ModeRMW:
		return surfaceModeRMW
	case manifest.ModeUnrendered:
		return surfaceModeUnrendered
	default:
		return "capture"
	}
}

// configLs implements `yolo config ls [--all]`.
//
// Only surfaces whose destination EXISTS are listed by default: the builtin manifest
// declares every agent's surfaces while a jail configures only the selected ones, so
// listing all 12 in a claude-only jail would report files that do not and will not exist.
// --all lists the whole manifest.
//
// PRESENCE IS THE TARGET'S, which is what closed the host-side row inflation
// (F2, docs/reference/config-target-resolution.md#the-config-target): a host-side `ls` in a workspace used to
// find presence unknowable, stop applying the existence filter, and print four extra rows —
// the same jail described differently depending on where the user stood, at exit 0. A
// workspace target resolves its own home host-side (configTarget.reachSurface), so the filter
// applies at every target the resolution can produce.
//
// A surface the target cannot resolve at all is listed under --all as NOT RESOLVABLE rather
// than as absent (docs/reference/config-target-resolution.md#unknown-is-not-empty): the two
// look alike and mean different things, and calling
// the second the first is what sends a reader hunting for a render that was never coming.
func configLs(t configTarget, args []string, out, errw io.Writer, color bool) (rc int) {
	// A link refused in the workspace's jail-writable state is named, and fails the listing:
	// a count or a presence read through it would describe a file the verb did not read
	// (stateRefusals).
	defer func() { rc = t.reportRefusals("ls", errw, rc, true) }()
	all := false
	for _, a := range args {
		switch {
		case isHelpToken(a):
			io.WriteString(out, configUsage+"\n")
			return 0
		case a == "--all":
			all = true
		default:
			fmt.Fprintf(errw, "yolo config ls: unknown argument %q\n\n%s\n", a, configUsage)
			return 2
		}
	}

	rows := collectSurfaceRows(t, all)
	if len(rows) == 0 {
		// Said and then CONTINUED, not returned: the provenance report below is about the
		// packs' declarations and the render's own record, so a target with no listable
		// surface can still have a key yolo wrote for a layer that no longer claims it. An
		// early return here would make that key unreportable in exactly the jail where it is
		// the only thing left to report.
		fmt.Fprintln(out, "No composed surfaces found.")
	} else {
		writeSurfaceTable(out, t, rows, color)
	}
	// PER-KEY PROVENANCE LANDS HERE, and this is where it belongs: `ls` is the verb that
	// describes how a file is CONSTRUCTED, and which layer set a key is a fact about the
	// construction ([OQ-CR7](docs/reference/config-target-resolution.md#oq-cr7)). It came out of
	// `config diff`, whose subject is the capture store alone — see configprovenance.go.
	//
	// No agent filter: `ls` lists the whole manifest, so it asks about every one. It normally
	// prints nothing — only a surface another pack contributes to through `config-overlay` or
	// `config-list`, or one carrying a key yolo wrote for a layer that has since stopped
	// claiming it, reaches the report at all.
	pr := richtext.Printer{W: out, Color: color}
	overlaid, unresolved := overlayContributionRows(t, "", "")
	if len(unresolved) > 0 {
		// A pack this command could not read might be the one contributing the key the user is
		// asking about, so an incomplete answer says so rather than reading as complete.
		pr.Printf("")
		pr.Printf("[yellow]⚠ not inspected — could not be resolved: %s. Any config-overlay "+
			"or config-list they declare is not listed below.[/yellow]", describeUnresolved(unresolved))
	}
	if len(overlaid) > 0 {
		pr.Printf("")
	}
	writeOverlayContributions(pr, overlaid)
	return 0
}

// collectSurfaceRows builds the listing: every builtin surface, then every
// user-declared host_files entry.
func collectSurfaceRows(t configTarget, all bool) []surfaceRow {
	var rows []surfaceRow
	for _, s := range surfaceManifest().Surfaces() {
		key := s.Agent + "/" + s.Name
		mode := surfaceMode(s)
		reach := t.reachSurface(s.Path)
		row := surfaceRow{
			Surface:     key,
			Path:        s.Path,
			Codec:       s.Codec,
			Mode:        mode,
			Layers:      builtinLayers(s),
			Overlay:     -1,
			HasFile:     reach == surfaceReachable,
			Reserved:    mode == surfaceModeUnrendered,
			Unreachable: reach == surfaceUnreachable,
		}
		if mode == "capture" {
			row.Overlay = overlayKeyCountAt(t.overlayFile(s.Agent, s.Name))
			row.ListEntries = listCaptureCountAt(t.listCaptureFile(s.Agent, s.Name))
		}
		if all || row.HasFile {
			rows = append(rows, row)
		}
	}
	rows = append(rows, hostFileRows(t)...)
	return rows
}

// hostFileRows lists the user's host_files entries. Read with probeSource=false:
// `config ls` is an inspection command and must never fail (or differ) because a
// host path is absent, and in-jail those paths are not in the mount namespace.
func hostFileRows(t configTarget) []surfaceRow {
	cfg, err := config.LoadConfig("", false, func(string) {})
	if err != nil {
		cfg = jsonx.NewOrderedMap()
	}
	entries, err := config.LoadHostFiles(cfg, func(string) {}, false)
	if err != nil {
		return nil
	}
	var rows []surfaceRow
	for _, e := range entries {
		reach := t.reachSurface("~/" + e.Path)
		row := surfaceRow{
			Surface:     "user/" + e.Slug(),
			Path:        "~/" + e.Path,
			Codec:       e.Codec,
			Mode:        e.Mode,
			Layers:      hostFileLayers(e),
			Overlay:     -1,
			HasFile:     reach == surfaceReachable,
			Unreachable: reach == surfaceUnreachable,
		}
		if e.IsDir {
			row.Codec = "(dir)"
		}
		if e.Mode == config.HostFileModeCapture {
			row.Overlay = overlayKeyCountAt(t.overlayFile("user", e.Slug()))
		}
		rows = append(rows, row)
	}
	return rows
}

// builtinLayers names the layers a builtin surface composes from.
//
// EVERY COLUMN IS DERIVED FROM THE SURFACE, and none is enumerated here. Two of them used
// to be: `surfaceHasHostLayer` and `surfaceHasComputedLayer` were `map[string]bool`s of
// "agent/name" living directly below this function, restating what the render knows
// structurally — the pair docs/design/host-render-target.md §3.4 promised would die with
// the Target abstraction and that outlived it. A map keyed on identity cannot answer for a
// surface it has never heard of, so adding a surface silently made this print a layer
// stack the jail does not compose; measured, the computed map was three surfaces behind
// the shipped packs (see surfaceHasComputedLayer, in surfaces.go, for what it got wrong).
//
// The two replacements read the real producers: Surface.HasHostLayer is the surface's own
// HostSource, which is the SAME predicate the boot render's host-layer read consults
// (entrypoint.hostSurfaceBytes), and surfaceHasComputedLayer runs the packs' derive
// registrations.
func builtinLayers(s manifest.Surface) []string {
	var layers []string
	if s.Defaults != nil {
		layers = append(layers, "defaults")
	}
	if s.HasHostLayer() {
		layers = append(layers, "host")
	}
	if surfaceHasComputedLayer(s) {
		layers = append(layers, "computed")
	}
	if s.Managed != nil {
		layers = append(layers, "managed")
	}
	return layers
}

// hostFileLayers names the layers a host_files entry composes from.
func hostFileLayers(e config.HostFileEntry) []string {
	var layers []string
	if e.Defaults != nil {
		layers = append(layers, "defaults")
	}
	switch {
	case e.SourceBearing():
		layers = append(layers, "host")
	case e.HasContent:
		layers = append(layers, "content")
	}
	if e.Managed != nil {
		layers = append(layers, "managed")
	}
	return layers
}

// overlayKeyCount is `yolo apply --sealed`'s reader: how many captured keys the CWD's
// workspace store holds for one surface. t is sealedConfigTarget's, and a link it refused is
// recorded in t's refusals for the verb to name; the count reads it as 0.
//
// ⚠ IT IS DELIBERATELY NOT THE CONFIG TARGET'S. See workspaceRoot, one file over: whether
// `applySealed` takes the resolved target is Blocker 3 of
// docs/design/config-target-resolution-plan.md and is unruled, so this keeps the bare-cwd
// answer that verb has always been given. Every `yolo config` verb reads
// overlayKeyCountAt(configTarget.overlayFile(...)) instead.
func overlayKeyCount(t configTarget, agent, name string) int {
	return overlayKeyCountAt(t.overlayFile(agent, name))
}

// overlayKeyCountAt is overlayKeyCount over an explicit sidecar — what a resolved target
// needs, since its store is the target's answer rather than the cwd's, and a jail's store is
// read beneath a root (captureFile).
func overlayKeyCountAt(f captureFile) int {
	data, err := f.read()
	if err != nil {
		return 0
	}
	v, err := jsonx.Decode(data)
	if err != nil || v == nil {
		return 0
	}
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		return t.Len()
	case []any:
		if len(t) == 0 {
			return 0
		}
		return 1
	case string:
		if t == "" {
			return 0
		}
		return 1
	default:
		return 1
	}
}

// writeSurfaceTable renders the listing plus the divergence footer.
func writeSurfaceTable(out io.Writer, t configTarget, rows []surfaceRow, color bool) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Surface < rows[j].Surface })

	widths := []int{len("SURFACE"), len("PATH"), len("CODEC"), len("MODE"), len("LAYERS")}
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		row := []string{r.Surface, r.Path, r.Codec, r.Mode, strings.Join(r.Layers, " ")}
		if row[4] == "" {
			row[4] = "-"
		}
		if row[2] == "" {
			row[2] = "-"
		}
		for i, c := range row {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
		cells = append(cells, row)
	}

	pr := richtext.Printer{W: out, Color: color}
	pad := func(s string, w int) string { return s + strings.Repeat(" ", w-len(s)) }
	pr.Printf("[bold]%s  %s  %s  %s  %s  %s[/bold]",
		pad("SURFACE", widths[0]), pad("PATH", widths[1]), pad("CODEC", widths[2]),
		pad("MODE", widths[3]), pad("LAYERS", widths[4]), "OVERLAY")

	diverged := 0
	for i, r := range rows {
		c := cells[i]
		overlay := "–"
		switch {
		case r.Reserved:
			overlay = "[dim](not rendered at boot)[/dim]"
		case r.Overlay > 0 || r.ListEntries > 0:
			diverged++
			overlay = fmt.Sprintf("[yellow]%s ⚠[/yellow]", capturedCount(r.Overlay, r.ListEntries))
		}
		missing := ""
		switch {
		case r.Unreachable:
			missing = " [dim](" + t.notResolvableHere() + ")[/dim]"
		case !r.HasFile:
			missing = " [dim](absent)[/dim]"
		}
		pr.Printf("%s  %s  %s  %s  %s  %s%s",
			pad(c[0], widths[0]), pad(c[1], widths[1]), pad(c[2], widths[2]),
			pad(c[3], widths[3]), pad(c[4], widths[4]), overlay, missing)
	}

	if diverged > 0 {
		pr.Printf("")
		pr.Printf("[yellow]⚠ %d %s captured in-jail edits that outrank every layer but `computed` and `managed`.[/yellow]",
			diverged, plural(diverged, "surface has", "surfaces have"))
		pr.Printf("  Inspect: [cyan]yolo config diff <agent>/<surface>[/cyan]")
		pr.Printf("  Discard: [cyan]yolo config reset <agent>/<surface>[/cyan]")
	}
}

// capturedCount spells a surface's captured edits for the OVERLAY column: keys, per-entry
// list captures, or both — the column's one number would otherwise count list entries as
// keys, and `yolo config diff` itemizes them as entries.
func capturedCount(keys, entries int) string {
	var parts []string
	if keys > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", keys, plural(keys, "key", "keys")))
	}
	if entries > 0 {
		parts = append(parts, fmt.Sprintf("%d list %s", entries, plural(entries, "entry", "entries")))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
