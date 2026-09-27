package packload_test

// projectskilldirs_test.go pins what each shipped agent pack declares as the project-scope skills
// directories its agent reads (`project_dirs`, docs/design/workspace-skills.md §5). The
// declaration is DATA about a vendor's binary, so this is a census: the rows are what the
// bundles installed on 2026-09-27 contain (design §2.1), and a new agent pack fails here until
// somebody measures its agent. Whether the installed binaries still name these strings is
// integration/agents_test.go's probe (TestPackProjectSkillDirsAreInTheInstalledAgent) — this
// half runs under -short, where a pack is added.

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs
)

// shippedProjectSkillDirs is the census, in each agent's own precedence order. A nil row is a
// decision too, and says why.
var shippedProjectSkillDirs = map[string][]string{
	"claude": {".claude/skills"},
	// `.agents/skills` appears in the claude binary only inside an IMPORTER that copies it into
	// .claude/skills — not a directory claude reads.
	"copilot":  {".github/skills", ".agents/skills", ".claude/skills"},
	"codex":    {".codex/skills"}, // `.agents/skills` is in the binary beside an external-agent migration; unconfirmed as a read path
	"opencode": {".opencode/skills", ".claude/skills", ".agents/skills"},
	// pi reads `.agents/skills` too — its docs/skills.md and its package manager's ancestor walk
	// (pi 0.87.1, 2026-09-27); the design's first table, read from `CONFIG_DIR_NAME` alone,
	// missed it.
	"pi":  {".pi/skills", ".agents/skills"},
	"agy": {".agents/skills"},
	// omp's bundle is not installable on every arch and was not installed where the census was
	// taken; an unmeasured path is not declared. It still RECEIVES every workspace source.
	"omp": nil,
}

func TestShippedAgentPacksDeclareTheirProjectSkillDirs(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range packload.Embedded() {
		dest := false
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindSkills && c.Agent != "" {
				dest = true
			}
		}
		if !dest {
			if got := p.Decl.ProjectSkillDirs(); len(got) != 0 {
				t.Errorf("pack %s declares project_dirs %v with no skills destination", p.Name, got)
			}
			continue
		}
		seen[p.Name] = true
		want, known := shippedProjectSkillDirs[p.Name]
		if !known {
			t.Errorf("pack %s has a skills destination and the project_dirs census does not "+
				"mention it — measure where its agent reads skills in a workspace, declare it, and "+
				"add a row (nil, with the reason, if it cannot be measured)", p.Name)
			continue
		}
		if got := p.Decl.ProjectSkillDirs(); !reflect.DeepEqual(got, want) {
			t.Errorf("pack %s declares project_dirs %v, census says %v", p.Name, got, want)
		}
	}
	for name := range shippedProjectSkillDirs {
		if !seen[name] {
			t.Errorf("census row %q names no shipped pack with a skills destination", name)
		}
	}
}
