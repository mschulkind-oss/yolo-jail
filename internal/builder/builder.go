// Package builder is the Go port of the PURE generators in src/cli/builder.py —
// the ssh_config block, nix builders line, trusted-users merge, and the
// single-sudo root script that `yolo builder setup` runs. These byte-exact
// strings are what the Mac builder runbooks depend on (Stage 14 builder slice).
//
// Only the pure string/logic functions are ported here (fully unit-testable);
// the socket probe (TCP 31022) + PID/killpg lifecycle + sudo piping stay in the
// front-door wiring / delegated Python until the macos backend port lands
// (§13), since they're macOS-side and covered by the Mac runbooks.
//
// Source of truth: src/cli/builder.py.
package builder

import (
	"strings"
)

// Frozen constants (byte-identical to builder.py).
const (
	BuilderSSHHost = "linux-builder"
	BuilderPort    = 31022
	BuilderUser    = "builder"
	BuilderKeyPath = "/etc/nix/builder_ed25519"
	sshConfigPath  = "/etc/ssh/ssh_config.d/100-linux-builder.conf"
)

// SSHConfigBlock is the /etc/ssh/ssh_config.d block letting the daemon ssh the
// VM. Mirrors ssh_config_block byte-for-byte.
func SSHConfigBlock() string {
	return "Host " + BuilderSSHHost + "\n" +
		"  Hostname localhost\n" +
		"  HostKeyAlias " + BuilderSSHHost + "\n" +
		"  Port " + itoa(BuilderPort) + "\n" +
		"  User " + BuilderUser + "\n" +
		"  IdentityFile " + BuilderKeyPath + "\n"
}

// NixBuildersLine is the builders + builders-use-substitutes lines for
// nix.conf. Mirrors nix_builders_line(maxJobs).
func NixBuildersLine(maxJobs int) string {
	return "builders = ssh-ng://" + BuilderUser + "@" + BuilderSSHHost + " " +
		"aarch64-linux " + BuilderKeyPath + " " + itoa(maxJobs) + " - - - -\n" +
		"builders-use-substitutes = true\n"
}

// TrustedUsersLine returns a merged trusted-users line adding `me`, or ""
// (Python None) if already trusted (me present, or @admin/@wheel covers it).
// Preserves root + every existing entry, dedup keeping order. Mirrors
// trusted_users_line.
func TrustedUsersLine(current []string, me string) (string, bool) {
	have := map[string]struct{}{}
	for _, c := range current {
		have[c] = struct{}{}
	}
	if _, ok := have[me]; ok {
		return "", false
	}
	if _, ok := have["@admin"]; ok {
		return "", false
	}
	if _, ok := have["@wheel"]; ok {
		return "", false
	}
	merged := dedupKeepOrder(append([]string{"root"}, append(append([]string{}, current...), me)...))
	return "trusted-users = " + strings.Join(merged, " "), true
}

// SetupRootScript builds the single-sudo root script `yolo builder setup` runs.
// Mirrors setup_root_script exactly (guarded builders append, optional
// trusted-users merge, ssh alias write, daemon kickstart). `label` is the
// nix-daemon launchd label (caller resolves it; default org.nixos.nix-daemon).
func SetupRootScript(maxJobs int, me string, currentTrusted []string, confPath, label string) string {
	if label == "" {
		label = "org.nixos.nix-daemon"
	}
	tuLine, hasTU := TrustedUsersLine(currentTrusted, me)

	var lines []string
	lines = append(lines,
		"set -euo pipefail",
		"",
		"# 1. Offload aarch64-linux builds to the builder VM (guard: skip if present).",
		"if ! grep -qs 'ssh-ng://"+BuilderUser+"@"+BuilderSSHHost+"' "+confPath+"; then",
		"  cat >> "+confPath+" <<'YOLO_EOF'",
		strings.TrimRight(NixBuildersLine(maxJobs), "\n"),
		"YOLO_EOF",
		"fi",
	)
	if hasTU {
		lines = append(lines,
			"",
			"# 2. Trust this user to hand the daemon a builder (merged, not clobbered).",
			"cat >> "+confPath+" <<'YOLO_EOF'",
			tuLine,
			"YOLO_EOF",
		)
	}
	lines = append(lines,
		"",
		"# 3. ssh host alias so the daemon can reach the VM.",
		"mkdir -p "+sshConfigDir(),
		"cat > "+sshConfigPath+" <<'YOLO_EOF'",
		strings.TrimRight(SSHConfigBlock(), "\n"),
		"YOLO_EOF",
		"",
		"# 4. Apply: restart the nix-daemon.",
		"launchctl kickstart -k system/"+label,
	)
	return strings.Join(lines, "\n") + "\n"
}

func sshConfigDir() string {
	if i := strings.LastIndexByte(sshConfigPath, '/'); i > 0 {
		return sshConfigPath[:i]
	}
	return sshConfigPath
}

func dedupKeepOrder(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
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
