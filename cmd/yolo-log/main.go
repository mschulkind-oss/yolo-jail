// Command yolo-log is the in-jail client for the macos-log loophole (packs/macos-log): it
// forwards a `log` invocation to the host, where Apple's /usr/bin/log runs as the host user,
// streams the output back, and exits with the host process's code.
//
// It replaces the `yolo-log` shell wrapper a macos-user bootstrap used to write into the
// sandbox's ~/.local/bin, which ran /usr/bin/log INSIDE the sandbox. That could never read
// anything: the sandbox account cannot read the unified log even with no Seatbelt profile
// (packs/macos-log/README.md). The bootstrap now removes that wrapper, which would otherwise
// shadow this binary on PATH.
//
// THE FRAMING IS THE JOURNAL BRIDGE'S, not frameproto: ">BI" frames whose stream IDs are
// 1=stdout, 2=stderr, 3=exit (internal/journald). The request is one JSON line,
// {"args": [...]}. main_test.go pins the IDs to the daemon's.
//
// TRANSPORT: loopback-TLS (internal/svcendpoint), through the endpoint FILE the variable
// names. That file carries this jail's bearer token, so nothing here prints its contents.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// dialTimeout bounds the dial and the accept ack only, never the session: `yolo-log stream`
// runs for as long as the user leaves it.
const dialTimeout = 30 * time.Second

const (
	frameStdout = 1
	frameStderr = 2
	frameExit   = 3
)

const usage = "yolo-log — read the Mac's unified log through the yolo-jail macos-log bridge.\n" +
	"\n" +
	"Usage: yolo-log [show|stream] [log args...]\n" +
	"\n" +
	"Runs Apple's `log` on the host and streams its output back. With no arguments it\n" +
	"shows the last five minutes; a first argument that is a flag is a `show` flag.\n" +
	"\n" +
	"By default the bridge reads only `show` and `stream`, prints ndjson, and sends only\n" +
	"entries logged by processes running as the macos-user sandbox account — this\n" +
	"sandbox's own processes, and any other sandbox's on this Mac. The loophole's `full`\n" +
	"setting (user config only) passes every argument to `log` unchanged.\n" +
	"\n" +
	"Examples:\n" +
	"  yolo-log show --last 10m --predicate 'process == \"myapp\"'\n" +
	"  yolo-log stream --level debug\n"

// endpointEnv is the variable the run pipeline emits for the macos-log loophole: a PATH to
// the endpoint file. Its absence means the bridge is off in this jail.
const endpointEnv = paths.MacosLogEndpointEnv

const sigintExit = 130

func main() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	go func() { <-sigCh; os.Exit(sigintExit) }()
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main's testable body.
func run(args []string, stdout, stderr io.Writer) int {
	endpoint := os.Getenv(paths.MacosLogEndpointEnv)
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, usage+"\n")
		if endpoint == "" {
			fmt.Fprint(stdout, "Endpoint: (not set — the macos-log bridge is off in this jail)\n")
		} else {
			fmt.Fprintf(stdout, "Endpoint: %s\n", endpoint)
		}
		return 0
	}
	if endpoint == "" {
		fmt.Fprint(stderr, "yolo-log: the macOS log bridge is not available in this jail.\n")
		fmt.Fprintf(stderr, "  %s is not set. Ask the human to:\n", endpointEnv)
		fmt.Fprint(stderr, "  1. select the pack in ~/.config/yolo-jail/config.jsonc (user scope only):\n")
		fmt.Fprint(stderr, "       \"packs\": [\"macos-log\"]\n")
		fmt.Fprint(stderr, "  2. enable the loophole, in either scope:\n")
		fmt.Fprint(stderr, "       \"loopholes\": {\"macos-log\": {\"enabled\": true}}\n")
		fmt.Fprint(stderr, "  For every entry on the Mac rather than this sandbox account's, add\n")
		fmt.Fprint(stderr, "  \"settings\": {\"full\": true} to that entry — user config only.\n")
		fmt.Fprint(stderr, "  Then relaunch the jail.\n")
		return 1
	}
	conn, err := svcendpoint.Dial(endpoint, dialTimeout)
	if err != nil {
		switch {
		case errors.Is(err, svcendpoint.ErrEndpointMissing):
			fmt.Fprintf(stderr, "yolo-log: no endpoint published at %s. The host-side bridge "+
				"never started or its dir was removed; relaunch the jail.\n", endpoint)
		case errors.Is(err, svcendpoint.ErrEndpointMalformed):
			fmt.Fprintf(stderr, "yolo-log: endpoint file %s is incomplete. It was truncated or "+
				"written by an older yolo; relaunch the jail to republish it.\n", endpoint)
		case errors.Is(err, svcendpoint.ErrAuthRejected):
			fmt.Fprintf(stderr, "yolo-log: the macos-log bridge rejected this jail's token. The "+
				"endpoint file %s is stale relative to the running bridge; relaunch the jail.\n", endpoint)
		default:
			fmt.Fprintf(stderr, "yolo-log: cannot reach the macos-log bridge named by %s: %v. "+
				"Relaunch the jail; if it persists, `yolo loopholes list` on the host shows "+
				"whether the bridge is running.\n", endpoint, err)
		}
		return 1
	}
	defer conn.Close()
	return converse(conn, args, stdout, stderr)
}

// converse sends the request and streams the framed reply until the exit frame. A stream that
// ends without one is a failure (1), never an empty success.
func converse(conn io.ReadWriter, args []string, stdout, stderr io.Writer) int {
	if args == nil {
		args = []string{}
	}
	body, err := json.Marshal(map[string]any{"args": args})
	if err != nil {
		return 1
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return 1
	}
	for {
		var header [5]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return 1
		}
		length := binary.BigEndian.Uint32(header[1:])
		payload := make([]byte, length)
		if length > 0 {
			if _, err := io.ReadFull(conn, payload); err != nil {
				return 1
			}
		}
		switch header[0] {
		case frameStdout:
			_, _ = stdout.Write(payload)
		case frameStderr:
			_, _ = stderr.Write(payload)
		case frameExit:
			if len(payload) != 4 {
				return 1
			}
			return int(int32(binary.BigEndian.Uint32(payload)))
		}
	}
}
