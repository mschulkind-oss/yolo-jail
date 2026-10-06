package main

// fallback_test.go pins `just install`'s seed when the pinned toolchain cannot be fetched
// (BP-D21): offline, after `go clean -modcache`, or after a Toolchain bump. Seeding falls back to
// the go on PATH when that go reports exactly Toolchain — packbin.Seed admits only bytes whose
// digest is the pin, so a toolchain that builds other bytes seeds nothing — and only a re-pin,
// which would write digests that go made, is refused.
//
// Call-site checks:
//   - drop the PATH-go fallback from seed → TestSeedFallsBackToThePathGoOfThePinnedVersion.
//   - drop its version check → TestSeedRefusesAPathGoOfAnotherVersion.
//   - drop the refusal to re-pin with it → TestSeedDoesNotRepinWithThePathGo.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packbin"
)

// pathGoReporting is a `go` that answers `version` as version on this machine and runs the real
// go for everything else, standing in for a go on PATH of that version.
func pathGoReporting(t *testing.T, version string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "go")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'go version " + version + " " +
		runtime.GOOS + "/" + runtime.GOARCH + "'; exit 0; fi\nexec '" + hostGo(t) + "' \"$@\"\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

var errOffline = errors.New("dial tcp: lookup proxy.golang.org: no such host")

// offlineWith is deps whose toolchain cannot be fetched, and whose go on PATH is pathGo.
func offlineWith(pathGo string) func(*deps) {
	return func(d *deps) {
		d.toolchain = func() (string, error) { return "", errOffline }
		d.pathGo = func() (string, error) { return pathGo, nil }
	}
}

func TestSeedFallsBackToThePathGoOfThePinnedVersion(t *testing.T) {
	root := machineCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	pinned := pins(t, root)
	cache := t.TempDir()

	r := runToolWith(t, root, offlineWith(pathGoReporting(t, Toolchain)), "seed", "--repin", cache)
	if r.code != 0 {
		t.Fatalf("an offline seed with %s on PATH: exit %d\n%s%s", Toolchain, r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{"could not be fetched", "the go on PATH", "seeded binary toold"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the seed does not say %q:\n%s", want, r.stdout)
		}
	}
	for _, platform := range []string{machineHost, machineJail} {
		if !packbin.Present(packbin.Path(cache, pinned[platform], "toold")) {
			t.Errorf("the pinned %s build is not in the cache: %v", platform, cacheFiles(t, cache))
		}
	}
}

func TestSeedRefusesAPathGoOfAnotherVersion(t *testing.T) {
	root := machineCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	cache := t.TempDir()

	r := runToolWith(t, root, offlineWith(pathGoReporting(t, "go1.25.0")), "seed", cache)
	if r.code != 1 {
		t.Fatalf("an offline seed with another go on PATH: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{errOffline.Error(), "go1.25.0", Toolchain, "nothing was seeded"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, r.stderr)
		}
	}
	if got := cacheFiles(t, cache); len(got) != 0 {
		t.Errorf("a seed with no usable toolchain cached %v", got)
	}
}

// A re-pin writes the digests the build made, and the release is made with the fetched
// toolchain, so a re-pin from the PATH's go is refused: the program, or only the toolchain, may
// be what moved.
func TestSeedDoesNotRepinWithThePathGo(t *testing.T) {
	root := machineCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	writeFile(t, root, "cmd/toold/main.go", machineMain("a fork"))
	before := readFile(t, root, fixtureManifestPath)

	r := runToolWith(t, root, offlineWith(pathGoReporting(t, Toolchain)), "seed", "--repin", t.TempDir())
	if r.code != 1 {
		t.Fatalf("an offline re-pin: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{"not re-pinned", "could not be fetched", "just install"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, r.stderr)
		}
	}
	if after := readFile(t, root, fixtureManifestPath); after != before {
		t.Errorf("an offline re-pin rewrote the manifest:\n%s", after)
	}
}
