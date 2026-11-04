package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// TestDescribeAnswersForTheSelectedSetup: describe's notch is the setup a launch from here would
// select (config.SelectSetup), so the user's `setup` and YOLO_SETUP move it as they move the
// launch, and the setup line names the setup and where it came from.
func TestDescribeAnswersForTheSelectedSetup(t *testing.T) {
	for _, tc := range []struct {
		name, env, wantSetup, wantFrom string
		wantNotch                      render.Kind
	}{
		{name: "the user's setup", wantSetup: "host-self", wantFrom: "your user config", wantNotch: render.KindHost},
		{name: "YOLO_SETUP outranks it", env: "podman-self", wantSetup: "podman-self", wantFrom: "YOLO_SETUP", wantNotch: render.KindJail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, repo := withHomeAndCwd(t)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("YOLO_RUNTIME", "")
			t.Setenv("YOLO_SETUP", tc.env)
			writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"), "setup = \"host-self\"\n")
			writeFile(t, filepath.Join(repo, "yolo-jail.toml"), "\n")
			var out, errw bytes.Buffer
			if rc := describeMain(nil, &out, &errw, false); rc != 0 {
				t.Fatalf("describe rc=%d: %s", rc, errw.String())
			}
			if line := describeLine(t, out.String(), "setup "); !strings.Contains(line, tc.wantSetup) ||
				!strings.Contains(line, "from "+tc.wantFrom) {
				t.Errorf("setup line = %q, want %s from %s", line, tc.wantSetup, tc.wantFrom)
			}
			if line := describeLine(t, out.String(), "environment "); !strings.Contains(line, "confinement "+tc.wantNotch.String()) {
				t.Errorf("environment line = %q, want the %s notch", line, tc.wantNotch)
			}
		})
	}
}

// TestDescribeTakesASetupFlagAndPrintsTheSelectionsWarnings: --setup outranks YOLO_SETUP and the
// user's `setup`, as it does for a launch; a --setup no machine defines is misuse, exit 2; and
// the selection's warnings (here SUP-D12's ignored inherited YOLO_SETUP) reach stderr in every
// output mode.
func TestDescribeTakesASetupFlagAndPrintsTheSelectionsWarnings(t *testing.T) {
	home, repo := withHomeAndCwd(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_RUNTIME", "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"), "setup = \"host-self\"\n")
	writeFile(t, filepath.Join(repo, "yolo-jail.toml"), "\n")

	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_SETUP", "host-self")
	var out, errw bytes.Buffer
	if rc := describeMain([]string{"--setup", "podman-self"}, &out, &errw, false); rc != 0 {
		t.Fatalf("describe --setup podman-self rc=%d: %s", rc, errw.String())
	}
	if line := describeLine(t, out.String(), "setup "); !strings.Contains(line, "podman-self") {
		t.Errorf("--setup podman-self: setup line = %q", line)
	}
	out.Reset()
	errw.Reset()
	if rc := describeMain([]string{"--setup=no-such-setup"}, &out, &errw, false); rc != 2 ||
		!strings.Contains(errw.String(), "no-such-setup") {
		t.Errorf("--setup no-such-setup: rc=%d stderr=%q, want 2 naming it", rc, errw.String())
	}

	onPlatform(t, false)
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	t.Setenv("YOLO_SETUP", "seatbelt-self")
	for _, args := range [][]string{nil, {"--json"}, {"--hash"}} {
		out.Reset()
		errw.Reset()
		if rc := describeMain(args, &out, &errw, false); rc != 0 {
			t.Fatalf("describe %v rc=%d: %s", args, rc, errw.String())
		}
		if !strings.Contains(errw.String(), "YOLO_SETUP=seatbelt-self came from the session") {
			t.Errorf("describe %v: stderr %q lacks the inherited-setup warning", args, errw.String())
		}
	}
}
