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
// It is also why ONE backend gets no store at all — see capturesArgs' Apple Container arm.
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
// the destination differs per backend, so it is not a constant the jail side may assume.
// On Apple Container there is no destination at all; the branch below says why.
const capturesCtxDir = "/ctx/captures"

// capturesArgs emits the store bind plus the env var naming it, or nothing.
//
// NOTHING is a real and expected answer, FOUR ways: a launch whose Options.CapturesDir seam
// returns "" (the capture jail — see Options.CapturesDir), a store path that does not exist
// (nothing has ever been captured on this machine, and podman would otherwise CREATE the
// bind source as an empty directory, which is a store that answers every lookup with a miss
// while looking like a store), the macos-user backend, which never reaches this code, and
// Apple Container, which cannot be given a read-only bind (below). The launcher's branch is
// written to treat all four as "no capture" — `[ -n "$CAPTURES_DIR" ] || return 1` then
// `[ -d "$CAPTURES_DIR" ] || return 1`, shims.go — so the jail degrades to today's download
// rather than failing.
func (o *Options) capturesArgs(rt, dir string) []string {
	if dir == "" || !o.PathExists(dir) {
		return nil
	}
	// THE SAME ANSWER THE ARGV AND THE BRIEFING USE, not a fourth spelling of
	// `rt == "container"`. This was that bare comparison until 2026-09-16, which made it the
	// one `:ro` site that did not consult the shared predicate — so it refused the store on
	// EVERY Apple Container launch, including the versions that honor the suffix. The bar the
	// long comment below sets ("a writable machine-wide store is cross-jail injection") is
	// met by a `:ro` that is enforced, and 1.1.0 enforces it: measured 2026-09-16 in an AC
	// jail, an overwrite of a bound file and a create beside it both fail
	// `Read-only file system` and the host bytes are unchanged. So the refusal now expires
	// with the version it is about, exactly as mounts.go's does. G27 in
	// docs/plans/setup-support-gaps.md.
	if reason := o.roBindsUnsupported(rt); reason != "" { // parity: Dropped below acROBindsFloor — a writable machine-wide store is cross-jail injection, so the mount is refused rather than downgraded
		// APPLE CONTAINER BELOW THE FLOOR GETS NO STORE, and this is the honest spelling of
		// what it already had rather than a capability being withdrawn.
		//
		// It used to emit `-e YOLO_CAPTURES_DIR=<host path>` under the premise that "Apple
		// Container puts the whole workspace state at /home/agent in ONE bind and cannot
		// nest another; it reads host paths directly instead, exactly as the pack staging
		// tree does". Both halves were false, and the second was false in the same way that
		// left that backend with no packs at all (issue #44, assemble.go's YOLO_PACK_ROOT
		// branch): Apple Container exposes only what the launch shares, so the variable named
		// a path that is not in the jail. NESTING was never the blocker either —
		// appleContainerBaseMounts nests GlobalCache at /home/agent/.cache, inside the
		// wsState bind, and packFilesMountArgs nests a `files` directory.
		//
		// WHAT IS ACTUALLY IN THE WAY IS `:ro`. That backend accepts the suffix and ignores
		// it (roBindsUnsupported), and the header above states why a writable store is not a
		// degradation anyone may accept here: an entry is admitted by the host act alone and
		// a jail that could rewrite one would be rewriting bytes EVERY OTHER WORKSPACE on
		// this machine runs. That is cross-jail code injection, which is a strictly larger
		// crossing than the within-session one the pack tree's copy accepts, and
		// roBindsUnsupported's rule already covers it: a caller refuses the mount rather
		// than downgrade it.
		//
		// AND THE PACK TREE'S ANSWER — copy into ws_state — DOES NOT TRANSFER, for three
		// reasons that are all about what a store IS. It is machine-wide, so a per-workspace
		// copy is N copies of the thing whose whole purpose is to exist once. It is LARGE
		// (claude's five builds measured 1.2 GB, paths.CapturesDir), so copying it per launch
		// spends more than the download it saves. And the header's reflink argument dies with
		// it: the mount is worth having because FICLONE's predicate is the filesystem, so
		// materialize is a 3 ms clone — a copy into ws_state has already paid the bytes that
		// buys back.
		//
		// So the jail is told nothing, which is a state every reader already handles, and
		// which is also what it effectively had: the host path failed the launcher's
		// `[ -d "$CAPTURES_DIR" ]` test, so this backend has always downloaded. The cost is
		// unchanged and now it is stated rather than implied.
		//
		// SAID, not merely implied, since 2026-09-16: silence was defensible while every AC
		// launch behaved this way, and it is not once the same machine can go either way on a
		// `container` upgrade. The line names the version reason, in mounts.go's shape, so a
		// user who wonders why the vendor installer downloads again gets the answer at the
		// launch instead of from this comment.
		o.pr(o.Stdout).print("[yellow]Vendor-installer captures are not mounted on this " +
			"runtime[/yellow] — " + reason + " Installs in the jail download as usual.")
		return nil
	}
	return []string{
		"-v", dir + ":" + capturesCtxDir + ":ro",
		"-e", entrypoint.CapturesDirEnv + "=" + capturesCtxDir,
	}
}
