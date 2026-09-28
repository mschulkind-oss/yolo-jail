package ioprio_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio/iopriotest"
)

const ws = "/yolo-test-ws/project"

// TestResolveFollowsTheSourceThroughLUKSAndAPartition is the verification host's own chain:
// /dev/mapper/root is found by dm/name (the node does not exist in a jail), dm-0's slave is
// a partition, and the partition's disk is the one with a scheduler. The crossing of
// dm-crypt is recorded, because BFQ loses the priority on its writes.
func TestResolveFollowsTheSourceThroughLUKSAndAPartition(t *testing.T) {
	sys := iopriotest.Kyber(t, ws)
	res := ioprio.Resolve(sys.Root, ws+"/src/deep")
	if res.Kind != ioprio.KindBlock || res.MountPoint != ws || res.Source != "/dev/mapper/root" {
		t.Fatalf("resolution %+v", res)
	}
	if len(res.Disks) != 1 || res.Disks[0] != (ioprio.Disk{Name: "nvme0n1", Scheduler: "kyber", Crypt: true}) {
		t.Fatalf("disks %+v, want nvme0n1 on kyber through dm-crypt", res.Disks)
	}
}

// TestResolvePicksTheLongestMountPoint: /yolo-test-ws/project is under both / and its own
// mount, and only the longer one describes it; a sibling with a shared string prefix
// (/yolo-test-ws/project2) is not under the mount at all.
func TestResolvePicksTheLongestMountPoint(t *testing.T) {
	sys := iopriotest.New(t).
		Mount("/", "ext4", "/dev/sdb1").
		Mount(ws, "ext4", "/dev/sda1").
		Disk("sda", "none [bfq]").Part("sda", "sda1").
		Disk("sdb", "[kyber] none").Part("sdb", "sdb1")
	if got := ioprio.Resolve(sys.Root, ws).Disks[0].Name; got != "sda" {
		t.Errorf("the workspace resolved to %s, want sda (its own mount)", got)
	}
	if got := ioprio.Resolve(sys.Root, ws+"2").Disks[0].Name; got != "sdb" {
		t.Errorf("a sibling with a shared prefix resolved to %s, want sdb (the root mount)", got)
	}
}

// TestResolveReadsEveryDiskOfAnLVMVolume: one volume over two disks is two rows.
func TestResolveReadsEveryDiskOfAnLVMVolume(t *testing.T) {
	sys := iopriotest.New(t).
		Mount(ws, "xfs", "/dev/mapper/vg-data").
		DM("dm-3", "vg-data", "LVM-abc", "sda2", "nvme0n1p1").
		Disk("sda", "mq-deadline [bfq]").Part("sda", "sda2").
		Disk("nvme0n1", "[none] mq-deadline").Part("nvme0n1", "nvme0n1p1")
	res := ioprio.Resolve(sys.Root, ws)
	if len(res.Disks) != 2 {
		t.Fatalf("disks %+v, want both", res.Disks)
	}
	bad := ioprio.NoEffect(ioprio.Low, res)
	if len(bad) != 1 || bad[0].Name != "nvme0n1" || bad[0].Crypt {
		t.Errorf("NoEffect(low) = %+v, want nvme0n1 alone", bad)
	}
}

// TestGradeIsTheDesignTable: bfq honors both; mq-deadline honors "idle" only; kyber and
// none honor neither; an unknown or unread scheduler grades nothing.
func TestGradeIsTheDesignTable(t *testing.T) {
	for _, tc := range []struct {
		sched     string
		idle, low ioprio.Effect
	}{
		{"bfq", ioprio.EffectActs, ioprio.EffectActs},
		{"mq-deadline", ioprio.EffectActs, ioprio.EffectNone},
		{"kyber", ioprio.EffectNone, ioprio.EffectNone},
		{"none", ioprio.EffectNone, ioprio.EffectNone},
		{"", ioprio.EffectUnknown, ioprio.EffectUnknown},
		{"cfq", ioprio.EffectUnknown, ioprio.EffectUnknown},
	} {
		if got := ioprio.Grade(ioprio.Idle, tc.sched); got != tc.idle {
			t.Errorf("idle on %q = %v, want %v", tc.sched, got, tc.idle)
		}
		if got := ioprio.Grade(ioprio.Low, tc.sched); got != tc.low {
			t.Errorf("low on %q = %v, want %v", tc.sched, got, tc.low)
		}
	}
}

