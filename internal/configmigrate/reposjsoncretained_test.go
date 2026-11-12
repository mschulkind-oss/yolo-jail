package configmigrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// These tests go through Plan, Transcript and Apply on a legacy JSONC project file, then read the
// written TOML with the launch's own readers. They cover the three shapes a JSONC file keeps a
// named write in: unqualified lines beside a second remote, a `brokered.<source>.repos` list, and
// a `requests.<source>.repos` list (GW-D79, GW-D80; the plan's Task 6 table).

// namedWritesOf is every `repos.<name>.requests.<source>.<capability>` line of a TOML file, as
// `<dotted key> = <compact value>`.
func namedWritesOf(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		t.Fatalf("the written file does not parse: %v\n%s", err, data)
	}
	out := map[string]bool{}
	repos, ok := mapAt(doc.Values, "repos")
	if !ok {
		return out
	}
	for _, name := range repos.Keys() {
		table, _ := mapAt(repos, name)
		requests, ok := mapAt(table, "requests")
		if !ok {
			continue
		}
		for _, source := range requests.Keys() {
			asked, ok := mapAt(requests, source)
			if !ok {
				continue
			}
			for _, capability := range asked.Keys() {
				v, _ := asked.Get(capability)
				out["repos."+name+".requests."+source+"."+capability+" = "+jsonx.MustDumpsCompact(v)] = true
			}
		}
	}
	return out
}

// printedNamedWrites are the transcript's retained-write lines, each cut to `<dotted key> =
// <value>`, in the order printed.
func printedNamedWrites(transcript string) []string {
	var out []string
	for _, line := range strings.Split(transcript, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "repos.") || !strings.Contains(line, ".requests.") {
			continue
		}
		head, _, _ := strings.Cut(line, " (")
		out = append(out, head)
	}
	return out
}

// requireTranscriptPrintsEveryNamedWrite fails unless the transcript prints each named write of
// the written file exactly once, and nothing the file does not hold.
func requireTranscriptPrintsEveryNamedWrite(t *testing.T, transcript string, written []byte) {
	t.Helper()
	want := namedWritesOf(t, written)
	if len(want) == 0 {
		t.Fatalf("the written file holds no named write, so the check proves nothing:\n%s", written)
	}
	seen := map[string]bool{}
	for _, line := range printedNamedWrites(transcript) {
		if seen[line] {
			t.Fatalf("the transcript prints %q twice:\n%s", line, transcript)
		}
		seen[line] = true
		if !want[line] {
			t.Fatalf("the transcript prints %q, which the written file does not hold:\n%s\n%s", line, transcript, written)
		}
	}
	for line := range want {
		if !seen[line] {
			t.Fatalf("the transcript does not print the retained write %q:\n%s", line, transcript)
		}
	}
}

// gateWritesAfterApply applies the plan and returns the write grants the launch's scope check
// gives the converted workspace, after confirming the new reader's projection of the written
// files equals the old reader's projection of the old file.
func gateWritesAfterApply(t *testing.T, p *MigrationPlan, ws, oldJSON string) map[string]bool {
	t.Helper()
	decls, _, err := config.ReadableDeclarationsForPacks(packload.Embedded())
	if err != nil {
		t.Fatalf("shipped declarations: %v", err)
	}
	wantRaw := config.LegacyWorkspaceBrokerGrantsOf(mustJSON(t, oldJSON), ws, decls)
	if u := unplacedOf(wantRaw); len(u) > 0 {
		t.Fatalf("the old file leaves grants unplaced before the migration: %v", u)
	}
	want := grantKeys(t, wantRaw)
	if len(want) == 0 {
		t.Fatalf("the old file grants nothing, so the comparison proves nothing")
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatalf("the converted files do not read through the gate: %v", err)
	}
	gotRaw := config.WorkspaceBrokerGrantsOf(after.Config, ws, decls)
	if u := unplacedOf(gotRaw); len(u) > 0 {
		t.Fatalf("the converted files leave grants unplaced: %v", u)
	}
	if missing, extra := diffGrantSets(want, grantKeys(t, gotRaw)); len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("effective grants changed by the migration\nmissing after: %v\nextra after: %v", missing, extra)
	}
	check := config.NewScopeCheck(ws, []config.ScopeBroker{shippedGitHubScopeBroker(t, decls)}, after)
	if check == nil || check.Fatal != nil {
		t.Fatalf("the gate's scope check failed after the migration: %+v", check)
	}
	writes := map[string]bool{}
	for _, src := range check.Sources {
		for _, g := range src.Grants {
			if g.Kind != brokerscope.KindRead {
				writes[g.String()] = true
			}
		}
	}
	return writes
}

