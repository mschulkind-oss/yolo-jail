// Package version provides version-string discovery and normalization. The
// normalization is a byte contract pinned by goldens; in particular a
// dirty-on-tag build normalizes to "0.1.0+dirty" (WITH the "+").
package version

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// buildVersion is stamped at build time via -ldflags -X (see
// scripts/build-go.sh, .goreleaser.yaml, the homebrew formula in
// .github/workflows/release.yml, and go-to-wheel's --set-version-var in
// publish.yml). It's the installed-wheel analog of setuptools-scm's baked
// version: when present it IS the binary's version (D18) — git describe is
// only consulted for unstamped `go build`/`go install` binaries.
var buildVersion = ""

// GitCommit is the short commit hash stamped into release builds via
// -ldflags -X (goreleaser's {{.ShortCommit}}, scripts/build-go.sh). Empty for
// unstamped builds. Not part of any user-facing byte contract yet — carried
// for release forensics until the post-cutover CLI surface pass surfaces it.
var GitCommit = ""

// Normalize converts a raw git-describe/env version string to the canonical
// form. Mirrors the tail of src/cli/version.py:_git_describe_version:
//
//	git format: 0.1.0-3-gabcdef1-dirty -> 0.1.0+3.gabcdef1.dirty
//	exactly on tag: 0.1.0 -> 0.1.0
//	dirty on tag: 0.1.0-dirty -> 0.1.0+dirty
//	leading "v" is stripped.
func Normalize(raw string) string {
	raw = strings.TrimPrefix(raw, "v")

	parts := strings.Split(raw, "-")

	dirty := false
	if len(parts) > 0 && parts[len(parts)-1] == "dirty" {
		dirty = true
		parts = parts[:len(parts)-1]
	}

	var commitHash, commitCount string
	if len(parts) >= 2 &&
		strings.HasPrefix(parts[len(parts)-1], "g") &&
		isDigits(parts[len(parts)-2]) {
		commitHash = parts[len(parts)-1]
		commitCount = parts[len(parts)-2]
		parts = parts[:len(parts)-2]
	}

	baseVersion := strings.Join(parts, "-")

	var suffixParts []string
	if commitCount != "" && commitHash != "" {
		suffixParts = append(suffixParts, commitCount, commitHash)
	}
	if dirty {
		suffixParts = append(suffixParts, "dirty")
	}

	if len(suffixParts) > 0 {
		return baseVersion + "+" + strings.Join(suffixParts, ".")
	}
	return baseVersion
}

// isDigits mirrors Python str.isdigit() for the ASCII case used here: true iff
// non-empty and every rune is a decimal digit. (Python's isdigit also accepts
// some unicode digit categories, but git commit counts are always ASCII.)
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// gitDescribe resolves the version: YOLO_VERSION env override wins and is
// returned VERBATIM (Python early-returns it before the normalization block —
// the host sets it so the in-jail banner matches the host exactly, so it must
// not be re-normalized); else the -ldflags baked version, normalized; else
// `git describe --tags --dirty --always` in repoRoot, normalized. Returns ""
// (the Go analog of Python None) when nothing resolves.
//
// Divergence D18 vs src/cli/version.py:_git_describe_version: Python asked
// git describe BEFORE the baked fallback. For a compiled binary that order is
// wrong twice over — `git describe --always` succeeds inside ANY git repo, so
// an installed (brew/pipx/goreleaser) binary run from a user's project would
// report THAT repo's describe; and a stale dist-go binary would report the
// live checkout's version instead of its own. The stamp, when present, is the
// binary's identity — the Go analog of Python reading the installed wheel's
// baked version.
func gitDescribe(repoRoot string) string {
	if raw := os.Getenv("YOLO_VERSION"); raw != "" {
		return raw // verbatim — matches Python's early return
	}

	// "unknown" guard: pre-D18 scripts/build-go.sh stamped the literal
	// string "unknown" when describe failed at build time; never let that
	// legacy stamp shadow a live describe.
	if buildVersion != "" && buildVersion != "unknown" {
		return Normalize(buildVersion)
	}

	// No stamp and no repo root: an unstamped `go build`/`go install` binary
	// run from who-knows-where. Do NOT fall through to `git describe` in the
	// process cwd — `--always` succeeds inside ANY git repository, so the
	// binary would report the version of whatever repo the user happens to
	// be standing in (reproduced: an unstamped yolo run inside a foreign
	// checkout tagged v5.2.0 reported 5.2.0+…). "unknown" (via Get) is the
	// honest answer — the §2d "sane default", swarf's "dev" analog.
	if repoRoot == "" {
		return ""
	}

	var raw string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "describe", "--tags", "--dirty", "--always")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err == nil {
		raw = strings.TrimSpace(string(out))
	}

	if raw == "" {
		return ""
	}
	return Normalize(raw)
}

// Get returns the yolo-jail version string. Mirrors _get_yolo_version: the
// git-describe result, or "unknown" if even the baked fallback is empty.
func Get(repoRoot string) string {
	v := gitDescribe(repoRoot)
	if v == "" {
		return "unknown"
	}
	return v
}
