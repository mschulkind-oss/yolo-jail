// Command yolo-cglimit is the in-jail client for the cgroup-delegate loophole:
// it asks the host-side delegate to create a child cgroup with the requested
// limits, move THIS process into it, and then execs the user's command.
//
// It replaces the stdlib-only Python script internal/entrypoint/scripts.go used
// to generate into ~/.local/bin. Two implementations of one client is the drift
// the transport unification exists to end (docs/design/loophole-transport.md
// §7.4 / §8.4), and a Go client is the only kind that can dial the framework's
// transport at all — a second TLS+token implementation in generated Python is
// explicitly not an option.
//
// EXEC, NOT SPAWN. The daemon moves the CALLER into the job cgroup by the PID
// it read off the connection, so this process must hand its PID to the user's
// command rather than fork one. syscall.Exec keeps the PID; exec.Command would
// place the limits on a process that immediately exits.
//
// PARITY IS THE CONTRACT for this step: same socket path, same newline-JSON
// request, same messages, same exit codes as the script it replaces.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// usage is the help text, byte-for-byte the retired script's module docstring.
// `--help` printed it verbatim and the integration suite greps it for "--cpu",
// so it is part of the parity surface, not decoration.
const usage = `yolo-cglimit — Run a command under cgroup v2 resource limits.

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
`

// cgdSocket is the delegate's in-jail socket path. The retired script hardcoded
// the literal; this composes it from internal/paths, which is where the
// producer/consumer contract for the name is documented (a refactor once left
// the two spellings apart and silently disabled the delegate in every jail).
var cgdSocket = paths.JailHostServicesDir + "/" + paths.CgdSocketName

// options is the parsed command line.
type options struct {
	cpuPct  *int
	memory  *string
	pids    *int
	name    string
	command []string
}

// request is the wire request. The field set and their names are frozen: the
// daemon (internal/cgd) reads exactly these, and `omitempty` on the pointers
// reproduces Python's "only set the key when the flag was given" — a present
// key with a zero value is NOT the same request (cgd range-checks 0 and errors).
type request struct {
	Op     string  `json:"op"`
	Name   string  `json:"name"`
	CPUPct *int    `json:"cpu_pct,omitempty"`
	Memory *string `json:"memory,omitempty"`
	Pids   *int    `json:"pids,omitempty"`
}

func main() {
	code, command := run(os.Args[1:], os.Stdout, os.Stderr)
	if command != nil {
		code = execCommand(command, os.Stderr)
	}
	os.Exit(code)
}

// run is main's testable body: argv in, streams out, and either an exit code or
// the command to exec.
//
// The exec is main's job, not run's, and the split is deliberate rather than a
// testing convenience: everything run does is reversible, and the exec is the
// one step that ends this process. Returning the argv makes "the delegate said
// yes" an assertable outcome instead of something only observable by watching a
// process disappear.
func run(args []string, stdout, stderr io.Writer) (int, []string) {
	opts, code, done := parseArgs(args, stdout, stderr)
	if done {
		return code, nil
	}

	// ORDER IS PARITY: the retired script checked for a command BEFORE it
	// checked for the socket, so `yolo-cglimit --cpu 50` in a jail with no
	// delegate reports the usage error, not the unavailability one.
	if len(opts.command) == 0 {
		fmt.Fprintln(stderr, "Error: no command specified. Usage: yolo-cglimit [OPTIONS] -- COMMAND")
		return 1, nil
	}
	if _, err := os.Stat(cgdSocket); err != nil {
		fmt.Fprintln(stderr, "Error: cgroup delegation not available — host daemon socket not found.")
		fmt.Fprintln(stderr, "This requires the jail to be started with the yolo CLI (which runs the")
		fmt.Fprintln(stderr, "host-side cgroup delegate daemon automatically).")
		return 1, nil
	}

	name := opts.name
	if name == "" {
		name = "job-" + strconv.Itoa(os.Getpid())
	}
	req := request{Op: "create_and_join", Name: name, CPUPct: opts.cpuPct, Memory: opts.memory, Pids: opts.pids}

	resp, err := sendRequest(cgdSocket, req)
	if err != nil {
		fmt.Fprintf(stderr, "Error: failed to contact cgroup daemon: %v\n", err)
		return 1, nil
	}
	if !truthy(resp["ok"]) {
		msg := "unknown error"
		if s, ok := resp["error"].(string); ok {
			msg = s
		}
		fmt.Fprintf(stderr, "Error: %s\n", msg)
		return 1, nil
	}
	for _, w := range warnings(resp) {
		fmt.Fprintf(stderr, "Warning: %s\n", w)
	}

	// We are already in the cgroup — the daemon moved us there by the PID it
	// read off the connection. The exec is the caller's, so the limits land on
	// the command with this PID intact.
	return 0, opts.command
}

