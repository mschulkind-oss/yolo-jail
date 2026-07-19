// Command yolo-journald is the builtin journal-bridge daemon. It listens on a
// Unix socket, reads a newline-terminated JSON request, validates the journalctl
// args, execs journalctl, and streams stdout/stderr/exit back as ">BI" frames
// with stream IDs 1/2/3 (DELIBERATELY distinct from the loophole protocol's
// 0/1/2).
//
// Frozen: socket chmod 0777, the arg validation + "user"-mode --user prepend,
// the journalctl-not-found (127) / spawn-failure (1) exit codes.
//
// The daemon body lives in internal/journald.Main so it can also be reached
// in-process via the hidden `yolo internal daemon journal`.
package main

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/journald"
)

func main() {
	os.Exit(journald.Main(os.Args[1:]))
}
