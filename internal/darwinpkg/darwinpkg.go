// Package darwinpkg provides NATIVE materialization of the config `packages:`
// for a notch with no baked image — macos-user today, Linux `guest` next
// (env-manager Phase 7.2). The nix argv builders, the YOLO_EXTRA_PACKAGES env
// contract, the buildEnv out-path → PATH/env derivation, and the flake.lock rev
// read are pure functions; materialize's actual nix invocation (streaming
// build) stays in the run wiring.
//
// The MECHANISM is platform-neutral even though the package name is not:
// `system` is a parameter that defaults to the RUNNING platform (NativeSystem),
// the flake attr it realizes is `yoloNoncontainerPackages`, and nixpkgs'
// availableOn is a per-system predicate — so the exact same code resolves
// x86_64-linux. Only the package's own name is still darwin-shaped, and
// renaming a Go package is a mechanical move left for the consumer that needs
// it (docs/design/noncontainer-nix-environment.md §8 Option 1).
package darwinpkg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Flake attribute contract — the two attrs flake.nix exposes for a
// non-container notch's tool closure.
const (
	ProfileAttr     = "yoloNoncontainerPackages" // packages.<system>.<attr> (buildEnv)
	UnavailableAttr = "yoloUnavailablePackages"  // <attr>.<system> -> [str]
)

// DarwinPackages is the result of materializing `packages:` natively.
type DarwinPackages struct {
	PathPrefix []string          // /nix/store/*/bin dirs
	Env        map[string]string // whitelisted non-PATH vars
	Skipped    []string          // names with no build for the target system
	// ProfilePath is the buildEnv store out path itself — the thing the GC root
	// pins and the thing `describe` / `check` report. Distinct from PathPrefix,
	// which is <ProfilePath>/bin: a caller that wants to name the closure must
	// not have to strip a suffix back off.
	ProfilePath string
}

// NativeSystem returns the nix system double for the RUNNING platform — the
// default target for a non-container notch, whose tool closure is by definition
// built for the machine the agent runs on.
//
// This replaces a `DarwinSystem = "aarch64-darwin"` constant, which was true of
// an Apple Silicon Mac and wrong of every other host — the same defect class as
// BACKLOG E8's hardcoded `aarch64-linux` in internal/containerbuilder, and it
// hid the same way: macos-user is macOS-only, so nobody noticed the constant
// also excluded an Intel Mac.
func NativeSystem() string { return nixSystem(runtime.GOOS, runtime.GOARCH) }

// nixSystem maps Go's GOOS/GOARCH to nix's `<arch>-<os>` double. Pure so every
// platform combination is unit-testable from one host, mirroring
// containerbuilder.nixLinuxSystem and check.machineForPlatform.
//
// An unrecognized GOARCH passes through verbatim rather than being defaulted:
// `riscv64-linux` is wrong in the same way a hardcoded constant is wrong, but
// it NAMES what it saw, and nix rejects an unknown system loudly where a
// plausible-but-wrong one resolves a package set for the wrong machine. GOOS
// passes through for the same reason — nix's own spelling for both platforms
// yolo supports matches Go's ("darwin", "linux").
func nixSystem(goos, goarch string) string {
	arch := goarch
	switch goarch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	return arch + "-" + goos
}

// nixFlags returns the flags shared by every nix invocation here:
//   - experimental-features so the CLI works regardless of the host's nix.conf;
//   - --accept-flake-config so nix honors THIS flake's own declared binary
//     cache (flake.nix nixConfig: yolo-jail.cachix.org). Without it nix prints
//     "ignoring untrusted flake configuration setting 'extra-substituters'" on
//     every run and never consults the cache — forcing a from-source native
//     build even when a cached closure exists. Trusting the project's own flake
//     config from the project's own build step is the happy path; it mutates no
//     system nix.conf (a trusted user still gates whether the substituter is
//     actually used).
func nixFlags() []string {
	return []string{
		"--extra-experimental-features", "nix-command flakes",
		"--accept-flake-config",
	}
}

// BuildEnv returns the environment for the nix invocations: the parent env plus
// the YOLO_EXTRA_PACKAGES contract (compact JSON, the flake reads it via
// builtins.getEnv — hence --impure). When packages is empty the var is removed.
// os.Environ()); the return is the same form, with YOLO_EXTRA_PACKAGES set or
// removed.
func BuildEnv(baseEnv []string, packages []any) ([]string, error) {
	out := make([]string, 0, len(baseEnv)+1)
	for _, kv := range baseEnv {
		if !strings.HasPrefix(kv, "YOLO_EXTRA_PACKAGES=") {
			out = append(out, kv)
		}
	}
	if len(packages) > 0 {
		s, err := jsonx.DumpsCompact(packages)
		if err != nil {
			return nil, err
		}
		out = append(out, "YOLO_EXTRA_PACKAGES="+s)
	}
	return out, nil
}