func requireWrites(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()
	wanted := map[string]bool{}
	for _, w := range want {
		wanted[w] = true
	}
	if missing, extra := diffGrantSets(wanted, got); len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("the gate's write grants after the migration differ from the old file's\nmissing after: %v\nextra after: %v", missing, extra)
	}
}

// jsoncWorkspace is a workspace with the user ceiling of reposUserTOML, the remotes given as
// name, url pairs, and one legacy project file.
func jsoncWorkspace(t *testing.T, project string, remotes ...string) (dir, ws, projectPath string) {
	t.Helper()
	home := env(t)
	dir = filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws = realDir(t)
	namedRemotes(t, ws, remotes...)
	projectPath = filepath.Join(ws, "yolo-jail.jsonc")
	write(t, projectPath, project+"\n")
	return dir, ws, projectPath
}

// The first shape: no `repos` list anywhere, a second remote on the host. The remote's table is
// written in the converted file, printed, and granted by the gate after Apply.
func TestAJSONCFileOfRequestLinesKeepsAndPrintsTheOtherRemotesWrites(t *testing.T) {
	project := `{"requests": {"github": {"runs": ["ci.yml"], "create_pr": true}}}`
	dir, ws, projectPath := jsoncWorkspace(t, project,
		"origin", "https://github.com/acme/app.git", "upstream", "https://github.com/acme/up.git")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	written := target(t, p, projectPath)
	text := string(written.Bytes)
	for _, want := range []string{"[requests.github]", "[repos.upstream.requests.github]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the converted file lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, `runs = ["ci.yml"]`) != 2 || strings.Count(text, "create_pr = true") != 2 {
		t.Fatalf("want each line under [requests.github] and under the remote's table:\n%s", text)
	}
	if strings.Contains(text, "repo = ") {
		t.Fatalf("a remote's table was given a repo line:\n%s", text)
	}
	transcript := p.Transcript()
	requireTranscriptPrintsEveryNamedWrite(t, transcript, written.Bytes)
	for _, want := range []string{"remote upstream = github.com/acme/up", "requests.github.runs", "requests.github.create_pr", projectPath} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the transcript does not say %q of the retained writes:\n%s", want, transcript)
		}
	}
	requireWrites(t, gateWritesAfterApply(t, p, ws, project),
		"runs github.com/acme/app ci.yml", "runs github.com/acme/up ci.yml",
		"create-pr github.com/acme/app", "create-pr github.com/acme/up")
}

// A remote on another host was never reached by a github line, so it takes no table; two remotes
// naming one repository each take their own table and the repository is granted once.
func TestAJSONCFileOfRequestLinesNamesOnlyTheRemotesOnTheSourcesHost(t *testing.T) {
	project := `{"requests": {"github": {"runs": ["ci.yml"]}}}`
	dir, ws, projectPath := jsoncWorkspace(t, project,
		"origin", "https://github.com/acme/app.git",
		"lab", "https://gitlab.com/acme/app.git",
		"upstream", "https://github.com/acme/up.git",
		"mirror", "https://github.com/acme/up.git")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	written := target(t, p, projectPath)
	text := string(written.Bytes)
	for _, want := range []string{"[repos.upstream.requests.github]", "[repos.mirror.requests.github]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the converted file lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "repos.lab") || strings.Contains(text, "gitlab.com") {
		t.Fatalf("a remote on another host took a github table:\n%s", text)
	}
	requireTranscriptPrintsEveryNamedWrite(t, p.Transcript(), written.Bytes)
	requireWrites(t, gateWritesAfterApply(t, p, ws, project),
		"runs github.com/acme/app ci.yml", "runs github.com/acme/up ci.yml")
}

