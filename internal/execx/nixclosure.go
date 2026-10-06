package execx

import (
	"os"
	"os/exec"
	"strings"
)

// LoaderOverrideVars are the dynamic-loader variables a Nix store closure is run
// without: LD_LIBRARY_PATH, searched BEFORE a binary's RUNPATH, and LD_PRELOAD,
// loaded into every process.
//
// A Nix-built binary names every library it needs by store path in its RUNPATH,
// so it never needs the caller's LD_LIBRARY_PATH — and inheriting one can only
// substitute a library it was not built against. That is not theoretical: after
// the 2026-10 flake.lock update the image copier links glibc 2.44, and a jail's
// baked LD_LIBRARY_PATH=/lib:/usr/lib:… (the jail image's own glibc 2.42) made
// the loader pick the older libc and the copier abort at startup with
// "*** stack smashing detected ***", so every image delivery failed. The same
// happens on any host whose user exports either variable.
// docs/reference/image-staging-vs-baking.md#delivering-into-the-runtime (LI-D1).
var LoaderOverrideVars = []string{"LD_LIBRARY_PATH", "LD_PRELOAD"}

// NixClosureEnv is environ with every LoaderOverrideVars entry removed, and
// nothing else changed. The input is not mutated.
func NixClosureEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if isLoaderOverride(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func isLoaderOverride(name string) bool {
	for _, v := range LoaderOverrideVars {
		if name == v {
			return true
		}
	}
	return false
}

// NixClosureCommand is exec.Command for a binary yolo built or resolved from the
// Nix store itself (the image copier is the one today), run in this process's
// environment minus LoaderOverrideVars.
//
// It is THE way such a binary is started, so an exec site cannot forget the
// scrub. It is not for tools the user supplied — their podman, nix itself — which
// may genuinely need the caller's loader settings. When argv[0] is a wrapper that
// passes its environment to the closure it runs (`podman unshare -- <copier>`),
// the closure's environment IS the wrapper's, so the wrapped exec is a closure
// exec and goes through here too.
func NixClosureCommand(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	cmd.Env = NixClosureEnv(os.Environ())
	return cmd
}
