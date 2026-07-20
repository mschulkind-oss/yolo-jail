// Command yolo is the yolo-jail CLI. All logic lives in internal/cli; this is a
// thin shim that forwards os.Args and exits with the returned code.
package main

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args))
}
