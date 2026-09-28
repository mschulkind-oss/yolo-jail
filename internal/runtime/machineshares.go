package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// machineshares.go reads the folders a macOS Podman Machine shares with its VM, and
// answers whether a bind-mount source is among them.
//
// WHY IT EXISTS. `podman run -v <src>:…` on a Mac is handed to the machine's Linux VM,
// and the VM looks <src> up in ITS filesystem. A Mac folder is there only if the machine
// was created sharing it (`podman machine init -v <folder>:<folder>`), and that list is
// fixed at init. A source outside it makes podman fail with
// `statfs <src>: no such file or directory` and rc 125, before the jail's pid1 runs and
// with nothing from yolo to say why. The case that motivated this file is a Homebrew
// install, whose jail-prefix sources resolve into $(brew --prefix)/Cellar — outside the
// default list, and shared only if the user followed the guide's init command.
//
// A FIXED RULE IS WRONG, which is why this reads the machine instead of hardcoding
// "/Users, /private, /var/folders": the user guide tells Homebrew users to add Cellar,
// and a fixed rule would refuse exactly the users who did.
//
// WHERE THE LIST COMES FROM. Not `podman machine inspect`: its output struct
// (machine.InspectInfo) has had no Mounts field in podman 4, 5 or 6 — only the unused
// InternalInspectInfo carries one — so `--format '{{range .Mounts}}…'` is a template
// error. The list lives in the machine's own config file, which inspect locates:
//
//	podman 5/6: InspectInfo.ConfigDir.Path + "/" + Name + ".json"
//	podman 4:   InspectInfo.ConfigPath.Path (the file itself)
//
// and whose MachineConfig.Mounts entries carry Source and Target (read from podman's
// pkg/machine/vmconfigs/config.go and cmd/podman/machine/inspect.go, v4.9.0 through
// main, 2026-09-28).
//
// TRI-STATE, NEVER A GUESS. Every step that cannot be read — no answer, an unrecognized
// shape, an ambiguous active machine, an empty list — yields ok=false, and callers stay
// silent on it. A refusal is issued only on a list actually read.

// MachineShare is one folder a Podman Machine shares: Source on the Mac, Target in the VM.
type MachineShare struct {
	Source   string
	Target   string
	ReadOnly bool
}

// MachineShares is the active machine and the folders it shares.
type MachineShares struct {
	// Machine is the machine's name, as `podman machine init <name>` spells it.
	Machine string
	// Shares is its share list, in the order the config file records it.
	Shares []MachineShare
}

// MachineShareRunner runs one podman probe and returns its stdout, ok=false when it did
// not run, timed out or exited non-zero. Both CLIs adapt their own Exec seam to it.
type MachineShareRunner func(argv []string) (string, bool)

// MachineShareProbeTimeout bounds each podman probe the share read makes.
const MachineShareProbeTimeout = 10 * time.Second

// ReadMachineShares finds the Podman Machine `podman run` would use and reads its share
// list. ok=false means UNKNOWN — the caller must not refuse on it.
//
// CONTAINER_HOST / CONTAINER_CONNECTION redirect podman to a connection this cannot map
// to a machine, so either one set is unknown rather than a guess at the default.
func ReadMachineShares(run MachineShareRunner, getenv func(string) string) (MachineShares, bool) {
	if getenv("CONTAINER_HOST") != "" || getenv("CONTAINER_CONNECTION") != "" {
		return MachineShares{}, false
	}
	listOut, ok := run([]string{"podman", "machine", "list", "--format", "json"})
	if !ok {
		return MachineShares{}, false
	}
	names, def, ok := ParseMachineList(listOut)
	if !ok {
		return MachineShares{}, false
	}
	name := def
	if name == "" {
		// A ROOTFUL machine's default connection is "<name>-root", which `machine list`
		// does not mark Default (it compares the connection name to the machine name), so
		// the connection list is the one that says which machine is active.
		connOut, ok := run([]string{"podman", "system", "connection", "list", "--format", "json"})
		if !ok {
			return MachineShares{}, false
		}
		name, ok = MachineForDefaultConnection(connOut, names)
		if !ok {
			return MachineShares{}, false
		}
	}
	inspectOut, ok := run([]string{"podman", "machine", "inspect", name})
	if !ok {
		return MachineShares{}, false
	}
	configFile, ok := ParseMachineInspectConfigFile(inspectOut)
	if !ok {
		return MachineShares{}, false
	}
	raw, err := os.ReadFile(configFile)
	if err != nil {
		return MachineShares{}, false
	}
	shares, ok := ParseMachineConfigShares(raw)
	if !ok {
		return MachineShares{}, false
	}
	return MachineShares{Machine: name, Shares: shares}, true
}

