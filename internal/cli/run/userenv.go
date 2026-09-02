package run

import (
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// writeUserEnvFile writes yolo-user-env.sh. Frozen contract (must not drift —
// the in-jail entrypoint reads this file back and depends on the exact format).
// When userEnv is non-empty it writes the two header comment lines then one
//
//	export K=${K:-'v'}
//
// line per entry (in userEnv order), with each value's single quotes escaped as
// '\” (the `'` → `'\”` replacement).
//
// An empty userEnv TRUNCATES to zero bytes, leaving the file in place so the
// bind mount still has a source (podman refuses to start on a missing one). It
// must not merely touch: dropping env_sources from config yields an empty map,
// and a no-op on an existing path left the previous launch's render mounted —
// so commented-out credentials kept being exported, rebuild after rebuild, via
// hydrateEnvFromUserEnvFile and .bashrc alike. Removing a key from config has to
// revoke it. Returns the file path.
//
// MODE 0600, NOT 0644. This file holds every hydrated env_sources VALUE in
// plaintext — API keys, in practice — and it was world-readable until 2026-09-01
// (measured in a live jail: `-rw-r--r--` holding two provider keys). packs/zai's
// README tells the user to keep that key in a file that is "untracked, 0600", and
// yolo's own copy of the value was downgrading the mode the user had chosen.
//
// 0600 is safe for every reader this file HAS, which is worth stating because a
// mode tightening that breaks a reader is worse than the exposure: in a container
// the entrypoint (boot.go) and the generated .bashrc both read it as the jail's own
// uid, and the file is owned by that same uid; on the macos-user backend it is read
// by the invoking user, who wrote it. Nothing reads it as a non-owner. Both the
// empty and non-empty paths carry the mode, because a truncation that widened the
// mode back to 0644 would undo this on the next launch that drops env_sources.
//
// os.WriteFile applies the mode only when CREATING; an existing file keeps its own.
// So the chmod is explicit — a file created 0644 by an older yolo must be narrowed
// in place, and every launch is the migration.
func writeUserEnvFile(userEnvFile string, userEnv *jsonx.OrderedMap) {
	if userEnv == nil || userEnv.Len() == 0 {
		_ = os.WriteFile(userEnvFile, nil, userEnvFileMode)
		_ = os.Chmod(userEnvFile, userEnvFileMode)
		return
	}
	var b strings.Builder
	b.WriteString("# Auto-generated from yolo-jail.jsonc env config.\n")
	b.WriteString("# Override by editing this file or workspace .env (mise).\n")
	for _, k := range userEnv.Keys() {
		v, _ := userEnv.Get(k)
		val, _ := v.(string)
		escaped := strings.ReplaceAll(val, "'", `'\''`)
		b.WriteString("export " + k + "=${" + k + ":-'" + escaped + "'}\n")
	}
	_ = os.WriteFile(userEnvFile, []byte(b.String()), userEnvFileMode)
	_ = os.Chmod(userEnvFile, userEnvFileMode)
}

// userEnvFileMode is owner-only: the file holds hydrated secrets in plaintext.
// See writeUserEnvFile's doc for why every reader is the owner.
const userEnvFileMode = 0o600
