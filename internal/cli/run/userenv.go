package run

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// channelSectionHeader marks the per-entry channel inside yolo-user-env.sh. The
// whole file is rewritten by every launch, but the two halves answer to different
// authorities and the comment says which is which: the lines above it are
// env_sources defaults (def-form, overridable), the lines below it are what THIS
// entry composed and land unconditionally.
const channelSectionHeader = entrypoint.EntryChannelSectionHeader

// writeUserEnvFile writes yolo-user-env.sh. Frozen contract (must not drift —
// the in-jail entrypoint reads this file back and depends on the exact format).
// When userEnv is non-empty it writes the two header comment lines then one
//
//	export K=${K:-'v'}
//
// line per entry (in userEnv order), with each value's single quotes escaped as
// '\” (the `'` → `'\”` replacement).
//
// THE CHANNEL SECTION. A non-nil channel appends the provider/profile environment
// this entry composed that EVERY process may see — the three wire tables
// (YOLO_PROVIDERS, YOLO_PROFILES, YOLO_USE_PROFILES) and the ungated pack env fold's
// winners in the shared composition (sharedFoldWinners: a name env_sources assigns or removes
// is not among them, nor a wire table's) — as UNCONDITIONAL `export K='v'` lines. What the credential gate scopes to one agent
// (a provider's claimed credentials, a profile-gated env, the shape vars) is NOT
// here: this file's first reader exports it into every process of the jail, so those
// values go to that agent's own env file instead (agentenvfiles.go, OQ-CN6), and
// userEnv is the gate's shared half (deliverChannel) rather than the hydration. The
// two grammars are the precedence, read
// off the line by every consumer: def-form `${K:-'v'}` is a default the
// environment may beat, plain-form `'v'` is this entry's value and beats
// everything, container environment included. That inversion is the point of the
// spelling: the channel must not survive into an entry that did not compose it,
// so it can never be a def-form default and the file can never hold a second,
// stale copy of a previous entry's providers.
//
// This file and the per-agent files are the channel's ONLY crossings (per-entry env
// delivery, agent-auth-modes.md §4.3): the podman argv carries none of it, so the
// container's frozen environment holds no provider state for a later exec to
// inherit. The bind is live — writing the file in place is visible inside a
// running jail, which is how an attach delivers (write, then `podman exec`; the
// exec'd entrypoint re-runs the boot and hydrates the fresh file). The line
// order is frozen to match the argv spelling this replaced: tables (in wire
// order), then the shared pack env fold (sorted), then the pack services' caller
// tokens (sorted; callertokens.go), which no argv ever carried.
// The three tables are written even when empty — `{}` crosses "none" and
// revokes what a previous entry selected.
//
// An empty userEnv AND nil channel TRUNCATES to zero bytes, leaving the file in
// place so the bind mount still has a source (podman refuses to start on a
// missing one). It must not merely touch: dropping env_sources from config
// yields an empty map, and a no-op on an existing path left the previous
// launch's render mounted — so commented-out credentials kept being exported,
// rebuild after rebuild, via hydrateEnvFromUserEnvFile and .bashrc alike.
// Removing a key from config has to revoke it. A non-nil channel always writes
// its section (possibly all-empty tables), which revokes the same way: the file
// is rewritten whole, so what the new entry did not compose is gone. Returns
// the file path.
//
// MODE 0600, NOT 0644. This file holds hydrated env_sources VALUES in plaintext —
// API keys, in practice, though since the credential gate only the ones no provider
// claims (the claimed ones, and the provider tokens the shape vars relay, sit in the
// per-agent files at the same mode) — and it was world-readable until
// 2026-09-01 (measured in a live jail: `-rw-r--r--` holding two provider keys).
// packs/zai's README tells the user to keep that key in a file that is
// "untracked, 0600", and yolo's own copy of the value was downgrading the mode
// the user had chosen.
//
// 0600 is safe for every reader this file HAS, which is worth stating because a
// mode tightening that breaks a reader is worse than the exposure: in a container
// the entrypoint (boot.go) and the generated .bashrc both read it as the jail's
// own uid, and the file is owned by that same uid; on the macos-user backend it
// is read by the invoking user, who wrote it. Nothing reads it as a non-owner.
// Both the empty and non-empty paths carry the mode, because a truncation that
// widened the mode back to 0644 would undo this on the next launch that drops
// env_sources.
//
// os.WriteFile applies the mode only when CREATING; an existing file keeps its own.
// So the chmod is explicit — a file created 0644 by an older yolo must be narrowed
// in place, and every launch is the migration.
func writeUserEnvFile(userEnvFile string, userEnv *jsonx.OrderedMap, channel *packChannel) {
	// The parent must exist for the ATTACH write in particular: the fresh path runs
	// prepareWsState first, an attach does not — it counts on the launch that created
	// the jail, and mkdir is cheaper than that assumption. writeFileBeneathMode creates it.
	//
	// BENEATH the file's directory (wsState), never a plain path write, and the mode is set
	// through the open file: the jail can replace this file with a link, and a write and chmod
	// that followed it would put these secrets in, and narrow, a host file of its choosing
	// (wsstatebeneath.go).
	dir, name := filepath.Dir(userEnvFile), filepath.Base(userEnvFile)
	if channel == nil && (userEnv == nil || userEnv.Len() == 0) {
		_ = writeFileBeneathMode(dir, name, nil, userEnvFileMode)
		return
	}
	var b strings.Builder
	b.WriteString("# Auto-generated from yolo-jail.jsonc env config.\n")
	b.WriteString("# Override by editing this file or workspace .env (mise).\n")
	if userEnv != nil {
		for _, k := range userEnv.Keys() {
			v, _ := userEnv.Get(k)
			if v == nil {
				continue // a removal (config.HydrateEnvSources): nothing to write
			}
			val, _ := v.(string)
			b.WriteString(exportDefault(k, val))
		}
	}
	if channel != nil {
		b.WriteString(channelSectionHeader + "\n")
		wire := channel.wireTableValues()
		for _, k := range entrypoint.WireTables() {
			b.WriteString(exportPlain(k, wire[k]))
		}
		// The shared pack env, ONE LINE PER KEY (packload's envcompose.go): only the names whose
		// winner in the shared composition is the fold. A name env_sources assigns is its
		// def-form line above, which beats the fold, and a name an env_sources null removes is
		// written nowhere. Writing both lines for one name made the plain fold line win, a pack's
		// default beating the user's dotenv value in every jail shell.
		//
		// A fold winner under a wire table's name is not written either (channelPackEnv): the
		// tables are the launch's, written after the composition at every vehicle (the host exec,
		// launchEnv), and a plain fold line after them here would hand every process a pack's value
		// instead.
		shared := channel.channelPackEnv()
		keys := make([]string, 0, len(shared))
		for k := range shared {
			if userEnv != nil {
				if _, assigned := userEnv.Get(k); assigned {
					continue
				}
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(exportPlain(k, shared[k]))
		}
		// The pack services' CALLER TOKENS, last, so no pack env of the same name can shadow
		// what the daemon demands (callertokens.go, WB-D18). Here, in the 0600 channel, and on
		// no argv: the daemon's boot hydrates this section, every jail process inherits it,
		// and a bridged client's rendered config names the variable rather than the value.
		tokenVars := make([]string, 0, len(channel.callerTokens))
		for k := range channel.callerTokens {
			tokenVars = append(tokenVars, k)
		}
		sort.Strings(tokenVars)
		for _, k := range tokenVars {
			// A SCOPED token (OQ-CN7 (c)) is exported only in the agent files its pointer
			// reaches; here it is a record no reader exports (entrypoint.ScopedCallerTokenRecord).
			if channel.scopedTokenVars[k] {
				b.WriteString(entrypoint.ScopedCallerTokenRecord(k, channel.callerTokens[k]))
				continue
			}
			b.WriteString(exportPlain(k, channel.callerTokens[k]))
		}
		carried := make([]string, 0, len(channel.carriedTokens))
		for k := range channel.carriedTokens {
			if _, own := channel.callerTokens[k]; !own {
				carried = append(carried, k)
			}
		}
		sort.Strings(carried)
		for _, k := range carried {
			b.WriteString(entrypoint.ScopedCallerTokenRecord(k, channel.carriedTokens[k]))
		}
		// The SERVED ADDRESSES this entry composed, when it moved any (servedaddresses.go):
		// the jail's daemons bound them at boot, and the next attach reads them back from here
		// to compose its clients for the same ports.
		if v := servedAddressesValue(channel.servedAddresses); v != "" {
			b.WriteString(exportPlain(paths.ServedAddressesEnv, v))
		}
	}
	_ = writeFileBeneathMode(dir, name, []byte(b.String()), userEnvFileMode)
}

// sharedFoldWinners is the shared composition's fold winners (packload's envcompose.go): the
// pack env every process receives, minus each name env_sources assigns or removes. The channel
// section's pack env lines, plain-form.
func (c *packChannel) sharedFoldWinners() map[string]string {
	out := map[string]string{}
	for _, e := range c.scope.SharedEnv().Entries() {
		if e.Origin == packload.FromPackEnv && !e.Unset {
			out[e.Key] = e.Value
		}
	}
	return out
}

// channelPackEnv is the pack env the shared file's channel section carries, plain-form: the
// shared composition's fold winners (sharedFoldWinners) minus every wire table's name, which the
// section writes as the launch's own table. It is what a container's jail-daemon supervisor
// inherits as pack env from that section.
func (c *packChannel) channelPackEnv() map[string]string {
	out := c.sharedFoldWinners()
	for _, k := range entrypoint.WireTables() {
		delete(out, k)
	}
	return out
}

// sharedEnvSourceWinners is the shared composition's env_sources winners, in hydration order: the
// unclaimed assignments every process receives, which the shared file writes def-form. Every one
// of them wins its name in the shared composition, since env_sources ranks above the fold there.
func (c *packChannel) sharedEnvSourceWinners() *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, e := range c.scope.SharedEnv().Entries() {
		if e.Origin == packload.FromEnvSources && !e.Unset {
			out.Set(e.Key, e.Value)
		}
	}
	return out
}

// exportDefault renders one overridable env_sources default: the environment
// wins. Single quotes are escaped for the single-quoted context.
func exportDefault(key, val string) string {
	return "export " + key + "=${" + key + ":-'" + strings.ReplaceAll(val, "'", `'\''`) + "'}\n"
}

// exportPlain renders one unconditional channel value: the file wins, which is
// what makes a channel line per-entry rather than a default.
func exportPlain(key, val string) string {
	return "export " + key + "='" + strings.ReplaceAll(val, "'", `'\''`) + "'\n"
}

// userEnvFileMode is owner-only: the file holds hydrated secrets in plaintext.
// See writeUserEnvFile's doc for why every reader is the owner.
const userEnvFileMode = 0o600
