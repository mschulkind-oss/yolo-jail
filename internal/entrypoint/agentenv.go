package entrypoint

// agentenv.go is the JAIL HALF of the per-agent env file (docs/reference/providers.md,
// OQ-CN6): where the file sits in the jail home, and the launcher fragment that sources it.
//
// WHY A FILE PER AGENT. Every other channel ends in ~/.config/yolo-user-env.sh, whose first
// reader exports it into the entrypoint's process environment — so whatever it carries is in
// every process the jail runs (§2.7). A value the credential gate scopes to one agent therefore
// leaves that file entirely and lands here, in <dir>/<agent>.sh, written per entry by the host
// launcher from the one gate (internal/cli/run's writeAgentEnvFiles) and read by exactly one
// thing: that agent's launcher, immediately before it hands over to the program.
//
// WHAT IT DOES NOT BUY. The file is readable by every process of the jail's uid, as the shared
// file is; the gate governs what each agent's PROCESS ENVIRONMENT carries, not what the uid can
// open (the doc's non-goal "protecting an agent from itself"). And it only ADDS: an agent
// started by another agent inherits that agent's environment, as any child does, and a value a
// user exported by hand in a jail shell is never unset by it — EXCEPT the agent's provider
// variables, which the provider-keys file sourced after it (ProviderKeysFile) makes exactly what
// the agent's own profile selection says (PP-D14, 2026-10-11).

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/claudesecurity"
)

// AgentEnvDirRel is the per-agent env directory, relative to the jail home. podman binds it
// `:ro` from <workspace>/.yolo/home/agent-env; Apple Container, which binds the workspace's
// home state itself, has the host launcher write it in place on every entry. The wire bridge
// reads a served agent's key out of it.
const AgentEnvDirRel = ".config/yolo-agent-env"

// AgentEnvFilesEnv is the LEGACY spelling of the `agent-env-files` contract tag
// (ContractTagsEnv). The credential gate's first build froze it as "1" into every container it
// launched, the first named contract marker in practice (providers.md, CN-D18),
// and the tag set folded it in (attach-skew.md, OQ-SK2). No launch
// writes it any more. It is still READ, by internal/cli/run's jailContractTags, for a container
// that carries no ContractTagsEnv: a jail launched between the gate and the tags must keep
// counting as one whose launchers source the per-agent files.
const AgentEnvFilesEnv = "YOLO_AGENT_ENV_FILES"

// AgentEnvFile is the env file of one agent (its CLI name) under home.
func AgentEnvFile(home, agent string) string {
	return filepath.Join(home, AgentEnvDirRel, agent+".sh")
}

// ProviderKeysSubdir is the directory, inside the per-agent env directory, holding each agent's
// PROVIDER-KEYS FILE (internal/cli/run's providerEnvFileContent): its provider variables made
// exactly what its profile selection says (docs/design/providers-and-profiles-redesign.md
// PP-D14). A directory of its own, so no reader of the env files (agentsWithEnvFiles, the wire
// bridge, the macos-user MCP view) mistakes one for an agent's env file.
const ProviderKeysSubdir = "provider-keys"

// ProviderKeysFile is agent's provider-keys file under home.
func ProviderKeysFile(home, agent string) string {
	return filepath.Join(home, AgentEnvDirRel, ProviderKeysSubdir, agent+".sh")
}

// SessionSnapshotsSubdir is the directory, inside the per-agent env directory, holding one ENTRY
// SNAPSHOT per session id (docs/design/per-session-profiles.md §4.3, SP-D12): the host launcher
// writes the entry's shared yolo-user-env.sh, its <agent>.sh, <agent>.<name> and
// provider-keys/<agent>.sh files there, complete, before that entry's exec, and never rewrites
// them. A session reads its own snapshot instead of the shared files the NEXT entry rewrites,
// which is what keeps one session's profile from reaching another session's processes. A
// directory, so no reader of the env files (agentsWithEnvFiles, the wire bridge) mistakes it for
// an agent's file.
const SessionSnapshotsSubdir = "sessions"

// SessionSnapshotDirRel is session id's entry snapshot, relative to the jail home. The host
// writes it at the same path beneath its bind source (internal/cli/run's writeSessionSnapshot);
// the layout is the contract between the two halves.
func SessionSnapshotDirRel(id string) string {
	return AgentEnvDirRel + "/" + SessionSnapshotsSubdir + "/" + id
}

