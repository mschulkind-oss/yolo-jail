// Command yolo-jaild is the in-jail infrastructure daemon. It is baked into the
// jail image and dispatches on its first argument to one of the in-jail
// daemons:
//
//	yolo-jaild supervise          # read YOLO_JAIL_DAEMONS + supervise each entry
//	yolo-jaild oauth-terminator   # in-jail TLS terminator for Claude OAuth
//
// This is a plain subcommand dispatch on args[0]; it is NOT argv[0]/symlink
// dispatch. Unknown or missing subcommands print usage and exit 2.
package main

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/oauthterminator"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		return usage()
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "supervise":
		return supervisor.Main(rest)
	case "oauth-terminator":
		return oauthterminator.Main(rest)
	default:
		return usage()
	}
}

func usage() int {
	fmt.Fprintln(os.Stderr, "usage: yolo-jaild <supervise|oauth-terminator> [args...]")
	return 2
}
