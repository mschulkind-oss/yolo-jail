package ghbroker

import (
	"encoding/csv"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Native fuzz targets for the scope decision (docs/design/boundary-broker.md BB-D62). The host
// gh holds a broad login (repo, read:org, gist), so the broker's parse is the only thing that
// keeps a jail's call inside its repository scope, and a gap in it is full access. Each target
// throws argv at Classify and hands every call it would run (standing or windowed) to an
// ORACLE: an independent reading of the canonical argv, the way gh 2.101.0 and GitHub would
// read it, written from the grammar table and from what gh was MEASURED to send (BB-D58 to
// BB-D61), not from the classifier's own code. A call the broker would run must reach
// only repositories in scope, never an organization, a user or the whole account, unless it is
// one of the few commands that read nothing the login makes private.
//
// Run one for longer than the seed corpus with, for example,
//
//	go test -run XXX -fuzz FuzzClassify -fuzztime 2m ./internal/ghbroker/
//
// An input that fails is written under testdata/fuzz/<target>/, where `go test` replays it
// from then on; keep it there with the fix.

var fuzzScope = NewScope([]string{"o/r", "me/r-fork"})

// fuzzSeeds are the cases an agent's field report asked for (.. in paths, ? and # in
// endpoints, --hostname, -F field=@file, repeated -X, flags after --, endpoints with a scheme
// or a host), the shapes the classifier's tables name, and what the oracle found.
var fuzzSeeds = [][]string{
	{"api", "repos/o/r/pulls"},
	{"api", "repos/o/r/branches/MS%2Fmain"},
	{"api", "repos/o/r/../../other/x"},
	{"api", "repos/o/r/%2e%2e/%2e%2e/other/x"},
	{"api", "repos/o%2Fother/r"},
	{"api", "repos/o/r?x=1/../../other"},
	{"api", "repos/o/r#/../../other/x"},
	{"api", "repos/o/r/issues?per_page=1#frag"},
	{"api", "https://api.github.com/repos/other/x"},
	{"api", "//other/x"},
	{"api", "//api.github.com/repos/other/x"},
	{"api", "@other/x"},
	{"api", "repos/o/r", "--hostname", "evil.example"},
	{"api", "repos/o/r", "--hostname=github.com"},
	{"api", "-h", "repos/o/r"},
	{"api", "-F", "body=@/etc/passwd", "repos/o/r/issues"},
	{"api", "--field", "body=@/etc/passwd", "repos/o/r/issues"},
	{"api", "-f", "body=@x", "repos/o/r/issues"},
	{"api", "--input", "/etc/passwd", "repos/o/r/issues"},
	{"api", "--input", "-", "repos/o/r/issues"},
	{"api", "-X", "GET", "-X", "DELETE", "repos/o/r"},
	{"api", "-X", "get", "repos/o/r"},
	{"api", "repos/o/r", "--", "--hostname=evil"},
	{"api", "--", "repos/other/x"},
	{"api", "graphql", "-f", "query={viewer{login}}"},
	{"api", "/graphql", "-f", "query={viewer{login}}"},
	{"api", "repos/{owner}/{repo}/pulls"},
	{"api", "repos/{owner}/{repo}/../../other/x"},
	{"api", "user"},
	{"api", "repositories/1/pulls"},
	{"api", "repos/:owner/:repo/pulls"},
	{"api", "repos/o/r/contents/{{owner}wner}"},
	{"api", "repos/o/r/commits?sha=:branch"},
	{"api", "-X", "GET", "-F", "x=:repo", "repos/o/r/issues"},
	{"api", "-X", "GET", "-f", "x={{repo}epo}", "repos/o/r/issues"},
	{"pr", "view", "1", "-R", "o/r"},
	{"-R", "o/r", "pr", "view", "1"},
	{"pr", "view", "1", "-R", "other/x"},
	{"pr", "view", "1", "-R", "github.com/o/r"},
	{"pr", "view", "1", "-R", "o/r", "-R", "other/x"},
	{"pr", "view", "https://github.com/other/x/pull/1"},
	{"pr", "view", "https://github.com/o/r/issues/1"},
	{"ruleset", "check", "https://github.com/o/r/x"},
	{"run", "view", "https://github.com/o/r/x"},
	{"pr", "view", "https://github.com/o/r/../../other/x/pull/1"},
	{"pr", "view", "https://github.com/o%2Fother/r/pull/1"},
	{"pr", "view", "1", "-R", "o/r", "--", "-1"},
	{"pr", "list", "-S", "repo:other/x", "-R", "o/r"},
	{"search", "issues", "bug", "--repo", "o/r"},
	{"search", "issues", "bug", "--repo", "o/r,me/r-fork"},
	{"search", "issues", "bug", "--repo", `"o/r,other/x"`},
	{"search", "code", "x", "--repo", "o/r", "--owner", "other"},
	{"repo", "view", "o/r"},
	{"repo", "view", "other/x"},
	{"repo", "read-file", "README.md", "-R", "o/r"},
	{"repo", "read-file", "../../../other/x/contents/secret", "-R", "o/r"},
	{"repo", "gitignore", "view", "../../repos/other/x"},
	{"repo", "license", "view", "../repos/other/x"},
	{"run", "view", "../../../other/x", "-R", "o/r"},
	{"run", "view", "--job", "../../../other/x", "-R", "o/r"},
	{"release", "view", "../../../other/x", "-R", "o/r"},
	{"secret", "list", "--env", "../../../other/x", "-R", "o/r"},
	{"secret", "list", "--org", "other", "-R", "o/r"},
	{"secret", "set", "X", "--org", "other", "--body", "y", "-R", "o/r"},
	{"secret", "delete", "X", "--user", "-R", "o/r"},
	{"variable", "set", "X", "--org", "other", "--body", "y", "-R", "o/r"},
	{"variable", "delete", "X", "--org", "other", "-R", "o/r"},
	{"issue", "develop", "1", "--branch-repo", "other/x", "-R", "o/r"},
	{"issue", "transfer", "1", "other/x", "-R", "o/r"},
	{"label", "clone", "other/x", "-R", "o/r"},
	{"pr", "comment", "1", "--body", "./build.sh fails", "-R", "o/r"},
	{"auth", "status"},
	{"auth", "token"},
	{"--version"},
	{"pr", "view", "--help"},
}

func FuzzClassify(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(strings.Join(s, "\x00"), "o/r")
	}
	f.Add("pr\x00view\x001", "")
	f.Add("pr\x00view\x001", "other/x")
	f.Fuzz(func(t *testing.T, blob, field string) {
		checkScopeDecision(t, strings.Split(blob, "\x00"), field)
	})
}

