// Package loopholes is the host-side registry that discovers, validates, and
// translates "loophole" manifests into container-runtime flags. A loophole is
// a single declared host<->jail permeability point (Claude OAuth broker TLS
// intercept, host-process viewer, audio socket pass-through).
package loopholes

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// DefaultBrokerIP mirrors DEFAULT_BROKER_IP. The container runtime translates
// the literal "host-gateway" into the right host-reachable address for the
// active runtime.
const DefaultBrokerIP = "host-gateway"

// Valid enum values. Kept as ordered slices whose sort matches Python's
// sorted(set) so the "not in [...]" error strings render identically.
var (
	validTransports = []string{"tls-intercept", "unix-socket", "none"}
	validLifecycles = []string{"external", "spawned"}
	validRestarts   = []string{"always", "on-failure", "no"}
)

// Source labels, ordered weakest -> strongest: bundled < user < config.
const (
	SourceBundled = "bundled"
	SourceUser    = "user"
	SourceConfig  = "config"
)

// repoRoot resolves the repo root for bundled_loopholes discovery.
// (1) trust YOLO_REPO_ROOT when set, (2) else walk up from cwd for a YOLO-JAIL
// checkout (flake.nix AND go.mod), (3) else the in-jail default /opt/yolo-jail.
func repoRoot() string {
	if r := os.Getenv("YOLO_REPO_ROOT"); r != "" {
		return r
	}
	if dir, err := os.Getwd(); err == nil {
		for {
			if fileExists(filepath.Join(dir, "flake.nix")) &&
				fileExists(filepath.Join(dir, "go.mod")) {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "/opt/yolo-jail"
}

// fileExists reports whether path exists (a file or dir).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// BundledLoopholesDir returns the loopholes that ship with the binary. Package
// var so tests can override. Falls back to the go:embed copy when no checkout
// or in-jail copy exists (installed binary outside any repo).
var BundledLoopholesDir = func() string {
	dir := filepath.Join(repoRoot(), "bundled_loopholes")
	if fileExists(dir) {
		return dir
	}
	if mat, err := materializeEmbedded(); err == nil {
		return mat
	}
	return dir // cache dir unwritable etc. — degrade to the pre-embed behavior
}

// UserLoopholesDir returns the third-party loopholes dir (overrides bundled on
// name collision). Mirrors user_loopholes_dir().
var UserLoopholesDir = func() string {
	return filepath.Join(paths.GlobalStorage(), "loopholes")
}

// StateDirFor returns the writable per-loophole state directory. Package var so
// tests can monkeypatch it (mirrors src.loopholes.state_dir_for). Mirrors
// state_dir_for(name).
var StateDirFor = func(name string) string {
	return filepath.Join(paths.GlobalStorage(), "state", name)
}

// Intercept mirrors the Intercept dataclass.
type Intercept struct {
	Host string
}

// JailDaemon mirrors the JailDaemon dataclass. Restart defaults to "on-failure".
type JailDaemon struct {
	Cmd     []string
	Restart string
}

// HostDaemon mirrors the HostDaemon dataclass. Env is insertion-ordered.
type HostDaemon struct {
	Cmd []string
	Env *EnvMap
}

// HostBindMount mirrors the HostBindMount dataclass. Readonly defaults true.
type HostBindMount struct {
	Host      string
	Container string
	Readonly  bool
}

// Requires mirrors the Requires dataclass. A nil-valued field means "absent"
// (Python None); we track presence with the *Set booleans so an explicit value
// is distinguishable from an unset one.
type Requires struct {
	CommandOnPath    string
	CommandOnPathSet bool
	FileExists       string
	FileExistsSet    bool
}

// Loophole mirrors the Loophole dataclass — a loaded, validated manifest.
type Loophole struct {
	Name          string
	Description   string
	Path          string
	Enabled       bool
	Transport     string
	Lifecycle     string
	Intercepts    []Intercept
	BrokerIP      string
	CACert        string // "" == None
	CACertSet     bool
	JailEnv       *EnvMap
	DoctorCmd     []string // nil == None
	DoctorCmdSet  bool
	HostDaemon    *HostDaemon
	JailDaemon    *JailDaemon
	HostBindMount []HostBindMount
	HostDevices   []string
	Requires      Requires
	Source        string
}

// FromConfig reports whether this loophole came from a yolo-jail.jsonc
// loopholes: entry (no manifest file). Mirrors the from_config property.
func (l *Loophole) FromConfig() bool { return l.Source == SourceConfig }

// HasCA mirrors the has_ca property: ca_cert is set and points at a regular
// file.
func (l *Loophole) HasCA() bool {
	if !l.CACertSet || l.CACert == "" {
		return false
	}
	fi, err := os.Stat(l.CACert)
	return err == nil && fi.Mode().IsRegular()
}

// StateDir mirrors the state_dir property.
func (l *Loophole) StateDir() string { return StateDirFor(l.Name) }

// inJail reports whether YOLO_VERSION is present in the environment (Python's
// os.environ.get("YOLO_VERSION") is not None — an empty value still counts).
func inJail() bool {
	_, ok := os.LookupEnv("YOLO_VERSION")
	return ok
}

// RequirementsMet mirrors the requirements_met property.
func (l *Loophole) RequirementsMet() bool {
	if inJail() {
		return l.inJailActive()
	}
	req := l.Requires
	if req.CommandOnPathSet {
		if _, err := exec.LookPath(req.CommandOnPath); err != nil {
			return false
		}
	}
	if req.FileExistsSet {
		expanded := expandEnv(req.FileExists)
		if expanded == "" || !pathExists(expanded) {
			return false
		}
	}
	return true
}

// inJailActive mirrors _in_jail_active.
func (l *Loophole) inJailActive() bool {
	if len(l.HostBindMount) == 0 {
		return true
	}
	for _, bm := range l.HostBindMount {
		if pathExists(bm.Container) {
			return true
		}
	}
	return false
}

// Active mirrors the active property.
func (l *Loophole) Active() bool { return l.Enabled && l.RequirementsMet() }

// InactiveReason mirrors the inactive_reason property. Returns "" for None.
func (l *Loophole) InactiveReason() (string, bool) {
	if !l.Enabled {
		return "disabled", true
	}
	if inJail() {
		if len(l.HostBindMount) > 0 && !l.inJailActive() {
			return "host-side wiring not visible in this jail", true
		}
		return "", false
	}
	req := l.Requires
	if req.CommandOnPathSet {
		if _, err := exec.LookPath(req.CommandOnPath); err != nil {
			return pytext.Repr(req.CommandOnPath) + " not on PATH", true
		}
	}
	if req.FileExistsSet {
		expanded := expandEnv(req.FileExists)
		if expanded == "" || !pathExists(expanded) {
			raw := req.FileExists
			shown := expanded
			if shown == "" {
				shown = "<empty after env expansion>"
			}
			return "host path " + pytext.Repr(raw) + " missing (resolved to " + pytext.Repr(shown) + ")", true
		}
	}
	return "", false
}

// _ENV_REF: \$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)
var envRef = regexp.MustCompile(`\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// expandEnv mirrors _expand_env: ${VAR}/$VAR expand against the environment,
// and UNRESOLVED refs collapse to the empty string (deliberately unlike shell).
func expandEnv(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		sub := envRef.FindStringSubmatch(m)
		name := sub[1]
		if name == "" {
			name = sub[2]
		}
		return os.Getenv(name)
	})
}

// pathExists mirrors Path.exists() (follows symlinks; any stat error -> false).
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// stat is os.Stat, aliased so the parser reads like the Python source.
func stat(p string) (os.FileInfo, error) { return os.Stat(p) }

// readFile is os.ReadFile.
func readFile(p string) ([]byte, error) { return os.ReadFile(p) }

// resolvePath mirrors pathlib.Path.resolve() (non-strict): make absolute, then
// resolve symlinks + ".." as far as the filesystem allows, falling back to a
// lexical clean when the path doesn't exist. Matches internal/config's resolve.
func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if evaled, err := filepath.EvalSymlinks(abs); err == nil {
		return evaled
	}
	return filepath.Clean(abs)
}

// pyStr renders a decoded-JSON scalar the way Python's str() does inside an
// f-string / dict comprehension: string as-is, bool -> True/False, int ->
// decimal, float -> repr.
func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return jsonx.FormatFloatRepr(t)
	default:
		if lit, ok := jsonx.AsIntLiteral(v); ok {
			return lit
		}
		s, _ := jsonx.DumpsCompact(v)
		return s
	}
}

// pyTruthy bool(v) for decoded-JSON values.
func pyTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return len(t) > 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case *jsonx.OrderedMap:
		return t.Len() > 0
	default:
		if lit, ok := jsonx.AsIntLiteral(v); ok {
			return !isZeroIntLiteral(lit)
		}
		return true
	}
}

func isZeroIntLiteral(lit string) bool {
	s := strings.TrimPrefix(lit, "-")
	s = strings.TrimPrefix(s, "+")
	for _, c := range s {
		if c != '0' {
			return false
		}
	}
	return len(s) > 0
}

// pyListRepr renders repr() of a Python list of strings: ['a', 'b'].
func pyListRepr(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = pytext.Repr(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
