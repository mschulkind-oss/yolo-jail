// Command yolo-ps is the in-jail client for the host-processes loophole. It's a
// pure frameproto client over the framework's transport (no config, no json5),
// baked into the jail image.
//
// CLI contract: -t/--tree, --pid, --endpoint. The endpoint resolves from
// $YOLO_SERVICE_HOST_PROCESSES_ENDPOINT and names a FILE, not an address: the
// address lives inside it so a restarted daemon is picked up without relaunching
// the jail, whose environment is frozen at container start.
//
// # This client no longer names its own jail
//
// It used to: every request carried a jail_id read from $YOLO_JAIL_ID, else
// $HOSTNAME, else the literal "unknown", and the daemon's access log printed
// whatever it was told. That was always the wrong end of the connection to ask.
// The HOST knows which jail it handed an endpoint to, and it now says so itself
// — yolo prepends a connection preamble carrying the jail's identity, derived
// host-side from the path it published at (internal/svcendpoint/preamble.go),
// and hostservice prefers it over anything in the request. So the field is not
// removed to save bytes: it is removed because a value a client asserts about
// itself cannot be audit evidence, and leaving it on the wire invites someone to
// start trusting it again.
//
// It was also WRONG in a real configuration, not merely redundant: nothing in
// this repo sets $YOLO_JAIL_ID, so the value was always $HOSTNAME — and a nested
// jail is forced onto --net=host, where $HOSTNAME is the HOST's hostname.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// dialTimeout bounds the TCP+TLS dial and the accept ack, and NOTHING ELSE — see
// call below for why a session deadline would be a bug here.
const dialTimeout = 30 * time.Second

// inertEnv carries the inert note's reason when the launch found host-processes inert on
// this setup (plan PD-8). paths.LoopholeInertEnv is the one spelling the launch writes and
// recognizes, so the client never spells the name itself.
var inertEnv = paths.LoopholeInertEnv("host-processes")

// noEndpointMsg is the client's fallback when no endpoint names the daemon (design §5's
// shape, filled for a loophole whose pack is not selected).
const noEndpointMsg = "yolo-ps: no host-processes endpoint in this jail, so nothing ran.\n" +
	"  The host-processes loophole is not running here: its pack is not selected.\n" +
	"  Ask for this project:   yolo request host-processes view <process names>\n" +
	"  then the human selects the pack, on the host:   yolo pack add host-processes\n" +
	"  and restarts the jail, answering y at its config-change prompt.\n"

func main() {
	os.Exit(run())
}

func run() int {
	return runArgs(os.Args[1:])
}

// runArgs is the client on its arguments, parsed by its own flag set so a test can run it.
func runArgs(args []string) int {
	fs := flag.NewFlagSet("yolo-ps", flag.ExitOnError)
	tree := fs.Bool("tree", false, "pstree-style output, filtered to allowlisted comms + their children")
	fs.BoolVar(tree, "t", false, "pstree-style output (short)")
	pid := fs.Int("pid", 0, "Details for a single PID (rejected if its comm isn't allowlisted)")
	endpoint := fs.String("endpoint", "", "Override endpoint file (default: $YOLO_SERVICE_HOST_PROCESSES_ENDPOINT)")
	socket := fs.String("socket", "", "Alias for --endpoint (accepted for compatibility)")
	_ = fs.Parse(args)

	// INERT FIRST, before any endpoint is looked for: on a setup where the loophole does
	// nothing, the launch says why in this variable (plan PD-8), and that is the whole answer.
	// A missing endpoint there is the setup's doing, and a dial error would send the reader to
	// relaunch a jail that will never have one.
	if reason := os.Getenv(inertEnv); reason != "" {
		fmt.Fprint(os.Stderr, loopholegrant.Refusal{Client: "yolo-ps", Pack: "host-processes", Inert: reason}.Text())
		return loopholegrant.RefusedExit
	}

	ep := *endpoint
	if ep == "" {
		ep = *socket
	}
	if ep == "" {
		ep = os.Getenv("YOLO_SERVICE_HOST_PROCESSES_ENDPOINT")
	}
	if ep == "" {
		// No endpoint is a loophole that is not running here, which means its pack is not
		// selected: a selected loophole runs, and the daemon itself refuses an ungranted
		// request with the step its grant file names. So this, the client's own fallback,
		// names the request and the selection, never a switch.
		fmt.Fprint(os.Stderr, noEndpointMsg)
		return 2
	}

	// --pid is detected by PRESENCE, not by value: `--pid 0` is a query about
	// pid 0, not an absent flag.
	pidSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "pid" {
			pidSet = true
		}
	})

	return call(ep, buildRequest(pidSet, *pid, *tree))
}