// FuzzAPIEndpoint mutates a `gh api` endpoint alone, with an optional method and field.
func FuzzAPIEndpoint(f *testing.F) {
	for _, s := range fuzzSeeds {
		if s[0] == "api" && len(s) > 1 {
			f.Add(s[1], "", "")
		}
	}
	f.Add("repos/o/r/contents/a%23b%3F.md", "GET", "")
	f.Add("repos/o/r/issues", "POST", "title=x")
	f.Add("repos/{owner}/{repo}/git/refs/heads%2Fx", "", "ref={owner}")
	f.Fuzz(func(t *testing.T, endpoint, method, field string) {
		argv := []string{"api", endpoint}
		if method != "" {
			argv = append(argv, "--method="+method)
		}
		if field != "" {
			argv = append(argv, "--raw-field="+field)
		}
		checkScopeDecision(t, argv, "o/r")
	})
}

// checkScopeDecision is the property every target checks.
func checkScopeDecision(t *testing.T, argv []string, field string) {
	t.Helper()
	d := Classify(argv, field, fuzzScope)
	switch d.Outcome {
	case OutcomeRefused, OutcomeOutOfScope:
		if d.Argv != nil {
			t.Fatalf("gh %q: %s, yet it carries an argv to run: %q", argv, d.Outcome, d.Argv)
		}
		if d.Reason == "" {
			t.Fatalf("gh %q: %s with no reason", argv, d.Outcome)
		}
		return
	case OutcomeStanding, OutcomeWindowed:
	default:
		t.Fatalf("gh %q: unknown outcome %q", argv, d.Outcome)
	}
	if len(d.Argv) == 0 {
		t.Fatalf("gh %q: %s with nothing to run", argv, d.Outcome)
	}
	r := ghReach(d.Argv)
	switch {
	case r.problem != "":
		t.Fatalf("gh %q runs %q (%s): %s", argv, d.Argv, d.Outcome, r.problem)
	case r.account:
		t.Fatalf("gh %q runs %q (%s), which reaches past every repository: %s", argv, d.Argv, d.Outcome, r.why)
	case len(r.repos) == 0 && !r.scopeFree:
		t.Fatalf("gh %q runs %q (%s), which names no repository, so gh would read one from its cwd",
			argv, d.Argv, d.Outcome)
	}
	for _, repo := range r.repos {
		if !fuzzScope.Contains(repo) {
			t.Fatalf("gh %q runs %q (%s), which reaches %s, outside the scope", argv, d.Argv, d.Outcome, repo)
		}
	}
}

