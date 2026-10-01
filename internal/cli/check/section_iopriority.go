package check

import (
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sectionIOPriority grades the disk a declared `resources.io.priority` lands on
// (docs/design/io-priority.md §5.4). It prints nothing at all unless a priority other than
// "normal" is declared: an undeclared key gets no row (OQ-DP5).
//
// The grading is the launch's own (internal/ioprio.Resolve and Grade, IO-D5), so `yolo
// check` and the launch's disclosure line cannot disagree about a disk. What this adds is
// the cases the launch stays silent on — a disk that honors the value, and a path that
// cannot be graded — and the host change for a disk that ignores it.
//
// SEVERITY IS NOT A NEW LEVEL. A disk that ignores the value is [WARN], "the badge that
// means act on this", because a declaration doing nothing is P1's one illegal state; it
// never changes the exit code, since Check() returns 1 on failures alone. A host fact a
// podman jail cannot see is hostFact's [SKIP] (OQ-IO5, answered from reporter.go's rulings).
func (o *Options) sectionIOPriority(r *reporter, merged *jsonx.OrderedMap, runtimeSel string) {
	var res *jsonx.OrderedMap
	if merged != nil {
		v, _ := merged.Get("resources")
		res, _ = v.(*jsonx.OrderedMap)
	}
	p := ioprio.FromResources(res)
	if !p.Declared() {
		return
	}
	r.sectionHeader("Disk I/O priority")
	defer r.blank()
	key := `resources.io.priority "` + string(p) + `"`

	if o.IsMacOS {
		switch {
		case inStrSlice(paths.NativeRuntimes, runtimeSel):
			r.warn(key+" is not applied on macos-user yet",
				"The launch names the key. setiopolicy_np is the planned mechanism, once a Mac\n"+
					"shows the policy survives the launch's sudo and sandbox-exec. Until yolo applies\n"+
					"it, nothing on this Mac can: "+ioDropKey)
		default:
			backend := "podman on macOS"
			if runtimeSel == "container" {
				backend = "Apple Container"
			}
			r.warn(key+" is not applied on "+backend, ioVirtiofsWhy+"\n"+ioDropKey)
		}
		return
	}

	o.gradeIOPath(r, p, key, o.Workspace)
	if !o.inJail() && runtimeSel == "podman" {
		if root := o.podmanGraphRoot(); root != "" {
			o.gradeIOPath(r, p, key, root)
		}
	}
}

// ioDropKey is the next step where no host change makes a priority act: leave the key, which
// is harmless, or drop it, which silences the row.
const ioDropKey = "Nothing here can make it act; remove resources.io.priority to silence this, " + recheck

// ioVirtiofsWhy is why a priority does nothing on a VM backend.
const ioVirtiofsWhy = "The jail runs in a VM, and its workspace reaches the Mac over VirtioFS,\n" +
	"which carries no I/O priority."

// gradeIOPath is one path's rows: one per parent disk, or one saying why there is none.
func (o *Options) gradeIOPath(r *reporter, p ioprio.Priority, key, path string) {
	res := ioprio.Resolve(o.ioSysRoot, path)
	switch res.Kind {
	case ioprio.KindVirtiofs:
		r.warn(key+" does nothing under "+res.Path+", a VirtioFS mount", ioVirtiofsWhy+"\n"+ioDropKey)
	case ioprio.KindNoBlock:
		r.skip(res.Path+" is on "+res.FSType+", with no block device behind it: no disk scheduler to grade", "")
	case ioprio.KindUnreadable:
		msg := "could not read " + res.Unreadable + " to grade the disk under " + res.Path
		if o.inJail() {
			r.hostFact(msg, "Run `cat /sys/block/<disk>/queue/scheduler` on the host.")
		} else {
			r.skip(msg, "")
		}
	case ioprio.KindBlock:
		for _, d := range res.Disks {
			o.gradeIODisk(r, p, key, res.Path, d)
		}
	}
}

// gradeIODisk is one parent disk's row.
func (o *Options) gradeIODisk(r *reporter, p ioprio.Priority, key, path string, d ioprio.Disk) {
	on := d.Name + " (scheduler " + d.Scheduler + "), the disk under " + path
	switch ioprio.Grade(p, d.Scheduler) {
	case ioprio.EffectActs:
		msg := key + " acts on " + on
		if d.Crypt && d.Scheduler == "bfq" {
			msg += ", on reads only: dm-crypt submits its writes from worker threads"
		}
		r.ok(msg)
	case ioprio.EffectNone:
		note := ioprio.IgnoresWhy + ".\nHost change (root): switch " + d.Name +
			"'s scheduler to bfq, and persist it with a udev rule:\n" +
			"  echo bfq | sudo tee /sys/block/" + d.Name + "/queue/scheduler\n" +
			"  echo 'ACTION==\"add|change\", KERNEL==\"" + d.Name + "\", ATTR{queue/scheduler}=\"bfq\"' | " +
			"sudo tee /etc/udev/rules.d/60-yolo-ioscheduler.rules"
		if d.Scheduler == "mq-deadline" {
			note += `
Or declare "idle", which mq-deadline honors.`
		}
		note += "\n" + recheck
		r.warn(key+" does nothing on "+on, note)
	default:
		if d.Scheduler == "" {
			r.skip(d.Name+", the disk under "+path+", has no queue/scheduler file: no disk scheduler to grade", "")
		} else {
			r.skip(d.Name+", the disk under "+path+", runs scheduler "+d.Scheduler+", which this check does not grade", "")
		}
	}
}

// podmanGraphRoot is podman's storage root, where a jail's container layers are written,
// or "" when podman cannot say.
func (o *Options) podmanGraphRoot() string {
	res := o.Exec([]string{"podman", "info", "--format", "{{.Store.GraphRoot}}"}, "", nil, 10*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}
