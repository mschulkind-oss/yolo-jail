package hostskills

// plugin_names_test.go pins what a WRAPPED PLUGIN puts in front of an agent under pack naming
// (docs/reference/pack-system.md#delivered-names, #pack-name-clashes): a wrapper that did not opt in delivers the plugin's
// skills alone, named like any other skill, with its hooks and MCP servers refused as on main; a
// whole plugin's inner skills are claimed under the name each agent shows them by; and two names
// one namespace delivers alike are refused naming both.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// flatWrapper is a pack with no manifest (so the flat default) wrapping plugin kit, which carries
// skill hello, a hooks file and an MCP server.
func flatWrapper(t *testing.T, namespace string) Layer {
	t.Helper()
	root := t.TempDir()
	plug := filepath.Join(root, "skills", "kit")
	writePluginFile(t, filepath.Join(plug, ".claude-plugin", "plugin.json"), `{"name":"kit"}`)
	writePluginFile(t, filepath.Join(plug, "skills", "hello", "SKILL.md"), "---\nname: hello\n---\nHi.\n")
	writePluginFile(t, filepath.Join(plug, "hooks", "hooks.json"), `{"hooks":{}}`)
	writePluginFile(t, filepath.Join(plug, ".mcp.json"), `{"mcpServers":{"srv":{"command":"srv"}}}`)
	p, problems := packload.LoadDirForUse(root, "acme/kit")
	if p == nil {
		t.Fatal(problems)
	}
	p.Namespace = namespace
	l := PackLayer(p)
	if len(l.Plugins) != 1 || l.Tier != TierFlat {
		t.Fatalf("fixture: %d plugins at tier %q, want one at the flat default", len(l.Plugins), l.Tier)
	}
	return l
}

// M1: a wrapper that did not opt in delivers its plugin's SKILLS, under the pack-naming rules, and
// refuses the hooks and MCP servers by name. It fails if writeLayer asks for the whole tree again.
func TestAFlatWrapperDeliversItsPluginsSkillsAlone(t *testing.T) {
	for _, tc := range []struct{ namespace, want string }{{"acme", "acme-hello"}, {"", "hello"}} {
		t.Run("namespace="+tc.namespace, func(t *testing.T) {
			dir := t.TempDir()
			got, err := ComposeInto(Destination{Dir: "~/.claude/skills", Agent: "claude",
				Layers: []Layer{flatWrapper(t, tc.namespace)}}, dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, tc.want, "SKILL.md")); err != nil {
				t.Errorf("the plugin's skill is not at %s: %v", tc.want, err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("the destination holds %v; want only %s (no plugin tree, no hooks, no MCP)",
					names, tc.want)
			}
			refused := map[string]bool{}
			for _, r := range got.Results {
				if r.Action == ActionRefused {
					for _, c := range []string{"hooks", "mcpServers"} {
						if strings.Contains(r.Name, c) {
							refused[c] = true
						}
					}
				}
			}
			if !refused["hooks"] || !refused["mcpServers"] {
				t.Errorf("hooks and MCP must be refused by name, as on main: %+v", got.Results)
			}
		})
	}
}

// namespacedWrapper is a layer delivering plugin kit WHOLE (its wrapper opted in), whose inner
// skill's frontmatter calls it review.
func namespacedWrapper(t *testing.T) Layer {
	t.Helper()
	root := t.TempDir()
	plug := filepath.Join(root, "skills", "kit")
	writePluginFile(t, filepath.Join(root, "pack.json"), `{"name":"kit","skills_tier":"namespaced"}`)
	writePluginFile(t, filepath.Join(plug, ".claude-plugin", "plugin.json"), `{"name":"kit"}`)
	writePluginFile(t, filepath.Join(plug, "skills", "rev", "SKILL.md"), "---\nname: \"review\"\n---\nR.\n")
	p, problems := packload.LoadDirForUse(root, "acme/kit")
	if p == nil {
		t.Fatal(problems)
	}
	p.Namespace = "acme"
	l := PackLayer(p)
	if l.Tier != TierNamespaced {
		t.Fatalf("fixture: tier %q, want namespaced", l.Tier)
	}
	return l
}

