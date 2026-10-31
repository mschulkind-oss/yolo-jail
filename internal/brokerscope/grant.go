package brokerscope

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
)

// grant.go is a scope file's user grants (docs/design/git-broker-plan.md ID-10, BB-D96): the
// specific grants a workspace config names, each of a kind the source defines and the user's own
// config allows, as the config-change gate approved them. The file carries them for the broker
// to read at each write; the scope file is still written only from a gate's approved result.

// Grant is one user grant: its kind (`create-branch`, `branch`, `tag`, `dispatch-workflow`) and
// the repository it covers, a repository identity (`github.com/o/r`, gitproto.Identity) or
// AnyRepo. A kind that names something (GrantTakesName) carries its name in Branch (`branch`),
// Tag (`tag`) or Workflow (`dispatch-workflow`), and no other kind has any of them.
type Grant struct {
	Kind string `json:"kind"`
	Repo string `json:"repo"`
	// Branch is a `branch` grant's branch, a short name or a refspec pattern (`main`, meaning
	// refs/heads/main; `patch/*`, every branch under refs/heads/patch/): working-set branches
	// declared in config (docs/design/git-broker.md GB-D12, GB-D13, GB-D25).
	Branch string `json:"branch,omitempty"`
	// Tag is a `tag` grant's tag, a short name or a refspec pattern (`v*`, every tag under
	// refs/tags/ starting with v): tags the broker may create, update, force-update and delete
	// unasked (docs/design/git-broker.md GB-D25).
	Tag string `json:"tag,omitempty"`
	// Workflow is a `dispatch-workflow` grant's workflow: a workflow file name in the
	// repository's .github/workflows (`nightly-macos.yml`), or AnyWorkflow for every one
	// (docs/design/github-write-scopes.md GW-D14).
	Workflow string `json:"workflow,omitempty"`
}

// KindDispatchWorkflow is the GitHub grant kind that names a workflow, or AnyWorkflow: the
// broker may run `gh workflow run <file>` for it unasked, on the repository's default branch only
// (docs/design/github-write-scopes.md GW-D14).
const KindDispatchWorkflow = "dispatch-workflow"

// AnyWorkflow is a `dispatch-workflow` grant's wildcard: every workflow in the repository.
const AnyWorkflow = "*"

// AnyRepo is a grant's written-out wildcard: every repository the scope holds.
const AnyRepo = "*"

// KindBranch is the grant kind that names a branch, or a pattern of them: such a branch is in
// the workspace's working set, declared in config rather than recorded in the store
// (docs/design/git-broker.md GB-D12, GB-D25).
const KindBranch = "branch"

// KindTag is the grant kind that names a tag, or a pattern of them, under refs/tags/: the broker
// may create, update, force-update and delete such a tag unasked (docs/design/git-broker.md
// GB-D25). A tag never joins the working set; the config holds the grant.
const KindTag = "tag"

// KindRead is the grant kind every brokered source defines without declaring it: reading the
// repositories of the approved scope (docs/design/boundary-broker.md BB-D105, BB-D107). A broker
// whose live scope file holds no read grant covering a repository refuses every call on it.
const KindRead = "read"

// ReadCovers reports whether grants hold a read grant on repo, or on AnyRepo. same compares a
// grant's repository identity with repo, which each source spells its own way (git a remote's
// identity, gh `OWNER/REPO`).
func ReadCovers(grants []Grant, repo string, same func(grantRepo, repo string) bool) bool {
	for _, g := range grants {
		if g.Kind == KindRead && (g.Repo == AnyRepo || same(g.Repo, repo)) {
			return true
		}
	}
	return false
}

// HoldsRead reports whether grants hold any read grant.
func HoldsRead(grants []Grant) bool {
	for _, g := range grants {
		if g.Kind == KindRead {
			return true
		}
	}
	return false
}

// GrantTakesBranch reports whether a grant of kind names a branch.
func GrantTakesBranch(kind string) bool { return kind == KindBranch }

// GrantTakesRef reports whether a grant of kind names a ref, a branch or a tag, by short name or
// pattern.
func GrantTakesRef(kind string) bool { return kind == KindBranch || kind == KindTag }

// GrantTakesName reports whether a grant of kind names something beside its repository: a ref
// (GrantTakesRef) or a workflow (`dispatch-workflow`). Config spells such a kind's request as a
// list of names, and an `always` capability cannot hold one.
func GrantTakesName(kind string) bool { return GrantTakesRef(kind) || kind == KindDispatchWorkflow }

// GrantNameKey is the field a name-taking kind's name is written in, in a grant entry and the
// scope file: `branch`, `tag` or `workflow`; "" for a kind that names nothing.
func GrantNameKey(kind string) string {
	switch kind {
	case KindBranch:
		return "branch"
	case KindTag:
		return "tag"
	case KindDispatchWorkflow:
		return "workflow"
	}
	return ""
}

