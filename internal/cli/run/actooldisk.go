package run

// actooldisk.go creates an Apple Container workspace's TOOL DISK — the named volume its jail
// mounts at /mise, a term coined in internal/prune/misevolumes.go, which owns the disk's name,
// its labels and its reaper.
//
// ONE PER WORKSPACE (OQ-MB1 of docs/research/macos-backend-performance.md, ruled A on
// 2026-10-05). Every Apple Container jail mounted one shared volume before, and a disk image
// attaches to one VM at a time, so while one jail ran a jail in another workspace failed at once
// with VZErrorDomain Code=2. A named volume stays an ext4 disk image, so /mise keeps the VM
// disk's speed and its case-sensitive names; podman's machine-wide volume is untouched.
//
// WHY THE LAUNCH CREATES IT rather than leaving `container run` to: run makes a missing named
// volume itself, but unlabelled, and the label naming the workspace is the only way `yolo prune`
// can later ask whether that workspace still exists — a container name is a hash. So the disk is
// made here, before the argv names it, and only on a fresh launch: an attach mounts nothing.

import (
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// Timeouts for the two runtime calls: a lookup is one XPC call; a create formats a sparse ext4
// image, which writes only its metadata.
const (
	toolDiskInspectTimeout = 15 * time.Second
	toolDiskCreateTimeout  = 2 * time.Minute
)

// ensureAppleContainerToolDisk makes sure cname's tool disk exists before `container run`
// mounts it, creating it labelled with this launch's resolved workspace when it does not.
//
// IT NEVER REFUSES THE LAUNCH. A create that fails leaves the jail exactly where it was before
// this file existed: `container run` makes the disk itself, unlabelled, and the jail works. What
// that costs is said once, with the way out: `yolo prune` cannot attribute an unlabelled disk, so
// it keeps it, `yolo stores` lists it, and `container volume rm` removes it. A runtime too broken
// to create a volume fails the run that follows with its own error.
//
// A disk that exists is left as it is, labelled or not: recreating it would throw away every
// tool the workspace installed.
func (o *Options) ensureAppleContainerToolDisk(cname string, out printer) {
	name := prune.MiseVolumeName(cname)
	if o.toolDiskExists(name) {
		return
	}
	res := o.Exec(prune.MiseVolumeCreateArgv(cname, runtime.ResolveWorkspace(o.Workspace)), "", nil,
		toolDiskCreateTimeout)
	if res.Ran && !res.Timeout && res.RC == 0 {
		out.printf("[dim]Created this workspace's tool disk %s for /mise. On Apple Container each "+
			"workspace keeps its own, so this workspace downloads its tools once, and `yolo prune --apply` "+
			"removes the disk once the workspace is gone.[/dim]", richtext.Escape(name))
		return
	}
	// Another launch of this workspace may have made it meanwhile; that is the outcome wanted.
	if o.toolDiskExists(name) {
		return
	}
	out.printf("[yellow]Warning: could not create this workspace's tool disk %s (%s). The launch goes on, "+
		"and Apple Container makes the disk itself, without the label naming this workspace, so "+
		"`yolo prune` will keep it after the workspace is gone. `yolo stores` lists it, and "+
		"`container volume rm %s` removes it once no jail uses it.[/yellow]",
		richtext.Escape(name), richtext.Escape(toolDiskFailure(res)), richtext.Escape(name))
}

// toolDiskExists asks the runtime whether the named volume exists. Only a lookup that answered
// counts: anything else reads as "not there", which costs one create the runtime refuses.
func (o *Options) toolDiskExists(name string) bool {
	res := o.Exec([]string{"container", "volume", "inspect", name}, "", nil, toolDiskInspectTimeout)
	return res.Ran && !res.Timeout && res.RC == 0
}

// toolDiskFailure is a failed create's reason in a few words: the runtime's last line, or why
// there is none.
func toolDiskFailure(res ExecResult) string {
	switch {
	case !res.Ran:
		return "`container volume create` did not run"
	case res.Timeout:
		return "`container volume create` did not finish within " + toolDiskCreateTimeout.String()
	}
	lines := strings.Split(strings.TrimSpace(res.Stderr), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	return "`container volume create` exited " + strconv.Itoa(res.RC)
}
