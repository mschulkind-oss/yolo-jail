package wirebridged

// keyfile.go is the key channel (wire-bridge.md §5): the launcher writes the
// credential into a 0600 file from the hydrated env_sources, and the bridge reads
// that file ONCE at boot, then holds the value in memory. One writer, one
// reader; the daemon never appears in `ps` with the key. The daemon's own
// process environment is the fallback (§5: `yolo host`-style notches where the
// file may not exist), and no key means a healthy idle — never a request
// served upstream without the credential it was configured to carry.
//
// THE FILE IS THE SERVED AGENT'S OWN, since the credential gate
// (docs/design/provider-credential-scope.md, OQ-CN6): a provider credential the
// gate scopes to the agent that selected the provider no longer sits in the shared
// yolo-user-env.sh, it sits in that agent's env file (entrypoint.AgentEnvFile). A
// route is always served for one agent — the one whose profile selected the
// provider — so the bridge reads that agent's file first, and the shared file after
// it for a credential no provider claims. That is the §6 constraint the ruling kept:
// the bridge still reaches the key of every provider it serves.

import (
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// resolveKey finds the provider credential: the served agent's own env file
// first, then the shared yolo-user-env.sh, then this process's own environment.
// Returns the key and, for the startup log line, WHERE it came from — the source
// is safe to log, the value never is. An empty keyEnvName means the provider
// names no credential at all; that is not a miss but a "serve without
// Authorization" (the pre-flight's existence-only rule for a provider with no
// api_key_env_name), so the zero result is reserved for "a variable was named and
// is nowhere". An empty agent skips the agent file.
func resolveKey(keyEnvName, home, agent string) (key, source string) {
	if keyEnvName == "" {
		return "", ""
	}
	if agent != "" {
		path := entrypoint.AgentEnvFile(home, agent)
		if v, ok := keyFromUserEnvFile(path, keyEnvName); ok {
			return v, path
		}
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

// keyChannelDescription names the files resolveKey reads for agent, for a
// refusal that has to say where it looked.
func keyChannelDescription(home, agent string) string {
	if agent == "" {
		return userEnvFilePath(home)
	}
	return entrypoint.AgentEnvFile(home, agent) + " or " + userEnvFilePath(home)
}

// reportUnreadableKeyFile is the DEGRADATION NOTICE for the key channel: falling
// back from the file to the process environment is normal (a `yolo host` notch has
// no file), but falling back because the file is THERE AND UNREADABLE is a fault
// wearing the normal case's clothes. os.IsNotExist separates them, and only the
// second is worth a line — the daemon then either finds the variable in its own
// environment, and the serve line says "from process environment" while the
// operator believes the file is in play, or finds nothing and idles for a reason
// that names the file it never managed to open.
func reportUnreadableKeyFile(path string, err error) {
	if err == nil || os.IsNotExist(err) {
		return
	}
	logOnce("keyfile-unreadable:"+path, "the credential channel %s exists but could not be read "+
		"(%v); falling back to this process's own environment for the provider credential", path, err)
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
		reportUnreadableKeyFile(path, err)
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
