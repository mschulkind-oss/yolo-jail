package ghbroker

import (
	"sort"
	"strings"
)

// policy.go is the broker's own code: which commands no set may hold, which flags make any
// command unsafe to run on the host, and which commands GitHub's default standing set,
// `read-only`, admits (docs/design/boundary-broker.md §5.2 to §5.4). None of it is
// configuration, by BB-P8: no set definition and no human answer moves a refusal.

// refusedCommand is a command path (or a group of them, by prefix) the broker never runs,
// whatever set the jail holds.
type refusedCommand struct {
	path string
	why  string
	// except are paths under this one that the refusal does not cover; they are
	// classified like any other command.
	except []string
	// unless names a flag whose presence lifts the refusal (`repo set-default --view`).
	unless string
}

// refusedCommands is §5.4's table, in the classifier's vocabulary. A prefix covers every
// command under it, so `codespace` covers `codespace ssh`.
var refusedCommands = []refusedCommand{
	{path: "auth", except: []string{"auth status"},
		why: "it prints or changes the host's GitHub login"},
	{path: "config", why: "it reads or changes the broker's gh configuration, which holds the login"},
	{path: "alias", why: "an alias changes what the broker's gh runs, and can run a shell"},
	{path: "extension", except: []string{"extension search"},
		why: "an extension is a program gh downloads and runs on the host"},
	{path: "copilot", why: "it downloads and runs a program on the host"},
	{path: "preview", why: "it runs preview features that open host programs"},
	{path: "codespace", except: []string{"codespace list", "codespace view"},
		why: "it opens a session or a program on the host, or changes a codespace"},
	{path: "browse", why: "it runs the host's web browser"},
	{path: "repo clone", why: "it needs a local checkout, which the broker does not have"},
	{path: "gist clone", why: "it needs a local checkout, which the broker does not have"},
	{path: "pr checkout", why: "it needs a local checkout, which the broker does not have"},
	{path: "co", why: "it is `pr checkout`, which needs a local checkout the broker does not have"},
	{path: "repo sync", why: "it changes a local checkout, which the broker does not have"},
	{path: "repo set-default", unless: "view", why: "it changes a local checkout's git config"},
	{path: "run download", why: "it writes files on the host"},
	{path: "release download", why: "it writes files on the host"},
	{path: "attestation download", why: "it writes files on the host"},
	{path: "attestation verify", why: "it reads a host file the command names"},
	{path: "release verify-asset", why: "it reads a host file the command names"},
	{path: "release upload", why: "it uploads host files the command names"},
	{path: "repo deploy-key add", why: "it uploads a host file the command names"},
	{path: "ssh-key add", why: "it uploads a host file the command names"},
	{path: "gpg-key add", why: "it uploads a host file the command names"},
	{path: "skill", why: "it installs, reads or publishes agent skills on the host's disk"},
}

// hostFileFlag is a flag whose value is a HOST path (H9). stdinOK says the command reads
// the jail's standard input when the value is "-", which is the only form the broker
// accepts for such a flag; with stdinOK false every value is refused.
type hostFileFlag struct {
	path, long string
	stdinOK    bool
}

// hostFileFlags is every flag in the measured grammar whose value is a host path. A path
// of "*" matches every command that has the flag. grammar_test.go's sweep fails when a
// regenerated grammar carries a file-shaped flag that is neither here nor in
// notHostFileFlags, so a flag a gh upgrade adds is reviewed before it is accepted.
var hostFileFlags = []hostFileFlag{
	{path: "*", long: "body-file", stdinOK: true},
	{path: "*", long: "notes-file", stdinOK: true},
	{path: "agent-task create", long: "from-file", stdinOK: true},
	{path: "api", long: "input", stdinOK: true},
	{path: "attestation trusted-root", long: "tuf-root"},
	{path: "codespace ssh", long: "debug-file"},
	{path: "gist edit", long: "add"},
	{path: "*", long: "attach"},
	{path: "issue develop", long: "worktree"},
	{path: "pr checkout", long: "worktree"},
	{path: "*", long: "recover"},
	{path: "pr create", long: "template"},
	{path: "release download", long: "dir"},
	{path: "release download", long: "output"},
	{path: "run download", long: "dir"},
	{path: "repo create", long: "source"},
	{path: "repo create", long: "clone"},
	{path: "repo read-file", long: "output"},
	{path: "repo read-file", long: "clobber"},
	{path: "secret set", long: "env-file"},
	{path: "variable set", long: "env-file"},
	{path: "issue develop", long: "checkout"},
}

