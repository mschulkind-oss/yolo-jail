package entrypoint

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// noteServiceCallerAuth writes one boot.log line per pack service this launch handed a
// CALLER TOKEN (paths.ServiceCallerTokenEnv; coined in docs/reference/wire-bridge.md, WB-D18):
// that service refuses every request that does not carry it, so a 401 from one later in the
// session reads against this record. Log-only (e.note), because it is the positive record of a
// healthy launch rather than something the terminal needs; the variable's NAME only, never its
// value, which is a credential.
//
// A malformed value is reported too, since the daemon will refuse to serve behind it: that is
// a launcher and an entrypoint disagreeing about the token's shape.
func noteServiceCallerAuth(e *Env) {
	var keys []string
	for k := range e.Vars {
		if paths.IsServiceCallerTokenEnv(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		slug := strings.TrimSuffix(strings.TrimPrefix(k, paths.ServiceEnvVarPrefix), paths.ServiceCallerTokenSuffix)
		service := strings.ToLower(strings.ReplaceAll(slug, "_", "-"))
		if svcendpoint.IsToken(e.Vars[k]) {
			e.note("yolo: " + service + " requires caller auth: every request must carry this launch's " +
				"token ($" + k + ", per launch), and is refused 401 without it (wire-bridge.md WB-D18)")
			continue
		}
		e.note("yolo: " + service + " was handed a malformed caller token ($" + k + "), so it will " +
			"serve nothing (wire-bridge.md WB-D18)")
	}
}
