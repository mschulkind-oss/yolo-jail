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

// fakeGuestBuild stands a `.#guestPrefix` build's output holding names, and returns its store dir.
func fakeGuestBuild(t *testing.T, names []string) string {
	t.Helper()
	store := t.TempDir()
	if err := os.MkdirAll(filepath.Join(store, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(store, "bin", name), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// asCheckout makes root look like a source checkout: a bundle ships no go.mod (THE BUNDLE IS
// PREBUILT, NOT SOURCE, stage-source-bundle.sh), a checkout always does.
func asCheckout(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/mschulkind-oss/yolo-jail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A SOURCE THAT SHIPS NO PREBUILT DIR BUILDS .#guestPrefix, says so, and uses its bin; a failed
// build is an error naming nix's tail and the next step.
func TestResolveGuestBinariesBuildsWhenTheSourceShipsNone(t *testing.T) {
	root := t.TempDir()
	asCheckout(t, root)
	store := fakeGuestBuild(t, macosuser.GuestBinaries)
	var said bytes.Buffer
	got, err := resolveGuestBinaries(root, func(r string, _ io.Writer) (string, []string) {
		if r != root {
			t.Errorf("built in %q, want %q", r, root)
		}
		return store, nil
	}, &said)
	if err != nil || got != filepath.Join(store, "bin") {
		t.Errorf("resolveGuestBinaries = %q, %v", got, err)
	}
	if !strings.Contains(said.String(), ".#guestPrefix") {
		t.Errorf("the build is not announced: %q", said.String())
	}
	_, err = resolveGuestBinaries(root, func(string, io.Writer) (string, []string) {
		return "", []string{"error: builder failed"}
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "builder failed") || !strings.Contains(err.Error(), "Fix the build") {
		t.Errorf("a failed build did not surface nix's tail and the next step: %v", err)
	}
}

// A BUNDLE WHOSE PREBUILT DIR LACKS A MEMBER IS REFUSED BEFORE ANY BUILD, naming what is missing
// and how to restage. A bundle carries no Go sources, and the flake's prebuilt branch asks only
// whether the directory exists, so a build there either fails at the copy of the missing name
// (this flake) or succeeds without it (the older flake such a bundle carries) — never the set.
// An EMPTY dir is the same case. Each member's absence is asked separately, so the check cannot
// regress to any one name.
func TestResolveGuestBinariesRefusesABundleShortOfAMember(t *testing.T) {
	cases := map[string][]string{"empty": nil}
	for _, missing := range macosuser.GuestBinaries {
		var have []string
		for _, n := range macosuser.GuestBinaries {
			if n != missing {
				have = append(have, n)
			}
		}
		cases["without "+missing] = have
	}
	for name, have := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeGuestDir(t, root, have)
			_, err := resolveGuestBinaries(root, func(string, io.Writer) (string, []string) {
				t.Error("built .#guestPrefix from a bundle, which has no Go sources to build from")
				return "", nil
			}, io.Discard)
			if err == nil {
				t.Fatal("a bundle short of a guest binary resolved")
			}
			for _, n := range macosuser.GuestBinaries {
				if !slices.Contains(have, n) && !strings.Contains(err.Error(), n) {
					t.Errorf("the refusal does not name the missing %s: %v", n, err)
				}
			}
			if !strings.Contains(err.Error(), "just install") || !strings.Contains(err.Error(), "reinstall yolo-jail") {
				t.Errorf("the refusal names no way to restage the bundle: %v", err)
			}
		})
	}
	if !slices.Contains(macosuser.GuestBinaries, "yolo-serial") || !slices.Contains(macosuser.GuestBinaries, "yolo-ps") {
		t.Errorf("the guest set %v lacks a loophole client this test is for", macosuser.GuestBinaries)
	}
}

// A CHECKOUT WITH A PARTIAL PREBUILT DIR BUILDS: the dir is untracked there (/bin/ is
// gitignored), so a git flake never sees it and compiles the set from goSrc.
func TestResolveGuestBinariesBuildsACheckoutWithAStrayPartialDir(t *testing.T) {
	root := t.TempDir()
	asCheckout(t, root)
	writeGuestDir(t, root, []string{macosuser.JaildName})
	store := fakeGuestBuild(t, macosuser.GuestBinaries)
	got, err := resolveGuestBinaries(root, func(string, io.Writer) (string, []string) { return store, nil }, io.Discard)
	if err != nil || got != filepath.Join(store, "bin") {
		t.Errorf("resolveGuestBinaries = %q, %v; want the build's bin", got, err)
	}
}

// A BUILD WHOSE OUTPUT LACKS A MEMBER IS REFUSED, naming it: a flake source older than this yolo's
// guest set builds a prefix without the clients, and the launch would otherwise fail at the stage
// copy of the missing name, after the privileged steps began, with no next step.
func TestResolveGuestBinariesRefusesABuildShortOfAMember(t *testing.T) {
	root := t.TempDir()
	asCheckout(t, root)
	store := fakeGuestBuild(t, []string{macosuser.JaildName})
	_, err := resolveGuestBinaries(root, func(string, io.Writer) (string, []string) { return store, nil }, io.Discard)
	if err == nil {
		t.Fatal("a build without the guest clients resolved")
	}
	for _, want := range []string{"yolo-serial", "yolo-ps", "YOLO_REPO_ROOT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v", want, err)
		}
	}
}
