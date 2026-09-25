package run

// packfilesretire_test.go pins that preparePackFiles' RETIRE loop never removes a host file
// through a link the jail planted (docs/reference/jail-home.md, "Host code in jail-writable
// state"). The loop runs on the HOST, as the host user, and removes the mountpoints the
// ownership manifest records beneath the workspace overlay (wsState, `<workspace>/.yolo/home`).
// The jail can write both halves: the manifest sits in `.yolo`, so the jail chooses each
// recorded path and its digest, and the overlay is reachable at /workspace/.yolo/home. A link at
// wsState, or at a directory on the way swapped in after the check, had os.Remove delete the
// host file of that name, and a forged digest let that be any host file whose content the jail
// knows.
//
// Each case plants the link, runs preparePackFiles, and checks the host side.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// forgeRetireManifest writes, as the jail can, an ownership manifest recording rel as a file
// mountpoint with the digest of body, so the retire loop treats a file holding body at rel as
// its own unchanged scaffold.
func forgeRetireManifest(t *testing.T, wsState, rel, body string) {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	writeFixture(t, filepath.Join(filepath.Dir(wsState), packFilesMountpointManifestName),
		`{"version":1,"entries":{"`+filepath.ToSlash(rel)+`":{"kind":"file","sha256":"`+
			hex.EncodeToString(sum[:])+`"}}}`)
}

// A LINK PLANTED BEFORE THE LAUNCH, at wsState itself, at a directory on the way to the recorded
// path, or at the recorded path: to an existing host directory (or file, at the path) holding a
// file the manifest matches, and dangling. Through a link at wsState both sides of the old
// containment check resolved into the host directory, so the check passed and the host file was
// removed.
func TestRetireNeverRemovesThroughAPlantedLink(t *testing.T) {
	rel := filepath.Join("pi", "agent", "zshrc")
	for _, at := range []struct {
		name string
		link string // relative to wsState's parent, `.yolo`
		// hostRel is where, below the host directory the link names, the recorded file sits.
		hostRel string
	}{
		{"the overlay", "home", rel},
		{"a directory on the way", filepath.Join("home", "pi", "agent"), "zshrc"},
	} {
		t.Run(at.name+"/a link to an existing host directory", func(t *testing.T) {
			wsState := filepath.Join(t.TempDir(), ".yolo", "home")
			host := t.TempDir()
			hostFile := filepath.Join(host, at.hostRel)
			writeFixture(t, hostFile, hostFileBody)
			must(t, os.MkdirAll(filepath.Dir(wsState), 0o755))
			symlinkAt(t, host, filepath.Join(filepath.Dir(wsState), at.link))
			forgeRetireManifest(t, wsState, rel, hostFileBody)

			preparePackFiles(nil, wsState, "podman")

			if got, err := os.ReadFile(hostFile); err != nil || string(got) != hostFileBody {
				t.Errorf("the retire loop removed the host file through the jail's link at %s: %q, %v",
					at.link, got, err)
			}
		})
		t.Run(at.name+"/a dangling link", func(t *testing.T) {
			wsState := filepath.Join(t.TempDir(), ".yolo", "home")
			dir := t.TempDir()
			must(t, os.MkdirAll(filepath.Dir(wsState), 0o755))
			symlinkAt(t, filepath.Join(dir, "created-by-the-follow"), filepath.Join(filepath.Dir(wsState), at.link))
			forgeRetireManifest(t, wsState, rel, hostFileBody)

			preparePackFiles(nil, wsState, "podman")

			assertEmptyDir(t, dir, "a dangling link at "+at.link)
		})
	}
	t.Run("the recorded path/a link to an existing host file", func(t *testing.T) {
		wsState := filepath.Join(t.TempDir(), ".yolo", "home")
		hostFile := outsideFile(t)
		symlinkAt(t, hostFile, filepath.Join(wsState, rel))
		forgeRetireManifest(t, wsState, rel, hostFileBody)

		preparePackFiles(nil, wsState, "podman")

		if got, err := os.ReadFile(hostFile); err != nil || string(got) != hostFileBody {
			t.Errorf("the retire loop removed the host file behind the jail's link: %q, %v", got, err)
		}
	})
	t.Run("the recorded path/a dangling link", func(t *testing.T) {
		wsState := filepath.Join(t.TempDir(), ".yolo", "home")
		dir := t.TempDir()
		symlinkAt(t, filepath.Join(dir, "created-by-the-follow"), filepath.Join(wsState, rel))
		forgeRetireManifest(t, wsState, rel, hostFileBody)

		preparePackFiles(nil, wsState, "podman")

		assertEmptyDir(t, dir, "a dangling link at the recorded path")
	})
}

// A DIRECTORY SWAPPED FOR A LINK between the check that the recorded mountpoint is unchanged
// and its removal. The plain os.Remove resolved the swapped-in link and deleted the host file of
// the same name, whatever its content, since the check had already passed on the jail's copy.
func TestRetireNeverRemovesThroughALinkSwappedInAfterTheCheck(t *testing.T) {
	rel := filepath.Join("pi", "agent", "stale")
	for _, swapAt := range []string{filepath.Dir(rel), filepath.Dir(filepath.Dir(rel))} {
		t.Run(swapAt, func(t *testing.T) {
			wsState := filepath.Join(t.TempDir(), ".yolo", "home")
			writeFixture(t, filepath.Join(wsState, rel), "")
			writeFixture(t, filepath.Join(filepath.Dir(wsState), packFilesMountpointManifestName),
				`{"version":1,"entries":{"`+filepath.ToSlash(rel)+`":{"kind":"file"}}}`)
			host := t.TempDir()
			below, _ := filepath.Rel(swapAt, rel)
			hostFile := filepath.Join(host, below)
			writeFixture(t, hostFile, "host content the check never saw\n")

			swapped := false
			packFilesBeforeRetire = func(string) {
				if !swapped {
					swapped = true
					dir := filepath.Join(wsState, swapAt)
					must(t, os.Rename(dir, dir+".moved-aside"))
					must(t, os.Symlink(host, dir))
				}
			}
			t.Cleanup(func() { packFilesBeforeRetire = nil })

			preparePackFiles(nil, wsState, "podman")

			if !swapped {
				t.Fatal("the retire loop never reached the removal: the fixture is not exercising the race")
			}
			if _, err := os.Lstat(hostFile); err != nil {
				t.Errorf("the retire loop removed the host file through the link swapped in at %s: %v", swapAt, err)
			}
		})
	}
	t.Run("an unchanged mountpoint is still retired", func(t *testing.T) {
		wsState := filepath.Join(t.TempDir(), ".yolo", "home")
		writeFixture(t, filepath.Join(wsState, rel), "")
		must(t, os.MkdirAll(filepath.Join(wsState, "pi", "empty-dir"), 0o755))
		writeFixture(t, filepath.Join(filepath.Dir(wsState), packFilesMountpointManifestName),
			`{"version":1,"entries":{"`+filepath.ToSlash(rel)+`":{"kind":"file"},"pi/empty-dir":{"kind":"dir"}}}`)

		preparePackFiles(nil, wsState, "podman")

		for _, p := range []string{rel, filepath.Join("pi", "empty-dir")} {
			if _, err := os.Lstat(filepath.Join(wsState, p)); !os.IsNotExist(err) {
				t.Errorf("the recorded, unchanged mountpoint %s was not retired: %v", p, err)
			}
		}
	})
}
