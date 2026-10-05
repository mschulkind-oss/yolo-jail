package run

// agentenvfiles.go is the CONTAINER VEHICLE's half of the credential gate
// (docs/reference/providers.md, OQ-CN6): the per-agent env files, written from
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
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
// THE SHARED FILE GETS THE GATE'S SHARED COMPOSITION, never the hydration: a caller that handed
// it channel.userEnv would put every provider credential back in every process, which is the
// leak this whole design closes (TestDeliverChannelScopesCredentialsPerAgent is the pin). Both
// files serialize the one ordered composition (packload's envcompose.go), so a name has one
// winner in a jail shell, in an agent's process, and at every other vehicle.
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
	writeUserEnvFile(shared, channel.sharedEnvSourceWinners(), channel)
	if rt == "container" { // parity: HonoredBy — Apple Container binds wsState at /home/agent, so both files are written at their in-home paths beneath it, live on every entry, instead of bound
		acMaterialize(shared, ".config/yolo-user-env.sh", wsState)
		writeAgentEnvFiles(wsState, entrypoint.AgentEnvDirRel, channel)
		return
	}
	writeAgentEnvFiles(wsState, agentEnvStateDir, channel)
}

// clearAgentEnvFiles removes every per-agent env file a container jail's entries wrote under
// wsState, where rt reads them (deliverChannel's two places), keeping the directory the next
// launch binds. The jail's teardown calls it (forgetGoneCredentials, ES-D38): the files carry
// provider keys, and nothing else removes them once the jail is gone.
func clearAgentEnvFiles(wsState, rt string) {
	dir := agentEnvStateDir
	if rt == "container" { // parity: HonoredBy — Apple Container's agent files are written in the home it binds, at their in-jail path (deliverChannel)
		dir = entrypoint.AgentEnvDirRel
	}
	if _, err := os.Stat(filepath.Join(wsState, dir)); err != nil {
		return // nothing written, so nothing to make
	}
	writeAgentEnvFiles(wsState, dir, nil)
}

// writeMacosUserAgentEnvFiles is the macos-user arm's delivery of the per-agent files (OQ-CN9):
// writeAgentEnvFiles, the container vehicle's own writer, into <sidecar>/config/yolo-agent-env —
// entrypoint.AgentEnvDirRel with its leading dot trimmed, the rule the bootstrap's home layout
// links the sandbox's ~/.config by (entrypoint.DeriveDarwinHomeLayout: `.config` → `config`).
// One writer, so the two backends' files cannot differ in grammar, mode or revocation.
func writeMacosUserAgentEnvFiles(sidecar string, channel *packChannel) {
	writeAgentEnvFiles(sidecar, macosUserAgentEnvDir, channel)
}

// macosUserAgentEnvDir is the per-agent env directory beneath the macos-user sidecar.
var macosUserAgentEnvDir = strings.TrimPrefix(entrypoint.AgentEnvDirRel, ".")

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

// agentsWithOwnValues lists, sorted, the agents this entry's gate scoped anything to. Each gets
// a file of its own when its composition differs from the shared one somewhere
// (agentEnvFileContent), which a scoped value almost always makes it. Empty for a channel with no
// scope.
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

