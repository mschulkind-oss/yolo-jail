package run

import "github.com/mschulkind-oss/yolo-jail/internal/entrypoint"

// captures.go binds the machine's INSTALL-CAPTURE STORE into the jail, read-only, and tells
// the jail where it landed.
//
// # Why a mount at all, when the design said "hardlink"
//
// program-delivery.md §6.3's pipeline originally read *"materialize (per jail, offline):
// unpack/hardlink the capture"*, which reads as if no mount were needed — as if a host-side
// step could link the store's inodes into `<ws>/.yolo/home/...` before the jail starts. It
// cannot be done from INSIDE, which is where §5.2 requires it to happen (materialize from the
// launcher, so "you pay nothing for a tool you never invoke" survives), and `link(2)` cannot
// do it from a jail at all: it compares the MOUNT, so a hardlink from any bind into any other
// bind of the same filesystem returns EXDEV. Mounting the store does not fix that — whatever
// it is mounted at is one more mount.
//
// REFLINK is what makes the mount worth having: FICLONE's predicate is the FILESYSTEM (one
// superblock), and every bind of one filesystem shares it. MEASURED 2026-09-04, in a real
// podman container against exactly the two mounts this file creates and consumes — a `:ro`
// bind of the store and a rw bind of a home surface, one btrfs: FICLONE succeeded (256 MiB
// in 3 ms, 32 KiB of new space) where link(2) returned EXDEV. See capture/clone_linux.go.
//
// # :ro, and what that is and is not
//
// The store is machine-wide state that every jail on the machine reads and NO jail may write:
// an entry is admitted by the host act alone (`yolo capture`), its files are frozen at admit,
// and a jail that could rewrite one would be rewriting bytes every other workspace runs. Same
// argument, same flag, as the pack manifests two blocks up in the argv. It is NOT a
// confidentiality boundary — the store holds vendor installers this machine already runs.

// capturesCtxDir is where the store is mounted in the jail.
//
// Under /ctx with the pack trees and the host-file grants, because it is the same kind of
// thing: a host directory the jail reads and never owns. The entrypoint finds it through
// entrypoint.CapturesDirEnv rather than hardcoding it, for the reason packCtxDir gives —
// Apple Container cannot nest this bind and reads the host path instead, so the destination
// is not a constant the jail side may assume.
const capturesCtxDir = "/ctx/captures"

// capturesArgs emits the store bind plus the env var naming it, or nothing.
//
// NOTHING is a real and expected answer, three ways: a launch whose Options.CapturesDir seam
// returns "" (the capture jail — see Options.CapturesDir), a store path that does not exist
// (nothing has ever been captured on this machine, and podman would otherwise CREATE the
// bind source as an empty directory, which is a store that answers every lookup with a miss
// while looking like a store), and the macos-user backend, which never reaches this code.
// The launcher's branch is written to treat all three as "no capture", so the jail degrades
// to today's download rather than failing.
func (o *Options) capturesArgs(rt, dir string) []string {
	if dir == "" || !o.PathExists(dir) {
		return nil
	}
	if rt == "container" {
		// Apple Container puts the whole workspace state at /home/agent in ONE bind and
		// cannot nest another; it reads host paths directly instead, exactly as the pack
		// staging tree does. The jail-side path is therefore the HOST path.
		return []string{"-e", entrypoint.CapturesDirEnv + "=" + dir}
	}
	return []string{
		"-v", dir + ":" + capturesCtxDir + ":ro",
		"-e", entrypoint.CapturesDirEnv + "=" + capturesCtxDir,
	}
}
