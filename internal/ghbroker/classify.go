package ghbroker

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// classify.go answers the classifier's one question per call
// (docs/design/boundary-broker.md §5): which permission set does this command belong to,
// and is its repository in scope? The four outcomes are checked in the design's order —
// refused, out of scope, a standing set, a windowed set — so no set and no grant can reach
// a refused command (BB-P8).

// Outcome is the classifier's verdict.
type Outcome string

const (
	// OutcomeRefused: never run, whatever set the jail holds (§5.4).
	OutcomeRefused Outcome = "refused"
	// OutcomeOutOfScope: its repository is outside the scope, or it names none the broker
	// can check (§5.6).
	OutcomeOutOfScope Outcome = "out-of-scope"
	// OutcomeStanding: a standing set admits it; it runs at once.
	OutcomeStanding Outcome = "standing"
	// OutcomeWindowed: only a windowed set admits it; it runs only under a grant.
	OutcomeWindowed Outcome = "windowed"
)

// The GitHub source's default set names (§5.7).
const (
	SetReadOnly  = "read-only"
	SetReadWrite = "read-write"
)

// Decision is one classified call.
type Decision struct {
	Outcome Outcome
	// Set is the permission set the command belongs to: "read-only" or "read-write" for a
	// command that may run, "" otherwise.
	Set string
	// Reason says why a refused or out-of-scope command does not run.
	Reason string
	// Path is the canonical command path ("pr view"), "" when the argv did not parse.
	Path string
	// Argv is the canonical argv the broker would run, after `gh`. It is rebuilt from the
	// parse, never the jail's spelling (§4.1), with the repository made explicit.
	Argv []string
	// Repos are the repositories the command touches.
	Repos []string
	// ReadsStdin says the canonical argv reads the forwarder's standard input.
	ReadsStdin bool
	// AccountWide says an out-of-scope command names no repository the broker can check.
	AccountWide bool
}

