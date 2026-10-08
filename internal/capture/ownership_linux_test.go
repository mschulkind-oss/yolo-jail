//go:build linux

package capture

// ownership_linux_test.go pins who owns a captured tree when the capture driver runs as root and
// the store belongs to somebody else — the arrangement of every container capture on a ROOTLESS
// podman, which a nested jail (rootful, --userns=host) cannot show and CI's runners can.
//
// On a rootless podman the capture jail's root is the host user, but a file a vendor's tarball
// extracts as root keeps the ARCHIVE'S owner (GNU tar's --same-owner is root's default): copilot's
// copilot-linux-x64.tar.gz says 1001/1001, so its binary lands on the host as a subordinate uid
// the host user cannot chmod or unlink. The host's admit then failed — `chmod …/copilot: operation
// not permitted` freezing copilot's tree, `unlinkat …/codex-path/rg: permission denied` clearing
// the unfinished entry an earlier capture of codex left (Pack Installs, 2026-10-06).
//
// Reproduced here without podman: root plays the jail's root, uid 4242 the tarball's owner, and
// uid 65534 the host user who owns the store and runs the admit, in a re-executed copy of this
// test binary.

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/treedigest"
)

const (
	// storeOwnerUID is the host user the store belongs to, in the re-executed admit.
	storeOwnerUID = 65534
	// tarballOwnerUID is the owner a vendor's archive records, which root's tar keeps.
	tarballOwnerUID = 4242
	// ownerHelperEnv carries the helper's act and arguments into the re-executed test binary.
	ownerHelperEnv = "YOLO_CAPTURE_OWNER_HELPER"
)

// foreignOwnedInstaller is a vendor installer of the tarball class: a binary in ~/.local/bin and a
// versioned directory holding a helper, as codex's codex-path/rg, all left owned by the archive's
// uid rather than by the root that extracted them.
const foreignOwnedInstaller = `#!/bin/sh
set -eu
mkdir -p "$HOME/.local/bin" "$HOME/.local/share/vendor/1.0/codex-path"
printf 'vendor binary\n' > "$HOME/.local/bin/vendor"
chmod 755 "$HOME/.local/bin/vendor"
printf 'rg\n' > "$HOME/.local/share/vendor/1.0/codex-path/rg"
chmod 755 "$HOME/.local/share/vendor/1.0/codex-path/rg"
chown 4242:4242 "$HOME/.local/bin/vendor"
chown -R 4242:4242 "$HOME/.local/share/vendor"
`

// TestOwnerHelper is the re-executed half: it does nothing unless ownerHelperEnv names an act, and
// then runs that act as whatever uid it was started under, printing "ok" or the error.
func TestOwnerHelper(t *testing.T) {
	spec := os.Getenv(ownerHelperEnv)
	if spec == "" {
		t.Skip("the re-executed half of the ownership tests")
	}
	parts := strings.Split(spec, "\x1f")
	s := &Store{Dir: parts[1]}
	switch parts[0] {
	case "admit-and-reap":
		e, err := s.AdmitEntry(parts[2])
		if err != nil {
			os.Stdout.WriteString("admit: " + err.Error() + "\n")
			return
		}
		if err := filepath.WalkDir(e.Tree, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			if fi, ierr := d.Info(); ierr != nil || fi.Mode().Perm()&0o222 != 0 {
				os.Stdout.WriteString("not frozen: " + p + "\n")
			}
			return nil
		}); err != nil {
			os.Stdout.WriteString("walk: " + err.Error() + "\n")
			return
		}
		if err := s.ReapEntry(e.Key); err != nil {
			os.Stdout.WriteString("reap: " + err.Error() + "\n")
			return
		}
		os.Stdout.WriteString("ok\n")
	case "admit":
		if _, err := s.AdmitEntry(parts[2]); err != nil {
			os.Stdout.WriteString("admit: " + err.Error() + "\n")
			return
		}
		os.Stdout.WriteString("ok\n")
	}
}

// requireRootForOwnership skips unless this process can hand a file to another uid.
func requireRootForOwnership(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root: only root can leave a file owned by another uid, which is the case under test")
	}
}

