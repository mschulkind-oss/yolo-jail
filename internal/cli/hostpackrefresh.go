package cli

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// hostpackrefresh.go is the HOST NOTCH's launch-time pack fetch — the same
// run.RefreshConfiguredPacks a jail launch runs, called at the host's two launch-shaped
// entries: `yolo host -- <bin>` (before the apply gate and the composition, both of which
// resolve packs) and `yolo host apply` / `yolo apply --at host` (before the render).
//
// NOT inside applyHost, where it would also run inside the launch gate's observe pass and
// its one-second budget (hostapplygate.go), and not in `yolo host env`, `yolo check-deps`
// or any other observe verb: a read-only surface never fetches.

// refreshHostPacks runs the refresh, printing its disclosures and warnings to errw. A var
// only so a test can count the calls a path makes; production never reassigns it.
var refreshHostPacks = func(errw io.Writer) {
	run.RefreshConfiguredPacks(
		func(line string) { fmt.Fprintln(errw, line) },
		func(line string) { fmt.Fprintf(errw, "Warning: %s\n", line) },
	)
}