// buildRequest maps the selector flags to the daemon's request object: --pid
// takes priority over --tree, which takes priority over the list default. The
// daemon dispatches on "mode" and reads "pid" and nothing else
// (internal/hostprocesses.BuildHandler).
//
// Split out of run so a test can pin the request WITHOUT a daemon, because the
// property worth pinning is an absence — see requestBody.
func buildRequest(pidSet bool, pid int, tree bool) map[string]any {
	switch {
	case pidSet:
		return map[string]any{"mode": "pid", "pid": pid}
	case tree:
		return map[string]any{"mode": "tree"}
	default:
		return map[string]any{"mode": "list"}
	}
}

// requestBody renders the request exactly as it goes on the wire: the object
// buildRequest returned, and nothing else.
//
// It is a named function for one reason — "and nothing else" is testable here
// and nowhere else. This is the site where a jail_id used to be merged in ahead
// of the caller's fields, so this is where a regression would land.
func requestBody(request map[string]any) []byte {
	// A map of strings and ints; json.Marshal cannot fail on it.
	body, _ := json.Marshal(request)
	return body
}

// unansweredMsg attributes a connection that AUTHENTICATED and then produced no
// exit frame.
//
// IT EXISTS BECAUSE THE DAEMON MOVED BEHIND YOLO'S FRONT. While host-processes
// published its own endpoint file, a dead daemon was a DIAL failure — the file
// was unlinked with its listener, or named a port nobody was on — so the switch
// in call() attributed it and returned 2. Under `publishes: "socket"` yolo owns
// the listener and the daemon is upstream of it, so the front authenticates this
// jail from a perfectly valid file, fails to reach the daemon, and hangs up. The
// dial SUCCEEDS and the stream ends silently.
//
// Without this the whole failure was `yolo-ps` exiting 1 with no output at all,
// which from inside the jail is indistinguishable from a query that matched
// nothing. The exit code is left at 1 rather than moved to the 2 the dial
// failures use: 1 is what an interrupted stream has always returned, and the
// daemon's own exit codes ride this same channel.
//
// The endpoint's PATH is named and its CONTENTS never are — that file carries
// this jail's bearer token, and the rule holds for every diagnostic in this file.
func unansweredMsg(endpointPath string, partial bool) string {
	what := "never answered"
	if partial {
		what = "stopped answering mid-response"
	}
	return fmt.Sprintf("yolo-ps: the host-processes daemon %s.  The endpoint at %s "+
		"authenticated, so yolo's front is up and the daemon behind it is not — it "+
		"crashed or was killed on the host.  Relaunch the jail; the reason is in the "+
		"host's ~/.local/share/yolo-jail/logs/host-service-host-processes.log.\n",
		what, endpointPath)
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

	if err := frameproto.WriteRequest(conn, requestBody(request)); err != nil {
		fmt.Fprint(os.Stderr, unansweredMsg(endpointPath, false))
		return 1
	}

	// Stream framed response: stdout/stderr to our fds, exit frame -> rc.
	answered := false
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			fmt.Fprint(os.Stderr, unansweredMsg(endpointPath, answered))
			return 1 // EOF before an exit frame
		}
		answered = true
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
