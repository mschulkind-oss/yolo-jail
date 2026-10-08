package nixroots

import (
	"context"
	"testing"
	"time"
)

// Run's inotify loop exists only on Linux (watch_linux.go); elsewhere Run refuses
// (watch_other_test.go), the design's §5.2 running no watcher on macos-user.
func TestTheWatcherKeepsAJailLinkTheDaemonWasAskedToRoot(t *testing.T) {
	f, w := watchFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() {
		cancel()
		if done != nil {
			<-done
		}
	}()

	src := f.userLink(t, "result", f.paths[0])
	// Let the watch be placed before the event, as it is at boot (Run watches, then scans).
	time.Sleep(100 * time.Millisecond)
	autoEntry(t, w, "aaaa", src)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			done = nil
			t.Fatalf("the watcher stopped before keeping the root: %v", err)
		default:
		}
		roots, _ := f.reg.List()
		if len(roots) == 1 && roots[0].Source == src && roots[0].SourceHost == "/host/proj/result" &&
			roots[0].Target == f.paths[0] {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	roots, _ := f.reg.List()
	t.Fatalf("watcher kept %+v, want one root for %s", roots, src)
}
