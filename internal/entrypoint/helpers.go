package entrypoint

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/fsx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// sha256Hex returns the hex sha256 of s (for history isolation keying).
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// truthy mirrors Python's bool(v) for decoded JSON values: "" / 0 / 0.0 /
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

// pyEqual mirrors Python's == for decoded JSON values (jsonx model): recursive
// structural equality across OrderedMap / []any / scalars. Int-vs-float equality
// follows Python (1 == 1.0), compared via canonical compact encoding for numbers.
func pyEqual(a, b any) bool {
	switch av := a.(type) {
	case *jsonx.OrderedMap:
		bv, ok := b.(*jsonx.OrderedMap)
		if !ok || av.Len() != bv.Len() {
			return false
		}
		// Python dict == ignores order; compare by key membership + values.
		for _, k := range av.Keys() {
			x, _ := av.Get(k)
			y, ok := bv.Get(k)
			if !ok || !pyEqual(x, y) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !pyEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case nil:
		return b == nil
	default:
		// Numbers (float64 or jsonInt): Python compares by value, so 1 == 1.0.
		if isNumeric(a) && isNumeric(b) {
			return numericEqual(a, b)
		}
		return false
	}
}

func isNumeric(v any) bool {
	switch v.(type) {
	case float64:
		return true
	default:
		return isJSONInt(v)
	}
}

// numericEqual compares two numeric JSON values by float value (Python's
// cross-type numeric ==). Integer literals are parsed as floats for the compare;
// yolo-jail's numbers (expiresAt, speeds) are well within float64 exact range.
func numericEqual(a, b any) bool {
	return numFloat(a) == numFloat(b)
}

func numFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	s, _ := jsonx.DumpsCompact(v)
	var f float64
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return f
		}
		f = f*10 + float64(c-'0')
	}
	if neg {
		return -f
	}
	return f
}

// writeExecutable writes content to path (truncate-in-place via fsx.WriteInPlace
// to preserve inodes for bind-mounted files, per docs/design/agent-briefings.md)
// then sets the executable bit. Mirrors Python's write_text + chmod(mode|S_IEXEC).
//
// Python's chmod ORs S_IEXEC (owner-execute, 0o100) onto the file's current
// mode. A freshly-created file has mode 0o644 under the default umask, giving
// 0o744. We create with 0o644 then chmod to 0o755 to match what the golden
// harness observes: Python's test-observed shim modes are 0o744 (0o644|0o100),
// but the generators in scripts.py/shell.py that use `chmod(0o755)` produce
// 0o755. We follow each generator's exact Python chmod below; this helper is
// the "OR in owner-execute" variant used by shims and mcp_wrappers.
func writeExecutable(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := fsx.WriteStringInPlace(path, content, 0o644); err != nil {
		return err
	}
	// Python: path.chmod(path.stat().st_mode | stat.S_IEXEC). The current mode
	// after WriteInPlace is 0o644 (umask-independent: WriteFile on create uses
	// the perm arg, and on an existing file leaves the mode). OR owner-execute.
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.Chmod(path, fi.Mode()|0o100)
}

// writeMode writes content to path (truncate-in-place) with an explicit mode,
// mirroring generators that call chmod(0o755) outright (scripts.py).
func writeMode(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := fsx.WriteStringInPlace(path, content, mode); err != nil {
		return err
	}
	// WriteInPlace won't downgrade an existing file's mode to `mode`; force it
	// so a re-run produces the exact bits Python's explicit chmod sets.
	return os.Chmod(path, mode)
}

// writeInPlaceString writes content with mode 0o644, truncate-in-place. For
// non-executable config files whose Python writer uses plain write_text (no
// chmod), so the file keeps the mode it had (0o644 on first create).
func writeInPlaceString(path, content string) error {
	return fsx.WriteStringInPlace(path, content, 0o644)
}

// pyStr renders a decoded JSON scalar the way Python's str() would inside an
// f-string: bool -> "True"/"False", int -> decimal, float -> repr, str -> as-is.
// jsonx.Decode yields bool, string, jsonInt (via IntLiteral wrapper) or float64
// for numbers; we render each accordingly.
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
		// Reuse jsonx's Python-repr(float) via DumpsCompact of the float.
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
	if err := fsx.WriteInPlace(path, data, mode); err != nil {
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
// A missing key or non-list value yields nil (mirrors `x.get(key) or []` when
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
