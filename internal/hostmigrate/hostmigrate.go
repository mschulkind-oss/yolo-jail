// Package hostmigrate retires host-side artifacts left behind by the pre-Go
// (Python) yolo-jail distribution, so `go install ./cmd/yolo` can land its
// binary.
//
// Background: yolo-jail used to ship as a Python package installed with `uv
// tool install`, which put four console scripts on PATH as symlinks into a
// venv: yolo, yolo-ps, yolo-host-processes, yolo-claude-oauth-broker-host.
// After the Go port those symlinks are dead weight, and the one named `yolo`
// actively breaks the build — `go install` refuses to write over a GOBIN
// target that is not a Go object file, and fails with the decidedly
// non-obvious:
//
//	build output "…/.local/bin/yolo" already exists and is not an object file
//
// Contracts:
//   - Only entries whose base name is in LegacyNames are ever considered.
//   - Only entries positively identified as stale Python (a symlink into a
//     directory holding pyvenv.cfg, a broken symlink, or a file with a python
//     shebang) are removed. Anything else is reported and left alone — this
//     package never deletes a file it cannot explain.
//   - Idempotent: on a clean host it finds nothing and prints nothing.
package hostmigrate

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LegacyNames are the console scripts the Python distribution installed. Only
// these names are eligible for retirement.
var LegacyNames = []string{
	"yolo",
	"yolo-ps",
	"yolo-host-processes",
	"yolo-claude-oauth-broker-host",
}

// Kind classifies what is sitting at a candidate path.
type Kind int

const (
	// KindAbsent — nothing there.
	KindAbsent Kind = iota
	// KindPythonVenvLink — symlink into a Python virtualenv (uv, pipx, plain
	// venv: all of them drop a pyvenv.cfg at the venv root).
	KindPythonVenvLink
	// KindBrokenLink — dangling symlink; resolves to nothing, so it is dead
	// weight no matter what it once pointed at.
	KindBrokenLink
	// KindPythonScript — regular file with a python shebang.
	KindPythonScript
	// KindGoBinary — a Go binary. `go install` overwrites these happily.
	KindGoBinary
	// KindUnknown — something we cannot explain. Never removed.
	KindUnknown
)

// Stale reports whether this kind is a pre-Go artifact safe to remove.
func (k Kind) Stale() bool {
	return k == KindPythonVenvLink || k == KindBrokenLink || k == KindPythonScript
}

func (k Kind) String() string {
	switch k {
	case KindAbsent:
		return "absent"
	case KindPythonVenvLink:
		return "python venv symlink"
	case KindBrokenLink:
		return "broken symlink"
	case KindPythonScript:
		return "python script"
	case KindGoBinary:
		return "go binary"
	default:
		return "unrecognized file"
	}
}

// Migrator performs one migration pass. The function fields are injection
// points so tests can drive the logic without a real uv or go toolchain.
type Migrator struct {
	// GOBIN is the directory `go install` writes to.
	GOBIN string
	// IsGoBinary reports whether a path is a Go-built executable.
	IsGoBinary func(string) bool
	// LookPath resolves an executable name, as exec.LookPath does.
	LookPath func(string) (string, error)
	// Exec runs a command and returns its combined output.
	Exec func(name string, args ...string) ([]byte, error)
	// Out receives human-readable progress. Nil discards it.
	Out io.Writer
}

// New returns a Migrator wired to the real host.
func New(gobin string) *Migrator {
	return &Migrator{
		GOBIN:      gobin,
		IsGoBinary: isGoBinary,
		LookPath:   exec.LookPath,
		Exec: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		Out: os.Stderr,
	}
}

// Result records what a pass did.
type Result struct {
	// UvUninstalled is true if the `yolo-jail` uv tool was uninstalled.
	UvUninstalled bool
	// Removed lists paths that were deleted.
	Removed []string
	// Blocked is a path that would break `go install` but that we refused to
	// delete because we could not identify it. Empty when there is none.
	Blocked string
}

// Clean reports whether the host needs no further attention.
func (r Result) Clean() bool {
	return !r.UvUninstalled && len(r.Removed) == 0 && r.Blocked == ""
}

func (m *Migrator) logf(format string, args ...any) {
	if m.Out == nil {
		return
	}
	fmt.Fprintf(m.Out, format+"\n", args...)
}

