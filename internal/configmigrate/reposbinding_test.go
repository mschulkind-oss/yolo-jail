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

// A legacy pattern entry is not converted: a table for it would be a live grant equal to an inert
// one (GW-D80). The JSONC file holding it stays whole, unconverted and still read, with the reason
// and the next step.
func TestMigrateKeepsAJSONCFileWithALegacyPatternEntryWhole(t *testing.T) {
	env(t)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	jsonc := `{"brokered": {"github": {"repos": ["acme/*"]}}}` + "\n"
	jsoncPath := filepath.Join(ws, "yolo-jail.jsonc")
	write(t, jsoncPath, jsonc)
	p := plan(t, Request{Workspace: ws})
	if len(p.NotConverted) != 1 || p.NotConverted[0].File != jsoncPath ||
		!strings.Contains(p.NotConverted[0].Reason, "yolo request") {
		t.Fatalf("not converted = %+v", p.NotConverted)
	}
	for _, tg := range p.Targets {
		if tg.Old == jsoncPath {
			t.Fatalf("the file with the pattern entry was converted:\n%s", tg.Bytes)
		}
	}
	if text := targetText(p); strings.Contains(text, "repos") {
		t.Fatalf("a TOML companion was written for the pattern entry:\n%s", text)
	}
	if got, _ := os.ReadFile(jsoncPath); string(got) != jsonc {
		t.Fatalf("planning changed the file:\n%s", got)
	}
}

// A pattern entry stays where it is, and its reason names the verb that declares a repository,
// `yolo request repos <alias> <host>/<path>`, never a `yolo request <source> <host>/<path>` no
// command spells. What the file needs differs by format: a legacy JSONC file is still read with
// the entry in it; a TOML file holding `requests.<source>.repos` is refused at load (GW-D77), so
// its reason says the entry has to leave the file.
func TestARetainedPatternEntryNamesTheDeclaringVerbAndWhatItsFileNeeds(t *testing.T) {
	const verb = "`yolo request repos <alias> <host>/<path>`"
	reasonOf := func(t *testing.T, ws, path string) string {
		t.Helper()
		g, err := Discover(Request{UserConfigDir: filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail"), Workspace: ws})
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		p, _ := Plan(g)
		if p == nil {
			t.Fatal("no plan")
		}
		for _, n := range p.NotConverted {
			if n.Path == path {
				return n.Reason
			}
		}
		t.Fatalf("no not-converted entry at %s: %+v", path, p.NotConverted)
		return ""
	}

	t.Run("a legacy JSONC file is kept and still read", func(t *testing.T) {
		home := env(t)
		write(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"), reposUserTOML)
		ws := realDir(t)
		namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
		write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"repos": ["acme/*"]}}}`+"\n")
		reason := reasonOf(t, ws, "brokered.github.repos")
		for _, want := range []string{verb, "which yolo keeps reading"} {
			if !strings.Contains(reason, want) {
				t.Errorf("reason lacks %q:\n%s", want, reason)
			}
		}
		if strings.Contains(reason, "yolo request github <host>") {
			t.Errorf("reason names a command that does not exist:\n%s", reason)
		}
	})

	t.Run("a TOML file is refused until the entry leaves it", func(t *testing.T) {
		home := env(t)
		write(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"), reposUserTOML)
		ws := realDir(t)
		namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
		write(t, filepath.Join(ws, oracleProject), "[requests.github]\nrepos = [\"acme/*\"]\n")
		reason := reasonOf(t, ws, "requests.github.repos")
		for _, want := range []string{verb, "yolo refuses this file while the entry is in it", "yolo config unset 'requests.github.repos[<n>]'"} {
			if !strings.Contains(reason, want) {
				t.Errorf("reason lacks %q:\n%s", want, reason)
			}
		}
		for _, wrong := range []string{"yolo request github <host>", "keeps reading"} {
			if strings.Contains(reason, wrong) {
				t.Errorf("reason says %q of a file yolo refuses:\n%s", wrong, reason)
			}
		}
	})
}
