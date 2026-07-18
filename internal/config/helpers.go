package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

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

// resolveJoin mirrors `(base_dir / entry).resolve()`.
func resolveJoin(baseDir, entry string) string {
	joined := filepath.Join(baseDir, entry)
	if r, err := resolve(joined); err == nil {
		return r
	}
	return joined
}

// resolve mirrors Python's Path.resolve() (strict=False): make absolute,
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
