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
// CLI contract: --socket, --broker, --jail, all required; --endpoint optional.
// On SIGTERM/SIGINT it shuts the listener and unlinks its own socket (only if the
// file is still the one it bound), then exits 0.
//
// There is NO --token-file and no token flag of any kind. The front mints its own
// token in process memory and publishes it inside --endpoint, so no secret crosses
// this argv and none is persisted for something else to leak later.
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-broker-relay", flag.ExitOnError)
	socket := fs.String("socket", "", "HOST-ONLY relay listen socket (the loopback-TLS front splices into it)")
	broker := fs.String("broker", "", "real broker socket, dialed per connection")
	jail := fs.String("jail", "", "container name stamped as jail_id on each request")
	endpoint := fs.String("endpoint", "",
		"publish the jail-facing loopback-TLS endpoint file here (0600; it carries this jail's bearer token)")
	_ = fs.Parse(argv)

	// argparse: a bad flag combination -> exit 2 with a usage error.
	if msg := relayFlagError(*socket, *broker, *jail, *endpoint); msg != "" {
		fmt.Fprintln(os.Stderr, "yolo-broker-relay: "+msg)
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

	if err := Serve(Config{
		SocketPath:   *socket,
		BrokerPath:   *broker,
		JailID:       *jail,
		EndpointPath: *endpoint,
	}, stop); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-broker-relay:", err)
		return 1
	}
	return 0
}

// relayFlagError returns the usage error for a flag combination, or "" when it is
// runnable.
//
// A pure function so the combination that is easy to get wrong is testable without
// starting a daemon: --endpoint WITHOUT --socket would publish a credential for a
// front that splices into nothing, so a jail would authenticate successfully and
// then have its connection dropped — which reads as a broker failure. That check
// comes FIRST so the message names the real mistake rather than the generic
// "required" list.
func relayFlagError(socket, broker, jail, endpoint string) string {
	if endpoint != "" && socket == "" {
		return "--endpoint requires --socket: the loopback-TLS front splices into that " +
			"socket, so publishing an endpoint without one advertises a credential for a " +
			"listener that leads nowhere"
	}
	if socket == "" || broker == "" || jail == "" {
		return "--socket, --broker, and --jail are required"
	}
	return ""
}