// Classify decides one call. argv is everything after `gh`; fieldRepo is the repository the
// forwarder resolved in the jail, a convenience and never an authority (§4.3): it is used
// only where the argv names none, and it is checked against scope like any other.
func Classify(argv []string, fieldRepo string, scope Scope) Decision {
	// `gh --version` alone is the one root-flag call an agent makes, to see whether gh is
	// there at all. It reads nothing the login makes private, so it is a read in no repository.
	if len(argv) == 1 && argv[0] == "--version" {
		return Decision{Outcome: OutcomeStanding, Set: SetReadOnly, Path: "--version", Argv: []string{"--version"}}
	}
	p, err := parseArgv(argv)
	if err != nil {
		d := Decision{Outcome: OutcomeRefused, Reason: err.Error()}
		if p != nil && p.cmd != nil {
			d.Path = p.cmd.path
			if why := refusedFor(p); why != "" {
				d.Reason = "`gh " + p.cmd.path + "` is never brokered: " + why
			}
		}
		return d
	}
	d := Decision{Path: p.cmd.path}

	// 1. Refused.
	if why := refusedFor(p); why != "" {
		d.Outcome, d.Reason = OutcomeRefused, "`gh "+p.cmd.path+"` is never brokered: "+why
		return d
	}
	for _, u := range p.flags {
		if why := globalFlagRefusal(p, u); why != "" {
			d.Outcome, d.Reason = OutcomeRefused, why
			return d
		}
	}
	if r, ok := readOnly[p.cmd.path]; ok {
		for _, long := range sortedKeys(r.refuseFlags) {
			if p.has(long) {
				d.Outcome, d.Reason = OutcomeRefused, "--"+long+" is refused on `gh "+
					p.cmd.path+"`: "+r.refuseFlags[long]
				return d
			}
		}
	}
	if rule, ok := hostFilePositionals[p.cmd.path]; ok {
		n := 0
		for _, a := range p.positionals {
			if a != "-" {
				n++
			}
		}
		if n > rule.keep {
			d.Outcome, d.Reason = OutcomeRefused, "`gh "+p.cmd.path+"` is refused with these "+
				"arguments: "+rule.why+", and the broker never reads a host file"
			return d
		}
	}
	for _, a := range p.positionals {
		if strings.Contains(a, "://") && !strings.HasPrefix(a, "https://github.com/") {
			d.Outcome, d.Reason = OutcomeRefused, fmt.Sprintf("argument %q is a URL off "+
				"https://github.com/, and the broker talks to github.com only", a)
			return d
		}
	}
	if why := climbingWord(p); why != "" {
		d.Outcome, d.Reason = OutcomeRefused, why
		return d
	}

	// 2. Scope: which repositories the command touches, made explicit in the argv.
	var sr scopeResult
	switch {
	case p.has("help"):
		// `--help` prints gh's own help text and contacts no one, so it is run alone.
		sr = scopeResult{argv: append(strings.Fields(p.cmd.path), "--help")}
	case p.cmd.path == "api":
		sr = apiScope(p, fieldRepo)
	case searchBacked[p.cmd.path] && queryWidens(p) != "":
		sr = scopeResult{account: true, why: queryWidens(p)}
	case strings.HasPrefix(p.cmd.path, "search "):
		sr = searchScope(p)
	case readOnly[p.cmd.path].scope == scopeNone && readOnly[p.cmd.path].accountFlags == nil &&
		inReadOnly(p.cmd.path):
		sr = scopeResult{argv: p.canonical()}
	case isAccountWide(p.cmd.path):
		sr = scopeResult{account: true}
	default:
		sr = repoScope(p, fieldRepo)
	}
	if sr.refused != "" {
		d.Outcome, d.Reason = OutcomeRefused, sr.refused
		return d
	}
	for _, u := range p.flags {
		if accountFlagOf(p.cmd.path, u.flag.long) {
			sr.account = true
			sr.why = "--" + u.flag.long + " makes it act on an organization's or a user's, not on " +
				"one repository's"
			sr.next = "Leave out --" + u.flag.long + " to act on the repository's own; an " +
				"organization's or a user's is the host user's to run on the host."
			break
		}
	}
	d.Repos = sr.repos
	if sr.account {
		// No widening entry admits an account-wide command (OQ-BB9, ruled A: an entry adds
		// whole repositories), so the message says so rather than naming one.
		d.Outcome, d.AccountWide = OutcomeOutOfScope, true
		d.Reason = "`gh " + p.cmd.path + "` names no repository the broker can check: it reads or " +
			"writes across the account, which no scope admits and no widening entry can add. This " +
			"jail's repository scope is " + scope.describe() + "."
		if sr.why != "" {
			d.Reason = "`gh " + p.cmd.path + "`: " + sr.why + ". No widening entry admits a command " +
				"across the account. This jail's repository scope is " + scope.describe() + "."
		}
		switch {
		case sr.next != "":
			d.Reason += " " + sr.next
		case sr.why == "":
			d.Reason += " A command across the account is the host user's to run on the host, " +
				"outside the jail."
		}
		return d
	}
	for _, r := range sr.repos {
		if !scope.Contains(r) {
			d.Outcome = OutcomeOutOfScope
			d.Reason = r + " is outside this jail's repository scope (" + scope.describe() + "). " +
				scope.widenAdvice(r)
			return d
		}
	}
	d.Argv = sr.argv
	d.ReadsStdin = readsStdin(p)

	// 3. A standing set, then 4. a windowed one.
	if isStandingRead(p, sr) {
		d.Outcome, d.Set = OutcomeStanding, SetReadOnly
		return d
	}
	d.Outcome, d.Set = OutcomeWindowed, SetReadWrite
	return d
}

func inReadOnly(path string) bool {
	_, ok := readOnly[path]
	return ok
}

// isStandingRead reports whether the read-only set admits the command with the flags it
// carries.
func isStandingRead(p *parsed, sr scopeResult) bool {
	if p.has("help") {
		return true
	}
	r, ok := readOnly[p.cmd.path]
	if !ok {
		return false
	}
	for _, f := range r.require {
		if !p.has(f) {
			return false
		}
	}
	if p.cmd.path == "api" {
		return sr.apiRead
	}
	return true
}

// scopeResult is what the scope pass found.
type scopeResult struct {
	repos   []string
	account bool
	// why replaces the account-wide reason for a command that names a repository but
	// whose text could reach past it (queryWidens).
	why string
	// next is the next step an account-wide refusal ends with.
	next    string
	refused string
	argv    []string
	apiRead bool
}

