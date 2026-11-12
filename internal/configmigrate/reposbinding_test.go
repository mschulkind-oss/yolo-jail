package configmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// namedRemotes writes the workspace's git config with the remotes named as given, in order:
// name, url, name, url.
func namedRemotes(t *testing.T, ws string, pairs ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("[core]\n\tbare = false\n")
	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString("[remote \"" + pairs[i] + "\"]\n\turl = " + pairs[i+1] + "\n")
	}
	write(t, filepath.Join(ws, ".git", "config"), b.String())
	write(t, filepath.Join(ws, ".git", "HEAD"), "ref: refs/heads/main\n")
}

const reposUserTOML = "packs = [\"git\", \"github\"]\n\n[allow.github]\nalways = [\"read\"]\non_request = [\"runs\", \"approve_runs\", \"create_pr\"]\n"

// The real shape: the project file asks, the local file lists the extra repository. Today the
// listed repository takes every project line; the migration writes each as its own line.
func TestMigrateTurnsATOMLReposListIntoANamedTableWithItsRetainedWrites(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	project := "[requests.github]\nruns = [\"nightly.yml\", \"release.yml\"]\napprove_runs = [\"github-actions[bot]\"]\n"
	write(t, filepath.Join(ws, "yolo-jail.toml"), project)
	localPath := filepath.Join(ws, "yolo-jail.local.toml")
	write(t, localPath, "# mine\n[requests.github]\nrepos = [\"other/lib\"]\n")

	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	if len(p.Targets) != 1 {
		t.Fatalf("targets = %+v; want the local file alone", p.Targets)
	}
	local := target(t, p, localPath)
	if local.New != localPath {
		t.Fatalf("a TOML file is rewritten in place; New = %s", local.New)
	}
	text := string(local.Bytes)
	for _, want := range []string{"# mine", "[repos.lib]", `repo = "github.com/other/lib"`, "[repos.lib.requests.github]",
		`runs = ["nightly.yml", "release.yml"]`, `approve_runs = ["github-actions[bot]"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the rewritten local file lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "repos = [") {
		t.Fatalf("the old list survived:\n%s", text)
	}
	for _, want := range []string{`"runs github.com/other/lib nightly.yml"`, `"runs github.com/other/lib release.yml"`,
		`"approve-run github.com/other/lib github-actions[bot]"`, `"runs github.com/acme/app nightly.yml"`} {
		if !strings.Contains(p.canonical.grants, want) {
			t.Fatalf("the proof's grants lack %s: %s", want, p.canonical.grants)
		}
	}
	transcript := p.Transcript()
	for _, want := range []string{"github.com/other/lib", "repos.lib.requests.github.runs", "yolo-jail.local.toml"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the transcript does not print the retained write (%s):\n%s", want, transcript)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "yolo-jail.toml")); string(got) != project {
		t.Fatalf("planning wrote the project file:\n%s", got)
	}
}

// Before GW-D79 every unqualified line also reached each other remote. A converted workspace
// keeps those writes as lines of a table named for the remote, printed like the rest.
func TestMigrateKeepsTheWritesAnotherRemoteHad(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git", "upstream", "https://github.com/acme/up.git")
	project := "[requests.github]\nruns = [\"nightly.yml\"]\n"
	write(t, filepath.Join(ws, "yolo-jail.toml"), project)
	localPath := filepath.Join(ws, "yolo-jail.local.toml")
	write(t, localPath, "[requests.github]\nrepos = [\"other/lib\"]\n")

	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	text := string(target(t, p, localPath).Bytes)
	for _, want := range []string{"[repos.upstream.requests.github]", "[repos.lib.requests.github]", `repo = "github.com/other/lib"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the rewritten local file lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, `runs = ["nightly.yml"]`) != 2 {
		t.Fatalf("want the line once under each table:\n%s", text)
	}
	if strings.Contains(text, "github.com/acme/up") {
		t.Fatalf("a remote's table was given a repo line:\n%s", text)
	}
	for _, want := range []string{`"runs github.com/acme/app nightly.yml"`, `"runs github.com/acme/up nightly.yml"`, `"runs github.com/other/lib nightly.yml"`} {
		if !strings.Contains(p.canonical.grants, want) {
			t.Fatalf("the proof's grants lack %s: %s", want, p.canonical.grants)
		}
	}
	transcript := p.Transcript()
	for _, want := range []string{"github.com/acme/up", "repos.upstream.requests.github.runs"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the transcript does not print the retained write (%s):\n%s", want, transcript)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "yolo-jail.toml")); string(got) != project {
		t.Fatalf("planning wrote the project file:\n%s", got)
	}
}

// A TOML file without the old key is not rewritten: a workspace whose only old spelling is in
// another file keeps this file's bytes, and a second plan over the converted file finds nothing.
func TestMigrateLeavesATOMLFileWithoutTheOldKeyAsItIs(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	project := "# keep\n[requests.github]\nruns = [\"nightly.yml\"]\n"
	write(t, filepath.Join(ws, "yolo-jail.toml"), project)

	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	for _, tg := range p.Targets {
		if tg.Old == filepath.Join(ws, "yolo-jail.toml") {
			t.Fatalf("a file with no old key is a target: %+v", tg)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "yolo-jail.toml")); string(got) != project {
		t.Fatalf("planning wrote the project file:\n%s", got)
	}
}
