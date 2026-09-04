package run

import (
	"strings"
)

// LaunchBanner formats the launch line(s) the run pipeline writes to stderr once
// it knows what it is about to start.
//
// It carries ONLY what a launch knows. The version and the platform are NOT
// here: internal/cli's dispatch already wrote them (banner.Startup) before this
// package was entered, and repeating them would print the same two fields twice
// on the single most-used command. That split is also why the version now
// survives a launch that never gets this far — a config parse error, a failed
// nix build, a source-skew refusal — which is the whole reason the startup half
// exists.
//
// jailVersion is the version baked into an ALREADY-RUNNING container (the attach
// path); it is rendered only when it differs from hostVersion, because a host
// CLI attaching to a pre-upgrade jail is running against stale shims, mounts and
// entrypoint, and that is worth a glance. resParts, if non-empty, adds the
// resource-limits line. No trailing newline — the caller terminates it.
func LaunchBanner(runtimeName, cname, hostVersion, jailVersion string, resParts []string) string {
	parts := []string{"Jail: " + cname, runtimeName}
	if jailVersion != "" && jailVersion != hostVersion {
		parts = append(parts, "built at "+jailVersion)
	}
	line := strings.Join(parts, " | ")
	if len(resParts) > 0 {
		line += "\nResource limits: " + strings.Join(resParts, ", ")
	}
	return line
}
