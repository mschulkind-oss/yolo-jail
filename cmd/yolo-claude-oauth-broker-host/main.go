// Command yolo-claude-oauth-broker-host is the per-host Claude OAuth refresh
// daemon.
//
// CLI contract: --socket, --creds-file, --init-ca, --force-init-ca,
// --self-check, --no-background-refresh, -v/--verbose.
//
// The daemon body lives in internal/oauthbroker.Main so it can also be reached
// in-process via the hidden `yolo internal daemon claude-oauth-broker`.
package main

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
)

func main() {
	os.Exit(oauthbroker.Main(os.Args[1:]))
}
