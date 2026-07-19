// Command goprobe is a deployment-mechanics tripwire. It proves the whole
// build channel — nix cross-compile → image bake → `just load` → nested jail —
// works. Deliberately dependency-free; does nothing but print a line the
// harness can grep for.
package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	fmt.Printf("goprobe ok: %s/%s (go %s)\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	// A non-default arg lets a smoke test assert argv passthrough works.
	if len(os.Args) > 1 {
		fmt.Printf("args: %v\n", os.Args[1:])
	}
}
