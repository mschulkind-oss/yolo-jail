// Package darwinpkg provides native aarch64-darwin materialization of the
// config `packages:` for the macos-user backend. The nix argv builders, the
// YOLO_EXTRA_PACKAGES env contract, the buildEnv out-path → PATH/env
// derivation, and the flake.lock rev read are pure functions; materialize's
// actual nix invocation (streaming build) stays in the run wiring.
package darwinpkg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Frozen attribute contract (byte-identical to darwin_packages.py).
const (
	DarwinSystem    = "aarch64-darwin"
	ProfileAttr     = "yoloDarwinPackages"        // packages.<system>.yoloDarwinPackages (buildEnv)
	UnavailableAttr = "darwinUnavailablePackages" // <attr>.<system> -> [str]
)

// DarwinPackages is the result of materializing `packages:` natively on darwin.
type DarwinPackages struct {
	PathPrefix []string          // /nix/store/*/bin dirs
	Env        map[string]string // whitelisted non-PATH vars
	Skipped    []string          // names with no darwin build
}

// nixFlags returns the experimental-features flags so the CLI works regardless
// of the host's nix.conf.
func nixFlags() []string {
	return []string{"--extra-experimental-features", "nix-command flakes"}
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

// BuildProfileArgv is the argv to realize the darwin buildEnv profile and print
// its store out path.
func BuildProfileArgv(system string) []string {
	if system == "" {
		system = DarwinSystem
	}
	argv := []string{"nix"}
	argv = append(argv, nixFlags()...)
	argv = append(argv,
		"build",
		"--impure",
		"--no-link",
		"--print-out-paths",
		"--print-build-logs",
		".#packages."+system+"."+ProfileAttr,
	)
	return argv
}

// UnavailableEvalArgv is the argv to read the no-darwin-build skip list as JSON.
func UnavailableEvalArgv(system string) []string {
	if system == "" {
		system = DarwinSystem
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
