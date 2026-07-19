// Command yolo-broker-relay is the per-jail Claude OAuth broker relay.
//
// CLI contract: --socket, --broker, --jail, all required. On SIGTERM/SIGINT it
// shuts the listener and unlinks its own socket (only if the file is still the
// one it bound), then exits 0.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerrelay"
)

func main() {
	socket := flag.String("socket", "", "relay listen socket (inside the jail's host-services dir)")
	broker := flag.String("broker", "", "real broker socket, dialed per connection")
	jail := flag.String("jail", "", "container name stamped as jail_id on each request")
	flag.Parse()

	// argparse: required args missing -> exit 2 with a usage error.
	if *socket == "" || *broker == "" || *jail == "" {
		fmt.Fprintln(os.Stderr, "yolo-broker-relay: --socket, --broker, and --jail are required")
		os.Exit(2)
	}

	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sigCh
		brokerrelay.Logger.Printf("signal %v — shutting down", s)
		close(stop)
	}()

	if err := brokerrelay.Serve(*socket, *broker, *jail, stop); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-broker-relay:", err)
		os.Exit(1)
	}
}
