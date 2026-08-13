// Command yolo-ps is the in-jail client for the host-processes loophole. It's a
// pure frameproto client over the framework's transport (no config, no json5),
// baked into the jail image.
//
// CLI contract: -t/--tree, --pid, --endpoint. The endpoint resolves from
// $YOLO_SERVICE_HOST_PROCESSES_ENDPOINT and names a FILE, not an address: the
// address lives inside it so a restarted daemon is picked up without relaunching
// the jail, whose environment is frozen at container start. jail_id from
// $YOLO_JAIL_ID or $HOSTNAME (default "unknown").
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// dialTimeout bounds the TCP+TLS dial and the accept ack, and NOTHING ELSE — see
// call below for why a session deadline would be a bug here.
const dialTimeout = 30 * time.Second

func main() {
	os.Exit(run())
}

func run() int {
	tree := flag.Bool("tree", false, "pstree-style output, filtered to allowlisted comms + their children")
	flag.BoolVar(tree, "t", false, "pstree-style output (short)")
	pid := flag.Int("pid", 0, "Details for a single PID (rejected if its comm isn't allowlisted)")
	endpoint := flag.String("endpoint", "", "Override endpoint file (default: $YOLO_SERVICE_HOST_PROCESSES_ENDPOINT)")
	socket := flag.String("socket", "", "Alias for --endpoint (accepted for compatibility)")
	flag.Parse()

	ep := *endpoint
	if ep == "" {
		ep = *socket
	}
	if ep == "" {
		ep = os.Getenv("YOLO_SERVICE_HOST_PROCESSES_ENDPOINT")
	}
	if ep == "" {
		fmt.Fprintln(os.Stderr,
			"yolo-ps: no endpoint.  The host-processes loophole isn't wired "+
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

	return call(ep, req)
}

// call performs one request/response round trip, returning the daemon exit code.
// stream stdout/stderr, return the exit-frame code.
func call(endpointPath string, request map[string]any) int {
	// NO SESSION DEADLINE, deliberately. Python yolo-ps used a plain blocking
	// socket with no timeout after connect (yolo_ps._call): it streams until the
	// exit frame arrives. A whole-session deadline here would break the canonical
	// 124 path (the daemon's 30s ExecAllowlisted timeout sends exit-frame 124 just
	// after 30s; a client deadline started at connect would fire first) and abort
	// any legitimately long stream. svcendpoint.Dial honours that: dialTimeout
	// bounds the dial and the accept ack, and the returned conn carries no deadline.
	conn, err := svcendpoint.Dial(endpointPath, dialTimeout)
	if err != nil {
		// Attribution: three different faults, three different fixes. Nothing
		// printed here can carry the endpoint file's CONTENTS — that file holds
		// this jail's bearer token, and a diagnostic is not a place for it.
		switch {
		case errors.Is(err, svcendpoint.ErrEndpointMissing):
			fmt.Fprintf(os.Stderr,
				"yolo-ps: no endpoint published at %s.  The host-side daemon never "+
					"started or its dir was removed; relaunch the jail.\n", endpointPath)
		case errors.Is(err, svcendpoint.ErrEndpointMalformed):
			fmt.Fprintf(os.Stderr,
				"yolo-ps: endpoint file %s is incomplete.  It was truncated or "+
					"written by an older yolo; relaunch the jail to republish it.\n", endpointPath)
		case errors.Is(err, svcendpoint.ErrAuthRejected):
			fmt.Fprintf(os.Stderr,
				"yolo-ps: the host-processes daemon rejected this jail's token.  "+
					"The endpoint file %s is stale relative to the running daemon; "+
					"relaunch the jail.\n", endpointPath)
		default:
			fmt.Fprintf(os.Stderr,
				"yolo-ps: cannot reach the host-processes daemon named by %s: %v\n",
				endpointPath, err)
		}
		return 2
	}
	defer conn.Close()

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