// repoScope finds the repository of a command that names one: its -R, its repository
// positional, a github.com URL among its arguments, or else the forwarder's. The canonical
// argv always names it explicitly.
func repoScope(p *parsed, fieldRepo string) scopeResult {
	var sr scopeResult
	add := func(r string) {
		for _, have := range sr.repos {
			if strings.EqualFold(have, r) {
				return
			}
		}
		sr.repos = append(sr.repos, r)
	}
	for _, a := range p.positionals {
		if strings.HasPrefix(a, "https://github.com/") {
			r := repoFromGitHubURL(a)
			if r == "" {
				sr.refused = fmt.Sprintf("argument %q is a github.com URL that names no repository "+
					"the broker can read plainly: OWNER/REPO as its first two path segments, with no "+
					"percent-encoding. Pass the repository with -R OWNER/REPO and the number instead", a)
				return sr
			}
			add(r)
		}
	}
	argv := p.canonical()
	switch {
	case p.cmd.flagByLong("repo") != nil:
		vals := p.values("repo")
		for _, v := range vals {
			if !ValidRepo(v) {
				return scopeResult{refused: fmt.Sprintf("-R %q is not OWNER/REPO: the broker takes "+
					"exactly two segments, since a HOST/OWNER/REPO sends the login to the host it names", v)}
			}
			add(v)
		}
		if len(vals) == 0 {
			if len(sr.repos) > 0 {
				// A github.com URL argument named the repository, and it goes in as --repo too:
				// gh reads the URL only where it expects one (a pull request's to `pr view`), and
				// anywhere else takes it as a branch, a tag or a path and asks the git checkout
				// it finds from its cwd for the repository (MEASURED: `ruleset check URL` read a
				// checkout's above the broker's empty cwd; H7). Where gh reads the URL, it reads
				// the same repository either way (MEASURED).
				argv = append(argv, "--repo="+sr.repos[0])
				break
			}
			if fieldRepo == "" {
				return scopeResult{account: true, next: "Name the repository with -R OWNER/REPO, or " +
					"run it in a checkout whose origin remote is in scope."}
			}
			if !ValidRepo(fieldRepo) {
				return scopeResult{refused: fmt.Sprintf("the forwarder's repository %q is not OWNER/REPO", fieldRepo)}
			}
			add(fieldRepo)
			argv = append(argv, "--repo="+fieldRepo)
		}
	case repoPositional(p.cmd):
		if len(p.positionals) > 0 {
			first := p.positionals[0]
			switch {
			case strings.HasPrefix(first, "https://github.com/"):
				// Already added from the URL pass.
			case ValidRepo(first):
				add(first)
			default:
				return scopeResult{refused: fmt.Sprintf("argument %q is not OWNER/REPO: the broker "+
					"needs the repository spelled in full to check it against the scope", first)}
			}
		} else {
			if fieldRepo == "" || !ValidRepo(fieldRepo) {
				return scopeResult{account: true, next: "Name the repository: gh " + p.cmd.path +
					" OWNER/REPO."}
			}
			add(fieldRepo)
			argv = append(argv, fieldRepo)
		}
	default:
		if len(sr.repos) == 0 {
			return scopeResult{account: true}
		}
	}
	// A flag whose value is a repository (`issue develop --branch-repo`) names one the scope
	// checks too; gh takes a name or a github.com URL there.
	for _, long := range repoFlags[p.cmd.path] {
		for _, v := range p.values(long) {
			r := v
			if strings.HasPrefix(v, "https://github.com/") {
				r = repoFromGitHubURL(v)
			}
			if !ValidRepo(r) {
				return scopeResult{refused: fmt.Sprintf("--%s %q is not OWNER/REPO: the broker needs "+
					"the repository spelled in full to check it against the scope. Spell it OWNER/REPO", long, v)}
			}
			add(r)
		}
	}
	// A second repository a command names positionally (`issue transfer <destination-repo>`,
	// `label clone <source-repository>`) must be in scope too.
	// An empty one is checked too, rather than left for gh to refuse (found by FuzzClassify).
	if extra, ok := secondRepoPositional(p); ok {
		if !ValidRepo(extra) {
			return scopeResult{refused: fmt.Sprintf("argument %q is not OWNER/REPO: the broker "+
				"needs the repository spelled in full to check it against the scope. Spell it OWNER/REPO", extra)}
		}
		add(extra)
	}
	sr.argv = argv
	return sr
}

// repoPositional reports whether a command's first positional is a repository, from its
// usage line (`gh repo view [<repository>]`).
func repoPositional(c *ghCommand) bool {
	u := strings.Fields(strings.TrimPrefix(c.usage, "gh "+c.path))
	if len(u) == 0 {
		return false
	}
	first := strings.Trim(u[0], "[]<>{}")
	return first == "repository"
}

func secondRepoPositional(p *parsed) (string, bool) {
	switch p.cmd.path {
	case "issue transfer":
		if len(p.positionals) > 1 {
			return p.positionals[1], true
		}
	case "label clone":
		if len(p.positionals) > 0 {
			return p.positionals[0], true
		}
	}
	return "", false
}

