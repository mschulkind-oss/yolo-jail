// Command yolo-serial is the in-jail client for the serial loophole. It communicates
// with the host serial bridge daemon over loopback-TLS.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const dialTimeout = 30 * time.Second

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	cmd := args[0]
	subArgs := args[1:]

	switch cmd {
	case "list":
		return runList(subArgs)
	case "read":
		return runRead(subArgs)
	case "write":
		return runWrite(subArgs)
	case "monitor", "stream":
		return runMonitor(subArgs)
	case "pty", "bridge":
		return runPty(subArgs)
	case "-h", "--help", "help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "yolo-serial: unknown subcommand %q\nRun `yolo-serial --help` for usage.\n", cmd)
		return 2
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `yolo-serial — Access and monitor host serial devices from inside YOLO Jail.

Usage:
  yolo-serial list [--json] [--endpoint PATH]
  yolo-serial read <device> [--baud RATE] [--timeout DURATION] [--max-bytes N] [--endpoint PATH]
  yolo-serial write <device> <data> [--baud RATE] [--no-newline] [--endpoint PATH]
  yolo-serial monitor <device> [--baud RATE] [--endpoint PATH]
  yolo-serial pty <device> [--link PATH] [--baud RATE] [--endpoint PATH]

Subcommands:
  list       List host serial devices matching allowlist patterns
  read       Read available data from a host serial port
  write      Send data to a host serial port
  monitor    Stream serial output continuously with auto-reconnect on resets
  pty        Create a virtual pseudo-terminal (PTY) device node in the jail
             that standard serial tools (minicom, esptool, screen, etc.) can connect to

Options:
  --link PATH        Create a symlink to the virtual PTY (e.g. /tmp/ttyUSB0)
  --endpoint PATH    Override endpoint file (default: $YOLO_SERVICE_SERIAL_ENDPOINT)
  --baud RATE        Baud rate (default: 115200)
  --timeout DURATION Read timeout (e.g. 2s, 500ms; default: 2s)
  --json             Format device list as JSON
`)
}

func resolveEndpoint(custom string) (string, error) {
	if custom != "" {
		return custom, nil
	}
	if ep := os.Getenv("YOLO_SERVICE_SERIAL_ENDPOINT"); ep != "" {
		return ep, nil
	}
	return "", errors.New("no endpoint")
}

func noEndpointMsg(w io.Writer) {
	fmt.Fprintln(w,
		"yolo-serial: no endpoint.  The serial loophole isn't wired up in "+
			"this jail.  Two things turn it on:\n"+
			"  1. ~/.config/yolo-jail/config.jsonc:  \"packs\": [..., \"serial\"]\n"+
			"  2. ~/.config/yolo-jail/config.jsonc:  \"loopholes\": {\"serial\": {\"enabled\": true}}\n"+
			"Then restart the jail.")
}

func runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	jsonFormat := fs.Bool("json", false, "Output in JSON format")
	endpoint := fs.String("endpoint", "", "Override endpoint file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ep, err := resolveEndpoint(*endpoint)
	if err != nil {
		noEndpointMsg(os.Stderr)
		return 2
	}

	req := map[string]any{"mode": "list"}
	if *jsonFormat {
		req["format"] = "json"
	}
	return call(ep, req)
}

func runRead(args []string) int {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	baud := fs.Int("baud", 115200, "Baud rate")
	timeout := fs.Duration("timeout", 2*time.Second, "Read timeout duration")
	maxBytes := fs.Int("max-bytes", 65536, "Max bytes to read")
	endpoint := fs.String("endpoint", "", "Override endpoint file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	remaining := fs.Args()
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yolo-serial read <device> [options]")
		return 2
	}

	ep, err := resolveEndpoint(*endpoint)
	if err != nil {
		noEndpointMsg(os.Stderr)
		return 2
	}

	req := map[string]any{
		"mode":       "read",
		"device":     remaining[0],
		"baud":       *baud,
		"timeout_ms": int(timeout.Milliseconds()),
		"max_bytes":  *maxBytes,
	}
	return call(ep, req)
}

func runWrite(args []string) int {
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	baud := fs.Int("baud", 115200, "Baud rate")
	noNewline := fs.Bool("no-newline", false, "Do not append newline to data")
	endpoint := fs.String("endpoint", "", "Override endpoint file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	remaining := fs.Args()
	if len(remaining) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yolo-serial write <device> <data> [options]")
		return 2
	}

	ep, err := resolveEndpoint(*endpoint)
	if err != nil {
		noEndpointMsg(os.Stderr)
		return 2
	}

	req := map[string]any{
		"mode":           "write",
		"device":         remaining[0],
		"data":           remaining[1],
		"baud":           *baud,
		"append_newline": !*noNewline,
	}
	return call(ep, req)
}

