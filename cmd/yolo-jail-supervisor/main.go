// Command yolo-jail-supervisor reads YOLO_JAIL_DAEMONS from the env and
// supervises each daemon as a subprocess (start, restart-per-policy,
// log-rotate, SIGTERM/SIGINT teardown). Baked into the jail image.
package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
)

func main() {
	os.Exit(run())
}

func run() int {
	raw := strings.TrimSpace(os.Getenv("YOLO_JAIL_DAEMONS"))
	if raw == "" {
		// "YOLO_JAIL_DAEMONS unset — nothing to supervise"
		return 0
	}
	specs := supervisor.ParseEnv(raw)
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

	supervisor.Run(specs, stop)
	return 0
}
