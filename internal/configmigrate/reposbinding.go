package configmigrate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// requestLine is one unqualified `requests.<source>.<kind>` value the merged workspace writes,
// the write every repository the old list named takes a copy of (GW-D79, GW-D80).
type requestLine struct {
	kind  string
	value any
}

// legacyRepos is what a TOML project file's `requests.<source>.repos` list converts against:
// the workspace's remotes, the unqualified writes of the merged workspace, the host each source
// names repositories on, and the names already taken by another identity.
type legacyRepos struct {
	remotes []namedRemote
	lines   map[string][]requestLine
	hosts   map[string]string
	taken   map[string]string
}

// namedRemote is a git remote of the workspace: its name and its repository as host/owner/repo.
type namedRemote struct {
	name, repo string
}

// workspaceRemoteNames reads the workspace's remotes with their names, in config order.
func workspaceRemoteNames(workspace string) []namedRemote {
	if workspace == "" {
		return nil
	}
	var out []namedRemote
	for _, r := range brokerscope.ReadAllRemotes(workspace).Remotes {
		if repo := remoteRepo(r.Repo); repo != "" {
			out = append(out, namedRemote{name: r.Name, repo: repo})
		}
	}
	return out
}

// legacyReposOf gathers what withLegacyReposConverted needs from the graph: the merged
// workspace's unqualified writes, and the names the workspace's `repos` tables already take.
func (g *Graph) legacyReposOf(remotes []namedRemote, declarations []config.ReadableBrokerDeclaration) legacyRepos {
	in := legacyRepos{remotes: remotes, lines: map[string][]requestLine{}, hosts: map[string]string{}, taken: map[string]string{}}
	for _, d := range declarations {
		if d.CallBroker == nil && d.Brokered.RemoteHost != brokerscope.AnyRemoteHost {
			in.hosts[d.Brokered.Source] = d.Brokered.RemoteHost
		}
	}
	for _, r := range remotes {
		in.taken[r.name] = r.repo
	}
	for _, f := range g.project {
		if f.doc == nil {
			continue
		}
		if repos, ok := mapAt(f.doc.Values, "repos"); ok {
			for _, name := range repos.Keys() {
				if table, ok := mapAt(repos, name); ok {
					if repo, ok := table.Get("repo"); ok {
						in.taken[name] = fmt.Sprint(repo)
					}
				}
			}
		}
		requests, ok := mapAt(f.doc.Values, "requests")
		if !ok {
			continue
		}
		for _, source := range requests.Keys() {
			table, ok := mapAt(requests, source)
			if !ok {
				continue
			}
			for _, kind := range table.Keys() {
				if kind == "repos" || kind == "read" {
					continue
				}
				v, _ := table.Get(kind)
				in.add(source, kind, v)
			}
		}
	}
	return in
}

// add merges one unqualified line into the workspace's writes: a list's refs join in order and
// without repeats, any other value takes the later file's word.
func (in legacyRepos) add(source, kind string, value any) {
	for i, l := range in.lines[source] {
		if l.kind != kind {
			continue
		}
		list, isList := value.([]any)
		prior, priorIsList := l.value.([]any)
		if !isList || !priorIsList {
			in.lines[source][i].value = value
			return
		}
		for _, item := range list {
			if !containsValue(prior, item) {
				prior = append(prior, item)
			}
		}
		in.lines[source][i].value = prior
		return
	}
	if list, ok := value.([]any); ok {
		value = append([]any(nil), list...)
	}
	in.lines[source] = append(in.lines[source], requestLine{kind: kind, value: value})
}

// unionNamedRequest is the value a named table's request line holds once a second value lands on
// the same line: a list's refs join in order without repeats, and a yes/no kind stays true. ok is
// false for any other path or pair, which keep the refusal they had. The copied unqualified line
// and an explicit grant on the same repository are both the old reader's union (GW-D80).
func unionNamedRequest(path []string, prior, value any) (any, bool) {
	if len(path) != 5 || path[0] != "repos" || path[2] != "requests" {
		return nil, false
	}
	switch p := prior.(type) {
	case bool:
		v, ok := value.(bool)
		return p || v, ok
	case []any:
		list, ok := value.([]any)
		if !ok {
			return nil, false
		}
		out := append([]any(nil), p...)
		for _, item := range list {
			if !containsValue(out, item) {
				out = append(out, item)
			}
		}
		return out, true
	}
	return nil, false
}

