package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"
)

// A local TOML file already names a repository `lib` for a different identity than the project
// list's `other/lib`. The migration allocates the listed repository its own name (other-lib), so
// the two declarations do not clobber each other, and the plan proves the conversion equal: the
// local table is the same declaration before and after (GW-D79, GW-D80).
func TestMigrateKeepsALocalNamedRepoBesideAListedRepoWithTheSameName(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	localPath := filepath.Join(ws, oracleLocal)
	write(t, localPath, "[repos.lib]\nrepo = \"github.com/acme/lib\"\n")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"repos": ["other/lib"]}}}`+"\n")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	text := targetText(p)
	if !strings.Contains(text, "[repos.other-lib]") || !strings.Contains(text, `repo = "github.com/other/lib"`) {
		t.Fatalf("the converted project lacks the allocated repo-only table:\n%s", text)
	}
	if strings.Contains(text, "github.com/acme/lib") {
		t.Fatalf("the local file's table leaked into the project file:\n%s", text)
	}
	for _, target := range p.Targets {
		if target.File != nil && target.File.Logical == oracleLocal {
			t.Fatalf("the local file, which already spells its table, is rewritten: %+v", target)
		}
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}
