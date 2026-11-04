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
