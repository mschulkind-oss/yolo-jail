// Package iopriotest builds fake mount tables and sysfs trees for ioprio.Resolve, so the
// run pipeline's and `yolo check`'s tests grade the same shapes the resolver's own tests
// do. Test code only.
//
// Name FICTIONAL paths in the mount table ("/yolo-test-ws/…"): Resolve evaluates symlinks
// on the path it is given, so a real t.TempDir() path reads differently on macOS, where
// /var is a link to /private/var.
package iopriotest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sys is one fake root: <root>/proc/self/mountinfo and <root>/sys.
type Sys struct {
	t     *testing.T
	Root  string
	mount []string
}

// New returns an empty fake root.
func New(t *testing.T) *Sys {
	t.Helper()
	return &Sys{t: t, Root: t.TempDir()}
}

// Mount adds one mountinfo line and rewrites the table.
func (s *Sys) Mount(point, fstype, source string) *Sys {
	s.t.Helper()
	s.mount = append(s.mount, fmt.Sprintf("%d 1 0:28 / %s rw,relatime - %s %s rw",
		len(s.mount)+100, strings.ReplaceAll(point, " ", `\040`), fstype, source))
	s.write("proc/self/mountinfo", strings.Join(s.mount, "\n")+"\n")
	return s
}

// Disk adds a whole disk with the given scheduler line ("none mq-deadline [kyber] bfq"),
// or with no scheduler file when sched is "".
func (s *Sys) Disk(name, sched string) *Sys {
	s.t.Helper()
	s.dir("sys/block/" + name)
	s.dir("sys/class/block/" + name)
	if sched != "" {
		s.write("sys/class/block/"+name+"/queue/scheduler", sched+"\n")
	}
	return s
}

// Part adds partition part of disk.
func (s *Sys) Part(disk, part string) *Sys {
	s.t.Helper()
	s.dir("sys/block/" + disk + "/" + part)
	s.write("sys/class/block/"+part+"/partition", "1\n")
	return s
}

// DM adds a device-mapper device dm-N named name, over slaves. A uuid starting "CRYPT-"
// makes it a dm-crypt mapping.
func (s *Sys) DM(dev, name, uuid string, slaves ...string) *Sys {
	s.t.Helper()
	s.write("sys/class/block/"+dev+"/dm/name", name+"\n")
	s.write("sys/class/block/"+dev+"/dm/uuid", uuid+"\n")
	s.dir("sys/class/block/" + dev + "/slaves")
	for _, sl := range slaves {
		s.dir("sys/class/block/" + dev + "/slaves/" + sl)
	}
	return s
}

// Kyber is the verification host's own chain: a btrfs workspace on /dev/mapper/root, a
// LUKS2 mapping over nvme0n1p2, on an NVMe disk running kyber.
func Kyber(t *testing.T, workspace string) *Sys {
	t.Helper()
	return New(t).
		Mount("/", "overlay", "overlay").
		Mount(workspace, "btrfs", "/dev/mapper/root").
		Disk("nvme0n1", "none mq-deadline [kyber] bfq").
		Part("nvme0n1", "nvme0n1p2").
		DM("dm-0", "root", "CRYPT-LUKS2-0123-root", "nvme0n1p2")
}

// Scheduler is a plain disk sda1 → sda running sched, under workspace.
func Scheduler(t *testing.T, workspace, sched string) *Sys {
	t.Helper()
	line := strings.Replace("none mq-deadline kyber bfq", sched, "["+sched+"]", 1)
	return New(t).
		Mount("/", "overlay", "overlay").
		Mount(workspace, "ext4", "/dev/sda1").
		Disk("sda", line).
		Part("sda", "sda1")
}

func (s *Sys) dir(rel string) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Join(s.Root, rel), 0o755); err != nil {
		s.t.Fatal(err)
	}
}

func (s *Sys) write(rel, content string) {
	s.t.Helper()
	p := filepath.Join(s.Root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}