// ValidGrantName reports whether name is one a grant of the name-taking kind can hold: a ref name
// or pattern for `branch` and `tag` (ValidGrantRefPattern), a workflow file name or `*` for
// `dispatch-workflow` (ValidGrantWorkflow). False for a kind that names nothing.
func ValidGrantName(kind, name string) bool {
	switch {
	case GrantTakesRef(kind):
		return ValidGrantRefPattern(name)
	case kind == KindDispatchWorkflow:
		return ValidGrantWorkflow(name)
	}
	return false
}

// ValidGrantWorkflow reports whether s is a workflow a `dispatch-workflow` grant can name:
// AnyWorkflow, or one workflow file name as gh takes it, ending in .yml or .yaml, made of
// letters, digits, `.`, `_` and `-`, and not starting with `.` or `-`. A path, a numeric
// workflow id and a display name are not file names; a grant cannot name them.
func ValidGrantWorkflow(s string) bool {
	if s == AnyWorkflow {
		return true
	}
	if len(s) > 255 || s == "" || s[0] == '.' || s[0] == '-' {
		return false
	}
	if !strings.HasSuffix(s, ".yml") && !strings.HasSuffix(s, ".yaml") {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// RefNamespace is where a ref-naming kind's names live: refs/heads/ for `branch`, refs/tags/
// for `tag`, "" for any other kind.
func RefNamespace(kind string) string {
	switch kind {
	case KindBranch:
		return "refs/heads/"
	case KindTag:
		return "refs/tags/"
	}
	return ""
}

// RefGrant is the grant of kind on repo naming name, in the field the kind uses (GrantNameKey).
// For a kind that names nothing, name is dropped.
func RefGrant(kind, repo, name string) Grant {
	g := Grant{Kind: kind, Repo: repo}
	switch kind {
	case KindBranch:
		g.Branch = name
	case KindTag:
		g.Tag = name
	case KindDispatchWorkflow:
		g.Workflow = name
	}
	return g
}

// Name is the name or pattern a name-taking grant holds, its Branch, Tag or Workflow.
func (g Grant) Name() string {
	switch {
	case g.Branch != "":
		return g.Branch
	case g.Tag != "":
		return g.Tag
	}
	return g.Workflow
}

// Wildcard reports whether a name-taking grant reaches further than one name on one
// repository: its repository is AnyRepo, or its name is a pattern (a ref pattern, or
// AnyWorkflow).
func (g Grant) Wildcard() bool {
	return GrantTakesName(g.Kind) && (g.Repo == AnyRepo || strings.Contains(g.Name(), "*"))
}

// Covers reports whether a ref-naming grant covers the full ref on repo: the kind's namespace
// holds ref, the grant's name matches its short name (RefPatternMatches), and the grant's
// repository is repo or AnyRepo. same compares a grant's repository with repo, as ReadCovers's
// does.
func (g Grant) Covers(repo, ref string, same func(grantRepo, repo string) bool) bool {
	ns := RefNamespace(g.Kind)
	short, ok := strings.CutPrefix(ref, ns)
	if ns == "" || !ok || g.Name() == "" {
		return false
	}
	return (g.Repo == AnyRepo || same(g.Repo, repo)) && RefPatternMatches(g.Name(), short)
}

// AnyRemoteNote ends a wildcard grant's note when its repository is AnyRepo.
const AnyRemoteNote = "every remote in scope now and any approved later"

// GrantNote is plain words for what a wildcard ref grant admits, for the gate's grant line
// (docs/design/git-broker.md GB-D25): "may create, update, force-push and delete branches
// matching `patch/*` on github.com/o/r". It is "" for a grant naming one ref or one workflow on
// one repository, which reads plainly as it is, and for a kind that names nothing.
func GrantNote(g Grant) string {
	if !g.Wildcard() {
		return ""
	}
	if g.Kind == KindDispatchWorkflow {
		return dispatchWorkflowNote(g)
	}
	what := "`" + g.Name() + "`"
	if strings.Contains(g.Name(), "*") {
		plural := "branches"
		if g.Kind == KindTag {
			plural = "tags"
		}
		what = plural + " matching " + what
	}
	on := g.Repo
	if g.Repo == AnyRepo {
		on = AnyRemoteNote
	}
	return "may create, update, force-push and delete " + what + " on " + on
}

// dispatchWorkflowNote is GrantNote for a wildcard dispatch-workflow grant, in plain words: "may
// run every workflow, including ones added to the default branch later, on the default branch of
// github.com/o/r, unasked".
func dispatchWorkflowNote(g Grant) string {
	what := "`" + g.Workflow + "`"
	if g.Workflow == AnyWorkflow {
		what = "every workflow, including ones added to the default branch later,"
	}
	on := g.Repo
	if g.Repo == AnyRepo {
		on = "every repository in scope now and any approved later"
	}
	return "may run " + what + " on the default branch of " + on + ", unasked"
}

// String is the grant as the gate's block and the approval record spell it: `<kind> <repo>`,
// then ` <name>` for a kind that names something (GrantTakesName). No part can hold a space (ValidGrant), so the
// spelling parses back (ParseGrant).
func (g Grant) String() string {
	if name := g.Name(); name != "" {
		return g.Kind + " " + g.Repo + " " + name
	}
	return g.Kind + " " + g.Repo
}

// ParseGrant reads a String spelling back, ok false for anything ValidGrant refuses.
func ParseGrant(s string) (Grant, bool) {
	kind, rest, found := strings.Cut(s, " ")
	repo, name, _ := strings.Cut(rest, " ")
	g := Grant{Kind: kind, Repo: repo, Branch: name}
	if kind == KindTag || kind == KindDispatchWorkflow {
		g = RefGrant(kind, repo, name)
	}
	return g, found && ValidGrant(g)
}

// ValidGrant reports whether g is well formed: a grant-kind name, a repository the grant can
// name, and a ref name or pattern exactly when the kind names one, in that kind's own field. It
// says nothing of whether a source defines the kind or the user allows it.
func ValidGrant(g Grant) bool {
	if !validGrantKind(g.Kind) || !ValidGrantRepo(g.Repo) {
		return false
	}
	switch g.Kind {
	case KindBranch:
		return g.Tag == "" && g.Workflow == "" && ValidGrantRefPattern(g.Branch)
	case KindTag:
		return g.Branch == "" && g.Workflow == "" && ValidGrantRefPattern(g.Tag)
	case KindDispatchWorkflow:
		return g.Branch == "" && g.Tag == "" && ValidGrantWorkflow(g.Workflow)
	}
	return g.Branch == "" && g.Tag == "" && g.Workflow == ""
}

// ValidGrantBranch reports whether s is one ref's short name a grant can name: one name git
// accepts as refs/heads/<s> or refs/tags/<s> (git check-ref-format's rules), that does not start
// with `-` and is not `HEAD` (which `git branch` refuses too), and that is not itself written as
// `refs/…` (the short name is what the grant means). It takes no wildcard: a pattern is
// ValidGrantRefPattern's.
func ValidGrantBranch(s string) bool {
	if s == "" || s == "HEAD" || s == "@" || s[0] == '-' || strings.HasPrefix(s, "refs/") {
		return false
	}
	if strings.Contains(s, "..") || strings.Contains(s, "@{") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, c := range []byte(s) {
		if c < 0x20 || c == 0x7f || strings.IndexByte(" ~^:?*[\\", c) >= 0 {
			return false
		}
	}
	for _, comp := range strings.Split(s, "/") {
		if comp == "" || comp[0] == '.' || strings.HasSuffix(comp, ".lock") {
			return false
		}
	}
	return true
}

// ValidGrantRefPattern reports whether s is a name a `branch` or `tag` grant can take: a short
// name ValidGrantBranch accepts, or git's own refspec pattern, the same with one `*` anywhere in
// it (git check-ref-format --refspec-pattern; docs/design/git-broker.md GB-D25). The `*` is
// checked as an ordinary name character standing in its place, which is what git's own check
// does; a second `*` is refused.
func ValidGrantRefPattern(s string) bool {
	if strings.Count(s, "*") > 1 {
		return false
	}
	return ValidGrantBranch(strings.Replace(s, "*", "x", 1))
}

// RefPatternMatches reports whether a grant's name, a short name or a one-`*` pattern, matches
// the short name name. The `*` matches any run of characters, `/` and the empty run included,
// as git's own refspec glob does: `patch/*` matches `patch/a/b`, and `v*` matches `v1.2`.
func RefPatternMatches(pattern, name string) bool {
	prefix, suffix, star := strings.Cut(pattern, "*")
	if !star {
		return pattern == name
	}
	return len(name) >= len(prefix)+len(suffix) && strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix)
}

// validGrantKind is loopholedecl.ValidGrantKind, restated so this package does not import the
// manifest decoder: a lowercase letter, then lowercase letters, digits and hyphens.
func validGrantKind(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// ValidGrantRepo reports whether s is a repository a grant can name: AnyRepo, or a repository
// identity already in its reduced form (gitproto.Identity) whose host has a dot or a port. The
// host rule is what tells `github.com/o/r` from an `OWNER/REPO` written without one: a bare
// single-label host such as `gitserver` cannot be named, a limit stated in the refusal.
func ValidGrantRepo(s string) bool {
	if s == AnyRepo {
		return true
	}
	id, err := gitproto.Identity("https://" + s)
	if err != nil || id != s {
		return false
	}
	host, _, _ := strings.Cut(s, "/")
	return strings.ContainsAny(host, ".:")
}

// checkGrants is the error for the first grant ValidGrant refuses, nil for none.
func checkGrants(grants []Grant) error {
	for _, g := range grants {
		if !ValidGrant(g) {
			return fmt.Errorf("scope file: grant %q is not a grant kind, a repository identity and, for a branch or tag grant, one name or pattern with at most one `*`, or for a dispatch-workflow grant, one workflow file name or `*`", g.String())
		}
	}
	return nil
}
