//go:build linux

package run

// embeddedrelease_test.go pins the ORDER inside the launcher's signal arm: the caller's
// teardown first, the embedded-tree release last.
//
// ttyproxy runs onTerminate and then os.Exit(128+n); os.Exit runs no defers, so cli.Main's
// deferred packload.ReleaseEmbedded never fires on Ctrl-C / window close / SIGTERM, and a
// per-process FALLBACK tree leaked on every interrupted launch. runWithProxy wraps every
// caller's onTerminate (withEmbeddedRelease) so the release runs on every arm, including the
// two that pass none. It must run LAST because the teardown (stopLoopholes, the capture) can
// still read an embedded Pack.Root.
//
// The wrapper being ON the arm is pinned by process, in embeddedrelease_linux_test.go: a
// child under the actual ttyproxy signal arm, killed with SIGTERM, with the real release and
// with it stubbed out.

import "testing"

func TestTerminateArmReleasesAfterTheCallersTeardown(t *testing.T) {
	var order []string
	orig := terminateRelease
	terminateRelease = func() { order = append(order, "release") }
	t.Cleanup(func() { terminateRelease = orig })

	withEmbeddedRelease(func() { order = append(order, "teardown") })()
	if len(order) != 2 || order[0] != "teardown" || order[1] != "release" {
		t.Errorf("signal arm ran %v, want [teardown release]: the teardown can still read a Pack.Root", order)
	}

	order = nil
	withEmbeddedRelease(nil)()
	if len(order) != 1 || order[0] != "release" {
		t.Errorf("signal arm with no teardown ran %v, want [release] — the attach arm and the "+
			"macos-user seam pass none, and their fallback tree would leak", order)
	}
}