// ValidSessionID reports whether id has the shape the entrypoint accepts as a session's name
// (lowercase hex), which is also what makes it safe as a snapshot's directory name.
func ValidSessionID(id string) bool { return validSessionID(id) }

// sessionSnapshotDir is the snapshot of the session e runs as, absolute, when e names a valid
// session id and the host published a snapshot for it; "" otherwise — no id (the jail's main
// process, a raw exec) or no snapshot (a launcher older than snapshots) — and the reader then
// falls back to the shared files, as before (per-session-profiles.md §4.3 step 3).
func sessionSnapshotDir(e *Env) string {
	id, _ := e.Lookup(SessionIDEnv)
	if !validSessionID(id) {
		return ""
	}
	dir := filepath.Join(e.Home, SessionSnapshotDirRel(id))
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return ""
	}
	return dir
}

// userEnvFilePath is the shared-env file (yolo-user-env.sh) the session e runs as reads: its
// entry snapshot's copy when it has one (sessionSnapshotDir), else the shared
// ~/.config/yolo-user-env.sh the last entry wrote. The boot's hydrate and the session's shell
// prefix read through it. The jail's long-lived daemons (the wire bridge's HydrateEntryChannel,
// ScopedCallerToken) do not: they follow the LATEST entry by design, and a snapshot is one
// entry's, frozen.
func userEnvFilePath(e *Env) string {
	if dir := sessionSnapshotDir(e); dir != "" {
		return filepath.Join(dir, "yolo-user-env.sh")
	}
	return filepath.Join(e.Home, ".config", "yolo-user-env.sh")
}

// sessionSnapshotShellFn picks, in shell, the directory this process's per-entry files are read
// from into _yolo_entry_dir: the session's snapshot when YOLO_SESSION_ID names one (the test
// sessionSnapshotDir makes in Go: an id of lowercase hex, a directory present), else the shared
// per-agent env directory. The whole directory, never file by file: a snapshot with no file for
// an agent means its entry scoped nothing to that agent, and the shared file beside it is
// whichever entry wrote last. The caller unsets the variable.
const sessionSnapshotShellFn = `_yolo_entry_dir="$HOME/` + AgentEnvDirRel + `"
case "${` + SessionIDEnv + `-}" in
    ''|*[!0-9a-f]*) ;;
    *) if [ -d "$_yolo_entry_dir/` + SessionSnapshotsSubdir + `/$` + SessionIDEnv + `" ]; then
           _yolo_entry_dir="$_yolo_entry_dir/` + SessionSnapshotsSubdir + `/$` + SessionIDEnv + `"
       fi ;;
esac
`

// sessionHomeBindShell exports, from the session's SessionHomesEnv, each variable a line names
// for THIS launcher's program, pointing at that program's own session home
// (per-session-profiles.md SP-D36): two packs may declare one variable, and the session-wide
// value is only the last declaration's. Ahead of the agent's own env files, so a value a launch
// scoped to this agent still wins, as a user's own value does at the session pass (SP-D5). A
// session with no homes, or none for this program, changes nothing. A here-string and IFS read,
// both in bash 3.2; the subshell a pipe would make would lose the exports.
const sessionHomeBindShell = `if [ -n "${` + SessionHomesEnv + `:-}" ]; then
    while IFS="$(printf '\t')" read -r _yolo_sh_bin _yolo_sh_var _yolo_sh_dir; do
        [ "$_yolo_sh_bin" = "$BIN" ] || continue
        case "$_yolo_sh_var" in ''|[!A-Za-z_]*|*[!A-Za-z0-9_]*) continue ;; esac
        [ -n "$_yolo_sh_dir" ] && export "$_yolo_sh_var=$_yolo_sh_dir"
    done <<< "$` + SessionHomesEnv + `"
    unset _yolo_sh_bin _yolo_sh_var _yolo_sh_dir
fi
`

