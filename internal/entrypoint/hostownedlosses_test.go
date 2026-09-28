package entrypoint

// hostownedlosses_test.go pins HC-D5 (docs/design/host-computed-layer.md §7): under
// `host_management: own` the host apply's loss report names an entry as dropped or replaced
// only when the `stateful` write actually drops or replaces it.
//
// The report used to be tableLosses for both contracts, which models the `assert` write: a
// table declared in full is regenerated wholesale, so every entry config does not declare
// goes. The `own` write is a different mechanism. Its adoption claims an in-full table only
// when this render put an entry in it (agentcfg's dropComputedTables), so with no MCP server
// configured the hand-added entry is ADOPTED and stays. Measured by the design's scratch test:
// a first owned apply kept a hand-added codex `mcp_servers` entry and an opencode `mcp` entry
// while the report named both as dropped, which is the confirmation prompt asking to approve
// a loss that never happens.
//
// The assertion is the invariant, not a list of cases: every entry the report names is one
// the write changed, and every entry the write changed is one the report names. Both
// directions run over the file before and after a real --assert.

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

// ownedLossCase is one agent surface whose file holds a named-entry table.
type ownedLossCase struct {
	pack, surface, table, rel, seed string
}

var ownedLossCases = []ownedLossCase{
	{pack: "codex", surface: "codex/config", table: "mcp_servers", rel: ".codex/config.toml",
		seed: "model = \"gpt-5\"\n\n[mcp_servers.mine]\ncommand = \"/bin/mine\"\n\n" +
			"[mcp_servers.tavily]\ncommand = \"/bin/my-tavily\"\n"},
	{pack: "opencode", surface: "opencode/config", table: "mcp", rel: ".config/opencode/opencode.json",
		seed: `{"theme":"x","mcp":{"mine":{"type":"local","command":["/bin/mine"]},` +
			`"tavily":{"type":"local","command":["/bin/my-tavily"]}}}`},
}

// mcpContributor is a pack contributing the given entries to one surface's table through a
// config-overlay — the only declaration that reaches a host MCP table today (HC-D2).
func mcpContributor(t *testing.T, surface, table string, entries map[string]any) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"managed": map[string]any{table: entries}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "my-mcp", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: surface, Raw: raw},
		},
	}}
}

// tableEntries decodes the named entries of one table from the surface file at path, each as
// canonical JSON so two decodes of one value compare equal.
func tableEntries(t *testing.T, s manifest.Surface, path, table string) map[string]string {
	t.Helper()
	obj := existingSurfaceObject(s, path)
	v, _ := obj.Get(table)
	out := map[string]string{}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return out
	}
	for _, name := range m.Keys() {
		entry, _ := m.Get(name)
		b, err := json.Marshal(jsonx.Plain(entry))
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(b)
	}
	return out
}

