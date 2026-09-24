package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// HostNix is the host's nix CLIENT as the sandbox can use it: the store bin dir that goes on
// the sandbox PATH, or the reason nothing does.
//
// WHY THE HOST'S CLIENT AND NOT A nix IN THE DARWIN FLOOR (internal/darwinpkg/floor.go).
// The sandbox user is not root and the store is not its to write, so every nix it runs is a
// daemon client — and the daemon it talks to is the host's. A floor nix is whatever version
// yolo's flake pins, which drifts from the daemon on every host upgrade and every flake bump.
// The client this resolves is the one the launcher ITSELF just ran to build the floor (the
// materializer execs `nix` off the launching user's PATH, darwinpkg.buildProfileArgv), so it
// is by construction a client that talked to this daemon successfully a moment ago.
//
// WHY THE RESOLVED STORE DIR AND NOT THE PROFILE DIR IT WAS FOUND THROUGH. `nix` is found as
// /nix/var/nix/profiles/default/bin/nix on a stock multi-user install,
// /run/current-system/sw/bin/nix under nix-darwin, or ~/.nix-profile/bin/nix, and every one of
// those is a symlink into /nix/store/<hash>-nix-<version>/bin. The profile dirs hold whatever
// else was installed into that profile — and the last one is inside the invoking user's home,
// which the Seatbelt profile denies (`(deny file-read* (subpath "/Users"))`). The store dir
// holds only the nix package's own binaries, is immutable, and needs no new profile allowance:
// the sandbox already reads /nix/store, because the floor it runs lives there.
//
// Measured on hardware 2026-09-16 (docs/plans/setup-support-gaps.md §5.1 rows 10-12): inside
// the shipped profile shape connect(2) to the daemon socket works and `nix build
// nixpkgs#hello` returns 0. The only break was that no `nix` was on the sandbox's PATH.
type HostNix struct {
	// BinDir is the store bin dir holding the host's nix client, "" when none is delivered.
	BinDir string
	// Absent says why BinDir is "", in words the launch prints. "" when BinDir is set.
	Absent string
}

// hostNixDaemonSocket is the multi-user daemon's socket. Its existence AS A SOCKET is the
// test for "a daemon exists for the sandbox user to be a client of".
const hostNixDaemonSocket = "/nix/var/nix/daemon-socket/socket"

// hostNixStoreDir is the store the resolved client must sit under — the one nix tree the
// sandbox is known to read (the floor runs from it) and cannot write.
const hostNixStoreDir = "/nix/store"

// hostNixEnv is what a daemon client in the sandbox needs to work with no flags.
//
//   - NIX_REMOTE=daemon: the sandbox user is not root and cannot write the store, so `auto`
//     would pick the daemon anyway — spelled so a local-store attempt can never be the answer
//     that surfaces as an obscure permission error under Seatbelt.
//   - NIX_CONFIG enabling nix-command + flakes, with the EXTRA- prefix so it ADDS to whatever
//     the host's /etc/nix/nix.conf enables (the sandbox reads that file; reads outside /Users
//     and /Volumes are allowed) rather than replacing it. The container image got
//     /etc/nix/nix.conf for the same reason (commit 6d3ded83); this backend has no image to put
//     a file in, and the sandbox home is shared across workspaces, so an env var is the
//     narrowest carrier.
var hostNixEnv = [][2]string{
	{"NIX_REMOTE", "daemon"},
	{"NIX_CONFIG", "extra-experimental-features = nix-command flakes"},
}

// resolveHostNix is the pure decision: the store dir and socket path are parameters, and its
// three probes are injected, so every arm runs on Linux.
func resolveHostNix(storeDir, sockPath string, lookPath func(string) (string, error),
	evalSymlinks func(string) (string, error), isSocket func(string) bool) HostNix {
	found, err := lookPath("nix")
	if err != nil || found == "" {
		return HostNix{Absent: "no `nix` on the launching user's PATH"}
	}
	real, err := evalSymlinks(found)
	if err != nil {
		return HostNix{Absent: "`" + found + "` could not be resolved (" + err.Error() + ")"}
	}
	// Under /nix/store or not at all: that is the one tree the sandbox is known to read and
	// cannot write, and a client anywhere else may sit under /Users, which it cannot read.
	if !strings.HasPrefix(real, storeDir+"/") {
		return HostNix{Absent: "`" + found + "` resolves to " + real +
			", outside " + storeDir + ", which is the only nix location the sandbox is known to read"}
	}
	// A SINGLE-USER install has no daemon: its store belongs to the launching user, which the
	// sandbox account is not, and Seatbelt denies it writes there anyway. A nix on PATH there
	// would fail on its first store operation, so none is delivered.
	if !isSocket(sockPath) {
		return HostNix{Absent: "no nix daemon socket at " + sockPath +
			" (a single-user install?) — the sandbox account cannot use a store it does not own"}
	}
	return HostNix{BinDir: filepath.Dir(real)}
}

// hostNixProbe is resolveHostNix with the REAL probes — exec.LookPath, filepath.EvalSymlinks,
// isUnixSocket — and only the two locations left as parameters, so a test can point it at a
// temp store and a real socket and exercise the production symlink resolution on Linux.
func hostNixProbe(storeDir, sockPath string) HostNix {
	return resolveHostNix(storeDir, sockPath, exec.LookPath, filepath.EvalSymlinks, isUnixSocket)
}

// hostNixReal is the production probe behind Deps.HostNix.
func hostNixReal() HostNix { return hostNixProbe(hostNixStoreDir, hostNixDaemonSocket) }

// isUnixSocket reports whether p exists and is a socket — a regular file or a dangling path
// at the daemon's location is not a daemon.
func isUnixSocket(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// withHostNixEnv returns env plus hostNixEnv, each a DEFAULT rather than an override — the
// same rule MISE_TRUSTED_CONFIG_PATHS follows (buildPlan):
//
//   - a user's own NIX_REMOTE (env_sources, the sandbox env) wins whole;
//   - a user's own NIX_CONFIG is KEPT and, only when none of its lines sets
//     experimental-features or extra-experimental-features, gets yolo's line appended after
//     a newline. NIX_CONFIG is newline-separated nix.conf text, and the session env file
//     carries a newline intact: it is SOURCED by /bin/sh (sandboxEnvReader) and exportLine
//     single-quotes the value, escaping only `'`. The appended line starts `extra-`, not
//     `export `, so SandboxEnvFileKeys still lists NIX_CONFIG once. A user who names the
//     features at all — including to turn them OFF — has decided, and is left alone.
func withHostNixEnv(env *jsonx.OrderedMap) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	if env != nil {
		for _, k := range env.Keys() {
			v, _ := env.Get(k)
			out.Set(k, v)
		}
	}
	for _, kv := range hostNixEnv {
		cur, ok := out.Get(kv[0])
		if !ok {
			out.Set(kv[0], kv[1])
			continue
		}
		if kv[0] != "NIX_CONFIG" {
			continue
		}
		user, isStr := cur.(string)
		if !isStr || nixConfigNamesFeatures(user) {
			continue
		}
		if user != "" && !strings.HasSuffix(user, "\n") {
			user += "\n"
		}
		out.Set(kv[0], user+kv[1])
	}
	return out
}

// nixConfigNamesFeatures reports whether nix.conf text sets experimental-features in either
// spelling. Per line, the key being what precedes `=`, trimmed — nix.conf's own syntax.
func nixConfigNamesFeatures(conf string) bool {
	for _, line := range strings.Split(conf, "\n") {
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "experimental-features", "extra-experimental-features":
			return true
		}
	}
	return false
}
