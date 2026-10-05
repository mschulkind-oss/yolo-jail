package entrypoint

// hosttablelosses_test.go pins the host apply's per-entry loss report against what the write
// does, over EVERY shipped surface that has a yolo-owned table at the host (hostTableKeys),
// through each mechanism an owned host renders it with (hostTableRuns).
//
// It generalizes hostownedlosses_test.go (HC-D5), whose seeds held two entries per table. Two
// is exactly the size a wholesale table write could not get wrong: regenerateManagedTables
// cleared the table by ranging over dest.Keys() while deleting from it, and Keys() is the
// map's own slice, which Delete shifts in place. With three or more entries the loop stepped
// over every other one, so a table of a, b, c kept b — while the report, computed from the
// file and the declaration, named all three dropped. Hence four entries here, and a fifth
// config also declares.
//
// The assertion is the same invariant as HC-D5's: every entry the report names is one the
// write changed, and every entry the write changed is one the report names, the kind of loss
// included. Both the dry run and the --assert are held to it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// hostTableSurface is one shipped surface with at least one yolo-owned table at the host.
type hostTableSurface struct {
	pack    *packload.Pack
	surface manifest.Surface
	tables  []string
}

// shippedHostTableSurfaces walks every embedded pack for the surfaces whose host render
// writes a table wholesale. A surface outside the home has no host path to seed, so it is
// left out, and the walk fails if it finds nothing — an empty walk would pass every check.
func shippedHostTableSurfaces(t *testing.T) []hostTableSurface {
	t.Helper()
	var out []hostTableSurface
	for _, name := range EmbeddedPackNames() {
		p, err := embeddedPack(name)
		if err != nil {
			t.Fatalf("embedded %s: %v", name, err)
		}
		surfaces, _ := p.SurfacesFor(false)
		for _, s := range surfaces {
			tables := hostTableKeys(p, s)
			if len(tables) == 0 || !strings.HasPrefix(s.Path, "~/") {
				continue
			}
			out = append(out, hostTableSurface{pack: p, surface: s, tables: tables})
		}
	}
	if len(out) == 0 {
		t.Fatal("no shipped surface has a host table — the walk measures nothing")
	}
	return out
}

// seededTableEntries is how many hand-added entries each table holds before the apply. Three
// is the smallest size the skipped-delete defect showed at; four leaves one kept and two
// dropped under it, so both halves of the invariant are exercised.
var seededTableEntries = []string{"alpha", "bravo", "charlie", "delta"}

// seedHostTables writes the surface's file into home with every table holding the seeded
// entries, through the surface's own codec, and returns the path.
func seedHostTables(t *testing.T, c hostTableSurface, home string) string {
	t.Helper()
	path := filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(c.surface.Path, "~/")))
	obj := jsonx.NewOrderedMap()
	obj.Set("handEdited", "keep")
	for _, table := range c.tables {
		m := jsonx.NewOrderedMap()
		for _, name := range seededTableEntries {
			entry := jsonx.NewOrderedMap()
			entry.Set("command", "/bin/"+name)
			m.Set(name, entry)
		}
		obj.Set(table, m)
	}
	text, err := encodeSurfaceObject(c.surface, obj, nil, nil)
	if err != nil {
		t.Fatalf("seed %s: %v", c.surface.Path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// hostTableState is every entry of every table in the file, keyed "table.name", each as
// canonical JSON so two decodes of one value compare equal.
func hostTableState(t *testing.T, s manifest.Surface, path string, tables []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range tables {
		for name, v := range tableEntries(t, s, path, table) {
			out[table+"."+name] = v
		}
	}
	return out
}

// requireReportMatchesWrite is the invariant over every table of one surface.
func requireReportMatchesWrite(t *testing.T, label string, before, after map[string]string, losses []string) {
	t.Helper()
	var want []string
	for key, prev := range before {
		now, kept := after[key]
		switch {
		case !kept:
			want = append(want, key+" (dropped")
		case now != prev:
			want = append(want, key+" (replaced")
		}
	}
	sort.Strings(want)
	var got []string
	for _, l := range losses {
		if i := strings.Index(l, " — "); i >= 0 {
			l = l[:i]
		}
		got = append(got, l)
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s: the loss report names %q, but the write %s\nbefore: %v\nafter:  %v",
			label, losses, describeWant(want), before, after)
	}
}

// tableContributor declares one entry in every table of the surface through a config-overlay,
// with a value different from the seed's, so the write both REPLACES it and drops the rest.
func tableContributor(t *testing.T, c hostTableSurface, entry string) *packload.Pack {
	t.Helper()
	managed := map[string]any{}
	for _, table := range c.tables {
		var value any = map[string]any{"command": "/bin/configured"}
		if c.surface.Agent == "opencode" {
			value = map[string]any{"type": "local", "command": []any{"/bin/configured"}}
		}
		managed[table] = map[string]any{entry: value}
	}
	raw, err := json.Marshal(map[string]any{"managed": managed})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "my-tables", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfigOverlay,
			Surface: c.surface.Agent + "/" + c.surface.Name, Raw: raw}},
	}}
}