// requireLossesDescribeTheWrite is HC-D5's invariant over one surface: the loss lines name
// exactly the entries the --assert removed or changed, and the kind of loss each suffered.
func requireLossesDescribeTheWrite(t *testing.T, c ownedLossCase, before, after map[string]string,
	losses []string) {
	t.Helper()
	var want []string
	for name, prev := range before {
		now, kept := after[name]
		switch {
		case !kept:
			want = append(want, c.table+"."+name+" (dropped")
		case now != prev:
			want = append(want, c.table+"."+name+" (replaced")
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
		t.Errorf("%s: the loss report names %q, but the owned write %s\nbefore: %v\nafter:  %v",
			c.surface, losses, describeWant(want), before, after)
	}
}

func describeWant(want []string) string {
	if len(want) == 0 {
		return "dropped and replaced nothing"
	}
	return "did " + strings.Join(want, ", ")
}

// runOwnedApply renders the case's pack under `own` into home, observing first and then
// asserting, and returns the observed losses with the table before and after the write.
func runOwnedApply(t *testing.T, c ownedLossCase, home string, extra *packload.Pack) (
	observed, asserted []string, before, after map[string]string) {
	t.Helper()
	p, err := embeddedPack(c.pack)
	if err != nil {
		t.Fatal(err)
	}
	set := []*packload.Pack{p}
	if extra != nil {
		set = append(set, extra)
	}
	overlays := packoverlay.Collect(set, false, nil)
	path := filepath.Join(home, filepath.FromSlash(c.rel))
	s := surfaceNamed(t, p, c.surface)
	before = tableEntries(t, s, path, c.table)

	obs, err := RenderHostPack(p, home, render.OwnershipOwn, true, overlays, nil)
	if err != nil {
		t.Fatalf("observe RenderHostPack(%s): %v", c.pack, err)
	}
	observed = resultFor(t, obs, c.surface).EntryLosses
	res, err := RenderHostPack(p, home, render.OwnershipOwn, false, overlays, nil)
	if err != nil {
		t.Fatalf("assert RenderHostPack(%s): %v", c.pack, err)
	}
	r := resultFor(t, res, c.surface)
	if strings.HasPrefix(r.Action, "refused") {
		t.Fatalf("%s refused under own: %s", c.surface, r.Action)
	}
	asserted = r.EntryLosses
	after = tableEntries(t, s, path, c.table)
	return observed, asserted, before, after
}

// surfaceNamed returns the pack's base surface with the given agent/name identity.
func surfaceNamed(t *testing.T, p *packload.Pack, id string) manifest.Surface {
	t.Helper()
	surfaces, _ := p.SurfacesFor(false)
	for _, s := range surfaces {
		if s.Agent+"/"+s.Name == id {
			return s
		}
	}
	t.Fatalf("pack %s declares no %s", p.Name, id)
	return manifest.Surface{}
}

func seedOwnedHome(t *testing.T, c ownedLossCase) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, filepath.FromSlash(c.rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(c.seed), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

// THE MEASURED CASE: no MCP server configured anywhere, so the owned write adopts the user's
// entries and the report must name none of them.
func TestAnOwnedApplyWithNoServerConfiguredReportsNoEntryLoss(t *testing.T) {
	for _, c := range ownedLossCases {
		t.Run(c.surface, func(t *testing.T) {
			home := seedOwnedHome(t, c)
			observed, asserted, before, after := runOwnedApply(t, c, home, nil)
			if len(after) != len(before) {
				t.Fatalf("fixture premise: the owned write was expected to keep every entry "+
					"here (an empty in-full table claims none); before %v after %v", before, after)
			}
			requireLossesDescribeTheWrite(t, c, before, after, observed)
			requireLossesDescribeTheWrite(t, c, before, after, asserted)
		})
	}
}

// A CONFIGURED SERVER makes the table yolo's at adoption, so the owned write does drop the
// user's other entry and replace the one config also declares — and the report must still
// name exactly those. The half that keeps the fix from reporting nothing at all.
func TestAnOwnedApplyWithAServerConfiguredReportsWhatItDrops(t *testing.T) {
	for _, c := range ownedLossCases {
		t.Run(c.surface, func(t *testing.T) {
			home := seedOwnedHome(t, c)
			var tavily any = map[string]any{"command": "/bin/tavily"}
			if c.pack == "opencode" {
				tavily = map[string]any{"type": "local", "command": []any{"/bin/tavily"}}
			}
			contributor := mcpContributor(t, c.surface, c.table, map[string]any{"tavily": tavily})
			observed, asserted, before, after := runOwnedApply(t, c, home, contributor)
			if _, kept := after["mine"]; kept {
				t.Fatalf("fixture premise: with a configured server the owned adoption was "+
					"expected to drop `mine`; after %v", after)
			}
			requireLossesDescribeTheWrite(t, c, before, after, observed)
			requireLossesDescribeTheWrite(t, c, before, after, asserted)
			if len(observed) == 0 {
				t.Errorf("the dry run reported no loss for a write that dropped `mine` and "+
					"replaced `tavily`: %v", observed)
			}
		})
	}
}

// STEADY STATE: once the home is owned, an entry the user adds by hand is captured by the
// owned write, and the next apply's report must agree with what that write does to it.
func TestASteadyStateOwnedApplyReportsWhatItDrops(t *testing.T) {
	for _, c := range ownedLossCases {
		t.Run(c.surface, func(t *testing.T) {
			home := seedOwnedHome(t, c)
			runOwnedApply(t, c, home, nil)
			path := filepath.Join(home, filepath.FromSlash(c.rel))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var edited string
			if c.pack == "codex" {
				edited = string(raw) + "\n[mcp_servers.later]\ncommand = \"/bin/later\"\n"
			} else {
				var doc map[string]any
				if err := json.Unmarshal(raw, &doc); err != nil {
					t.Fatalf("fixture: %v\n%s", err, raw)
				}
				mcp, _ := doc["mcp"].(map[string]any)
				if mcp == nil {
					mcp = map[string]any{}
				}
				mcp["later"] = map[string]any{"type": "local", "command": []any{"/bin/later"}}
				doc["mcp"] = mcp
				b, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				edited = string(b)
			}
			if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
				t.Fatal(err)
			}
			observed, asserted, before, after := runOwnedApply(t, c, home, nil)
			requireLossesDescribeTheWrite(t, c, before, after, observed)
			requireLossesDescribeTheWrite(t, c, before, after, asserted)
		})
	}
}
