package stores

import (
	"fmt"
	"os"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// toolDiskStores inventories Apple Container's TOOL DISKS (a term coined in
// internal/prune/misevolumes.go): the named volume each workspace's jail mounts at /mise, one per
// workspace since OQ-MB1, and the one disk every jail shared before. ONE ROW PER DISK, keyed by
// its workspace's container name as the durable dirs are keyed by theirs, because the question a
// reader brings is per workspace — which disk is whose, and whether `yolo prune` will take it.
//
// The verdict and the note come from prune's own MiseVolume.State, so this listing and the reaper
// cannot disagree about a disk: one that prune removes says so, and one it keeps because it
// cannot tell whose it is is the user's, with the command that removes it.
//
// Sized by ALLOCATED bytes (DiskBytes), not apparent size: a tool disk is a sparse image whose
// size is its 512 GB ceiling, so the apparent figure would be a wrong number.
//
// Podman has no row: its /mise is the machine's store, one host dir or one Podman Machine
// volume, and nothing in it is per workspace.
func toolDiskStores(o Options, rt string) []Store {
	if rt != "container" {
		return nil
	}
	pruneIt := Reclaimer{Func: "PruneMiseVolumes", Detail: "once its workspace is gone", Trigger: "yolo prune --apply"}
	vols, known := prune.ListMiseVolumes(rt, o.Exec)
	if !known {
		return []Store{{
			Key: "volumes.mise", Section: SectionToolDisks, Name: "tool disks", Path: "container volumes",
			Sizing: SizingUnknown, Reason: "could not list volumes (container unreachable or refused)",
			Reclaimer: pruneIt, Verdict: VerdictYolo,
		}}
	}
	var out []Store
	for _, v := range vols {
		key := "volumes.mise." + v.Cname
		if v.Shared {
			key = "volumes.mise.shared"
		}
		s := Store{Key: key, Section: SectionToolDisks, Name: v.Name, Path: v.Source,
			Reclaimer: pruneIt, Verdict: VerdictYolo}
		if s.Path == "" {
			s.Path = "container volume " + v.Name
		}
		st, why := v.State()
		s.Note = why
		switch st {
		case prune.MiseVolumeWorkspaceGone:
			s.Note += " — `yolo prune --apply` removes this disk"
		case prune.MiseVolumeRetired:
			s.Reclaimer.Detail = "no jail of this yolo mounts it"
			s.Note += " — `yolo prune --apply` removes it"
		case prune.MiseVolumeUnattributed:
			s.Reclaimer, s.Verdict = none(), VerdictHuman
			s.Note += "; `yolo prune` keeps it, and `container volume rm " + v.Name +
				"` removes it once its workspace is gone and no jail uses it"
		}
		sizeToolDisk(&s, v.Source, o)
		out = append(out, s)
	}
	return out
}

// sizeToolDisk folds one disk image's allocated size into its row: unknown, never zero, when
// the image cannot be read.
func sizeToolDisk(s *Store, image string, o Options) {
	if image == "" {
		s.Sizing, s.Reason = SizingUnknown, "the runtime named no disk image for it"
		return
	}
	n, err := o.DiskBytes(image)
	if err != nil {
		s.Sizing, s.Reason = SizingUnknown, fmt.Sprintf("its disk image is not readable here (%v)", err)
		return
	}
	s.Sizing, s.Bytes, s.Files = SizingMeasured, n, 1
}

// allocatedBytes is the real DiskBytes: the bytes a file occupies on its filesystem (its
// allocated blocks), which for a sparse disk image is what it costs the host, and not its
// apparent size, which is its ceiling.
func allocatedBytes(path string) (int64, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !fi.Mode().IsRegular() {
		return 0, fmt.Errorf("not a regular file")
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512, nil
	}
	return fi.Size(), nil
}
