package cli

// buildlinestatus_test.go pins the build line's own exit status from where the capture driver
// records it to what the host says, on both arms: `yolo internal capture-run --line-status` writes
// it, a container build jail's argv asks for it in its workspace, and the macos-user act is told
// where in the host's staging workspace to move it. Before, every failed build read as the driver's
// exit, 1, and as a build line that ran and failed, also when the line had succeeded.

import (
	"bytes"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// THE DRIVER'S FLAG: --line-status reaches capture.Options.StatusFile, so the line's own status is
// recorded however the driver ends. Red with the flag's case or its hand-off deleted.
func TestCaptureRunRecordsTheBuildLinesOwnStatus(t *testing.T) {
	status := filepath.Join(t.TempDir(), capture.InstallerStatusLeaf)
	args := []string{"--out=" + filepath.Join(t.TempDir(), "out"), "--home=" + t.TempDir(),
		"--line-status=" + status, "--", "/bin/sh", "-c", "exit 7"}
	if rc := runCaptureRun(args); rc != 1 {
		t.Errorf("capture-run over a failed build line exited %d, want 1", rc)
	}
	if got, ok := capture.ReadInstallerStatus(status); !ok || got != 7 {
		t.Errorf("the recorded status is %d (recorded %v), want 7", got, ok)
	}
}

// A CONTAINER BUILD JAIL ASKS FOR THE RECORD in its workspace, where buildFork reads it: a fork's
// argv and a patched extension's alike.
func TestAContainerBuildJailRecordsItsLinesStatusInItsWorkspace(t *testing.T) {
	want := "--line-status=" + path.Join(containerWorkspace, capture.InstallerStatusLeaf)
	fx := newTreeFixture(t, `"f.txt"`)
	if d, stream := fx.deliverWithStream(t, t.TempDir()); d.Dir == "" {
		t.Fatalf("the build failed: %+v\n%s", d, stream.terminal())
	}
	if len(fx.seen) == 0 || !slices.Contains(fx.seen[0].Args, want) {
		t.Errorf("the tree's build jail argv lacks %q: %d jails seen", want, len(fx.seen))
	}
	if argv := forkBuildJailArgv("make"); !slices.Contains(argv, want) {
		t.Errorf("a fork's build jail argv lacks %q: %q", want, argv)
	}
}

// THE MACOS-USER ACT IS TOLD WHERE TO MOVE THE RECORD: beside the toolchain record in the host's
// staging workspace, where buildFork reads it, so `yolo capture` on a Mac says the line's status.
// Red with capturehost's LineStatusDest deleted.
func TestAMacosUserBuildSaysItsLinesOwnStatus(t *testing.T) {
	forkBuildHome(t)
	t.Setenv("YOLO_SETUP", "seatbelt-yolo-account")
	origAct := macForkBuildAct
	macForkBuildAct = func(_ macosuser.Deps, o macosuser.ForkBuildOptions, _, toolchain string, _ bool) int {
		writeFile(t, toolchain, "yolo test")
		if o.LineStatusDest == "" || filepath.Dir(o.LineStatusDest) != filepath.Dir(toolchain) {
			t.Errorf("the act is told to move the line's status to %q, not beside %q", o.LineStatusDest, toolchain)
			return 1
		}
		writeFile(t, o.LineStatusDest, "4\n")
		return 1
	}
	t.Cleanup(func() { macForkBuildAct = origAct })
	withFakeCaptureJail(t, func(o run.Options) int {
		return o.MacosUserRun(jsonx.NewOrderedMap(), o.Workspace, nil, o.Args, "/flake", "", macosuser.HomeOverlay{},
			macosuser.HostContext{}, false, jsonx.NewOrderedMap(), nil, macosuser.JailDaemons{})
	})
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc == 0 {
		t.Fatalf("a failed build succeeded\n%s\n%s", out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "the build line ran and failed with exit status 4") {
		t.Errorf("the failure does not say the build line's own status:\n%s", errw.String())
	}
	// ITS DISCLOSURE NAMES THE MAC'S OWN NETWORK: a macos-user build has no network of its own, and
	// is launched from no jail. Red with buildFork's disclosure asked of "" rather than the build's
	// runtime, or sealDisclosure's macos-user case deleted.
	said := out.String() + errw.String()
	if !strings.Contains(said, "sealed: no credential, no host file, no env_sources, and this Mac's own network") ||
		strings.Contains(said, "launched from") || strings.Contains(said, "bridged") {
		t.Errorf("a macos-user build's disclosure does not name the Mac's own network:\n%s", said)
	}
}

// THE LAUNCH'S BUILD REPORT says the same of a macos-user launch's builds, whatever the network
// predicate answers for a container runtime. Red with sealDisclosure's macos-user case deleted.
func TestAMacosUserBuildsDisclosureNamesTheMacsOwnNetwork(t *testing.T) {
	prev := sealSharesNetwork
	sealSharesNetwork = func(string) bool { return true }
	t.Cleanup(func() { sealSharesNetwork = prev })
	got := sealDisclosure("macos-user")
	if !strings.Contains(got, "this Mac's own network") || strings.Contains(got, "launched from") {
		t.Errorf("macos-user's disclosure = %q", got)
	}
	if got := sealDisclosure("podman"); !strings.Contains(got, "the network of the jail it is launched from") {
		t.Errorf("a nested podman build's disclosure changed: %q", got)
	}
}
