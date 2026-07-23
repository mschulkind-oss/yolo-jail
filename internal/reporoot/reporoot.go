// Package reporoot locates the yolo-jail repo root (a directory containing
// flake.nix) for nix image builds. It is THE single resolution method shared by
// `yolo run` (internal/cli/run) and `yolo check` (internal/cli/check), so both
// agree on where the repo is — and, critically, it resolves identically inside
// and outside the jail. There is no in-jail-special path any more: the baked
// image ships the CLI as a real file under /opt/yolo-jail/bin with the flake
// bundle at /opt/yolo-jail/share/yolo-jail, so os.Executable()-relative
// discovery (Resolve step 3) finds the bundle exactly the way a Homebrew /
// release-archive install does on the host.
//
// The old user-config `repo_path` fallback was retired (2026-07-23). A
// from-source developer resolves their LIVE checkout via the cwd-walk (step 2)
// when launching from inside it, or via YOLO_REPO_ROOT (step 1) from anywhere —
// both point nix at the real source tree, which is what a source install wants.
// (A staged prebuilt bundle would be the wrong answer there: it carries
// prebuilt binaries, not goSrc, so it would build jails from stale artifacts
// instead of the developer's edits.) Retiring the key leaves ONE resolution
// method, identical inside and outside the jail, with no config pointer to
// drift.
package reporoot

import (
	"os"
	"path/filepath"
)

// Resolve locates the repo root. Returns (path, ok); ok=false means it could not
// be located (the caller prints its own actionable message). Resolve is PURE —
// no filesystem writes, no staging. Resolution order:
//
//  1. YOLO_REPO_ROOT env (CI + explicit override), validated to actually contain
//     source (flake.nix OR go.mod). Not the in-jail mechanism any more — just an
//     override that CI and the integration harness still set.
//  2. Source-checkout detection: walk up from cwd for a dir with BOTH flake.nix
//     AND go.mod. A bare-flake.nix match would hijack a user's own flake
//     workspace, so both are required. This is the host-dev + nested-dev-jail
//     path (the live-mounted /workspace checkout wins here).
//  3. Exe-relative bundle: a share/yolo-jail/ tree shipped beside the binary.
//     This one candidate list serves the checkout-less channels — Homebrew /
//     release-archive installs and the in-jail baked /opt/yolo-jail prefix —
//     with one method and one set of paths.
//
// There is deliberately NO user-config `repo_path` fallback: it was retired
// (2026-07-23). A from-source developer resolves their live checkout via step 2
// (launch from inside it) or step 1 (YOLO_REPO_ROOT), so the config pointer was
// redundant. Exactly one resolution method now, inside or outside the jail.
func Resolve(getenv func(string) string) (string, bool) {
	// 1. Env override, validated for source.
	if env := getenv("YOLO_REPO_ROOT"); env != "" {
		if fileExists(filepath.Join(env, "flake.nix")) ||
			fileExists(filepath.Join(env, "go.mod")) {
			return absOr(env), true
		}
	}

	// 2. Source checkout: walk up from cwd requiring BOTH flake.nix AND go.mod.
	if dir, err := os.Getwd(); err == nil {
		for {
			if fileExists(filepath.Join(dir, "flake.nix")) &&
				fileExists(filepath.Join(dir, "go.mod")) {
				return absOr(dir), true
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// 3. Exe-relative bundle (every install channel).
	if bundle, ok := BundledSourceDir(); ok {
		return bundle, true
	}

	return "", false
}

// BundledSourceDir discovers a flake bundle shipped alongside the executable.
// The bundle is share/yolo-jail/ holding flake.nix + flake.lock + prebuilt
// binaries under bin/linux-<arch>/. Returns (dir, true) only when dir/flake.nix
// exists; in a source checkout or a bare `go install` binary it returns
// ("", false).
func BundledSourceDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	return BundledSourceDirFrom(filepath.Dir(exe))
}

// BundledSourceDirFrom is the pure core of BundledSourceDir, taking the
// executable's directory explicitly so it is unit-testable without an installed
// binary. Candidate order, all variants of ONE method (exe-relative):
//   - <exeDir>/../share/yolo-jail — Homebrew (bin/yolo + prefix/share/yolo-jail),
//     AND the in-jail baked prefix (/opt/yolo-jail/bin + /opt/yolo-jail/share).
//   - <exeDir>/share/yolo-jail    — release archive (yolo + share/ at one level).
//   - <exeDir>                    — bundle unpacked directly beside the binary.
func BundledSourceDirFrom(exeDir string) (string, bool) {
	for _, cand := range []string{
		filepath.Join(exeDir, "..", "share", "yolo-jail"),
		filepath.Join(exeDir, "share", "yolo-jail"),
		exeDir,
	} {
		if fileExists(filepath.Join(cand, "flake.nix")) {
			return absOr(cand), true
		}
	}
	return "", false
}

// --- small filesystem helpers ---

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func absOr(p string) string {
	if r, err := filepath.Abs(p); err == nil {
		return r
	}
	return p
}
