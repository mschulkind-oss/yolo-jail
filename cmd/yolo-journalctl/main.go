// Command yolo-journalctl is the in-jail client for the journal-bridge
// loophole: it forwards a journalctl invocation to the host, streams the
// output back, and exits with the host process's code.
//
// It replaces the stdlib-only Python script internal/entrypoint/scripts.go used
// to generate into ~/.local/bin, for the reason recorded in
// docs/design/loophole-transport.md §8.4: the bridge is one of the last two
// consumers still on a plain AF_UNIX socket, and only a Go client can dial the
// framework's transport — a second TLS+token implementation in generated Python
// would be exactly the drift the unification exists to prevent.
//
// THE FRAMING IS NOT frameproto. The header shape is the same ">BI" (1-byte
// stream id, 4-byte BE length) but the journal bridge's stream IDs are
// deliberately 1=stdout, 2=stderr, 3=exit, where frameproto v1 uses 0/1/2. The
// daemon side says so too (internal/journald); do not conflate them.
//
// TRANSPORT: loopback-TLS (internal/svcendpoint). The bridge publishes an
// endpoint FILE, not an address — the address lives inside it, so a restarted
// daemon is picked up without relaunching the jail, whose environment is frozen
// at container start. That file also carries this jail's bearer token, which is
// why nothing here ever prints its contents.
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

	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// dialTimeout bounds the TCP+TLS dial and the accept ack, and NOTHING ELSE.
// svcendpoint.Dial sets no deadline on the returned conn, deliberately: a
// whole-session deadline would abort `yolo-journalctl -f`, whose entire job is
// to stream for as long as the user leaves it running.
const dialTimeout = 30 * time.Second

// The journal bridge's stream IDs. They are DELIBERATELY 1/2/3 where frameproto
// v1 uses 0/1/2 — see the package comment.
//
// Spelled here rather than imported from internal/journald so this baked client
// stays a pure protocol client (importing the daemon would drag jsonx and
// os/exec into a binary that needs neither). main_test.go asserts these equal
// the daemon's own constants, which is what keeps the duplication honest.
const (
	frameStdout = 1
	frameStderr = 2
	frameExit   = 3
)

// usage is the help text, byte-for-byte the retired script's module docstring.
const usage = "yolo-journalctl — Run journalctl on the host via the yolo-jail journal bridge.\n" +
	"\n" +
	"Usage: yolo-journalctl [journalctl args...]\n" +
	"\n" +
	"Forwards all arguments to `journalctl` running on the host, streams stdout\n" +
	"and stderr back to the local terminal, and exits with the host process's\n" +
	"exit code.  Enabled only when the jail's config sets `journal: \"user\"`\n" +
	"(forces --user) or `journal: \"full\"` (unrestricted).\n" +
	"\n" +
	"Examples:\n" +
	"  yolo-journalctl -u nginx -n 50\n" +
	"  yolo-journalctl --user -f\n" +
	"  yolo-journalctl -p err --since \"1 hour ago\"\n"

// endpointEnv names the variable the run pipeline emits for a loopback-TLS
// service. Its value is always a PATH to the endpoint file, never an address.
//
// THERE IS NO _SOCKET FALLBACK, and its absence is load-bearing. The retired
// spelling is deliberately not emitted alongside it (internal/paths): a jail
// where this variable is missing is a jail where the bridge is off, and hitting
// the clean "not wired up" message is exactly right. A fallback to the old
// socket path would instead make an off bridge look like a broken one.
const endpointEnv = "YOLO_SERVICE_JOURNAL_ENDPOINT"

// passthroughHelpEnv, when set, forwards -h/--help to the host journalctl
// instead of printing ours.
const passthroughHelpEnv = "YOLO_JOURNALCTL_PASSTHROUGH_HELP"

// sigintExit is what the retired script's `except KeyboardInterrupt` returned.
// Go's default SIGINT disposition would die by signal instead, which a shell
// reports as the same 130 but which is a different wait status to anything
// inspecting it — so the handler is explicit.
const sigintExit = 130

func main() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	go func() { <-sigCh; os.Exit(sigintExit) }()

	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main's testable body: argv in, streams out, exit code back.
func run(args []string, stdout, stderr io.Writer) int {
	endpoint := os.Getenv(endpointEnv)

	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") && os.Getenv(passthroughHelpEnv) == "" {
		// -h/--help without the env override prints our own doc, not
		// journalctl's. Only as the FIRST argument, matching the script: a
		// `-u foo --help` is a journalctl invocation and is forwarded.
		fmt.Fprint(stdout, usage+"\n")
		fmt.Fprintf(stdout, "Endpoint: %s\n", endpointOrNone(endpoint))
		return 0
	}

	if endpoint == "" {
		fmt.Fprint(stderr, "yolo-journalctl: host journal bridge is not available.\n")
		fmt.Fprintf(stderr, "  %s is not set in this jail\n", endpointEnv)
		fmt.Fprint(stderr, "  enable it by setting `journal: \"user\"` (or \"full\") in yolo-jail.jsonc\n")
		fmt.Fprint(stderr, "  or in ~/.config/yolo-jail/config.jsonc, then restart the jail.\n")
		return 1
	}

	conn, err := svcendpoint.Dial(endpoint, dialTimeout)
	if err != nil {
		// Attribution: four faults, four different fixes. Nothing printed here
		// carries the endpoint file's CONTENTS — that file holds this jail's
		// bearer token, and a diagnostic is not a place for it.
		switch {
		case errors.Is(err, svcendpoint.ErrEndpointMissing):
			fmt.Fprintf(stderr,
				"yolo-journalctl: no endpoint published at %s.  The host-side bridge "+
					"never started or its dir was removed; relaunch the jail.\n", endpoint)
		case errors.Is(err, svcendpoint.ErrEndpointMalformed):
			fmt.Fprintf(stderr,
				"yolo-journalctl: endpoint file %s is incomplete.  It was truncated or "+
					"written by an older yolo; relaunch the jail to republish it.\n", endpoint)
		case errors.Is(err, svcendpoint.ErrAuthRejected):
			fmt.Fprintf(stderr,
				"yolo-journalctl: the journal bridge rejected this jail's token.  The "+
					"endpoint file %s is stale relative to the running daemon; relaunch "+
					"the jail.\n", endpoint)
		default:
			fmt.Fprintf(stderr,
				"yolo-journalctl: cannot reach the journal bridge named by %s: %v\n", endpoint, err)
		}
		return 1
	}
	defer conn.Close()

	return converse(conn, args, stdout, stderr)
}

// endpointOrNone renders the endpoint for --help, saying so when it is unset
// rather than printing an empty value that reads like a path.
func endpointOrNone(endpoint string) string {
	if endpoint == "" {
		return "(not set — the journal bridge is off in this jail)"
	}
	return endpoint
}

// converse sends the request header and streams the framed reply until the exit
// frame. It is transport-agnostic on purpose (io.ReadWriter, not net.Conn), so
// the loopback-TLS flip is a change of dialer and nothing else.
//
// The default exit code is 1: a stream that ends WITHOUT an exit frame is a
// failure, never a success, and silently returning 0 there would make a killed
// bridge look like an empty journal.
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
		default:
			// Unknown frame type — ignored, forward-compat, keep reading.
		}
	}
}
