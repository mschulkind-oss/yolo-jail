// Package naming mirrors cli.runtime.container_name_for_workspace — the
// deterministic container name derived from a workspace path. The name is a
// frozen interop contract (both the Python and Go CLIs must compute the same
// name for the same workspace, or a mixed-era invocation can't find the
// other's container), so the sanitize+hash algorithm is pinned by the drift
// suite.
//
// Source of truth: src/cli/runtime.py:container_name_for_workspace.
package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

// Python: re.sub(r"[^a-z0-9-]", "-", name.lower()) — applied AFTER lowercasing,
// so uppercase ASCII is first folded to lowercase then (already lowercase)
// kept. Any rune that is not a lowercase ASCII letter, digit, or hyphen —
// including every non-ASCII rune — becomes a single "-".
var sanitizeRe = regexp.MustCompile(`[^a-z0-9-]`)

// FromResolved computes the container name from an ALREADY-RESOLVED absolute
// path. This is the pure, host-independent core the drift suite pins. Callers
// that start from an unresolved path use FromWorkspace, which resolves first.
//
// Faithful to Python:
//   - name = basename (empty for "/")
//   - safe = sub([^a-z0-9-] -> "-", lower(name)), then strip leading/trailing
//     "-", then truncate to the first 40 *runes* (Python str slicing is by
//     code point, not byte)
//   - safe = "jail" if empty
//   - hash = sha256(resolved)[:8] hex
//   - result = "yolo-<safe>-<hash>"
func FromResolved(resolved string) string {
	var name string
	if resolved != "/" {
		trimmed := strings.TrimRight(resolved, "/")
		if i := strings.LastIndex(trimmed, "/"); i >= 0 {
			name = trimmed[i+1:]
		} else {
			name = trimmed
		}
	}

	safe := sanitizeRe.ReplaceAllString(strings.ToLower(name), "-")
	safe = strings.Trim(safe, "-")
	safe = truncateRunes(safe, 40)
	if safe == "" {
		safe = "jail"
	}

	sum := sha256.Sum256([]byte(resolved))
	h := hex.EncodeToString(sum[:])[:8]
	return "yolo-" + safe + "-" + h
}

// FromWorkspace resolves symlinks + makes the path absolute (Python's
// Path.resolve()) then computes the name. Symlink resolution is host-dependent,
// so the resolve step is covered by tests against real temp dirs, while the
// sanitize+hash algorithm is pinned host-independently via FromResolved.
func FromWorkspace(workspace string) string {
	resolved, err := filepath.Abs(workspace)
	if err != nil {
		resolved = workspace
	}
	// filepath.EvalSymlinks errors on non-existent paths; Python's resolve()
	// with strict=False (the default) does not. Best-effort: if EvalSymlinks
	// succeeds use it, else fall back to the lexical abs path.
	if evaled, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = evaled
	}
	return FromResolved(resolved)
}

// truncateRunes returns the first n runes of s (Python str[:n] semantics),
// not the first n bytes. NOTE: Python's .lower() and Go's strings.ToLower may
// disagree on some non-ASCII runes, but after sanitization every non-[a-z0-9-]
// rune (which is where any casing disagreement would live) has already become
// "-", so the two agree on the sanitized output. See the drift-suite corpus,
// which includes accented input, for the pin.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
