package prune

import (
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
)

// PruneHostFloor reclaims what an interrupted install left in the HOST AGENT FLOOR
// (paths.HostFloorDir, docs/design/host-tool-provisioning.md): an install directory with no
// completion marker, and a Node download or unpack scratch tree, each only when no process holds
// the lock that would be writing it. Returns (bytesRemoved, itemsRemoved); apply=false reports
// without touching disk.
//
// THAT IS ITS WHOLE REACH, by design. A provisioned entry is never this sweep's: removing the
// agent a selected pack delivers is the job of `yolo host apply --assert`, the act that sees the
// selection (hostfloor.Floor.Reconcile), and a superseded version is bounded by the install that
// supersedes it (the current and the previous are kept). So the floor is self-bounded except for
// the one leftover nothing else revisits: a SIGKILLed install.
//
// Lock-gated rather than age-gated: the floor's installs hold a per-program flock, which the
// kernel releases when the installer dies, so "no holder" is a fact rather than a guess about how
// long an install takes.
func PruneHostFloor(dir string, apply bool) (bytesRemoved int64, itemsRemoved int) {
	return (&hostfloor.Floor{Dir: dir}).Sweep(apply)
}