// agentEnvShellFn is spliced into the npm and native agent launchers immediately ahead of the
// pre-launch authentication step and the exec, and into the wrapper ahead of its exec. Ahead
// of the authentication step because that step reads its own switches from the environment,
// and a per-agent one (pi's YOLO_AUTH_PRELAUNCH_PI_FLAG, which pi's env derive composes when its
// provider is openai-codex) is exactly the kind of value that now lives here rather than in the
// shared file. AFTER everything else the
// launchers run — the install, the update, the MCP server refresh (`yolo internal
// refresh-servers`, npm installs whose lifecycle scripts run) and the pre-launch refresh (pi's
// `update --extensions`) — because none of those needs a credential and none should run
// holding one.
//
// $HOME, not a baked path: the path is the same fact on every backend (a jail home's
// .config/yolo-agent-env), and the templates already name their install prefixes through
// $HOME. The directory is the session's entry snapshot when it has one
// (sessionSnapshotShellFn). A missing file is the ordinary case — an agent this launch scoped nothing to — and
// sources nothing.
const agentEnvShellFn = `# --- this agent's own environment (providers.md, OQ-CN6) ----------------
# The credentials and profile-gated variables a launch scoped to THIS agent alone, written
# per entry by yolo from its one credential gate. Absent when the launch scoped nothing here.
# Read from this session's entry snapshot when it has one (per-session-profiles.md), so a
# later attach's profile never reaches this session's agents.
` + sessionHomeBindShell + sessionSnapshotShellFn + `if [ -r "$_yolo_entry_dir/$BIN.sh" ]; then
    . "$_yolo_entry_dir/$BIN.sh"
fi
# Its PROVIDER variables, last, exactly as its profile selection says whatever this process
# inherited (PP-D14): a nested agent follows the same auth path as a fresh one.
if [ -r "$_yolo_entry_dir/` + ProviderKeysSubdir + `/$BIN.sh" ]; then
    . "$_yolo_entry_dir/` + ProviderKeysSubdir + `/$BIN.sh"
fi
unset _yolo_entry_dir
# A selected Claude credential hook materializes the adapter under its program name. Only that
# program's launcher adds the directory; other agents and interactive shells keep their PATH.
if [ "${` + claudesecurity.EnvVar + `:-}" = "account" ] &&
    [ -x "$HOME/` + claudeSecurityAdapterDir + `/$BIN/security" ]; then
    PATH="$HOME/` + claudeSecurityAdapterDir + `/$BIN${PATH:+:$PATH}"
    export PATH
fi
`

