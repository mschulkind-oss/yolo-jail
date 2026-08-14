// Package loopholes is the host-side registry that discovers, validates, and
// translates "loophole" manifests into container-runtime flags. A loophole is
// a single declared host<->jail permeability point (Claude OAuth broker TLS
// intercept, host-process viewer, audio socket pass-through).
//
// The manifest SCHEMA is not here: it lives in internal/loopholedecl, a leaf
// package the pack footprint can import (this one cannot be imported from there —
// see schema.go). What this package owns is everything the schema cannot decide on
// its own: resolving the module-dir tokens against real paths, evaluating
// `requires`, per-loophole state dirs, discovery order and precedence, the runtime
// argv, and toggling `enabled` in a manifest on disk.
package loopholes

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// Source labels, ordered weakest -> strongest: bundled < user < config.
const (
	SourceBundled = "bundled"
	SourceUser    = "user"
	SourceConfig  = "config"
)

// fileExists reports whether path exists (a file or dir).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// BundledLoopholesDir returns the loopholes that ship with the binary. Package
// var so tests can override. Repo discovery uses the single shared resolver
// (internal/reporoot) — the SAME method run/check use — so a bundled_loopholes
// checkout is found identically inside and outside the jail (a source checkout
// by cwd-walk, self-hosting /workspace included). The shipped "two files and a
// binary" bundle carries NO bundled_loopholes tree, so an installed binary
// resolves the manifests from the go:embed copy (materializeEmbedded) — that is
// the normal production path, not a fallback.
var BundledLoopholesDir = func() string {
	root, ok := reporoot.Resolve(os.Getenv)
	if ok {
		if dir := filepath.Join(root, "bundled_loopholes"); fileExists(dir) {
			return dir
		}
	}
	if mat, err := materializeEmbedded(); err == nil {
		return mat
	}
	// Embed materialization failed (cache dir unwritable etc.): degrade to the
	// resolved repo's bundled_loopholes path (may not exist — pre-embed
	// behavior), or a bare relative name when nothing resolved.
	if ok {
		return filepath.Join(root, "bundled_loopholes")
	}
	return "bundled_loopholes"
}

// UserLoopholesDir returns the third-party loopholes dir (overrides bundled on
// name collision).
var UserLoopholesDir = func() string {
	return filepath.Join(paths.GlobalStorage(), "loopholes")
}

// StateDirFor returns the writable per-loophole state directory. Package var
// so tests can monkeypatch it.
var StateDirFor = func(name string) string {
	return filepath.Join(paths.GlobalStorage(), "state", name)
}

// Loophole is one RESOLVED loophole: a decoded manifest (loopholedecl.Manifest)
// with its tokens substituted against this machine, plus where it came from.
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
	// StateFiles narrows what crosses from the per-loophole state dir into the
	// jail: paths relative to StateDir(), each mounted as a single :ro file.
	// nil/empty keeps the historical whole-directory mount, so an external
	// manifest without the key does not change meaning. Declaring it is
	// least-privilege — see runtime.go and issue #33 (the broker CA's PRIVATE
	// key rode the whole-dir mount into every jail, where nothing reads it).
	StateFiles []string
	Requires   Requires
	Source     string
	// SkewNotes are the version-skew reports from the TOLERANT manifest read: one
	// line per manifest key this build does not know, so the declaration is not
	// honored. NOT errors — a manifest key only a newer yolo knows must not make
	// the loophole vanish, which is exactly what the tolerant read exists for — but
	// never silent either: loadFromDir warns each one, so a DEGRADED loophole (a
	// key this build cannot act on) is as visible as a rejected one. Always empty
	// on the strict authoring path, where the same manifest is refused outright.
	// Mirrors packload.Pack.SkewNotes.
	SkewNotes []string
}

// FromConfig reports whether this loophole came from a yolo-jail.jsonc
// loopholes: entry (no manifest file).
func (l *Loophole) FromConfig() bool { return l.Source == SourceConfig }

// HasCA reports whether ca_cert is set and points at a regular file.
func (l *Loophole) HasCA() bool {
	if !l.CACertSet || l.CACert == "" {
		return false
	}
	fi, err := os.Stat(l.CACert)
	return err == nil && fi.Mode().IsRegular()
}

func (l *Loophole) StateDir() string { return StateDirFor(l.Name) }

// inJail reports whether YOLO_VERSION is present in the environment (an empty
// value still counts).
func inJail() bool {
	_, ok := os.LookupEnv("YOLO_VERSION")
	return ok
}

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

func (l *Loophole) Active() bool { return l.Enabled && l.RequirementsMet() }

// Returns "" for None.
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

// expandEnv expands ${VAR}/$VAR references against the environment.
// Unresolved refs collapse to the empty string (deliberately unlike shell).
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

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

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
