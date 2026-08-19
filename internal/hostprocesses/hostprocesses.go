// Package hostprocesses is the allowlisted host-process viewer daemon. It
// answers ps-style requests from the jail against an allowlist yolo resolved at
// launch, via internal/hostservice (the frame-protocol server).
// Frozen contracts: the DEFAULT_FIELDS, the list/tree/pid mode argv + allowlist
// construction, and the exit codes (3 empty-allowlist, 2 bad-mode/bad-pid/
// not-allowlisted).
//
// # The allowlist is FROZEN at launch, and that is a deliberate change
//
// This daemon used to open the raw workspace `yolo-jail.jsonc` itself, from an
// inherited cwd, ON EVERY REQUEST — which is the only reason editing
// `host_processes.visible` took effect without a restart. That affordance is real
// and it is indistinguishable from the hole: the same property let an AGENT widen
// its own allowlist mid-session, with no launch and therefore no config-approval
// gate, and the config diff was not in that causal path at all.
//
// It now reads ONE file, ONCE, at startup: the settings file yolo writes after
// validating the values against the loophole manifest's `settings` declarations
// (docs/design/pack-config-keys.md OQ-K3). Changing what yolo-ps may show requires a
// jail restart, which is exactly where the approval gate lives.
//
// The daemon therefore never parses a config file, never knows where the workspace
// is, and never sees a key it was not handed. What it reads is a flat JSON object
// of already-validated values.
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

var DefaultFields = []string{"pid", "comm", "args", "etime", "%cpu", "%mem", "rss"}

// Config is the resolved settings this daemon runs on.
type Config struct {
	Visible []string
	Fields  []string
}

// disabled is the fail-closed Config: no allowlist, so every request exits 3.
//
// It is what an absent, unreadable or malformed settings file resolves to, and the
// three cases share an answer on purpose. A daemon that could not read its
// allowlist has no basis for showing anything, and the alternative — refusing to
// start — would turn a transient read failure into a launch that fails with the
// daemon's readiness probe rather than with a sentence naming the file.
func disabled() Config {
	return Config{Visible: []string{}, Fields: append([]string(nil), DefaultFields...)}
}

// LoadSettings reads the flat settings file yolo wrote for this loophole: a JSON
// object of already-validated values, keyed by the names the manifest declares.
//
// It does NOT parse a yolo-jail.jsonc and does not know one exists. Every value here
// was type-checked against the manifest declaration before it was written, so this
// read is defensive rather than validating: anything of the wrong shape falls back
// to the same place an absent key does.
//
// `fields` falls back to DefaultFields when absent OR EMPTY, which is the one place
// an empty list is not taken literally — an empty `ps -o` column list is not a
// narrower view, it is a broken invocation. `visible` empty is taken literally and means the feature is off,
// which is what it has always meant.
func LoadSettings(settingsPath string) Config {
	cfg, _ := loadSettings(settingsPath)
	return cfg
}

// loadSettings is LoadSettings plus the DIAGNOSIS the health check needs and the
// daemon must not have.
//
// The daemon collapses every failure to `disabled()` on purpose: a running daemon
// with no readable allowlist has no basis for showing anything, and branching on why
// would only give it more ways to be wrong. `yolo check` has the opposite need — it
// exists to tell a human what is wrong — and the two cases it must not confuse are
// "no jail has launched this loophole yet", which is the normal state of a fresh
// machine, and "the file is there and does not parse", which is a real fault.
//
// ok is false only for the second. A MISSING file returns ok=true with the
// fail-closed Config, because absence is not a failure of anything.
func loadSettings(settingsPath string) (cfg Config, ok bool) {
	if settingsPath == "" {
		return disabled(), true
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return disabled(), true
	}
	decoded, err := json5.Decode(data)
	if err != nil {
		return disabled(), false
	}
	root, isMap := decoded.(*jsonx.OrderedMap)
	if !isMap {
		return disabled(), false
	}
	return Config{
		Visible: strListOrEmpty(root, "visible"),
		Fields:  strListOrDefault(root, "fields", DefaultFields),
	}, true
}

// strListOrEmpty returns the string elements of m[key], or [] if absent,
// null, or not a list.
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

// strListOrDefault returns the string elements of m[key], defaulting to def.
// The default applies to the RAW value, so an absent/empty/non-list value →
// def (then filtered, a no-op); a NON-EMPTY list → filtered to its str
// elements (which may be []).
func strListOrDefault(m *jsonx.OrderedMap, key string, def []string) []string {
	var raw []any
	if m != nil {
		if v, ok := m.Get(key); ok && v != nil {
			if arr, ok := v.([]any); ok {
				raw = arr
			}
		}
	}
	// empty/absent/non-list raw -> use the default.
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

// BuildHandler returns the hostservice.Handler for an ALREADY-RESOLVED config.
//
// It takes the values, not a path, and that signature is the freeze: there is no
// file for a request to re-read, so the "editing the allowlist takes effect on the
// next request" behaviour is not merely turned off, it is unrepresentable. The
// allowlist is closed over once, at startup, from the settings file yolo wrote at
// launch (see the package comment for what that trades away and why).
func BuildHandler(cfg Config) hostservice.Handler {
	visible := map[string]struct{}{}
	for _, c := range cfg.Visible {
		visible[c] = struct{}{}
	}
	fields := append([]string(nil), cfg.Fields...)
	return func(s *hostservice.Session) {
		// mode = str(request["mode"] or "list"). A truthy NON-string (e.g. 5,
		// {...}) is stringified and falls through to the unknown-mode exit-2
		// branch — it must NOT silently run list mode. Falsy (absent, "", 0,
		// null, false, []) -> "list".
		mode := pyStrOrList(func() (any, bool) { return s.Get("mode") })

		if len(visible) == 0 {
			// Names the CURRENT spelling, and ONLY it. The old top-level
			// `host_processes.visible` does not work any more — it was honored through the
			// step that moved the keys and REFUSED by the step that deleted it
			// (config.validateHostProcessesRetired), so there is no fold-in left to
			// mention. Naming the retired key here would teach it, and this is the one
			// line a user reads at the moment they are about to go edit a config.
			s.Stderr("loopholes.host-processes.settings.visible is empty — nothing to show. " +
				"Add process names to it and RESTART the jail: the allowlist is resolved " +
				"once at launch, so an edit does not take effect in a running jail.\n")
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

// pyStrOrList implements str(mode or "list"): if the value is falsy (absent,
// "", 0, 0.0, false, null, empty list/dict) -> "list"; otherwise str(value).
// For a string that's str(value)==value; for other truthy types we produce the
// str() form so a bogus mode still routes to the unknown-mode exit-2 branch
// (e.g. 5 -> "5", true -> "True").
func pyStrOrList(get func() (any, bool)) string {
	v, ok := get()
	if !ok || !pyTruthy(v) {
		return "list"
	}
	return pyStr(v)
}

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

// pyStr renders str(x) for the types a JSON "mode" could decode to.
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

// handleList runs `ps -o <fields> -C <comm>...` with an allowlist.
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
// allowlisted.
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
	// argv_positions = all positions.
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

// asIntStrict accepts only an actual JSON integer (a float like 42.0 or a
// string "42" does NOT count).
func asIntStrict(v any) (int, bool) {
	// jsonx decodes JSON integers to its internal integer type (re-encodes with
	// no "."); a float decodes to float64. Distinguish by re-encoding.
	if v == nil {
		return 0, false
	}
	if _, isFloat := v.(float64); isFloat {
		return 0, false // 42.0 is a float, not an int
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
