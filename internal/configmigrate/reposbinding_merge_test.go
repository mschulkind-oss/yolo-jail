package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"
)

// A listed repository takes a copy of each unqualified line, and an explicit grant on the same
// repository adds to the same named table: both are the old reader's union, so the two merge
// instead of refusing each other as already written (GW-D79, GW-D80).
func TestMigrateMergesAGrantIntoTheCopiedLineOfAListedRepository(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"repos": ["other/lib"],
    "grants": [{"kind": "runs", "repo": "github.com/other/lib", "workflow": "nightly.yml"},
               {"kind": "create-pr", "repo": "github.com/other/lib"}]}},
  "requests": {"github": {"runs": ["ci.yml"], "create_pr": true}}}`+"\n")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got := map[string]bool{}
	for _, w := range gateCheckOf(t, ws).Sources[0].Grants {
		got[w.String()] = true
	}
	for _, want := range []string{"runs github.com/other/lib ci.yml", "runs github.com/other/lib nightly.yml", "create_pr github.com/other/lib"} {
		if !got[want] && !got[strings.Replace(want, "create_pr", "create-pr", 1)] {
			t.Errorf("the migrated gate lacks %q: %v", want, got)
		}
	}
}

// A repository a local file lists and a project file grants on share one named table: each file
// declares its repo line, the reader unions the two layers, and the migrated gate grants the
// grant's write on that repository, with no clobbered or refused table (GW-D80).
func TestMigrateMergesASharedRepoDeclarationAcrossFiles(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	write(t, filepath.Join(ws, oracleLocal), "[requests.github]\nrepos = [\"other/lib\"]\n")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"grants": [{"kind": "runs", "repo": "github.com/other/lib", "workflow": "nightly.yml"}]}}}`+"\n")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	found := false
	for _, w := range gateCheckOf(t, ws).Sources[0].Grants {
		found = found || w.String() == "runs github.com/other/lib nightly.yml"
	}
	if !found {
		t.Fatal("the migrated gate lacks the shared repository's grant")
	}
}
