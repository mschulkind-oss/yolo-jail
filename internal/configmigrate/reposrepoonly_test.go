package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// A JSONC repos entry with no unqualified line for it converts to a repo-only named table. That
// table is a valid read-only declaration: the migrated workspace reads through the gate, grants
// only its read, and the plan's proof holds (GW-D79, GW-D80).
func TestMigrateConvertsAJSONCReposEntryWithNoLinesToARepoOnlyTable(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"repos": ["other/lib"]}}}`+"\n")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	text := targetText(p)
	if !strings.Contains(text, "[repos.lib]") || !strings.Contains(text, `repo = "github.com/other/lib"`) {
		t.Fatalf("the converted file lacks the repo-only table:\n%s", text)
	}
	if strings.Contains(text, "[repos.lib.requests") {
		t.Fatalf("a repo-only table declared a request:\n%s", text)
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	check := gateCheckOf(t, ws)
	for _, g := range check.Sources[0].Grants {
		if g.Kind != brokerscope.KindRead {
			t.Errorf("the migrated repo-only table grants %s", g.String())
		}
	}
}
