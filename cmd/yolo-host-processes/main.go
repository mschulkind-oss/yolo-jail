// Command yolo-host-processes is the Go port of src/host_processes.py — the
// allowlisted host-process viewer daemon. Selected during the go-port soak by
// YOLO_GO_DAEMONS + YOLO_GO_BIN_DIR (Stage 5); the binary name equals the
// Python console-script name so the manifest/doctor contract holds. It keeps
// exec'ing the real `ps` — the output format is the contract.
//
// CLI contract (byte-frozen against the Python argparse): --socket, --config,
// --self-check. Config defaults to $YOLO_HOST_PROCESSES_CONFIG or CWD/yolo-jail.jsonc.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/hostprocesses"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

func main() {
	os.Exit(run())
}

func run() int {
	socket := flag.String("socket", "", "Unix socket to bind")
	config := flag.String("config", "", "yolo-jail.jsonc path (defaults to $YOLO_HOST_PROCESSES_CONFIG)")
	selfCheck := flag.Bool("self-check", false, "Emit status and exit (used by `yolo doctor`)")
	flag.Parse()

	if *selfCheck {
		return hostprocesses.SelfCheck()
	}
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --socket is required")
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
	if err := hostservice.Serve(hostprocesses.BuildHandler(cfg), *socket, stop); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-host-processes:", err)
		return 1
	}
	return 0
}
