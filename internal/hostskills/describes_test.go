package hostskills

// describes_test.go pins the host half of `describes` on a `skills` contribution
// (docs/reference/agent-briefings.md BD-D3, packdecl.Contribution.Describes): a skills tree about a
// kind of its pack that the host notch does not deliver is left out of every host skills
// destination, as a briefing file about one is left out of every host briefing.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// describesFixture is an agent pack naming ~/.acme/skills and a content pack shaped like the git
// pack: a forwarder (`intercept`), and a conventional skills/ tree declared about it.
func describesFixture(t *testing.T, describes string) []*packload.Pack {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("agent/pack.json", `{"name":"agent","contributes":[`+
		`{"kind":"skills","agent":"acme","into":".acme/skills"}]}`)
	write("fwd/pack.json", `{"name":"fwd","contributes":[`+
		`{"kind":"intercept","bin":"gh","forward":["yolo","gh","--"]},`+
		`{"kind":"skills"`+describes+`}]}`)
	write("fwd/skills/yolo-fwd/SKILL.md", "---\nname: yolo-fwd\ndescription: d\n---\nbody\n")
	var set []*packload.Pack
	for _, name := range []string{"agent", "fwd"} {
		p, problems := packload.LoadDir(filepath.Join(root, name), name)
		if len(problems) > 0 {
			t.Fatalf("%s: %v", name, problems)
		}
		set = append(set, p)
	}
	resolved, _ := packload.ResolveDestinations(set)
	return resolved
}

func layerPacks(dests []Destination, dir string) []string {
	var out []string
	for _, d := range dests {
		if d.Dir != dir {
			continue
		}
		for _, l := range d.Layers {
			out = append(out, l.Pack)
		}
	}
	return out
}

func TestASkillsTreeAboutAJailOnlyKindIsNotComposedAtTheHost(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".acme", "skills")

	// Undeclared: the tree holds wherever it is delivered, as before.
	if got := layerPacks(ComposeHostSkills(describesFixture(t, ""), home), dir); len(got) != 2 {
		t.Fatalf("with no describes the host composes %v, want [agent fwd]", got)
	}

	gated := describesFixture(t, `,"describes":["intercept"]`)
	// The per-kind fallback (no census): no host verb puts a forwarder anywhere, so the tree is
	// withheld and the agent pack's own destination stays.
	if got := layerPacks(ComposeHostSkills(gated, home), dir); len(got) != 1 || got[0] != "agent" {
		t.Errorf("a skills tree describing intercept reached the host: layers %v, want [agent]", got)
	}
	// A census that says the host delivers the kind for this pack delivers the tree.
	delivers := func(pack string, k packdecl.Kind) bool { return true }
	if got := layerPacks(ComposeHostSkillsFor(gated, home, delivers), dir); len(got) != 2 {
		t.Errorf("a census delivering intercept still withheld the tree: layers %v", got)
	}
	// And one that says it does not withholds it, whatever the per-kind answer.
	never := func(pack string, k packdecl.Kind) bool { return pack != "fwd" }
	if got := layerPacks(ComposeHostSkillsFor(gated, home, never), dir); len(got) != 1 {
		t.Errorf("a census withholding the kind still composed the tree: layers %v", got)
	}
}
