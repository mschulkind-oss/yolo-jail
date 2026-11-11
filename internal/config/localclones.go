package config

// localclones.go gathers the LOCAL CLONE MAP (internal/packsrc/localclones.go; docs/reference/patched-forks.md
// §3.5, PF-D100) from the USER config, and owns the `local_clones` key.
//
//   - IMPLICIT REGISTRATION, the main path: every path the user config reads a pack from that lands inside a git
//     clone registers that clone — a pack named by path, one named by git+file://, and the conventional local
//     pack. The maintainer (2026-10-24): "if you have a path into any local clone of a repository, that adds that
//     repository to the mapping, and because of the newest wins, this is safe even for other packs." A pack that
//     is plain files in no repository registers nothing, and that is no error.
//   - EXPLICIT REGISTRATION, `local_clones`: a list of absolute paths, `~` expanded, for a clone that holds no
//     pack (a fork of an upstream tool). "We can implicitly register local clones by using a path from them, but
//     we can just also have a user level config to register them regardless." USER SCOPE ONLY: a workspace config
//     must not point yolo at host paths. An entry that is not absolute, does not exist, is in no git clone, or
//     has no remote is a FATAL config error at every verb that resolves patch branches (the maintainer,
//     2026-10-24: "It's in your user config. You don't want to silently not have this applied. That leads to
//     hair pulling."): LocalClones returns it, so the check refuses, and `yolo check`, the launch, `yolo host`
//     and the `yolo pack` verbs refuse up front with the same text (LocalClonesError).
//
// Gathering reads only: `git rev-parse --show-toplevel` and the clone's remote URLs from its configuration, with
// the caller's GIT_DIR and its kin cleaned away (packsrc.CleanGitEnv).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// localClonesKey is the user-scope list of clones registered explicitly.
const localClonesKey = "local_clones"

func init() {
	// The map is asked for once per patched check that names a patch branch URL, from the user config as it
	// stands then.
	packsrc.SetLocalCloneSource(LocalClones)
}

// LocalClonesError is a `local_clones` value in the user config that registers nothing: each problem names the
// entry and the next step. Every verb that resolves patch branches refuses with it.
type LocalClonesError struct{ Problems []string }

func (e *LocalClonesError) Error() string {
	return strings.Join(e.Problems, "\n")
}

// LocalClonesProblem is the user config's `local_clones` refusal, nil when the key is absent or every entry
// registers a clone.
func LocalClonesProblem() error {
	if problems := localClonesProblems(UserScopeConfigOrEmpty(), true, true); len(problems) > 0 {
		return &LocalClonesError{Problems: problems}
	}
	return nil
}

// LocalClones is the local clone map: the `local_clones` entries first, in their order, then every clone a
// configured pack's path lands in, in pack order (the conventional local pack last). A clone registered twice
// keeps its first, explicit-first, registration. A broken `local_clones` value is a LocalClonesError, and no map.
func LocalClones() ([]packsrc.LocalClone, error) {
	user := UserScopeConfigOrEmpty()
	if problems := localClonesProblems(user, true, true); len(problems) > 0 {
		return nil, &LocalClonesError{Problems: problems}
	}
	var out []packsrc.LocalClone
	seen := map[string]bool{}
	add := func(path, from string, explicit bool) {
		top, remotes, ok := gatherClone(path)
		if !ok || seen[top] || len(remotes) == 0 {
			return
		}
		seen[top] = true
		out = append(out, packsrc.LocalClone{Dir: top, Explicit: explicit, From: from, Remotes: remotes})
	}
	for _, e := range localCloneEntries(user) {
		add(expandUser(e), e, true)
	}
	entries, _, err := LoadPackEntries()
	if err != nil {
		return out, nil // the `packs` refusal is the selection's to say; no pack path registers anything
	}
	for _, e := range entries {
		if e.Embedded() {
			continue
		}
		if dir := packEntryPath(e.Source); dir != "" {
			add(dir, e.Name, false)
		}
	}
	return out, nil
}

// packEntryPath is the directory on this machine a pack source is read from: a file:// pack's path, a
// git+file:// pack's repository; "" for a URL.
func packEntryPath(source string) string {
	a, err := packsrc.Parse(source)
	if err != nil {
		return ""
	}
	if a.IsLocal() {
		return a.Path
	}
	if dir, ok := strings.CutPrefix(a.Repo, "file://"); ok {
		return dir
	}
	return ""
}

