package check

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestMacOSPlatformAsksForNoHostImageTool: an Apple Container Mac with neither
// skopeo nor podman on PATH is a complete setup, so the macOS Platform section
// must neither warn about it nor go looking for either tool.
//
// The section used to warn "No OCI conversion tool for Apple Container" and
// recommend `brew install skopeo`. That stopped being true at C9: delivery runs
// the flake-built copier by store path (image.ImageCopierBinary) and yolo tars
// the OCI layout itself, so a skopeo on PATH is never run. The warning was the
// one [WARN] every healthy fresh Mac showed, and the user guide had to tell
// people to ignore it.
//
// It drives the whole section rather than a helper, and records every PATH
// lookup, so re-adding the check anywhere in it fails here.
func TestMacOSPlatformAsksForNoHostImageTool(t *testing.T) {
	var out bytes.Buffer
	var looked []string
	o := &Options{IsMacOS: true, Stdout: &out, IsTTYStdout: func() bool { return false }}
	fillDefaults(o)
	o.Machine = "arm64"
	o.LookPath = func(name string) (string, bool) {
		looked = append(looked, name)
		return "/usr/local/bin/" + name, name == "container"
	}
	o.PathExists = func(p string) bool { return p == "/nix" }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		switch strings.Join(argv, " ") {
		case "container system status":
			return ExecResult{Ran: true, RC: 0, Stdout: "apiserver is running\n"}
		case "sw_vers -productVersion":
			return ExecResult{Ran: true, RC: 0, Stdout: "26.0\n"}
		case "mount":
			return ExecResult{Ran: true, RC: 0, Stdout: "/dev/disk3s7 on /nix (apfs, local, journaled)\n"}
		}
		return ExecResult{Ran: false}
	}
	r := newReporter(&out, false)
	o.sectionMacOSPlatform(r, nil)

	got := out.String()
	if !strings.Contains(got, "Apple Container system: running") {
		t.Fatalf("the section did not reach the Apple Container branch, so this test "+
			"proves nothing:\n%s", got)
	}
	if r.warned != 0 || r.failed != 0 {
		t.Errorf("a healthy Apple Container Mac must report no WARN/FAIL; got %d/%d:\n%s",
			r.warned, r.failed, got)
	}
	if strings.Contains(strings.ToLower(got), "skopeo") || strings.Contains(got, "OCI conversion") {
		t.Errorf("the section still talks about a host image tool yolo never runs:\n%s", got)
	}
	for _, name := range looked {
		if name == "skopeo" {
			t.Errorf("the section looked up skopeo on PATH; image delivery never runs a " +
				"PATH skopeo (image.ImageCopierBinary), so its presence decides nothing")
		}
	}
}
