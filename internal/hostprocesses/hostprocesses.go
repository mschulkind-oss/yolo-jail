// Package hostprocesses is the Go port of src/host_processes.py — the
// allowlisted host-process viewer daemon. It answers ps-style requests from the
// jail against an allowlist configured in yolo-jail.jsonc, via
// internal/hostservice (the frame-protocol server).
//
// Frozen contracts (go-port plan Stage 5): the config load (host_processes
// section, re-read PER REQUEST so operator edits take effect without restart),
// the DEFAULT_FIELDS, the list/tree/pid mode argv + allowlist construction, and
// the exit codes (3 empty-allowlist, 2 bad-mode/bad-pid/not-allowlisted).
//
// Source of truth: src/host_processes.py + src/host_service.py.
package hostprocesses

import (
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// DefaultFields mirrors DEFAULT_FIELDS.
var DefaultFields = []string{"pid", "comm", "args", "etime", "%cpu", "%mem", "rss"}

// Config is the loaded host_processes section.
type Config struct {
	Visible []string
	Fields  []string
}

// LoadConfig reads the host_processes section from the jsonc config at
// configPath. A missing file or missing/unreadable section → empty allowlist
// with DEFAULT_FIELDS (feature effectively disabled). Mirrors _load_config,
// including the str-filtering of visible/fields lists.
func LoadConfig(configPath string) Config {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{Visible: []string{}, Fields: append([]string(nil), DefaultFields...)}
	}
	decoded, err := json5.Decode(data)
	if err != nil {
		// Python logs "unreadable" and treats as empty.
		return Config{Visible: []string{}, Fields: append([]string(nil), DefaultFields...)}
	}
	root, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return Config{Visible: []string{}, Fields: append([]string(nil), DefaultFields...)}
	}
	hp := getMap(root, "host_processes")
	visible := strListOrEmpty(hp, "visible")
	fields := strListOrDefault(hp, "fields", DefaultFields)
	return Config{Visible: visible, Fields: fields}
}

func getMap(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if m == nil {
		return nil
	}
	v, ok := m.Get(key)
	if !ok || v == nil {
		return nil
	}
	sub, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return nil
	}
	return sub
}

// strListOrEmpty returns the string elements of m[key], or [] (Python
// `hp.get("visible") or []` then filter to str).
func strListOrEmpty(m *jsonx.OrderedMap, key string) []string {
	if m == nil {
		return []string{}
	}
	v, ok := m.Get(key)
	if !ok || v == nil {
		return []string{}
	}
	arr, ok := v.([]any)
	if !ok {
		return []string{}
	}
	out := []string{}
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// strListOrDefault mirrors `[str(x) for x in (hp.get("fields") or DEFAULT) if
// isinstance(x, str)]`: the `or DEFAULT` applies to the RAW value, so an
// absent/empty/non-list value → DEFAULT (then filtered, a no-op); a NON-EMPTY
// list → filtered to its str elements (which may be []). Verified against the
// live _load_config.
func strListOrDefault(m *jsonx.OrderedMap, key string, def []string) []string {
	var raw []any
	if m != nil {
		if v, ok := m.Get(key); ok && v != nil {
			if arr, ok := v.([]any); ok {
				raw = arr
			}
		}
	}
	// `or DEFAULT`: empty/absent/non-list raw is falsy -> use DEFAULT.
	if len(raw) == 0 {
		raw = make([]any, len(def))
		for i, d := range def {
			raw[i] = d
		}
	}
	out := []string{}
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// BuildHandler returns the hostservice.Handler, re-reading the config on every
// request (cheap; operator edits take effect without a restart). Mirrors
// build_handler + the mode dispatch in host_processes.py.
func BuildHandler(configPath string) hostservice.Handler {
	return func(s *hostservice.Session) {
		cfg := LoadConfig(configPath)
		visible := map[string]struct{}{}
		for _, c := range cfg.Visible {
			visible[c] = struct{}{}
		}
		fields := cfg.Fields
		// Python: mode = str(request.get("mode") or "list"). A truthy NON-string
		// (e.g. 5, {...}) is stringified and falls through to the unknown-mode
		// exit-2 branch — it must NOT silently run list mode. Falsy (absent, "",
		// 0, null, false, []) -> "list".
		mode := pyStrOrList(func() (any, bool) { return s.Get("mode") })

		if len(visible) == 0 {
			s.Stderr("host_processes.visible is empty in yolo-jail.jsonc — nothing to show\n")
			s.Exit(3)
			return
		}

		switch mode {
		case "list":
			handleList(s, visible, fields)
		case "tree":
			handleTree(s, visible)
		case "pid":
			handlePid(s, visible, fields)
		default:
			s.Stderr("unknown mode: " + pytext.Repr(mode) + "\n")
			s.Exit(2)
		}
	}
}

// pyStrOrList mirrors Python's str(request.get("mode") or "list"): if the
// value is falsy (absent, "", 0, 0.0, false, null, empty list/dict) -> "list";
// otherwise str(value). For a string that's str(value)==value; for other
// truthy types we produce Python's str() form so a bogus mode still routes to
// the unknown-mode exit-2 branch (e.g. 5 -> "5", true -> "True").
func pyStrOrList(get func() (any, bool)) string {
	v, ok := get()
	if !ok || !pyTruthy(v) {
		return "list"
	}
	return pyStr(v)
}

// pyTruthy mirrors Python bool(x) for the jsonx value model.
func pyTruthy(v any) bool {
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
		return len(t) != 0
	case *jsonx.OrderedMap:
		return t.Len() != 0
	default:
		// jsonx integer literal: truthy unless it's zero.
		s, _ := jsonx.DumpsCompact(v)
		s = strings.TrimSpace(s)
		return s != "0" && s != "-0" && s != ""
	}
}

// pyStr mirrors Python str(x) for the types a JSON "mode" could decode to.
func pyStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	default:
		// int / float literal -> its literal text; containers -> the compact
		// JSON form (close enough to route to unknown-mode; real clients never
		// send these).
		s, _ := jsonx.DumpsCompact(v)
		return s
	}
}