// The second shape, with a second remote: the list's table and the remote's table are each
// written once, though both the list and the file's own lines ask for the remote's, and each
// retained write is printed once.
func TestAJSONCBrokeredReposListPrintsEachRetainedWriteOnce(t *testing.T) {
	project := `{"brokered": {"github": {"repos": ["other/lib"]}}, "requests": {"github": {"runs": ["ci.yml"]}}}`
	dir, ws, projectPath := jsoncWorkspace(t, project,
		"origin", "https://github.com/acme/app.git", "upstream", "https://github.com/acme/up.git")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	written := target(t, p, projectPath)
	text := string(written.Bytes)
	for _, table := range []string{"[repos.lib.requests.github]", "[repos.upstream.requests.github]"} {
		if strings.Count(text, table) != 1 {
			t.Fatalf("want %s once:\n%s", table, text)
		}
	}
	if strings.Count(text, `runs = ["ci.yml"]`) != 3 {
		t.Fatalf("want the line under [requests.github] and once under each table:\n%s", text)
	}
	transcript := p.Transcript()
	requireTranscriptPrintsEveryNamedWrite(t, transcript, written.Bytes)
	for _, want := range []string{"github.com/other/lib", "remote upstream = github.com/acme/up", "brokered.github.repos", "requests.github.runs", projectPath} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the transcript does not say %q of the retained writes:\n%s", want, transcript)
		}
	}
	requireWrites(t, gateWritesAfterApply(t, p, ws, project),
		"runs github.com/acme/app ci.yml", "runs github.com/acme/up ci.yml", "runs github.com/other/lib ci.yml")
}

// The third shape: a JSONC file's `requests.<source>.repos` list converts as a TOML file's does.
func TestAJSONCRequestsReposListBecomesANamedTableWithItsRetainedWrites(t *testing.T) {
	project := `{"requests": {"github": {"repos": ["other/lib"], "runs": ["ci.yml"]}}}`
	dir, ws, projectPath := jsoncWorkspace(t, project, "origin", "https://github.com/acme/app.git")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	written := target(t, p, projectPath)
	text := string(written.Bytes)
	for _, want := range []string{"[repos.lib]", `repo = "github.com/other/lib"`, "[repos.lib.requests.github]", "[requests.github]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the converted file lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, `runs = ["ci.yml"]`) != 2 {
		t.Fatalf("want the line under [requests.github] and under the listed repository's table:\n%s", text)
	}
	if strings.Contains(text, "repos = [") {
		t.Fatalf("the old list survived:\n%s", text)
	}
	transcript := p.Transcript()
	requireTranscriptPrintsEveryNamedWrite(t, transcript, written.Bytes)
	for _, want := range []string{"github.com/other/lib", "requests.github.repos", "requests.github.runs", projectPath} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the transcript does not say %q of the retained writes:\n%s", want, transcript)
		}
	}
	requireWrites(t, gateWritesAfterApply(t, p, ws, project),
		"runs github.com/acme/app ci.yml", "runs github.com/other/lib ci.yml")
	if got, err := os.ReadFile(filepath.Join(ws, "yolo-jail.toml")); err != nil || strings.Contains(string(got), "repos = [") {
		t.Fatalf("the applied file holds the old key (%v):\n%s", err, got)
	}
}