// agentEnvFileContent renders one agent's file, "" when it has nothing to say.
//
// WHAT IT WRITES is the agent's composition where it differs from the shared one (packload's
// envcompose.go: the shape var over the env_sources it receives over the pack env fold, an
// env_sources null and a shape tombstone each removing what ranks below): one line per name
// whose winner for this agent is not the shared composition's, and none for a name the two
// answer alike (sharesWinner). The shared file is sourced first, so such a name is left as the
// jail shell holds it: the container's frozen value, where the shared file's line is a def-form
// default or a null it writes nowhere, and the channel's own lines (the wire tables, the caller
// tokens) are kept for the agent as for every process. So no agent's file sets a name to the
// shared composition's value, and every `case` line for a name the shared composition sets to a
// non-empty value lists that value: the macos-user MCP view reads the shared value back off the
// files that way (internal/entrypoint's sharedValueInAgentFiles).
//
// THE PRECEDENCE IS "THE USER'S EXPLICIT VALUE WINS" (docs/reference/providers.md
// OQ-CN8, ruled 2026-09-28). The file is sourced by the agent's launcher, AFTER the user's
// shell, so a plain-form line here would beat `ANTHROPIC_MODEL=x claude` and a jail-shell
// `export`, which the shared file, sourced before the user's command, never did. So every line
// is written against the launcher's INCOMING environment (CN-D21's grammar):
//
//   - a value whose name nothing else yolo set is def-form (`export K=${K:-'v'}`): the
//     environment wins, an unset or empty K takes the value;
//   - a value whose name yolo DID set elsewhere (inheritedValues: the shared file, the
//     container's frozen environment, another agent's file this entry wrote) is written as a
//     `case` over those values: it overrides only when the incoming value is empty or one of
//     them, because then it is an inherited default rather than the user's choice, and a stale
//     inherited value must not beat the profile. Any other value is the user's and is kept;
//   - a removal (a shape tombstone, an env_sources null) is `unset K` only when K's incoming
//     value is one yolo set elsewhere, for the same reason, and is not written when yolo set K
//     nowhere else.
//
// The comparison is by VALUE, not by name, because a name alone cannot tell a per-command
// override of a shared variable from the shared variable itself (CN-D21 in the design's ledger).
func agentEnvFileContent(channel *packChannel, agent string) string {
	d := channel.scope.Agent(agent)
	if d.Empty() {
		return ""
	}
	view := channel.envView()
	var lines strings.Builder
	for _, e := range view.agents[agent].Entries() {
		// A derive names its variables in Lua, and this file is bash SOURCE: a key that is
		// not a variable name is not written, rather than spliced into a shell line.
		if !packdecl.ValidEnvName(e.Key) {
			continue
		}
		// THE WIRE TABLES ARE THE LAUNCH'S: the shared file writes them plain-form after the
		// composition, as the host exec and launchEnv write them after it, so no entry of the
		// agent's composition (a pack's env, an env_sources value or null, a shape var) replaces
		// or removes one in the agent's process either.
		if slices.Contains(entrypoint.WireTables(), e.Key) {
			continue
		}
		if view.sharesWinner(e) {
			// THE AGENT'S WINNER IS EVERY PROCESS'S (an unclaimed env_sources value, an env_sources
			// null, a fold winner), so the shared file already answers it and this file writes no
			// line. The slot then holds what the shared file left there, the container's frozen
			// value over a def-form line or a null and the channel's own lines (the wire tables,
			// the caller tokens) included, which is what a jail shell holds too. Two readings were
			// built and withdrawn. Reading a frozen value as a stale default gave the agent a second
			// winner on an attach, the one entry that knows bootEnv. Overriding another agent's
			// value, for an agent started by that one, wrote the shared value into this file, and
			// the macos-user MCP view finds the shared value as the guard value no agent's file
			// sets (internal/entrypoint's sharedValueInAgentFiles), so a server gated on the name
			// left the shared table. An agent started by another agent keeps that agent's value of
			// such a name, as it did before the one composition.
			continue
		}
		boot := true
		if e.Origin == packload.FromProfileEnv && filledRegion(d, e) {
			// THE REGION FILL'S VALUE (packload's regionfill.go, docs/design/bedrock-plumbing.md
			// BR-D23) is a FALLBACK: the gate found no region for this agent anywhere it composes.
			// So it yields to the container's frozen environment, as the fresh launch's default
			// does (a `loopholes.<name>.jail_env` region on the argv), rather than reading that
			// value as a stale yolo default to override on an attach; it still overrides what
			// another agent's file composes, which an agent started by that one inherits.
			boot = false
		}
		inherited := channel.inheritedValues(view, agent, e.Key, boot)
		if e.Unset {
			if len(inherited) > 0 {
				lines.WriteString("case \"${" + e.Key + "-}\" in " + casePatterns(inherited) +
					") unset " + e.Key + " ;; esac\n")
			}
			continue
		}
		// A value equal to the agent's own needs no pattern: overriding it with itself is a
		// no-op, and leaving it out keeps a name only the agent's value holds def-form.
		others := slices.DeleteFunc(slices.Clone(inherited), func(v string) bool { return v == e.Value })
		lines.WriteString(exportComposed(e.Key, e.Value, others))
	}
	if lines.Len() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Auto-generated by yolo: what this launch scoped to " + agent + " alone\n")
	b.WriteString("# (provider-credential-scope.md). Rewritten by every entry; sourced by its launcher.\n")
	b.WriteString("# A value you set yourself wins over the profile's (OQ-CN8).\n")
	b.WriteString(lines.String())
	return b.String()
}