func containsValue(list []any, v any) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// nameUnsafe is a character no table name keeps.
var nameUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// repoTableName is the repository's last segment with each nameUnsafe character replaced by `-`.
func repoTableName(repo string) string {
	return nameUnsafe.ReplaceAllString(repo[strings.LastIndex(repo, "/")+1:], "-")
}

// ownerRepoName is the owner and repository joined: the name taken when the short one is taken by
// another identity (GW-D80's naming).
func ownerRepoName(repo string) string {
	parts := strings.Split(repo, "/")
	return nameUnsafe.ReplaceAllString(strings.Join(parts[len(parts)-2:], "-"), "-")
}

// claimRepoName is the named table a repository takes, claimed for it: its short name, else its
// owner-repo name when the short one is another repository's or is `all`, which a remote may not
// take as a table name (GW-D80). ok is false when the owner-repo name is taken by another repository too.
func (in *legacyRepos) claimRepoName(repo string) (name string, ok bool) {
	name = repoTableName(repo)
	if owner, taken := in.taken[name]; name == "all" || (taken && owner != repo) {
		name = ownerRepoName(repo)
	}
	if owner, taken := in.taken[name]; taken && owner != repo {
		return name, false
	}
	in.taken[name] = repo
	return name, true
}

// originRepo is the repository of the workspace's remote named origin, "" when there is none.
func (in *legacyRepos) originRepo() string {
	for _, r := range in.remotes {
		if r.name == "origin" {
			return r.repo
		}
	}
	return ""
}

// plainRepoEntry is an owner/repo entry: a pattern or wildcard has no named table.
func plainRepoEntry(text string) bool {
	return !strings.Contains(text, "*") && strings.Count(text, "/") == 1 && !strings.HasPrefix(text, "/")
}

// repoListFile is the kind of file a repository list is written in, which decides what a
// retained entry's reason may say about the file.
type repoListFile int

const (
	// legacyJSONCList is a `brokered.<source>` table of a legacy JSONC file: yolo keeps reading it
	// with the entry in it (RC-D19's window).
	legacyJSONCList repoListFile = iota
	// tomlRequestsList is a TOML file's `requests.<source>.repos`: the launch refuses the file
	// while the key holds an entry (GW-D77), so a retained entry has to leave it.
	tomlRequestsList
)

// declareRepoVerb is the command that declares a repository as its own `[repos.<alias>]` table.
const declareRepoVerb = "`yolo request repos <alias> <host>/<path>`"

// notPlainRepoReason is why an entry that is not plain stays in its old file, with its next step:
// the verb that declares a repository, and what the file holding the entry needs.
func notPlainRepoReason(text, source string, file repoListFile) string {
	why := fmt.Sprintf("%q is not a plain owner/repo; a pattern or wildcard entry has no named table (GW-D80). ", text)
	if file == tomlRequestsList {
		return why + fmt.Sprintf("yolo refuses this file while the entry is in it: remove it with `yolo config unset 'requests.%s.repos[<n>]'`, and name the repository with %s if the project still needs it", source, declareRepoVerb)
	}
	return why + "Leave it in this file, which yolo keeps reading, or name the repository with " + declareRepoVerb
}

// noHostReason is why an entry of source's list stays when no broker declares a host for source.
func noHostReason(source string, file repoListFile) string {
	why := "no broker declares a host for " + source + "; "
	if file == tomlRequestsList {
		return why + fmt.Sprintf("yolo refuses this file while the entry is in it: select the pack that declares %s and run `yolo config migrate` again, or remove the entry with `yolo config unset 'requests.%s.repos[<n>]'`", source, source)
	}
	return why + "leave the old file, which yolo keeps reading"
}

// repoTable is one named table a `requests.<source>.repos` entry or a non-origin remote converts
// to: repo is its `repo` line, "" for a remote's table; lines are the copies it takes.
type repoTable struct {
	name, repo, source string
	// remote is the remote's repository a remote's table is named for, "" for a listed repository.
	remote string
	lines  []requestLine
}

