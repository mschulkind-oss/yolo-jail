package cli

// forkbuildmacos_test.go pins what the build act reads differently of a macos-user build
// (docs/design/forked-programs-as-packs.md FP-D24): the workspace a link must not point into is the
// build's staging tree, whose home is inside it, and the platform its build is filed under is darwin.

import (
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// A LINK INTO THE CHECKOUT IS REFUSED ON macos-user TOO, and a link inside the build's home is not:
// there the home sits inside the staging tree the build's workspace is, where a container build's
// /home/agent is beside /workspace.
func TestLinksIntoTheBuildReadsAMacosUserBuildsWorkspace(t *testing.T) {
	ws := forkBuildWorkspace("macos-user", "0123456789abcdef")
	if ws != macosuser.ForkBuildStagingRoot("", "0123456789abcdef") {
		t.Fatalf("a macos-user build's workspace is %s, not its staging tree", ws)
	}
	home := macosuser.CaptureStagingHome(ws)
	src := macosuser.ForkBuildSourceDir(ws)
	m := func(target string) *capture.Manifest {
		return &capture.Manifest{Home: home, Entries: []capture.ManifestEntry{
			{Path: ".npm-global/lib/node_modules/tool", Kind: capture.KindSymlink, Target: target}}}
	}
	if why := linksIntoTheBuild(m(src+"/pkg"), ws); !strings.Contains(why, "a link into its own workspace") {
		t.Errorf("a link into the macos-user build's checkout was admitted: %q", why)
	}
	if why := linksIntoTheBuild(m("../../../../src/pkg"), ws); !strings.Contains(why, "a link into its own workspace") {
		t.Errorf("a relative link into the macos-user build's checkout was admitted: %q", why)
	}
	if why := linksIntoTheBuild(m("../../lib/node_modules/other"), ws); why != "" {
		t.Errorf("a link inside the build's home was refused: %q", why)
	}
	// The container build's rule is unchanged: /workspace is the workspace, and the home beside it.
	if forkBuildWorkspace("podman", "x") != containerWorkspace {
		t.Errorf("a container build's workspace is %s", forkBuildWorkspace("podman", "x"))
	}
	container := &capture.Manifest{Home: "/home/agent", Entries: []capture.ManifestEntry{
		{Path: ".npm-global/lib/node_modules/tool", Kind: capture.KindSymlink, Target: "/workspace/src"}}}
	if why := linksIntoTheBuild(container, containerWorkspace); why == "" {
		t.Error("a container build's link into /workspace was admitted")
	}
}

// A macos-user build is of darwin on this architecture, a container build of linux.
func TestAForkBuildsPlatformIsItsJails(t *testing.T) {
	if got := forkBuildPlatform("macos-user"); got != "darwin/"+goruntime.GOARCH {
		t.Errorf("a macos-user build is filed under %s", got)
	}
	for _, rt := range []string{"podman", "container", ""} {
		if got := forkBuildPlatform(rt); got != captureJailPlatform() {
			t.Errorf("a %q build is filed under %s, want the container jail's %s", rt, got, captureJailPlatform())
		}
	}
}
