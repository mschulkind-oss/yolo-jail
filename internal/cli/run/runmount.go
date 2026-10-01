package run

// runmount provides the mount-argv builders for `yolo run` — the
// read-only-rootfs scratch mounts and the nested-jail bind-mountpoint
// dereference.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// ScratchMountArgs builds the mount args for the read-only-rootfs scratch dirs.
// --read-only on the rootfs means /tmp, /var/tmp, /var/lib/containers,
// /var/cache/containers, /run, /dev/shm all need explicit writable mounts.
// mode selects the backing for the first four: "volume" (default; disk-backed podman
// volumes) or "tmpfs" (RAM-backed). /run and /dev/shm are always tmpfs. Any
// non-"volume"/"tmpfs" value (including a non-string, modeled here as "") falls back to
// "volume". Frozen contract (argv order must not drift).
//
// THE VOLUMES ARE NAMED, PER LAUNCH (ScratchVolumeNames), and must never go back to
// anonymous `-v /tmp`. Under `podman run --rm` the attached client deletes a container's
// ANONYMOUS volumes itself before it exits, one unlinkat per file the jail ever wrote,
// with the user's terminal held for the whole of it: 32 s on the maintainer's host after
// a 59 h session, the "Window A" linger (docs/reference/perf-logging.md#the-linger-was-the-scratch-volumes).
// --rm leaves a named volume alone, and the launcher deletes it after the terminal is
// back (startScratchRemoval).
func ScratchMountArgs(mode, cname, launchID string) []string {
	if mode != "volume" && mode != "tmpfs" {
		mode = "volume"
	}
	if mode == "volume" {
		var args []string
		for _, s := range prune.ScratchSlots {
			args = append(args, "-v", prune.ScratchVolumeName(cname, launchID, s.Name)+":"+s.Dest)
		}
		return append(args,
			"--tmpfs", "/run",
			"--tmpfs", "/dev/shm:size=2g",
		)
	}
	return []string{
		"--tmpfs", "/tmp:exec,mode=1777",
		"--tmpfs", "/var/tmp:exec,mode=1777",
		"--tmpfs", "/var/lib/containers",
		"--tmpfs", "/var/cache/containers",
		"--tmpfs", "/run",
		"--tmpfs", "/dev/shm:size=2g",
	}
}

// ScratchVolumeNames is the set of volumes ScratchMountArgs mounts for the same inputs:
// the four names in volume mode, none in tmpfs mode. The launcher hands it to the
// remover, so the argv and the removal read one definition.
func ScratchVolumeNames(mode, cname, launchID string) []string {
	if mode == "tmpfs" {
		return nil
	}
	names := make([]string, 0, len(prune.ScratchSlots))
	for _, s := range prune.ScratchSlots {
		names = append(names, prune.ScratchVolumeName(cname, launchID, s.Name))
	}
	return names
}

// newScratchLaunchID is the per-launch half of a scratch volume's name: 16 hex digits,
// fresh on every launch. Never derived from the workspace — a relaunch while the last
// session's volumes are still being deleted would otherwise be handed them, since podman
// silently reuses a named volume that exists.
func newScratchLaunchID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on a supported platform; the clock is the fallback so
		// a launch never refuses over a name.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// BindMountTargets returns the set of paths that are themselves bind mountpoints
// in the current mount namespace, read from /proc/self/mountinfo (field 5, the
// mount point). Used to detect the nested-jail case where a host source *file*
// we want to bind-mount :ro is itself a bind mountpoint (rootless nested
// podman/crun can't use such a file as a bind source). Empty set on any read
// error (non-Linux, restricted proc).
//
// The field is DECODED: the kernel writes a space in it as \040 (and a tab, a newline
// and a backslash as \011, \012, \134), so a mount point with a space in it would
// otherwise never match the path it is.
func BindMountTargets() map[string]struct{} {
	return bindMountTargetsFrom("/proc/self/mountinfo")
}

func bindMountTargetsFrom(mountinfoPath string) map[string]struct{} {
	targets := map[string]struct{}{}
	data, err := os.ReadFile(mountinfoPath)
	if err != nil {
		return targets
	}
	for _, line := range splitLines(string(data)) {
		parts := fields(line)
		if len(parts) >= 5 {
			targets[ioprio.UnescapeMountinfo(parts[4])] = struct{}{}
		}
	}
	return targets
}

// IsBindMountpoint reports whether path (or its realpath) is itself a bind
// mountpoint. A plain directory-mountpoint check misses single-file binds, so
// we match against the /proc/self/mountinfo targets.
func IsBindMountpoint(path string, mountTargets map[string]struct{}) bool {
	rp, err := filepath.EvalSymlinks(path)
	if err != nil {
		rp = path
	}
	if _, ok := mountTargets[path]; ok {
		return true
	}
	_, ok := mountTargets[rp]
	return ok
}

// ROFileMountArg returns the `-v host_file:container_path:ro` args, dereferencing
// a nested bind. When hostFile is itself a bind mountpoint (nested jail),
// rootless podman can't use it as a bind source, so it's copied to a plain file
// at wsState/rel and that stable inode is mounted instead; a copy failure falls
// back to the direct mount. On a real host the file is plain → direct mount, no
// copy. copyFile is the test seam (nil for the real copy, copyFileBeneath).
//
// THE COPY IS BENEATH wsState (copyFileBeneath): wsState is writable from the jail, so a
// link left at rel, or at a directory above it, would carry the copy onto a host file, and
// podman would then bind the link's target. A link at rel is replaced; one above it refuses
// the copy, which falls back to the direct mount.
func ROFileMountArg(hostFile, containerPath, wsState, rel string, mountTargets map[string]struct{}, copyFile func(src, root, rel string) error) []string {
	src := hostFile
	if IsBindMountpoint(hostFile, mountTargets) {
		cp := copyFile
		if cp == nil {
			cp = copyFileBeneath
		}
		if err := cp(hostFile, wsState, rel); err == nil {
			src = filepath.Join(wsState, rel)
		}
		// copy failure → keep src = hostFile (best-effort direct mount).
	}
	return []string{"-v", src + ":" + containerPath + ":ro"}
}

// splitLines splits on '\n' (mountinfo/proc lines are LF-delimited).
func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// fields splits on runs of ASCII whitespace (mountinfo parsing uses this —
// field 5 is the mount point).
func fields(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		for i < len(s) && !isSpace(s[i]) {
			i++
		}
		out = append(out, s[start:i])
	}
	return out
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
