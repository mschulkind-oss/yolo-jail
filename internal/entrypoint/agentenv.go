package entrypoint

// agentenv.go is the JAIL HALF of the per-agent env file (docs/design/provider-credential-scope.md,
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
	"sort"
	"strings"
)

// AgentEnvDirRel is the per-agent env directory, relative to the jail home. podman binds it
// `:ro` from <workspace>/.yolo/home/agent-env; Apple Container, which binds the workspace's
// home state itself, has the host launcher write it in place on every entry. The wire bridge
// reads a served agent's key out of it.
const AgentEnvDirRel = ".config/yolo-agent-env"

// AgentEnvFilesEnv marks a container whose jail reads the per-agent env files: the host
// launcher that created it sets it to "1" in the container's frozen environment, beside the
// directory's bind. Nothing in the jail reads it. It is for the NEXT host yolo that attaches:
// a container's frozen environment is the one fact about its launch an attach can inspect,
// and a jail an older yolo launched has neither the bind nor launchers that source the file,
// so its absence tells deliverChannelOnAttach that a per-agent delivery cannot reach that
// jail (provider-credential-scope.md, OQ-CN6).
const AgentEnvFilesEnv = "YOLO_AGENT_ENV_FILES"

// AgentEnvFile is the env file of one agent (its CLI name) under home.
func AgentEnvFile(home, agent string) string {
	return filepath.Join(home, AgentEnvDirRel, agent+".sh")
}

// agentEnvShellFn is spliced into the npm and native agent launchers immediately ahead of the
// pre-launch authentication step and the exec, and into the wrapper ahead of its exec. Ahead
// of the authentication step because that step reads its own switches from the environment,
// and a profile-gated one (pi's YOLO_AUTH_PRELAUNCH_PI_FLAG, gated on `codex`) is exactly the
// kind of value that now lives here rather than in the shared file. AFTER everything else the
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
// with agent's env file applied over it, in the file's own grammar — a def-form line
// (`export K=${K:-'v'}`) sets K only when the environment lacks it, a plain-form line sets
// it, and `unset K` removes it. A boot-time answer to "will this agent's process hold K?",
// for the gates that decide what its config names (loadMCPTables). A file it cannot read
// changes nothing.
func agentEnvLookup(e *Env, agent string) func(string) (string, bool) {
	own := map[string]string{}
	unset := map[string]bool{}
	if data, err := os.ReadFile(AgentEnvFile(e.Home, agent)); err == nil {
		for _, line := range splitLines(string(data)) {
			if key, ok := strings.CutPrefix(strings.TrimSpace(line), "unset "); ok {
				key = strings.TrimSpace(key)
				delete(own, key)
				unset[key] = true
				continue
			}
			loc := exportLineRe.FindStringSubmatchIndex(line)
			if loc == nil {
				continue
			}
			key := groupStr(line, loc, exportGroupKey)
			var raw string
			switch {
			case groupParticipated(loc, exportGroupDef):
				if v, ok := e.Vars[key]; ok && v != "" && !unset[key] {
					continue // a default the environment already beats
				}
				raw = groupStr(line, loc, exportGroupDef)
			case groupParticipated(loc, exportGroupSq):
				raw = groupStr(line, loc, exportGroupSq)
			case groupParticipated(loc, exportGroupDq):
				raw = groupStr(line, loc, exportGroupDq)
			default:
				raw = groupStr(line, loc, exportGroupBare)
			}
			own[key] = strings.ReplaceAll(raw, "'\\''", "'")
			delete(unset, key)
		}
	}
	return func(key string) (string, bool) {
		if v, ok := own[key]; ok {
			return v, true
		}
		if unset[key] {
			return "", false
		}
		return e.Lookup(key)
	}
}