// reach is what one canonical argv touches on GitHub, by the oracle's reading.
type reach struct {
	repos     []string
	account   bool
	why       string
	scopeFree bool
	// problem says the canonical argv is one gh would read differently from the broker, or one
	// that reaches the host; a call with one must never be allowed.
	problem string
}

// The oracle's own statements of what reaches past a repository, kept apart from the
// classifier's tables on purpose: a classifier change that lets through a call these say
// reaches past the scope fails a fuzz run, whichever table the change was made in.
var (
	// oracleScopeFree are the commands that read nothing the login makes private: GitHub's
	// public templates, and the login's own status.
	oracleScopeFree = map[string]bool{
		"repo gitignore list": true, "repo gitignore view": true,
		"repo license list": true, "repo license view": true, "auth status": true,
	}
	// oracleAccountFlags send a command past its repository: an organization's or a user's
	// secrets and variables, an organization's rulesets, a search across an owner.
	oracleAccountFlags = map[string][]string{
		"secret list": {"org", "user"}, "secret set": {"org", "user", "repos", "no-repos-selected", "visibility"},
		"secret delete": {"org", "user"},
		"variable list": {"org"}, "variable get": {"org"}, "variable set": {"org", "repos", "visibility"},
		"variable delete": {"org"},
		"ruleset list":    {"org"}, "ruleset view": {"org"},
		"search code": {"owner"}, "search commits": {"owner"}, "search issues": {"owner"}, "search prs": {"owner"},
	}
	// oracleRepoFlags take a repository as their value.
	oracleRepoFlags = map[string][]string{"issue develop": {"branch-repo"}}
	// oracleSearchText are the commands whose positionals and flag values gh sends as search
	// text beside its own repo: qualifier (MEASURED, BB-D46).
	oracleSearchText = map[string]bool{
		"pr list": true, "issue list": true, "discussion list": true,
		"search code": true, "search commits": true, "search issues": true, "search prs": true,
	}
	// oracleFreeText are flags whose value gh sends as text (a request body, a GraphQL variable,
	// a search query), never as part of a REST path.
	oracleFreeText = map[string]bool{
		"body": true, "title": true, "notes": true, "search": true, "description": true,
		"subject": true, "text": true, "query": true, "field": true, "raw-field": true, "json": true,
		"squash-merge-commit-message": true,
	}
	oracleWidensRE = regexp.MustCompile(`(?i)(^|\W)(repo|org|user|owner):|[()]|(^|\W)(OR|NOT)(\W|$)`)
	// oraclePlaceholderRE is what gh 2.101.0 fills (MEASURED): {owner}, {repo} and {branch},
	// and :owner, :repo and :branch ending at a word boundary.
	oraclePlaceholderRE = regexp.MustCompile(`\{(owner|repo|branch)\}|:(owner|repo|branch)\b`)
)

