package macosuser

import (
	"path"
	"regexp"
	"strings"
)

// DeviceRefusal is one raw-path `devices` entry this backend does not carve out: the entry
// as written, why, and the next step the launch's warning names.
type DeviceRefusal struct {
	Entry  string
	Reason string
	Next   string
}

// rawDeviceRe mirrors the profile's raw-device deny (#seatbelt-test-id:raw-device-deny#): a
// declared raw disk or bpf node is NOT re-opened, so that block's "never" stays true.
var rawDeviceRe = regexp.MustCompile(`^/(private/)?dev/(r?disk|bpf)`)

// DeviceIoctlPaths classifies config.devices' raw-path entries for this backend: the device
// nodes whose control calls (`file-ioctl`) the profile re-allows, and the entries it refuses.
//
// ONE CLASSIFIER, read by the profile (deviceIoctlAllow) and by the launch's disclosure
// (run.noteMacosUserPlatformGaps), so what the user is told and what the kernel is handed are
// never two judgements.
//
// file-ioctl ONLY, because that is the one thing the profile takes away from a device node:
// reads are under `(allow default)` and /dev is in the writable set (profileWritableRoots), so
// an `open` of a serial port already succeeds and every tcsetattr on it fails. A read or write
// allow per entry would instead be a hole: an entry is a free string, and a write allow
// placed after the readonly denies would re-open whatever path it names.
//
// Resolved for the reason the workspace is (BuildRunPlanWithDaemons): the kernel resolves
// symlinks before the policy is consulted. An entry absent at launch is KEPT, because a USB
// serial adapter's node exists only while it is plugged in, and a literal that matches nothing
// widens nothing. Duplicates collapse to the first.
func DeviceIoctlPaths(entries []string) (allowed []string, refused []DeviceRefusal) {
	seen := map[string]bool{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		p := path.Clean(resolvePathAbs(e))
		switch {
		case !strings.HasPrefix(p, "/dev/"):
			refused = append(refused, DeviceRefusal{Entry: e,
				Reason: "it is not a device node under /dev",
				Next: "name the device's node under /dev instead (a USB serial adapter is " +
					"/dev/cu.*, and `ls /dev/cu.*` lists the ones plugged in)"})
		case rawDeviceRe.MatchString(p):
			refused = append(refused, DeviceRefusal{Entry: e,
				Reason: "raw disks and packet capture stay denied on this backend",
				Next: "remove the entry, or run this work in a podman jail on Linux, which " +
					"passes a raw device through"})
		case !seen[p]:
			seen[p] = true
			allowed = append(allowed, p)
		}
	}
	return allowed, refused
}

// deviceIoctlAllow renders the declared device nodes' file-ioctl re-allow, or "" when there
// are none, so a launch that declares no device gets the profile it always got. It is placed
// LAST, after the ioctl deny it overrides, and it names file-ioctl alone, so it re-opens
// nothing but the control calls of the nodes it lists.
func deviceIoctlAllow(devices []string) string {
	allowed, _ := DeviceIoctlPaths(devices)
	if len(allowed) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range allowed {
		b.WriteString("    (literal " + sbplStr(p) + ")\n")
	}
	return ";; --- config.devices: each declared device node's control calls (a serial\n" +
		";;     port's tcsetattr is an ioctl).  ioctl ONLY: reads and /dev writes are\n" +
		";;     already allowed.  Must follow the ioctl deny — last match wins. ---\n" +
		";; #seatbelt-test-id:device-ioctl-allow#\n" +
		"(allow file-ioctl\n" + strings.TrimSuffix(b.String(), "\n") + ")\n"
}
