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
// user exported by hand in a jail shell is never unset by it.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// AgentEnvDirRel is the per-agent env directory, relative to the jail home. podman binds it
// `:ro` from <workspace>/.yolo/home/agent-env; Apple Container, which binds the workspace's
// home state itself, has the host launcher write it in place on every entry. The wire bridge
// reads a served agent's key out of it.
const AgentEnvDirRel = ".config/yolo-agent-env"

// AgentEnvFilesEnv is the LEGACY spelling of the `agent-env-files` contract tag
// (ContractTagsEnv). The credential gate's first build froze it as "1" into every container it
// launched, the first named contract marker in practice (provider-credential-scope.md, CN-D18),
// and the tag set folded it in (attach-skew-and-contract-guardrails.md, OQ-SK2). No launch
// writes it any more. It is still READ, by internal/cli/run's jailContractTags, for a container
// that carries no ContractTagsEnv: a jail launched between the gate and the tags must keep
// counting as one whose launchers source the per-agent files.
const AgentEnvFilesEnv = "YOLO_AGENT_ENV_FILES"

// AgentEnvFile is the env file of one agent (its CLI name) under home.
func AgentEnvFile(home, agent string) string {
	return filepath.Join(home, AgentEnvDirRel, agent+".sh")
}

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
// $HOME. A missing file is the ordinary case — an agent this launch scoped nothing to — and
// sources nothing.
const agentEnvShellFn = `# --- this agent's own environment (provider-credential-scope.md, OQ-CN6) ----------------
# The credentials and profile-gated variables a launch scoped to THIS agent alone, written
# per entry by yolo from its one credential gate. Absent when the launch scoped nothing here.
if [ -r "$HOME/` + AgentEnvDirRel + `/$BIN.sh" ]; then
    . "$HOME/` + AgentEnvDirRel + `/$BIN.sh"
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
	for _, l := range readAgentEnvFile(e.Home, agent) {
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
	data, err := os.ReadFile(AgentEnvFile(home, agent))
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
