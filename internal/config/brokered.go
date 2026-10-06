package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
const (
	brokeredKey           = "brokered"
	brokeredWorkspacesKey = "workspaces"
	brokeredReposKey      = "repos"
)

// knownBrokeredSourceKeys are the keys a source object may hold: `repos`, and the retired
// `workspaces`, which stays known so that its targeted message is its only error.
var knownBrokeredSourceKeys = set(brokeredReposKey, brokeredWorkspacesKey)

// brokeredReposPath is `brokered.<source>.repos`, the key every core-side text names (WW-D13).
func brokeredReposPath(source string) string {
	return brokeredKey + "." + source + "." + brokeredReposKey
}

// BrokeredReposKey is brokeredReposPath for callers outside the package.
func BrokeredReposKey(source string) string { return brokeredReposPath(source) }

// approvalConfigPart is the approval record's config part: the workspace config with `brokered`
// left out (WW-D11). The entry is approved in the scope part, as rows of the labeled block, and
// never as JSON lines in the config diff (WW-D4), so it must not move the config part's bytes.
//
// It is applied BEFORE SnapshotJSON, at every comparison and every writer of the config part,
// and whether or not a broker starts, so turning the broker on or off never moves those bytes. It
// never goes inside SnapshotJSON, which drift, the delivery copy and the inherited files share.
func approvalConfigPart(wsCfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	if wsCfg == nil {
		return jsonx.NewOrderedMap()
	}
	if _, has := wsCfg.Get(brokeredKey); !has {
		return wsCfg
	}
	out := jsonx.NewOrderedMap()
	for _, k := range wsCfg.Keys() {
		if k == brokeredKey {
			continue
		}
		v, _ := wsCfg.Get(k)
		out.Set(k, v)
	}
	return out
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
func validateBrokered(config *jsonx.OrderedMap, workspace string, errs, warns *[]string) {
	if _, present := config.Get(brokeredKey); !present {
		// Every scope's keys survive into the merged map, so an absent key proves no scope
		// wrote one.
		return
	}
	validateBrokeredRetired(config, workspace, errs, warns)
	validateBrokeredUserScope(errs, warns)
	validateBrokeredWorkspace(workspace, errs)
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
				if r, ok := e.(string); ok && brokerscope.ValidRepo(r) {
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
// workspace. Other shapes are refused as they are at workspace scope; `workspaces` is the retired
// check's. In a jail the user scope is the host-generated one, and each refusal is a warning.
func validateBrokeredUserScope(errs, warns *[]string) {
	v, present := UserScopeConfigOrEmpty().Get(brokeredKey)
	if !present || v == nil {
		return
	}
	refuse := func(msg string) {
		msg = UserScopeSources().AnnotateOne(msg)
		if inJail() {
			add(warns, msg+inJailSuffix)
			return
		}
		add(errs, msg)
	}
	sources, ok := asMap(v)
	if !ok {
		refuse("config." + brokeredKey + ": expected an object of source name -> {\"repos\": " +
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
			refuse(path + ": expected an object holding \"repos\" (got " + pyReprValue(sv) + ")")
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
				"remove this key from " + paths.UserConfigPath() + ".")
		}
	}
}

// validateBrokeredWorkspace checks the workspace's own `brokered`, read from its files with
// their provenance: the shape of the value in effect, located at the file and line each problem
// was written at; any file outside the workspace that wrote it (WW-D17); and a key written twice
// in one file (WW-D24). Errors in a jail too: there the files are the agent's own, and in-jail
// `yolo check --no-build` is how it checks its edit.
func validateBrokeredWorkspace(workspace string, errs *[]string) {
	wsCfg, src, err := LoadWorkspaceConfigWithSources(workspace, false, func(string) {})
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
		rv, _ := src.Get(brokeredReposKey)
		if rv == nil {
			continue
		}
		list, ok := rv.([]any)
		if !ok {
			out = append(out, srcPath+"."+brokeredReposKey+": expected a list of OWNER/REPO strings (got "+
				pyReprValue(rv)+")")
			continue
		}
		for i, e := range list {
			if r, ok := e.(string); !ok || !brokerscope.ValidRepo(r) {
				out = append(out, srcPath+"."+brokeredReposKey+"["+itoa(i)+"]: "+pyReprValue(e)+
					" is not OWNER/REPO: two names of letters, digits, `_`, `.` and `-`, with one `/` "+
					"between them, and no host")
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
