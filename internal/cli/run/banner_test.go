package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/banner"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

func TestLaunchBannerNamesSetupFieldsAndSource(t *testing.T) {
	got := LaunchBanner(setup.MustBuiltIn("podman-self"), "bridge", setup.FromDefault, "podman", "yolo-x-abc", "0.6.0", "", nil)
	if !strings.HasPrefix(got, "yolo: setup podman-self (Podman, as you; bridged) - from default") {
		t.Fatalf("launch banner does not identify the selected setup: %q", got)
	}
	got = LaunchBanner(setup.MustBuiltIn("seatbelt-self"), "host", setup.FromFlag, "macos-user", "yolo-x-abc", "0.6.0", "", nil)
	if !strings.HasPrefix(got, "yolo: setup seatbelt-self (Seatbelt, deny-by-default, as you; shares the Mac's network) - from --setup") {
		t.Fatalf("launch banner does not spell Seatbelt setup fields: %q", got)
	}
	hostNetwork := LaunchBanner(setup.MustBuiltIn("podman-self"), "host", setup.FromUserConfig, "podman", "yolo-x-abc", "0.6.0", "", nil)
	if !strings.HasPrefix(hostNetwork, "yolo: setup podman-self (Podman, as you; shares the host's network) - from user config") {
		t.Fatalf("launch banner does not report the applied host network and source: %q", hostNetwork)
	}
	envSelection := LaunchBanner(setup.MustBuiltIn("seatbelt-self"), "host", setup.FromEnv, "macos-user", "yolo-x-abc", "0.6.0", "", nil)
	if !strings.HasPrefix(envSelection, "yolo: setup seatbelt-self (Seatbelt, deny-by-default, as you; shares the Mac's network) - from YOLO_SETUP") {
		t.Fatalf("launch banner does not identify an inherited setup selection: %q", envSelection)
	}
}

func TestLaunchBannerAttachedVersionAndResources(t *testing.T) {
	s := setup.MustBuiltIn("podman-self")
	got := LaunchBanner(s, "bridge", setup.FromEnv, "podman", "c", "0.6.0", "0.5.0", nil)
	if !strings.Contains(got, "built at 0.5.0") {
		t.Errorf("attached banner omitted the jail version: %q", got)
	}
	if got := LaunchBanner(s, "bridge", setup.FromEnv, "podman", "c", "0.6.0", "0.6.0", nil); strings.Contains(got, "built at") {
		t.Errorf("banner = %q — a jail built at the host's own version is not worth a field", got)
	}
	got = LaunchBanner(s, "bridge", setup.FromEnv, "podman", "c", "0.6.0", "", []string{"pids=32768"})
	if !strings.HasSuffix(got, "\nResource limits: pids=32768") {
		t.Errorf("res banner = %q", got)
	}
}

// THE LAUNCH LINE MUST NOT REPEAT THE STARTUP BANNER. internal/cli's dispatch
// writes `yolo-jail <version> | <platform> | host` to stderr before this package
// is ever entered, so a launch line that also carried the version and the
// platform would print both fields twice on the single most-used command — the
// double-print this split exists to avoid.
func TestLaunchBannerDoesNotRepeatTheStartupFields(t *testing.T) {
	s := setup.MustBuiltIn("podman-self")
	got := LaunchBanner(s, "bridge", setup.FromDefault, "podman", "yolo-x-abc", "0.6.0", "", []string{"pids=32768"})
	if strings.Contains(got, "yolo-jail") {
		t.Errorf("launch line = %q — the version line is the startup banner's, printed at dispatch", got)
	}
	if strings.Contains(got, banner.Platform()) {
		t.Errorf("launch line = %q — the platform is the startup banner's, printed at dispatch", got)
	}
	attached := LaunchBanner(s, "bridge", setup.FromDefault, "podman", "c", "0.6.0", "0.5.0", nil)
	if strings.Contains(attached, "yolo-jail") {
		t.Errorf("attach line = %q — must not re-render the product name + version", attached)
	}
}

func TestEmitLaunchBannerColorsOnATerminal(t *testing.T) {
	render := func(color, tty bool) string {
		var buf bytes.Buffer
		o := goldenOptions("/ws", t.TempDir())
		o.Stderr = &buf
		o.Setup = setup.MustBuiltIn("podman-self")
		o.setupChoice = config.SetupChoice{Setup: o.Setup, Source: setup.FromDefault}
		o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{}, false }
		o.Color = color
		o.IsTTYStderr = func() bool { return tty }
		o.Getenv = func(string) string { return "" } // no ambient NO_COLOR
		o.emitLaunchBanner("podman", "yolo-x-abc", nil, []string{"pids=32768"}, "")
		return buf.String()
	}

	plain := render(false, false)
	colored := render(true, true)
	if !strings.Contains(colored, "\x1b[") {
		t.Fatalf("control: a terminal launch line emitted no ANSI: %q", colored)
	}
	if got := string(stripANSI([]byte(colored))); got != plain {
		t.Errorf("the colored launch line is not the plain one plus escapes:\n stripped %q\n plain %q",
			got, plain)
	}
	if off := render(true, false); off != plain {
		t.Errorf("a launch line off a terminal is not byte-identical to the plain one:\n off %q\n plain %q",
			off, plain)
	}
}
