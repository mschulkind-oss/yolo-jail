// Package containerbuilder provides the on-demand container-based Linux builder for
// the macOS container runtimes. When a `packages:` build isn't cached, nix must
// offload to Linux; instead of a second hypervisor, a tiny nix+sshd builder runs
// as a container on the runtime already up, and the host drives it via nix's
// ssh-ng remote-builder protocol. The argv/URI/`builders`-line builders and the
// `container ls` ADDR parse are byte-exact contracts (the nix --builders line is
// as runbook-critical as internal/builder's); the pull/run/wait/stop lifecycle
// and the session context manager stay in the run wiring until the macos_user
// port lands.
package containerbuilder

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Frozen constants (byte-identical to container_builder.py).
const (
	BuilderImage     = "ghcr.io/mschulkind-oss/yolo-jail-builder:latest"
	BuilderContainer = "yolo-linux-builder"
	BuilderSSHUser   = "root"
	BuilderGuestPort = 22
	BuilderHostPort  = 31022
)

// BuilderKeyDir is the per-workspace host-daemon key dir under GLOBAL_STORAGE.
// that reads GLOBAL_STORAGE at import).
func BuilderKeyDir() string {
	return paths.GlobalStorage() + "/linux-builder-container"
}

// BuilderKey is the private-key path; its .pub half is authorized in the
// container.
func BuilderKey() string {
	return BuilderKeyDir() + "/id_ed25519"
}

// PullArgv returns the argv to pull the builder image on the given runtime.
func PullArgv(runtime, image string) []string {
	if image == "" {
		image = BuilderImage
	}
	if runtime == "container" {
		return []string{"container", "image", "pull", image}
	}
	return []string{runtime, "pull", image}
}

// RunArgv returns the argv to start the builder container detached. podman
// publishes sshd to 127.0.0.1:<hostPort>; Apple Container has no -p (each
// container gets its own VM IP), so the publish is omitted there. Mirrors
// run_argv. Empty image/name/0 hostPort fall back to the frozen defaults.
func RunArgv(runtime, pubkey, image, name string, hostPort int) []string {
	if image == "" {
		image = BuilderImage
	}
	if name == "" {
		name = BuilderContainer
	}
	if hostPort == 0 {
		hostPort = BuilderHostPort
	}
	common := []string{
		"run", "-d", "--rm", "--name", name,
		"-e", "YOLO_BUILDER_PUBKEY=" + pubkey,
	}
	if runtime == "container" {
		argv := []string{"container"}
		argv = append(argv, common...)
		return append(argv, image)
	}
	argv := []string{runtime}
	argv = append(argv, common...)
	argv = append(argv, "-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, BuilderGuestPort), image)
	return argv
}

// BuilderURI is the ssh-ng store/builder URI nix uses to reach the container.
func BuilderURI(host string, port int, keyPath string) string {
	if port == 0 {
		port = BuilderHostPort
	}
	if keyPath == "" {
		keyPath = BuilderKey()
	}
	return fmt.Sprintf("ssh-ng://%s@%s:%d?ssh-key=%s", BuilderSSHUser, host, port, keyPath)
}

// BuildersLine is a nix --builders spec pointing at the container. Format:
// "ssh-ng://user@host:port aarch64-linux key maxjobs". System is fixed to
// aarch64-linux (the arch a Mac needs).
// to BuilderKey() when empty; port 0 / maxJobs 0 fall back to defaults.
func BuildersLine(host string, port, maxJobs int, keyPath string) string {
	if port == 0 {
		port = BuilderHostPort
	}
	if maxJobs == 0 {
		maxJobs = 4
	}
	if keyPath == "" {
		keyPath = BuilderKey()
	}
	return fmt.Sprintf("ssh-ng://%s@%s:%d aarch64-linux %s %d", BuilderSSHUser, host, port, keyPath, maxJobs)
}

// NixSSHOpts is the NIX_SSHOPTS for talking to an ephemeral container (no
// host-key pinning — the container regenerates its key each boot). Mirrors
// nix_ssh_opts byte-for-byte.
func NixSSHOpts() string {
	return "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
}

// ReachableAddressFromContainerLs parses `container ls` stdout for the running
// builder's VM IP:22 (Apple Container has no host port-publish).
// container branch of reachable_address: skip header, find the row whose first
// field == name, then the first token containing exactly 3 dots is the ADDR
// (e.g. "192.168.64.2/24"); strip the "/mask". Returns (host, port, true) or
// (,,false). The podman branch (always 127.0.0.1:BUILDER_HOST_PORT) is a
// constant the caller applies directly.
func ReachableAddressFromContainerLs(stdout, name string) (string, int, bool) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return "", 0, false
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= 1 {
		return "", 0, false
	}
	for _, line := range lines[1:] { // skip header
		parts := strings.Fields(line)
		if len(parts) == 0 || parts[0] != name {
			continue
		}
		for _, tok := range parts {
			if strings.Count(tok, ".") == 3 {
				ip := strings.SplitN(tok, "/", 2)[0]
				return ip, BuilderGuestPort, true
			}
		}
	}
	return "", 0, false
}
