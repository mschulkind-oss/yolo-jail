package entrypoint

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// sha256Hex returns the hex sha256 of s (for history isolation keying).
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// truthy bool(v) for decoded JSON values: "" / 0 / 0.0 /
// [] / {} / false / null are falsy; everything else truthy.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case *jsonx.OrderedMap:
		return t.Len() > 0
	default:
		// jsonInt literal: falsy iff it equals 0.
		s, err := jsonx.DumpsCompact(v)
		if err != nil {
			return true
		}
		return s != "0"
	}
}

// writeExecutable writes content to path (truncate-in-place via WriteInPlace
// to preserve inodes for bind-mounted files, per docs/design/agent-briefings.md)
// then sets the executable bit.
// This is the "OR in owner-execute" variant used by shims and mcp_wrappers: it
// ORs owner-execute (0o100) onto the file's current mode, yielding 0o744 for a
// freshly-created 0o644 file. (Other generators that emit 0o755 scripts chmod
// to 0o755 directly.)
func writeExecutable(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := WriteStringInPlace(path, content, 0o644); err != nil {
		return err
	}
	// The current mode after WriteInPlace is 0o644 (umask-independent: WriteFile
	// on create uses the perm arg, and on an existing file leaves the mode). OR
	// in owner-execute.
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.Chmod(path, fi.Mode()|0o100)
}

// writeInPlaceString writes content with mode 0o644, truncate-in-place. For
// non-executable config files that need no chmod, so the file keeps the mode it
// had (0o644 on first create).
func writeInPlaceString(path, content string) error {
	return WriteStringInPlace(path, content, 0o644)
}

// pyStr renders a decoded JSON scalar as a string: bool -> "True"/"False",
// int -> decimal, float -> repr, str -> as-is. jsonx.Decode yields bool,
// string, jsonInt (via IntLiteral wrapper) or float64 for numbers; we render
// each accordingly.
func pyStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		// Reuse jsonx's float formatting via DumpsCompact of the float.
		s, _ := jsonx.DumpsCompact(t)
		return s
	default:
		// jsonInt and any other numeric literal re-encode as their integer text.
		s, _ := jsonx.DumpsCompact(t)
		return s
	}
}

// writeBytesMode writes bytes truncate-in-place then forces mode.
func writeBytesMode(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := WriteInPlace(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// stringValue returns the string value at key in m, and whether it is present
// AND a string.
func stringValue(m *jsonx.OrderedMap, key string) (string, bool) {
	v, ok := m.Get(key)
	if !ok {
		return "", false
	}
	s, isStr := v.(string)
	return s, isStr
}

// stringList returns the list at key as []string, dropping non-string elements.
// A missing key or non-list value yields nil or []` when
// callers treat absent as empty).
func stringList(m *jsonx.OrderedMap, key string) []string {
	v, ok := m.Get(key)
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range arr {
		if s, isStr := e.(string); isStr {
			out = append(out, s)
		}
	}
	return out
}
