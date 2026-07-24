// Package nixdiag provides the shared nix build-failure classifier used by both
// the check and run slices — the nix-build-failure classifier, the dry-run
// "will be built" stderr parser (a tri-state: build / substitutable /
// inconclusive), the /etc/nix builders-config parser that decides whether an
// aarch64-linux builder is reachable, the credentials-freshness duration
// formatter, and the Linux-builder remedy.
package nixdiag

import (
	"regexp"
	"strconv"
	"strings"
)

// WillBuild is the tri-state result of a `nix build --dry-run` parse. Unknown
// (inconclusive: offline / substituter unreachable / dry-run errored) must NEVER
// be treated as a cache miss — offline makes everything look built.
// Optional[bool] of _nix_dry_run_will_build.
type WillBuild int

const (
	// WillBuildUnknown: inconclusive — the caller must not act as if it were a
	// miss.
	WillBuildUnknown WillBuild = iota
	// WillBuildYes: nix's plan lists derivations that will be BUILT.
	WillBuildYes
	// WillBuildNo: everything is substitutable.
	WillBuildNo
)

var willBuildRe = regexp.MustCompile(`(?m)^(this derivation|these \d+ derivations) will be built:`)

// ParseDryRunWillBuild classifies `nix build --dry-run` output. returncode is
// the process exit; stderr is its captured stderr; ok reports whether the
// subprocess ran at all (false → the caller already returned Unknown). Stderr
// handling:
//   - subprocess failed to run (ok=false) → (Unknown, nil)
//   - non-zero exit AND no "will be built" header → (Unknown, nil)
//   - no header → (No, nil)
//   - header present → (Yes, offending .drv basenames under the header)
//
// The offending scan starts after the header line and stops at the first blank
// line or a "will be fetched" line, collecting lines ending in ".drv" (basename
// after the last "/").
func ParseDryRunWillBuild(returncode int, stderr string, ok bool) (WillBuild, []string) {
	if !ok {
		return WillBuildUnknown, nil
	}
	if returncode != 0 && !willBuildRe.MatchString(stderr) {
		return WillBuildUnknown, nil
	}
	if !willBuildRe.MatchString(stderr) {
		return WillBuildNo, nil
	}
	var offending []string
	inBuild := false
	for _, line := range strings.Split(stderr, "\n") {
		// Python re.match anchors at the start of each line; willBuildRe is
		// multiline-anchored, so test the line in isolation.
		if willBuildRe.MatchString(line) {
			inBuild = true
			continue
		}
		if inBuild {
			s := strings.TrimSpace(line)
			if strings.HasSuffix(s, ".drv") {
				if i := strings.LastIndex(s, "/"); i >= 0 {
					offending = append(offending, s[i+1:])
				} else {
					offending = append(offending, s)
				}
			} else if s == "" || strings.Contains(line, "will be fetched") {
				inBuild = false
			}
		}
	}
	return WillBuildYes, offending
}

// DiagnoseNixBuildFailure turns opaque nix build stderr into a (title,
// remediation) pair. remedy is the resolved LinuxBuilderRemedy(). isMacOS gates
// the ambiguous "dependency failed" branch.
func DiagnoseNixBuildFailure(stderrTail []string, isMacOS bool, remedy string) (title, remediation string) {
	text := strings.Join(stderrTail, "\n")
	low := strings.ToLower(text)

	explicitCross := (strings.Contains(low, "required to build") && strings.Contains(low, "aarch64-linux")) ||
		(strings.Contains(low, "cannot build") && strings.Contains(low, "aarch64-linux"))
	ambiguousMac := isMacOS && strings.Contains(low, "dependency failed") && !explicitCross

	if explicitCross {
		return "Image build needs a Linux builder",
			"Part of the image isn't in the binary cache and must be built.\n" + remedy
	}
	if ambiguousMac {
		return "Image build needs a Linux builder (or a cached package)",
			"A Linux derivation had to be built from source and couldn't be.\n" + remedy
	}
	// Fallback: the last 10 stderr lines (or empty).
	if len(stderrTail) == 0 {
		return "nix build failed", ""
	}
	tail := stderrTail
	if len(tail) > 10 {
		tail = tail[len(tail)-10:]
	}
	return "nix build failed", strings.Join(tail, "\n")
}