// notHostFileFlags are file-shaped flags the sweep found that name something other than
// a host path: a file inside a repository or a gist, a search filter, a display switch.
// Reviewed, not assumed; the sweep requires every file-shaped flag to be in one of the two
// lists.
var notHostFileFlags = map[string]string{
	"attestation verify --bundle":              "refused command",
	"attestation verify --custom-trusted-root": "refused command",
	"attestation verify --signer-workflow":     "refused command; a workflow path on GitHub",
	"browse --blame":                           "refused command",
	"codespace cp --expand":                    "refused command",
	"codespace cp --recursive":                 "refused command",
	"codespace create --devcontainer-path":     "refused command; a path in the repository",
	"codespace ssh --debug":                    "refused command",
	"gist create --filename":                   "names the gist file for standard input",
	"gist edit --filename":                     "a file inside the gist",
	"gist edit --remove":                       "a file inside the gist",
	"gist list --include-content":              "a search switch",
	"gist view --filename":                     "a file inside the gist",
	"gist view --files":                        "a display switch",
	"repo create --add-readme":                 "creates a file on GitHub",
	"search code --extension":                  "a search filter",
	"search code --filename":                   "a search filter",
	"search code --match":                      "a search filter",
	"skill install --allow-hidden-dirs":        "refused command",
	"skill install --dir":                      "refused command",
	"skill install --from-local":               "refused command",
	"skill list --dir":                         "refused command",
	"skill preview --allow-hidden-dirs":        "refused command",
	"skill update --dir":                       "refused command",
	"workflow run --ref":                       "a branch or tag on GitHub",
	"workflow view --ref":                      "a branch or tag on GitHub",
	"workflow view --yaml":                     "a display switch",
}

// atFileFlags are flags whose value may name a host file with gh's `@path` syntax
// (`-F key=@file`). An `@` value is refused on them, stdin included (§5.3 rule 4).
var atFileFlags = map[string]bool{
	"api --field":          true,
	"workflow run --field": true,
}

// hostFilePositionals are commands whose usage takes a host file, pattern or directory as
// a positional argument and that the refusal table does not already refuse whole. A
// command here is refused when it carries more positionals than keepPositionals, the
// count that name something other than a host file (BB-D28).
var hostFilePositionals = map[string]struct {
	keep int
	why  string
}{
	"release create": {keep: 1, why: "its arguments after the tag are host files to upload"},
	"gist create":    {keep: 0, why: "its arguments are host files to upload"},
}

// notHostFilePositionals are commands whose usage line names a file, path or directory
// that is NOT on the host: a path in a repository, a key id, a workflow file on GitHub.
var notHostFilePositionals = map[string]string{
	"cache delete":             "a cache key",
	"config get":               "refused command",
	"config set":               "refused command",
	"alias import":             "refused command",
	"attestation download":     "refused command",
	"attestation trusted-root": "names no positional; a host path only through --tuf-root, a host-file flag",
	"attestation verify":       "refused command",
	"browse":                   "refused command",
	"codespace cp":             "refused command",
	"extension install":        "refused command",
	"gist clone":               "refused command",
	"gist edit":                "a file inside the gist",
	"gist rename":              "files inside the gist",
	"gpg-key add":              "refused command",
	"gpg-key delete":           "a key id",
	"issue transfer":           "a repository",
	"label clone":              "a repository",
	"release upload":           "refused command",
	"release verify-asset":     "refused command",
	"repo archive":             "a repository",
	"repo autolink create":     "a key prefix",
	"repo clone":               "refused command",
	"repo delete":              "a repository",
	"repo deploy-key add":      "refused command",
	"repo deploy-key delete":   "a key id",
	"repo edit":                "a repository",
	"repo fork":                "a repository",
	"repo license view":        "a license key",
	"repo read-dir":            "a path in the repository",
	"repo read-file":           "a path in the repository",
	"repo set-default":         "a repository",
	"repo sync":                "refused command",
	"repo unarchive":           "a repository",
	"repo view":                "a repository",
	"skill install":            "refused command",
	"skill preview":            "refused command",
	"skill publish":            "refused command",
	"ssh-key add":              "refused command",
	"workflow view":            "a workflow file on GitHub",
}

