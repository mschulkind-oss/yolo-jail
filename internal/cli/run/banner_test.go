package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/banner"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

func TestLaunchBanner(t *testing.T) {
	got := LaunchBanner("podman", "yolo-x-abc", "0.6.0", "", nil)
	if got != "Jail: yolo-x-abc | podman" {
		t.Errorf("banner = %q", got)
	}

	// The attach path shows the container's baked version only when it differs
	// from the host's — a host CLI attaching to a pre-upgrade jail is running
	// against stale shims, mounts and entrypoint.
	got = LaunchBanner("podman", "c", "0.6.0", "0.5.0", nil)
	if got != "Jail: c | podman | built at 0.5.0" {
		t.Errorf("attached banner = %q", got)
	}
	if got := LaunchBanner("podman", "c", "0.6.0", "0.6.0", nil); strings.Contains(got, "built at") {
		t.Errorf("banner = %q — a jail built at the host's own version is not worth a field", got)
	}

	got = LaunchBanner("podman", "c", "0.6.0", "", []string{"pids=32768"})
	if !strings.HasSuffix(got, "\nResource limits: pids=32768") {
		t.Errorf("res banner = %q", got)
	}
}

// THE LAUNCH LINE MUST NOT REPEAT THE STARTUP BANNER. internal/cli's dispatch
// writes `yolo-jail <version> | <platform> | host` to stderr before this package
// is ever entered, so a launch line that also carried the version and the
// platform would print both fields twice on the single most-used command — the
// double-print this split exists to avoid.
//
// Spelled as a property of the rendered line rather than of the signature: the
// signature still TAKES a hostVersion (it needs one to decide whether the baked
// jail version is worth showing), so "it cannot know the version" is not the
// invariant. "It does not print it" is.
func TestLaunchBannerDoesNotRepeatTheStartupFields(t *testing.T) {
	got := LaunchBanner("podman", "yolo-x-abc", "0.6.0", "", []string{"pids=32768"})
	if strings.Contains(got, "yolo-jail") {
		t.Errorf("launch line = %q — the version line is the startup banner's, printed at dispatch", got)
	}
	if strings.Contains(got, banner.Platform()) {
		t.Errorf("launch line = %q — the platform is the startup banner's, printed at dispatch", got)
	}
	// The one version-shaped field it may carry is the ATTACHED jail's, and only
	// as "built at", never as a second `yolo-jail <v>`.
	attached := LaunchBanner("podman", "c", "0.6.0", "0.5.0", nil)
	if strings.Contains(attached, "yolo-jail") {
		t.Errorf("attach line = %q — must not re-render the product name + version", attached)
	}
}

// emitLaunchBanner must TERMINATE its line. LaunchBanner returns no trailing
// newline, and emitLaunchBanner used Fprint, so the next writer landed on the same
// line: a nested launch printed "…Resource limits: pids=32768No packs are configured…".
// It hid for as long as the next writer was the container's own output (which opens
// with a newline of its own), and surfaced the moment a host-side notice followed.
func TestEmitLaunchBannerEndsItsLine(t *testing.T) {
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{}, false }
	o.emitLaunchBanner("podman", "yolo-x-abc", []string{"pids=32768"}, "")
	if got := errBuf.String(); !strings.HasSuffix(got, "\n") {
		t.Errorf("banner does not end its line — the next line will be glued on: %q", got)
	}
}
