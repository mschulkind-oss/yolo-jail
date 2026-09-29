package ghbroker

import (
	"fmt"
	"regexp"
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

	// 2. Scope: which repositories the command touches, made explicit in the argv.
	var sr scopeResult
	switch {
	case p.has("help"):
		// `--help` prints gh's own help text and contacts no one, so it is run alone.
		sr = scopeResult{argv: append(strings.Fields(p.cmd.path), "--help")}
	case p.cmd.path == "api":
		sr = apiScope(p, fieldRepo)
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
	if r, ok := readOnly[p.cmd.path]; ok {
		for _, f := range r.accountFlags {
			if p.has(f) {
				sr.account = true
			}
		}
	}
	d.Repos = sr.repos
	if sr.account {
		d.Outcome, d.AccountWide = OutcomeOutOfScope, true
		d.Reason = "`gh " + p.cmd.path + "` names no repository the broker can check: it reads or " +
			"writes across the account, which no scope admits. This jail's repository scope is " +
			scope.describe() + "."
		return d
	}
	for _, r := range sr.repos {
		if !scope.Contains(r) {
			d.Outcome = OutcomeOutOfScope
			d.Reason = r + " is outside this jail's repository scope (" + scope.describe() + "). " +
				"The scope is this workspace's GitHub remotes, approved at a fresh launch; this " +
				"version has no way to widen it."
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
				sr.refused = fmt.Sprintf("argument %q is a github.com URL that names no repository", a)
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
				// A github.com URL argument named the repository; gh reads it from there.
				break
			}
			if fieldRepo == "" {
				return scopeResult{account: true}
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
				return scopeResult{account: true}
			}
			add(fieldRepo)
			argv = append(argv, fieldRepo)
		}
	default:
		if len(sr.repos) == 0 {
			return scopeResult{account: true}
		}
	}
	// A second repository a command names positionally (`issue transfer <destination-repo>`,
	// `label clone <source-repository>`) must be in scope too.
	if extra := secondRepoPositional(p); extra != "" {
		if !ValidRepo(extra) {
			return scopeResult{refused: fmt.Sprintf("argument %q is not OWNER/REPO", extra)}
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

func secondRepoPositional(p *parsed) string {
	switch p.cmd.path {
	case "issue transfer":
		if len(p.positionals) > 1 {
			return p.positionals[1]
		}
	case "label clone":
		if len(p.positionals) > 0 {
			return p.positionals[0]
		}
	}
	return ""
}

// qualifierRE finds a search qualifier that names repositories, owners or organizations in
// a query's text. Several `repo:` qualifiers are ORed by GitHub, which is why the text is
// checked as well as the flags (§5.2).
var qualifierRE = regexp.MustCompile(`(?i)(^|[\s"'(-])(repo|org|user|owner):`)

// searchScope is §5.2's search rule: in scope only when the complete qualifier set, from
// flags and query text together, names in-scope repositories alone.
func searchScope(p *parsed) scopeResult {
	if p.has("owner") {
		return scopeResult{account: true}
	}
	for _, a := range p.positionals {
		if qualifierRE.MatchString(a) {
			return scopeResult{account: true}
		}
	}
	var repos []string
	for _, v := range p.values("repo") {
		for _, r := range strings.Split(v, ",") {
			r = strings.TrimSpace(r)
			if !ValidRepo(r) {
				return scopeResult{refused: fmt.Sprintf("--repo %q is not OWNER/REPO", r)}
			}
			repos = append(repos, r)
		}
	}
	if len(repos) == 0 {
		return scopeResult{account: true}
	}
	return scopeResult{repos: repos, argv: p.canonical()}
}
