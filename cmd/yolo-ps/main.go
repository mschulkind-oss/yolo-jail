// Command yolo-ps is the Go port of src/yolo_ps.py — the in-jail client for the
// host-processes loophole. It's a pure frameproto client (no config, no json5),
// baked into the jail image with the jail-side wave (Stage 11); ported now
// because it exercises internal/frameproto end-to-end and pairs with the
// host-processes daemon.
//
// CLI contract (byte-frozen against the Python argparse): -t/--tree, --pid,
// --socket. Socket resolves from $YOLO_SERVICE_HOST_PROCESSES_SOCKET.
// jail_id from $YOLO_JAIL_ID or $HOSTNAME (default "unknown").
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
)

func main() {
	os.Exit(run())
}

func run() int {
	tree := flag.Bool("tree", false, "pstree-style output, filtered to allowlisted comms + their children")
	flag.BoolVar(tree, "t", false, "pstree-style output (short)")
	pid := flag.Int("pid", 0, "Details for a single PID (rejected if its comm isn't allowlisted)")
	socket := flag.String("socket", "", "Override socket path (default: $YOLO_SERVICE_HOST_PROCESSES_SOCKET)")
	flag.Parse()

	sock := *socket
	if sock == "" {
		sock = os.Getenv("YOLO_SERVICE_HOST_PROCESSES_SOCKET")
	}
	if sock == "" {
		fmt.Fprintln(os.Stderr,
			"yolo-ps: no socket.  The host-processes loophole isn't wired "+
				"up in this jail.  Add `host_processes.visible: [...]` to your "+
				"yolo-jail.jsonc and restart the jail.")
		return 2
	}

	// Build the request. --pid takes priority over --tree over the list default.
	req := map[string]any{}
	pidSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "pid" {
			pidSet = true
		}
	})
	switch {
	case pidSet:
		req["mode"] = "pid"
		req["pid"] = *pid
	case *tree:
		req["mode"] = "tree"
	default:
		req["mode"] = "list"
	}

	return call(sock, req)
}

// call performs one request/response round trip, returning the daemon exit code.
// Mirrors yolo_ps._call: connect (ENOENT/refused -> exit 2), stamp jail_id,
// stream stdout/stderr, return the exit-frame code.
func call(socketPath string, request map[string]any) int {
	conn, err := net.DialTimeout("unix", socketPath, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo-ps: cannot reach loophole socket %s: %v\n", socketPath, err)
		return 2
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	jailID := os.Getenv("YOLO_JAIL_ID")
	if jailID == "" {
		jailID = os.Getenv("HOSTNAME")
	}
	if jailID == "" {
		jailID = "unknown"
	}
	// {"jail_id": ..., **request} — jail_id first, then the request fields.
	full := map[string]any{"jail_id": jailID}
	for k, v := range request {
		full[k] = v
	}
	body, _ := json.Marshal(full)
	if err := frameproto.WriteRequest(conn, body); err != nil {
		return 1
	}

	// Stream framed response: stdout/stderr to our fds, exit frame -> rc.
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			return 1 // EOF before an exit frame
		}
		switch f.StreamID {
		case frameproto.StreamStdout:
			os.Stdout.Write(f.Payload)
		case frameproto.StreamStderr:
			os.Stderr.Write(f.Payload)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(f.Payload)
			if err != nil {
				return 1
			}
			return rc
		default:
			// Unknown stream — ignore, keep reading (matches yolo_ps).
		}
	}
}