// scopeKind is how a command's repository is found.
type scopeKind int

const (
	// scopeRepo: the command names one repository through -R, a repository positional or
	// a github.com URL, or takes the forwarder's.
	scopeRepo scopeKind = iota
	// scopeNone: the command reads nothing the user's login makes private.
	scopeNone
	// scopeAccount: the command reads or writes across the account; always out of scope.
	scopeAccount
)

// readRule is one command in GitHub's default standing set.
type readRule struct {
	scope scopeKind
	// accountFlags make the command account-wide when present (`secret list --org`).
	accountFlags []string
	// refuseFlags are refused on this command, beyond the global refusals.
	refuseFlags map[string]string
	// require are flags the command must carry to be a read; without them it is a write
	// (`issue develop` without `--list` creates a branch).
	require []string
}

// readOnly is §5.2's table: the commands GitHub's default standing set admits, measured
// against gh 2.101.0. Every other command that is parsed, in scope and not refused
// belongs to `read-write` alone.
var readOnly = map[string]readRule{
	"pr view":   {},
	"pr list":   {},
	"pr diff":   {},
	"pr status": {},
	"pr checks": {},

	"issue view":    {},
	"issue list":    {},
	"issue status":  {},
	"issue develop": {require: []string{"list"}},

	"run view":  {},
	"run list":  {},
	"run watch": {},

	"workflow view": {},
	"workflow list": {},

	"repo view":            {},
	"repo read-dir":        {},
	"repo read-file":       {},
	"repo autolink list":   {},
	"repo autolink view":   {},
	"repo deploy-key list": {},
	"repo gitignore list":  {scope: scopeNone},
	"repo gitignore view":  {scope: scopeNone},
	"repo license list":    {scope: scopeNone},
	"repo license view":    {scope: scopeNone},

	"release view":   {},
	"release list":   {},
	"release verify": {},

	"label list": {},
	"cache list": {},

	"secret list":   {accountFlags: []string{"org", "user"}},
	"variable list": {accountFlags: []string{"org"}},
	"variable get":  {accountFlags: []string{"org"}},

	"ruleset list":  {accountFlags: []string{"org"}},
	"ruleset view":  {accountFlags: []string{"org"}},
	"ruleset check": {},

	"discussion list": {},
	"discussion view": {},

	// The searches: in scope only when the complete qualifier set names in-scope
	// repositories alone (searchRepos).
	"search code":    {},
	"search commits": {},
	"search issues":  {},
	"search prs":     {},

	// In the set, and account-wide: refused as out of scope (OQ-BB6), which the scope
	// check reaches before the set does.
	"search repos":       {scope: scopeAccount},
	"repo list":          {scope: scopeAccount},
	"gist view":          {scope: scopeAccount},
	"gist list":          {scope: scopeAccount},
	"org list":           {scope: scopeAccount},
	"status":             {scope: scopeAccount},
	"agent-task list":    {scope: scopeAccount},
	"agent-task view":    {scope: scopeAccount},
	"project list":       {scope: scopeAccount},
	"project view":       {scope: scopeAccount},
	"project field-list": {scope: scopeAccount},
	"project item-list":  {scope: scopeAccount},

	"auth status": {scope: scopeNone, refuseFlags: map[string]string{
		"show-token": "it prints the host's token"}},

	// `api` is read-only only under apiRule; the entry makes it a candidate.
	"api": {},
}

