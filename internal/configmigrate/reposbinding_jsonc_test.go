package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"
)

// A JSONC file's `brokered.<source>.repos` entry is a named table, as a TOML file's list is: the
// listed repository takes each unqualified line the merged workspace writes (GW-D79, GW-D80).
func TestMigrateConvertsAJSONCReposEntryToANamedTableWithItsLines(t *testing.T) {
	env(t)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	jsoncPath := filepath.Join(ws, "yolo-jail.jsonc")
	write(t, jsoncPath, `{"brokered": {"github": {"repos": ["other/lib"]}},
  "requests": {"github": {"runs": ["ci.yml"]}}}`+"\n")
	p := plan(t, Request{Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	text := targetText(p)
	for _, want := range []string{"[repos.lib]", `repo = "github.com/other/lib"`, "[repos.lib.requests.github]", `runs = ["ci.yml"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the converted file lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "repos = [") {
		t.Fatalf("the old list survived:\n%s", text)
	}
	if !strings.Contains(p.canonical.grants, `"runs github.com/other/lib ci.yml"`) {
		t.Fatalf("the proof's grants lack the copied line: %s", p.canonical.grants)
	}
}

// Before GW-D79 every unqualified line also reached each other remote: a JSONC entry keeps those
// writes as lines of a table named for the remote, with no repo line.
func TestMigrateKeepsTheWritesAnotherRemoteHadInAJSONCFile(t *testing.T) {
	env(t)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git", "upstream", "https://github.com/acme/up.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"repos": ["other/lib"]}},
  "requests": {"github": {"runs": ["ci.yml"]}}}`+"\n")
	p := plan(t, Request{Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	text := targetText(p)
	for _, want := range []string{"[repos.upstream.requests.github]", "[repos.lib.requests.github]", `repo = "github.com/other/lib"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the converted file lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "github.com/acme/up") {
		t.Fatalf("a remote's table was given a repo line:\n%s", text)
	}
	for _, want := range []string{`"runs github.com/acme/up ci.yml"`, `"runs github.com/other/lib ci.yml"`} {
		if !strings.Contains(p.canonical.grants, want) {
			t.Fatalf("the proof's grants lack %s: %s", want, p.canonical.grants)
		}
	}
}

// A short name taken by another repository is the owner-repo name, not a refusal.
func TestMigrateNamesAJSONCReposEntryOwnerRepoWhenTheShortNameIsTaken(t *testing.T) {
	env(t)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"repos": ["a/lib", "b/lib"]}},
  "requests": {"github": {"runs": ["ci.yml"]}}}`+"\n")
	p := plan(t, Request{Workspace: ws})
	if len(p.NotConverted) > 0 {
		t.Fatalf("not converted = %+v", p.NotConverted)
	}
	text := targetText(p)
	for _, want := range []string{"[repos.lib]", `repo = "github.com/a/lib"`, "[repos.b-lib]", `repo = "github.com/b/lib"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the converted file lacks %q:\n%s", want, text)
		}
	}
}

// A grant on the origin repository stays unqualified whatever the remote count, as the
// readable path reads it; a grant on another repository takes its own declared table, once the
// repository is also listed in repos: an unlisted grant on a non-remote repository is unplaced by
// the old reader (inert), so it is a migration difference and is not converted to a live table.
func TestMigratePlacesAJSONCGrantsByTheirRepository(t *testing.T) {
	env(t)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git", "upstream", "https://github.com/acme/up.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"repos": ["other/lib"], "grants": [
		{"kind": "runs", "repo": "github.com/acme/app", "workflow": "a.yml"},
		{"kind": "runs", "repo": "github.com/other/lib", "workflow": "b.yml"}
	]}}}`+"\n")
	p := plan(t, Request{Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	text := targetText(p)
	for _, want := range []string{"[requests.github]", `runs = ["a.yml"]`, "[repos.lib]", `repo = "github.com/other/lib"`,
		"[repos.lib.requests.github]", `runs = ["b.yml"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the converted file lacks %q:\n%s", want, text)
		}
	}
	for _, never := range []string{"github.com/other/lib a.yml", "github.com/acme/app b.yml", "github.com/acme/up"} {
		if strings.Contains(p.canonical.grants, never) {
			t.Fatalf("the conversion widened: %s in %s", never, p.canonical.grants)
		}
	}
}

// A remote named all cannot serve a named table (all is reserved), so its writes move to a table
// named for the repository with a repo line: every write is kept, none is left unconverted.
func TestMigrateNamesARemoteAllTableOwnerRepo(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git", "all", "https://github.com/other/all.git")
	write(t, filepath.Join(ws, "yolo-jail.toml"), "[requests.github]\nruns = [\"nightly.yml\"]\n")
	localPath := filepath.Join(ws, "yolo-jail.local.toml")
	write(t, localPath, "[requests.github]\nrepos = [\"x/y\"]\n")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	text := string(target(t, p, localPath).Bytes)
	if strings.Contains(text, "[repos.all]") {
		t.Fatalf("the reserved name all was written as a table:\n%s", text)
	}
	for _, want := range []string{"[repos.other-all]", `repo = "github.com/other/all"`, "[repos.other-all.requests.github]", `runs = ["nightly.yml"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the rewritten local file lacks %q:\n%s", want, text)
		}
	}
}