// parseArgs mirrors the retired script's hand-rolled loop exactly, including
// its quirks: a flag whose value is missing falls through to the unknown-option
// branch, and `--` ends parsing with everything after it as the command.
//
// done reports that run should return code immediately (help or a parse error).
func parseArgs(args []string, stdout, stderr io.Writer) (options, int, bool) {
	var o options
	for i := 0; i < len(args); {
		switch {
		case args[i] == "--cpu" && i+1 < len(args):
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				fmt.Fprintf(stderr, "Error: --cpu expects an integer, got %q\n", args[i+1])
				return o, 1, true
			}
			o.cpuPct = &n
			i += 2
		case args[i] == "--memory" && i+1 < len(args):
			v := args[i+1]
			o.memory = &v
			i += 2
		case args[i] == "--pids" && i+1 < len(args):
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				fmt.Fprintf(stderr, "Error: --pids expects an integer, got %q\n", args[i+1])
				return o, 1, true
			}
			o.pids = &n
			i += 2
		case args[i] == "--name" && i+1 < len(args):
			o.name = args[i+1]
			i += 2
		case args[i] == "--":
			o.command = args[i+1:]
			return o, 0, false
		case args[i] == "-h", args[i] == "--help":
			fmt.Fprint(stdout, usage+"\n")
			return o, 0, true
		default:
			fmt.Fprintf(stderr, "Unknown option: %s\n", args[i])
			return o, 1, true
		}
	}
	return o, 0, false
}

// sendRequest performs the newline-JSON round trip: one request line, one
// response line. The 8192-byte read cap is the retired script's and is kept —
// it bounds what a compromised or wedged daemon can make this client allocate.
func sendRequest(socket string, req request) (map[string]any, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return nil, err
	}
	line, err := readLineCapped(conn, 8192)
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// readLineCapped reads until '\n' or cap bytes, whichever comes first, and
// returns what it got. A clean EOF before any newline is not an error here —
// the JSON decode is what judges the payload, exactly as the script's was.
func readLineCapped(conn net.Conn, cap int) ([]byte, error) {
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 4096)
	for len(buf) < cap {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for _, b := range buf {
				if b == '\n' {
					return buf, nil
				}
			}
		}
		if err != nil {
			// EOF with bytes in hand is the normal end of a short response.
			if len(buf) > 0 {
				return buf, nil
			}
			return nil, err
		}
	}
	return buf, nil
}

// truthy applies Python's `if not resp.get("ok")` to a decoded JSON value:
// absent, null, false, 0 and "" are all falsy.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	default:
		return true
	}
}

// warnings extracts resp["warnings"] as strings, ignoring anything else in the
// list — a diagnostic channel must never be the reason a limited command
// refuses to run.
func warnings(resp map[string]any) []string {
	list, ok := resp["warnings"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, w := range list {
		if s, ok := w.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// execCommand replaces this process with command, resolving argv[0] through
// PATH the way execvp does.
//
// It returns only on failure. The retired script let a missing command surface
// as an uncaught Python traceback (exit 1); this reports it on one line and
// keeps the code.
func execCommand(command []string, stderr io.Writer) int {
	bin, err := exec.LookPath(command[0])
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			fmt.Fprintf(stderr, "Error: command not found: %s\n", command[0])
		} else {
			fmt.Fprintf(stderr, "Error: cannot execute %s: %v\n", command[0], err)
		}
		return 1
	}
	if err := syscall.Exec(bin, command, os.Environ()); err != nil {
		fmt.Fprintf(stderr, "Error: cannot execute %s: %v\n", command[0], err)
		return 1
	}
	return 0 // unreachable: a successful Exec never returns
}
