package brokerrelay

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
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
	tcpPublish := fs.String("tcp-publish", "", "optional loopback TCP front (macOS): bind 127.0.0.1:0 and publish the advertised host:port to this file for the in-jail terminator; empty disables it")
	tcpAdvertise := fs.String("tcp-advertise", "host.containers.internal", "host the jail uses to reach the TCP front (written into --tcp-publish)")
	tokenFile := fs.String("token-file", "", "file holding the per-jail bearer token required on the TCP front's leading frame (host-only; passed by path so the secret isn't visible in process listings)")
	_ = fs.Parse(argv)

	// argparse: required args missing -> exit 2 with a usage error.
	if *socket == "" || *broker == "" || *jail == "" {
		fmt.Fprintln(os.Stderr, "yolo-broker-relay: --socket, --broker, and --jail are required")
		return 2
	}
	// The TCP front (macOS transport) must not run without a token — a tokenless
	// loopback port would expose the broker to any local process. The token
	// arrives by file (not argv) so it stays out of process listings.
	token := ""
	if *tokenFile != "" {
		data, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "yolo-broker-relay: reading --token-file:", err)
			return 2
		}
		token = strings.TrimSpace(string(data))
	}
	if *tcpPublish != "" && token == "" {
		fmt.Fprintln(os.Stderr, "yolo-broker-relay: --tcp-publish requires a non-empty --token-file")
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

	cfg := Config{SocketPath: *socket, BrokerPath: *broker, JailID: *jail}
	if *tcpPublish != "" {
		cfg.TCP = &TCPFront{PublishPath: *tcpPublish, AdvertiseHost: *tcpAdvertise, Token: token}
	}
	if err := Serve(cfg, stop); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-broker-relay:", err)
		return 1
	}
	return 0
}
