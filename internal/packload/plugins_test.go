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
	decl := `{"name":"` + name + `","contributes":[` +
		`{"kind":"skills","from":"skills","into":".claude/skills","tier":"namespaced"}]}`
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
	p, problems := LoadDir(root, "wrapper", true)
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
	p, _ := LoadDir(root, "wrapper", true)
	for _, c := range FootprintOf(p).Claims {
		if c.Target == "plugin:acme-tools" && c.ReviewWorthy {
			t.Errorf("a skills-only plugin must not be review-worthy: %+v", c)
		}
	}
	if claims := p.PluginHostAccessClaims(); len(claims) != 0 {
		t.Errorf("PluginHostAccessClaims() = %v, want none for a skills-only plugin", claims)
	}
}

// The ORIGIN GATE: a FETCHED pack (MayAccessHost=false) has its plugin's code-running components
// refused BY NAME, while the plugin's skills still come along. This is the specific hole
// plugin-as-pack could have opened — a fetched tree running code with no approval.
func TestFetchedPackPluginCodeIsRefusedByName(t *testing.T) {
	root := writeWrapperPack(t, "wrapper", "acme-tools",
		`{"name":"acme-tools","skills":["./"],"hooks":{"PreToolUse":[]},
		  "mcpServers":{"db":{"command":"x"}},"agents":["./agents"]}`)

	// mayAccessHost=false is what a fetched, unapproved pack gets.
	fetched, _ := LoadDir(root, "wrapper", false)
	granted, refused := fetched.HonoredPlugins()
	if len(refused) != 2 {
		t.Fatalf("both code-running components must be refused by name, got %v", refused)
	}
	joined := strings.Join(refused, "\n")
	for _, want := range []string{"hooks", "mcpServers", "acme-tools", "FETCHED"} {
		if !strings.Contains(joined, want) {
			t.Errorf("refusal must name %q so the user knows what was withheld:\n%s", want, joined)
		}
	}
	// `agents` is content and is NOT refused — the gate is about code, not about file count.
	if strings.Contains(joined, "agents") {
		t.Errorf("content-only components must not be refused:\n%s", joined)
	}
	// The plugin is still delivered: its skills are the reason the user wrapped it.
	if len(granted) != 1 {
		t.Errorf("the plugin's skills must still be delivered when its code is refused, got %d",
			len(granted))
	}

	// An APPROVED (or embedded/local) pack gets everything, with nothing refused.
	approved, _ := LoadDir(root, "wrapper", true)
	if _, refused := approved.HonoredPlugins(); len(refused) != 0 {
		t.Errorf("an origin-permitted pack must not have its plugin refused: %v", refused)
	}
}

// The approval strings a plugin contributes are SPECIFIC and stable, so a pin that later gains a
// hook re-prompts while an unchanged one does not.
func TestPluginHostAccessClaimsAreSpecific(t *testing.T) {
	root := writeWrapperPack(t, "wrapper", "acme-tools",
		`{"name":"acme-tools","hooks":{"PreToolUse":[]}}`)
	p, _ := LoadDir(root, "wrapper", true)
	claims := p.PluginHostAccessClaims()
	if len(claims) != 1 {
		t.Fatalf("claims = %v, want one per code-running component", claims)
	}
	if !strings.Contains(claims[0], "acme-tools") || !strings.Contains(claims[0], "hooks") {
		t.Errorf("claim %q must name both the plugin and the component, or approving it "+
			"approves something the user cannot identify", claims[0])
	}
	// Stable across reads: the lockfile compares these as strings.
	if again := p.PluginHostAccessClaims(); again[0] != claims[0] {
		t.Errorf("claims must be stable: %q vs %q", claims[0], again[0])
	}
}

// Two packs wrapping same-named plugins is a COLLISION, reported before an apply silently lets
// the later one win. The generic exclusive-target check cannot catch this: the claim's kind is
// skills, which merges by design.
func TestTwoPacksWrappingOnePluginNameCollide(t *testing.T) {
	a := writeWrapperPack(t, "packa", "acme-tools", `{"name":"acme-tools"}`)
	b := writeWrapperPack(t, "packb", "acme-tools", `{"name":"acme-tools"}`)
	pa, _ := LoadDir(a, "packa", true)
	pb, _ := LoadDir(b, "packb", true)

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
	p, _ := LoadDir(root, "plain", true)
	if pls := p.Plugins(); len(pls) != 0 {
		t.Errorf("a plain pack must carry no plugins, got %d", len(pls))
	}
	for _, c := range FootprintOf(p).Claims {
		if strings.HasPrefix(c.Target, "plugin:") {
			t.Errorf("a plain pack must make no plugin claim: %+v", c)
		}
	}
}
