package packload

// shippedprosebudget_test.go holds the shipped packs to the briefing diet's pack budgets
// (docs/reference/agent-briefings.md BD-D4): a shipped pack's always-loaded prose is at most 250
// bytes for any one destination it reaches, and the `description` of each skill a shipped pack
// ships is at most 200 bytes. Shipped content only: a user's or a fetched pack is user prose, which
// yolo never caps or trims (run.TestAUserPacksLongProseIsComposedWhole pins that half).

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

const (
	// shippedProseBudget is BD-D4's bound on one shipped pack's prose for one destination.
	shippedProseBudget = 250
	// shippedSkillDescriptionBudget is BD-D4's bound on a shipped skill's `description`.
	shippedSkillDescriptionBudget = 200
)

// briefingDest is one briefing destination a shipped agent pack declares.
type briefingDest struct{ agent, into string }

// reaches reports whether governor c's prose is delivered to d: a broadcast reaches every
// destination, an addressed one the destinations of the agents it names, and a path-named one its
// own path.
func (d briefingDest) reaches(c packdecl.Contribution) bool {
	switch {
	case c.Into != "":
		return c.Into == d.into
	case len(c.Agents) > 0:
		for _, a := range c.Agents {
			if a == d.agent {
				return true
			}
		}
		return false
	}
	return true
}

// longestLines is the n longest lines of s, longest first, for a budget failure to name.
func longestLines(s string, n int) []string {
	lines := strings.Split(s, "\n")
	sort.SliceStable(lines, func(i, j int) bool { return len(lines[i]) > len(lines[j]) })
	if len(lines) > n {
		lines = lines[:n]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = fmt.Sprintf("    %4d bytes: %s", len(l), l)
	}
	return out
}

func materializedShippedPacks(t *testing.T) []*Pack {
	t.Helper()
	shipped, probs := MaterializeEmbedded(packs.FS, t.TempDir())
	if len(probs) > 0 {
		t.Fatalf("materializing the shipped packs: %v", probs)
	}
	if len(shipped) == 0 {
		t.Fatal("no shipped packs materialized — this test would cover nothing")
	}
	return shipped
}

func TestEveryShippedPacksProseFitsItsBudgetPerDestination(t *testing.T) {
	shipped := materializedShippedPacks(t)
	var dests []briefingDest
	for _, p := range shipped {
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindBriefing && c.Agent != "" && c.Into != "" {
				dests = append(dests, briefingDest{agent: c.Agent, into: c.Into})
			}
		}
	}
	if len(dests) == 0 {
		t.Fatal("no shipped pack declares a briefing destination — this test would cover nothing")
	}
	measured := 0
	for _, p := range shipped {
		sources, probs := p.GovernedSources(packdecl.KindBriefing)
		if len(probs) > 0 {
			t.Errorf("pack %s: %v", p.Name, probs)
		}
		for _, d := range dests {
			var reached []GovernedSource
			for _, s := range sources {
				if d.reaches(s.By) {
					reached = append(reached, s)
				}
			}
			prose := JoinBriefingSources(reached)
			if prose == "" {
				continue
			}
			measured++
			if over := len(prose) - shippedProseBudget; over > 0 {
				var files []string
				for _, s := range reached {
					files = append(files, s.Rel)
				}
				t.Errorf("shipped pack %s puts %d bytes of prose into ~/%s (%s), %d over the "+
					"%d-byte budget (docs/reference/agent-briefings.md BD-D4). Move the text into a "+
					"skill the pack ships (packs/%s/skills/yolo-<subject>/SKILL.md) and leave a "+
					"pointer naming it; do not raise the budget. Longest lines:\n%s",
					p.Name, len(prose), d.into, strings.Join(files, ", "), over,
					shippedProseBudget, p.Name, strings.Join(longestLines(prose, 3), "\n"))
			}
		}
	}
	if measured == 0 {
		t.Fatal("no shipped pack has prose for any destination — this test would cover nothing")
	}
}

// skillFrontmatter is the two fields of a SKILL.md frontmatter this test reads.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func readSkillFrontmatter(t *testing.T, path string) skillFrontmatter {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		t.Fatalf("%s: no frontmatter", path)
	}
	head, _, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		t.Fatalf("%s: unterminated frontmatter", path)
	}
	var fm skillFrontmatter
	if err := yaml.Unmarshal(head, &fm); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return fm
}

func TestEveryShippedPacksSkillDescriptionFitsItsBudget(t *testing.T) {
	measured := 0
	for _, p := range materializedShippedPacks(t) {
		sources, _ := p.GovernedSources(packdecl.KindSkills)
		for _, s := range sources {
			entries, err := os.ReadDir(s.Abs)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				path := filepath.Join(s.Abs, e.Name(), "SKILL.md")
				if _, err := os.Stat(path); err != nil {
					continue
				}
				measured++
				fm := readSkillFrontmatter(t, path)
				if fm.Name != e.Name() {
					t.Errorf("packs/%s/%s/%s: frontmatter name %q is not its directory's",
						p.Name, s.Rel, e.Name(), fm.Name)
				}
				// BD-D2: a shipped pack's skill is `yolo-<subject>`, clear of a user's own
				// skill of the subject's bare name.
				if !strings.HasPrefix(e.Name(), "yolo-") {
					t.Errorf("packs/%s/%s/%s: a shipped pack's skill is named yolo-<subject> "+
						"(docs/reference/agent-briefings.md BD-D2)", p.Name, s.Rel, e.Name())
				}
				if fm.Description == "" {
					t.Errorf("packs/%s/%s/%s: no description", p.Name, s.Rel, e.Name())
				}
				if over := len(fm.Description) - shippedSkillDescriptionBudget; over > 0 {
					t.Errorf("packs/%s/%s/%s: description is %d bytes, %d over the %d-byte "+
						"budget (docs/reference/agent-briefings.md BD-D4). Name the subjects an agent "+
						"would search for and move the rest into the skill's body; do not raise "+
						"the budget:\n    %s", p.Name, s.Rel, e.Name(), len(fm.Description), over,
						shippedSkillDescriptionBudget, fm.Description)
				}
			}
		}
	}
	if measured == 0 {
		t.Fatal("no shipped pack ships a skill — this test would cover nothing")
	}
}