// ownershipBase is a scratch directory uid 65534 can reach, or a skip naming why it cannot.
func ownershipBase(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp("", "yolo-capture-owner-")
	must(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	must(t, os.Chmod(base, 0o755))
	resolved, err := filepath.EvalSymlinks(base)
	must(t, err)
	for d := filepath.Dir(resolved); ; d = filepath.Dir(d) {
		fi, err := os.Stat(d)
		must(t, err)
		if fi.Mode().Perm()&0o001 == 0 {
			t.Skipf("uid %d cannot reach %s (%s is %v): set TMPDIR to a world-searchable directory",
				storeOwnerUID, base, d, fi.Mode().Perm())
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	return resolved
}

// runAsStoreOwner re-executes this test binary as uid 65534 to run one helper act. The binary is
// copied beside the fixture first, since go's own build directory is root's alone.
func runAsStoreOwner(t *testing.T, base string, act ...string) string {
	t.Helper()
	self, err := os.Executable()
	must(t, err)
	bin := filepath.Join(base, "capture.test")
	src, err := os.Open(self)
	must(t, err)
	dst, err := os.OpenFile(bin, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	must(t, err)
	_, err = io.Copy(dst, src)
	src.Close()
	must(t, err)
	must(t, dst.Close())
	cmd := exec.Command(bin, "-test.run=^TestOwnerHelper$", "-test.count=1")
	cmd.Dir = base
	cmd.Env = append(os.Environ(), ownerHelperEnv+"="+strings.Join(act, "\x1f"), "HOME="+base)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: storeOwnerUID, Gid: storeOwnerUID}}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("the admit as uid %d did not run: %v\n%s", storeOwnerUID, err, out.String())
	}
	return out.String()
}

// chownAll hands every path under root to uid:gid, links included and never followed.
func chownAll(t *testing.T, root string, uid int) {
	t.Helper()
	must(t, filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, uid)
	}))
}

// jailRootIsTheStoreOwner gives the store owner what this test's root made, which on a rootless
// podman is the host user's already: the jail's root IS that user. Only a file root handed to
// another uid keeps that uid, as the archive's files do in a rootless capture jail.
func jailRootIsTheStoreOwner(t *testing.T, root string) {
	t.Helper()
	must(t, filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ownerOfPath(t, p) == 0 {
			return os.Lchown(p, storeOwnerUID, storeOwnerUID)
		}
		return nil
	}))
}

func ownerOfPath(t *testing.T, p string) uint32 {
	t.Helper()
	fi, err := os.Lstat(p)
	must(t, err)
	return fi.Sys().(*syscall.Stat_t).Uid
}

// A capture run as root of an installer whose files keep their archive's owner leaves the store's
// owner able to admit it — freeze every file read-only — and to reap it again. Before, the driver
// moved those files into the tree with the archive's uid, and the store owner's admit failed to
// chmod the first one: copilot's failure on CI's rootless runners.
func TestARootCaptureOfAnArchivesFilesIsTheStoreOwnersToAdmitAndReap(t *testing.T) {
	requireRootForOwnership(t)
	if _, err := exec.LookPath("chown"); err != nil {
		t.Skip("the fixture installer needs chown")
	}
	base := ownershipBase(t)
	store := &Store{Dir: filepath.Join(base, "store")}
	staged, err := store.Stage("vendor")
	must(t, err)
	// The host user's store, and the scratch dir its capture act made, as a rootless jail's root
	// sees them: the jail's root is that user, so a capture jail sees them as its own.
	chownAll(t, store.Dir, storeOwnerUID)
	home := filepath.Join(base, "home")
	fixtureHome(t, home)
	out := filepath.Join(staged, "out")

	res, err := Run(Options{Home: home, Out: out, Command: writeInstaller(t, foreignOwnedInstaller)})
	must(t, err)
	jailRootIsTheStoreOwner(t, out)

	for _, p := range []string{out, ManifestPath(out), filepath.Join(res.Tree, ".local", "bin", "vendor"),
		filepath.Join(res.Tree, ".local", "share", "vendor", "1.0", "codex-path"),
		filepath.Join(res.Tree, ".local", "share", "vendor", "1.0", "codex-path", "rg")} {
		if uid := ownerOfPath(t, p); uid != storeOwnerUID {
			t.Errorf("%s is owned by uid %d after the capture, want the store owner's %d", p, uid, storeOwnerUID)
		}
	}
	if got := runAsStoreOwner(t, base, "admit-and-reap", store.Dir, out); !strings.HasPrefix(got, "ok\n") {
		t.Fatalf("the store owner could not admit and reap the capture:\n%s", got)
	}
}

