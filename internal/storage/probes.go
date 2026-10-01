// Package storage provides the host-side filesystem-state plumbing — the
// set-up that runs before any container starts, plus the pure host-state probes
// run() and check use (nix installer detection, timezone inheritance, workspace
// login-state sync).
package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LinuxMultilib returns the Linux multilib directory name for the current
// architecture. The container is always Linux; the arch matches the host
// (native, not emulated).
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

// The host paths the Nix probes below are handed by their callers, which may read them under a
// different root (check's tests do) while still printing these.
const (
	// NixConfDir holds nix.conf and, where an install has one, nix.custom.conf.
	NixConfDir = "/etc/nix"
	// LaunchDaemonsDir holds a Mac's launchd daemon plists, each named for its label.
	LaunchDaemonsDir = "/Library/LaunchDaemons"
)

// NixDistribution is which Nix a host runs, as `nix --version` names it.
type NixDistribution int

const (
	// NixUnknown is a `nix --version` line neither distribution prints (a fork such as Lix), or
	// no answer at all.
	NixUnknown NixDistribution = iota
	// NixUpstream is the Nix nixos.org releases, whichever installer put it there.
	NixUpstream
	// NixDeterminate is Determinate Nix: Determinate Systems' build of Nix, whose daemon is
	// determinate-nixd and whose /etc/nix/nix.conf that daemon owns and replaces.
	NixDeterminate
)

func (d NixDistribution) String() string {
	switch d {
	case NixUpstream:
		return "upstream Nix"
	case NixDeterminate:
		return "Determinate Nix"
	}
	return "unknown Nix"
}

// The launchd labels of the two distributions' daemons on a Mac, each also its plist's filename
// stem in LaunchDaemonsDir. Every upstream installer uses the first and Determinate Nix the
// second; the Determinate installer's systems.determinate.nix-installer.nix-hook is not a daemon.
const (
	UpstreamNixDaemonLabel    = "org.nixos.nix-daemon"
	DeterminateNixDaemonLabel = "systems.determinate.nix-daemon"
)

// DaemonLabel is the launchd label this distribution's daemon is installed under, "" for an
// unknown one.
func (d NixDistribution) DaemonLabel() string {
	switch d {
	case NixUpstream:
		return UpstreamNixDaemonLabel
	case NixDeterminate:
		return DeterminateNixDaemonLabel
	}
	return ""
}

// NixVersion is what `nix --version` says about the installed Nix.
type NixVersion struct {
	Distribution NixDistribution
	// Version is the distribution's own version: Determinate Nix's ("3.22.5") or upstream's
	// ("2.34.7"). "" for an unknown distribution.
	Version string
	// Nix is the upstream Nix version the binary is built from: Determinate Nix's second field
	// ("2.35.2"), and Version itself on upstream Nix.
	Nix string
	// Line is the first line of the output, trimmed.
	Line string
}

// ParseNixVersion reads `nix --version` output. Determinate Nix prints
// `nix (Determinate Nix 3.22.5) 2.35.2` and upstream Nix `nix (Nix) 2.34.7` (nix-src
// src/libmain/shared.cc); anything else is NixUnknown, kept whole in Line.
func ParseNixVersion(out string) NixVersion {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	line = strings.TrimSpace(line)
	v := NixVersion{Line: line}
	if rest, ok := strings.CutPrefix(line, "nix (Determinate Nix "); ok {
		own, base, ok := strings.Cut(rest, ") ")
		if ok && own != "" && base != "" && !strings.ContainsAny(own+base, " ()") {
			v.Distribution, v.Version, v.Nix = NixDeterminate, own, base
		}
		return v
	}
	if rest, ok := strings.CutPrefix(line, "nix (Nix) "); ok && rest != "" && !strings.ContainsAny(rest, " ()") {
		v.Distribution, v.Version, v.Nix = NixUpstream, rest, rest
	}
	return v
}

// Describe is the version as a person reads it: "Determinate Nix 3.22.5 (based on Nix 2.35.2)",
// upstream's bare version, or an unknown Nix's line verbatim.
func (v NixVersion) Describe() string {
	switch v.Distribution {
	case NixDeterminate:
		return "Determinate Nix " + v.Version + " (based on Nix " + v.Nix + ")"
	case NixUpstream:
		return v.Version
	}
	return v.Line
}

// NixCustomConfTakesEffect reports whether a `key = …` line appended to the nix.custom.conf
// beside confPath would take effect: confPath includes that file, and assigns key itself
// nowhere after its last include of it. nix reads an include where it stands and keeps a
// setting's LAST assignment, so a nix.conf setting the key again below the include overrides the
// custom file's line (measured on a real Mac: docs/plans/runbooks/mac-sandvault-session.md §5).
// An `extra-<key>` line appends rather than assigns, so it does not count.
//
// known is false when confPath cannot be read (missing, a directory, a permission error).
func NixCustomConfTakesEffect(confPath, key string) (effective, known bool) {
	info, err := os.Stat(confPath)
	if err != nil || info.IsDir() {
		return false, false
	}
	data, err := os.ReadFile(confPath)
	if err != nil {
		return false, false
	}
	included := false
	for _, raw := range strings.Split(string(data), "\n") {
		stripped := strings.TrimSpace(raw)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		if includesCustomConf(stripped, confPath) {
			included = true
			continue
		}
		if name, _, ok := strings.Cut(stripped, "="); ok && strings.TrimSpace(name) == key {
			included = false // assigned again after any include above it
		}
	}
	return included, true
}

// includesCustomConf reports whether one stripped, uncommented nix.conf line includes the
// nix.custom.conf beside confPath.
func includesCustomConf(stripped, confPath string) bool {
	// Match both "include" (fatal if missing) and "!include" (non-fatal).
	// Order matters: check "!include" first so "!include X" doesn't match
	// the "include" prefix against the leading '!'.
	for _, prefix := range []string{"!include", "include"} {
		if strings.HasPrefix(stripped, prefix) {
			rest := strings.TrimLeft(stripped[len(prefix):], " \t\r\f\v")
			// nix resolves a relative include against the including file's directory,
			// and the Determinate installer writes exactly that: `!include nix.custom.conf`.
			target := rest
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(confPath), target)
			}
			return rest == "/etc/nix/nix.custom.conf" ||
				filepath.Clean(target) == filepath.Join(filepath.Dir(confPath), "nix.custom.conf")
		}
	}
	return false
}

// NixDaemonLabelIn returns the launchd label of a nix daemon installed in daemonDir (the
// plist's filename stem), or ("", false) if none. prefer wins when its plist is there, since a
// switch between distributions can leave both daemons' plists behind; otherwise the first in
// sorted order.
func NixDaemonLabelIn(daemonDir, prefer string) (string, bool) {
	entries, err := os.ReadDir(daemonDir)
	if err != nil {
		return "", false
	}
	first := ""
	// os.ReadDir returns entries sorted by name.
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "nix-daemon.plist") {
			continue
		}
		// stem = filename without its final extension.
		label := strings.TrimSuffix(name, filepath.Ext(name))
		if prefer != "" && label == prefer {
			return label, true
		}
		if first == "" {
			first = label
		}
	}
	return first, first != ""
}

// DetectHostTimezone returns the host's IANA timezone name, or ("", false) if
// none could be detected. Detection order
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
