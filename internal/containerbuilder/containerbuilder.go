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
	"encoding/base64"
	"fmt"
	"runtime"
	"strconv"
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
//
// This is the NOT-nested argv, byte for byte what it has always been; Session.Start
// adds NestedNetwork when podman itself runs inside a container (see runArgv).
func RunArgv(runtime, pubkey, image, name string, hostPort int) []string {
	return runArgv(runtime, pubkey, image, name, hostPort, false)
}

// NestedNetwork is the network a podman builder takes when podman runs INSIDE a
// container — a yolo jail. podman's default there is a netavark bridge, which needs a
// veth pair, and a container's netns cannot always make one: the kernel's veth module
// must already be loaded on the host, because a non-initial user namespace may not
// autoload it, so after a host reboot that has not loaded it every nested `podman run -p`
// fails with `netavark: create veth pair: Netlink error: Operation not supported (os
// error 95)` (measured 2026-10-09, podman 5.8.7, kernel 7.2.7). slirp4netns needs only a
// tap device, and it keeps the `-p 127.0.0.1:<hostPort>:22` publish, so the address
// Session dials and the builders line are unchanged. The jail image ships slirp4netns
// (flake.nix: it is the nested podman's default rootless network command).
//
// Not `--net=host`, which is what the run assembler forces on a nested JAIL: the builder
// image's sshd listens on port 22 and nothing else, so sharing the launcher's namespace
// would serve the builder on that namespace's port 22 — the host's own sshd's port on a
// `network.mode: host` chain — and leave 127.0.0.1:<hostPort> empty (measured the same day:
// keyscan answers on :22 and nothing on :31022).
const NestedNetwork = "--network=slirp4netns"

