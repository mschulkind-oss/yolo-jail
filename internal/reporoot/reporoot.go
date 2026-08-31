// Package reporoot locates the yolo-jail repo root (a directory containing
// flake.nix) for nix image builds. It is THE single resolution method shared by
// `yolo run` (internal/cli/run) and `yolo check` (internal/cli/check), so both
// agree on where the repo is — and it resolves identically inside and outside
// the jail, and identically in EVERY directory.
//
// # Resolution does not read the cwd (2026-08-31)
//
// It used to. A walk up from the working directory for a dir holding BOTH
// flake.nix and go.mod outranked every bundle, so `yolo` launched inside a
// yolo-jail checkout built the image from that live tree while the same `yolo`
// launched anywhere else built it from the snapshot the last `just install`
// staged. One binary, one config, two different sources for the multi-gigabyte
// artifact it boots — selected by nothing the command line showed.
//
// It was also the only way the two halves of yolo could disagree. Both bundles
// ship WITH the binary, so neither can be older than it; a live checkout moves
// with every commit, and the launcher only moves at `just install`. That gap is
// what version.SourceSkew exists to refuse, and the cwd-walk was the thing that
// opened it by default for anyone standing in the repo.
//
// Removing the walk makes yolo behave the same in every directory, and makes
// skew unreachable unless a live tree is asked for BY NAME. Asking is
// YOLO_REPO_ROOT (step 1) — the same override CI and the integration harness
// already set, now the only way to point yolo at source it was not built from.
// Nothing infers it: an in-jail agent verifying a Go or flake change against the
// live /workspace checkout sets `YOLO_REPO_ROOT=/workspace` and can see, in the
// launch's own "Image source:" line, that it took.
//
// The old user-config `repo_path` fallback was retired earlier (2026-07-23), and
// is not coming back for the same reason: a config pointer drifts silently.
package reporoot

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Source names WHICH candidate produced a root. It exists so a launch can REPORT
// where its image source came from rather than leaving the reader to infer it
// from a path — the reporting half of the cwd removal above. Callers render it
// with Describe.
type Source string

const (
	// FromEnv: the YOLO_REPO_ROOT override. The ONLY source that can be a live
	// checkout, and therefore the only one version.SourceSkew can fire on.
	FromEnv Source = "env"
	// FromBesideBinary: a share/yolo-jail bundle shipped next to the executable —
	// Homebrew, the release archive, and the jail's baked /opt/yolo-jail prefix.
	FromBesideBinary Source = "beside-binary"
	// FromInstalledBundle: the bundle a from-source `just install` staged under
	// paths.FlakeBundleDir, which is what makes such an install self-contained.
	FromInstalledBundle Source = "installed-bundle"
)

// Describe renders a Source as the human phrase a launch prints in parentheses
// after the path.
func (s Source) Describe() string {
	switch s {
	case FromEnv:
		return "YOLO_REPO_ROOT"
	case FromBesideBinary:
		return "flake bundle beside the binary"
	case FromInstalledBundle:
		return "flake bundle staged by `just install`"
	}
	return string(s)
}

// Resolution is a located repo root together with what located it.
type Resolution struct {
	Root   string
	Source Source
}

// Resolve locates the repo root. Returns (resolution, ok); ok=false means it
// could not be located (the caller prints its own actionable message). Resolve is
// PURE — no filesystem writes, no staging, and it never reads the working
// directory (see the package doc). Resolution order:
//
//  1. YOLO_REPO_ROOT env, validated to actually contain source (flake.nix OR
//     go.mod). The explicit override: CI, the integration harness, and any
//     developer or agent who wants the image built from a live checkout.
//  2. Exe-relative bundle: a share/yolo-jail/ tree shipped beside the binary.
//     This one candidate list serves the checkout-less channels — Homebrew /
//     release-archive installs and the in-jail baked /opt/yolo-jail prefix — with
//     one method and one set of paths.
//  3. State-dir bundle: paths.FlakeBundleDir (GlobalStorage/flake-bundle), what a
//     from-source `just install` stages so the install is SELF-CONTAINED —
//     resolvable from any cwd. Last because a real distribution bundle (step 2)
//     ships with the binary that is running.
func Resolve(getenv func(string) string) (Resolution, bool) {
	// 1. Env override, validated for source.
	if env := getenv("YOLO_REPO_ROOT"); env != "" {
		if fileExists(filepath.Join(env, "flake.nix")) ||
			fileExists(filepath.Join(env, "go.mod")) {
			return Resolution{Root: absOr(env), Source: FromEnv}, true
		}
	}

	// 2. Exe-relative bundle (Homebrew / release archive / baked /opt prefix).
	if bundle, ok := BundledSourceDir(); ok {
		return Resolution{Root: bundle, Source: FromBesideBinary}, true
	}

	// 3. Staged bundle under yolo's own state dir. It is a fixed leaf under
	//    GlobalStorage, which can never equal GlobalStorage itself (see
	//    paths.FlakeBundleDir).
	if bundle := paths.FlakeBundleDir(); fileExists(filepath.Join(bundle, "flake.nix")) {
		return Resolution{Root: absOr(bundle), Source: FromInstalledBundle}, true
	}

	return Resolution{}, false
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
