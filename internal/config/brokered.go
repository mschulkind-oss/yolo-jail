package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// brokered.go is the `brokered` key (docs/design/boundary-broker.md §5.6, OQ-BB6, BB-D33): what
// the host user lets a brokered loophole's daemon reach beyond what a launch reads for itself.
// A brokered loophole is one whose manifest declares a `brokered` block (loopholedecl), and its
// `source` names the key below. Today the key carries one thing per source, the WIDENING ENTRY,
// a term that design coined (§1.3) for a user-scope entry, keyed by one workspace's host path,
// that adds repositories to that workspace's repository scope alone:
//
//	"brokered": {
//	  "github": {
//	    "workspaces": {
//	      "~/code/app": { "repos": ["org/lib"] }
//	    }
//	  }
//	}
//
// # What an entry admits
//
// Whole repositories, which join the scope for every permission set exactly as a repository
// read from the workspace's remotes does (OQ-BB9, ruled A). Nothing else: no entry admits a
// command that names no repository, and no entry limits a repository to some sets.
//
// # Why it is keyed by the workspace's host path
//
// BB-D33: the remotes are re-read at every fresh launch from a file the agent edits, so an entry
// keyed by a remote would let the agent choose which entry applies to it. The key is the host
// path, `~/` expanded and symlinks resolved, matched against the workspace the launch resolved
// the same way, which is the path its container name is derived from (runtime.FromWorkspace). A
// key whose path runs through the workspace's own folder never matches it (brokeredKeyNames).
//
// # Why it is read from the USER config directly
//
// A workspace may never widen its own reach (BB-P9; OQ-BB1's ruling, "we can't allow it to be
// widened in the workspace"), and a workspace config is agent-editable. So the reader takes the
// user scope alone, `host_path`'s construction, and a workspace spelling is a validation error
// that is never read. Being user config, an entry is not in the config-change diff (user config
// never prompts, OQ-S1 in docs/reference/config-safety.md); the launch discloses it instead.
//
// # Where it takes effect
//
// At a fresh launch that starts the broker, which writes the entry's repositories into the
// launch's scope file as `widened` (brokerscope.File); an attach reads no config and writes
// nothing, so an edit lands at the next fresh launch.
const (
	brokeredKey           = "brokered"
	brokeredWorkspacesKey = "workspaces"
	brokeredReposKey      = "repos"
)

var (
	knownBrokeredSourceKeys = set(brokeredWorkspacesKey)
	knownBrokeredEntryKeys  = set(brokeredReposKey)
)

// BrokeredWidening is what the user config's widening entries add to one source's scope for a
// workspace: every repository an entry lists whose key resolves to that workspace, each once,
// sorted, with every entry and repository validation refuses left out. Empty when there is none.
func BrokeredWidening(source, workspace string) []string {
	return brokeredWidening(UserScopeConfigOrEmpty(), source, workspace)
}

