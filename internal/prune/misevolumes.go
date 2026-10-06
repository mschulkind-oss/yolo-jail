package prune

// misevolumes.go owns Apple Container's TOOL DISKS: the named volume each workspace's jail
// mounts at /mise, mise's tool installs. "Tool disk" is a term coined here, for that volume:
// on Apple Container a named volume is an ext4 disk image attached to the jail's VM as a
// block device (docs/research/macos-backend-performance.md §7).
//
// WHY ONE PER WORKSPACE (OQ-MB1, ruled A on 2026-10-05). Every Apple Container jail used to
// mount the one volume SharedMiseVolume names, and a disk image attaches to one VM at a time:
// while one jail ran, a jail in another workspace failed at once with VZErrorDomain Code=2,
// "The storage device attachment is invalid" (MEASURED on container 1.1.0, §7). A disk per
// workspace keeps what the shared one gave — the VM disk's speed and a case-sensitive
// filesystem, which a Linux tool tree needs (python-build-standalone's terminfo holds names
// that differ only in case) and an APFS folder over virtiofs would not give — at the cost of
// one download per workspace and a disk per workspace, which this file's reaper bounds to the
// workspaces that still exist. Podman's machine-wide volume is untouched: a podman volume
// mounts into any number of containers.
//
// THE NAME AND THE LABEL ARE THE OWNERSHIP EVIDENCE, two halves of it:
//
//	<cname>.mise     e.g. yolo-app-1a2b3c4d.mise
//
// is the disk of the workspace whose container name is <cname> (runtime.FromResolved), so a
// workspace's launches find its disk again from its path alone, and the launch creates it
// with `--label org.yolo-jail.workspace=<resolved workspace>` (MiseVolumeCreateArgv), which is
// what lets the reaper ask whether that workspace still exists — a container name is a hash
// and cannot be turned back into a path. A disk is reaped only when both halves agree and the
// workspace is gone; a disk with no label (Apple Container's `container run` creates a named
// volume itself, unlabelled, when it is missing) or whose label names another workspace is
// kept, and `yolo stores` lists it for the user.
//
// LIKE EVERY REAPER HERE IT IS TRI-STATE: a listing that did not run, failed or did not parse
// is "could not ask", and reaps nothing. In use is the runtime's answer, not this file's:
// `container volume rm` refuses a volume any container, running or stopped, still names.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// SharedMiseVolume is the one named volume every jail mounted at /mise before OQ-MB1, and
// still the one a podman jail on macOS mounts (a Podman Machine volume attaches to any number
// of containers). Versioned so a bump forces a fresh store. On Apple Container no jail of this
// yolo mounts it any more, so the reaper below offers it for removal there.
const SharedMiseVolume = "yolo-mise-data-v2"

// The labels a tool disk is created with. The owner pair is the image's (JailImageOwnerLabel):
// one key says "yolo made this" wherever yolo labels something.
const (
	MiseVolumeOwnerLabel = JailImageOwnerLabel
	MiseVolumeOwnerValue = JailImageOwnerValue
	// MiseVolumeWorkspaceLabel records the resolved workspace path the disk is for: the
	// reaper's only way back from a container name to a directory it can stat.
	MiseVolumeWorkspaceLabel = "org.yolo-jail.workspace"
)

// miseVolumeSuffix follows the container name in a tool disk's name. A container name is
// `yolo-` + [a-z0-9-] (runtime.FromResolved), so the dot cannot occur inside it and the parse
// is exact, as the scratch volumes' is.
const miseVolumeSuffix = ".mise"

// Timeouts for the runtime calls. Listing is one XPC call; a removal deletes one disk image.
const (
	miseVolumeListTimeout = 20 * time.Second
	miseVolumeRmTimeout   = 2 * time.Minute
)

// MiseVolumeName spells the tool disk of the workspace whose container name is cname. The
// launch's mount (run.appleContainerBaseMounts), its create step and this reaper all read it.
func MiseVolumeName(cname string) string { return cname + miseVolumeSuffix }

var miseVolumeRe = regexp.MustCompile(`^(yolo-[a-z0-9-]+)\.mise$`)

