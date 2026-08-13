package hostprocesses

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// Main is the allowlisted host-process viewer daemon entry point. It keeps
// exec'ing the real `ps` — the output format is the contract.
//
// CLI contract: --endpoint, --config, --self-check. Config defaults to
// $YOLO_HOST_PROCESSES_CONFIG or CWD/yolo-jail.jsonc.
//
// --socket is retained as an ALIAS for --endpoint, not as a second transport: it
// is the escape hatch for a host yolo older than this binary, whose manifest still
// spawns us with `--socket {socket}`. The value is used identically — a path to
// publish an endpoint file at — so such a spawn produces a working daemon at an
// oddly named path rather than a daemon that refuses to start.
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-host-processes", flag.ExitOnError)
	endpoint := fs.String("endpoint", "", "Endpoint file to publish (loopback-TLS)")
	socket := fs.String("socket", "", "Alias for --endpoint (accepted for compatibility)")
	config := fs.String("config", "", "yolo-jail.jsonc path (defaults to $YOLO_HOST_PROCESSES_CONFIG)")
	selfCheck := fs.Bool("self-check", false, "Emit status and exit (used by `yolo doctor`)")
	_ = fs.Parse(argv)

	if *selfCheck {
		return SelfCheck()
	}
	publish := *endpoint
	if publish == "" {
		publish = *socket
	}
	if publish == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --endpoint is required")
		return 2
	}
	cfg := *config
	if cfg == "" {
		if env := os.Getenv("YOLO_HOST_PROCESSES_CONFIG"); env != "" {
			cfg = env
		} else {
			cwd, _ := os.Getwd()
			cfg = filepath.Join(cwd, "yolo-jail.jsonc")
		}
	}

	stop := make(chan struct{})
	if err := hostservice.Serve(BuildHandler(cfg), publish, stop); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-host-processes:", err)
		return 1
	}
	return 0
}