// convert names the tables the entries of source's `repos` list become, the list's entries that
// stay (a wildcard, or a name taken by another identity), and the reason each stays. A name is
// claimed as it is given, so a later entry or remote cannot take it. file is the kind of file the
// list is written in, for the reason a retained entry gives.
func (in *legacyRepos) convert(source string, entries []any, logical string, file repoListFile) ([]repoTable, []any, []NotConverted) {
	var tables []repoTable
	var kept []any
	var skipped []NotConverted
	path := "requests." + source + ".repos"
	for _, entry := range entries {
		text, _ := entry.(string)
		if !plainRepoEntry(text) {
			kept = append(kept, entry)
			skipped = append(skipped, NotConverted{File: logical, Path: path, Reason: notPlainRepoReason(text, source, file)})
			continue
		}
		host := in.hosts[source]
		if host == "" {
			kept = append(kept, entry)
			skipped = append(skipped, NotConverted{File: logical, Path: path, Reason: noHostReason(source, file)})
			continue
		}
		repo := host + "/" + text
		name, ok := in.claimRepoName(repo)
		if !ok {
			kept = append(kept, entry)
			skipped = append(skipped, NotConverted{File: logical, Path: path, Reason: fmt.Sprintf("the name %s is taken by another repository in this workspace; run `yolo request repos <alias> %s`, then `yolo config migrate` again", name, repo)})
			continue
		}
		tables = append(tables, repoTable{name: name, repo: repo, source: source, lines: in.lines[source]})
	}
	if len(in.lines[source]) == 0 {
		return tables, kept, skipped
	}
	// Before GW-D79 every unqualified line also reached each other remote of the workspace.
	for _, r := range in.remotes {
		if r.name == "origin" {
			continue
		}
		if r.name == "all" {
			// `all` is reserved as a table name, so the remote's writes take its owner-repo table,
			// which names the repository itself.
			name, ok := in.claimRepoName(r.repo)
			if !ok {
				skipped = append(skipped, NotConverted{File: logical, Path: path, Reason: fmt.Sprintf("the name %s is taken by another repository in this workspace; run `yolo request repos <alias> %s`, then `yolo config migrate` again", name, r.repo)})
				continue
			}
			tables = append(tables, repoTable{name: name, repo: r.repo, source: source, lines: in.lines[source]})
			continue
		}
		if owner, taken := in.taken[r.name]; taken && owner != r.repo {
			skipped = append(skipped, NotConverted{File: logical, Path: path, Reason: fmt.Sprintf("the name %s is taken by another repository in this workspace; run `yolo request repos <alias> %s`, then `yolo config migrate` again", r.name, r.repo)})
			continue
		}
		in.taken[r.name] = r.repo
		tables = append(tables, repoTable{name: r.name, remote: r.repo, source: source, lines: in.lines[source]})
	}
	return tables, kept, skipped
}

// withLegacyReposConverted rewrites, in the TOML document data, each `requests.<source>.repos`
// list as the named tables that reach the same repositories (GW-D79, GW-D80): each listed
// repository becomes `[repos.<name>] repo = "<host>/<path>"` with a copy of every unqualified
// write under `[repos.<name>.requests.<source>]`; each non-origin remote of the workspace gets a
// table of the same copies, named for the remote and without a repo line. An entry that stays is
// left in the list with its reason, and the list is removed once none remains. It returns the new
// bytes and one line per retained write, as the transcript prints it.
func withLegacyReposConverted(data []byte, logical string, in *legacyRepos) ([]byte, []string, []NotConverted, error) {
	var rewrote []string
	var skipped []NotConverted
	for _, source := range legacyRepoSources(data) {
		doc, err := configdoc.Parse(data, configdoc.TOML)
		if err != nil {
			return nil, nil, nil, err
		}
		requests, _ := mapAt(doc.Values, "requests")
		table, _ := mapAt(requests, source)
		list, _ := table.Get("repos")
		entries, _ := list.([]any)
		tables, kept, reasons := in.convert(source, entries, logical, tomlRequestsList)
		skipped = append(skipped, reasons...)
		for _, t := range tables {
			if t.repo != "" {
				if data, err = applyMutation(data, configedit.Mutation{Action: configedit.Add,
					Path: []configdoc.Step{{Key: "repos"}, {Key: t.name}, {Key: "repo"}}, Value: t.repo}); err != nil {
					return nil, nil, nil, err
				}
			}
			for _, l := range t.lines {
				if data, err = applyMutation(data, configedit.Mutation{Action: configedit.Add,
					Path:  []configdoc.Step{{Key: "repos"}, {Key: t.name}, {Key: "requests"}, {Key: source}, {Key: l.kind}},
					Value: l.value}); err != nil {
					return nil, nil, nil, err
				}
				rewrote = append(rewrote, fmt.Sprintf("repos.%s.requests.%s.%s = %s (%s)", t.name, source, l.kind, jsonx.MustDumpsCompact(l.value), t.where()))
			}
		}
		if data, err = setRepoList(data, source, kept); err != nil {
			return nil, nil, nil, err
		}
	}
	return data, rewrote, skipped, nil
}

