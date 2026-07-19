// Package runmount provides the mount-argv builders for `yolo run` — the
// read-only-rootfs scratch mounts and the nested-jail bind-mountpoint
// dereference.
package runmount

import (
	"os"
	"path/filepath"
)

// ScratchMountArgs builds the mount args for the read-only-rootfs scratch dirs.
// --read-only on the rootfs means /tmp, /var/tmp, /var/lib/containers,
// /var/cache/containers, /run, /dev/shm all need explicit writable mounts.
// mode selects the backing for the first four: "volume" (default; anonymous
// podman volumes, disk-backed, --rm-wiped) or "tmpfs" (RAM-backed). /run and
// /dev/shm are always tmpfs. Any non-"volume"/"tmpfs" value (including a
// non-string, modeled here as "") falls back to "volume". Mirrors
// _scratch_mount_args byte-for-byte incl. argv order.
func ScratchMountArgs(mode string) []string {
	if mode != "volume" && mode != "tmpfs" {
		mode = "volume"
	}
	if mode == "volume" {
		return []string{
			"-v", "/tmp",
			"-v", "/var/tmp",
			"-v", "/var/lib/containers",
			"-v", "/var/cache/containers",
			"--tmpfs", "/run",
			"--tmpfs", "/dev/shm:size=2g",
		}
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

// BindMountTargets returns the set of paths that are themselves bind mountpoints
// in the current mount namespace, read from /proc/self/mountinfo (field 5, the
// mount point). Used to detect the nested-jail case where a host source *file*
// we want to bind-mount :ro is itself a bind mountpoint (rootless nested
// podman/crun can't use such a file as a bind source). Empty set on any read
// error (non-Linux, restricted proc). Mirrors _bind_mount_targets.
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
			targets[parts[4]] = struct{}{}
		}
	}
	return targets
}

// IsBindMountpoint reports whether path (or its realpath) is itself a bind
// mountpoint. os.path.ismount only detects directory mountpoints, not single-
// file binds, so we match against the /proc/self/mountinfo targets. Mirrors
// _is_bind_mountpoint.
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
// copy. Mirrors _ro_file_mount_arg. copyFile is injected for testability (pass
// nil to use the real copy).
func ROFileMountArg(hostFile, containerPath, wsState, rel string, mountTargets map[string]struct{}, copyFile func(src, dst string) error) []string {
	src := hostFile
	if IsBindMountpoint(hostFile, mountTargets) {
		deref := filepath.Join(wsState, rel)
		if err := os.MkdirAll(filepath.Dir(deref), 0o755); err == nil {
			cp := copyFile
			if cp == nil {
				cp = copyFileReal
			}
			if err := cp(hostFile, deref); err == nil {
				src = deref
			}
			// copy failure → keep src = hostFile (best-effort direct mount).
		}
	}
	return []string{"-v", src + ":" + containerPath + ":ro"}
}

func copyFileReal(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
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

// fields splits on runs of ASCII whitespace, like Python's str.split() with no
// args (which is what mountinfo parsing uses — field 5 is the mount point).
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
