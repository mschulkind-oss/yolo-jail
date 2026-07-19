// Package storage is the Go port of the host-side filesystem-state plumbing in
// src/cli/storage.py — the set-up that runs before any container starts, plus
// the pure host-state probes run() and check use. The subprocess-free probes
// and the claude.json login-state sync carry behavior contracts (which of the
// two nix installers is present, which timezone the jail inherits, how a fresh
// workspace boots already-logged-in), so those are ported byte-exact and tested
// against live Python and real temp dirs.
package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LinuxMultilib returns the Linux multilib directory name for the current
// architecture. The container is always Linux; the arch matches the host
// (native, not emulated). Mirrors _linux_multilib, including the arm64→aarch64
// macOS spelling and the "<machine>-linux-gnu" fallback for unknown machines.
//
// Python reads platform.machine(); Go's runtime.GOARCH uses amd64/arm64, so we
// map to the Python spellings first (the same contract as the startup banner).
func LinuxMultilib() string {
	machine := pythonMachine()
	switch machine {
	case "x86_64":
		return "x86_64-linux-gnu"
	case "aarch64", "arm64":
		return "aarch64-linux-gnu"
	default:
		return machine + "-linux-gnu"
	}
}

// pythonMachine returns platform.machine()'s spelling for the current arch
// (x86_64 / aarch64), NOT Go's amd64/arm64.
func pythonMachine() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}

// NixCustomConfIncluded reports whether /etc/nix/nix.conf includes
// /etc/nix/nix.custom.conf. Returns (true/false, true) when nix.conf is
// readable, (false, false) when it is not (missing file, permission error, non-
// macOS host). Mirrors _nix_custom_conf_included's Optional[bool] — the second
// return is the "known" flag (Python None → ok=false).
func NixCustomConfIncluded() (included bool, ok bool) {
	return nixCustomConfIncludedAt("/etc/nix/nix.conf")
}

// nixCustomConfIncludedAt is the testable core (path injected).
func nixCustomConfIncludedAt(confPath string) (bool, bool) {
	info, err := os.Stat(confPath)
	if err != nil || info.IsDir() {
		return false, false
	}
	data, err := os.ReadFile(confPath)
	if err != nil {
		return false, false
	}
	for _, raw := range strings.Split(string(data), "\n") {
		stripped := strings.TrimSpace(raw)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		// Match both "include" (fatal if missing) and "!include" (non-fatal).
		// Order matters: check "!include" first so "!include X" doesn't match
		// the "include" prefix against the leading '!'.
		for _, prefix := range []string{"!include", "include"} {
			if strings.HasPrefix(stripped, prefix) {
				rest := strings.TrimLeft(stripped[len(prefix):], " \t\r\f\v")
				if rest == "/etc/nix/nix.custom.conf" {
					return true, true
				}
			}
		}
	}
	return false, true
}

// DetectNixDaemonLabel returns the launchd Label of the installed nix-daemon on
// macOS (the plist filename stem), or ("", false) if none. First match wins in
// sorted order. Mirrors _detect_nix_daemon_label.
func DetectNixDaemonLabel() (string, bool) {
	return detectNixDaemonLabelIn("/Library/LaunchDaemons")
}

func detectNixDaemonLabelIn(daemonDir string) (string, bool) {
	entries, err := os.ReadDir(daemonDir)
	if err != nil {
		return "", false
	}
	// os.ReadDir already returns entries sorted by name (matches Python's
	// sorted(iterdir())).
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, "nix-daemon.plist") {
			// stem = filename without its final extension.
			return strings.TrimSuffix(name, filepath.Ext(name)), true
		}
	}
	return "", false
}

// DetectHostTimezone returns the host's IANA timezone name, or ("", false) if
// none could be detected. Detection order mirrors _detect_host_timezone:
// $TZ → /etc/timezone → /etc/localtime symlink suffix after "/zoneinfo/".
// getenv is injected for testability; pass os.Getenv in production.
func DetectHostTimezone() (string, bool) {
	return detectHostTimezone(os.Getenv, "/etc/timezone", "/etc/localtime")
}

func detectHostTimezone(getenv func(string) string, etcTimezone, etcLocaltime string) (string, bool) {
	// 1. Explicit $TZ on host.
	if tz := getenv("TZ"); tz != "" {
		return tz, true
	}
	// 2. /etc/timezone (plain-text zone name).
	if info, err := os.Stat(etcTimezone); err == nil && !info.IsDir() {
		if data, err := os.ReadFile(etcTimezone); err == nil {
			if content := strings.TrimSpace(string(data)); content != "" {
				return content, true
			}
		}
	}
	// 3. /etc/localtime symlink target — zone name is the suffix after
	// "/zoneinfo/".
	if info, err := os.Lstat(etcLocaltime); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(etcLocaltime); err == nil {
			const marker = "/zoneinfo/"
			if idx := strings.Index(target, marker); idx >= 0 {
				return target[idx+len(marker):], true
			}
		}
	}
	return "", false
}
