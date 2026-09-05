package wirebridged

// keyfile.go is the key channel (wire-bridge.md §5): the launcher writes
// yolo-user-env.sh (0600) from the hydrated env_sources, and the bridge reads
// that file ONCE at boot, then holds the value in memory. One writer, one
// reader; the daemon never appears in `ps` with the key. The daemon's own
// process environment is the fallback (§5: `yolo host`-style notches where the
// file may not exist), and no key means a healthy idle — never a request
// served upstream without the credential it was configured to carry.

import (
	"os"
	"strings"
)

// resolveKey finds the provider credential: the yolo-user-env.sh file first
// (the channel every non-claude agent already reads), then this process's own
// environment. Returns the key and, for the startup log line, WHERE it came
// from — the source is safe to log, the value never is. An empty keyEnvName
// means the provider names no credential at all; that is not a miss but a
// "serve without Authorization" (the pre-flight's existence-only rule for a
// provider with no api_key_env_name), so the zero result is reserved for "a
// variable was named and is nowhere".
func resolveKey(keyEnvName, home string) (key, source string) {
	if keyEnvName == "" {
		return "", ""
	}
	path := userEnvFilePath(home)
	if v, ok := keyFromUserEnvFile(path, keyEnvName); ok {
		return v, path
	}
	if v := os.Getenv(keyEnvName); v != "" {
		return v, "process environment"
	}
	return "", ""
}

// keyFromUserEnvFile reads one variable's value out of a yolo-user-env.sh-shaped
// file. The launcher's frozen write format is one `export K=${K:-'v'}` line per
// key (internal/cli/run's writeUserEnvFile, which the entrypoint and .bashrc
// read back), and the file's own header invites hand edits — so the two
// hand-editable spellings (`export K='v'`, `K=v`) are honored too. A variable
// that resolves to an EMPTY value is a miss, not a hit: an empty credential
// would go upstream as `Bearer ` and that is exactly the guess §5 forbids.
func keyFromUserEnvFile(path, name string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 || strings.TrimSpace(line[:eq]) != name {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		// ${K:-'v'} → the default half. ${K:-} is the unset spelling and
		// unwraps to empty, which the miss rule below handles.
		if strings.HasPrefix(val, "${") && strings.HasSuffix(val, "}") {
			inner := val[2 : len(val)-1]
			if i := strings.Index(inner, ":-"); i >= 0 {
				val = strings.TrimSpace(inner[i+2:])
			} else {
				val = ""
			}
		}
		// Single quotes, with the writer's '\'' escape undone.
		if len(val) >= 2 && strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'") {
			val = strings.ReplaceAll(val[1:len(val)-1], `'\''`, "'")
		}
		if val == "" {
			return "", false
		}
		return val, true
	}
	return "", false
}