// runArgv is RunArgv with the nested choice made explicit. Apple Container takes no
// network selector, so nested is ignored there.
func runArgv(runtime, pubkey, image, name string, hostPort int, nested bool) []string {
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
	if nested {
		argv = append(argv, NestedNetwork)
	}
	argv = append(argv, "-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, BuilderGuestPort), image)
	return argv
}

// BuilderURI is the ssh-ng store/builder URI nix uses to reach the container. The key path is
// percent-encoded (nixQueryEscape), which nix decodes, so a path holding a space or a '&' is
// still the one ssh is handed.
func BuilderURI(host string, port int, keyPath string) string {
	if port == 0 {
		port = BuilderHostPort
	}
	if keyPath == "" {
		keyPath = BuilderKey()
	}
	return fmt.Sprintf("ssh-ng://%s@%s:%d?ssh-key=%s", BuilderSSHUser, host, port, nixQueryEscape(keyPath))
}

// nixBuildersSeparators are the bytes nix's `builders` parser cuts a line on: '\n' and ';'
// separate machines, '#' starts a comment, and " \t\n\r" separate a machine's fields. A key path
// holding any of them cannot be field 3 (BuildersLine).
const nixBuildersSeparators = " \t\n\r;#"

// nixQueryEscape percent-encodes s for a nix store URI's query: every byte but the unreserved
// ones and '/', so no separator above, and no '&', '=', '%' or '+', survives unencoded. nix
// decodes %XX in a query value and nothing else.
func nixQueryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
			strings.IndexByte("-._~/", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// BuilderSystem is the nix system the container builder advertises: the LINUX system
// matching this host's architecture, because a Linux container on an arm64 Mac runs
// aarch64-linux and on an x86_64 Mac runs x86_64-linux.
//
// This used to be hardcoded `aarch64-linux`, with the comment "the arch a Mac needs" —
// true of an Apple Silicon Mac and wrong of every other host. The consequence was
// specific and silent: nix would connect to a working builder, be told it serves
// aarch64-linux, and then decline to offload an x86_64-linux build to it — so on an
// x86_64 host the builder appeared healthy and did nothing. That is the second half of
// BACKLOG E8; the first half (publishing the builder image for both arches) landed in
// 7cc54a0, and a multi-arch image is useless while the advertised system is fixed.
//
// Derived from GOARCH rather than probed: the builder runs a Linux container on THIS
// machine, so its system is a fact about the local architecture, known without asking
// anything. An unrecognized GOARCH passes through as `<goarch>-linux`, which is wrong in
// the same way a hardcoded constant is wrong but at least names what it saw — and nix
// rejects an unknown system loudly rather than silently not offloading.
func BuilderSystem() string { return nixLinuxSystem(runtime.GOARCH) }

// nixLinuxSystem maps Go's GOARCH to nix's `<arch>-linux` double. Split out so the
// mapping is testable without a cross-compile, mirroring machineForPlatform in
// internal/cli/check.
func nixLinuxSystem(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64-linux"
	case "arm64":
		return "aarch64-linux"
	default:
		return goarch + "-linux"
	}
}

// BuildersLine is a nix --builders spec pointing at the container. Format:
// "ssh-ng://user@host:port <system> key maxjobs", where <system> is BuilderSystem() —
// the Linux system for this host's arch, NOT a constant. keyPath falls back
// to BuilderKey() when empty; port 0 / maxJobs 0 fall back to defaults.
//
// publicHostKey is nix's EIGHTH field, and it is the whole reason this offload can
// work at all on a normal macOS install. `builders` is a RESTRICTED nix setting: a
// client that passes it does not act on it — the setting crosses the daemon socket
// and the NIX-DAEMON, running as root, forks `ssh` itself. So the NIX_SSHOPTS the
// caller exports never reaches that ssh (the launchd plist sets no environment, and
// a fork inherits the daemon's), and root meets an unknown host key — this builder
// regenerates one on every boot — with StrictHostKeyChecking at OpenSSH's default
// `ask`, no tty and no askpass. That is a deterministic
//
//	Host key verification failed.
//	cannot build on 'ssh-ng://root@127.0.0.1:31022': error: failed to start SSH connection
//
// and, because nix wires ssh's stderr to a log fd only for the legacy `ssh://` scheme,
// the first of those two lines lands in /var/log/nix-daemon.log and never in the
// caller's output. Both halves measured 2026-09-13 against nix 2.34.8 and reproduced
// byte-for-byte from the macOS nightly's logs.
//
// Filling field 8 replaces the option nix cannot deliver with one it can: nix writes
// "<host> <key>" to a temp file and passes `-oUserKnownHostsFile=<that>`, so the
// connection is VERIFIED rather than unchecked, in the daemon's process, with nothing
// to configure on the host. Fields 5-7 (speedFactor, supported, mandatory) have to be
// spelled to reach it, and "-" is nix's own "default" token. Empty publicHostKey keeps
// the historical 4-field line.
//
// A KEY PATH NIX WOULD SPLIT MOVES INTO THE STORE URI. The key sits under the state dir in the
// user's home, so a home holding a space put that space in field 3: every later field moved one
// place right and nix refused the whole line ("bad machine specification: failed to convert
// column #3 … to 'unsigned int'", measured against nix 2.34.8), while a ';' or '#' cut the line
// at that byte, so ssh got a truncated key path and no host-key pin. nix has no quoting for a
// field, but it percent-decodes the URI's query, and a key field of "-" leaves the URI's ssh-key
// in force. So such a path is spelled `?ssh-key=<encoded>` on field 1 with "-" in field 3, and
// every other path keeps the field-3 line, byte for byte. builderslineparse_test.go reads both
// forms as nix does and records the measurement.
func BuildersLine(host string, port, maxJobs int, keyPath, publicHostKey string) string {
	if port == 0 {
		port = BuilderHostPort
	}
	if maxJobs == 0 {
		maxJobs = 4
	}
	if keyPath == "" {
		keyPath = BuilderKey()
	}
	uri := fmt.Sprintf("ssh-ng://%s@%s:%d", BuilderSSHUser, host, port)
	keyField := keyPath
	if strings.ContainsAny(keyPath, nixBuildersSeparators) {
		uri, keyField = BuilderURI(host, port, keyPath), "-"
	}
	line := fmt.Sprintf("%s %s %s %d", uri, BuilderSystem(), keyField, maxJobs)
	if publicHostKey != "" {
		line += " 1 - - " + publicHostKey
	}
	return line
}

// HostKeyScanArgv reads the builder's SSH host key off the wire, from the same
// address nix is about to be pointed at.
//
// It is deliberately NOT `podman exec cat /etc/ssh/ssh_host_ed25519_key.pub`, which
// would be a stronger provenance claim and a WEAKER probe: this runs end to end over
// the address that has to work, so it fails when the path to the builder is broken
// even though the container is healthy — which is the failure this offload actually
// has on macOS. It also needs no per-runtime argv, so podman and Apple Container share
// one spelling.
//
// ed25519 only, because that is the one HostKey the builder image's sshd config
// declares. Asking for a type the server does not have returns nothing rather than a
// wrong key, and Start treats "nothing" as "not ready yet".
func HostKeyScanArgv(host string, port int) []string {
	if port == 0 {
		port = BuilderHostPort
	}
	return []string{"ssh-keyscan", "-T", "5", "-t", "ed25519", "-p", strconv.Itoa(port), host}
}

// EncodeHostKey turns ssh-keyscan stdout into the base64 blob BuildersLine's eighth
// field wants: base64 of "<type> <key>", which is what nix base64-DECODES and writes
// after the hostname into the known-hosts file it hands ssh. Returns "" when the
// output carries no key line — ssh-keyscan prints a "# host:port SSH-2.0-…" banner
// comment on stderr AND stdout, and prints nothing at all when the far end never
// answers.
//
// The comment the scan emits alongside the key ("root@<container id>") is dropped:
// nix writes the decoded bytes verbatim, and a known-hosts line is <host> <type>
// <key>, with anything after the key ignored — but keeping it would put a
// container-id into a launch's argv for no gain.
func EncodeHostKey(keyscanStdout string) string {
	for _, line := range strings.Split(keyscanStdout, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		// fields: <[host]:port> <keytype> <base64 key> [comment]
		if !strings.Contains(fields[1], "-") || fields[2] == "" {
			continue
		}
		return base64.StdEncoding.EncodeToString([]byte(fields[1] + " " + fields[2]))
	}
	return ""
}

// NIX_SSHOPTS IS GONE, AND ITS ABSENCE IS LOAD-BEARING. A helper here used to
// return `-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null`, exported by
// internal/image/autoload.go onto the `nix` child. It never worked for a
// `--builders` build — that setting is RESTRICTED, so the nix-daemon consumes it and
// forks ssh in the daemon's own environment — and wherever it DID apply it defeated
// the host-key pin, because ssh takes the first occurrence of an option and nix
// appends NIX_SSHOPTS ahead of its own `-oUserKnownHostsFile=`. MEASURED 2026-09-13:
// a deliberately WRONG eighth field still connected with those options set, and
// failed without them.
//
// Deleted 2026-09-13 rather than kept for the client-forks-ssh case, because nothing
// called it for that case: a helper with no production caller and a test asserting
// its string reads as covered while pinning nothing. BuildersLine's eighth field is
// the mechanism now.

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
