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
	"slices"
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

// writeGuestDir stages names into root's prebuilt guest dir, as a bundle would.
func writeGuestDir(t *testing.T, root string, names []string) string {
	t.Helper()
	dir := macosuser.PrebuiltGuestBinDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A BUNDLE THAT SHIPS THE WHOLE GUEST SET IS USED AS IT IS: no build.
func TestResolveGuestBinariesPrefersTheBundlesPrebuiltDir(t *testing.T) {
	root := t.TempDir()
	dir := writeGuestDir(t, root, macosuser.GuestBinaries)
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

// A PREBUILT DIR SHORT OF ONE MEMBER BUILDS. The launch stages the WHOLE guest set
// (StageGuestBinaryCommands), so a bundle staged before the set grew to hold the loophole
// clients — yolo-jaild alone — would otherwise pass the old "yolo-jaild is there" check and fail
// at the stage copy of yolo-serial, after the privileged steps began. Each member's absence is
// asked separately, so the check cannot regress to any one name.
func TestResolveGuestBinariesBuildsWhenThePrebuiltDirLacksAMember(t *testing.T) {
	for _, missing := range macosuser.GuestBinaries {
		t.Run(missing, func(t *testing.T) {
			root := t.TempDir()
			var have []string
			for _, n := range macosuser.GuestBinaries {
				if n != missing {
					have = append(have, n)
				}
			}
			writeGuestDir(t, root, have)
			built := false
			got, err := resolveGuestBinaries(root, func(string, io.Writer) (string, []string) {
				built = true
				return "/nix/store/abc-yolo-jail-guest", nil
			}, io.Discard)
			if !built || err != nil || got != "/nix/store/abc-yolo-jail-guest/bin" {
				t.Errorf("a prebuilt dir without %s resolved to %q, %v (built %v); want the "+
					".#guestPrefix build", missing, got, err, built)
			}
		})
	}
	if !slices.Contains(macosuser.GuestBinaries, "yolo-serial") || !slices.Contains(macosuser.GuestBinaries, "yolo-ps") {
		t.Errorf("the guest set %v lacks a loophole client this test is for", macosuser.GuestBinaries)
	}
}
