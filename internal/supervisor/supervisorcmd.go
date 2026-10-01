package supervisor

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// StartedLinePrefix begins the one line Main writes on its OWN stderr once it holds a payload
// naming at least one valid daemon, just before it starts them: `yolo-jaild supervise:
// supervising <name>, <name> (pid <n>)`.
//
// It is the supervisor's readiness line, and the macos-user launcher is its reader: there the
// guest's supervisor sends its stdout and stderr to supervisor.log beside the daemons' own logs,
// and the launch prints "Started …" only once this line has appeared in it
// (internal/macosuser's startJailDaemons; macos-user-nix-and-features.md JD-8). Reaching it
// proves every layer in front of the supervisor let it run — sudo, sandbox-exec, the env-file
// reader and the exec of this binary. In a container the supervisor's stderr is /dev/null
// (internal/entrypoint's startJailDaemonSupervisor), so the line changes nothing there.
const StartedLinePrefix = "yolo-jaild supervise: supervising "

// stderr is where Main writes its own lines; a variable so the test can read them.
var stderr io.Writer = os.Stderr

// Main is the entry point for the `yolo-jaild supervise` subcommand. It reads
// YOLO_JAIL_DAEMONS from the env and supervises each daemon as a subprocess
// (start, restart-per-policy, log-rotate, SIGTERM/SIGINT teardown). Baked into
// the jail image.
//
// argv carries any args after the `supervise` subcommand; the supervisor takes
// no flags (its whole input is YOLO_JAIL_DAEMONS), so argv is accepted only to
// match the yolo-jaild dispatch signature.
//
// Its own stderr carries one line saying what it is doing — the readiness line above, or why it
// is exiting with nothing to do — because a caller that sends that stderr to a file (the
// macos-user guest) has no other way to tell "started" from "exited at once".
func Main(argv []string) int {
	raw := strings.TrimSpace(os.Getenv("YOLO_JAIL_DAEMONS"))
	if raw == "" {
		fmt.Fprintln(stderr, "yolo-jaild supervise: YOLO_JAIL_DAEMONS is unset; nothing to supervise")
		return 0
	}
	specs := ParseEnv(raw)
	if len(specs) == 0 {
		fmt.Fprintln(stderr, "yolo-jaild supervise: YOLO_JAIL_DAEMONS names no valid daemon; "+
			"nothing to supervise")
		return 0
	}

	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	var once bool
	go func() {
		<-sigCh
		if !once {
			once = true
			close(stop)
		}
	}()

	// After signal.Notify, so a reader that has seen this line may already signal the supervisor.
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	fmt.Fprintf(stderr, "%s%s (pid %d)\n", StartedLinePrefix, strings.Join(names, ", "), os.Getpid())

	Run(specs, stop)
	return 0
}