// brokeredWidening is the reading, split from the user-scope load so the validator and the tests
// exercise one implementation.
func brokeredWidening(cfg *jsonx.OrderedMap, source, workspace string) []string {
	if cfg == nil || source == "" || workspace == "" {
		return nil
	}
	v, _ := cfg.Get(brokeredKey)
	sources, ok := asMap(v)
	if !ok {
		return nil
	}
	sv, _ := sources.Get(source)
	src, ok := asMap(sv)
	if !ok {
		return nil
	}
	wv, _ := src.Get(brokeredWorkspacesKey)
	workspaces, ok := asMap(wv)
	if !ok {
		return nil
	}
	want := expandAndResolve(workspace)
	seen := map[string]bool{}
	var out []string
	for _, key := range workspaces.Keys() {
		if brokeredWorkspaceKeyProblem(key) != "" || !brokeredKeyNames(key, want) {
			continue
		}
		ev, _ := workspaces.Get(key)
		entry, ok := asMap(ev)
		if !ok {
			continue
		}
		rv, _ := entry.Get(brokeredReposKey)
		list, _ := rv.([]any)
		for _, e := range list {
			r, ok := e.(string)
			if !ok || !brokerscope.ValidRepo(r) || seen[strings.ToLower(r)] {
				continue
			}
			seen[strings.ToLower(r)] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// brokeredKeyNames reports whether one accepted `workspaces` key names the resolved workspace
// want: the key, `~/` expanded and symlinks resolved, is want, and resolving it never passes
// through a folder or a link inside want.
//
// The second half is BB-P9, a workspace never widening its own reach, applied to symlinks. What
// lies inside the workspace is the agent's to change, so a key whose resolution passes through it
// resolves wherever the agent points it: without the check, an entry for `~/code/app/sub` would
// widen `~/code/app` once that jail's agent replaced `sub` with a link to `.`, and so would an
// entry keyed by a link of the user's own that reaches `sub`. A link the user made outside the
// workspace, pointing at the workspace itself, still matches, since nothing inside it is read.
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

// brokeredWorkspaceKeyProblem is why one `workspaces` key is refused, "" when it is accepted: an
// absolute path, or one starting with `~/`.
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

// brokeredProblems is every shape problem in a `brokered` value, each a full message.
func brokeredProblems(v any) []string {
	path := "config." + brokeredKey
	sources, ok := asMap(v)
	if !ok {
		return []string{path + `: expected an object of source name -> {"workspaces": {...}} (got ` +
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
			out = append(out, srcPath+`: expected an object holding "workspaces" (got `+pyReprValue(sv)+")")
			continue
		}
		reportUnknownKeysTo(&out, src, knownBrokeredSourceKeys, srcPath)
		wv, _ := src.Get(brokeredWorkspacesKey)
		if wv == nil {
			continue
		}
		wsPath := srcPath + "." + brokeredWorkspacesKey
		workspaces, ok := asMap(wv)
		if !ok {
			out = append(out, wsPath+`: expected an object of workspace path -> {"repos": [...]} (got `+
				pyReprValue(wv)+")")
			continue
		}
		for _, key := range workspaces.Keys() {
			entryPath := wsPath + "." + key
			if prob := brokeredWorkspaceKeyProblem(key); prob != "" {
				out = append(out, entryPath+": "+prob)
			}
			ev, _ := workspaces.Get(key)
			if ev == nil {
				continue
			}
			entry, ok := asMap(ev)
			if !ok {
				out = append(out, entryPath+`: expected an object holding "repos" (got `+pyReprValue(ev)+")")
				continue
			}
			reportUnknownKeysTo(&out, entry, knownBrokeredEntryKeys, entryPath)
			rv, _ := entry.Get(brokeredReposKey)
			if rv == nil {
				continue
			}
			list, ok := rv.([]any)
			if !ok {
				out = append(out, entryPath+"."+brokeredReposKey+": expected a list of OWNER/REPO strings (got "+
					pyReprValue(rv)+")")
				continue
			}
			for i, e := range list {
				if r, ok := e.(string); !ok || !brokerscope.ValidRepo(r) {
					out = append(out, entryPath+"."+brokeredReposKey+"["+itoa(i)+"]: "+pyReprValue(e)+
						" is not OWNER/REPO: two names of letters, digits, `_`, `.` and `-`, with one `/` "+
						"between them, and no host")
				}
			}
		}
	}
	return out
}

// validateBrokered checks the `brokered` key: its shape, its scope, and that each source names a
// brokered loophole a selected pack ships.
//
// User scope only (BB-P9): an entry widens what a daemon holding the host's credential reaches,
// and a workspace config is agent-editable, so a workspace spelling is refused, naming the user
// config, rather than left looking as if it worked (BrokeredWidening never reads it).
//
// A source no selected pack's loophole brokers is a warning, not an error: the entry is inert
// there, and one user config serves machines that select different packs, as a `loopholes` entry
// for a loophole no selected pack ships is. The resolver is asked only when the key is present,
// as validateBrokerMountFence asks only when there is a mount, because its first answer is
// memoized for the process.
func validateBrokered(config *jsonx.OrderedMap, workspace string, resolver LoopholeResolver, errs, warns *[]string) {
	v, present := config.Get(brokeredKey)
	if !present {
		return
	}
	if wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {}); err == nil && wsCfg != nil {
		if wsValue, atWorkspace := wsCfg.Get(brokeredKey); atWorkspace && wsValue != nil {
			add(errs, "config."+brokeredKey+": user-scope only — it widens what a brokered loophole "+
				"reaches with the host's credential, and a workspace config is agent-editable, so a "+
				"workspace may never widen its own reach and a workspace value is never read. Move it to "+
				paths.UserConfigPath()+", keyed by this workspace's path, or remove it.")
		}
	}
	if v == nil {
		return
	}
	for _, prob := range brokeredProblems(v) {
		add(errs, prob)
	}
	sources, ok := asMap(v)
	if !ok || resolver == nil || inJail() {
		return
	}
	known, _ := resolver.Known()
	brokers := map[string]bool{}
	for _, info := range known {
		if info.Brokered != nil {
			brokers[info.Brokered.Source] = true
		}
	}
	for _, name := range sources.Keys() {
		if sv, _ := sources.Get(name); sv == nil || brokers[name] {
			continue
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
		add(warns, "config."+brokeredKey+"."+name+": no loophole a selected pack ships brokers the source "+
			pyReprValue(name)+", so this entry has no effect (brokered sources here: "+names+")")
	}
}
