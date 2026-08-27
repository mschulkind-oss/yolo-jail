package serialdaemon

import (
	"flag"
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// Main is the host serial bridge daemon entry point.
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-serial", flag.ExitOnError)
	endpoint := fs.String("endpoint", "", "Endpoint file to publish (jail-facing loopback-TLS)")
	socket := fs.String("socket", "", "AF_UNIX socket to bind (behind yolo's front)")
	settings := fs.String("settings", "", "Resolved settings file written by yolo")
	selfCheck := fs.Bool("self-check", false, "Emit status and exit (used by yolo doctor)")
	_ = fs.Parse(argv)

	if *selfCheck {
		return SelfCheck(*settings)
	}

	switch {
	case *socket == "" && *endpoint == "":
		fmt.Fprintln(os.Stderr, "ERROR: one of --endpoint or --socket is required")
		return 2
	case *socket != "" && *endpoint != "":
		fmt.Fprintln(os.Stderr, "ERROR: --endpoint and --socket are mutually exclusive")
		return 2
	}

	cfg := LoadSettings(*settings)
	stop := make(chan struct{})
	handler := BuildHandler(cfg)
	serve := func() error { return hostservice.ServeEndpoint(handler, *endpoint, stop) }
	if *socket != "" {
		serve = func() error { return hostservice.ServeFrontedUnix(handler, *socket, stop) }
	}

	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-serial:", err)
		return 1
	}
	return 0
}
