package entrypoint

import (
	"os"
	"path/filepath"
)

// GenerateCglimitScript writes ~/.local/bin/yolo-cglimit (a stdlib-only Python
// client to the host cgroup daemon).
func GenerateCglimitScript(e *Env) error {
	return writeExecutable(filepath.Join(e.LocalBin(), "yolo-cglimit"), cglimitScript)
}

// GenerateJournalctlScript writes ~/.local/bin/yolo-journalctl.
func GenerateJournalctlScript(e *Env) error {
	return writeExecutable(filepath.Join(e.LocalBin(), "yolo-journalctl"), journalctlScript)
}

// GenerateYoloWrapper cleans up stale shim files from older
// entrypoints. The yolo and yolo-ps Go binaries are now baked into the nix
// image directly — no runtime wrapper generation needed.
func GenerateYoloWrapper(e *Env) error {
	// Remove stale ~/.local/bin/yolo (older entrypoints put a script here).
	stale := filepath.Join(e.LocalBin(), "yolo")
	if fi, err := os.Lstat(stale); err == nil && fi.Mode().IsRegular() {
		_ = os.Remove(stale)
	}
	// Remove stale ~/.local/bin/yolo-ps (was a Python wrapper).
	stalePsPath := filepath.Join(e.LocalBin(), "yolo-ps")
	if fi, err := os.Lstat(stalePsPath); err == nil && fi.Mode().IsRegular() {
		_ = os.Remove(stalePsPath)
	}
	// Remove stale shim files from the shim dir.
	for _, name := range []string{"_yolo_bootstrap.py", "_yolo_python", "yolo", "yolo-go"} {
		_ = os.Remove(filepath.Join(e.ShimDir(), name))
	}
	return nil
}

// cglimitScript is the embedded Python body for yolo-cglimit.
const cglimitScript = `#!/usr/bin/env python3
"""yolo-cglimit — Run a command under cgroup v2 resource limits.

Usage: yolo-cglimit [OPTIONS] -- COMMAND [ARGS...]

Options:
  --cpu PCT       CPU limit as percentage of ALL CPUs (e.g. 75 = 75% of total)
  --memory LIMIT  Memory limit (e.g. 512m, 2g, 1073741824)
  --pids LIMIT    Max number of processes
  --name NAME     Cgroup name (default: auto-generated from PID)

Examples:
  yolo-cglimit --cpu 75 -- python train.py           # 75% of all CPUs
  yolo-cglimit --cpu 50 --memory 2g -- make -j8      # 50% CPU + 2GB RAM
  yolo-cglimit --pids 100 -- ./fork-heavy-script.sh  # Max 100 processes

Resource limits are enforced by the kernel via cgroup v2 and cannot be exceeded.
The host-side daemon handles all privileged cgroup operations securely.
"""
import json
import os
import socket
import sys

CGD_SOCKET = "/run/yolo-services/cgroup-delegate.sock"


def send_request(request: dict) -> dict:
    """Send a JSON request to the host-side cgroup delegate daemon."""
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        sock.connect(CGD_SOCKET)
        sock.sendall((json.dumps(request) + "\n").encode())
        data = b""
        while b"\n" not in data and len(data) < 8192:
            chunk = sock.recv(4096)
            if not chunk:
                break
            data += chunk
        return json.loads(data.decode())
    finally:
        sock.close()


def main():
    cpu_pct = None
    memory = None
    pids = None
    name = None
    command = []

    args = sys.argv[1:]
    i = 0
    while i < len(args):
        if args[i] == "--cpu" and i + 1 < len(args):
            cpu_pct = int(args[i + 1])
            i += 2
        elif args[i] == "--memory" and i + 1 < len(args):
            memory = args[i + 1]
            i += 2
        elif args[i] == "--pids" and i + 1 < len(args):
            pids = int(args[i + 1])
            i += 2
        elif args[i] == "--name" and i + 1 < len(args):
            name = args[i + 1]
            i += 2
        elif args[i] == "--":
            command = args[i + 1:]
            break
        elif args[i] in ("-h", "--help"):
            print(__doc__)
            sys.exit(0)
        else:
            print(f"Unknown option: {args[i]}", file=sys.stderr)
            sys.exit(1)

    if not command:
        print("Error: no command specified. Usage: yolo-cglimit [OPTIONS] -- COMMAND",
              file=sys.stderr)
        sys.exit(1)

    if not os.path.exists(CGD_SOCKET):
        print("Error: cgroup delegation not available — host daemon socket not found.",
              file=sys.stderr)
        print("This requires the jail to be started with the yolo CLI (which runs the",
              file=sys.stderr)
        print("host-side cgroup delegate daemon automatically).", file=sys.stderr)
        sys.exit(1)

    # Build the request
    request = {"op": "create_and_join", "name": name or f"job-{os.getpid()}"}
    if cpu_pct is not None:
        request["cpu_pct"] = cpu_pct
    if memory is not None:
        request["memory"] = memory
    if pids is not None:
        request["pids"] = pids

    try:
        resp = send_request(request)
    except Exception as e:
        print(f"Error: failed to contact cgroup daemon: {e}", file=sys.stderr)
        sys.exit(1)

    if not resp.get("ok"):
        print(f"Error: {resp.get('error', 'unknown error')}", file=sys.stderr)
        sys.exit(1)

    if resp.get("warnings"):
        for w in resp["warnings"]:
            print(f"Warning: {w}", file=sys.stderr)

    # exec the command — we're already in the cgroup (daemon moved us via SO_PEERCRED)
    os.execvp(command[0], command)


if __name__ == "__main__":
    main()
`

