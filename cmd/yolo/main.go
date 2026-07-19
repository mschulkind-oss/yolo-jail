// Command yolo is the yolo-jail CLI. It handles all subcommands natively.
package main

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/frontdoor"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if cwd := frontdoor.InvocationCWD(); cwd != "" {
		_ = os.Chdir(cwd)
	}

	if len(argv) >= 1 && argv[0] == "internal" {
		return runInternal(argv[1:])
	}

	if len(argv) >= 1 && argv[0] == "--version" {
		fmt.Println("yolo-jail " + version.Get(os.Getenv("YOLO_REPO_ROOT")))
		return 0
	}

	args := frontdoor.RewriteArgv(argv)
	sub := frontdoor.Subcommand(args)

	if !frontdoor.IsNative(sub) {
		fmt.Fprintf(os.Stderr, "yolo: unknown command %q\n", sub)
		return 1
	}
	return dispatchNative(sub, args)
}

// dispatchNative handles native subcommands. Registered in native.go.
func dispatchNative(sub string, args []string) int {
	if fn, ok := nativeDispatch[sub]; ok {
		return fn(args)
	}
	fmt.Fprintf(os.Stderr, "yolo: unimplemented command %q\n", sub)
	return 1
}
