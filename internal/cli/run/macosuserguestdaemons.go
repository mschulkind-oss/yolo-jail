package run

// macosuserguestdaemons.go composes what the macos-user arm hands its Seatbelt guest's
// jail-daemon supervisor (internal/macosuser's jaildaemon.go;
// docs/reference/macos-user-nix-and-features.md, built on OQ-DP8 and OQ-DP9 of
// docs/design/declaration-parity.md).
//
// # Two vehicles, and why the daemons get their own
//
// A container's supervisor inherits the jail's environment: the `-e YOLO_JAIL_DAEMONS=`
// payload, the endpoint variables, and the channel section of yolo-user-env.sh the boot
// hydrates, where every caller token is either exported or, for a SCOPED one, kept as a
// record the daemon reads (entrypoint.ScopedCallerTokenRecord). This backend has no jail
// environment to inherit — each sandboxed process reads one root-owned env file — and the
// agent's file is narrowed by the credential gate to what THAT program may see. So the
// supervisor gets a file of its own (macosuser.SandboxDaemonEnvFile), composed here from the
// same channel: the payload, the shared channel values the container's supervisor inherits,
// every caller token its daemons demand (a scoped one exported, since the daemon is its
// consumer and no agent reads this file), and the endpoint file each daemon dials.
//
// # The agent's half
//
// A client that binds a caller token itself needs it in ITS environment, as it has in a
// container's shared channel: the Codex launcher's auth.json writer binds
// $YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN into the refresh marker the adapter checks
// (openauthclient.WriteCodexAuth). guestSharedCallerTokens is that exported, UNSCOPED set, for
// the daemons the guest runs; a scoped token keeps reaching only the agents its pointer
// reaches, through their per-agent env files, exactly as on a container.

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// guestJailDaemons is the macos-user guest's supervisor input for this launch: runs, the
// daemons the guest runs (loopholes.JailDaemonsRunIn), composed into the env
// jailDaemonEnv describes. The zero value when the guest runs none.
func (c *packChannel) guestJailDaemons(runs []loopholes.JailDaemonSpec, launchEnv *jsonx.OrderedMap) macosuser.JailDaemons {
	if len(runs) == 0 {
		return macosuser.JailDaemons{}
	}
	return macosuser.JailDaemons{Env: c.jailDaemonEnv(runs, launchEnv)}
}

// jailDaemonEnv is the supervisor's environment, in this order: the payload, the three wire
// tables and the shared pack env (what a container's supervisor inherits from the channel
// section), the caller token of every daemon in runs that demands one, and every endpoint
// variable the launch carries. Every key is set once; a later layer never shadows the payload.
func (c *packChannel) jailDaemonEnv(runs []loopholes.JailDaemonSpec, launchEnv *jsonx.OrderedMap) *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	payload, err := jsonx.DumpsCompact(loopholes.JailDaemonPayload(runs))
	if err != nil {
		return env // no payload: the plan's invariant then refuses the launch, naming it
	}
	env.Set("YOLO_JAIL_DAEMONS", payload)
	wire := c.wireTableValues()
	for _, k := range entrypoint.WireTables() {
		env.Set(k, wire[k])
	}
	if c.scope != nil {
		shared := c.scope.SharedPackEnv()
		keys := make([]string, 0, len(shared))
		for k := range shared {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env.Set(k, shared[k])
		}
	}
	for _, k := range callerTokenVars(runs) {
		if tok := c.callerTokens[k]; tok != "" {
			env.Set(k, tok)
		}
	}
	if launchEnv != nil {
		for _, k := range launchEnv.Keys() {
			if isServiceEndpointEnv(k) {
				v, _ := launchEnv.Get(k)
				env.Set(k, v)
			}
		}
	}
	return env
}

// guestSharedCallerTokens is the exported caller token of every daemon in runs that demands
// one and whose token is not SCOPED (OQ-CN7 (c)): the ones a container's shared channel
// exports to every process, which the agent's own env file carries on this backend. The arm
// hands it every daemon the launch SERVES (loopholes.ServedJailDaemons), the doorways it opens
// outside the sandbox included (macosuserdoorways.go): the Codex launcher binds the refresh
// doorway's token into the marker that doorway checks, wherever it listens.
func (c *packChannel) guestSharedCallerTokens(runs []loopholes.JailDaemonSpec) map[string]string {
	out := map[string]string{}
	for _, k := range callerTokenVars(runs) {
		if c.scopedTokenVars[k] {
			continue
		}
		if tok := c.callerTokens[k]; tok != "" {
			out[k] = tok
		}
	}
	return out
}

// isServiceEndpointEnv reports whether key is a host service's ENDPOINT-FILE variable, by
// the producer's own two halves (hostServiceEnvVar), never a literal.
func isServiceEndpointEnv(key string) bool {
	return len(key) > len(paths.ServiceEnvVarPrefix)+len(paths.ServiceEnvVarSuffix) &&
		strings.HasPrefix(key, paths.ServiceEnvVarPrefix) &&
		strings.HasSuffix(key, paths.ServiceEnvVarSuffix)
}
