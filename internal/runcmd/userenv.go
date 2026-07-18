package runcmd

import (
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// writeUserEnvFile ports run()'s yolo-user-env.sh writer (1997-2012) — a FROZEN
// cross-language contract (Go-writer / Python-reader round-trip; the reader is
// entrypoint._hydrate_env_from_user_env_file). When userEnv is non-empty it
// writes the two header comment lines then one
//
//	export K=${K:-'v'}
//
// line per entry (in userEnv order), with each value's single quotes escaped as
// '\” (the `'` → `'\”` replacement). An empty userEnv just ensures the file
// exists (touch) so the mount doesn't fail. Returns the file path.
func writeUserEnvFile(userEnvFile string, userEnv *jsonx.OrderedMap) {
	if userEnv == nil || userEnv.Len() == 0 {
		touchFile(userEnvFile)
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
	_ = os.WriteFile(userEnvFile, []byte(b.String()), 0o644)
}