// searchBacked are the commands whose query text and filter values gh sends to GitHub's
// search next to a repository qualifier of its own. MEASURED against gh 2.101.0 and a fake
// API (BB-D46): `gh search issues x --repo o/r` sends `( x ) repo:o/r type:issue`, `gh pr
// list -S x -R o/r` sends `( x ) repo:o/r state:open type:pr`, `gh search code x --repo o/r`
// sends `x repo:o/r`, `gh discussion list -S x -R o/r` sends `repo:o/r is:open
// sort:updated-desc x`, and a filter such as `--author` or `--label` becomes `author:"…"`
// with a `"` inside it backslash-escaped. The jail's text sits beside the qualifier gh
// added, not under it.
var searchBacked = map[string]bool{
	"pr list": true, "issue list": true, "discussion list": true,
	"search code": true, "search commits": true, "search issues": true, "search prs": true,
}

var (
	// scopeQualifierRE finds a qualifier that names repositories, owners or organizations.
	// GitHub ORs several `repo:` qualifiers (§5.2), so one in the jail's text widens the
	// search past the one gh added.
	scopeQualifierRE = regexp.MustCompile(`(?i)(^|\W)(repo|org|user|owner):`)
	// boolOperatorRE finds OR or NOT as a word. `x) OR (is:pr` closes gh's parenthesis and
	// ORs the jail's term against the scoped part, and a trailing NOT in `search code`
	// negates the `repo:` gh appends after it. Whether GitHub reads a lower-case `or` as the
	// operator was not measured, so the match ignores case.
	boolOperatorRE = regexp.MustCompile(`(?i)(^|\W)(OR|NOT)(\W|$)`)
)

// queryWidens returns why a search-backed command's text could reach past the repository
// it names, or "". Every positional and every flag value is checked, not only `--search`:
// the filters become quoted qualifiers, and a `"` in one ends gh's quotes if GitHub does
// not honor the backslash, which was not measured. The rule refuses the three things that
// can widen a search, a scope qualifier, a parenthesis and OR or NOT, rather than parse
// GitHub's query grammar.
func queryWidens(p *parsed) string {
	check := func(what, v string) string {
		var why string
		switch {
		case scopeQualifierRE.MatchString(v):
			why = "a repo:, org:, user: or owner: qualifier"
		case strings.ContainsAny(v, "()"):
			why = "a parenthesis"
		case boolOperatorRE.MatchString(v):
			why = "an OR or a NOT"
		default:
			return ""
		}
		return what + " carries " + why + ", which GitHub's search can use to reach past the " +
			"repository gh adds to the query; search with plain words and filter the --json " +
			"output in the jail instead"
	}
	for _, a := range p.positionals {
		if w := check("the query "+strconv.Quote(a), a); w != "" {
			return w
		}
	}
	for _, u := range p.flags {
		if !u.hasValue || isOutputFilter(u.flag) {
			continue
		}
		if w := check("--"+u.flag.long+" "+strconv.Quote(u.value), u.value); w != "" {
			return w
		}
	}
	return ""
}

// isOutputFilter reports whether a flag is one gh applies to its own output after the
// request, `--jq` or the formatting `--template`, which it sends to no one (BB-D64). Its
// text is never search query text, so queryWidens does not read it: a jq filter's
// parenthesis is jq's.
func isOutputFilter(f *ghFlag) bool { return f.long == "jq" || isFormattingTemplate(f) }

// searchScope is §5.2's search rule: in scope only when the complete qualifier set, from
// flags and query text together, names in-scope repositories alone. queryWidens has
// already checked the query text.
func searchScope(p *parsed) scopeResult {
	if p.has("owner") {
		return scopeResult{account: true, next: "Leave out --owner and name the repositories " +
			"with --repo OWNER/REPO."}
	}
	// gh splits --repo as CSV and quotes a value holding a space into the query, `repo:"o/r "`
	// (MEASURED), so each name is checked exactly as gh will send it, never trimmed; a name
	// that is OWNER/REPO holds no quote or space, so CSV and a split on commas agree on it.
	var repos []string
	for _, v := range p.values("repo") {
		for _, r := range strings.Split(v, ",") {
			if !ValidRepo(r) {
				return scopeResult{refused: fmt.Sprintf("--repo %q is not OWNER/REPO. Spell each "+
					"repository OWNER/REPO, separated by commas with no spaces or quotes", r)}
			}
			repos = append(repos, r)
		}
	}
	if len(repos) == 0 {
		return scopeResult{account: true, next: "Add --repo OWNER/REPO naming a repository in scope."}
	}
	return scopeResult{repos: repos, argv: p.canonical()}
}

