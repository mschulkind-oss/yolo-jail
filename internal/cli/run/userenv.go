package run

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// channelSectionHeader marks the per-entry channel inside yolo-user-env.sh. The
// whole file is rewritten by every launch, but the two halves answer to different
// authorities and the comment says which is which: the lines above it are
// env_sources defaults (def-form, overridable), the lines below it are what THIS
// entry composed and land unconditionally.
const channelSectionHeader = "# --- per-entry channel (rewritten by every yolo launch) ---"

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
// this entry composed — the three wire tables (YOLO_PROVIDERS, YOLO_PROFILES,
// YOLO_USE_PROFILES), the pack env fold, and the provider shape vars — as
// UNCONDITIONAL `export K='v'` lines. The two grammars are the precedence, read
// off the line by every consumer: def-form `${K:-'v'}` is a default the
// environment may beat, plain-form `'v'` is this entry's value and beats
// everything, container environment included. That inversion is the point of the
// spelling: the channel must not survive into an entry that did not compose it,
// so it can never be a def-form default and the file can never hold a second,
// stale copy of a previous entry's providers.
//
// This file is the channel's ONLY crossing (per-entry env delivery,
// agent-auth-modes.md §4.3): the podman argv carries none of it, so the
// container's frozen environment holds no provider state for a later exec to
// inherit. The bind is live — writing the file in place is visible inside a
// running jail, which is how an attach delivers (write, then `podman exec`; the
// exec'd entrypoint re-runs the boot and hydrates the fresh file). The line
// order is frozen to match the argv spelling this replaced: tables (in wire
// order), then the pack env fold (sorted), then the shape vars (channel order).
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
// MODE 0600, NOT 0644. This file holds every hydrated env_sources VALUE in
// plaintext — API keys, in practice, and since the channel moved here, the
// provider tokens the shape vars relay — and it was world-readable until
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
	// the jail, and mkdir is cheaper than that assumption.
	_ = os.MkdirAll(filepath.Dir(userEnvFile), 0o755)
	if channel == nil && (userEnv == nil || userEnv.Len() == 0) {
		_ = os.WriteFile(userEnvFile, nil, userEnvFileMode)
		_ = os.Chmod(userEnvFile, userEnvFileMode)
		return
	}
	var b strings.Builder
	b.WriteString("# Auto-generated from yolo-jail.jsonc env config.\n")
	b.WriteString("# Override by editing this file or workspace .env (mise).\n")
	if userEnv != nil {
		for _, k := range userEnv.Keys() {
			v, _ := userEnv.Get(k)
			val, _ := v.(string)
			b.WriteString(exportDefault(k, val))
		}
	}
	if channel != nil {
		b.WriteString(channelSectionHeader + "\n")
		b.WriteString(exportPlain("YOLO_PROVIDERS", jsonDumpsOrEmptyObj(channel.providers)))
		b.WriteString(exportPlain("YOLO_PROFILES",
			jsonDumpsOrEmptyObj(packload.ProfilesWireTable(channel.resolvedProfiles))))
		b.WriteString(exportPlain("YOLO_USE_PROFILES", jsonDumpsOrEmptyObj(channel.profiles)))
		keys := make([]string, 0, len(channel.packEnv))
		for k := range channel.packEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(exportPlain(k, channel.packEnv[k]))
		}
		for _, v := range channel.shapeVars {
			// Unset has no file spelling and no producer emits it today — the
			// old argv loop skipped it with the same silence.
			if v.Unset {
				continue
			}
			b.WriteString(exportPlain(v.Key, v.Value))
		}
	}
	_ = os.WriteFile(userEnvFile, []byte(b.String()), userEnvFileMode)
	_ = os.Chmod(userEnvFile, userEnvFileMode)
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
