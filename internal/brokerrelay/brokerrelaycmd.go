package brokerrelay

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Main is the per-jail Claude OAuth broker relay entry point.
//
// CLI contract: --socket, --broker, --jail, all required. On SIGTERM/SIGINT it
// shuts the listener and unlinks its own socket (only if the file is still the
// one it bound), then exits 0.
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-broker-relay", flag.ExitOnError)
	socket := fs.String("socket", "", "relay listen socket (inside the jail's host-services dir)")
	broker := fs.String("broker", "", "real broker socket, dialed per connection")
	jail := fs.String("jail", "", "container name stamped as jail_id on each request")
	_ = fs.Parse(argv)

	// argparse: required args missing -> exit 2 with a usage error.
	if *socket == "" || *broker == "" || *jail == "" {
		fmt.Fprintln(os.Stderr, "yolo-broker-relay: --socket, --broker, and --jail are required")
		return 2
	}

	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sigCh
		Logger.Printf("signal %v — shutting down", s)
		close(stop)
	}()

	if err := Serve(*socket, *broker, *jail, stop); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-broker-relay:", err)
		return 1
	}
	return 0
}