// agentsWithEnvFiles lists, sorted, the agents this entry wrote an env file for — the names
// in AgentEnvDirRel whose file is <agent>.sh.
func agentsWithEnvFiles(e *Env) []string {
	entries, err := os.ReadDir(filepath.Join(e.Home, AgentEnvDirRel))
	if err != nil {
		return nil
	}
	var out []string
	for _, ent := range entries {
		if name, ok := strings.CutSuffix(ent.Name(), ".sh"); ok && name != "" && !ent.IsDir() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// agentEnvLookup is e.Lookup as agent's own launcher will see it: the boot's environment
// with agent's env file applied over it, line by line in the file's own grammar
// (agentEnvLine). A def-form line (`export K=${K:-'v'}`) sets K only when K is unset or
// empty at that point, a plain-form line sets it, `unset K` removes it, and the writer's
// `case` lines set or remove K only when K's value at that point is one the line lists. A
// boot-time answer to "will this agent's process hold K?", for the gates that decide what its
// config names (loadMCPTables). A file it cannot read changes nothing.
func agentEnvLookup(e *Env, agent string) func(string) (string, bool) {
	own := map[string]string{}
	unset := map[string]bool{}
	current := func(key string) (string, bool) {
		if v, ok := own[key]; ok {
			return v, true
		}
		if unset[key] {
			return "", false
		}
		return e.Lookup(key)
	}
	// The provider-keys file is sourced after the env file (agentEnvShellFn), so its plain
	// exports and unsets apply last; its value-conditional lines are bash tests this grammar
	// does not read, and change nothing here.
	// A session with an entry snapshot reads ITS files (sessionSnapshotDir), the ones its launcher
	// sources, rather than the shared ones the next entry rewrites (per-session-profiles.md §4.3).
	envFile, keysFile := AgentEnvFile(e.Home, agent), ProviderKeysFile(e.Home, agent)
	if dir := sessionSnapshotDir(e); dir != "" {
		envFile = filepath.Join(dir, agent+".sh")
		keysFile = filepath.Join(dir, ProviderKeysSubdir, agent+".sh")
	}
	lines := append(readEnvLines(envFile), readEnvLines(keysFile)...)
	for _, l := range lines {
		if v, _ := current(l.key); !l.appliesTo(v) {
			continue
		}
		if l.unset {
			delete(own, l.key)
			unset[l.key] = true
			continue
		}
		own[l.key] = l.value
		delete(unset, l.key)
	}
	return current
}

// agentEnvLine is one line of a per-agent env file, in the grammar of its writer
// (internal/cli/run's agentEnvFileContent), which writes three shapes:
//
//	export K=${K:-'v'}
//	case "${K-}" in ''|'a'|'b') export K='v' ;; esac
//	case "${K-}" in 'a'|'b') unset K ;; esac
//
// The first, the def form, is an env_sources value or a composed value whose name yolo set
// nowhere else this entry, and it sets K only when K is unset or empty. The second is a
// composed value whose name yolo DID set elsewhere this entry: it overrides only an empty K or
// one of the values yolo set there (the line's guard), so a value the user set is kept. The
// third is a profile's removal of K, applied only when K holds one of those values.
//
// A plain `export K='v'` and a bare `unset K` are read too: the writer writes neither, but a hand
// edit may. Where the line is a `case`, cased is set and guard holds its patterns, the empty
// pattern as "".
type agentEnvLine struct {
	key   string
	value string
	unset bool
	def   bool
	cased bool
	guard []string
}

// appliesTo reports whether the line acts on a K whose value at that point is cur ("" for an
// unset K, as `${K-}` and `${K:-}` read it).
func (l agentEnvLine) appliesTo(cur string) bool {
	switch {
	case l.cased:
		return slices.Contains(l.guard, cur)
	case l.def:
		return cur == ""
	}
	return true
}

// agentEnvCaseRe is the writer's `case` line, both actions. RE2 has no backreference, so the
// action's name is captured separately and parseAgentEnvLine checks it is the guard's.
var agentEnvCaseRe = regexp.MustCompile(`^\s*case "\$\{(?P<key>[A-Za-z_][A-Za-z0-9_]*)-\}" in ` +
	`(?P<pats>` + agentEnvQuoted + `(?:\|` + agentEnvQuoted + `)*)\) ` +
	`(?:export (?P<setkey>[A-Za-z_][A-Za-z0-9_]*)='(?P<val>(?:[^']|'\\'')*)'|unset (?P<unsetkey>[A-Za-z_][A-Za-z0-9_]*))` +
	` ;; esac\s*$`)

// agentEnvQuoted is one single-quoted word with the writer's escape for an embedded single
// quote (a single quote, a backslash, and two single quotes); agentEnvPatternRe takes one apart.
const agentEnvQuoted = `'(?:[^']|'\\'')*'`

var agentEnvPatternRe = regexp.MustCompile(`'((?:[^']|'\\'')*)'`)

// parseAgentEnvLine reads one line of a per-agent env file (agentEnvLine's grammar), and
// reports false for a comment, a blank line or anything else.
func parseAgentEnvLine(line string) (agentEnvLine, bool) {
	trimmed := strings.TrimSpace(line)
	if key, ok := strings.CutPrefix(trimmed, "unset "); ok {
		return agentEnvLine{key: strings.TrimSpace(key), unset: true}, true
	}
	if key, val, def, ok := parseExportLine(line); ok {
		return agentEnvLine{key: key, value: val, def: def}, true
	}
	m := agentEnvCaseRe.FindStringSubmatch(line)
	if m == nil {
		return agentEnvLine{}, false
	}
	group := func(name string) string { return m[agentEnvCaseRe.SubexpIndex(name)] }
	l := agentEnvLine{key: group("key"), cased: true}
	switch {
	case group("setkey") == l.key:
		l.value = unescapeSingleQuoted(group("val"))
	case group("unsetkey") == l.key:
		l.unset = true
	default:
		return agentEnvLine{}, false // a case on one name acting on another: not the writer's
	}
	for _, p := range agentEnvPatternRe.FindAllStringSubmatch(group("pats"), -1) {
		l.guard = append(l.guard, unescapeSingleQuoted(p[1]))
	}
	return l, true
}

// unescapeSingleQuoted reverses the writer's escape for a single quote inside a single-quoted
// word.
func unescapeSingleQuoted(s string) string { return strings.ReplaceAll(s, "'\\''", "'") }

// readAgentEnvFile is agent's env file under home, parsed (parseAgentEnvLine); nil when it
// cannot be read.
func readAgentEnvFile(home, agent string) []agentEnvLine {
	return readEnvLines(AgentEnvFile(home, agent))
}

// readEnvLines is the file at path parsed in the env file's grammar (parseAgentEnvLine); nil
// when it cannot be read.
func readEnvLines(path string) []agentEnvLine {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []agentEnvLine
	for _, line := range splitLines(string(data)) {
		if l, ok := parseAgentEnvLine(line); ok {
			out = append(out, l)
		}
	}
	return out
}
