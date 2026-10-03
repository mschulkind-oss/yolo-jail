package packload

// plugindefaults_test.go pins the footprint of a wrapped plugin whose code sits at Claude Code's
// DEFAULT locations with no manifest entry: `yolo pack footprint` and the launch's jail-code line
// both read this claim.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack/pluginpacktest"
)

func TestFootprintSurfacesDefaultLocationPluginCode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wrapper")
	pluginpacktest.WriteDefaultLocationPlugin(t, filepath.Join(root, "skills", "acme-tools"), "acme-tools")
	decl := `{"name":"wrapper","skills_tier":"namespaced","contributes":[` +
		`{"kind":"skills","from":"skills","into":".claude/skills"}]}`
	if err := os.WriteFile(filepath.Join(root, packdecl.ManifestName), []byte(decl), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := LoadDir(root, "wrapper")
	if len(problems) > 0 {
		t.Fatalf("unexpected manifest problems: %v", problems)
	}
	var claim *Claim
	claims := FootprintOf(p).Claims
	for i := range claims {
		if claims[i].Target == "plugin:acme-tools" {
			claim = &claims[i]
		}
	}
	if claim == nil {
		t.Fatalf("the wrapped plugin makes no footprint claim: %+v", claims)
	}
	if !claim.ReviewWorthy {
		t.Errorf("a plugin whose hooks, MCP server, monitor and executable sit at their default "+
			"locations was not flagged for review — its manifest names none of them, and "+
			"Claude Code loads every one: %q", claim.Detail)
	}
	for _, want := range []string{"hooks", "mcpServers", "monitors", "bin", "RUNS CODE"} {
		if !strings.Contains(claim.Detail, want) {
			t.Errorf("footprint detail %q must name %q", claim.Detail, want)
		}
	}
}
