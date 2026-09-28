package run

// callertokens.go is the launcher half of a pack service's CALLER TOKEN (coined in
// docs/reference/wire-bridge.md, WB-D18; the variable's spelling is paths.ServiceCallerTokenEnv):
// a random per-launch secret the launcher mints for every jail daemon this launch runs that
// demands one — every selected pack service's, and each loophole's that declares
// `jail_daemon.caller_token` — and that the daemon then demands of every caller.
//
// WHY. WB-D4 once ruled the bridge's inbound auth out because "the jail is the trust boundary".
// It is not, on loopback: a jail on `network.mode: host` and a macos-user sandbox share the
// host's loopback, and a nested podman forced onto `--net=host` shares its parent jail's, so the
// bridge's port is one any process on that loopback can reach, or take first. The maintainer's ruling of 2026-09-27 is quoted in the
// decision row.
//
// WHERE IT TRAVELS. Only through the per-entry channel: an unconditional line in the 0600
// yolo-user-env.sh channel section (writeUserEnvFile), which the daemon's boot hydrates and every
// jail process inherits, and — composed through the credential gate (packload.ScopeInput's
// CallerTokens) — into the agent derives that point a client at the service, which is how
// claude's ANTHROPIC_AUTH_TOKEN and copilot's key end up carrying it. Never on the container
// argv (podman inspect would print it), never in a rendered config file (the derives write the
// variable's NAME there), never in a log.
//
// LIFETIME. A fresh launch mints; an ATTACH reuses the running jail's, read back from the live
// channel file that launch wrote (runningCallerTokens), because the daemon serving in that jail
// read its token once at boot and a new one would lock every new entry's clients out of it.

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// callerTokenVars is the variable of every jail daemon in this launch's composed payload
// (jailDaemonsFor: the active loopholes' own and the selected pack services') that demands a
// caller token, sorted. A pack service's always does, because every address it serves names
// its token as the credential (packload's serviceCredentialEnv); a loophole's does when its
// manifest declares `jail_daemon.caller_token` — the OpenAI and AWS credential adapters, whose
// clients carry the token in a slot they already have, and not the Claude OAuth terminator,
// whose client cannot and which authenticates by refresh-token match instead
// (docs/plans/notch-convergence.md §2.3, NC-D3). A daemon the payload does not name runs
// nowhere in this launch, so it has nothing to demand a token and gets none.
func callerTokenVars(specs []loopholes.JailDaemonSpec) []string {
	seen := map[string]bool{}
	var out []string
	for _, spec := range specs {
		if !spec.CallerToken {
			continue
		}
		v := paths.ServiceCallerTokenEnv(spec.Name)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// launchCallerTokens is the caller token of every daemon callerTokenVars names in specs, for
// this launch: the one o already settled on for that variable (minted by an earlier
// composition in this process, or adopted from the running jail by an attach), else a fresh
// one. Settled once per process, so the two compositions one launch can run (Run's, and an
// attach's over the running jail's packs) cannot hand one jail two tokens. nil when no daemon
// in specs needs one.
func (o *Options) launchCallerTokens(specs []loopholes.JailDaemonSpec) (map[string]string, error) {
	vars := callerTokenVars(specs)
	if len(vars) == 0 {
		return nil, nil
	}
	if o.callerTokens == nil {
		o.callerTokens = map[string]string{}
	}
	out := make(map[string]string, len(vars))
	for _, v := range vars {
		tok := o.callerTokens[v]
		if !svcendpoint.IsToken(tok) {
			minted, err := svcendpoint.NewToken()
			if err != nil {
				return nil, err
			}
			tok = minted
			o.callerTokens[v] = tok
		}
		out[v] = tok
	}
	return out, nil
}

// runningCallerTokens reads the caller tokens out of the live per-entry channel file the running
// jail's launch wrote (<wsState>/yolo-user-env.sh), keyed by variable. Only well-formed tokens
// are returned: the file is jail-writable, so a line the jail rewrote is either a token (which it
// already knew) or ignored, and the read refuses a link at either path component
// (readRegularFileIn), so the jail cannot turn it into a read of a host file. nil when the file
// or its channel section is missing — a jail launched by a yolo older than caller tokens, whose
// bridge demands none.
func runningCallerTokens(wsState string) map[string]string {
	data, err := readRegularFileIn(wsState, "yolo-user-env.sh")
	if err != nil {
		return nil
	}
	values, ok := entrypoint.ParseEntryChannel(data)
	if !ok {
		return nil
	}
	var out map[string]string
	for k, v := range values {
		if !paths.IsServiceCallerTokenEnv(k) || !svcendpoint.IsToken(v) {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	return out
}

// adoptRunningCallerTokens makes the running jail's tokens this process's, over any it minted:
// what an attach must deliver is the token the jail's daemon already demands.
func (o *Options) adoptRunningCallerTokens(running map[string]string) {
	if len(running) == 0 {
		return
	}
	if o.callerTokens == nil {
		o.callerTokens = map[string]string{}
	}
	for k, v := range running {
		o.callerTokens[k] = v
	}
}

// callerTokensAgree reports whether channel was composed with the tokens o has settled on, for
// every variable it carries. A channel composed before an attach adopted the running jail's
// tokens does not, and is composed again (rekeyChannelForAttach).
func (c *packChannel) callerTokensAgree(settled map[string]string) bool {
	if c == nil {
		return true
	}
	for k, v := range c.callerTokens {
		if s, ok := settled[k]; ok && s != v {
			return false
		}
	}
	return true
}

// rekeyChannelForAttach returns the channel an attach delivers into the running jail, keyed with
// THAT jail's caller tokens: channel itself when it already carries them (or the jail has none —
// launched by a yolo older than caller tokens, whose daemon demands nothing), else the same
// composition run again over packs, the jail's own, with the running tokens adopted. Composed
// again rather than patched, because the token reaches a derive's output (claude's
// ANTHROPIC_AUTH_TOKEN) and only the derive knows where.
func (o *Options) rekeyChannelForAttach(wsState string, cfgPacks []*packload.Pack,
	channel *packChannel, compose func([]*packload.Pack) (*packChannel, error)) (*packChannel, error) {
	o.adoptRunningCallerTokens(runningCallerTokens(wsState))
	if channel.callerTokensAgree(o.callerTokens) {
		return channel, nil
	}
	return compose(cfgPacks)
}
