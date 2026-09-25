package run

// trackingcleanup.go drops a jail's TRACKING FILE (runtime.WriteContainerTracking:
// CONTAINER_DIR/<cname>, the container → workspace map `yolo ps` reads) once the launch that
// wrote it has observed the container gone.
//
// WHY IT EXISTS: the per-jail home skeleton's cleanup depends on it. The maintainer's OQ-BH10
// ruling (docs/design/base-home-legacy-state.md#OQ-BH10) is that old skeletons go with the
// jail's AGENTS_DIR/<cname> entry through prune.PruneOrphanAgentStaging, and that reaper
// keeps every name that is live OR TRACKED. Every jail runs `--rm`, yet nothing on a normal
// exit removed its tracking file: only a stale-container removal, `yolo ps`, `yolo check`'s
// cleanup and a capture did. So a workspace in use kept its entry, and gained one skeleton
// per fresh launch, for as long as nobody ran `yolo ps`.
//
// THE RULE IS "KNOWN GONE", never "probably gone". The tracking file goes only when all of
// these hold:
//
//   - the workspace lock is free, taken non-blocking. A fresh launch holds it from before it
//     writes its own tracking file until its container is running, so taking it here means
//     no other launch of this workspace is inside that window; failing to take it means one
//     may be, and the file may be ITS, so it stays;
//   - the runtime ANSWERED that no container of this name exists (probeExistingContainer's
//     known=true with no id). A probe that did not run, timed out or failed is "could not
//     ask", and the file stays, the same fail-safe every liveness-gated sweep here uses.
//
// Whatever it declines to remove is left exactly as before this existed: the next `yolo ps`
// or stale-container removal clears it.

import (
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// trackingProbeTimeout bounds the one runtime query forgetGoneContainer makes. It runs in
// the teardown chain, where a hung `podman ps` would hold the user's prompt; the same bound
// liveYoloContainers uses.
const trackingProbeTimeout = 10 * time.Second

// forgetGoneContainer removes cname's tracking file when no container of that name exists
// and no other launch of the workspace is in flight (the file header states the rule).
//
// Called from the three places a launch sees its container end: the normal-exit teardown
// (teardownAfterExit), the signal arm (runContainer's onTerminate) and a runtime that never
// started (runContainer's runErr branch). Each calls it AFTER the launch's own workspace lock
// is released, or the non-blocking take below would find this process holding it.
//
// skeleton is the per-jail home skeleton THIS launch built, or "" (an attach, Apple Container,
// or a launch that built none). On the same known-gone evidence it goes too (OQ-BH16,
// docs/design/base-home-legacy-state.md): this launch is the only one that knows its name, and
// the one container that could hold it is gone. That is not the edit of a live skeleton the
// design's rule 2 forbids. A skeleton this declines to remove stays for the reaper, which then
// holds only launches that died with no teardown at all (SIGKILL, OOM).
func (o *Options) forgetGoneContainer(cname, rt, skeleton string) {
	if cname == "" {
		return
	}
	lock, ok := tryWorkspaceLock(cname)
	if !ok {
		return
	}
	defer lock.Close()
	if id, known := o.probeExistingContainer(cname, rt, trackingProbeTimeout); !known || id != "" {
		return
	}
	runtime.CleanupContainerTracking(cname)
	// The same path guard as a skeleton no container ever held: a direct child of this
	// cname's skeleton root, never anything else.
	discardUnheldSkeleton(cname, skeleton)
}

// tryWorkspaceLock takes cname's workspace lock (the file acquireWorkspaceLock blocks on)
// WITHOUT waiting, and reports whether it got it. Any failure, contention or otherwise, is
// "not taken": the caller then leaves alone whatever the lock would have protected.
func tryWorkspaceLock(cname string) (*workspaceLock, bool) {
	lockDir := filepath.Join(paths.GlobalStorage(), "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, false
	}
	f, err := os.OpenFile(filepath.Join(lockDir, cname+".lock"), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, false
	}
	if err := flockSyscall(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, false
	}
	return &workspaceLock{f: f}, true
}