// where names the repository a table's writes are for: its repo line, or the remote a remote's
// table is named for and the repository that remote points at.
func (t repoTable) where() string {
	if t.repo != "" {
		return t.repo
	}
	return "remote " + t.name + " = " + t.remote
}

// legacyRepoSources are the sources whose `requests.<source>.repos` key the TOML document holds,
// in file order.
func legacyRepoSources(data []byte) []string {
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		return nil
	}
	requests, ok := mapAt(doc.Values, "requests")
	if !ok {
		return nil
	}
	var out []string
	for _, source := range requests.Keys() {
		if table, ok := mapAt(requests, source); ok {
			if _, has := table.Get("repos"); has {
				out = append(out, source)
			}
		}
	}
	return out
}

// lowerLegacyRepos is the BEFORE side's spelling of the same conversion, in memory: each
// `requests.<source>.repos` list becomes the named tables withLegacyReposConverted writes, so the
// proof compares the spelling the rewritten files read as. The grants themselves are compared
// as the old and new readers make them (brokerGrantsLayer), so this lowering adds no authority.
func lowerLegacyRepos(values *jsonx.OrderedMap, in *legacyRepos) {
	if values == nil {
		return
	}
	requests, ok := mapAt(values, "requests")
	if !ok {
		return
	}
	for _, source := range requests.Keys() {
		table, ok := mapAt(requests, source)
		if !ok {
			continue
		}
		list, has := table.Get("repos")
		if !has {
			continue
		}
		entries, _ := list.([]any)
		tables, kept, _ := in.convert(source, entries, "", tomlRequestsList)
		if len(tables) > 0 {
			repos, ok := mapAt(values, "repos")
			if !ok {
				repos = jsonx.NewOrderedMap()
				values.Set("repos", repos)
			}
			addNamedRepos(repos, source, tables)
		}
		table.Delete("repos")
		if len(kept) > 0 {
			table.Set("repos", kept)
		}
	}
}

// addNamedRepos sets each table of source in repos as lowerLegacyRepos spells it.
// A table another source already holds keeps its repo line and gains this source's lines.
func addNamedRepos(repos *jsonx.OrderedMap, source string, tables []repoTable) {
	for _, t := range tables {
		named, ok := mapAt(repos, t.name)
		if !ok {
			named = jsonx.NewOrderedMap()
			repos.Set(t.name, named)
		}
		if t.repo != "" {
			named.Set("repo", t.repo)
		}
		if len(t.lines) == 0 {
			continue
		}
		requests, ok := mapAt(named, "requests")
		if !ok {
			requests = jsonx.NewOrderedMap()
			named.Set("requests", requests)
		}
		written, ok := mapAt(requests, source)
		if !ok {
			written = jsonx.NewOrderedMap()
			requests.Set(source, written)
		}
		for _, l := range t.lines {
			written.Set(l.kind, l.value)
		}
	}
}

// setRepoList leaves the list of source's `repos` key as entries; an empty list removes the key.
func setRepoList(data []byte, source string, entries []any) ([]byte, error) {
	key := []configdoc.Step{{Key: "requests"}, {Key: source}, {Key: "repos"}}
	if len(entries) == 0 {
		return applyMutation(data, configedit.Mutation{Action: configedit.Remove, Path: key})
	}
	return applyMutation(data, configedit.Mutation{Action: configedit.Set, Path: key, Value: entries})
}

func applyMutation(data []byte, m configedit.Mutation) ([]byte, error) {
	edit, err := configedit.PlanEdit(data, configdoc.TOML, m)
	if err != nil {
		return nil, err
	}
	return edit.Updated, nil
}
