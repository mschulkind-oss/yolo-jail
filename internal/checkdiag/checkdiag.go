// Package checkdiag is the Go port of the PURE diagnostic helpers of
// src/cli/check_cmd.py — the nix-build-failure classifier, the dry-run
// "will be built" stderr parser (a tri-state: build / substitutable /
// inconclusive), the /etc/nix builders-config parser that decides whether an
// aarch64-linux builder is reachable, the credentials-freshness duration
// formatter, and the Linux-builder remedy template. These carry byte-exact
// output + tri-state polarity contracts (Stage 15's check command); the
// subprocess wrappers that feed them stay in the check wiring.
package checkdiag

import (
	"regexp"
	"strings"
)

// WillBuild is the tri-state result of a `nix build --dry-run` parse. Unknown
// (inconclusive: offline / substituter unreachable / dry-run errored) must NEVER
// be treated as a cache miss — offline makes everything look built. Mirrors the
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
// subprocess ran at all (false → the caller already returned Unknown). Mirrors
// _nix_dry_run_will_build's stderr handling:
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
// remediation) pair. remedy is the resolved _linux_builder_remedy() (caller
// substitutes the daemon label). isMacOS gates the ambiguous "dependency
// failed" branch. Mirrors _diagnose_nix_build_failure.
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
// usable aarch64-linux builder with a non-zero job slot is configured. Mirrors
// _has_linux_builder's parse. readMachines(path) returns the file's lines and
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

// FmtDuration formats a second count the way _check_broker_creds_freshness's
// nested _fmt does: negative → "?"; < 3600 → "<m>m"; else "<h>h<m>m". Mirrors
// _fmt exactly (integer division; no zero-padding).
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

// linuxBuilderRemedyTemplate is the builder remedy with a NIX_DAEMON_LABEL
// placeholder. LinuxBuilderRemedy substitutes the resolved label. Verbatim from
// _LINUX_BUILDER_REMEDY_TEMPLATE.
const linuxBuilderRemedyTemplate = "The jail image is a Linux image; part of it must be built from source, " +
	"and macOS can't build Linux locally — it offloads to a small Linux VM.  " +
	"Set that up ONCE, then yolo starts/stops it for you on demand (no VM to " +
	"babysit):\n" +
	"\n" +
	"  1. Run:  yolo builder setup\n" +
	"     (It prints the one privileged step — wiring the Nix daemon to the\n" +
	"      builder VM — for you to review and run.)\n" +
	"  2. Ensure YOUR user is trusted by the Nix daemon (only 'trusted' users\n" +
	"     may hand it a builder to offload to; this grants nix-build trust to\n" +
	"     your login user, NOT general admin — the `sudo` is only because\n" +
	"     /etc/nix/nix.conf is root-owned):\n" +
	"       echo \"trusted-users = root $(whoami)\" | sudo tee -a /etc/nix/nix.conf\n" +
	"       sudo launchctl kickstart -k system/NIX_DAEMON_LABEL\n" +
	"  3. Run `yolo` again — it auto-starts the builder, builds, and starts " +
	"the jail.\n" +
	"\n" +
	"Steps 1–2 are one-time (verified by `yolo check` / `yolo builder " +
	"status`).  From then on the builder is on-demand: yolo brings it up " +
	"before a build and a launchd idle-timer stops it, so it doesn't hold " +
	"RAM while you're not building.\n" +
	"(If you added a custom `packages` entry: a {version,url,hash} override " +
	"is never cached, so a rebuild is unavoidable; a {nixpkgs:<commit>} pin " +
	"may just need a released revision that IS in the cache.)"

// LinuxBuilderRemedy returns the remedy with the daemon-restart label filled in.
// label is the resolved nix-daemon launchd label (caller passes
// storage.DetectNixDaemonLabel() or "org.nixos.nix-daemon"). Mirrors
// _linux_builder_remedy.
func LinuxBuilderRemedy(label string) string {
	if label == "" {
		label = "org.nixos.nix-daemon"
	}
	return strings.ReplaceAll(linuxBuilderRemedyTemplate, "NIX_DAEMON_LABEL", label)
}