// BuildProfileArgv is the argv to realize the buildEnv profile and print its
// store out path. system "" defaults to NativeSystem().
//
// outLink, when non-empty, becomes `--out-link <outLink>` — which is BOTH the
// symlink and the GC ROOT (nix registers an indirect root under
// /nix/var/nix/gcroots/auto pointing back at it). That is the N1 fix, and the
// reason it is an out-link rather than a follow-up `nix-store --add-root` like
// image.RegisterImageRoot uses: the image path builds with `--no-link` and then
// roots the printed path in a second process, leaving a window in which a
// concurrent GC can collect a just-built closure. There is no such window here
// because nix creates the root as part of the build it is already running.
//
// An empty outLink keeps the historical `--no-link` — the UNROOTED build, which
// is what a caller that only wants the path (a diagnostic, a dry run) should
// ask for, stated rather than defaulted.
func BuildProfileArgv(system, outLink string) []string {
	if system == "" {
		system = NativeSystem()
	}
	argv := []string{"nix"}
	argv = append(argv, nixFlags()...)
	argv = append(argv, "build", "--impure")
	if outLink != "" {
		argv = append(argv, "--out-link", outLink)
	} else {
		argv = append(argv, "--no-link")
	}
	argv = append(argv,
		"--print-out-paths",
		"--print-build-logs",
		".#packages."+system+"."+ProfileAttr,
	)
	return argv
}

// UnavailableEvalArgv is the argv to read the no-build-for-this-system skip list
// as JSON. system "" defaults to NativeSystem().
func UnavailableEvalArgv(system string) []string {
	if system == "" {
		system = NativeSystem()
	}
	argv := []string{"nix"}
	argv = append(argv, nixFlags()...)
	argv = append(argv,
		"eval",
		"--impure",
		"--json",
		".#"+UnavailableAttr+"."+system,
	)
	return argv
}

// ProfilePaths derives (PATH prefix, non-PATH env) from the buildEnv store out
// path. The profile's bin is the only PATH entry contributed; if lib/pkgconfig
// exists, PKG_CONFIG_PATH is exposed.
// results). checkPkgConfigDir reports whether <out>/lib/pkgconfig is a dir; pass
// nil to use the real filesystem.
func ProfilePaths(outPath string, checkPkgConfigDir func(string) bool) ([]string, map[string]string) {
	out := strings.TrimSpace(outPath)
	if out == "" {
		return nil, map[string]string{}
	}
	pathPrefix := []string{out + "/bin"}
	env := map[string]string{}
	pc := filepath.Join(out, "lib", "pkgconfig")
	isDir := checkPkgConfigDir
	if isDir == nil {
		isDir = func(p string) bool {
			info, err := os.Stat(p)
			return err == nil && info.IsDir()
		}
	}
	if isDir(pc) {
		env["PKG_CONFIG_PATH"] = pc
	}
	return pathPrefix, env
}

// LockedNixpkgsRev returns the pinned nixpkgs rev from flake.lock (diagnostics/
// dry-run only).
// ["rev"]. Errors (missing file, bad JSON, missing keys) surface to the caller.
func LockedNixpkgsRev(flakeLock string) (string, error) {
	data, err := os.ReadFile(flakeLock)
	if err != nil {
		return "", err
	}
	var doc struct {
		Nodes struct {
			Nixpkgs struct {
				Locked struct {
					Rev string `json:"rev"`
				} `json:"locked"`
			} `json:"nixpkgs"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	return doc.Nodes.Nixpkgs.Locked.Rev, nil
}

// ParseSkippedNames parses the `nix eval --json` skip-list stdout into a []string,
// mirroring _skipped_names's JSON handling: a JSON array → its elements as
// strings; anything else (or a decode error) → nil. The subprocess/timeout
// wrapper lives in the run path; this is the pure output handling.
func ParseSkippedNames(stdout string) []string {
	var val any
	if err := json.Unmarshal([]byte(stdout), &val); err != nil {
		return nil
	}
	arr, ok := val.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		out = append(out, pyStr(x))
	}
	return out
}

// pyStr renders a JSON-decoded scalar the way Python's str(x) does for the
// values that appear in the skip list (strings verbatim). Non-strings are
// unusual here; format them plainly.
func pyStr(x any) string {
	if s, ok := x.(string); ok {
		return s
	}
	b, _ := json.Marshal(x)
	return string(b)
}
