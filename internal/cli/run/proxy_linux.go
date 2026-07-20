//go:build linux

package run

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/ttyproxy"
)

// runWithProxy wraps ttyproxy.RunWithProxy (the in-process TTY proxy) so a
// host-side ^Z suspends the proxy instead of wedging the agent, SIGWINCH
// propagates, and window-close/SIGTERM tears the jail down via onTerminate.
// onStarted releases the workspace lock once the container is visible.
func runWithProxy(cmd []string, onStarted func(*os.Process), onTerminate func()) (int, error) {
	return ttyproxy.RunWithProxy(cmd, onStarted, onTerminate)
}