func runMonitor(args []string) int {
	fs := flag.NewFlagSet("monitor", flag.ContinueOnError)
	baud := fs.Int("baud", 115200, "Baud rate")
	endpoint := fs.String("endpoint", "", "Override endpoint file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	remaining := fs.Args()
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yolo-serial monitor <device> [options]")
		return 2
	}

	ep, err := resolveEndpoint(*endpoint)
	if err != nil {
		noEndpointMsg(os.Stderr)
		return 2
	}

	req := map[string]any{
		"mode":   "monitor",
		"device": remaining[0],
		"baud":   *baud,
	}
	return call(ep, req)
}

func runPty(args []string) int {
	fs := flag.NewFlagSet("pty", flag.ContinueOnError)
	baud := fs.Int("baud", 115200, "Baud rate")
	link := fs.String("link", "", "Symlink path to create pointing to the PTY (e.g. /tmp/ttyUSB0)")
	endpoint := fs.String("endpoint", "", "Override endpoint file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	remaining := fs.Args()
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yolo-serial pty <device> [--link PATH] [--baud RATE]")
		return 2
	}

	device := remaining[0]
	ep, err := resolveEndpoint(*endpoint)
	if err != nil {
		noEndpointMsg(os.Stderr)
		return 2
	}

	masterFile, slavePath, err := openPty()
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo-serial: cannot allocate virtual PTY: %v\n", err)
		return 1
	}
	defer masterFile.Close()

	if *link != "" {
		_ = os.Remove(*link)
		if err := os.Symlink(slavePath, *link); err != nil {
			fmt.Fprintf(os.Stderr, "yolo-serial: warning: failed to create symlink %s -> %s: %v\n", *link, slavePath, err)
		} else {
			defer os.Remove(*link)
		}
	}

	fmt.Println("=== YOLO Jail Serial PTY Bridge Active ===")
	fmt.Printf("Host Device:  %s (%d baud)\n", device, *baud)
	fmt.Printf("Virtual PTY:  %s\n", slavePath)
	if *link != "" {
		fmt.Printf("Symlink:      %s -> %s\n", *link, slavePath)
		fmt.Printf("\nPoint serial tools (minicom, esptool, screen, etc.) to:\n  %s\n\n", *link)
	} else {
		fmt.Printf("\nPoint serial tools (minicom, esptool, screen, etc.) to:\n  %s\n\n", slavePath)
	}
	fmt.Println("Press Ctrl+C to disconnect.")

	conn, err := svcendpoint.Dial(ep, dialTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo-serial: dial daemon failed: %v\n", err)
		return 2
	}
	defer conn.Close()

	req := map[string]any{"mode": "monitor", "device": device, "baud": *baud}
	body, _ := json.Marshal(req)
	if err := frameproto.WriteRequest(conn, body); err != nil {
		fmt.Fprintf(os.Stderr, "yolo-serial: send request failed: %v\n", err)
		return 1
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	stopCh := make(chan struct{})

	go func() {
		<-sigCh
		close(stopCh)
		conn.Close()
		masterFile.Close()
	}()

	// Pump PTY master -> loopback-TLS connection
	go func() {
		buf := make([]byte, 1024)
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			n, err := masterFile.Read(buf)
			if n > 0 {
				_, _ = frameproto.WriteFrame(conn, frameproto.StreamStdout, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	// Read frames from loopback-TLS connection -> PTY master
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			break
		}
		if f.StreamID == frameproto.StreamStdout {
			_, _ = masterFile.Write(f.Payload)
		}
		if f.StreamID == frameproto.StreamStderr {
			os.Stderr.Write(f.Payload)
		}
		if f.StreamID == frameproto.StreamExit {
			break
		}
	}

	return 0
}

func call(endpointPath string, request map[string]any) int {
	conn, err := svcendpoint.Dial(endpointPath, dialTimeout)
	if err != nil {
		switch {
		case errors.Is(err, svcendpoint.ErrEndpointMissing):
			fmt.Fprintf(os.Stderr, "yolo-serial: no endpoint published at %s.\n", endpointPath)
		case errors.Is(err, svcendpoint.ErrEndpointMalformed):
			fmt.Fprintf(os.Stderr, "yolo-serial: endpoint file %s is malformed.\n", endpointPath)
		case errors.Is(err, svcendpoint.ErrAuthRejected):
			fmt.Fprintf(os.Stderr, "yolo-serial: serial daemon rejected this jail's token.\n")
		default:
			fmt.Fprintf(os.Stderr, "yolo-serial: cannot reach serial daemon at %s: %v\n", endpointPath, err)
		}
		return 2
	}
	defer conn.Close()

	body, _ := json.Marshal(request)
	if err := frameproto.WriteRequest(conn, body); err != nil {
		fmt.Fprintf(os.Stderr, "yolo-serial: failed to send request: %v\n", err)
		return 1
	}

	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "yolo-serial: stream error: %v\n", err)
			return 1
		}
		switch f.StreamID {
		case frameproto.StreamStdout:
			os.Stdout.Write(f.Payload)
		case frameproto.StreamStderr:
			os.Stderr.Write(f.Payload)
		case frameproto.StreamExit:
			if rc, err := frameproto.ExitCode(f.Payload); err == nil {
				return rc
			}
			return 0
		}
	}
	return 0
}
