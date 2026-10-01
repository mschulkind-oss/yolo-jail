package ioprio

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// disk.go answers "which physical disks does this path's I/O land on, and what scheduler
// does each run" — the one resolver the launch's disclosure line and `yolo check`'s row
// share, so the two cannot disagree about a disk (IO-D5).
//
// It reads two things and nothing else: the mount table (/proc/self/mountinfo) and sysfs
// (/sys/class/block, /sys/block). Both are readable inside a podman jail (measured), which
// is how a nested launch grades the disk from inside the outer jail.
//
// ⚠ IT NEVER MATCHES A DEVICE NUMBER. On btrfs the mount table and stat(2) disagree: every
// subvolume gets an anonymous device of its own, so /workspace's st_dev is 0:61 while the
// mount table says 0:28 (measured). And the mount's source node (/dev/mapper/root) does not
// exist inside a jail. So a path is matched to its mount by the longest mount-point prefix,
// and the mount is followed by its SOURCE NAME through sysfs.

// Kind is what the resolver found under a path.
type Kind int

const (
	// KindBlock: the mount is block-backed and its parent disks were found. Each Disk
	// carries the scheduler it read, or "" where the disk has no scheduler file.
	KindBlock Kind = iota
	// KindVirtiofs: a VirtioFS mount, as in an Apple Container or podman-machine jail. Its
	// I/O reaches the Mac over a protocol with no priority field, so no disk is graded.
	KindVirtiofs
	// KindNoBlock: no block device is behind the mount (ZFS, NFS, tmpfs, an overlay), so
	// there is no scheduler to grade.
	KindNoBlock
	// KindUnreadable: the mount table or sysfs could not answer. That proves nothing about
	// the disk either way.
	KindUnreadable
)

// Disk is one parent disk: a device with neither slaves nor a partition parent, which is
// the only kind that has a scheduler (a LUKS or LVM device has no queue/scheduler).
type Disk struct {
	Name      string
	Scheduler string
	// Crypt: the path to this disk crossed a dm-crypt mapping, whose writes BFQ serves
	// at the worker thread's priority rather than the caller's (docs/design/io-priority.md
	// §3.1).
	Crypt bool
}

// Resolution is what Resolve found for one path.
type Resolution struct {
	Path       string
	MountPoint string
	FSType     string
	Source     string
	Kind       Kind
	Disks      []Disk
	// Unreadable names what could not be read, for KindUnreadable.
	Unreadable string
}

// maxDepth bounds the slave walk. Real stacks are two or three deep (LUKS on LVM on a
// partition); a cycle in a fake or corrupt sysfs must not hang a launch.
const maxDepth = 16

// Resolve finds path's mount and follows it to its parent disks. root is the filesystem
// root the mount table and sysfs are read under: "/" in production, a fixture tree in a
// test. It never fails: every problem is a Kind.
func Resolve(root, path string) Resolution {
	if root == "" {
		root = "/"
	}
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = resolved
	}
	res := Resolution{Path: clean}
	mountinfo := filepath.Join(root, "proc", "self", "mountinfo")
	data, err := os.ReadFile(mountinfo)
	if err != nil {
		res.Kind, res.Unreadable = KindUnreadable, "/proc/self/mountinfo"
		return res
	}
	m, ok := longestMount(string(data), clean)
	if !ok {
		res.Kind, res.Unreadable = KindUnreadable, "/proc/self/mountinfo (no mount covers "+clean+")"
		return res
	}
	res.MountPoint, res.FSType, res.Source = m.point, m.fstype, m.source
	switch {
	case m.fstype == "virtiofs":
		res.Kind = KindVirtiofs
		return res
	case !strings.HasPrefix(m.source, "/dev/"):
		res.Kind = KindNoBlock
		return res
	}
	name, err := blockName(root, m.source)
	if err != nil {
		if byDev, ok := blockNameByDev(root, m.dev); ok {
			name, err = byDev, nil
		}
	}
	if err != nil {
		res.Kind, res.Unreadable = KindUnreadable, err.Error()
		return res
	}
	seen := map[string]bool{}
	if err := walk(root, name, false, 0, seen, &res.Disks); err != nil {
		res.Kind, res.Unreadable, res.Disks = KindUnreadable, err.Error(), nil
		return res
	}
	res.Kind = KindBlock
	return res
}

