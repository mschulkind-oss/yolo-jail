package entrypoint

import (
	"os"
	"path/filepath"
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

// callerTokenDir is paths.JailCallerTokenDir, a variable so a test can point it at a temp dir.
var callerTokenDir = paths.JailCallerTokenDir

// writeCallerTokenFiles writes every well-formed caller token this launch was handed to its own
// 0600 file under callerTokenDir (paths.JailCallerTokenFile), for a client that reads its
// credential from a file: the AWS SDKs read AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE, which the
// aws-auth pack names beside its credentials URI (docs/plans/notch-convergence.md §2.3). The
// bytes are the token alone, with no newline, because an SDK sends the file's contents verbatim.
//
// Re-run by every entry's boot, and an attach carries the running jail's token, so a rewrite
// writes the same bytes. A failure is a warning, not a refusal: the daemon still demands the
// token, so the client that cannot read it is refused 401 naming yolo, and nothing is served
// unauthenticated.
func writeCallerTokenFiles(e *Env) {
	var keys []string
	for k, v := range e.Vars {
		if paths.IsServiceCallerTokenEnv(k) && svcendpoint.IsToken(v) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	if err := os.MkdirAll(callerTokenDir, 0o700); err != nil {
		e.warn("yolo: could not create " + callerTokenDir + " for the caller-token files: " + err.Error())
		return
	}
	for _, k := range keys {
		if err := writePrivateFileAtomic(filepath.Join(callerTokenDir, k), []byte(e.Vars[k])); err != nil {
			e.warn("yolo: could not write the caller-token file for $" + k + ": " + err.Error() +
				" — a client that reads it will be refused")
		}
	}
}

// writePrivateFileAtomic replaces path with data, mode 0600, through a temp file in the same
// directory, so a client never reads a half-written token.
func writePrivateFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