// writeAccountFlags are the flags that make a command outside the read-only set act on an
// organization's or a user's secret or variable rather than the repository's (BB-D59), as
// readRule.accountFlags do for the read-only set's own. With one present the command is
// account-wide, whatever -R says: `gh secret set X --org acme -R o/r` sets acme's secret.
// --repos, --no-repos-selected and --visibility describe an organization's or a user's
// secret alone, so they count too. TestEveryAccountShapedFlagIsReviewed fails when a
// regenerated grammar adds such a flag nobody placed.
var writeAccountFlags = map[string][]string{
	"secret set":      {"org", "user", "repos", "no-repos-selected", "visibility"},
	"secret delete":   {"org", "user"},
	"variable set":    {"org", "repos", "visibility"},
	"variable delete": {"org"},
}

// accountFlagOf reports whether a flag makes a command account-wide when present.
func accountFlagOf(path, long string) bool {
	for _, f := range readOnly[path].accountFlags {
		if f == long {
			return true
		}
	}
	for _, f := range writeAccountFlags[path] {
		if f == long {
			return true
		}
	}
	return false
}

// repoFlags are flags whose value names a repository, which the scope checks like -R
// (BB-D59): `issue develop --branch-repo` makes the branch in the repository it names.
var repoFlags = map[string][]string{"issue develop": {"branch-repo"}}

func repoValuedFlag(path, long string) bool {
	for _, f := range repoFlags[path] {
		if f == long {
			return true
		}
	}
	return false
}

// freeTextFlags are flags whose value gh sends as text, in a request body, a GraphQL variable
// or a search query, and never as a REST path segment, so their values may hold any dot
// segment (BB-D60). A free-text flag missing from this list fails safe: a value of it with a
// . or .. segment is refused, and the refusal says to write it without one.
var freeTextFlags = map[string]bool{
	"body": true, "title": true, "notes": true, "search": true, "description": true,
	"subject": true, "text": true, "query": true, "field": true, "raw-field": true,
	"squash-merge-commit-message": true,
	// A --jq filter or a formatting --template runs over gh's output on the host and is never
	// part of a request (BB-D64), so its . and .. are jq's and the template's, not path segments.
	"jq": true, "template": true,
}

// climbingWord returns why an argument or a flag value would climb out of the repository's
// REST path, or "" (BB-D60). gh puts many of them into a path segment, escaping a / as %2F
// and leaving the dots (MEASURED against gh 2.101.0 and a local fake API: `run view
// ../../../other/x -R o/r` sends GET /repos/o/r/actions/runs/..%2F..%2F..%2Fother%2Fx, and so
// do run view --job, release view's tag, repo read-file's path, --env, and the gitignore and
// license names, which name no repository at all). A server that decodes that %2F and then
// resolves dot segments reads /repos/other/x. Whether GitHub does is UNMEASURED, so the broker
// does not rely on it. `gh api` has its own rule (apiPathProblem), and a search sends its
// words as query text.
func climbingWord(p *parsed) string {
	if p.cmd.path == "api" || strings.HasPrefix(p.cmd.path, "search ") {
		return ""
	}
	const why = "has a dot segment (. or ..), as typed or once decoded, and gh puts it into a " +
		"REST path, where a server that decodes the %%2F gh writes for each / and resolves dot " +
		"segments reads a path outside the repository. Name it without . or .. segments: a file " +
		"path from the repository's root, or a tag, branch or id as GitHub spells it"
	for _, a := range p.positionals {
		if hasDotSegment(a) {
			return fmt.Sprintf("argument %q "+why, a)
		}
	}
	for _, u := range p.flags {
		if u.hasValue && !freeTextFlags[u.flag.long] && hasDotSegment(u.value) {
			return fmt.Sprintf("--%s %q "+why, u.flag.long, u.value)
		}
	}
	return ""
}

// hasDotSegment reports whether v, split on / or \, has a . or .. segment as typed or once
// or twice percent-decoded: what a server reads once it undoes the escape gh adds, or that
// and the jail's own.
func hasDotSegment(v string) bool {
	for i := 0; i < 3; i++ {
		for _, seg := range strings.FieldsFunc(v, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == "." || seg == ".." {
				return true
			}
		}
		d, err := url.PathUnescape(v)
		if err != nil || d == v {
			return false
		}
		v = d
	}
	return false
}