// The third shape with a second remote: the listed repository and the remote each keep the line.
func TestAJSONCRequestsReposListKeepsTheOtherRemotesWritesToo(t *testing.T) {
	project := `{"requests": {"github": {"repos": ["other/lib"], "runs": ["ci.yml"]}}}`
	dir, ws, projectPath := jsoncWorkspace(t, project,
		"origin", "https://github.com/acme/app.git", "upstream", "https://github.com/acme/up.git")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	written := target(t, p, projectPath)
	text := string(written.Bytes)
	for _, table := range []string{"[repos.lib.requests.github]", "[repos.upstream.requests.github]"} {
		if strings.Count(text, table) != 1 {
			t.Fatalf("want %s once:\n%s", table, text)
		}
	}
	requireTranscriptPrintsEveryNamedWrite(t, p.Transcript(), written.Bytes)
	requireWrites(t, gateWritesAfterApply(t, p, ws, project),
		"runs github.com/acme/app ci.yml", "runs github.com/acme/up ci.yml", "runs github.com/other/lib ci.yml")
}

// A pattern in a JSONC `requests.<source>.repos` list has no named table: the plan is refused
// naming the key, and no table, wildcard or otherwise, is written for it.
func TestAJSONCRequestsReposPatternIsNotConverted(t *testing.T) {
	project := `{"requests": {"github": {"repos": ["acme/*"], "runs": ["ci.yml"]}}}`
	dir, ws, _ := jsoncWorkspace(t, project, "origin", "https://github.com/acme/app.git")
	g, err := Discover(Request{UserConfigDir: dir, Workspace: ws})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	_, err = Plan(g)
	var inequality *InequalityError
	if !errors.As(err, &inequality) {
		t.Fatalf("Plan: %v, want the pattern entry refused as not converted", err)
	}
	named := false
	for _, n := range inequality.Plan.NotConverted {
		named = named || n.Path == "requests.github.repos"
	}
	if !named {
		t.Fatalf("the plan does not name requests.github.repos as not converted: %+v", inequality.Plan.NotConverted)
	}
	for _, tg := range inequality.Plan.Targets {
		if strings.Contains(string(tg.Bytes), "[repos.") {
			t.Fatalf("a table was written for the pattern entry:\n%s", tg.Bytes)
		}
	}
	if exists(filepath.Join(ws, "yolo-jail.toml")) {
		t.Fatalf("a refused plan wrote the converted file")
	}
}

// With no origin an unqualified line has no repository after GW-D79, so the conversion cannot
// say what the old file said: the plan is refused and nothing is written.
func TestAJSONCFileOfRequestLinesWithNoOriginIsRefused(t *testing.T) {
	project := `{"requests": {"github": {"runs": ["ci.yml"]}}}`
	dir, ws, _ := jsoncWorkspace(t, project, "upstream", "https://github.com/acme/up.git")
	g, err := Discover(Request{UserConfigDir: dir, Workspace: ws})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if _, err := Plan(g); err == nil {
		t.Fatalf("a workspace with no origin converted its unqualified line")
	}
	if exists(filepath.Join(ws, "yolo-jail.toml")) {
		t.Fatalf("a refused plan wrote the converted file")
	}
}

// A TOML project file's unqualified lines are not an old spelling: converting the user's legacy
// file beside it adds no table to the project and does not rewrite it.
func TestATOMLProjectBesideAConvertedUserFileGainsNoRemoteTable(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["git", "github"]}`+"\n")
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git", "upstream", "https://github.com/acme/up.git")
	projectPath := filepath.Join(ws, "yolo-jail.toml")
	project := "[requests.github]\nruns = [\"ci.yml\"]\n"
	write(t, projectPath, project)
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal {
		t.Fatalf("plan unequal: %+v", p.Differences)
	}
	for _, tg := range p.Targets {
		if tg.Old == projectPath || strings.Contains(string(tg.Bytes), "[repos.") {
			t.Fatalf("the TOML project was rewritten or a table was added: %+v\n%s", tg.Old, tg.Bytes)
		}
	}
	if strings.Contains(p.canonical.grants, "github.com/acme/up") {
		t.Fatalf("the proof read the TOML project's line on the second remote: %s", p.canonical.grants)
	}
}
