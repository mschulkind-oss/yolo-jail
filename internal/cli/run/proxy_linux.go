//go:build linux

package run

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/ttyproxy"
)

// runWithProxy wraps ttyproxy.RunWithProxy (the in-process TTY proxy) so a
// host-side ^Z suspends the proxy instead of wedging the agent, SIGWINCH
// propagates, and Ctrl-C/window-close/SIGTERM tear the jail down via onTerminate.
// onStarted releases the workspace lock once the container is visible.
//
// The stage hook turns the proxy's own transitions into `child.*` marks on
// the timing collector — the pair child.exited/child.drain_done is what
// bounds the proxy-drain hypothesis (design H2), and a nil collector's Mark
// is a no-op, so the hook is unconditional at this altitude.
//
// THE SIGNAL ARM RELEASES THE EMBEDDED PACK TREE, for EVERY caller — the fresh launch, the
// attach arm and the macos-user seam alike, two of which pass no onTerminate at all. ttyproxy
// os.Exit(128+n)s straight after onTerminate, so cli.Main's deferred release never fires on
// that arm, and a per-process FALLBACK tree (packload's, the one shape that is the process's
// to delete) leaked on every Ctrl-C / window close / SIGTERM. The release runs AFTER the
// caller's onTerminate because that teardown can still read an embedded Pack.Root (the
// loophole resolver's fallback reads one).
//
// THE OBSERVER'S OTHER TWO HALVES feed the Window A probe (lingerprobe.go): every
// forwarded stdin chunk's SIZE (never its content) so the log can say whether a
// lingering client exits right after a keystroke, and the pty's line-discipline
// mode, read through the master, so it can say whether a forwarded ^C reached
// podman as data or as a signal.
func runWithProxy(cmd []string, onStarted func(*os.Process), onTerminate func(), o *Options) (int, error) {
	obs := ttyproxy.Observer{
		Stage: func(stage string) { o.Perf.Mark("child." + stage) },
		Input: o.noteForwardedInput,
		Pty:   o.linger.setPtyMode,
	}
	return ttyproxy.RunWithProxyObserved(cmd, onStarted, withEmbeddedRelease(onTerminate), obs)
}

// terminateRelease is what the signal arm calls last. A variable only so the control half of
// TestSignalArmReleasesTheFallbackTree can prove the release is what removes the tree.
var terminateRelease = packload.ReleaseEmbedded

// withEmbeddedRelease is onTerminate followed by the embedded-tree release. Never nil, so the
// release runs on the arms whose caller had no teardown of its own.
func withEmbeddedRelease(onTerminate func()) func() {
	return func() {
		if onTerminate != nil {
			onTerminate()
		}
		terminateRelease()
	}
}