// TestNoEffectOverRealShapes is the launch's whole decision: which disks get named.
func TestNoEffectOverRealShapes(t *testing.T) {
	for _, tc := range []struct {
		sched     string
		p         ioprio.Priority
		wantNamed bool
	}{
		{"bfq", ioprio.Low, false},
		{"bfq", ioprio.Idle, false},
		{"mq-deadline", ioprio.Low, true},
		{"mq-deadline", ioprio.Idle, false},
		{"kyber", ioprio.Idle, true},
		{"none", ioprio.Low, true},
	} {
		res := ioprio.Resolve(iopriotest.Scheduler(t, ws, tc.sched).Root, ws)
		if got := len(ioprio.NoEffect(tc.p, res)) > 0; got != tc.wantNamed {
			t.Errorf("%s on %s: named=%v, want %v (%+v)", tc.p, tc.sched, got, tc.wantNamed, res)
		}
	}
	if got := ioprio.NoEffect(ioprio.Normal, ioprio.Resolve(iopriotest.Kyber(t, ws).Root, ws)); got != nil {
		t.Errorf("normal declares nothing, so it names nothing: %+v", got)
	}
}

// TestResolveSortsWhatCannotBeGraded: VirtioFS, a mount with no block device, and a sysfs
// that cannot answer are three different answers, and none of them names a disk.
func TestResolveSortsWhatCannotBeGraded(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sys    *iopriotest.Sys
		want   ioprio.Kind
		fstype string
	}{
		{"virtiofs", iopriotest.New(t).Mount(ws, "virtiofs", "mount0"), ioprio.KindVirtiofs, "virtiofs"},
		{"tmpfs", iopriotest.New(t).Mount(ws, "tmpfs", "tmpfs"), ioprio.KindNoBlock, "tmpfs"},
		{"zfs", iopriotest.New(t).Mount(ws, "zfs", "tank/home"), ioprio.KindNoBlock, "zfs"},
		{"nfs", iopriotest.New(t).Mount(ws, "nfs4", "server:/export"), ioprio.KindNoBlock, "nfs4"},
		{"no sysfs", iopriotest.New(t).Mount(ws, "ext4", "/dev/sda1"), ioprio.KindUnreadable, "ext4"},
		{"unknown dm name", iopriotest.New(t).Mount(ws, "ext4", "/dev/mapper/gone").Disk("sda", "[bfq]"), ioprio.KindUnreadable, "ext4"},
	} {
		res := ioprio.Resolve(tc.sys.Root, ws)
		if res.Kind != tc.want || res.FSType != tc.fstype || len(res.Disks) != 0 {
			t.Errorf("%s: %+v, want kind %v on %s and no disks", tc.name, res, tc.want, tc.fstype)
		}
		if tc.want == ioprio.KindUnreadable && res.Unreadable == "" {
			t.Errorf("%s: an unreadable resolution must say what could not be read", tc.name)
		}
		if got := ioprio.NoEffect(ioprio.Low, res); got != nil {
			t.Errorf("%s: an ungraded path named disks %+v", tc.name, got)
		}
	}
	res := ioprio.Resolve(t.TempDir(), ws)
	if res.Kind != ioprio.KindUnreadable || res.Unreadable != "/proc/self/mountinfo" {
		t.Errorf("no mount table: %+v", res)
	}
}

// TestResolveLeavesADiskWithoutASchedulerUngraded: zram and other bio-based devices have no
// queue/scheduler, and nothing is claimed about them.
func TestResolveLeavesADiskWithoutASchedulerUngraded(t *testing.T) {
	sys := iopriotest.New(t).Mount(ws, "ext4", "/dev/zram0").Disk("zram0", "")
	res := ioprio.Resolve(sys.Root, ws)
	if res.Kind != ioprio.KindBlock || len(res.Disks) != 1 || res.Disks[0].Scheduler != "" {
		t.Fatalf("%+v", res)
	}
	if ioprio.NoEffect(ioprio.Low, res) != nil {
		t.Error("an unread scheduler must not be named as ignoring the value")
	}
}

// TestResolveUnescapesMountPoints: a space in a mount point is \040 in the table.
func TestResolveUnescapesMountPoints(t *testing.T) {
	spaced := "/yolo-test-ws/my project"
	sys := iopriotest.New(t).Mount("/", "overlay", "overlay").Mount(spaced, "ext4", "/dev/sda1").
		Disk("sda", "[bfq]").Part("sda", "sda1")
	if res := ioprio.Resolve(sys.Root, spaced); res.MountPoint != spaced || res.Kind != ioprio.KindBlock {
		t.Errorf("%+v", res)
	}
}

// TestResolveEvaluatesSymlinksOnThePath: a workspace reached through a symlink is graded
// by the mount it really lives on — the darwin /var → /private/var class, reproduced here.
func TestResolveEvaluatesSymlinksOnThePath(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	sys := iopriotest.New(t).Mount("/", "overlay", "overlay").Mount(real, "ext4", "/dev/sda1").
		Disk("sda", "[kyber]").Part("sda", "sda1")
	if res := ioprio.Resolve(sys.Root, link); res.MountPoint != real || res.Disks[0].Scheduler != "kyber" {
		t.Errorf("a symlinked path resolved to %+v, want the mount at %s", res, real)
	}
}