// M2: pi lists a whole plugin's inner skills by their bare frontmatter name, so an inner `review`
// clashes with another pack's `review` there; Claude Code shows it as /acme-kit:review, so it does
// not clash there.
func TestAWholePluginsInnerSkillsAreClaimedUnderTheNameEachAgentShows(t *testing.T) {
	bare := exampleLayer(t, "dotfiles/matt", "", false, "review")
	for _, tc := range []struct {
		agent   string
		collide bool
	}{{"pi", true}, {"copilot", true}, {"claude", false}} {
		t.Run(tc.agent, func(t *testing.T) {
			d := Destination{Dir: "~/." + tc.agent + "/skills", Agent: tc.agent,
				Layers: []Layer{namespacedWrapper(t), bare}}
			cols := Collisions([]Destination{d})
			if !tc.collide {
				if len(cols) != 0 {
					t.Fatalf("Claude Code namespaces a plugin's skills; no clash expected: %+v", cols)
				}
				return
			}
			if len(cols) != 1 || cols[0].Name != "review" {
				t.Fatalf("the inner review must clash with the bare review at %s: %+v", tc.agent, cols)
			}
			msg := cols[0].Message()
			// Minor 4: no namespace on the plugin's pack can help here, so the step names what can.
			for _, want := range []string{"acme/kit", "dotfiles/matt", "give pack dotfiles/matt a namespace",
				"`yolo pack remove acme/kit`", "`yolo pack remove dotfiles/matt`"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not name %q:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "Choose a different namespace") {
				t.Errorf("the refusal offers a namespace for the plugin's pack, which cannot help:\n%s", msg)
			}
		})
	}
}

// m1: two author names one namespace delivers alike are refused naming both, deterministically,
// both by the pre-flight and by the writer; neither wins by map order.
func TestTwoNamesOneNamespaceDeliversAlikeAreRefusedNamingBoth(t *testing.T) {
	for i := 0; i < 5; i++ {
		l := exampleLayer(t, "acme/tools", "acme", false, "Foo_Bar", "foo-bar")
		const step = "`yolo config set 'packs[2].bare' true --user`"
		l.BareStep = step
		d := Destination{Dir: "~/.claude/skills", Agent: "claude", Layers: []Layer{l}}
		cols := Collisions([]Destination{d})
		if len(cols) != 1 || cols[0].Name != "acme-foo-bar" || len(cols[0].Claims) != 2 {
			t.Fatalf("want one clash on acme-foo-bar naming both: %+v", cols)
		}
		if cols[0].Claims[0].Author != "Foo_Bar" || cols[0].Claims[1].Author != "foo-bar" {
			t.Errorf("claims out of order: %+v", cols[0].Claims)
		}

		// Minor 3: the pre-flight and the writer offer the same steps, the concrete command included.
		want := aliketStep("acme/tools", step)
		if msg := cols[0].Message(); !strings.Contains(msg, want) || !strings.Contains(want, step) {
			t.Errorf("the pre-flight does not offer %q:\n%s", want, msg)
		}
		res, err := deliverFlat(Request{Pack: "acme/tools", Namespace: "acme", BareStep: step, SkillsDir: t.TempDir()},
			map[string]string{"Foo_Bar": filepath.Join(l.Sources[0], "Foo_Bar"),
				"foo-bar": filepath.Join(l.Sources[0], "foo-bar")})
		if err != nil {
			t.Fatal(err)
		}
		refused := 0
		for _, r := range res {
			if r.Action == ActionRefused && strings.Contains(r.Detail, `"Foo_Bar" and "foo-bar"`) &&
				strings.Contains(r.Detail, want) {
				refused++
			}
			if r.Action == ActionWrote {
				t.Errorf("one of two names delivered alike was written: %+v", r)
			}
		}
		if refused != 2 {
			t.Errorf("want both refused naming each other: %+v", res)
		}
	}
}