// accountWide are command paths (or groups, by prefix) that touch the account rather
// than one repository, whatever they are passed: out of scope in every set (§5.6).
var accountWide = []string{
	"gist", "org", "project", "ssh-key", "gpg-key", "status", "search repos", "repo list",
	"repo create", "repo fork", "agent-task list", "agent-task view", "codespace",
	"extension", "attestation trusted-root", "completion", "licenses",
}

func pathUnder(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+" ")
}

// refusedFor returns why a command path is refused whole, or "".
func refusedFor(p *parsed) string {
	for _, r := range refusedCommands {
		if !pathUnder(p.cmd.path, r.path) {
			continue
		}
		excepted := false
		for _, e := range r.except {
			if pathUnder(p.cmd.path, e) {
				excepted = true
			}
		}
		if excepted || (r.unless != "" && p.has(r.unless)) {
			continue
		}
		return r.why
	}
	return ""
}

func isAccountWide(path string) bool {
	for _, a := range accountWide {
		if pathUnder(path, a) {
			return true
		}
	}
	if r, ok := readOnly[path]; ok && r.scope == scopeAccount {
		return true
	}
	return false
}

// hostFileRule returns the host-file rule for one flag of one command, if any.
func hostFileRule(path, long string) (hostFileFlag, bool) {
	for _, h := range hostFileFlags {
		if h.long == long && (h.path == "*" || h.path == path) {
			return h, true
		}
	}
	return hostFileFlag{}, false
}

// isFormattingTemplate tells the formatting `--template` (a Go template over gh's own JSON
// output) from the `-T/--template` issue and pr create take, by the command's own grammar
// rather than the spelling (§5.2).
func isFormattingTemplate(f *ghFlag) bool {
	return f.long == "template" && strings.HasPrefix(f.desc, "Format JSON output using a Go template")
}

// globalFlagRefusal returns why one flag use is refused on any command, or "".
//
// `--jq` and the formatting `--template` are NOT here (BB-D64). Both run inside the host gh,
// over its own output, and what they can reach there was measured against gh 2.101.0 and
// read in the go-gh and gojq it links: gh compiles a jq filter with the environment and
// nothing else (no module loader, so `import` and `include` fail; no input iterator, so
// `input` fails; no file, network or process builtin exists), and its templates add
// formatting functions only (no `env`). The environment is the one buildEnv makes from
// nothing, which holds no token (TestTheEnvironmentAJqFilterCanReadIsExactlyTheReviewedSet).
// So a filter prints nothing the jail may not have, and the output crosses verbatim (OQ-C).
func globalFlagRefusal(p *parsed, u flagUse) string {
	f := u.flag
	switch {
	case f.long == "web":
		return "--web is refused: it runs the host's web browser"
	case f.long == "editor":
		return "--editor is refused: it runs a text editor on the host"
	case f.long == "hostname":
		return "--hostname is refused: the broker talks to github.com only, and another host would " +
			"be sent the login"
	case f.long == "verbose" && p.cmd.path == "api":
		return "api --verbose is refused: it prints the whole HTTP request, headers included"
	}
	if h, ok := hostFileRule(p.cmd.path, f.long); ok {
		if h.stdinOK && u.hasValue && u.value == "-" {
			return ""
		}
		if h.stdinOK {
			return "--" + f.long + " names a host file; pass `-` and pipe the jail's file to " +
				"standard input instead"
		}
		return "--" + f.long + " names a host path, which the broker never reads or writes"
	}
	if atFileFlags[p.cmd.path+" --"+f.long] && u.hasValue {
		if _, val, ok := strings.Cut(u.value, "="); ok && strings.HasPrefix(val, "@") {
			return "a --" + f.long + " value beginning with @ reads a host file"
		}
	}
	return ""
}

// readsStdin reports whether the canonical argv reads the jail's standard input.
func readsStdin(p *parsed) bool {
	for _, u := range p.flags {
		if h, ok := hostFileRule(p.cmd.path, u.flag.long); ok && h.stdinOK && u.value == "-" {
			return true
		}
	}
	return false
}

// sortedKeys is for deterministic messages and tests.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
