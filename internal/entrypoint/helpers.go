package entrypoint

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/fsx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

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
	if err := fsx.WriteStringInPlace(path, content, mode); err != nil {
		return err
	}
	// WriteInPlace won't downgrade an existing file's mode to `mode`; force it
	// so a re-run produces the exact bits Python's explicit chmod sets.
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
