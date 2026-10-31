package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// brokered.go is the `brokered` key (docs/design/workspace-widening.md; boundary-broker.md §5.6):
// the repositories a brokered loophole's daemon may reach beyond the workspace's own remotes. A
// brokered loophole is one whose manifest declares a `brokered` block (loopholedecl), and its
// `source` names the key below. A workspace lists what it asks for in its own config:
//
//	"brokered": { "github": { "repos": ["org/lib", "org/docs"] } }
//
// # Workspace scope, behind the gate
//
// The maintainer's ruling (WW-D1): an agent can already widen the scope with `git remote add`
// and one y at the next launch, so a user-scope list fenced off nothing, and it cost the human a
// host-side edit. So the list is a workspace key, and it takes effect only through the
// config-change gate, which shows each repository as a row of the labeled scope block and
// records it in the approval record's scope part (scopeapproval.go). Nothing reads it but the
// gate's one read (gateread.go): the broker gets what that gate approved, and nothing else.
//
// # What it may not be
//
//   - A user-scope `repos` list: with no workspace key it would widen every workspace.
//   - A workspace-scope `sets` list: a set decides what a write grant hands over, and the
//     workspace is the agent's to edit, so the sets live at user scope alone (BB-D23, BB-P9).
//   - The old user-scope form, `brokered.<source>.workspaces`, keyed by a workspace's host path,
//     which shipped in 0.11.1: retired at every scope, its message naming the edit to make.
//   - A `brokered` key from a file outside the workspace, reached by an include or a link: it
//     would point the scope at another project's private list (WW-D17, wsroot.go).
//   - A `brokered`, `<source>` or `repos` key written twice in one file, which the decoder
//     resolves by keeping the last one, silently (WW-D24).
//
// # What an entry admits
//
// Whole repositories, which join the scope for every permission set exactly as a repository
// read from the workspace's remotes does (OQ-BB9, ruled A). Nothing else: no entry admits a
// command that names no repository, and no entry limits a repository to some sets.
//
// # User grants, in two layers
//
// BB-D96 (docs/design/git-broker-plan.md ID-10): the user's own config lists the kinds of grant
// a workspace may hold, `brokered.<source>.grant_kinds`, and the workspace config names each
// grant and the repository it covers, `brokered.<source>.grants`:
//
//	"brokered": { "git": { "grants": [{"kind": "create-branch", "repo": "github.com/o/r"}] } }
//
// A `branch` grant names its branch too, `"branch": "main"`: a working-set branch declared in
// config (docs/design/git-broker.md GB-D12, GB-D13), and a `tag` grant its tag, `"tag": "v*"`;
// either may be git's refspec pattern with one `*`, and either may name "*" for its repository
// (GB-D25). No other kind takes either key. A grant joins only at the config-change gate, as a
// line of its source's scope block recorded under `<source>.grants` (scopeapproval.go), and only
// within the user's kinds: one outside them is refused here, naming the user-config key that
// would allow it.
const (
	brokeredKey           = "brokered"
	brokeredWorkspacesKey = "workspaces"
	brokeredReposKey      = "repos"
	brokeredSetsKey       = "sets"
	// brokeredGrantsKey and brokeredGrantKindsKey are the two layers of a user grant
	// (docs/design/boundary-broker.md BB-D96; docs/design/git-broker-plan.md ID-10): the user's own
	// config lists the kinds a workspace may hold (`grant_kinds`, user scope only), and the
	// workspace config names each grant and its repository (`grants`, workspace scope only), which
	// joins only at the config-change gate and only within the user's kinds.
	brokeredGrantsKey     = "grants"
	brokeredGrantKindsKey = "grant_kinds"
)

// The keys of one `brokered.<source>.grants` entry.
const (
	brokeredGrantKindKey     = "kind"
	brokeredGrantRepoKey     = "repo"
	brokeredGrantBranchKey   = "branch"
	brokeredGrantTagKey      = "tag"
	brokeredGrantWorkflowKey = "workflow"
)

var knownBrokeredGrantKeys = set(brokeredGrantKindKey, brokeredGrantRepoKey, brokeredGrantBranchKey, brokeredGrantTagKey,
	brokeredGrantWorkflowKey)

// grantRefKeys are the grant-entry keys that name something beside the repository, each the key
// brokerscope.GrantNameKey gives its kind: `branch` (KindBranch), `tag` (KindTag) and `workflow`
// (KindDispatchWorkflow).
var grantRefKeys = []string{brokeredGrantBranchKey, brokeredGrantTagKey, brokeredGrantWorkflowKey}

// grantRefKind is the kind each name key belongs to.
var grantRefKind = map[string]string{
	brokeredGrantBranchKey:   brokerscope.KindBranch,
	brokeredGrantTagKey:      brokerscope.KindTag,
	brokeredGrantWorkflowKey: brokerscope.KindDispatchWorkflow,
}

// grantRefExample is each name key's example name and what it means, for a refusal's next step.
var grantRefExample = map[string][2]string{
	brokeredGrantBranchKey:   {"main", "refs/heads/main"},
	brokeredGrantTagKey:      {"v*", "refs/tags/v1, refs/tags/v2.0 and every other tag starting with v"},
	brokeredGrantWorkflowKey: {"nightly.yml", ".github/workflows/nightly.yml, run on the default branch; \"*\" names every workflow"},
}

// grantEntry is one `grants` entry as a grant, ok false unless it holds exactly a kind, a
// repository and, for a kind that names something, its `branch`, `tag` or `workflow`, all
// strings, making a grant brokerscope.ValidGrant accepts. A name key is part of the grant only
// for its own kind (ValidGrant refuses it on any other, and a name-taking grant without one).
func grantEntry(m *jsonx.OrderedMap) (brokerscope.Grant, bool) {
	str := func(key string) (string, bool, bool) {
		v, has := m.Get(key)
		s, ok := v.(string)
		return s, has, ok || !has
	}
	kind, _, kindOK := str(brokeredGrantKindKey)
	repo, _, repoOK := str(brokeredGrantRepoKey)
	branch, hasBranch, branchOK := str(brokeredGrantBranchKey)
	tag, hasTag, tagOK := str(brokeredGrantTagKey)
	workflow, hasWorkflow, workflowOK := str(brokeredGrantWorkflowKey)
	keys := 2
	for _, has := range []bool{hasBranch, hasTag, hasWorkflow} {
		if has {
			keys++
		}
	}
	g := brokerscope.Grant{Kind: kind, Repo: repo, Branch: branch, Tag: tag, Workflow: workflow}
	return g, kindOK && repoOK && branchOK && tagOK && workflowOK && len(m.Keys()) == keys && brokerscope.ValidGrant(g)
}

// grantRefProblem is the error for a grant entry's `branch`, `tag` and `workflow`, "" when they
// are right: a kind that names a ref (brokerscope.GrantTakesRef) has a valid name or one-`*`
// pattern under the key of its own name, on a repository or "*" (GB-D25); a `dispatch-workflow`
// grant has a workflow file name or "*" under `workflow` (GW-D14); and no kind has another kind's
// key. The second result is the name as written, valid or not.
func grantRefProblem(entryPath, kind string, m *jsonx.OrderedMap) (string, string) {
	for _, key := range grantRefKeys {
		if _, has := m.Get(key); has && grantRefKind[key] != kind {
			return entryPath + "." + key + ": only a " + grantRefKind[key] + " grant names a " + key + "; a " + pyReprValue(kind) +
				" grant does not take one. Remove this key, or make the grant {\"kind\": \"" + grantRefKind[key] + "\", …} " +
				"to name that " + key + ".", ""
		}
	}
	if !brokerscope.GrantTakesName(kind) {
		return "", ""
	}
	key := brokerscope.GrantNameKey(kind)
	v, has := m.Get(key)
	path := entryPath + "." + key
	example := grantRefExample[key]
	if !has {
		return path + ": a " + kind + " grant names its " + key + ", such as \"" + key + "\": \"" + example[0] +
			"\" (" + example[1] + "). Add it.", ""
	}
	name, ok := v.(string)
	if kind == brokerscope.KindDispatchWorkflow {
		if !ok || !brokerscope.ValidGrantWorkflow(name) {
			return path + ": " + pyReprValue(v) + " is not a workflow a dispatch-workflow grant can name: write the " +
				"workflow's file name in .github/workflows, such as \"" + example[0] + "\" (not its path, numeric " +
				"id or display name), or \"*\" for every workflow in the repository. List each workflow as its own grant.", name
		}
		return "", name
	}
	if ok && strings.Count(name, "*") > 1 {
		return path + ": " + pyReprValue(v) + " has more than one `*`: a pattern takes one, which matches any run " +
			"of characters, `/` included (\"patch/*\", \"v*\"). Keep one `*`, or list each pattern as its own grant.", name
	}
	if !ok || !brokerscope.ValidGrantRefPattern(name) {
		return path + ": " + pyReprValue(v) + " is not a " + key + " name a " + key + " grant can name: write its " +
			"short name as git accepts it (\"" + example[0] + "\", \"release/1.2\"; not \"" +
			brokerscope.RefNamespace(kind) + "…\"), optionally with one `*` matching any run of characters " +
			"(\"patch/*\"). List each name as its own grant.", name
	}
	return "", name
}

