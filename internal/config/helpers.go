package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// userHomeDir returns the passwd-database home dir (Python's
// pwd.getpwuid(getuid()).pw_dir), used by expanduser when HOME is unset.
func userHomeDir() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}

// dedupKey reproduces _merge_lists' canonical key,
// json.dumps(item, sort_keys=True, default=str). jsonx.DumpsSnapshot is
// json.dumps(item, indent=2, sort_keys=True, ensure_ascii=True): it differs
// from the compact form only in whitespace, which is a pure function of the
// value's structure — so it induces the IDENTICAL equality partition. Two items
// dedup together iff Python's json.dumps(sort_keys=True) forms are equal, which
// is exactly what _merge_lists needs. default=str never fires for decoded JSON
// values (all are natively serializable), so no fallback is needed.
func dedupKey(item any) string {
	s, err := jsonx.DumpsSnapshot(item)
	if err != nil {
		// Unreachable for decoded config values; degrade to a per-instance key
		// so distinct un-encodable values never collapse.
		return fmt.Sprintf("\x00go:%p:%T", &item, item)
	}
	return s
}

// typeName reproduces Python's type(x).__name__ for the value types that reach
// the two "got {type(...).__name__}" error strings in _validate_config. Decoded
// JSON values map to: dict->*OrderedMap, list->[]any, str->string,
// int->jsonx int, float->float64, bool->bool, None->nil.
func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case string:
		return "str"
	case float64:
		return "float"
	case []any:
		return "list"
	case *jsonx.OrderedMap:
		return "dict"
	default:
		if jsonx.IsInt(v) {
			return "int"
		}
		return fmt.Sprintf("%T", v)
	}
}

// resolvePathForSeen mirrors `path.resolve() if path.exists() else path`
// (wrapped in try/except OSError -> path) for the include cycle-detection key.
func resolvePathForSeen(path string) string {
	if pathExists(path) {
		if r, err := resolve(path); err == nil {
			return r
		}
	}
	return path
}

func resolveJoin(baseDir, entry string) string {
	joined := filepath.Join(baseDir, entry)
	if r, err := resolve(joined); err == nil {
		return r
	}
	return joined
}

// resolve Path.resolve() (strict=False): make absolute,
// resolve symlinks where possible, normalize lexically. filepath.EvalSymlinks
// errors on nonexistent paths (Python does not), so fall back to the lexical
// absolute clean — matching internal/naming.FromWorkspace's approach.
func resolve(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if evaled, err := filepath.EvalSymlinks(abs); err == nil {
		return evaled, nil
	}
	return filepath.Clean(abs), nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func cwd() string {
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return "."
}

// ---------------------------------------------------------------------------
// validation helpers
// ---------------------------------------------------------------------------

func isStr(v any) bool {
	_, ok := v.(string)
	return ok
}

// isStrList list) and all(isinstance(x, str) for x in v)`.
func isStrList(v any) bool {
	l, ok := asList(v)
	if !ok {
		return false
	}
	for _, x := range l {
		if !isStr(x) {
			return false
		}
	}
	return true
}

// inStrList reports whether v (any) equals a string in list.
func inStrList(list []string, v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	return inStrSlice(list, s)
}

func inStrSlice(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// pyInt mirrors int(value) for validate_port_number: accept an int literal or a
// string that Python's int() parses (base-10, optional sign, surrounding
// whitespace, underscores between digits). Returns (n, true) on success. A bool
// is an int in Python (True->1, False->0). A float raises TypeError? No —
// int(3.5)==3, but validate_port_number receives already-decoded JSON where a
// port is an int or a string; int(float) truncates.
// truncate toward zero.
func pyInt(value any) (int64, bool) {
	switch t := value.(type) {
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case float64:
		// int(float) truncates toward zero.
		return int64(t), true
	case string:
		return pyIntFromString(t)
	default:
		if n, ok := jsonx.AsInt(value); ok {
			return n, true
		}
		// A very large int literal beyond int64 still parses in Python; for
		// port validation it will fail the 1..65535 range anyway. Try literal.
		if lit, ok := jsonx.AsIntLiteral(value); ok {
			return pyIntFromString(lit)
		}
		return 0, false
	}
}

// whitespace, optional +/- sign, digits with single underscores allowed between
// digits. Returns (0,false) on any malformed input.
func pyIntFromString(s string) (int64, bool) {
	str := strings.TrimSpace(s)
	if str == "" {
		return 0, false
	}
	neg := false
	if str[0] == '+' || str[0] == '-' {
		neg = str[0] == '-'
		str = str[1:]
	}
	if str == "" {
		return 0, false
	}
	// Underscores allowed only between digits.
	var digits strings.Builder
	prevDigit := false
	for i := 0; i < len(str); i++ {
		c := str[i]
		if c == '_' {
			if !prevDigit || i+1 >= len(str) || str[i+1] < '0' || str[i+1] > '9' {
				return 0, false
			}
			prevDigit = false
			continue
		}
		if c < '0' || c > '9' {
			return 0, false
		}
		digits.WriteByte(c)
		prevDigit = true
	}
	n, err := strconv.ParseInt(digits.String(), 10, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}

// ['off', 'user', 'full'].
func pyListRepr(items []string) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = pytext.Repr(it)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// joinSorted ".join(sorted(SET)) for a Go set.
func joinSorted(m map[string]struct{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrs(keys)
	return strings.Join(keys, ", ")
}

func sortStrs(s []string) {
	sort.Strings(s)
}

func containsDotDot(entry string) bool {
	for _, part := range strings.Split(entry, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func expandAndResolve(hostPath string) string {
	expanded := expandUser(hostPath)
	if r, err := resolve(expanded); err == nil {
		return r
	}
	return expanded
}

func hasKey(m *jsonx.OrderedMap, key string) bool {
	_, ok := m.Get(key)
	return ok
}

// keysSubsetOf mirrors `set(spec) <= allowed`.
func keysSubsetOf(m *jsonx.OrderedMap, allowed map[string]struct{}) bool {
	for _, k := range m.Keys() {
		if _, ok := allowed[k]; !ok {
			return false
		}
	}
	return true
}

func hasPrefix(s, prefix string) bool { return strings.HasPrefix(s, prefix) }

func itoa(n int) string { return strconv.Itoa(n) }