// ghReach reads a canonical argv as gh would.
func ghReach(argv []string) reach {
	if len(argv) == 1 && argv[0] == "--version" {
		return reach{scopeFree: true}
	}
	// The command: cobra walks the leading words while each names a child.
	k := 0
	path := ""
	for k < len(argv) && !strings.HasPrefix(argv[k], "-") {
		next := argv[k]
		if path != "" {
			next = path + " " + argv[k]
		}
		if grammar[next] == nil {
			break
		}
		path = next
		k++
	}
	cmd := grammar[path]
	if cmd == nil {
		return reach{problem: "gh resolves no built-in command from " + strings.Join(argv, " ")}
	}
	if isGroup(path) {
		return reach{problem: "`gh " + path + "` is a group"}
	}

	// The flags, as pflag reads them.
	var flags []flagValue
	var positionals []string
	for i := k; i < len(argv); i++ {
		tok := argv[i]
		switch {
		case tok == "--":
			positionals = append(positionals, argv[i+1:]...)
			i = len(argv)
		case strings.HasPrefix(tok, "--"):
			name, val, hasEq := strings.Cut(tok[2:], "=")
			var f *ghFlag
			for j := range cmd.flags {
				if cmd.flags[j].long == name {
					f = &cmd.flags[j]
				}
			}
			if f == nil {
				return reach{problem: "gh has no flag --" + name + " on `gh " + path + "`"}
			}
			if f.value && !hasEq {
				return reach{problem: "gh would take the next word as --" + name + "'s value"}
			}
			flags = append(flags, flagValue{name, val})
		case strings.HasPrefix(tok, "-") && tok != "-":
			return reach{problem: "gh would read " + tok + " as short flags"}
		default:
			positionals = append(positionals, tok)
		}
	}
	has := func(long string) bool {
		for _, u := range flags {
			if u.long == long {
				return true
			}
		}
		return false
	}
	if has("help") {
		return reach{scopeFree: true}
	}
	for _, long := range []string{"hostname", "jq", "web", "editor"} {
		if has(long) {
			return reach{problem: "--" + long + " reaches the host or another server"}
		}
	}
	r := reach{scopeFree: oracleScopeFree[path]}
	addRepo := func(v string) bool {
		owner, name, ok := strings.Cut(v, "/")
		if !ok || owner == "" || name == "" || strings.ContainsAny(name, "/:") || strings.Contains(owner, ":") {
			r.problem = "gh would resolve " + v + " as something other than OWNER/REPO"
			return false
		}
		r.repos = append(r.repos, strings.ToLower(v))
		return true
	}
	for _, u := range flags {
		for _, long := range oracleAccountFlags[path] {
			if u.long == long {
				r.account, r.why = true, "--"+long
			}
		}
		for _, long := range oracleRepoFlags[path] {
			if u.long == long && !addRepo(u.value) {
				return r
			}
		}
		if u.long == "repo" {
			// A search's --repo is a pflag string slice, split as CSV (MEASURED); every other
			// command's names one repository.
			values := []string{u.value}
			if strings.HasPrefix(path, "search ") {
				var err error
				if values, err = csv.NewReader(strings.NewReader(u.value)).Read(); err != nil {
					return reach{scopeFree: true} // gh refuses the flag
				}
			}
			for _, v := range values {
				if !addRepo(v) {
					return r
				}
			}
		}
		if (u.long == "field" || u.long == "input") && path == "api" {
			_, v, _ := strings.Cut(u.value, "=")
			if (u.long == "field" && strings.HasPrefix(v, "@")) || (u.long == "input" && u.value != "-") {
				r.problem = "--" + u.long + " reads a host file"
				return r
			}
		}
	}
	if oracleSearchText[path] {
		for _, v := range positionals {
			if oracleWidensRE.MatchString(v) {
				r.account, r.why = true, "search text "+v
			}
		}
		for _, u := range flags {
			if oracleWidensRE.MatchString(u.value) {
				r.account, r.why = true, "search text --"+u.long
			}
		}
	}

	if path == "api" {
		// gh fills its placeholders in the endpoint and in a --field value, never in a
		// --raw-field value, a header or a preview name (MEASURED).
		filled := append([]string(nil), positionals...)
		for _, u := range flags {
			if u.long == "field" {
				_, v, _ := strings.Cut(u.value, "=")
				filled = append(filled, v)
			}
		}
		for _, v := range filled {
			if oraclePlaceholderRE.MatchString(v) {
				r.problem = "gh fills the placeholder in " + v + " from the git checkout it finds from its cwd"
				return r
			}
		}
		if len(positionals) != 1 {
			r.problem = "gh api takes one endpoint"
			return r
		}
		return apiReach(r, positionals[0])
	}

	// A word gh puts into a REST path segment (MEASURED: run view's id, run view --job, release
	// view's tag, repo read-file's path, --env, gitignore and license names, all path-escaped):
	// a dot segment there climbs out of the repository's path for a server that decodes a %2F
	// and resolves dot segments.
	if !strings.HasPrefix(path, "search ") {
		for _, v := range positionals {
			if climbs(v) {
				r.problem = "the argument " + v + " has a dot segment gh would put into a REST path"
				return r
			}
		}
		for _, u := range flags {
			if !oracleFreeText[u.long] && climbs(u.value) {
				r.problem = "--" + u.long + " " + u.value + " has a dot segment gh would put into a REST path"
				return r
			}
		}
	}

	// A command that takes --repo and was given none asks the git checkout gh finds from its cwd
	// for its repository, wherever an argument does not name one gh reads (MEASURED: gh reads a
	// github.com URL only where it expects one, so `ruleset check URL` and `pr view` of an
	// issue's URL both asked the checkout).
	if cmd.flagByLong("repo") != nil && !has("repo") {
		r.problem = "`gh " + path + "` carries no --repo, so gh reads its repository from the git checkout it finds from its cwd"
		return r
	}

	// Positionals that name a repository: a github.com URL, or a usage placeholder naming one.
	slots := usageSlots(cmd)
	for i, v := range positionals {
		if strings.Contains(v, "://") {
			if !strings.HasPrefix(v, "https://github.com/") {
				r.problem = "the URL " + v + " is off github.com"
				return r
			}
			u, err := url.Parse(v)
			if err != nil {
				continue // gh fails to parse it too
			}
			for _, p := range []string{u.Path, u.EscapedPath()} {
				segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
				if len(segs) >= 2 && !addRepo(segs[0]+"/"+segs[1]) {
					return r
				}
			}
			continue
		}
		slot := ""
		if i < len(slots) {
			slot = slots[i]
		} else if len(slots) > 0 && strings.HasSuffix(slots[len(slots)-1], "...") {
			slot = slots[len(slots)-1]
		}
		if strings.Contains(strings.ToLower(slot), "repo") && !addRepo(v) {
			return r
		}
	}
	return r
}

