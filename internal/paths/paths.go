// Package paths mirrors src/cli/paths.py — the module-level constants used
// across the CLI. These are cross-image, cross-language contracts (socket
// names especially: CGD_SOCKET_NAME's Python comment records a real
// regression from re-typing one), so they are pinned by the drift suite and
// must stay byte-identical to the Python source of truth.
package paths

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
)

// Platform predicates (IS_LINUX / IS_MACOS in Python).
var (
	IsLinux = runtime.GOOS == "linux"
	IsMacOS = runtime.GOOS == "darwin"
)

// Container runtimes that build an argv, load an image, and answer `<rt> ps`.
// Iterate this — never AllRuntimes — in container-side code.
var SupportedRuntimes = []string{"podman", "container"}

// Native (non-container) runtimes. macos-user runs under Seatbelt with no VM,
// no Linux image; explicit opt-in only, never auto-detected.
var NativeRuntimes = []string{"macos-user"}

// AllRuntimes is every value the `runtime` config key / YOLO_RUNTIME may take.
var AllRuntimes = append(append([]string{}, SupportedRuntimes...), NativeRuntimes...)

const (
	// JailImage is the fully-qualified image ref; JailImageShort drops the
	// localhost/ prefix Apple Container's CLI doesn't recognize.
	JailImage      = "localhost/yolo-jail:latest"
	JailImageShort = "yolo-jail:latest"

	// JailHostServicesDir is where all host service sockets appear in-jail.
	JailHostServicesDir = "/run/yolo-services"

	// BuiltinCgroupLoopholeName is the reserved cgroup-delegate service name.
	BuiltinCgroupLoopholeName = "cgroup-delegate"
	// BuiltinJournalLoopholeName is the reserved journal-bridge service name.
	BuiltinJournalLoopholeName = "journal"
	// JournalSocketName is the journal bridge's socket filename.
	JournalSocketName = "journal.sock"
)

// CgdSocketName MUST be "<BuiltinCgroupLoopholeName>.sock": the entrypoint
// (baked into the image) and YOLO_SERVICE_CGROUP_DELEGATE_SOCKET both expect
// /run/yolo-services/cgroup-delegate.sock. A refactor once kept the legacy
// "cgroup.sock" name here and every jail silently reported the delegate as
// unavailable (see the Python comment in src/cli/paths.py).
const CgdSocketName = BuiltinCgroupLoopholeName + ".sock"

// Home-relative storage layout. Python computes these from Path.home() at
// import time; Go exposes the fixed suffixes plus helpers that join with the
// caller's home dir, so the constant *strings* are what the drift suite pins
// (they don't vary by host) while the absolute paths resolve at runtime.
const (
	globalStorageSuffix = ".local/share/yolo-jail"
	userConfigSuffix    = ".config/yolo-jail/config.jsonc"
)

// GlobalStorage returns $HOME/.local/share/yolo-jail.
func GlobalStorage() string { return filepath.Join(home(), globalStorageSuffix) }

// GlobalHome returns the shared container /home/agent backing dir.
func GlobalHome() string { return filepath.Join(GlobalStorage(), "home") }

// GlobalMise returns the shared mise data dir.
func GlobalMise() string { return filepath.Join(GlobalStorage(), "mise") }

// GlobalCache returns the shared cache dir.
func GlobalCache() string { return filepath.Join(GlobalStorage(), "cache") }

// ContainerDir returns the tracking-files dir.
func ContainerDir() string { return filepath.Join(GlobalStorage(), "containers") }

// AgentsDir returns the per-jail briefing staging dir.
func AgentsDir() string { return filepath.Join(GlobalStorage(), "agents") }

// BuildDir returns the nix build-root dir.
func BuildDir() string { return filepath.Join(GlobalStorage(), "build") }

// UserConfigPath returns $HOME/.config/yolo-jail/config.jsonc.
func UserConfigPath() string { return filepath.Join(home(), userConfigSuffix) }

// home mirrors Python's Path.home() / os.path.expanduser("~") resolution,
// which the paths constants depend on — NOT Go's os.UserHomeDir(), which reads
// only $HOME and errors when it is unset (audit finding: that made every path
// helper return a RELATIVE path in a stripped environment). Python's rules:
//
//   - $HOME set and non-empty  -> $HOME
//   - $HOME set but empty       -> "/"  (expanduser: userhome="" then `or "/"`)
//   - $HOME unset               -> pwd.getpwuid(getuid()).pw_dir (the passwd
//     database home), and if THAT is empty, "/"
//
// This keeps the paths absolute in cron/systemd/subprocess contexts where the
// CLI may run without $HOME, matching Python.
func home() string {
	h, ok := os.LookupEnv("HOME")
	if ok {
		if h == "" {
			return "/" // Python expanduser: empty HOME -> "/"
		}
		return h
	}
	// HOME unset: fall back to the passwd database (Python's pwd.getpwuid).
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	return "/"
}