// HasLinuxBuilderFromConfig parses `nix config show` output plus any
// @/etc/nix/machines files (supplied via readMachines) to decide whether a
// usable aarch64-linux builder with a non-zero job slot is configured.
// readMachines(path) returns the file's lines and
// whether it was readable; pass a loader that reads real files (or a stub).
func HasLinuxBuilderFromConfig(nixConfigShow string, readMachines func(path string) ([]string, bool)) bool {
	var builderLines []string
	for _, line := range strings.Split(nixConfigShow, "\n") {
		if strings.HasPrefix(line, "builders =") {
			spec := strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
			for _, part := range strings.Split(spec, ";") {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "@") {
					if readMachines != nil {
						if lines, ok := readMachines(part[1:]); ok {
							builderLines = append(builderLines, lines...)
						}
					}
				} else if part != "" {
					builderLines = append(builderLines, part)
				}
			}
		}
	}
	for _, entry := range builderLines {
		entry = strings.TrimSpace(entry)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		fields := strings.Fields(entry)
		var systems []string
		if len(fields) > 1 {
			systems = strings.Split(fields[1], ",")
		}
		maxJobs := "1"
		if len(fields) > 3 {
			maxJobs = fields[3]
		}
		if contains(systems, "aarch64-linux") && maxJobs != "0" {
			return true
		}
	}
	return false
}

func contains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// FmtDuration formats a second count: negative → "?"; < 3600 → "<m>m"; else
// "<h>h<m>m" (integer division; no zero-padding).
func FmtDuration(seconds int) string {
	if seconds < 0 {
		return "?"
	}
	if seconds < 3600 {
		return itoa(seconds/60) + "m"
	}
	return itoa(seconds/3600) + "h" + itoa((seconds%3600)/60) + "m"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// linuxBuilderRemedyText is the macOS from-source-build remedy. On macOS a
// `packages:` derivation that isn't in the binary cache must be built on Linux,
// which macOS can't do locally; a normal `yolo` run offloads that build
// AUTOMATICALLY to a tiny nix+sshd container on whichever container runtime is
// already up (podman or Apple Container) — no sudo, no VM, no `yolo builder`
// command, zero idle RAM. This text is shown when that automatic offload could
// not run or the offloaded build failed, so it diagnoses the runtime rather
// than telling the user to set a builder up.
const linuxBuilderRemedyText = "The jail image is a Linux image, and a package in it isn't in the binary " +
	"cache, so it must be built from source — which macOS can't do locally.  " +
	"A normal `yolo` run handles that for you AUTOMATICALLY: it offloads the " +
	"build to a tiny Linux builder CONTAINER on whichever runtime is already up " +
	"(podman or Apple Container), then tears it down when the build finishes — " +
	"no sudo, no VM, no `yolo builder` command, and zero idle RAM.\n" +
	"\n" +
	"This message means that automatic offload could not run or the offloaded " +
	"build failed.  The usual cause is that your container runtime isn't running " +
	"(so there's nowhere to start the builder), or the builder image couldn't be " +
	"pulled:\n" +
	"\n" +
	"  1. Make sure your container runtime is up:\n" +
	"       podman:           podman machine start\n" +
	"       Apple Container:  container system start\n" +
	"  2. Run `yolo` again — it starts the builder container, runs the build on " +
	"it, and launches the jail.\n" +
	"\n" +
	"(Already running your OWN Linux builder — nix-darwin `linux-builder`, or a " +
	"machine in /etc/nix/machines?  That keeps working; Nix uses it and yolo " +
	"never starts its own container.)\n" +
	"(If you added a custom `packages` entry: a {version,url,hash} override is " +
	"never cached, so a rebuild is unavoidable; a {nixpkgs:<commit>} pin may " +
	"just need a released revision that IS in the cache.)"

// LinuxBuilderRemedy returns the macOS from-source-build remedy. It takes no
// arguments: the container-builder path needs no nix-daemon restart (the old
// daemon-label substitution was VM-builder setup, now removed).
func LinuxBuilderRemedy() string {
	return linuxBuilderRemedyText
}

// MinFreeFromConfig parses the `min-free` setting out of `nix config show`
// output, returning (bytes, ok). ok is false when the key is absent or
// unparseable. `min-free = 0` (the nix default) means the daemon's automatic
// GC — which frees UNROOTED store paths when a build runs low on space — is
// effectively OFF: with §1 rooting in place, a non-zero min-free is the safety
// net that keeps the store from growing unbounded WITHOUT ever touching a
// running jail's rooted image closure. This is a read-only observation; the
// value is a /etc/nix/nix.conf edit only a human can make.
func MinFreeFromConfig(nixConfigShow string) (int64, bool) {
	return intSettingFromConfig(nixConfigShow, "min-free")
}

// intSettingFromConfig extracts a single integer nix setting ("<key> = <int>")
// from `nix config show` output. Returns (value, ok=false) when the key is
// missing or its value is not a plain integer (e.g. "auto").
func intSettingFromConfig(nixConfigShow, key string) (int64, bool) {
	prefix := key + " ="
	for _, line := range strings.Split(nixConfigShow, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		val := strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