type flagValue struct{ long, value string }

// apiReach reads a `gh api` endpoint as gh builds its URL (https://api.github.com/ plus the
// endpoint with one leading / trimmed; the endpoint used whole when it carries ://; `graphql`
// sent to the GraphQL endpoint) and as a server might read the path gh sends: split then
// decoded per segment, decoded once whole, decoded until it stops changing, with a backslash
// read as a slash, with empty segments collapsed, each with its dot segments resolved. Every
// reading must reach the same in-scope repository.
func apiReach(r reach, endpoint string) reach {
	if strings.Contains(endpoint, "://") {
		r.problem = "gh uses the endpoint " + endpoint + " as the whole URL"
		return r
	}
	p := strings.TrimPrefix(endpoint, "/")
	if p == "graphql" {
		r.account, r.why = true, "GraphQL"
		return r
	}
	u, err := url.Parse("https://api.github.com/" + p)
	if err != nil {
		return reach{scopeFree: true} // gh builds no request
	}
	if u.Host != "api.github.com" || u.User != nil {
		r.problem = "the request goes to " + u.Host
		return r
	}
	sent := strings.TrimPrefix(u.EscapedPath(), "/")
	var readings [][]string
	if segs, ok := decodeEach(strings.Split(sent, "/")); ok {
		readings = append(readings, segs)
	}
	whole := sent
	for i := 0; i < 3; i++ {
		d, err := url.PathUnescape(whole)
		if err != nil {
			break
		}
		readings = append(readings, strings.Split(d, "/"),
			strings.Split(strings.ReplaceAll(d, "\\", "/"), "/"), dropEmpty(strings.Split(d, "/")))
		if d == whole {
			break
		}
		whole = d
	}
	for _, segs := range readings {
		segs = resolveDots(segs)
		if len(segs) < 3 || segs[0] != "repos" {
			r.account, r.why = true, "the path /"+strings.Join(segs, "/")
			return r
		}
		if !addRepoTo(&r, segs[1]+"/"+segs[2]) {
			return r
		}
	}
	return r
}

func addRepoTo(r *reach, v string) bool {
	owner, name, ok := strings.Cut(v, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		r.problem = "a server could read " + v + " as the repository"
		return false
	}
	r.repos = append(r.repos, strings.ToLower(v))
	return true
}

func decodeEach(segs []string) ([]string, bool) {
	out := make([]string, len(segs))
	for i, s := range segs {
		d, err := url.PathUnescape(s)
		if err != nil {
			return nil, false
		}
		out[i] = d
	}
	return out, true
}

