// Package repopath writes the yolo-jail source-checkout path into the user
// config's `repo_path` key. `just deploy` invokes this (via `yolo internal
// write-repo-path`) so an installed `yolo` can locate the repo for nix image
// builds from any directory — the Go analog of the Python wheel's bundled
// source (see docs/research/repo-root-and-distribution.md). The transform is a
// comment-preserving text edit, kept pure so it is unit-testable without disk.
package repopath

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// activeRepoPathLine matches an active (non-commented) top-level repo_path
// assignment: leading whitespace, then the quoted key, up to and including its
// string value. A commented line (`// "repo_path": …`) does not match because
// the `//` sits before the quote, so the `^\s*"` anchor fails.
var activeRepoPathLine = regexp.MustCompile(`(?m)^(\s*)"repo_path"(\s*):(\s*)"(?:[^"\\]|\\.)*"`)

// Set returns user-config JSONC content with repo_path set to repoDir.
//
//   - hasActiveKey true (an uncommented repo_path exists, per a JSONC parse):
//     replace its value in place, preserving all comments and other keys.
//   - hasActiveKey false, existing is non-empty JSONC with an opening brace:
//     insert a repo_path line right after the first `{`.
//   - existing empty (no file yet): produce a minimal, commented starter file.
//
// repoDir is JSON-encoded, so quotes/backslashes in the path are escaped.
func Set(existing string, hasActiveKey bool, repoDir string) string {
	enc := strconv.Quote(repoDir)

	if hasActiveKey {
		return activeRepoPathLine.ReplaceAllString(existing, `${1}"repo_path"${2}:${3}`+quoteReplacement(enc))
	}

	if trimmed := strings.TrimSpace(existing); trimmed != "" {
		if i := strings.IndexByte(existing, '{'); i >= 0 {
			// Insert after the brace's line so we don't disturb a leading
			// comment banner that precedes the `{`.
			nl := strings.IndexByte(existing[i:], '\n')
			if nl < 0 {
				// Single-line `{...}` — insert right after the brace.
				return existing[:i+1] + "\n  \"repo_path\": " + enc + "," + existing[i+1:]
			}
			insertAt := i + nl + 1
			return existing[:insertAt] + "  \"repo_path\": " + enc + ",\n" + existing[insertAt:]
		}
	}

	// No usable existing content — write a minimal starter.
	return "{\n" +
		"  // Path to the yolo-jail source checkout, written by `just deploy`.\n" +
		"  // Lets an installed `yolo` find the repo for nix image builds from any\n" +
		"  // directory. See docs/research/repo-root-and-distribution.md.\n" +
		"  \"repo_path\": " + enc + "\n" +
		"}\n"
}

// quoteReplacement escapes `$` in the replacement string so regexp.ReplaceAll
// treats it literally (a path could, in theory, contain `$`).
func quoteReplacement(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}

// activeRepoPath reports whether content parses as JSONC with a non-empty
// string repo_path (an ACTIVE key, so Set replaces in place) and its value.
// A parse failure or a commented-out key yields (false, "").
func activeRepoPath(content string) (string, bool) {
	if strings.TrimSpace(content) == "" {
		return "", false
	}
	parsed, err := json5.Decode([]byte(content))
	if err != nil {
		return "", false
	}
	m, ok := parsed.(*jsonx.OrderedMap)
	if !ok {
		return "", false
	}
	v, present := m.Get("repo_path")
	if !present {
		return "", false
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// WriteFile idempotently sets repo_path=repoDir in the user config at path,
// creating it (and its parent dir) if absent. It preserves existing comments
// and keys. Writes only when the effective value changes; reports what happened
// to out. Returns an error only on a filesystem failure.
func WriteFile(path, repoDir string, out io.Writer) error {
	repoDir = filepath.Clean(repoDir)

	var existing string
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}

	if cur, ok := activeRepoPath(existing); ok && filepath.Clean(cur) == repoDir {
		fmt.Fprintf(out, "repo_path already set to %s in %s\n", repoDir, path)
		return nil
	}

	_, hadActive := activeRepoPath(existing)
	updated := Set(existing, hadActive, repoDir)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return err
	}
	verb := "Set"
	if hadActive {
		verb = "Updated"
	} else if existing == "" {
		verb = "Created " + path + " and set"
	}
	fmt.Fprintf(out, "%s repo_path = %s in %s\n", verb, repoDir, path)
	return nil
}
