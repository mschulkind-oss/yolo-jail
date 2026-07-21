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
// CLI contract: --socket, --config, --self-check. Config defaults to
// $YOLO_HOST_PROCESSES_CONFIG or CWD/yolo-jail.jsonc.
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-host-processes", flag.ExitOnError)
	socket := fs.String("socket", "", "Unix socket to bind")
	config := fs.String("config", "", "yolo-jail.jsonc path (defaults to $YOLO_HOST_PROCESSES_CONFIG)")
	selfCheck := fs.Bool("self-check", false, "Emit status and exit (used by `yolo doctor`)")
	_ = fs.Parse(argv)

	if *selfCheck {
		return SelfCheck()
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
	if err := hostservice.Serve(BuildHandler(cfg), *socket, stop); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-host-processes:", err)
		return 1
	}
	return 0
}
