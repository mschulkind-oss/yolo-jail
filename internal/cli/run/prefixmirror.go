package run

// prefixmirror.go copies a BUILT prefix out of the nix store for a Mac whose podman VM cannot
// see /nix, so a live-checkout launch (YOLO_REPO_ROOT) runs there instead of being refused.
//
// THE CASE. A checkout ships no prebuilt binaries, so the launch builds `.#installPrefix` and
// binds the result into the jail (jailPrefixSource). On a Mac that result is in /nix/store, which
// podman's machine VM does not share by default, and prefixUnreachableFromVM refused the launch:
// a from-source developer on a Mac could only run what `just install` last staged.
//
// THE COPY IS A FLAKE-BUNDLE GENERATION, and that is the whole design. The built prefix's
// share/yolo-jail is already a self-contained bundle — flake.nix, flake.lock and
// bin/linux-<arch>/ (flake.nix's installPrefix) — so copying that one directory into
// flakebundle.StageDir yields exactly the tree `just install` stages, under $HOME, which the VM
// does share. It is mounted the way an installed bundle is (bin/linux-<arch> and the bundle
// itself), and it inherits that tree's lifecycle without a line of new policy: never activated,
// so `yolo` without YOLO_REPO_ROOT still runs the installed bundle; protected for
// flakebundle.GenerationGrace after it is written, which covers the gap before its container
// starts; and reaped by liveness once no jail mounts it (housekeeping.go's ReapGuarded).
//
// ONE COPY PER STORE PATH. A store path names its content, so a copy of it never goes stale: a
// launch whose build produced the same store path as an earlier one reuses that copy, renewing
// its grace, and pays the copy (one bundle, ~125 MB) only when the binaries really changed. The
// marker naming the source is written LAST, so an interrupted copy is never reused; it is an
// ordinary unmounted generation, and the reaper takes it.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/flakebundle"
)

// prefixMirrorMarker names, inside a mirrored generation, the store bundle it is a copy of.
const prefixMirrorMarker = ".yolo-mirror-of"

// readStoreFrom maps a store path to where the copy reads it. The identity in production; a
// test points it at a temp dir, since no test can write /nix/store.
var readStoreFrom = func(p string) string { return p }

// mirrorBuiltPrefix copies p's bundle (a built prefix's share/yolo-jail) into a generation under
// stableDir's generations dir, or reuses one already copied from the same store path, and
// returns the prefix to mount instead. reused says which happened, for the launch's notice.
func mirrorBuiltPrefix(stableDir string, p jailPrefix, now time.Time, pid int) (mirrored jailPrefix, reused bool, err error) {
	src := p.shareDir
	for _, gen := range flakebundle.Generations(stableDir) {
		if !isMirrorOf(gen, src) {
			continue
		}
		// Renew the grace before trusting the copy: a reap that listed this generation
		// before the touch rechecks its age under the deletion lock and declines.
		if err := os.Chtimes(gen, now, now); err != nil {
			continue
		}
		if isMirrorOf(gen, src) {
			return mirroredPrefix(gen, src), true, nil
		}
	}
	gen, err := flakebundle.StageDir(stableDir, now, pid)
	if err != nil {
		return jailPrefix{}, false, err
	}
	if err := copyBundle(readStoreFrom(src), gen); err != nil {
		_ = os.RemoveAll(gen)
		return jailPrefix{}, false, fmt.Errorf("copying %s to %s: %w", src, gen, err)
	}
	if _, err := os.Stat(filepath.Join(prebuiltBinDir(gen), "yolo-entrypoint")); err != nil {
		_ = os.RemoveAll(gen)
		return jailPrefix{}, false, fmt.Errorf("%s holds no %s/yolo-entrypoint", src,
			filepath.Base(prebuiltBinDir(gen)))
	}
	if err := os.WriteFile(filepath.Join(gen, prefixMirrorMarker), []byte(src+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(gen)
		return jailPrefix{}, false, err
	}
	return mirroredPrefix(gen, src), false, nil
}

// mirroredPrefix is the prefix a mirrored generation mounts: the installed-bundle shape.
func mirroredPrefix(gen, src string) jailPrefix {
	return jailPrefix{binDir: prebuiltBinDir(gen), shareDir: gen, built: true, mirroredFrom: src}
}

// isMirrorOf reports whether gen is a COMPLETE copy of src: its marker names src, and the
// entrypoint and the flake it mounts are both there.
func isMirrorOf(gen, src string) bool {
	b, err := os.ReadFile(filepath.Join(gen, prefixMirrorMarker))
	if err != nil || !bytes.Equal(bytes.TrimSpace(b), []byte(src)) {
		return false
	}
	for _, f := range []string{filepath.Join(prebuiltBinDir(gen), "yolo-entrypoint"), filepath.Join(gen, "flake.nix")} {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	return true
}

// copyBundle copies the regular files and directories under src into dst, which must not
// exist. Store modes are read-only, and a generation the reaper cannot delete is a leak, so
// every directory is made 0755 and every file gains owner-write, keeping its execute bits. A
// symlink or any other kind of entry is refused: a bundle holds neither, and following one
// would copy something other than the bundle.
func copyBundle(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return copyBundleFile(path, target, info.Mode().Perm()|0o200)
		default:
			return errors.New(path + " is not a regular file or directory")
		}
	})
}

func copyBundleFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
