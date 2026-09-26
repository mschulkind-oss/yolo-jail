package run

// agentenvfiles.go is the CONTAINER VEHICLE's half of the credential gate
// (docs/design/provider-credential-scope.md, OQ-CN6): the per-agent env files, written from
// the one gate's answer (packChannel.scope) beside the shared yolo-user-env.sh.
//
// ONE WRITER FOR BOTH CROSSINGS. deliverChannel is what a fresh launch and an attach both
// call, so the shared file and the per-agent files are always written from ONE composition
// and cannot describe two different entries. The shared file gets only what every process
// may see — the unclaimed env_sources, the three wire tables and the ungated pack env — and
// each profiled agent's file gets what only it receives: its selected provider's claimed
// credentials, the gated env its own selection satisfies, and its pack's env derive's output.
//
// HOW IT REACHES THE JAIL. On podman the files live in <wsState>/agent-env/<agent>.sh, a
// directory the launch binds `:ro` at ~/.config/yolo-agent-env (assemble.go); a directory
// bind shows a rewrite to a running jail, so an attach delivers the same way the shared file
// does: write, then exec. Apple Container binds wsState ITSELF at /home/agent and ignores
// `:ro`, so there deliverChannel writes the files straight into <wsState>/.config/yolo-agent-env
// — the jail's own path, live through that bind — on the fresh launch and on every attach
// alike, owner-only like the podman source. The reader is the agent's launcher
// (entrypoint.agentEnvShellFn), and the wire bridge reads a served agent's key from it.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// agentEnvStateDir is the per-agent env directory beneath wsState, the bind source.
const agentEnvStateDir = "agent-env"

// agentEnvFileMode and agentEnvDirMode are owner-only, for userEnvFileMode's reason: the
// files hold provider credentials in plaintext.
const (
	agentEnvFileMode = 0o600
	agentEnvDirMode  = 0o700
)

// deliverChannel writes this entry's channel into both of its crossings under wsState: the
// shared yolo-user-env.sh and one env file per profiled agent. The fresh launch calls it
// after prepareWsState, and deliverChannelOnAttach calls it after its pre-flights.
//
// THE SHARED FILE GETS THE GATE'S SHARED HALF, never the hydration: a caller that handed it
// channel.userEnv would put every provider credential back in every process, which is the
// leak this whole design closes (TestDeliverChannelScopesCredentialsPerAgent is the pin).
//
// rt picks where the files land, because the two container backends reach the jail home
// differently. podman binds <wsState>/yolo-user-env.sh and <wsState>/agent-env into it;
// Apple Container binds wsState itself at /home/agent, so the jail reads
// <wsState>/.config/yolo-user-env.sh and <wsState>/.config/yolo-agent-env, and those are
// written HERE, on every entry. They used to be copied only by the fresh launch's argv
// assembly, so an attach rewrote files that jail never reads: a deselecting attach left the
// previous entry's credential in the agent's file, and the copy made the files 0644 in a
// 0755 directory on a host path under the workspace.
func deliverChannel(wsState, rt string, channel *packChannel) {
	shared := filepath.Join(wsState, "yolo-user-env.sh")
	writeUserEnvFile(shared, channel.scope.SharedEnvSources(), channel)
	if rt == "container" { // parity: HonoredBy — Apple Container binds wsState at /home/agent, so both files are written at their in-home paths beneath it, live on every entry, instead of bound
		acMaterialize(shared, ".config/yolo-user-env.sh", wsState)
		writeAgentEnvFiles(wsState, entrypoint.AgentEnvDirRel, channel)
		return
	}
	writeAgentEnvFiles(wsState, agentEnvStateDir, channel)
}

