package cli

// macosguestbinaries_test.go pins where the macos-user guest's darwin in-jail binaries come from
// (docs/design/declaration-parity.md OQ-DP8): the resolved flake bundle's own bin/darwin-<arch>
// when it ships one, else a `.#guestPrefix` build of that source — and that a launch's Deps are
// wired to that resolver and to the real background starter, so deleting either assignment
// fails a test rather than leaving every macos-user launch unable to start its daemons.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

func TestMacosLaunchDepsWireTheGuestBinariesAndTheSupervisorStarter(t *testing.T) {
	deps := macosLaunchDeps(nil, nil)
	if deps.GuestBinaries == nil {
		t.Error("a macos-user LAUNCH has no guest-binary resolver, so every launch with a jail " +
			"daemon to run refuses")
	}
	if deps.StartBackground == nil {
		t.Error("a macos-user LAUNCH has no background starter for the jail-daemon supervisor")
	}
}

// A BUNDLE THAT SHIPS THE GUEST DIR IS USED AS IT IS: no build.
func TestResolveGuestBinariesPrefersTheBundlesPrebuiltDir(t *testing.T) {
	root := t.TempDir()
	dir := macosuser.PrebuiltGuestBinDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, macosuser.JaildName), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveGuestBinaries(root, func(string, io.Writer) (string, []string) {
		t.Error("built .#guestPrefix although the bundle ships the guest dir")
		return "", nil
	}, io.Discard)
	if err != nil || got != dir {
		t.Errorf("resolveGuestBinaries = %q, %v; want the prebuilt %q", got, err, dir)
	}
}

// A CHECKOUT (NO PREBUILT DIR, OR AN EMPTY ONE) BUILDS .#guestPrefix, says so, and uses its bin;
// a failed build is an error naming nix's tail.
func TestResolveGuestBinariesBuildsWhenTheSourceShipsNone(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(macosuser.PrebuiltGuestBinDir(root), 0o755); err != nil { // half-staged
		t.Fatal(err)
	}
	var said bytes.Buffer
	got, err := resolveGuestBinaries(root, func(r string, _ io.Writer) (string, []string) {
		if r != root {
			t.Errorf("built in %q, want %q", r, root)
		}
		return "/nix/store/abc-yolo-jail-guest", nil
	}, &said)
	if err != nil || got != "/nix/store/abc-yolo-jail-guest/bin" {
		t.Errorf("resolveGuestBinaries = %q, %v", got, err)
	}
	if !strings.Contains(said.String(), ".#guestPrefix") {
		t.Errorf("the build is not announced: %q", said.String())
	}
	_, err = resolveGuestBinaries(root, func(string, io.Writer) (string, []string) {
		return "", []string{"error: builder failed"}
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "builder failed") {
		t.Errorf("a failed build did not surface nix's tail: %v", err)
	}
}
