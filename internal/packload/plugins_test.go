package packload

// plugins_test.go pins the TRUST GATE over a wrapped plugin. The specific hole it guards: a
// plugin manifest can declare hooks and MCP servers, which are code that runs, and none of that
// appears in pack.json. So a footprint or an approval computed from the contributions alone
// would let a fetched tree arrive with code to run and nothing to approve.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// writeWrapperPack builds a pack whose skills/ dir carries a plugin with the given manifest.
func writeWrapperPack(t *testing.T, name, pluginName, pluginManifest string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	md := filepath.Join(root, "skills", pluginName, ".claude-plugin")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(md, "plugin.json"),
		[]byte(pluginManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	decl := `{"name":"` + name + `","skills_tier":"namespaced","contributes":[` +
		`{"kind":"skills","from":"skills","into":".claude/skills"}]}`
	if err := os.WriteFile(filepath.Join(root, packdecl.ManifestName),
		[]byte(decl), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// A plugin's code-running components appear in the FOOTPRINT, flagged for review. Without this
// a `yolo pack footprint` on a plugin wrapper says only "skills" while the pack starts MCP
// servers — the footprint would be actively misleading, which is worse than incomplete.
func TestFootprintSurfacesPluginHooksAndServers(t *testing.T) {
	root := writeWrapperPack(t, "wrapper", "acme-tools",
		`{"name":"acme-tools","hooks":{"PreToolUse":[]},"mcpServers":{"db":{"command":"x"}}}`)
	p, problems := LoadDir(root, "wrapper")
	if len(problems) > 0 {
		t.Fatalf("unexpected manifest problems: %v", problems)
	}

	var claim *Claim
	for i, c := range FootprintOf(p).Claims {
		if c.Target == "plugin:acme-tools" {
			claim = &FootprintOf(p).Claims[i]
			break
		}
	}
	if claim == nil {
		t.Fatalf("a wrapped plugin makes no footprint claim — its hooks and MCP servers would "+
			"be invisible to `pack footprint`. Claims: %+v", FootprintOf(p).Claims)
	}
	if !claim.ReviewWorthy {
		t.Error("a plugin declaring hooks/MCP servers must be flagged for review — it runs code")
	}
	for _, want := range []string{"hooks", "mcpServers", "RUNS CODE"} {
		if !strings.Contains(claim.Detail, want) {
			t.Errorf("footprint detail %q must name %q", claim.Detail, want)
		}
	}
	// And the review summary picks it up, which is the line a user actually reads.
	if rw := ReviewWorthy([]*Pack{p}); len(rw) == 0 {
		t.Error("a code-running plugin must appear in the review-worthy set")
	}
}

// A skills-only plugin is content, so it is NOT flagged. A gate that fires on everything is a
// gate nobody reads.
func TestSkillsOnlyPluginIsNotReviewWorthy(t *testing.T) {
	root := writeWrapperPack(t, "wrapper", "acme-tools", `{"name":"acme-tools","skills":["./"]}`)
	p, _ := LoadDir(root, "wrapper")
	for _, c := range FootprintOf(p).Claims {
		if c.Target == "plugin:acme-tools" && c.ReviewWorthy {
			t.Errorf("a skills-only plugin must not be review-worthy: %+v", c)
		}
	}
}

// A WRAPPED PLUGIN IS DELIVERED WHOLE, hooks and servers included, whoever shipped the pack.
//
// TWO TESTS USED TO LIVE HERE. TestFetchedPackPluginCodeIsRefusedByName pinned the origin
// gate: a fetched pack's code-running components were stripped and named in a refusal while
// its skills came along. TestPluginHostAccessClaimsAreSpecific pinned the approval strings
// those components contributed to the install prompt and the lockfile. OQ-TP9
// (docs/design/trust-paths.md, 2026-09-04) deleted the gate, the prompt and the lockfile
// record, so both are replaced by this — the assertion that goes red if any of it returns.
//
// WHAT THE RULING KEPT is the DISCLOSURE, and it is asserted here beside the delivery,
// because delivering the hook while dropping its footprint line would be strictly worse than
// the gate: the pack would run code on a lifecycle event with nothing anywhere saying so.
func TestWrappedPluginCodeIsDeliveredAndDisclosed(t *testing.T) {
	root := writeWrapperPack(t, "wrapper", "acme-tools",
		`{"name":"acme-tools","skills":["./"],"hooks":{"PreToolUse":[]},
		  "mcpServers":{"db":{"command":"x"}},"agents":["./agents"]}`)

	p, probs := LoadDir(root, "wrapper")
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}
	granted, refused := p.HonoredPlugins()
	if len(refused) != 0 {
		t.Errorf("a wrapped plugin's components were REFUSED: %v\nThe origin gate is deleted; "+
			"a refusal here is a gate that came back without a ruling", refused)
	}
	if len(granted) != 1 {
		t.Fatalf("the plugin was not delivered, got %d", len(granted))
	}
	if !granted[0].RunsCode() {
		t.Error("the delivered plugin reports RunsCode()=false — its hooks and mcpServers " +
			"were stripped somewhere between the manifest and here")
	}

	// The disclosure the gate was traded for.
	var claim *Claim
	for i, c := range FootprintOf(p).Claims {
		if c.Target == "plugin:acme-tools" {
			claim = &FootprintOf(p).Claims[i]
			_ = i
		}
	}
	if claim == nil {
		t.Fatal("the wrapped plugin has no footprint claim at all — since OQ-TP9 that report " +
			"is the only place a user learns this pack ships code that runs")
	}
	if !claim.ReviewWorthy {
		t.Errorf("the plugin claim is not ReviewWorthy, so it never reaches the launch "+
			"banner: %+v", *claim)
	}
	if !strings.Contains(claim.Detail, "hooks") || !strings.Contains(claim.Detail, "mcpServers") {
		t.Errorf("the claim does not name the code-running components, so a reader cannot "+
			"tell what runs: %q", claim.Detail)
	}
}

// Two packs wrapping same-named plugins is a COLLISION, reported before an apply silently lets
// the later one win. The generic exclusive-target check cannot catch this: the claim's kind is
// skills, which merges by design.
func TestTwoPacksWrappingOnePluginNameCollide(t *testing.T) {
	a := writeWrapperPack(t, "packa", "acme-tools", `{"name":"acme-tools"}`)
	b := writeWrapperPack(t, "packb", "acme-tools", `{"name":"acme-tools"}`)
	pa, _ := LoadDir(a, "packa")
	pb, _ := LoadDir(b, "packb")

	cols := Collisions([]*Pack{pa, pb})
	var found *Collision
	for i, c := range cols {
		if c.Target == "plugin:acme-tools" {
			found = &cols[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("two packs wrapping one plugin name must collide: %+v", cols)
	}
	if len(found.Packs) != 2 {
		t.Errorf("the collision must name both packs: %v", found.Packs)
	}

	// One pack wrapping one plugin is NOT a collision (the same pack's own claim must not
	// collide with itself).
	if cols := Collisions([]*Pack{pa}); len(cols) != 0 {
		t.Errorf("a single wrapper must not self-collide: %+v", cols)
	}
}

// A pack with no plugin is unaffected: no claim, no refusal, no collision. The shipped packs
// are all in this category, so a regression here would be visible in every footprint.
func TestPackWithoutPluginIsUnaffected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(filepath.Join(root, "skills", "a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "a-skill", "SKILL.md"),
		[]byte("---\nname: a-skill\ndescription: d\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := LoadDir(root, "plain")
	if pls := p.Plugins(); len(pls) != 0 {
		t.Errorf("a plain pack must carry no plugins, got %d", len(pls))
	}
	for _, c := range FootprintOf(p).Claims {
		if strings.HasPrefix(c.Target, "plugin:") {
			t.Errorf("a plain pack must make no plugin claim: %+v", c)
		}
	}
}
