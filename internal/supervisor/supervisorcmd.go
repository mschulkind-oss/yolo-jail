package supervisor

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// Main is the entry point for the `yolo-jaild supervise` subcommand. It reads
// YOLO_JAIL_DAEMONS from the env and supervises each daemon as a subprocess
// (start, restart-per-policy, log-rotate, SIGTERM/SIGINT teardown). Baked into
// the jail image.
//
// argv carries any args after the `supervise` subcommand; the supervisor takes
// no flags (its whole input is YOLO_JAIL_DAEMONS), so argv is accepted only to
// match the yolo-jaild dispatch signature.
func Main(argv []string) int {
	raw := strings.TrimSpace(os.Getenv("YOLO_JAIL_DAEMONS"))
	if raw == "" {
		// "YOLO_JAIL_DAEMONS unset — nothing to supervise"
		return 0
	}
	specs := ParseEnv(raw)
	if len(specs) == 0 {
		// "no valid daemons to supervise"
		return 0
	}

	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	var once bool
	go func() {
		<-sigCh
		if !once {
			once = true
			close(stop)
		}
	}()

	Run(specs, stop)
	return 0
}