// localCloneEntries is the key's string entries, as written.
func localCloneEntries(cfg *jsonx.OrderedMap) []string {
	v, present := cfg.Get(localClonesKey)
	list, ok := v.([]any)
	if !present || !ok {
		return nil
	}
	var out []string
	for _, item := range list {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// localClonesProblems is every reason cfg's `local_clones` value registers less than it says: its shape, and each
// entry that is not absolute, does not exist, is in no clone, or whose clone has no remote. Each names the next
// step.
func localClonesProblems(cfg *jsonx.OrderedMap, inspectHostPaths, inspectGit bool) []string {
	v, present := cfg.Get(localClonesKey)
	if !present || v == nil {
		return nil
	}
	where := paths.UserConfigDisplayPath()
	list, ok := v.([]any)
	if !ok {
		return []string{"config." + localClonesKey + ": expected a list of paths (got " + pyReprValue(v) +
			") — write it as [\"~/src/repo\"], or remove it from " + where + "."}
	}
	var out []string
	for _, item := range list {
		s, ok := item.(string)
		if !ok || strings.TrimSpace(s) == "" {
			out = append(out, "config."+localClonesKey+": expected a list of paths (got the entry "+pyReprValue(item)+
				") — write each entry as a path, or remove it from "+where+".")
			continue
		}
		s = strings.TrimSpace(s)
		path := expandUser(s)
		if !filepath.IsAbs(path) {
			out = append(out, "config."+localClonesKey+": the entry "+pyReprValue(s)+" is not an absolute path (a relative one would name a different directory from each place yolo runs), so write it from / or ~/ — fix the path, clone the repository there, or remove the entry from "+where+".")
			continue
		}
		if !inspectHostPaths {
			continue
		}
		if why := localClonePathProblem(path); why != "" {
			out = append(out, "config."+localClonesKey+": the entry "+pyReprValue(s)+" "+why+" — fix the path, clone the repository there, or remove the entry from "+where+".")
			continue
		}
		if inspectGit {
			if why := localCloneRepositoryProblem(path); why != "" {
				out = append(out, "config."+localClonesKey+": the entry "+pyReprValue(s)+" "+why+" — fix the path, clone the repository there, or remove the entry from "+where+".")
			}
		}
	}
	return out
}

// gatherClone is the clone path lands in, its top level and its remotes; ok false when path is in no clone.
func gatherClone(path string) (string, []packsrc.CloneRemote, bool) {
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return "", nil, false
	}
	out, err := gitRead(path, "rev-parse", "--show-toplevel")
	top := strings.TrimSpace(out)
	if err != nil || top == "" {
		return "", nil, false
	}
	top = filepath.Clean(top)
	// The remotes as the configuration writes them (no insteadOf rewriting): what the user named.
	out, err = gitRead(top, "config", "--get-regexp", `^remote\..*\.url$`)
	var remotes []packsrc.CloneRemote
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			key, url, ok := strings.Cut(strings.TrimSpace(line), " ")
			if !ok {
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
			remotes = append(remotes, packsrc.CloneRemote{Name: name, URL: strings.TrimSpace(url)})
		}
	}
	return top, remotes, true
}

// gitRead runs a read-only git in dir, stdout only, with no GIT_DIR or kin inherited from the caller, which would
// otherwise answer for that repository whatever dir is.
func gitRead(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return string(out), err
}

// validateLocalClones refuses a workspace value, and the user value's problems (localClonesProblems).
func validateLocalClones(config *jsonx.OrderedMap, workspace string, errs *[]string, skipHostPathFacts, skipGit bool, edit ...*editValidationContext) {
	if wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...); err == nil && wsCfg != nil {
		if wsValue, atWorkspace := wsCfg.Get(localClonesKey); atWorkspace && wsValue != nil && WorkspaceUserScopeOnlyKey(localClonesKey) {
			add(errs, "config."+localClonesKey+": user-scope only — it points yolo at clones on this machine, and a "+
				"workspace config must not point yolo at host paths. Move it to "+paths.UserConfigDisplayPath()+
				", or remove it from the workspace config.")
		}
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
	for _, p := range localClonesProblems(userConfig, !skipHostPathFacts, !skipGit) {
		add(errs, p)
	}
}

func localClonePathProblem(path string) string {
	if !filepath.IsAbs(path) {
		return "is not an absolute path (a relative one would name a different directory from each place yolo " +
			"runs), so write it from / or ~/"
	}
	fi, err := os.Stat(path)
	switch {
	case err != nil && os.IsNotExist(err):
		return "does not exist"
	case err != nil:
		return "cannot be read (" + err.Error() + ")"
	case !fi.IsDir():
		return "is not a directory"
	}
	return ""
}

func localCloneRepositoryProblem(path string) string {
	top, remotes, ok := gatherClone(path)
	if !ok {
		return "is not inside a git clone"
	}
	if len(remotes) == 0 {
		return "is in the git repository " + top + ", which has no remote, so no published URL can name it"
	}
	return ""
}