func dropEmpty(segs []string) []string {
	var out []string
	for _, s := range segs {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// resolveDots is RFC 3986's remove_dot_segments over a split path.
func resolveDots(segs []string) []string {
	var out []string
	for _, s := range segs {
		switch s {
		case ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, s)
		}
	}
	return out
}

// climbs reports whether v, as typed or decoded up to twice, has a . or .. segment split on
// / or \.
func climbs(v string) bool {
	for i := 0; i < 3; i++ {
		for _, seg := range strings.FieldsFunc(v, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == "." || seg == ".." {
				return true
			}
		}
		if v == "." || v == ".." {
			return true
		}
		d, err := url.PathUnescape(v)
		if err != nil || d == v {
			return false
		}
		v = d
	}
	return false
}

// usageSlots splits a command's usage line into its positional placeholders, keeping a
// bracketed alternative such as `[<number> | <url> | <branch>]` whole.
func usageSlots(c *ghCommand) []string {
	rest := strings.TrimPrefix(c.usage, "gh "+c.path)
	var slots []string
	depth, start := 0, -1
	for i, ch := range rest {
		switch ch {
		case '[', '{', '<':
			if depth == 0 {
				start = i
			}
			depth++
		case ']', '}', '>':
			depth--
			if depth == 0 && start >= 0 {
				end := i + 1
				for end < len(rest) && rest[end] == '.' {
					end++
				}
				slots = append(slots, rest[start:end])
				start = -1
			}
		}
	}
	var out []string
	for _, s := range slots {
		if s == "[flags]" || strings.HasPrefix(s, "[-- ") {
			continue
		}
		out = append(out, s)
	}
	return out
}

// The oracle is only worth something if it catches what it is for: each of these canonical
// argvs reaches past the scope, or the host, and the oracle must say so.
func TestTheFuzzOracleCatchesWhatItIsFor(t *testing.T) {
	for _, argv := range [][]string{
		{"pr", "view", "--repo=other/x", "1"},
		{"pr", "view", "--repo=o/r", "--repo=other/x", "1"},
		{"pr", "view", "--repo=github.com/o/r", "1"},
		{"pr", "view", "https://github.com/other/x/pull/1"},
		{"pr", "view", "https://github.com/o/r/issues/1"},
		{"ruleset", "check", "https://github.com/o/r/x"},
		{"run", "view", "https://github.com/o/r/x"},
		{"repo", "view", "other/x"},
		{"label", "clone", "--repo=o/r", "other/x"},
		{"issue", "transfer", "--repo=o/r", "1", "other/x"},
		{"issue", "develop", "--branch-repo=other/x", "--repo=o/r", "1"},
		{"secret", "set", "--org=other", "--body=x", "--repo=o/r", "X"},
		{"search", "issues", "--repo=o/r", "repo:other/x"},
		{"run", "view", "--repo=o/r", "../../../other/x"},
		{"run", "view", "--job=../../../other/x", "--repo=o/r"},
		{"repo", "gitignore", "view", "..%2F..%2Frepos%2Fother%2Fx"},
		{"api", "repos/other/x"},
		{"api", "repos/o/r/../../other/x"},
		{"api", "repos/o/r/%2e%2e/%2e%2e/other/x"},
		{"api", "repos/o%2Fother/r"},
		{"api", "repos/o/r/x%252F..%252F..%252F..%252Fother%252Fx"},
		{"api", `repos/o/r/a\..\..\..\other\x`},
		{"api", "graphql"},
		{"api", "user"},
		{"api", "https://api.github.com/repos/o/r"},
		{"api", "--field=x=@/etc/passwd", "repos/o/r/issues"},
		{"api", "repos/o/r/contents/{owner}"},
		{"api", "repos/o/r/contents/:owner"},
		{"api", "repos/o/r/commits?sha=:branch"},
		{"api", "--method=GET", "--field=x=a:repo", "repos/o/r/issues"},
		{"pr", "view", "1"},
		{"pr", "view", "--repo=o/r", "-1"},
		{"pr", "view", "--repo", "o/r", "1"},
	} {
		r := ghReach(argv)
		inScope := r.problem == "" && !r.account && (len(r.repos) > 0 || r.scopeFree)
		for _, repo := range r.repos {
			if !fuzzScope.Contains(repo) {
				inScope = false
			}
		}
		if inScope {
			t.Errorf("the oracle reads %q as in scope (%+v)", argv, r)
		}
	}
	for _, argv := range [][]string{
		{"pr", "view", "--repo=o/r", "1"},
		{"pr", "view", "https://github.com/o/r/pull/1", "--repo=o/r"},
		{"ruleset", "check", "https://github.com/o/r/x", "--repo=o/r"},
		{"api", "repos/o/r/contents/:ownerx"},
		{"api", "--method=GET", "--raw-field=x={owner}", "repos/o/r/issues"},
		{"api", "--preview=x", "repos/o/r"},
		{"api", "repos/o/r/branches/MS%2Fmain"},
		{"api", "repos/o/r/contents/a%23b.md?ref=main"},
		{"repo", "view", "me/r-fork"},
		{"repo", "gitignore", "list"},
		{"pr", "comment", "--body=./build.sh fails", "--repo=o/r", "1"},
	} {
		r := ghReach(argv)
		if r.problem != "" || r.account || (len(r.repos) == 0 && !r.scopeFree) {
			t.Errorf("the oracle reads %q as out of scope (%+v)", argv, r)
		}
	}
}
