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
// (openauthclient.WriteCodexAuth), and pi's via row names $YOLO_SERVICE_WIRE_BRIDGE_TOKEN as its
// apiKey (WG-I36). guestSharedCallerTokens is that exported, UNSCOPED set, for the daemons the
// guest runs, the doorways it opens outside, and the launch-owned services (macosuserservices.go)
// whose host halves answer only this launch's token; a scoped token keeps reaching only the
// agents its pointer reaches, through their per-agent env files, exactly as on a container.
//
// # Where a loophole's own files are
//
// The sandbox copies this launch's staged pack tree to a distinct root-owned guest path, world-readable
// and keeping each file's exec bits (macosuser.StagePackCommands), for the bootstrap to render from.
// So the module directory is in the sandbox at the same place under this launch's copy as it is
// under the host tree, and placeModuleDirsInGuest resolves the token there (JD-10).
//
// A GUEST PACK TREE IS PER HOST TREE, not per workspace: the root is derived from the exact
// immutable host staged tree passed to the pipeline. Another terminal therefore cannot replace
// bytes this session's daemon or its restart argv reads. Unknown post-consumer liveness retains
// the tree rather than guessing it is safe to remove.

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// placeModuleDirsInGuest is specs with each loophole daemon whose argv names its module
// directory (loopholes.JailDaemonSpec.NamesModuleDir) placed in the macos-user sandbox: its
// `{jail_loophole_dir}` resolved to the module directory's place in the sandbox's copy of this
// launch's staged packs (loopholes.JailDaemonSpec.InGuest). Every other runtime keeps the
// container's mount point, which the composer already resolved, so a container's payload is what
// it was. Called by jailDaemonsFor, so the split, the decline and the supervisor's payload read the
// argv the guest runs.
//
// A module directory outside this launch's staged tree has no copy in the sandbox. That cannot
// happen for a launch whose loopholes come from its staged packs, but if it does the spec keeps
// the container's path and loses its ModuleDir, so the guest's split declines it by name with the
// container mount its argv names (loopholes' guestrun.go) rather than handing the supervisor a
// path nothing copied.
func (o *Options) placeModuleDirsInGuest(rt string, specs []loopholes.JailDaemonSpec) []loopholes.JailDaemonSpec {
	if rt != "macos-user" { // parity: HonoredBy — a container mounts the module dir at the path the token resolved to at load; macos-user runs it from the sandbox's staged-pack copy
		return specs
	}
	out := append([]loopholes.JailDaemonSpec(nil), specs...)
	guestRoot := macosuser.StagedPackTreeRoot(runtime.FromWorkspace(o.Workspace), o.packTree, "")
	for i, s := range out {
		if !s.NamesModuleDir() {
			continue
		}
		rel, ok := relUnder(o.packTree, s.ModuleDir)
		if !ok {
			out[i].ModuleDir = ""
			continue
		}
		out[i] = s.InGuest(filepath.Join(guestRoot, rel))
	}
	return out
}

// relUnder is dir relative to root when dir is root or inside it, comparing the two as given and
// then with their symlinks resolved (a darwin temp or state path may be reached through /var, a
// link to /private/var). ok is false for an empty root, or a dir outside it.
func relUnder(root, dir string) (string, bool) {
	if root == "" || dir == "" {
		return "", false
	}
	try := func(r, d string) (string, bool) {
		rel, err := filepath.Rel(r, d)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return "", false
		}
		return rel, true
	}
	if rel, ok := try(root, dir); ok {
		return rel, true
	}
	r, rerr := filepath.EvalSymlinks(root)
	d, derr := filepath.EvalSymlinks(dir)
	if rerr != nil || derr != nil {
		return "", false
	}
	return try(r, d)
}

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
// tables and the channel section's pack env (channelPackEnv: what a container's supervisor
// inherits from that section, so a fold value env_sources beats or removes is not here and a
// pack's value never replaces a table), the caller token of every daemon in runs that demands
// one, and every endpoint variable the launch carries. Every key is set once; a later layer never
// shadows the payload. The section is all it mirrors: a container's supervisor also inherits the
// shared file's env_sources defaults, which this backend has never handed its guest daemons.
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
		shared := c.channelPackEnv()
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
//
// AND EVERY LAUNCH-OWNED SERVICE in services (HS-D30; macosuserservices.go): the wire bridge's host
// half serving pi's via route is no jail daemon this launch serves (the guest declines the bridge's
// jail daemon), yet it answers only its plan's token, and pi's via row names that token's variable,
// as a container's shared channel exports it to every process. Leaving it out sent pi's via route,
// and any shell that starts pi, no token at all, which the bridge refuses (401, WB-D18). The token is
// the one the channel composed with (c.callerTokens, which composePackChannel fills from the same
// plans), so the session and the host half agree.
func (c *packChannel) guestSharedCallerTokens(runs []loopholes.JailDaemonSpec, services []*launchservice.Plan) map[string]string {
	vars := callerTokenVars(runs)
	for k := range launchservice.CallerTokens(services) {
		if !slices.Contains(vars, k) {
			vars = append(vars, k)
		}
	}
	out := map[string]string{}
	for _, k := range vars {
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