// writeAgentEnvFiles rewrites <wsState>/<dir> to hold exactly this entry's per-agent files:
// one per profiled agent with anything of its own, and nothing else. REPLACE, NEVER MERGE —
// an agent the previous entry scoped a credential to and this one does not must lose its
// file, or the attach that deselected its profile would leave the credential in place.
// Best-effort like writeUserEnvFile: a failure leaves the agent without its values (fail
// closed), and podman names a bind source it cannot find.
//
// The directory is agentEnvDirMode and each file agentEnvFileMode on EVERY write, not only
// on creation: a directory an earlier build or the jail left wider is narrowed again. The
// writes and removals are beneath wsState's os.Root for writeUserEnvFile's reason.
func writeAgentEnvFiles(wsState, dir string, channel *packChannel) {
	r, err := openStateRoot(wsState)
	if err != nil {
		return
	}
	defer r.Close()
	if err := agentEnvDirBeneath(r, dir); err != nil {
		return
	}
	keep := map[string]bool{}
	if channel != nil && channel.scope != nil {
		for _, agent := range channel.scope.Agents() {
			if !packdecl.ValidBinName(agent) {
				continue
			}
			body := agentEnvFileContent(channel, agent)
			if body == "" {
				continue
			}
			name := agent + ".sh"
			if err := writeBeneath(r, filepath.Join(dir, name),
				agentEnvFileMode, agentEnvFileMode, writeBytes([]byte(body))); err != nil {
				continue
			}
			keep[name] = true
		}
	}
	for _, e := range readDirBeneath(r, dir) {
		if !keep[e.Name()] {
			_ = r.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// agentEnvDirBeneath makes dir below r a real directory of agentEnvDirMode. Its PARENTS are
// made the ordinary way (a link on the way that stays inside the root is followed — on Apple
// Container dir's parent is the jail home's own .config, which is the user's to shape), but
// the directory itself is yolo's: anything else found there, a symbolic link the jail left
// included, is removed rather than written through, so podman never binds a link and a copy
// never lands where a link points.
func agentEnvDirBeneath(r *os.Root, dir string) error {
	if err := mkdirParentBeneath(r, dir); err != nil {
		return err
	}
	if fi, err := r.Lstat(dir); err == nil && !fi.IsDir() {
		if err := r.RemoveAll(dir); err != nil {
			return err
		}
	}
	if err := r.Mkdir(dir, agentEnvDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return r.Chmod(dir, agentEnvDirMode)
}

// agentsWithOwnValues lists, sorted, the agents this entry's gate scoped anything to — the
// agents that get a file of their own. Empty for a channel with no scope.
func (c *packChannel) agentsWithOwnValues() []string {
	if c == nil || c.scope == nil {
		return nil
	}
	var out []string
	for _, agent := range c.scope.Agents() {
		if !c.scope.Agent(agent).Empty() {
			out = append(out, agent)
		}
	}
	sort.Strings(out)
	return out
}

// agentEnvFileContent renders one agent's file, "" when the gate scoped nothing to it. The
// grammar is the shared file's, and so is the precedence it carries: env_sources values are
// def-form defaults (`export K=${K:-'v'}`) the environment may beat, and what the launch
// composed — the gated pack env, the env derive's shape vars — is plain-form and wins. A
// shape var's tombstone is spelled `unset K`, which this file (sourced only by bash, never
// parsed back by the entrypoint) can say and the shared file cannot.
func agentEnvFileContent(channel *packChannel, agent string) string {
	d := channel.scope.Agent(agent)
	if d.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Auto-generated by yolo: what this launch scoped to " + agent + " alone\n")
	b.WriteString("# (provider-credential-scope.md). Rewritten by every entry; sourced by its launcher.\n")
	for _, k := range d.EnvSources.Keys() {
		v, _ := d.EnvSources.Get(k)
		val, _ := v.(string)
		b.WriteString(exportDefault(k, val))
	}
	keys := make([]string, 0, len(d.PackEnv))
	for k := range d.PackEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(exportPlain(k, d.PackEnv[k]))
	}
	for _, v := range d.Shape {
		// A derive names its variables in Lua, and this file is bash SOURCE: a key that is
		// not a variable name is not written, rather than spliced into a shell line.
		if !packdecl.ValidEnvName(v.Key) {
			continue
		}
		if v.Unset {
			b.WriteString("unset " + v.Key + "\n")
			continue
		}
		b.WriteString(exportPlain(v.Key, v.Value))
	}
	return b.String()
}
