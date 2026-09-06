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
//
// The stage hook turns the proxy's own transitions into `child.*` marks on
// the timing collector — the pair child.exited/child.drain_done is what
// bounds the proxy-drain hypothesis (design H2), and a nil collector's Mark
// is a no-op, so the hook is unconditional at this altitude.
func runWithProxy(cmd []string, onStarted func(*os.Process), onTerminate func(), o *Options) (int, error) {
	hook := ttyproxy.StageHook(func(stage string) { o.Perf.Mark("child." + stage) })
	return ttyproxy.RunWithProxyHooked(cmd, onStarted, onTerminate, hook)
}