// ParseMiseVolumeName reports whether name is a workspace's tool disk, and whose.
func ParseMiseVolumeName(name string) (cname string, ok bool) {
	m := miseVolumeRe.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// MiseVolumeCreateArgv is the command that creates cname's tool disk, labelled with the
// resolved workspace it is for. No size: the disk keeps Apple Container's default ceiling, the
// one the shared disk had (decision MB-D4 in docs/research/macos-backend-performance.md).
func MiseVolumeCreateArgv(cname, workspace string) []string {
	return []string{"container", "volume", "create",
		"--label", MiseVolumeOwnerLabel + "=" + MiseVolumeOwnerValue,
		"--label", MiseVolumeWorkspaceLabel + "=" + workspace,
		MiseVolumeName(cname)}
}

// MiseVolume is one tool disk as Apple Container reports it.
type MiseVolume struct {
	Name string
	// Cname is the container name the disk's name carries; empty for the shared disk.
	Cname string
	// Workspace is the label the launch recorded; empty when the disk carries none.
	Workspace string
	// Source is the disk image's path on the Mac, which `yolo stores` sizes.
	Source string
	// Shared is the one disk every jail mounted before OQ-MB1.
	Shared bool
}

// MiseVolumeState is what the reaper makes of one tool disk.
type MiseVolumeState int

const (
	// MiseVolumeLive is a disk whose workspace exists, or cannot be shown not to.
	MiseVolumeLive MiseVolumeState = iota
	// MiseVolumeWorkspaceGone is a disk whose recorded workspace no longer exists.
	MiseVolumeWorkspaceGone
	// MiseVolumeRetired is the shared disk, which no Apple Container jail of this yolo mounts.
	MiseVolumeRetired
	// MiseVolumeUnattributed is a disk whose workspace yolo cannot tell: no label, or a label
	// naming a workspace whose container name is not the disk's.
	MiseVolumeUnattributed
)

// Reclaimable reports whether `yolo prune --apply` removes a disk in this state.
func (s MiseVolumeState) Reclaimable() bool {
	return s == MiseVolumeWorkspaceGone || s == MiseVolumeRetired
}

// State is the reaper's verdict on v, and the reason in words for a report line. The words
// are rich-markup safe: the workspace label is escaped.
//
// GONE IS ONLY "does not exist". A stat that fails any other way (a permission, an unmounted
// share answering EIO) is a workspace that may still be there, and its disk is kept: removing
// it costs that workspace a re-download of every tool, and keeping it costs disk until the
// next prune that can see.
func (v MiseVolume) State() (MiseVolumeState, string) {
	if v.Shared {
		return MiseVolumeRetired, "the disk every Apple Container jail shared before each workspace got its own; " +
			"no jail of this yolo mounts it"
	}
	if v.Workspace == "" {
		return MiseVolumeUnattributed, "no workspace is recorded on it (Apple Container made it itself, unlabelled)"
	}
	ws := richtext.Escape(v.Workspace)
	if runtime.FromResolved(v.Workspace) != v.Cname {
		return MiseVolumeUnattributed, "its label names " + ws + ", whose disk would be named " +
			MiseVolumeName(runtime.FromResolved(v.Workspace))
	}
	if _, err := os.Stat(v.Workspace); errors.Is(err, fs.ErrNotExist) {
		return MiseVolumeWorkspaceGone, "workspace " + ws + " is gone"
	} else if err != nil {
		return MiseVolumeLive, "workspace " + ws + " could not be checked (" + err.Error() + "), so it is kept"
	}
	return MiseVolumeLive, "workspace " + ws
}

// acVolumeConfig is the subset of an Apple Container volume configuration read here: the
// fields `container volume ls --format json` prints for each volume (VolumeConfiguration,
// identical in the 1.1.0 and 1.5.0 sources). The creation date is not read: its encoding is
// the CLI's choice, and no decision here depends on age.
type acVolumeConfig struct {
	Name   string            `json:"name"`
	Source string            `json:"source"`
	Labels map[string]string `json:"labels"`
}

// acVolumeRow is one listing row: `{"id": …, "configuration": {…}}` (VolumeResource), or a
// bare configuration, which a listing that skipped the wrapper would print.
type acVolumeRow struct {
	Configuration *acVolumeConfig `json:"configuration"`
	acVolumeConfig
}

// ListMiseVolumes returns every tool disk Apple Container holds — each workspace's and the
// shared one — sorted by name, and whether the runtime ANSWERED. known=false is "could not
// ask", and callers must reap nothing on it. Apple Container only: podman's /mise is the
// machine's store, so there is nothing per workspace to list.
func ListMiseVolumes(rt string, run RunFunc) ([]MiseVolume, bool) {
	if rt != "container" {
		return nil, false
	}
	res := run([]string{"container", "volume", "ls", "--format", "json"}, miseVolumeListTimeout)
	if !res.Ran || res.RC != 0 {
		return nil, false
	}
	var rows []acVolumeRow
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &rows); err != nil {
		return nil, false
	}
	var out []MiseVolume
	for _, r := range rows {
		c := r.acVolumeConfig
		if r.Configuration != nil {
			c = *r.Configuration
		}
		v := MiseVolume{Name: c.Name, Source: c.Source, Workspace: c.Labels[MiseVolumeWorkspaceLabel]}
		if c.Name == SharedMiseVolume {
			v.Shared, v.Workspace = true, ""
		} else if cname, ok := ParseMiseVolumeName(c.Name); ok {
			v.Cname = cname
		} else {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, true
}

// RemoveMiseVolume removes one tool disk. Never forced: `container volume rm` refuses a disk
// a container still names, which is the safe answer to a listing that may be seconds old.
func RemoveMiseVolume(v MiseVolume, run RunFunc) bool {
	res := run([]string{"container", "volume", "rm", v.Name}, miseVolumeRmTimeout)
	return res.Ran && res.RC == 0
}

// PruneMiseVolumes is `yolo prune`'s section: the reclaimable tool disks, removed on apply.
// known=false means the runtime could not be asked and nothing was touched. removed is what
// was (or, dry-run, would be) removed, failed what apply could not, and unattributed the disks
// kept because yolo cannot tell whose they are, which the report names so they are not
// silently kept forever.
func PruneMiseVolumes(rt string, apply bool, run RunFunc) (removed, failed, unattributed []MiseVolume, known bool) {
	vols, known := ListMiseVolumes(rt, run)
	if !known {
		return nil, nil, nil, false
	}
	for _, v := range vols {
		st, _ := v.State()
		if st == MiseVolumeUnattributed {
			unattributed = append(unattributed, v)
		}
		if !st.Reclaimable() {
			continue
		}
		if apply && !RemoveMiseVolume(v, run) {
			failed = append(failed, v)
			continue
		}
		removed = append(removed, v)
	}
	return removed, failed, unattributed, true
}
