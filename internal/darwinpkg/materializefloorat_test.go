package darwinpkg

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// MaterializeFloorAt builds the FLOOR profile and roots it at the caller's link, never the home's
// ProfileRootLink: a macos-user fork build that retargeted the home's link would unroot the closure
// a running session executes from (docs/design/forked-programs-as-packs.md FP-D24). Asserted on the
// argv a stand-in nix receives, since the function runs nix.
func TestMaterializeFloorAtBuildsTheFloorRootedAtTheCallersLink(t *testing.T) {
	nixchildren.Isolate(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	standInNix(t, "echo '[]'", "echo \"$*\" > "+shquote.Quote(args)+"; echo /nix/store/fake-floor")
	link := filepath.Join(dir, "build", "toolchain-root")
	pkgs, err := MaterializeFloorAt(t.TempDir(), nil, "aarch64-darwin", link, io.Discard)
	if err != nil {
		t.Fatalf("MaterializeFloorAt: %v", err)
	}
	if pkgs.ProfilePath != "/nix/store/fake-floor" {
		t.Errorf("profile = %q, want the build's out path", pkgs.ProfilePath)
	}
	body, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "--out-link "+link) || strings.Contains(got, "--no-link") {
		t.Errorf("the build is not rooted at the caller's link %s:\n%s", link, got)
	}
	if strings.Contains(got, ProfileRootLink(home)) {
		t.Errorf("the build retargets the home's profile root:\n%s", got)
	}
	if !strings.Contains(got, ".#packages.aarch64-darwin."+FloorProfileAttr) {
		t.Errorf("the build is not the floor profile (%s):\n%s", FloorProfileAttr, got)
	}
}

// An empty link is refused before nix runs: an unrooted floor under a running build is N1.
func TestMaterializeFloorAtRefusesAnUnrootedBuild(t *testing.T) {
	nixchildren.Isolate(t)
	ran := filepath.Join(t.TempDir(), "ran")
	standInNix(t, "touch "+shquote.Quote(ran)+"; echo '[]'", "touch "+shquote.Quote(ran))
	if _, err := MaterializeFloorAt(t.TempDir(), nil, "aarch64-darwin", "", io.Discard); err == nil {
		t.Error("an unrooted floor build was accepted")
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("nix ran for an unrooted floor build")
	}
}