// filledRegion reports whether e is the region the gate's region fill appended to d's shape vars
// (AgentDelivery.RegionFile): the one shape var whose name no other channel of d's sets, since
// the fill runs only where none does.
func filledRegion(d *packload.AgentDelivery, e packload.EnvEntry) bool {
	l := d.RegionFile
	return l != nil && l.Region != "" && !e.Unset && e.Key == l.Var && e.Value == l.Region
}

// exportComposed renders one composed value against the incoming environment
// (agentEnvFileContent's precedence): def-form when yolo set the name nowhere else, else a
// `case` that overrides only an empty value or one of inherited.
func exportComposed(key, val string, inherited []string) string {
	if len(inherited) == 0 {
		return exportDefault(key, val)
	}
	return "case \"${" + key + "-}\" in ''|" + casePatterns(inherited) + ") export " + key + "='" +
		strings.ReplaceAll(val, "'", `'\''`) + "' ;; esac\n"
}

// casePatterns is values as literal `case` patterns, single-quoted and `|`-joined.
func casePatterns(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
	}
	return strings.Join(parts, "|")
}

// envView is every composition one delivery reads (packload's envcompose.go), composed once:
// the shared one and each agent's with a delivery of its own, beside the shared file's other
// plain-form values (sharedChannelExports).
type envView struct {
	shared  packload.EnvComposition
	agents  map[string]packload.EnvComposition
	exports map[string]string
}

// envView composes this channel's compositions.
func (c *packChannel) envView() envView {
	v := envView{shared: c.scope.SharedEnv(), agents: map[string]packload.EnvComposition{},
		exports: c.sharedChannelExports()}
	for _, agent := range c.scope.Agents() {
		v.agents[agent] = c.scope.EnvFor(agent)
	}
	return v
}

// inheritedValues is every non-empty value, sorted and deduplicated, that YOLO — not the user —
// may have put in key's slot of agent's launcher's incoming environment this entry, read off
// view (this channel's compositions, composed once): the shared file's own value (its def-form
// env_sources default or its plain pack env line, whichever the shared composition chose, and
// the wire tables and exported caller tokens of its channel section), the container's frozen
// environment (bootEnv, read off the running container on an attach) when boot is set, and the
// value any OTHER agent's composition gives key, which an agent started by that agent inherits
// from its file. OQ-CN8's "names the shared file or the boot set", answered by value: a value
// equal to one of these is inherited, and any other is the user's. The region fill's value
// passes boot false (filledRegion).
func (c *packChannel) inheritedValues(view envView, agent, key string, boot bool) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if v, ok := view.shared.Value(key); ok {
		add(v)
	}
	add(view.exports[key])
	if boot {
		add(c.bootEnv[key])
	}
	for _, v := range c.otherAgentsValues(view, agent, key) {
		add(v)
	}
	sort.Strings(out)
	return out
}

// otherAgentsValues is every non-empty value, sorted and deduplicated, that an agent other than
// agent receives under key from its own composition: what an agent started by that one inherits
// from its file.
func (c *packChannel) otherAgentsValues(view envView, agent, key string) []string {
	seen := map[string]bool{}
	var out []string
	for other, comp := range view.agents {
		if other == agent {
			continue
		}
		if v, ok := comp.Value(key); ok && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// sharesWinner reports whether e, an entry of one agent's composition, is also the shared
// composition's entry for its name: the same value, or a removal in both.
func (v envView) sharesWinner(e packload.EnvEntry) bool {
	s, ok := v.shared.Lookup(e.Key)
	return ok && s.Unset == e.Unset && s.Value == e.Value
}

// sharedChannelExports is the channel section's plain-form values beyond the pack env, as
// writeUserEnvFile writes them: the wire tables and the exported caller tokens.
func (c *packChannel) sharedChannelExports() map[string]string {
	out := map[string]string{}
	wire := c.wireTableValues()
	for _, k := range entrypoint.WireTables() {
		out[k] = wire[k]
	}
	for k, v := range c.callerTokens {
		if !c.scopedTokenVars[k] {
			out[k] = v
		}
	}
	return out
}