// handleList runs `ps -o <fields> -C <comm>...` with an allowlist. Mirrors the
// list branch: argv = ["ps","-o",joined] + ["-C",comm] for each sorted comm;
// allowlist = visible ∪ {"ps","-o","-C",joined}.
func handleList(s *hostservice.Session, visible map[string]struct{}, fields []string) {
	joined := strings.Join(fields, ",")
	argv := []string{"ps", "-o", joined}
	comms := sortedKeys(visible)
	for _, comm := range comms {
		argv = append(argv, "-C", comm)
	}
	allow := map[string]struct{}{}
	for c := range visible {
		allow[c] = struct{}{}
	}
	for _, k := range []string{"ps", "-o", "-C", joined} {
		allow[k] = struct{}{}
	}
	s.ExecAllowlisted(func(*jsonx.OrderedMap) []string { return argv }, allow, nil, 30_000_000_000)
}

// handlePid runs `ps -o <fields> -p <pid>` after verifying the pid's comm is
// allowlisted. Mirrors the pid branch (all positions validated).
func handlePid(s *hostservice.Session, visible map[string]struct{}, fields []string) {
	pidV, ok := s.Get("pid")
	pid, isInt := asIntStrict(pidV)
	if !ok || !isInt {
		s.Stderr("pid mode requires integer 'pid' in request\n")
		s.Exit(2)
		return
	}
	commBytes, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		s.Stderr("pid " + strconv.Itoa(pid) + " not found\n")
		s.Exit(1)
		return
	}
	comm := strings.TrimSpace(string(commBytes))
	if _, allowed := visible[comm]; !allowed {
		s.Stderr("pid " + strconv.Itoa(pid) + " has comm=" + pytext.Repr(comm) + " which is not allowlisted\n")
		s.Exit(2)
		return
	}
	joined := strings.Join(fields, ",")
	pidStr := strconv.Itoa(pid)
	argv := []string{"ps", "-o", joined, "-p", pidStr}
	allow := map[string]struct{}{
		"ps": {}, "-o": {}, joined: {}, "-p": {}, pidStr: {}, comm: {},
	}
	// argv_positions = all positions (Python passes set(range(len(argv)))).
	positions := map[int]struct{}{}
	for i := range argv {
		positions[i] = struct{}{}
	}
	s.ExecAllowlisted(func(*jsonx.OrderedMap) []string { return argv }, allow, positions, 30_000_000_000)
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// asIntStrict mirrors Python's isinstance(want_pid, int): only an actual JSON
// integer counts (a float like 42.0 or a string "42" does NOT).
func asIntStrict(v any) (int, bool) {
	// jsonx decodes JSON integers to its internal integer type (re-encodes with
	// no "."); a float decodes to float64. Distinguish by re-encoding.
	if v == nil {
		return 0, false
	}
	if _, isFloat := v.(float64); isFloat {
		return 0, false // Python: 42.0 is not an int
	}
	if _, isStr := v.(string); isStr {
		return 0, false
	}
	if _, isBool := v.(bool); isBool {
		return 0, false
	}
	s, err := jsonx.DumpsCompact(v)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return n, true
}
