// Package version mirrors src/cli/version.py's version-string discovery and
// normalization. The normalization is a byte contract pinned by the drift
// suite and goldens; in particular a dirty-on-tag build normalizes to
// "0.1.0+dirty" (WITH the "+" — a stale Python code comment once claimed
// "0.1.0.dirty" and poisoned rev 1 of the port plan; goldens are generated
// from observed Python output, never comments).
//
// Source of truth: src/cli/version.py.
package version

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// buildVersion is stamped at build time via -ldflags -X (see scripts/build-go.sh).
// It's the installed-wheel analog of setuptools-scm's baked version and the
// last fallback when git describe is unavailable.
var buildVersion = ""

// Normalize converts a raw git-describe/env version string to the canonical
// form. Mirrors the tail of src/cli/version.py:_git_describe_version:
//
//	git format: 0.1.0-3-gabcdef1-dirty -> 0.1.0+3.gabcdef1.dirty
//	exactly on tag: 0.1.0              -> 0.1.0
//	dirty on tag:   0.1.0-dirty        -> 0.1.0+dirty
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

// gitDescribe mirrors src/cli/version.py:_git_describe_version's discovery
// order: YOLO_VERSION env override wins and is returned VERBATIM (Python
// early-returns it before the normalization block — the host sets it so the
// in-jail banner matches the host exactly, so it must not be re-normalized);
// else `git describe --tags --dirty --always` in repoRoot, normalized; else
// the -ldflags baked version, normalized. Returns "" (the Go analog of
// Python None) when nothing resolves.
func gitDescribe(repoRoot string) string {
	if raw := os.Getenv("YOLO_VERSION"); raw != "" {
		return raw // verbatim — matches Python's early return
	}

	var raw string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "describe", "--tags", "--dirty", "--always")
	if repoRoot != "" {
		cmd.Dir = repoRoot
	}
	out, err := cmd.Output()
	if err == nil {
		raw = strings.TrimSpace(string(out))
	}

	if raw == "" {
		raw = buildVersion
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