// ParseMachineList reads `podman machine list --format json`: every machine's name, and
// the one marked Default ("" when none is). ok=false on an unrecognized shape or no
// machines, and when more than one claims Default.
func ParseMachineList(stdout string) (names []string, def string, ok bool) {
	var rows []struct {
		Name    *string
		Default bool
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil || len(rows) == 0 {
		return nil, "", false
	}
	for _, r := range rows {
		if r.Name == nil || *r.Name == "" {
			return nil, "", false
		}
		names = append(names, *r.Name)
		if r.Default {
			if def != "" {
				return nil, "", false
			}
			def = *r.Name
		}
	}
	return names, def, true
}

// MachineForDefaultConnection reads `podman system connection list --format json` and
// returns the machine the default connection belongs to: the one named exactly, or the
// one whose rootful connection ("<name>-root") it is. ok=false when no connection is the
// default or it is not one of machines.
func MachineForDefaultConnection(stdout string, machines []string) (string, bool) {
	var rows []struct {
		Name    string
		Default bool
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		return "", false
	}
	for _, r := range rows {
		if !r.Default {
			continue
		}
		for _, m := range machines {
			if r.Name == m || r.Name == m+"-root" {
				return m, true
			}
		}
		return "", false
	}
	return "", false
}

// ParseMachineInspectConfigFile reads `podman machine inspect <name>` (a JSON array of
// one) and returns the path of that machine's config file. ok=false on any other shape.
func ParseMachineInspectConfigFile(stdout string) (string, bool) {
	type vmFile struct{ Path string }
	var rows []struct {
		Name       string
		ConfigDir  *vmFile // podman 5 and 6
		ConfigPath *vmFile // podman 4
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil || len(rows) != 1 {
		return "", false
	}
	r := rows[0]
	switch {
	case r.ConfigDir != nil && r.ConfigDir.Path != "" && r.Name != "":
		return filepath.Join(r.ConfigDir.Path, r.Name+".json"), true
	case r.ConfigPath != nil && r.ConfigPath.Path != "":
		return r.ConfigPath.Path, true
	}
	return "", false
}

// ParseMachineConfigShares reads a machine config file's Mounts. A Target left empty
// means the same path as Source (podman's own `-v <path>` reading). ok=false when the key
// is missing, null or empty, or an entry has no absolute path: an EMPTY list is treated as
// unknown rather than "shares nothing", because every machine `podman machine init`
// makes has at least the defaults, and a refusal must never rest on a format that moved.
func ParseMachineConfigShares(raw []byte) ([]MachineShare, bool) {
	var cfg struct {
		Mounts []struct {
			Source   string
			Target   string
			ReadOnly bool
		}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Mounts) == 0 {
		return nil, false
	}
	out := make([]MachineShare, 0, len(cfg.Mounts))
	for _, m := range cfg.Mounts {
		target := m.Target
		if target == "" {
			target = m.Source
		}
		if !filepath.IsAbs(m.Source) || !filepath.IsAbs(target) {
			return nil, false
		}
		out = append(out, MachineShare{
			Source: filepath.Clean(m.Source), Target: filepath.Clean(target), ReadOnly: m.ReadOnly,
		})
	}
	return out, true
}

// Reaches reports whether the VM can see path: whether it lies at or under some share's
// TARGET (the path the VM mounts it at, which is where podman looks). Compared by path
// segment, so /Users-other is not inside /Users, and case-insensitively, because a Mac
// volume is case-insensitive by default and a spelling this cannot vouch for must not
// become a refusal.
func (s MachineShares) Reaches(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	p := strings.ToLower(filepath.Clean(path))
	for _, sh := range s.Shares {
		t := strings.ToLower(sh.Target)
		if t == "/" || p == t || strings.HasPrefix(p, t+"/") {
			return true
		}
	}
	return false
}

// Unreachable returns the bind sources the VM cannot see, sorted and de-duplicated. A
// source counts as reachable when EITHER its literal spelling or resolve(source) is: the
// launch hands podman the literal path, and a share of /var/folders serves
// /var/folders/… even though it resolves to /private/var/folders/…; the resolved form
// covers a share recorded through the other spelling. Non-absolute sources are named
// volumes, not host paths, and are skipped; so is anything under /dev, which the VM has
// of its own (the launch's `/dev/null` shadow binds are served by it, not by the Mac).
func (s MachineShares) Unreachable(sources []string, resolve func(string) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, src := range sources {
		if !filepath.IsAbs(src) || seen[src] {
			continue
		}
		if c := filepath.Clean(src); c == "/dev" || strings.HasPrefix(c, "/dev/") {
			continue
		}
		seen[src] = true
		if s.Reaches(src) || (resolve != nil && s.Reaches(resolve(src))) {
			continue
		}
		out = append(out, src)
	}
	sort.Strings(out)
	return out
}

// SuggestedShare is the folder to add to the machine for an unshared path. Inside
// Homebrew's Cellar it is the Cellar itself, as the user guide's init command spells it —
// sharing one versioned keg would break on the next `brew upgrade`. Otherwise the path.
func SuggestedShare(path string) string {
	clean := filepath.Clean(path)
	parts := strings.Split(clean, "/")
	for i, seg := range parts {
		if seg == "Cellar" && i > 0 {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return clean
}

// MachineInitCommand is the `podman machine init` that recreates the machine with its
// current shares plus one share for each unreachable path, in the guide's
// `-v <folder>:<folder>` form. The default machine name is left out, as the guide does.
func (s MachineShares) MachineInitCommand(unreachable []string) string {
	var vs []string
	for _, sh := range s.Shares {
		v := sh.Source + ":" + sh.Target
		if sh.ReadOnly {
			v += ":ro"
		}
		vs = append(vs, "-v "+shellWord(v))
	}
	// One share per folder: a suggestion inside another (a prefix's bin/ inside its
	// bundle) is covered by the outer one, so shortest first and nested ones dropped.
	var adds []string
	for _, p := range unreachable {
		adds = append(adds, SuggestedShare(p))
	}
	sort.Slice(adds, func(i, j int) bool { return len(adds[i]) < len(adds[j]) })
	var added MachineShares
	for _, a := range adds {
		if added.Reaches(a) {
			continue
		}
		added.Shares = append(added.Shares, MachineShare{Source: a, Target: a})
		vs = append(vs, "-v "+shellWord(a+":"+a))
	}
	cmd := "podman machine init"
	if s.Machine != "" && s.Machine != "podman-machine-default" {
		cmd += " " + shellWord(s.Machine)
	}
	return cmd + " " + strings.Join(vs, " ")
}

// shellWord single-quotes w when it carries anything a shell would split or expand.
func shellWord(w string) string {
	if w != "" && strings.IndexFunc(w, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("/._-:+@%=,", r))
	}) < 0 {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

// UnsharedRefusal is the text a launch prints when it refuses on unreachable sources, and
// the detail `yolo check` shows beside its FAIL: the paths, the machine, and the command
// that fixes it. The user guide section it names says why a machine has to be recreated.
func (s MachineShares) UnsharedRefusal(unreachable []string) string {
	name := s.Machine
	if name == "" {
		name = "podman-machine-default"
	}
	rm := "podman machine stop && podman machine rm"
	if name != "podman-machine-default" {
		rm = "podman machine stop " + shellWord(name) + " && podman machine rm " + shellWord(name)
	}
	var cur []string
	for _, sh := range s.Shares {
		cur = append(cur, sh.Target)
	}
	return "The Podman Machine '" + name + "' does not share these folders with its VM, so\n" +
		"podman would fail with `statfs …: no such file or directory` before the jail starts:\n" +
		"  " + strings.Join(unreachable, "\n  ") + "\n" +
		"It shares: " + strings.Join(cur, ", ") + ".\n" +
		"A machine's shared folders are fixed when it is created. Recreate it with the folder\n" +
		"added (this deletes the machine's images and containers; keep any --cpus, --memory\n" +
		"and --disk-size options you use):\n" +
		"  " + rm + "\n" +
		"  " + s.MachineInitCommand(unreachable) + "\n" +
		"  podman machine start\n" +
		"See userguide/guides/macos.md — Podman on a Mac."
}

// ResolveThroughExisting resolves the symlinks in path's longest EXISTING ancestor and
// re-appends the rest. filepath.EvalSymlinks alone fails outright on a path not created
// yet, and the one it would have resolved is the case that matters on a Mac: a
// host-services socket dir under /tmp, made after the check runs, is /private/tmp/… to
// the VM — shared by default — while its literal /tmp spelling is not.
func ResolveThroughExisting(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	p := filepath.Clean(path)
	var rest []string
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				r = filepath.Join(r, rest[i])
			}
			return r
		}
		parent := filepath.Dir(p)
		if parent == p {
			return path
		}
		rest = append(rest, filepath.Base(p))
		p = parent
	}
}
