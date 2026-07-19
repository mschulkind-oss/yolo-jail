// Command yolo-broker-relay is the per-jail Claude OAuth broker relay.
//
// CLI contract: --socket, --broker, --jail, all required. On SIGTERM/SIGINT it
// shuts the listener and unlinks its own socket (only if the file is still the
// one it bound), then exits 0.
//
// The daemon body lives in internal/brokerrelay.Main so it can also be reached
// in-process via the hidden `yolo internal daemon broker-relay`.
package main

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerrelay"
)

func main() {
	os.Exit(brokerrelay.Main(os.Args[1:]))
}
