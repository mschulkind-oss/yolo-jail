package check

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestAContainerThatIsNotApplesIsNotAppleContainer: the name `container` is generic, so check
// asks the binary on PATH what it is before grading it as Apple Container, and a binary that is
// not Apple's is a finding naming the install and another setup, never a connected runtime.
func TestAContainerThatIsNotApplesIsNotAppleContainer(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		apple         bool
	}{
		{"Apple's CLI", "container CLI version 1.1.0 (build: release)", true},
		{"another container", "container 2.3 (some other tool)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			opts := baseOptions(t, &out)
			opts.IsMacOS = true
			opts.Getenv = func(k string) string {
				if k == "YOLO_SETUP" {
					return "apple-container-self"
				}
				return ""
			}
			opts.LookPath = func(name string) (string, bool) { return "/usr/local/bin/" + name, name == "container" }
			opts.Exec = fakeExec(map[string]ExecResult{
				"container --version":     {Stdout: tc.version, Ran: true, RC: 0},
				"container system status": {Stdout: "apiserver is running", Ran: true, RC: 0},
			})
			rt, msg, _ := opts.resolveRuntimeForCheck(jsonx.NewOrderedMap())
			if tc.apple {
				if rt != "container" || msg != "" {
					t.Errorf("Apple's CLI: rt=%q msg=%q, want container and no finding", rt, msg)
				}
				return
			}
			if rt != "" || !strings.Contains(msg, "is not Apple's container CLI") ||
				!strings.Contains(msg, "https://github.com/apple/container") ||
				!strings.Contains(msg, "yolo --setup podman-self -- claude") {
				t.Errorf("another container: rt=%q msg=%q, want a finding naming the install and another setup", rt, msg)
			}
		})
	}
}