// journalctlScript is the embedded Python body for yolo-journalctl.
const journalctlScript = `#!/usr/bin/env python3
"""yolo-journalctl — Run journalctl on the host via the yolo-jail journal bridge.

Usage: yolo-journalctl [journalctl args...]

Forwards all arguments to ` + "`journalctl`" + ` running on the host, streams stdout
and stderr back to the local terminal, and exits with the host process's
exit code.  Enabled only when the jail's config sets ` + "`journal: \"user\"`" + `
(forces --user) or ` + "`journal: \"full\"`" + ` (unrestricted).

Examples:
  yolo-journalctl -u nginx -n 50
  yolo-journalctl --user -f
  yolo-journalctl -p err --since "1 hour ago"
"""
import json
import os
import socket
import struct
import sys

DEFAULT_SOCKET = "/run/yolo-services/journal.sock"
SOCKET_PATH = os.environ.get("YOLO_SERVICE_JOURNAL_SOCKET", DEFAULT_SOCKET)

FRAME_STDOUT = 1
FRAME_STDERR = 2
FRAME_EXIT = 3


def _read_exact(sock, n):
    buf = bytearray()
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            return bytes(buf)
        buf.extend(chunk)
    return bytes(buf)


def main():
    args = sys.argv[1:]
    if args and args[0] in ("-h", "--help") and not os.environ.get("YOLO_JOURNALCTL_PASSTHROUGH_HELP"):
        # -h/--help without env override prints our own doc, not journalctl's.
        # Set YOLO_JOURNALCTL_PASSTHROUGH_HELP=1 to forward it through.
        print(__doc__)
        print(f"Socket: {SOCKET_PATH}")
        return 0

    if not os.path.exists(SOCKET_PATH):
        sys.stderr.write(
            "yolo-journalctl: host journal bridge is not available.\n"
        )
        sys.stderr.write(
            f"  expected socket: {SOCKET_PATH}\n"
        )
        sys.stderr.write(
            "  enable it by setting ` + "`journal: \\\"user\\\"`" + ` (or \"full\") in yolo-jail.jsonc\n"
        )
        sys.stderr.write(
            "  or in ~/.config/yolo-jail/config.jsonc, then restart the jail.\n"
        )
        return 1

    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        sock.connect(SOCKET_PATH)
    except OSError as e:
        sys.stderr.write(f"yolo-journalctl: connect failed: {e}\n")
        return 1

    try:
        sock.sendall((json.dumps({"args": args}) + "\n").encode())
        exit_code = 1
        while True:
            header = _read_exact(sock, 5)
            if len(header) < 5:
                break
            stream, length = struct.unpack(">BI", header)
            payload = _read_exact(sock, length)
            if len(payload) < length:
                break
            if stream == FRAME_STDOUT:
                sys.stdout.buffer.write(payload)
                sys.stdout.flush()
            elif stream == FRAME_STDERR:
                sys.stderr.buffer.write(payload)
                sys.stderr.flush()
            elif stream == FRAME_EXIT:
                if len(payload) == 4:
                    (exit_code,) = struct.unpack(">i", payload)
                break
            else:
                # Unknown frame type — ignore, forward-compat.
                continue
        return exit_code
    except KeyboardInterrupt:
        return 130
    finally:
        try:
            sock.close()
        except Exception:
            pass


if __name__ == "__main__":
    sys.exit(main())
`
