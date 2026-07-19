// Command yolo-host-processes is the allowlisted host-process viewer daemon.
// It keeps exec'ing the real `ps` — the output format is the contract.
//
// CLI contract: --socket, --config, --self-check. Config defaults to
// $YOLO_HOST_PROCESSES_CONFIG or CWD/yolo-jail.jsonc.
//
// The daemon body lives in internal/hostprocesses.Main so it can also be
// reached in-process via the hidden `yolo internal daemon host-processes`.
package main

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/hostprocesses"
)

func main() {
	os.Exit(hostprocesses.Main(os.Args[1:]))
}