// Preflight retires the Python distribution and clears stale console scripts
// out of GOBIN. It is safe to run on a host that never had the Python
// version, and safe to run repeatedly.
//
// It returns a non-nil error only for an unidentifiable blocker at
// GOBIN/yolo — the one case where proceeding to `go install` would fail with
// an opaque message and where guessing could destroy something of the user's.
func (m *Migrator) Preflight() (Result, error) {
	var res Result

	if m.uninstallUvTool() {
		res.UvUninstalled = true
	}

	for _, name := range LegacyNames {
		path := filepath.Join(m.GOBIN, name)
		kind := m.classify(path)
		switch {
		case kind.Stale():
			if err := os.Remove(path); err != nil {
				m.logf("  ⚠ could not remove stale %s (%s): %v", path, kind, err)
				continue
			}
			res.Removed = append(res.Removed, path)
			m.logf("  retired legacy %s (%s)", path, kind)
		case kind == KindUnknown && name == "yolo":
			// Only `yolo` blocks the build; the other three are simply left
			// alone if we cannot explain them.
			res.Blocked = path
		}
	}

	if res.Blocked != "" {
		return res, fmt.Errorf(
			"%s already exists and is not a Go binary, so `go install` cannot replace it.\n"+
				"  yolo-jail could not identify it, so it has been left untouched.\n"+
				"  Inspect it and remove it by hand, then re-run:\n"+
				"      ls -l %s && file %s",
			res.Blocked, res.Blocked, res.Blocked)
	}
	return res, nil
}

// uninstallUvTool removes the Python distribution at its source. Unlinking
// the console scripts alone is not enough: the tool stays registered, keeps
// showing up in `uv tool list`, and `uv tool upgrade` would recreate every
// symlink we just deleted.
func (m *Migrator) uninstallUvTool() bool {
	if m.LookPath == nil || m.Exec == nil {
		return false
	}
	if _, err := m.LookPath("uv"); err != nil {
		return false
	}
	out, err := m.Exec("uv", "tool", "list")
	if err != nil {
		return false
	}
	if !uvToolListed(string(out), "yolo-jail") {
		return false
	}
	if out, err := m.Exec("uv", "tool", "uninstall", "yolo-jail"); err != nil {
		m.logf("  ⚠ `uv tool uninstall yolo-jail` failed: %v\n%s", err, strings.TrimSpace(string(out)))
		return false
	}
	m.logf("  retired legacy Python install (uv tool yolo-jail)")
	return true
}

// uvToolListed reports whether `uv tool list` output declares the named tool.
// Tool names start a line ("yolo-jail v0.6.1"); the console scripts they own
// are listed beneath, indented with a leading "- ".
func uvToolListed(out, tool string) bool {
	for _, line := range strings.Split(out, "\n") {
		if line == tool || strings.HasPrefix(line, tool+" ") {
			return true
		}
	}
	return false
}

// classify identifies what is at path, without modifying anything.
func (m *Migrator) classify(path string) Kind {
	fi, err := os.Lstat(path)
	if err != nil {
		return KindAbsent
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return KindUnknown
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		if _, err := os.Stat(target); err != nil {
			return KindBrokenLink
		}
		if inPythonVenv(target) {
			return KindPythonVenvLink
		}
		return m.goOrUnknown(path)
	}

	if fi.Mode().IsRegular() {
		if hasPythonShebang(path) {
			return KindPythonScript
		}
		return m.goOrUnknown(path)
	}

	return KindUnknown
}

func (m *Migrator) goOrUnknown(path string) Kind {
	if m.IsGoBinary != nil && m.IsGoBinary(path) {
		return KindGoBinary
	}
	return KindUnknown
}

// inPythonVenv reports whether exe lives in the bin/ of a Python virtualenv.
// Every venv builder — uv, pipx, python -m venv, virtualenv — writes a
// pyvenv.cfg at the venv root, which makes this independent of which one
// installed it and of where the venv happens to live.
func inPythonVenv(exe string) bool {
	binDir := filepath.Dir(exe)
	switch filepath.Base(binDir) {
	case "bin", "Scripts":
	default:
		return false
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(binDir), "pyvenv.cfg"))
	return err == nil
}

// hasPythonShebang reports whether path begins with a python shebang.
func hasPythonShebang(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	return strings.HasPrefix(line, "#!") && strings.Contains(line, "python")
}

// isGoBinary reports whether path is a Go-built executable, per `go version`.
// Note that `go version <file>` exits 0 even for non-Go files (it reports
// "could not read Go build info" on stderr), so the output has to be parsed
// rather than the exit code trusted.
func isGoBinary(path string) bool {
	out, err := exec.Command("go", "version", path).CombinedOutput()
	if err != nil {
		return false
	}
	_, ver, ok := strings.Cut(string(out), ": ")
	return ok && strings.HasPrefix(strings.TrimSpace(ver), "go1")
}

// DefaultGOBIN resolves the directory `go install` writes to: $GOBIN when
// set, otherwise $GOPATH/bin. It asks the go tool rather than reading the
// environment, so it honours the same precedence go itself applies.
func DefaultGOBIN() (string, error) {
	if out, err := exec.Command("go", "env", "GOBIN").Output(); err == nil {
		if dir := strings.TrimSpace(string(out)); dir != "" {
			return dir, nil
		}
	}
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return "", fmt.Errorf("resolving GOPATH: %w", err)
	}
	gopath := strings.TrimSpace(string(out))
	if gopath == "" {
		return "", fmt.Errorf("neither GOBIN nor GOPATH is set")
	}
	return filepath.Join(gopath, "bin"), nil
}