type mount struct {
	point, fstype, source string
	// dev is mountinfo's major:minor field (field 3), the device the kernel mounted. It is
	// the fallback when source names no /sys/class/block entry: `/dev/root` is a kernel
	// alias for the boot root (GitHub's runners mount / from it), with no sysfs entry of that
	// name. btrfs reports an anonymous 0:N here, so it is never trusted when the major is 0.
	dev string
}

// longestMount picks the mount whose mount point is the longest prefix of path. Where two
// mounts share a mount point, the later line wins: it is stacked on top.
func longestMount(mountinfo, path string) (mount, bool) {
	var best mount
	found := false
	for _, line := range strings.Split(mountinfo, "\n") {
		m, ok := parseMountinfoLine(line)
		if !ok || !covers(m.point, path) {
			continue
		}
		if !found || len(m.point) >= len(best.point) {
			best, found = m, true
		}
	}
	return best, found
}

// covers reports whether mount point mp contains path.
func covers(mp, path string) bool {
	if mp == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == mp || strings.HasPrefix(path, mp+"/")
}

// parseMountinfoLine reads one proc(5) mountinfo line:
//
//	36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
//
// field 5 is the mount point; after the " - " separator come the type and the source.
func parseMountinfoLine(line string) (mount, bool) {
	sep := strings.Index(line, " - ")
	if sep < 0 {
		return mount{}, false
	}
	pre, post := strings.Fields(line[:sep]), strings.Fields(line[sep+3:])
	if len(pre) < 5 || len(post) < 2 {
		return mount{}, false
	}
	return mount{point: UnescapeMountinfo(pre[4]), fstype: post[0], source: UnescapeMountinfo(post[1]), dev: pre[2]}, true
}

// UnescapeMountinfo undoes the kernel's octal escapes (\040 for a space, \011, \012, \134).
func UnescapeMountinfo(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, ok := octal3(s[i+1 : i+4]); ok {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func octal3(s string) (byte, bool) {
	v := 0
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0, false
		}
		v = v*8 + int(c-'0')
	}
	return byte(v), v < 256
}

// blockName maps a mount source to its /sys/class/block entry. /dev/mapper/<name> is
// matched against every dm device's dm/name, because the node itself does not exist inside
// a jail; /dev/<name> is taken as the entry's own name.
func blockName(root, source string) (string, error) {
	classBlock := filepath.Join(root, "sys", "class", "block")
	if name, ok := strings.CutPrefix(source, "/dev/mapper/"); ok {
		entries, err := os.ReadDir(classBlock)
		if err != nil {
			return "", fmt.Errorf("/sys/class/block (%s)", why(err))
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "dm-") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(classBlock, e.Name(), "dm", "name"))
			if err == nil && strings.TrimSpace(string(b)) == name {
				return e.Name(), nil
			}
		}
		return "", fmt.Errorf("/sys/class/block/dm-*/dm/name (no device-mapper device is named %s)", name)
	}
	name := filepath.Base(source)
	if _, err := os.Stat(filepath.Join(classBlock, name)); err != nil {
		return "", fmt.Errorf("/sys/class/block/%s (%s)", name, why(err))
	}
	return name, nil
}

// blockNameByDev maps a mount's major:minor to its /sys/class/block entry through the
// /sys/dev/block/<major>:<minor> link. A 0 major is an anonymous device (btrfs subvolumes,
// overlay) that names no block device, so it answers nothing.
func blockNameByDev(root, dev string) (string, bool) {
	if dev == "" || strings.HasPrefix(dev, "0:") {
		return "", false
	}
	target, err := os.Readlink(filepath.Join(root, "sys", "dev", "block", dev))
	if err != nil {
		return "", false
	}
	name := filepath.Base(target)
	if _, err := os.Stat(filepath.Join(root, "sys", "class", "block", name)); err != nil {
		return "", false
	}
	return name, true
}

