// Command goprobe is a throwaway deployment-mechanics tripwire (go-port
// plan Stage 0, docs/plans/go-port-plan.md §3). It proves the whole build
// channel — nix cross-compile → image bake → `just load` → nested jail →
// live-mount dist-go/ — works before any real module is ported.
//
// It is deliberately dependency-free and does nothing but print a line the
// harness can grep for. Deleted once the deployment channels are proven.
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