// runHostTableApply observes and then asserts one surface's pack into an owned home,
// returning the losses each reported and the tables before and after the write.
func runHostTableApply(t *testing.T, c hostTableSurface, home string,
	extra *packload.Pack) (observed, asserted []string, before, after map[string]string) {
	t.Helper()
	path := seedHostTables(t, c, home)
	set := []*packload.Pack{c.pack}
	if extra != nil {
		set = append(set, extra)
	}
	overlays := packoverlay.Collect(set, false, nil)
	id := c.surface.Agent + "/" + c.surface.Name
	before = hostTableState(t, c.surface, path, c.tables)

	obs, err := RenderHostPack(c.pack, home, render.OwnershipOwn, true, overlays, nil)
	if err != nil {
		t.Fatalf("observe RenderHostPack(%s): %v", c.pack.Name, err)
	}
	observed = resultFor(t, obs, id).EntryLosses
	res, err := RenderHostPack(c.pack, home, render.OwnershipOwn, false, overlays, nil)
	if err != nil {
		t.Fatalf("assert RenderHostPack(%s): %v", c.pack.Name, err)
	}
	r := resultFor(t, res, id)
	if strings.HasPrefix(r.Action, "refused") {
		t.Fatalf("%s refused: %s", id, r.Action)
	}
	asserted = r.EntryLosses
	after = hostTableState(t, c.surface, path, c.tables)
	return observed, asserted, before, after
}

// hostTableRuns is every (surface, mechanism) pair an owned host renders: each surface as
// shipped, and again re-declared `rmw` (declaredRMW) where that is a different mechanism — the
// rmw arm's wholesale table write, regenerateManagedTables, is where the skipped-delete defect
// lived, and every surface took it under the retired `assert` (OQ-CO14). A declaration the
// census runs no mechanism for is left out: that refusal writes nothing and reports nothing,
// and render's own tests pin it.
type hostTableRun struct {
	hostTableSurface
	label string
}

func hostTableRuns(t *testing.T) []hostTableRun {
	t.Helper()
	modes := render.Host(t.TempDir(), nil, render.OwnershipOwn).Modes()
	var out []hostTableRun
	for _, c := range shippedHostTableSurfaces(t) {
		seen := map[string]bool{}
		for _, m := range hostMechanisms {
			run := c
			if m.rmw {
				run.pack = declaredRMW(t, []*packload.Pack{c.pack}, c.pack.Name, true)[0]
				run.surface.Mode = manifest.ModeRMW
			}
			mechanism, runs := modes.Mechanism(run.surface.ResolvedMode())
			if !runs || seen[mechanism] {
				continue
			}
			seen[mechanism] = true
			out = append(out, hostTableRun{hostTableSurface: run,
				label: c.surface.Agent + "/" + c.surface.Name + "/" + mechanism})
		}
	}
	return out
}

// configuredTableEntry is the seeded entry config also declares. The LAST seed on purpose: the
// skipped-delete loop over a, b, c, d kept b, and a configured b is rewritten over whatever
// survived, so configuring it would have hidden the defect in every configured run.
const configuredTableEntry = "delta"

// NOTHING CONFIGURED: the report must name exactly the entries the write removes.
func TestEveryHostTableLossReportMatchesTheWriteWithNothingConfigured(t *testing.T) {
	for _, run := range hostTableRuns(t) {
		t.Run(run.label, func(t *testing.T) {
			observed, asserted, before, after := runHostTableApply(t, run.hostTableSurface,
				t.TempDir(), nil)
			requireReportMatchesWrite(t, run.label+" dry run", before, after, observed)
			requireReportMatchesWrite(t, run.label+" --assert", before, after, asserted)
		})
	}
}

// ONE ENTRY CONFIGURED in every table, differently from the seed: the write replaces it and
// drops the others, and the report must say exactly that.
func TestEveryHostTableLossReportMatchesTheWriteWithAnEntryConfigured(t *testing.T) {
	for _, run := range hostTableRuns(t) {
		t.Run(run.label, func(t *testing.T) {
			contributor := tableContributor(t, run.hostTableSurface, configuredTableEntry)
			observed, asserted, before, after := runHostTableApply(t, run.hostTableSurface,
				t.TempDir(), contributor)
			requireReportMatchesWrite(t, run.label+" dry run", before, after, observed)
			requireReportMatchesWrite(t, run.label+" --assert", before, after, asserted)
		})
	}
}

// THE DEFECT ITSELF, at the write: with one entry configured, every table is yolo's and holds
// exactly that entry afterwards. Asserted on the file alone, so it holds whatever the report
// says — the half that fails if the table clear skips an entry.
func TestAHostTableWriteKeepsOnlyTheConfiguredEntry(t *testing.T) {
	for _, run := range hostTableRuns(t) {
		t.Run(run.label, func(t *testing.T) {
			contributor := tableContributor(t, run.hostTableSurface, configuredTableEntry)
			_, _, before, after := runHostTableApply(t, run.hostTableSurface, t.TempDir(),
				contributor)
			if len(before) != len(run.tables)*len(seededTableEntries) {
				t.Fatalf("fixture premise: the seed was not read back whole: %v", before)
			}
			var want, got []string
			for _, table := range run.tables {
				want = append(want, table+"."+configuredTableEntry)
			}
			for key := range after {
				got = append(got, key)
			}
			sort.Strings(want)
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("%s: after the write the tables hold %v, want only %v — every "+
					"hand-added entry of a yolo-owned table goes", run.label, got, want)
			}
		})
	}
}