// A capture that FAILS still hands back what it left. The delta move is what normally carries the
// installer's files into the out tree, and the success path gives THAT to the store's owner; a
// failure before or during the move leaves them in a surface, where a root capture's archive uid
// would be one the store's owner cannot clear — and the next capture would stop at Store.Stage.
func TestAFailedRootCaptureStillHandsItsLeftoversToTheStoreOwner(t *testing.T) {
	requireRootForOwnership(t)
	if _, err := exec.LookPath("chown"); err != nil {
		t.Skip("the fixture installer needs chown")
	}
	base := ownershipBase(t)
	store := &Store{Dir: filepath.Join(base, "store")}
	staged, err := store.Stage("vendor")
	must(t, err)
	// The host user's store, as a rootless jail's root sees it: the jail's root is that user.
	chownAll(t, store.Dir, storeOwnerUID)
	home := filepath.Join(base, "home")
	fixtureHome(t, home)
	out := filepath.Join(staged, "out")

	if _, err := Run(Options{Home: home, Out: out,
		Command: writeInstaller(t, foreignOwnedInstaller+"\nexit 1\n")}); err == nil {
		t.Fatal("the fixture installer exits 1, so Run must fail")
	}

	stuck := filepath.Join(home, ".local", "share", "vendor", "1.0", "codex-path", "rg")
	if uid := ownerOfPath(t, stuck); uid != storeOwnerUID {
		t.Errorf("%s is owned by uid %d after a failed capture, want the store owner's %d",
			stuck, uid, storeOwnerUID)
	}
}

// A capture driver that is not root changes nobody's ownership: on macos-user it runs as the
// sandbox account, whose files reach the host user through the staging tree's ACLs, and a chown
// there would fail the capture for a file that is fine as it is.
func TestANonRootCaptureLeavesOwnershipAlone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the non-root half; the uid-65534 run of this package takes it")
	}
	home := t.TempDir()
	store := &Store{Dir: t.TempDir()}
	staged, err := store.Stage("plain")
	must(t, err)
	fixtureHome(t, home)
	res, err := Run(Options{Home: home, Out: filepath.Join(staged, "out"), Command: writeInstaller(t, installerScript)})
	must(t, err)
	if uid := ownerOfPath(t, filepath.Join(res.Tree, ".local", "share", "vendor", "1.2.3", "vendor")); uid != uint32(os.Geteuid()) {
		t.Errorf("a non-root capture's file is owned by uid %d, want this process's %d", uid, os.Geteuid())
	}
}

// An unfinished entry the store's owner cannot clear — an older yolo's capture on a rootless podman
// left its files a container user's — stops the admit with a message naming the entry and the one
// command that removes it, rather than a bare unlinkat error under the store: codex's failure.
func TestAnUnfinishedEntryTheOwnerCannotClearNamesTheCommandThatRemovesIt(t *testing.T) {
	requireRootForOwnership(t)
	base := ownershipBase(t)
	store := &Store{Dir: filepath.Join(base, "store")}
	staged, err := store.Stage("vendor")
	must(t, err)
	tree := TreeDir(staged)
	must(t, os.MkdirAll(filepath.Join(tree, ".local", "bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(tree, ".local", "bin", "vendor"), []byte("vendor binary\n"), 0o755))
	d, err := treedigest.Of(tree)
	must(t, err)
	key := Key(d)
	// The torn entry an older capture left: same bytes, so the same key, no marker, and a
	// directory inside it the store's owner cannot unlink from.
	stuck := filepath.Join(store.EntryDir(key), "tree", ".local", "share", "vendor", "codex-path")
	must(t, os.MkdirAll(stuck, 0o755))
	must(t, os.WriteFile(filepath.Join(stuck, "rg"), []byte("rg\n"), 0o755))
	chownAll(t, store.Dir, storeOwnerUID)
	chownAll(t, filepath.Join(store.EntryDir(key), "tree", ".local", "share", "vendor"), tarballOwnerUID)

	got := runAsStoreOwner(t, base, "admit", store.Dir, staged)
	for _, want := range []string{store.EntryDir(key), "podman unshare rm -rf " + store.EntryDir(key)} {
		if !strings.Contains(got, want) {
			t.Errorf("the admit's refusal does not name %q:\n%s", want, got)
		}
	}
}