// readGrantKind is the grant kind every brokered source defines without declaring it: reading
// the repositories of the approved scope (docs/design/boundary-broker.md BB-D105, BB-D107). A
// broker holding no read grant refuses every call, so its pack being selected grants nothing.
// It is the one kind the user config may hold as a grant itself, for every workspace.
const readGrantKind = brokerscope.KindRead

// definedGrantKinds is the kinds a source's grants may be of: read, which every source defines,
// then the kinds its manifest declares.
func definedGrantKinds(declared []string) []string {
	out := []string{readGrantKind}
	for _, k := range declared {
		if !inList(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// userGrantsValue parses a user-scope `brokered.<source>.grants` list (BB-D107): the user
// config holds only `{"kind": "read", "repo": "*"}`, which grants read on each workspace's own
// approved scope to every workspace, and needs no `grant_kinds` entry, since the user config is
// the ceiling's owner. It returns the valid grants and one message per problem, each starting
// with path; a malformed entry is skipped, never repaired.
func userGrantsValue(source, path string, v any) ([]brokerscope.Grant, []string) {
	list, ok := v.([]any)
	if !ok {
		return nil, []string{path + `: expected a list holding {"kind": "read", "repo": "*"} (got ` +
			pyReprValue(v) + ")"}
	}
	var out []brokerscope.Grant
	var problems []string
	for i, e := range list {
		entryPath := path + "[" + itoa(i) + "]"
		m, ok := asMap(e)
		if !ok {
			problems = append(problems, entryPath+`: expected {"kind": "read", "repo": "*"} (got `+pyReprValue(e)+")")
			continue
		}
		before := len(problems)
		reportUnknownKeysTo(&problems, m, knownBrokeredGrantKeys, entryPath)
		kv, _ := m.Get(brokeredGrantKindKey)
		rv, _ := m.Get(brokeredGrantRepoKey)
		_, hasBranch := m.Get(brokeredGrantBranchKey)
		_, hasTag := m.Get(brokeredGrantTagKey)
		_, hasWorkflow := m.Get(brokeredGrantWorkflowKey)
		switch {
		case kv != readGrantKind:
			problems = append(problems, entryPath+"."+brokeredGrantKindKey+": "+pyReprValue(kv)+" is not a grant "+
				"the user config holds: it holds only {\"kind\": \"read\", \"repo\": \"*\"}, read access for "+
				"every workspace to its own approved remotes. Any other grant names its repository in the "+
				"workspace config of the project that holds it, "+WorkspaceConfigName+", or "+
				WorkspaceLocalConfigName+" beside it, and the user config allows its kind in `"+
				brokeredGrantKindsPath(source)+"`. Move it there, or remove it.")
		case rv != brokerscope.AnyRepo:
			problems = append(problems, entryPath+"."+brokeredGrantRepoKey+": a read grant in the user config "+
				"names \"*\", meaning each workspace's own approved remotes (got "+pyReprValue(rv)+"). To "+
				"grant read on chosen repositories only, add \"read\" to `"+brokeredGrantKindsPath(source)+
				"` here and name the grant in each project's workspace config instead.")
		case hasBranch:
			problems = append(problems, entryPath+"."+brokeredGrantBranchKey+": a read grant names no branch. "+
				"Remove this key.")
		case hasTag:
			problems = append(problems, entryPath+"."+brokeredGrantTagKey+": a read grant names no tag. Remove this key.")
		case hasWorkflow:
			problems = append(problems, entryPath+"."+brokeredGrantWorkflowKey+": a read grant names no workflow. Remove this key.")
		}
		if len(problems) > before {
			continue
		}
		g := brokerscope.Grant{Kind: readGrantKind, Repo: brokerscope.AnyRepo}
		if slices.Contains(out, g) {
			problems = append(problems, entryPath+": the grant \"read *\" is listed twice; list it once")
			continue
		}
		out = append(out, g)
	}
	return out, problems
}

// allowedGrantKinds is the kinds a workspace grant of source may be of: the user's
// `grant_kinds`, and `read` too where the user config holds a read grant itself, so a workspace
// naming one is redundant rather than refused.
func allowedGrantKinds(source string) []string {
	return allowedGrantKindsFromUser(source, UserScopeConfigOrEmpty())
}

func allowedGrantKindsFromUser(source string, user *jsonx.OrderedMap) []string {
	allowed := grantKindsOf(user, source)
	if len(userGrantsOf(user, source)) > 0 && !inList(allowed, readGrantKind) {
		allowed = append(allowed, readGrantKind)
	}
	return allowed
}

// BrokeredUserGrants is the grants the user's own config holds for source, for every
// workspace (BB-D107): `read *` when it lists one, else nil. It reads the user scope alone, and
// skips a malformed entry, which the validator reports.
func BrokeredUserGrants(source string) []brokerscope.Grant {
	return userGrantsOf(UserScopeConfigOrEmpty(), source)
}

func userGrantsOf(user *jsonx.OrderedMap, source string) []brokerscope.Grant {
	v, _ := user.Get(brokeredKey)
	sources, ok := asMap(v)
	if !ok {
		return nil
	}
	sv, _ := sources.Get(source)
	src, ok := asMap(sv)
	if !ok {
		return nil
	}
	gv, has := src.Get(brokeredGrantsKey)
	if !has || gv == nil {
		return nil
	}
	grants, _ := userGrantsValue(source, "", gv)
	return grants
}

// BrokerGrantAdvice is the next step every refusal of a broker that grants nothing names
// (BB-D108, BB-D109): the user-config read grant for every workspace, and `yolo config apply`.
// It names the source's key, never a loophole or tool name core would have to know.
func BrokerGrantAdvice(source string) string {
	return "To let every workspace read its own approved remotes through it, add {\"kind\": \"read\", " +
		"\"repo\": \"*\"} to the list at `" + brokeredGrantsPath(source) + "` in your user config, " +
		paths.UserConfigDisplayPath() + " (written \"brokered\": {\"" + source + "\": {\"grants\": [{\"kind\": " +
		"\"read\", \"repo\": \"*\"}]}} where it has no `brokered` key), then run `yolo config apply` on " +
		"the host, in the workspace, and approve the grant there."
}

// BrokerGrantAdviceFor is BrokerGrantAdvice for a caller holding the source's declaration. A
// user config that is TOML (or that does not exist yet, which a first write makes TOML) takes
// the grant in the readable form, the source's read capability under `allow.<source>`, so the
// next step is the verb that writes it, `yolo allow <source> <capability>`
// (docs/design/config-commands.md §4), not a `brokered.<source>.grants` entry.
func BrokerGrantAdviceFor(b loopholedecl.Brokered) string {
	return brokerReadGrantAdvice(b.Source, b.ReadCapability)
}

// brokerReadGrantAdvice is BrokerGrantAdviceFor from the two fields it reads, for a caller
// holding them apart from a declaration (a broker daemon knows its own source and read
// capability). With no read capability there is no readable verb to name, so it is the legacy
// advice whatever the user config's format. A user config still in legacy JSONC gets the same
// verb behind the conversion that makes it usable, `yolo config migrate` (the verb does not
// write a legacy file), never a hand edit of the legacy `brokered.<source>.grants` list
// (docs/design/config-commands.md §4).
func brokerReadGrantAdvice(source, readCapability string) string {
	if readCapability == "" {
		return BrokerGrantAdvice(source)
	}
	path, toml := UserConfigFile()
	allow := "`" + BrokerReadAllowCommand(source, readCapability) + "`"
	if !toml {
		return "To let every workspace read its own approved remotes through it, convert your user config, " +
			path + ", to TOML with `yolo config migrate` on the host, then run " + allow + " there (it adds " +
			pyReprValue(readCapability) + " to `allow." + source + ".always` in the converted file), then " +
			"approve the workspace change it offers, or run `yolo config apply` on the host, in the workspace."
	}
	return "To let every workspace read its own approved remotes through it, run " + allow + " on the host " +
		"(it adds " + pyReprValue(readCapability) + " to `allow." + source + ".always` in your user config, " +
		path + ", or the file of it that already holds [allow]), then approve the workspace change it " +
		"offers, or run `yolo config apply` on the host, in the workspace."
}

// BrokerReadAllowCommand is the command the read-grant advice names: the user-ceiling verb that
// allows the source's read capability for every project. internal/cli pins that it parses.
func BrokerReadAllowCommand(source, readCapability string) string {
	return "yolo allow " + source + " " + readCapability
}

// BrokerNoReadRefusal is a broker's refusal of a call on repo (spelled as the source spells it)
// that no read grant covers (BB-D108): nothing ran, the grant to add, and how this jail's broker
// takes it — at its next call where the launch's scope is live, otherwise at a fresh launch.
// readCapability is the source's declared read capability (its manifest's
// `brokered.read_capability`), which the grant advice names to a TOML user as
// `yolo allow <source> <readCapability>` (brokerReadGrantAdvice).
func BrokerNoReadRefusal(source, readCapability, repo string, live bool) string {
	next := " This jail's running broker takes the grant at its next call."
	if !live {
		next = " This jail's broker cannot take a new grant while it runs, so restart the jail afterwards " +
			"and approve the grant at the launch's config-change gate."
	}
	return "this workspace holds no read grant on " + repo + ", so nothing ran: the " + source + " broker runs " +
		"whenever its pack is selected, but grants nothing until a grant in config does. " +
		brokerReadGrantAdvice(source, readCapability) + next
}

// The keys one entry of `brokered.<source>.sets` may hold (boundary-broker.md §5.7): the set's
// name and mode, the window a windowed set is granted for with its ceiling and use cap, and the
// command rules it admits. Like the source object, a set has one voice — an unknown key is
// refused, since a typo would otherwise drop a rule or a bound with nothing said.
const (
	brokeredSetNameKey      = "name"
	brokeredSetModeKey      = "mode"
	brokeredSetWindowKey    = "window"
	brokeredSetMaxWindowKey = "max_window"
	brokeredSetUsesKey      = "uses"
	brokeredSetAdmitKey     = "admit"
)

const (
	brokeredSetModeStanding = "standing"
	brokeredSetModeWindowed = "windowed"
)

var knownBrokeredSetKeys = set(brokeredSetNameKey, brokeredSetModeKey, brokeredSetWindowKey,
	brokeredSetMaxWindowKey, brokeredSetUsesKey, brokeredSetAdmitKey)

// knownBrokeredSourceKeys are the keys a source object may hold: `repos` and the retired
// `workspaces`, each with its own message at the scope that refuses it, the user-scope `sets`
// (BB-D23, BB-P9), and the two grant layers, `grant_kinds` (user) and `grants` (workspace).
// Keeping each known at both scopes is what lets each scope answer in its own words — one
// validates the key, the other refuses it — rather than either falling back to the generic
// unknown-key message.
var knownBrokeredSourceKeys = set(brokeredReposKey, brokeredWorkspacesKey, brokeredSetsKey,
	brokeredGrantsKey, brokeredGrantKindsKey)

// brokeredGrantsPath is `brokered.<source>.grants`, the workspace half of a user grant.
func brokeredGrantsPath(source string) string {
	return brokeredKey + "." + source + "." + brokeredGrantsKey
}

// brokeredGrantKindsPath is `brokered.<source>.grant_kinds`, the user-scope half.
func brokeredGrantKindsPath(source string) string {
	return brokeredKey + "." + source + "." + brokeredGrantKindsKey
}

// BrokeredGrantKinds is the grant kinds the user's own config allows for source
// (`brokered.<source>.grant_kinds`), each valid entry once, in the order written. It reads the
// user scope alone: a workspace's copy is refused rather than read. nil when none is listed,
// which allows no grant.
func BrokeredGrantKinds(source string) []string {
	return grantKindsOf(UserScopeConfigOrEmpty(), source)
}

func grantKindsOf(user *jsonx.OrderedMap, source string) []string {
	v, _ := user.Get(brokeredKey)
	sources, ok := asMap(v)
	if !ok {
		return nil
	}
	sv, _ := sources.Get(source)
	src, ok := asMap(sv)
	if !ok {
		return nil
	}
	kv, _ := src.Get(brokeredGrantKindsKey)
	list, _ := kv.([]any)
	var out []string
	for _, e := range list {
		if k, ok := e.(string); ok && loopholedecl.ValidGrantKind(k) && !inList(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// grantKindsProblems is every problem in a user-scope `grant_kinds` value, each starting with
// path.
func grantKindsProblems(path string, v any) []string {
	list, ok := v.([]any)
	if !ok {
		return []string{path + `: expected a list of grant kinds such as ["create-branch"] (got ` +
			pyReprValue(v) + ")"}
	}
	var out []string
	var seen []string
	for i, e := range list {
		k, ok := e.(string)
		switch {
		case !ok || !loopholedecl.ValidGrantKind(k):
			out = append(out, path+"["+itoa(i)+"]: "+pyReprValue(e)+" is not a grant kind: a lowercase "+
				"letter, then lowercase letters, digits and hyphens, such as \"create-branch\"")
		case inList(seen, k):
			out = append(out, path+"["+itoa(i)+"]: "+pyReprValue(k)+" is listed twice; list each kind once")
		default:
			seen = append(seen, k)
		}
	}
	return out
}

// brokeredReposPath is `brokered.<source>.repos`, the key every core-side text names (WW-D13).
func brokeredReposPath(source string) string {
	return brokeredKey + "." + source + "." + brokeredReposKey
}

// BrokeredReposKey is brokeredReposPath for callers outside the package.
func BrokeredReposKey(source string) string { return brokeredReposPath(source) }

// brokeredSetsPath is `brokered.<source>.sets`, the user-scope half of the key (BB-D23).
func brokeredSetsPath(source string) string {
	return brokeredKey + "." + source + "." + brokeredSetsKey
}

// BrokeredSet is one entry of a source's user-scope `sets` list (boundary-broker.md §5.7): a
// named permission set a grant can hand over. Only the fields the user WROTE are set, so a
// windowed set that names no window leaves the source's default in force; Disclosure says what
// the user configured.
type BrokeredSet struct {
	Name      string
	Mode      string // "standing" or "windowed"
	Window    string // windowed only: "15m", "1h", …, "" for the source's default
	MaxWindow string // windowed only: a duration or "session", "" for the source's default
	Uses      int    // windowed only: the use cap, 0 for the source's default
	Admit     []string
}

// BrokeredSets is the user-scope permission sets configured for source, in the order written,
// each valid entry once and a malformed one skipped (the validator is what reports it). nil
// when the source names none, which leaves its defaults in force. It reads the user scope alone:
// the sets are user scope only (BB-D23), and a workspace's copy is refused rather than read.
func BrokeredSets(source string) []BrokeredSet {
	v, _ := UserScopeConfigOrEmpty().Get(brokeredKey)
	sources, ok := asMap(v)
	if !ok {
		return nil
	}
	sv, _ := sources.Get(source)
	src, ok := asMap(sv)
	if !ok {
		return nil
	}
	rv, has := src.Get(brokeredSetsKey)
	if !has {
		return nil
	}
	sets, _ := brokeredSetsValue(brokeredSetsPath(source), rv)
	return sets
}

// CurrentBrokeredSets reads host user policy afresh and refuses malformed entries.
// Unlike the disclosure reader, it must never skip a bad rule or fall back on a read error.
func CurrentBrokeredSets(source string) ([]BrokeredSet, error) {
	cfg, err := UserScopeConfig(true, nil)
	if err != nil {
		return nil, err
	}
	v, has := cfg.Get(brokeredKey)
	if !has || v == nil {
		return nil, nil
	}
	sources, ok := asMap(v)
	if !ok {
		return nil, fmt.Errorf("invalid brokered host policy")
	}
	v, has = sources.Get(source)
	if !has || v == nil {
		return nil, nil
	}
	src, ok := asMap(v)
	if !ok {
		return nil, fmt.Errorf("invalid brokered.%s host policy", source)
	}
	v, has = src.Get(brokeredSetsKey)
	if !has {
		return nil, nil
	}
	sets, problems := brokeredSetsValue(brokeredSetsPath(source), v)
	if len(problems) != 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	if len(sets) == 0 {
		return nil, fmt.Errorf("empty permission sets; configure host permission sets before retrying")
	}
	return sets, nil
}

// Disclosure is the one-line rendering of a set for the launch's disclosure (BB-D23): the name,
// a windowed set's window and any ceiling and use cap it names, and the rules it admits. A rule
// the user wrote is shown with the doc's `+` lead, and control characters cannot reach here (the
// validator refuses them).
func (s BrokeredSet) Disclosure() string {
	b := s.Name
	if s.Mode == brokeredSetModeWindowed {
		if s.Window != "" {
			b += " " + s.Window
		}
		if s.MaxWindow != "" {
			b += " max " + s.MaxWindow
		}
		if s.Uses > 0 {
			b += " " + itoa(s.Uses) + " uses"
		}
	}
	if len(s.Admit) > 0 {
		rules := make([]string, len(s.Admit))
		for i, r := range s.Admit {
			rules[i] = "+ " + r
		}
		b += " (" + strings.Join(rules, ", ") + ")"
	}
	return b
}

// BrokeredSetsDisclosure joins one source's sets for the launch line, "" for none. The caller
// prints it: "<loophole>: sets from user config: <this>" (BB-D23).
func BrokeredSetsDisclosure(sets []BrokeredSet) string {
	parts := make([]string, len(sets))
	for i, s := range sets {
		parts[i] = s.Disclosure()
	}
	return strings.Join(parts, ", ")
}

// brokeredSetsValue parses a `sets` list, returning the valid sets and one message per problem,
// each starting with path. A malformed entry is SKIPPED, never repaired: a reader that does not
// validate admits nothing it could not read whole.
func brokeredSetsValue(path string, v any) ([]BrokeredSet, []string) {
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, []string{path + `: expected a list of sets, each {"name": "…", "mode": "standing"|"windowed"} (got ` +
			pyReprValue(v) + ")"}
	}
	if len(list) == 0 {
		return nil, []string{path + ": an empty list defines no set; remove the key to keep the " +
			"source's default sets"}
	}
	var out []BrokeredSet
	var problems []string
	seen := map[string]bool{}
	for i, e := range list {
		entryPath := path + "[" + itoa(i) + "]"
		m, ok := asMap(e)
		if !ok {
			problems = append(problems, entryPath+": expected an object (got "+pyReprValue(e)+")")
			continue
		}
		reportUnknownKeysTo(&problems, m, knownBrokeredSetKeys, entryPath)
		set, ok := brokeredSetEntry(entryPath, m, seen, &problems)
		if !ok {
			continue
		}
		seen[set.Name] = true
		out = append(out, set)
	}
	return out, problems
}

// brokeredSetEntry validates one set object, appending a message per problem and reporting ok
// false when the set must be skipped. seen is the names already accepted, so a name written
// twice is refused rather than silently resolving to the last.
func brokeredSetEntry(path string, m *jsonx.OrderedMap, seen map[string]bool, problems *[]string) (BrokeredSet, bool) {
	var s BrokeredSet
	good := true
	bad := func(msg string) {
		good = false
		*problems = append(*problems, msg)
	}

	nv, _ := m.Get(brokeredSetNameKey)
	switch name, ok := nv.(string); {
	case !ok:
		bad(path + "." + brokeredSetNameKey + ": expected a set name — a lowercase letter, then " +
			"lowercase letters, digits and hyphens (got " + pyReprValue(nv) + ")")
	case !validBrokeredSetName(name):
		bad(path + "." + brokeredSetNameKey + ": " + pyReprValue(name) + " is not a set name: a " +
			"lowercase letter, then lowercase letters, digits and hyphens")
	case seen[name]:
		bad(path + "." + brokeredSetNameKey + ": " + pyReprValue(name) + " is named twice; a " +
			"source's sets are one ordered list, each name once")
	default:
		s.Name = name
	}

	mode, _ := m.Get(brokeredSetModeKey)
	switch mode {
	case brokeredSetModeStanding, brokeredSetModeWindowed:
		s.Mode, _ = mode.(string)
	default:
		bad(path + "." + brokeredSetModeKey + ": expected " + pyReprValue(brokeredSetModeStanding) +
			" or " + pyReprValue(brokeredSetModeWindowed) + " (got " + pyReprValue(mode) + ")")
	}

	if s.Mode == brokeredSetModeStanding {
		for _, k := range []string{brokeredSetWindowKey, brokeredSetMaxWindowKey, brokeredSetUsesKey} {
			if _, has := m.Get(k); has {
				bad(path + "." + k + ": only a windowed set has a window, a ceiling or a use cap; a " +
					"standing set runs until it is switched off")
			}
		}
	}
	if s.Mode == brokeredSetModeWindowed {
		if wv, has := m.Get(brokeredSetWindowKey); has {
			w, ok := wv.(string)
			if !ok || !brokeredWindow(w, false) {
				bad(path + "." + brokeredSetWindowKey + `: expected a duration such as "15m" or "1h" (got ` +
					pyReprValue(wv) + ")")
			} else {
				s.Window = w
			}
		}
		if mv, has := m.Get(brokeredSetMaxWindowKey); has {
			mw, ok := mv.(string)
			if !ok || !brokeredWindow(mw, true) {
				bad(path + "." + brokeredSetMaxWindowKey + `: expected a duration such as "1h", or ` +
					`"session" for the jail's life (got ` + pyReprValue(mv) + ")")
			} else {
				s.MaxWindow = mw
			}
		}
		if uv, has := m.Get(brokeredSetUsesKey); has {
			n, ok := jsonx.AsInt(uv)
			if !ok || n < 1 {
				bad(path + "." + brokeredSetUsesKey + ": expected a whole number of uses, at least 1 (got " +
					pyReprValue(uv) + ")")
			} else {
				s.Uses = int(n)
			}
		}
	}

	if av, has := m.Get(brokeredSetAdmitKey); has {
		s.Admit = []string{}
		list, ok := av.([]any)
		switch {
		case !ok:
			bad(path + "." + brokeredSetAdmitKey + ": expected a list of command-rule strings (got " +
				pyReprValue(av) + ")")
		default:
			for i, r := range list {
				rule, ok := r.(string)
				if !ok || strings.TrimSpace(rule) == "" {
					bad(path + "." + brokeredSetAdmitKey + "[" + itoa(i) + "]: expected a non-empty " +
						"command rule (got " + pyReprValue(r) + ")")
					continue
				}
				if termsafe.HasUnsafe(rule) {
					bad(path + "." + brokeredSetAdmitKey + "[" + itoa(i) + "]: the rule " +
						pyReprValue(termsafe.Visible(rule)) + " carries a control character; a rule is plain text")
					continue
				}
				s.Admit = append(s.Admit, rule)
			}
		}
	}
	return s, good
}

// validBrokeredSetName reports whether s is a set name: a lowercase letter, then lowercase
// letters, digits and hyphens.
func validBrokeredSetName(s string) bool {
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

// brokeredWindow reports whether s is a window: a positive Go duration, and for a ceiling also
// `session`, the bound that names the jail's whole life (BB-D29). `session` written for a window
// is refused, since the window is what the grant is for and the ceiling is what bounds it.
func brokeredWindow(s string, ceiling bool) bool {
	if s == "session" {
		return ceiling
	}
	d, err := time.ParseDuration(s)
	return err == nil && d > 0
}

// approvalConfigPart is the approval record's config part: the workspace config with `brokered`
// left out (WW-D11). The entry is approved in the scope part, as rows of the labeled block, and
// never as JSON lines in the config diff (WW-D4), so it must not move the config part's bytes.
//
// It is applied BEFORE SnapshotJSON, at every comparison and every writer of the config part,
// and whether or not a broker starts, so turning the broker on or off never moves those bytes. It
// never goes inside SnapshotJSON, which drift, the delivery copy and the inherited files share.
//
// It also carries every workspace `mounts` element's source AS RESOLVED (mountRecordKey,
// oq-cx-symlink in docs/reference/context-mounts.md): a mount approved as "<link>" must ask again
// once the link points elsewhere, so the record holds what the human approved, not only what the
// agent wrote.
func approvalConfigPart(wsCfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	return approvalConfigPartWith(wsCfg, nil)
}

// approvalConfigPartWith is approvalConfigPart with the host paths the gate already resolved
// (WorkspaceRead.Mounts), so the record holds exactly the destinations the launch binds.
func approvalConfigPartWith(wsCfg *jsonx.OrderedMap, resolved map[string]string) *jsonx.OrderedMap {
	if wsCfg == nil {
		return jsonx.NewOrderedMap()
	}
	part := wsCfg
	if _, has := wsCfg.Get(brokeredKey); has {
		part = jsonx.NewOrderedMap()
		for _, k := range wsCfg.Keys() {
			if k == brokeredKey {
				continue
			}
			v, _ := wsCfg.Get(k)
			part.Set(k, v)
		}
	}
	return withMountRecord(part, wsCfg, resolved)
}

// validateBrokered checks the `brokered` key, each scope read on its own: the merged value
// mixes them, and a leftover user-scope `workspaces` beside a workspace `repos` would combine
// under one source.
//
//   - The retired `workspaces` form is read from the merged config, which is what catches an old
//     host-written .yolo/config-assembled.json inside a jail.
//   - The user scope's `repos` is refused, read from the user scope directly.
//   - The workspace's own value is checked file by file: its shape, where it was written, and a
//     key written twice.
//
// A source no selected pack brokers is not reported here, because a launch runs this too, and
// a committed entry would print it at every launch of every contributor who selects no such
// pack (WW-D22). Host `yolo check` reports it instead (UnbrokeredSourceWarnings).
func validateBrokered(config *jsonx.OrderedMap, workspace string, resolver LoopholeResolver, errs, warns *[]string, edit ...*editValidationContext) {
	if _, present := config.Get(brokeredKey); !present {
		// Every scope's keys survive into the merged map, so an absent key proves no scope
		// wrote one.
		return
	}
	validateBrokeredRetired(config, workspace, errs, warns)
	validateBrokeredUserScope(errs, warns, edit...)
	validateBrokeredWorkspace(workspace, resolver, errs, warns, edit...)
}

// inJailSuffix is the retired-key convention's in-jail ending (validateAgentsRetired).
const inJailSuffix = " (ignored here: this is the host-generated config snapshot, so remove the " +
	"key from the HOST config.)"

// validateBrokeredRetired refuses `brokered.<source>.workspaces`, the user-scope form that
// shipped in 0.11.1, at every scope (WW-D2, WW-D21). The key alone triggers it, whatever its
// shape, and its type checks went with it.
//
// THE MESSAGE NAMES THIS PROJECT ALONE. Everything a launch prints is teed into that
// workspace's .yolo/launch.log, which its jail reads, and this validator has three callers and no
// caller mode. So no form of it names another project's path or repositories: it gives the exact
// edit for the workspace being validated and counts the rest. Host `yolo check` prints every
// project's edit from a section of its own (RetiredBrokeredMoves).
func validateBrokeredRetired(config *jsonx.OrderedMap, workspace string, errs, warns *[]string) {
	v, _ := config.Get(brokeredKey)
	sources, ok := asMap(v)
	if !ok {
		return
	}
	for _, name := range sources.Keys() {
		sv, _ := sources.Get(name)
		src, ok := asMap(sv)
		if !ok {
			continue
		}
		wv, has := src.Get(brokeredWorkspacesKey)
		if !has {
			continue
		}
		msg := "config." + brokeredKey + "." + name + "." + brokeredWorkspacesKey + ": RETIRED — a " +
			"workspace's extra repositories are no longer listed in the user config, keyed by its " +
			"path. Each project lists its own in its workspace config, as `" + brokeredReposPath(name) +
			"`, and approves them at its next launch."
		if inJail() {
			add(warns, msg+inJailSuffix)
			continue
		}
		add(errs, msg+retiredMoveAdvice(name, wv, workspace))
	}
}

// retiredMoveAdvice is the launch form of the retired message's next step: this project's edit,
// a count of the others, and where the full list is printed.
func retiredMoveAdvice(source string, wv any, workspace string) string {
	moves, ok := retiredMoves(source, wv)
	if !ok {
		return " Move each repository it lists into its project's workspace config, then remove " +
			"the key."
	}
	want := expandAndResolve(workspaceOrCwd(workspace))
	var own []string
	others, unreadable := 0, 0
	for _, m := range moves {
		switch {
		case m.Problem != "":
			unreadable++
		case brokeredKeyNames(m.Key, want):
			own = append(own, m.Repos...)
		default:
			others++
		}
	}
	var b strings.Builder
	if own = dedupeRepos(own); len(own) > 0 {
		_, local := ResolveWorkspaceConfigPath(workspaceOrCwd(workspace), WorkspaceLocalConfigName)
		_, committed := ResolveWorkspaceConfigPath(workspaceOrCwd(workspace), WorkspaceConfigName)
		b.WriteString(" For this project, put " + reposSpelling(source, own) + " in " + local +
			", which yolo does not git-ignore, so check this project's .gitignore; a repository " +
			"the project itself needs can go in " + committed + " instead. The next launch here " +
			"asks you to approve them.")
	}
	if n := others + unreadable; n > 0 {
		noun := "projects have entries"
		if n == 1 {
			noun = "project has an entry"
		}
		fmt.Fprintf(&b, " %d other %s under this key; host `yolo check` prints the edit for each.", n, noun)
	}
	b.WriteString(" Then remove the `" + brokeredWorkspacesKey + "` key.")
	return b.String()
}

// BrokeredMove is one entry of the retired `brokered.<source>.workspaces` form, as host
// `yolo check` prints its move: the key as written, and the repositories it listed, or why the
// key cannot be read.
type BrokeredMove struct {
	Source, Key string
	Repos       []string
	// LocalFile is the local config file of the project the key names, where the move goes.
	LocalFile string
	// Problem is why the key names no project, "" when it does.
	Problem string
}

// RetiredBrokeredMoves is every entry of the retired form in the user scope, for host
// `yolo check`'s own section. It reads the user scope directly, and is never part of a message
// the shared validator prints (validateBrokeredRetired states why).
func RetiredBrokeredMoves() []BrokeredMove {
	if inJail() {
		return nil
	}
	v, _ := UserScopeConfigOrEmpty().Get(brokeredKey)
	sources, ok := asMap(v)
	if !ok {
		return nil
	}
	var out []BrokeredMove
	for _, name := range sources.Keys() {
		sv, _ := sources.Get(name)
		src, ok := asMap(sv)
		if !ok {
			continue
		}
		wv, has := src.Get(brokeredWorkspacesKey)
		if !has {
			continue
		}
		moves, ok := retiredMoves(name, wv)
		if !ok {
			out = append(out, BrokeredMove{Source: name, Problem: "the key is not an object of " +
				"workspace path -> {\"repos\": [...]}"})
			continue
		}
		for _, m := range moves {
			if m.Problem == "" {
				local, _ := ResolveWorkspaceConfigPath(expandAndResolve(m.Key), WorkspaceLocalConfigName)
				m.LocalFile = local
			}
			out = append(out, m)
		}
	}
	return out
}

// Edit is the move as one line: what to put where, or why the key names no project.
func (m BrokeredMove) Edit() string {
	if m.Problem != "" {
		return m.Key + ": " + m.Problem + "; move each repository it lists into that project's " +
			"workspace config"
	}
	if len(m.Repos) == 0 {
		return m.Key + ": lists no repository; remove it"
	}
	return m.Key + ": put " + reposSpelling(m.Source, m.Repos) + " in " + m.LocalFile +
		", which yolo does not git-ignore"
}

// retiredMoves reads the retired form's entries, ok false when the value is not an object.
func retiredMoves(source string, wv any) ([]BrokeredMove, bool) {
	workspaces, ok := asMap(wv)
	if !ok {
		return nil, false
	}
	var out []BrokeredMove
	for _, key := range workspaces.Keys() {
		m := BrokeredMove{Source: source, Key: key, Problem: brokeredWorkspaceKeyProblem(key)}
		ev, _ := workspaces.Get(key)
		entry, _ := asMap(ev)
		if entry != nil {
			rv, _ := entry.Get(brokeredReposKey)
			list, _ := rv.([]any)
			for _, e := range list {
				if r, ok := e.(string); ok && brokerscope.ValidPattern(r) {
					m.Repos = append(m.Repos, r)
				}
			}
		}
		m.Repos = dedupeRepos(m.Repos)
		out = append(out, m)
	}
	return out, true
}

// dedupeRepos keeps each repository once, compared without case, the first spelling kept, and
// sorts them the way GitHub compares names.
func dedupeRepos(repos []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range repos {
		if seen[strings.ToLower(r)] {
			continue
		}
		seen[strings.ToLower(r)] = true
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// reposSpelling is `"brokered": {"<source>": {"repos": [...]}}`, the source and each repository
// JSON-quoted as the user would type them (no HTML escaping).
func reposSpelling(source string, repos []string) string {
	quote := func(s string) string {
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(s)
		return strings.TrimSuffix(b.String(), "\n")
	}
	quoted := make([]string, len(repos))
	for i, r := range repos {
		quoted[i] = quote(r)
	}
	return `"` + brokeredKey + `": {` + quote(source) + `: {"` + brokeredReposKey + `": [` +
		strings.Join(quoted, ", ") + `]}}`
}

// validateBrokeredUserScope checks the user scope's own `brokered` (WW-D9): a `repos` list there
// is refused, naming the workspace files instead, since with no workspace key it would widen every
// workspace; a `sets` list there is validated, since user scope is where the sets live (BB-D23).
// Other shapes are refused as they are at workspace scope; `workspaces` is the retired check's.
// In a jail the user scope is the host-generated one, and each refusal is a warning.
func validateBrokeredUserScope(errs, warns *[]string, edit ...*editValidationContext) {
	var userCfg *jsonx.OrderedMap
	var userSources *Sources
	if len(edit) != 0 && edit[0] != nil {
		userCfg, userSources = edit[0].rawUser, edit[0].userOrigins
	} else {
		userCfg, userSources = UserScopeConfigOrEmpty(), UserScopeSources()
	}
	if userCfg == nil {
		userCfg = jsonx.NewOrderedMap()
	}
	v, present := userCfg.Get(brokeredKey)
	if !present || v == nil {
		return
	}
	refuse := func(msg string) {
		if userSources != nil {
			msg = userSources.AnnotateOne(msg)
		}
		if inJail() {
			add(warns, msg+inJailSuffix)
			return
		}
		add(errs, msg)
	}
	sources, ok := asMap(v)
	if !ok {
		refuse("config." + brokeredKey + ": expected an object of source name -> {\"sets\": " +
			"[...]} (got " + pyReprValue(v) + ")")
		return
	}
	for _, name := range sources.Keys() {
		sv, _ := sources.Get(name)
		if sv == nil {
			continue
		}
		path := "config." + brokeredKey + "." + name
		src, ok := asMap(sv)
		if !ok {
			refuse(path + ": expected an object holding \"sets\" (got " + pyReprValue(sv) + ")")
			continue
		}
		var unknown []string
		reportUnknownKeysTo(&unknown, src, knownBrokeredSourceKeys, path)
		for _, u := range unknown {
			refuse(u)
		}
		if rv, has := src.Get(brokeredReposKey); has && rv != nil {
			refuse(path + "." + brokeredReposKey + ": not in the user config — a list here, with no " +
				"workspace to key it, would widen every workspace's repository scope. Put each " +
				"repository in the workspace config of the project that needs it, " +
				WorkspaceConfigName + ", or " + WorkspaceLocalConfigName + " beside it for what the " +
				"project should not commit, and approve it at that project's next launch. Then " +
				"remove this key from " + paths.UserConfigDisplayPath() + ".")
		}
		if setsV, has := src.Get(brokeredSetsKey); has && setsV != nil {
			_, problems := brokeredSetsValue(path+"."+brokeredSetsKey, setsV)
			for _, p := range problems {
				refuse(p)
			}
		}
		if gv, has := src.Get(brokeredGrantsKey); has && gv != nil {
			_, problems := userGrantsValue(name, path+"."+brokeredGrantsKey, gv)
			for _, p := range problems {
				refuse(p)
			}
		}
		if kv, has := src.Get(brokeredGrantKindsKey); has && kv != nil {
			for _, p := range grantKindsProblems(path+"."+brokeredGrantKindsKey, kv) {
				refuse(p)
			}
		}
	}
}

// validateBrokeredWorkspace checks the workspace's own `brokered`, read from its files with
// their provenance: the shape of the value in effect, located at the file and line each problem
// was written at; any file outside the workspace that wrote it (WW-D17); and a key written twice
// in one file (WW-D24). Errors in a jail too: there the files are the agent's own, and in-jail
// `yolo check --no-build` is how it checks its edit.
//
// A `repos` entry for a source a known loophole brokers on any host (`remote_host: "*"`) is
// refused, through the resolver and never by the source's name: that source's scope is every
// remote URL, matched exactly, so a git remote joins with `git remote add` and reaches the gate as
// a new remote (docs/design/git-broker-plan.md ID-2).
//
// Each `brokered.<source>.grants` entry is checked too (brokeredGrantProblems): its shape, a
// kind the source defines (through the resolver) and the user's own config allows.
func validateBrokeredWorkspace(workspace string, resolver LoopholeResolver, errs, warns *[]string, edit ...*editValidationContext) {
	wsCfg, src, err := loadValidationWorkspaceConfigWithSources(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	v, present := wsCfg.Get(brokeredKey)
	if !present {
		return
	}
	node := src.node().key(brokeredKey)
	for _, p := range brokeredOutsideProblems(node) {
		add(errs, p)
	}
	for _, p := range brokeredWrittenTwiceProblems(node) {
		add(errs, p)
	}
	for _, p := range brokeredShapeProblems(v) {
		add(errs, src.AnnotateOne(p))
	}
	for _, p := range remoteURLSourceReposProblems(v, resolver) {
		add(errs, src.AnnotateOne(p))
	}
	var userConfig *jsonx.OrderedMap
	if len(edit) != 0 && edit[0] != nil {
		userConfig = edit[0].rawUser
	} else {
		userConfig = UserScopeConfigOrEmpty()
	}
	if userConfig == nil {
		userConfig = jsonx.NewOrderedMap()
	}
	gErrs, gWarns, gHostOnly := brokeredGrantProblemsWithUser(v, resolver, userConfig)
	for _, p := range gErrs {
		add(errs, src.AnnotateOne(p))
	}
	for _, p := range gWarns {
		add(warns, src.AnnotateOne(p))
	}
	for _, p := range gHostOnly {
		// The user's kinds are the host's user config, which a jail does not read as the host
		// does, so there the check is said and not enforced; the host's launch enforces it.
		if inJail() {
			add(warns, src.AnnotateOne(p)+" (checked against this jail's copy of the user config; "+
				"the host's launch enforces it.)")
			continue
		}
		add(errs, src.AnnotateOne(p))
	}
}

func brokeredGrantProblemsWithUser(v any, resolver LoopholeResolver, user *jsonx.OrderedMap) (errs, warns, userKind []string) {
	sources, ok := asMap(v)
	if !ok {
		return nil, nil, nil
	}
	defined := map[string][]string{}
	if resolver != nil {
		known, _ := resolver.Known()
		for _, info := range known {
			if info.Brokered != nil {
				defined[info.Brokered.Source] = definedGrantKinds(append(defined[info.Brokered.Source], info.Brokered.Grants...))
			}
		}
	}
	for _, name := range sources.Keys() {
		sv, _ := sources.Get(name)
		src, ok := asMap(sv)
		if !ok {
			continue
		}
		gv, has := src.Get(brokeredGrantsKey)
		if !has || gv == nil {
			continue
		}
		path := "config." + brokeredGrantsPath(name)
		list, ok := gv.([]any)
		if !ok {
			errs = append(errs, path+`: expected a list of grants, each {"kind": "create-branch", "repo": `+
				`"github.com/o/r"} (got `+pyReprValue(gv)+")")
			continue
		}
		kinds, sourceKnown := defined[name]
		allowed := allowedGrantKindsFromUser(name, user)
		var seen []string
		for i, e := range list {
			entryPath := path + "[" + itoa(i) + "]"
			m, ok := asMap(e)
			if !ok {
				errs = append(errs, entryPath+`: expected an object {"kind": "…", "repo": "…"} (got `+
					pyReprValue(e)+")")
				continue
			}
			reportUnknownKeysTo(&errs, m, knownBrokeredGrantKeys, entryPath)
			kv, _ := m.Get(brokeredGrantKindKey)
			kind, ok := kv.(string)
			if !ok || !loopholedecl.ValidGrantKind(kind) {
				errs = append(errs, entryPath+"."+brokeredGrantKindKey+": expected a grant kind such as "+
					"\"create-branch\" (got "+pyReprValue(kv)+")")
				continue
			}
			rv, _ := m.Get(brokeredGrantRepoKey)
			repo, ok := rv.(string)
			if !ok || !brokerscope.ValidGrantRepo(repo) {
				errs = append(errs, entryPath+"."+brokeredGrantRepoKey+": "+pyReprValue(rv)+" is not a "+
					"repository identity: the host and path a remote reduces to, such as \"github.com/o/r\" "+
					"(the host in lower case with a dot or a port, then the path with no `.git`, scheme or "+
					"user), or \"*\" for every repository of the scope")
				continue
			}
			if sourceKnown && !inList(kinds, kind) {
				defines := "none"
				if len(kinds) > 0 {
					defines = strings.Join(kinds, ", ")
				}
				errs = append(errs, entryPath+"."+brokeredGrantKindKey+": "+pyReprValue(kind)+" is not a "+
					"grant kind the "+name+" source defines (it defines: "+defines+"). Use one of those, or "+
					"remove this grant.")
				continue
			}
			why, refName := grantRefProblem(entryPath, kind, m)
			if why != "" {
				errs = append(errs, why)
				continue
			}
			if g := brokerscope.RefGrant(kind, repo, refName).String(); inList(seen, g) {
				errs = append(errs, entryPath+": the grant "+pyReprValue(g)+" is listed twice; list each once")
				continue
			} else {
				seen = append(seen, g)
			}
			if !inList(allowed, kind) {
				userKind = append(userKind, entryPath+"."+brokeredGrantKindKey+": your user config does not "+
					"allow a workspace to hold a "+pyReprValue(kind)+" grant. To allow it, add "+
					pyReprValue(kind)+" to `"+brokeredGrantKindsPath(name)+"` in "+paths.UserConfigDisplayPath()+
					"; otherwise remove this grant.")
			}
		}
	}
	return errs, warns, userKind
}

// remoteURLSourceReposProblems refuses `brokered.<source>.repos` for each source a known loophole
// brokers on any host (brokerscope.FormFor gives the remote-url form).
func remoteURLSourceReposProblems(v any, resolver LoopholeResolver) []string {
	sources, ok := asMap(v)
	if !ok || resolver == nil {
		return nil
	}
	known, _ := resolver.Known()
	anyHost := map[string]bool{}
	for _, info := range known {
		if info.Brokered != nil && brokerscope.FormFor(info.Brokered.RemoteHost) == brokerscope.FormRemoteURL {
			anyHost[info.Brokered.Source] = true
		}
	}
	var out []string
	for _, name := range sources.Keys() {
		if !anyHost[name] {
			continue
		}
		sv, _ := sources.Get(name)
		src, ok := asMap(sv)
		if !ok {
			continue
		}
		if rv, has := src.Get(brokeredReposKey); has && rv != nil {
			out = append(out, "config."+brokeredReposPath(name)+": not taken — this source's scope is "+
				"every remote of the workspace, on any host, as its URL, so there is nothing to list "+
				"here. Add a remote with `git remote add <name> <url>`, and the next launch's "+
				"config-change gate asks about it; then remove this key.")
		}
	}
	return out
}

// brokeredShapeProblems is every shape problem in a workspace `brokered` value, each a full
// message starting "config.brokered".
func brokeredShapeProblems(v any) []string {
	if v == nil {
		return nil
	}
	path := "config." + brokeredKey
	sources, ok := asMap(v)
	if !ok {
		return []string{path + `: expected an object of source name -> {"repos": ["OWNER/REPO", ...]} (got ` +
			pyReprValue(v) + ")"}
	}
	var out []string
	for _, name := range sources.Keys() {
		sv, _ := sources.Get(name)
		if sv == nil {
			continue
		}
		srcPath := path + "." + name
		src, ok := asMap(sv)
		if !ok {
			out = append(out, srcPath+`: expected an object holding "repos" (got `+pyReprValue(sv)+")")
			continue
		}
		reportUnknownKeysTo(&out, src, knownBrokeredSourceKeys, srcPath)
		if _, has := src.Get(brokeredSetsKey); has {
			out = append(out, srcPath+"."+brokeredSetsKey+": user scope only — a set decides what a "+
				"write grant hands over, and this workspace is the agent's to edit, so a workspace may "+
				"not name one (BB-P9). Put it in the user config, "+paths.UserConfigDisplayPath()+", and remove "+
				"this key.")
		}
		if _, has := src.Get(brokeredGrantKindsKey); has {
			out = append(out, srcPath+"."+brokeredGrantKindsKey+": user scope only — it lists the kinds of "+
				"grant a workspace may hold, and this workspace is the agent's to edit, so it may not "+
				"allow itself one (BB-D96). Put it in the user config, "+paths.UserConfigDisplayPath()+", and "+
				"remove this key.")
		}
		rv, _ := src.Get(brokeredReposKey)
		if rv == nil {
			continue
		}
		list, ok := rv.([]any)
		if !ok {
			out = append(out, srcPath+"."+brokeredReposKey+": expected a list of OWNER/REPO strings "+
				"or wildcard entries (got "+pyReprValue(rv)+")")
			continue
		}
		for i, e := range list {
			if r, ok := e.(string); !ok || !brokerscope.ValidPattern(r) {
				out = append(out, srcPath+"."+brokeredReposKey+"["+itoa(i)+"]: "+pyReprValue(e)+
					" is not a repository scope entry: OWNER/REPO, two names of letters, digits, `_`, `.` "+
					"and `-` with one `/` between them and no host, or a whole-segment wildcard — `*/*` "+
					"for every repository, `OWNER/*` for one owner's, `*/REPO` for a name under any owner. "+
					"A partial wildcard such as `org*` is not one")
			}
		}
	}
	return out
}

// brokeredWriters is every file that wrote any value at or under n, each once, in the order the
// record holds them.
func brokeredWriters(n *srcNode) []srcOrigin {
	seen := map[*srcFile]bool{}
	var out []srcOrigin
	var walk func(*srcNode)
	walk = func(n *srcNode) {
		if n == nil {
			return
		}
		for _, w := range n.writers {
			if w.file != nil && !seen[w.file] {
				seen[w.file] = true
				out = append(out, w)
			}
		}
		keys := make([]string, 0, len(n.keys))
		for k := range n.keys {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walk(n.keys[k])
		}
		for _, e := range n.elems {
			walk(e)
		}
	}
	walk(n)
	return out
}

// brokeredOutsideProblems is one refusal per file outside the workspace that wrote a `brokered`
// value: an include out of it, or a link out of it, including an absolute one (WW-D17). n is the
// `brokered` node of a workspace load that recorded, so every file it read beneath the root is
// marked contained.
func brokeredOutsideProblems(n *srcNode) []string {
	var out []string
	for _, w := range brokeredWriters(n) {
		if w.file.contained {
			continue
		}
		how := "a link that leads out of this workspace, or an absolute one"
		if w.file.via != "" {
			how = termsafe.Visible(w.file.via) + ", which leads out of this workspace"
		}
		out = append(out, w.String()+": config."+brokeredKey+": written in a file outside this "+
			"workspace, reached through "+how+". A `brokered` key is read only from this workspace's own "+
			"config files, so an include or a link cannot point its repository scope at another "+
			"project's list. Move the key into "+WorkspaceConfigName+" or "+WorkspaceLocalConfigName+
			", or make the link a relative one that stays inside the workspace.")
	}
	return out
}

// brokeredWrittenTwiceProblems is one refusal per `brokered`, `<source>` or `repos` key written
// more than once in one file (WW-D24): the decoder keeps the last one silently, so the
// repositories in the others would be dropped with nothing said.
func brokeredWrittenTwiceProblems(n *srcNode) []string {
	if n == nil {
		return nil
	}
	sources := make([]string, 0, len(n.keys))
	for k := range n.keys {
		sources = append(sources, k)
	}
	sort.Strings(sources)
	var out []string
	for _, w := range brokeredWriters(n) {
		paths := [][]json5.Step{{json5.Key(brokeredKey)}}
		for _, s := range sources {
			paths = append(paths, []json5.Step{json5.Key(brokeredKey), json5.Key(s)},
				[]json5.Step{json5.Key(brokeredKey), json5.Key(s), json5.Key(brokeredReposKey)})
		}
		for _, p := range paths {
			err := w.file.writtenTwice(p)
			if err == nil {
				continue
			}
			out = append(out, w.file.label()+": config."+strings.TrimPrefix(err.Error(), "json5: ")+
				". Merge them into one: the repositories in the others are dropped.")
			break
		}
	}
	return out
}

// UnbrokeredSourceWarnings is host `yolo check`'s report of a `brokered` source that no loophole
// a selected pack ships brokers (WW-D22): such an entry does nothing here, and a misspelled
// source is the case worth catching. Never a launch's: a committed entry would print it at every
// launch of every contributor who selects no such pack, with no step they could take. Each names
// the next step. Nil in a jail, where the selection is the host's.
func UnbrokeredSourceWarnings(merged *jsonx.OrderedMap, resolver LoopholeResolver) []string {
	v, _ := merged.Get(brokeredKey)
	sources, ok := asMap(v)
	if !ok || resolver == nil || inJail() {
		return nil
	}
	known, _ := resolver.Known()
	brokers := map[string]bool{}
	for _, info := range known {
		if info.Brokered != nil {
			brokers[info.Brokered.Source] = true
		}
	}
	have := make([]string, 0, len(brokers))
	for s := range brokers {
		have = append(have, s)
	}
	sort.Strings(have)
	names := "none"
	if len(have) > 0 {
		names = strings.Join(have, ", ")
	}
	var out []string
	for _, name := range sources.Keys() {
		if sv, _ := sources.Get(name); sv == nil || brokers[name] {
			continue
		}
		out = append(out, "config."+brokeredKey+"."+name+": no loophole a selected pack ships brokers "+
			"the source "+pyReprValue(name)+", so this entry does nothing here (brokered sources "+
			"here: "+names+"). Check the spelling against the loophole manifest's `brokered.source`, "+
			"or select the pack that ships it; if you use no such pack, nothing needs doing.")
	}
	return out
}

// UngrantedBrokerNotes are host `yolo check`'s up-front notes for each brokered loophole a
// selected pack installs whose source the user config grants nothing (docs/design/boundary-broker.md
// BB-D108, BB-D109, BB-D110). Selecting the pack with no grant is a legitimate choice, "basically
// a no-op option", so each is a plain note, never a warning: it says the broker grants nothing
// and names the read grant to add for anyone who meant otherwise. A user config listing `read`
// in `grant_kinds` leaves the grant to each workspace, and gets no note. Nil in a jail, whose
// user config is the host's snapshot.
func UngrantedBrokerNotes(resolver LoopholeResolver) []string {
	if resolver == nil || inJail() {
		return nil
	}
	known, _ := resolver.Known()
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	user := UserScopeConfigOrEmpty()
	var out []string
	for _, name := range names {
		info := known[name]
		if info.Brokered == nil {
			continue
		}
		source := info.Brokered.Source
		// The readable form of the same grant is the source's read capability under
		// `allow.<source>` (docs/design/config-user.md §6): `always` grants it, and `on_request`
		// leaves it to each workspace as `grant_kinds` does.
		if len(userGrantsOf(user, source)) > 0 || inList(grantKindsOf(user, source), readGrantKind) ||
			readableUserAllowsEither(user, source, info.Brokered.ReadCapability) {
			continue
		}
		out = append(out, "loophole "+name+" is selected and grants nothing: your user config holds no read "+
			"grant for the "+pyReprValue(source)+" source, so it refuses every call, and a workspace's "+
			"config-change prompt asks nothing about it (beyond showing, once, the removal of a read grant "+
			"that workspace approved earlier). "+BrokerGrantAdviceFor(*info.Brokered))
	}
	return out
}

// brokeredKeyNames reports whether one retired `workspaces` key names the resolved workspace
// want: the key, `~/` expanded and symlinks resolved, is want, and resolving it never passes
// through a folder or a link inside want. The per-workspace file matches its `workspace` field
// the same way (workspacefile.go).
//
// The second half applies to symlinks the rule that a workspace never chooses which host-side
// record applies to it. What lies inside the workspace is the agent's to change, so a key whose
// resolution passes through it resolves wherever the agent points it: without the check, a key
// for `~/code/app/sub` would match `~/code/app` once that jail's agent replaced `sub` with a link
// to `.`. A link the user made outside the workspace, pointing at the workspace itself, still
// matches, since nothing inside it is read.
func brokeredKeyNames(key, want string) bool {
	abs, err := filepath.Abs(expandUser(key))
	if err != nil || expandAndResolve(abs) != want {
		return false
	}
	return !resolvesThrough(abs, want)
}

// resolvesThrough reports whether resolving abs, an absolute path, one name at a time and
// following every link, visits a path strictly inside dir, a resolved folder. It answers true
// when it cannot finish the walk (a link it cannot read, or a link loop), so a key it cannot
// judge matches nothing.
func resolvesThrough(abs, dir string) bool {
	inside := strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)
	pending := strings.Split(abs, string(filepath.Separator))
	cur := string(filepath.Separator)
	for links := 0; len(pending) > 0; {
		name := pending[0]
		pending = pending[1:]
		switch name {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, name)
		if strings.HasPrefix(next, inside) {
			return true
		}
		fi, err := os.Lstat(next)
		if err != nil {
			// Nothing below a missing name is followed, so the walk visits nothing more.
			return false
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if links++; links > 255 {
			return true
		}
		target, err := os.Readlink(next)
		if err != nil {
			return true
		}
		if filepath.IsAbs(target) {
			cur = string(filepath.Separator)
		}
		pending = append(strings.Split(target, string(filepath.Separator)), pending...)
	}
	return false
}

// brokeredWorkspaceKeyProblem is why one workspace-path key is refused, "" when it is accepted:
// an absolute path, or one starting with `~/`. The retired form's keys and the per-workspace
// file's `workspace` field share it.
func brokeredWorkspaceKeyProblem(key string) string {
	switch {
	case key == "":
		return "an empty key names no workspace"
	case strings.Contains(key, "$"):
		return "it carries a `$`, and a workspace key expands no variable"
	case key == "~":
		return "`~` is your home, which no launch may use as a workspace; name the project folder, " +
			"as `~/<folder>`"
	case strings.HasPrefix(key, "~/"):
		return ""
	case strings.HasPrefix(key, "~"):
		return "`~user/` is not expanded; write the workspace as an absolute path, or `~/` for your own home"
	case !strings.HasPrefix(key, "/"):
		return "it is relative, and would name a different folder from every directory yolo is " +
			"started in; write the workspace's path absolute, or starting with `~/`"
	}
	return ""
}