// why is an error's cause without the path, which every message here already names.
func why(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// walk follows name down through device-mapper slaves and partitions to its parent disks,
// appending each once.
func walk(root, name string, crypt bool, depth int, seen map[string]bool, out *[]Disk) error {
	if depth > maxDepth {
		return fmt.Errorf("/sys/class/block/%s (the slave chain is deeper than %d)", name, maxDepth)
	}
	dev := filepath.Join(root, "sys", "class", "block", name)
	if _, err := os.Stat(dev); err != nil {
		return fmt.Errorf("/sys/class/block/%s (%s)", name, why(err))
	}
	if uuid, err := os.ReadFile(filepath.Join(dev, "dm", "uuid")); err == nil && strings.HasPrefix(string(uuid), "CRYPT-") {
		crypt = true
	}
	if slaves, err := os.ReadDir(filepath.Join(dev, "slaves")); err == nil && len(slaves) > 0 {
		names := make([]string, 0, len(slaves))
		for _, s := range slaves {
			names = append(names, s.Name())
		}
		sort.Strings(names)
		for _, s := range names {
			if err := walk(root, s, crypt, depth+1, seen, out); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err := os.Stat(filepath.Join(dev, "partition")); err == nil {
		parent, err := partitionParent(root, name)
		if err != nil {
			return err
		}
		return walk(root, parent, crypt, depth+1, seen, out)
	}
	if seen[name] {
		return nil
	}
	seen[name] = true
	*out = append(*out, Disk{Name: name, Scheduler: readScheduler(filepath.Join(dev, "queue", "scheduler")), Crypt: crypt})
	return nil
}

// partitionParent finds the whole disk a partition belongs to: the /sys/block/<disk> that
// holds a <partition> directory. /sys/block lists whole disks only.
func partitionParent(root, part string) (string, error) {
	sysBlock := filepath.Join(root, "sys", "block")
	entries, err := os.ReadDir(sysBlock)
	if err != nil {
		return "", fmt.Errorf("/sys/block (%s)", why(err))
	}
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(sysBlock, e.Name(), part)); err == nil {
			return e.Name(), nil
		}
	}
	return "", fmt.Errorf("/sys/block/*/%s (no disk holds this partition)", part)
}

// readScheduler returns the active scheduler: the bracketed name in
// "none mq-deadline [kyber] bfq", or the only name when there is one. "" when the file is
// missing or unreadable, which Grade reads as unknown.
func readScheduler(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(b))
	for _, f := range fields {
		if strings.HasPrefix(f, "[") && strings.HasSuffix(f, "]") {
			return strings.Trim(f, "[]")
		}
	}
	if len(fields) == 1 {
		return fields[0]
	}
	return ""
}

// Effect is what a priority does on one disk.
type Effect int

const (
	// EffectUnknown: the scheduler is unread or not one this grading knows.
	EffectUnknown Effect = iota
	// EffectActs: the scheduler honors the value.
	EffectActs
	// EffectNone: the scheduler ignores the value.
	EffectNone
)

// Grade is docs/design/io-priority.md §5.4's table. BFQ honors both values; mq-deadline
// keeps one queue per class and ignores the level, so "low" (BE7) is BE4 there and only
// "idle" yields; Kyber and none ignore priority altogether.
func Grade(p Priority, scheduler string) Effect {
	switch scheduler {
	case "bfq":
		return EffectActs
	case "mq-deadline":
		if p == Idle {
			return EffectActs
		}
		return EffectNone
	case "kyber", "none":
		return EffectNone
	}
	return EffectUnknown
}

// gradedSchedulers is every scheduler Grade knows, in the order a surface names them.
var gradedSchedulers = []string{"bfq", "mq-deadline", "kyber", "none"}

// IgnoredBy is the schedulers Grade says p has no effect on, for a surface that names them
// without a disk in hand: the briefing, which is written before any disk is graded. An
// undeclared value names none. So "low" names mq-deadline and "idle" does not, and no
// surface can say a class yields on a disk the launch line calls ignored.
func IgnoredBy(p Priority) []string {
	if !p.Declared() {
		return nil
	}
	var out []string
	for _, s := range gradedSchedulers {
		if Grade(p, s) == EffectNone {
			out = append(out, s)
		}
	}
	return out
}

// NoEffect is the disks under res on which p does nothing: the launch prints its disclosure
// line exactly when this is non-empty. An unresolved path yields none, because it proves
// nothing about whether the value acts there.
func NoEffect(p Priority, res Resolution) []Disk {
	if !p.Declared() || res.Kind != KindBlock {
		return nil
	}
	var out []Disk
	for _, d := range res.Disks {
		if Grade(p, d.Scheduler) == EffectNone {
			out = append(out, d)
		}
	}
	return out
}

// DiskList renders disks as "nvme0n1 (scheduler kyber)", joined as a sentence.
func DiskList(disks []Disk) string {
	parts := make([]string, 0, len(disks))
	for _, d := range disks {
		parts = append(parts, d.Name+" (scheduler "+d.Scheduler+")")
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// IgnoresWhy is the one sentence both surfaces use for why a disk ignores the value.
const IgnoresWhy = `kyber and none ignore I/O priority; bfq honors "low" and "idle", mq-deadline only "idle"`
