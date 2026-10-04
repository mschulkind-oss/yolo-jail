package pluginpack

// declaredpaths_test.go pins the components table's `paths` column from its TRUE side: a field
// whose value names plugin paths has those paths kept out of a root skill's flat copy. The false
// side (a setting's value is a name, not a path) is pinned in hostskills by
// TestFlatRootSkillKeepsTheFolderASettingNames.

import (
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack/pluginpacktest"
)

// The fixture covers every path-bearing row, so a row added with `paths: true` and no fixture
// entry, or a fixture entry whose row says its value is not a path, fails here rather than
// leaving that component's declared folder unguarded.
func TestEveryPathBearingComponentHasADeclaredPathFixture(t *testing.T) {
	var rows []string
	for _, c := range components {
		if c.pick != nil && c.paths {
			rows = append(rows, c.name)
		}
	}
	var fixture []string
	for name := range pluginpacktest.EveryDeclaredPath {
		fixture = append(fixture, name)
	}
	sort.Strings(rows)
	sort.Strings(fixture)
	if !slices.Equal(rows, fixture) {
		t.Errorf("components whose field names plugin paths = %v, but "+
			"pluginpacktest.EveryDeclaredPath covers %v", rows, fixture)
	}
}

// Every path a manifest field declares is plugin machinery, so ComponentPaths names it, and
// Components reports the component from the manifest.
func TestComponentPathsNameEveryDeclaredPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme-tools")
	pluginpacktest.WriteEveryDeclaredPathPlugin(t, dir, "acme-tools")
	p, ok := Load(dir)
	if !ok {
		t.Fatal("the fixture is not plugin-shaped")
	}
	excluded := p.ComponentPaths()
	for name, rel := range pluginpacktest.EveryDeclaredPath {
		file := filepath.Join(dir, filepath.FromSlash(rel))
		if !slices.ContainsFunc(excluded, func(x string) bool { return Contains(x, file) }) {
			t.Errorf("%s declared at %s is not among ComponentPaths, so a root skill's flat copy "+
				"carries it: %v", name, rel, excluded)
		}
		c, found := componentByName(p, name)
		if !found || !slices.Contains(c.Sources, ".claude-plugin/plugin.json") {
			t.Errorf("%s declared in the manifest was not reported from it: %+v", name,
				p.Components())
		}
	}
	// The root skill itself stays: excluding the plugin root would exclude everything.
	if slices.ContainsFunc(excluded, func(x string) bool { return Contains(x, dir) }) {
		t.Errorf("ComponentPaths names the plugin root, which a root skill's copy is: %v", excluded)
	}
}
