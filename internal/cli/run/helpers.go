package run

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sha1Hex8 returns the per-jail 8-hex key for a container name — the hash that
// names the host-services dir and each fronted daemon's upstream socket.
//
// It delegates to paths rather than recomputing: this repo carried three
// hand-copied implementations of it (here, internal/cli/check, internal/prune) and
// `yolo prune`'s sweep matches a pid file back to a live container THROUGH this
// value, so a divergence would silently either orphan or reap the wrong jail's
// files.
func sha1Hex8(s string) string { return paths.JailShortHash(s) }

// hostServiceSocketsDir returns this jail's host-side endpoint-file directory,
// /tmp/yolo-host-services-<8hex>. See paths.HostServicesDir.
func hostServiceSocketsDir(cname string, isMacOS bool) string {
	return paths.HostServicesDir(cname, isMacOS)
}

// mkdirHostServicesDir creates the per-jail host-services dir 0700, tightening it
// when an older yolo already created it looser.
//
// 0700 IS LOAD-BEARING, not tidiness. The directory holds each service's published
// endpoint file, and an endpoint file carries that service's per-jail bearer token —
// so svcendpoint REFUSES to publish into a group/world-accessible directory and the
// daemon dies at spawn instead. The path is fully deterministic and sits under a
// world-writable /tmp, which is the whole reason that refusal exists.
//
// The two branches are exclusive on purpose, so each mode has exactly ONE author:
//
//   - a dir that already exists with group/world bits is CHMODed. This is the only
//     thing carrying an existing host across the change — MkdirAll leaves an
//     existing directory's mode alone, and every host that ran an older yolo has a
//     0755 one. On a directory we do not own the Chmod fails, publication then fails
//     closed, and that is the intended outcome rather than a bug.
//   - otherwise it is CREATED 0700, never created loose and then tightened. Creating
//     it 0755 first would leave a window in which the credential directory is
//     world-readable, and a window is not something a mode assertion can see later.
func mkdirHostServicesDir(dir string) {
	if st, err := os.Lstat(dir); err == nil && st.IsDir() && st.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(dir, 0o700)
		return
	}
	_ = os.MkdirAll(dir, 0o700)
}

var nonAlnumRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

// serviceEnvSlug sanitizes a service name into the middle of its env var name.
func serviceEnvSlug(serviceName string) string {
	s := nonAlnumRe.ReplaceAllString(serviceName, "_")
	return strings.ToUpper(strings.Trim(s, "_"))
}

// hostServiceEnvVar returns YOLO_SERVICE_<SANITIZED>_ENDPOINT — the variable a
// loopback-TLS service's clients read to find its endpoint FILE.
//
// The spelling comes from internal/paths, where the producer/consumer contract is
// documented: yolo-ps and the OAuth terminator read exactly this name, and a drift
// between the two halves is what once silently disabled the cgroup delegate in
// every jail.
func hostServiceEnvVar(serviceName string) string {
	return paths.ServiceEnvVarPrefix + serviceEnvSlug(serviceName) + paths.ServiceEnvVarSuffix
}

// hostServiceSocketEnvVar returns YOLO_SERVICE_<SANITIZED>_SOCKET — the RETIRING
// spelling, emitted only for a service still on unix-socket.
//
// The two names are kept apart rather than one being renamed globally, because the
// name has to describe the VALUE: a service whose variable holds a socket path must
// not advertise an endpoint file, and a service whose variable holds an endpoint
// file must not advertise a socket. Emitting both for one service is what the
// rename exists to avoid — a stale baked client reading an ABSENT variable hits its
// own clear "not wired up in this jail" path, where one reading a same-named
// variable whose value is no longer a socket would dial a regular file and report
// something obscure. This function disappears with the last unix-socket service.
func hostServiceSocketEnvVar(serviceName string) string {
	return paths.ServiceEnvVarPrefix + serviceEnvSlug(serviceName) + "_SOCKET"
}

// hostServiceEndpointPath returns the IN-JAIL path of a service's endpoint file.
//
// It is always a PATH and never an address. The address lives inside the file, so
// it can change without relaunching the jail — whose environment is frozen at
// container start while the host side re-ensures its daemons on every later attach.
func hostServiceEndpointPath(serviceName string) string {
	return paths.JailHostServicesDir + "/" + serviceName + paths.ServiceEndpointExt
}

// acMaterialize copies src into ws_state/target_rel for Apple Container. is_dir=false here
// (all callers pass files).
//
// ⚠ THE COPY IS NOW A CHOICE, NOT A NECESSITY, and the citation it used to carry was false.
// This said "single-file mounts trip apple/container#1089". MEASURED 2026-09-14 on macOS 26.5
// arm64, `container` 1.1.0: a single REGULAR-FILE bind arrives intact, propagates writes to
// the host, and honors `:ro` (integration/applecontainer_test.go,
// TestAppleContainerBindsASingleFile). So #1089 does not hold on that release.
//
// The copy is KEPT anyway, deliberately. It works on EVERY version with no version gate to
// get wrong, and the failure mode of guessing wrong in the other direction is the one this
// codebase keeps paying for: a bind that does not arrive does not ERROR, so the surface
// composes from its defaults layer and the user's own file silently vanishes from the
// composition while the disclosure still says it was read. Every consumer here reads its file
// at BOOT, so a snapshot is as good as a bind — which is what makes retaining the copy cheap
// rather than a compromise. Revisit only with a measured version floor, the way
// acROBindsFloor is done (internal/cli/run/backendcaps.go).
func acMaterialize(src, targetRel, wsState string) {
	dst := filepath.Join(wsState, targetRel)
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	_ = copyFile2(src, dst)
}

// acMaterializeTree is acMaterialize's DIRECTORY twin: it copies a whole host tree into
// ws_state/targetRel, REPLACING whatever the previous launch left there, so Apple
// Container's single `ws_state:/home/agent` bind carries it into the jail.
//
// WHY A COPY AND NOT A BIND, since Apple Container nests directory mounts perfectly well
// (appleContainerBaseMounts puts GlobalCache at /home/agent/.cache, and packFilesMountArgs
// binds a `files` directory). Because this backend ACCEPTS `-v src:dest:ro` and IGNORES
// the suffix (roBindsUnsupported states the measurement). For the staged pack tree that
// is not a cosmetic downgrade: the tree a bind would expose is the launcher's own
// AGENTS_DIR/<cname>/packs, which the HOST reads on the next launch to decide host-file
// grants and mounts — so a writable bind is precisely the "an agent that could rewrite a
// manifest could grant its own pack a host file on the next boot" escalation that the
// podman arm's `:ro` exists to prevent. A copy leaves the host's own tree untouched.
//
// REPLACE, NEVER MERGE, which is the same rule macosuser.StagePackCommands states for the
// same content on the other no-mounts backend: a pack the user dropped from `packs` must
// stop rendering, and a copy over a live directory would leave the union of two launches
// — the exact bug pruneDroppedPackStaging exists to prevent on the host side.
//
// FAIL-CLOSED ON A PARTIAL TREE. copyTree stops at its first error, and a half-copied
// pack root is the worst of the three outcomes: the jail comes up rendering SOME packs
// and looking provisioned. The destination is therefore removed again, so the caller's
// only two states are "the whole tree arrived" and "nothing did, loudly".
func acMaterializeTree(src, targetRel, wsState string) error {
	dst := filepath.Join(wsState, targetRel)
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := copyTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return nil
}

func numCPU() int { return runtime.NumCPU() }

// appleContainerDefaultMemory returns the AC default memory:
// half of host memory, min 4 GB, formatted "<N>g"; "8g" on any probe failure.
func (o *Options) appleContainerDefaultMemory() string {
	var hostMemBytes int64
	if o.IsMacOS {
		res := o.Exec([]string{"sysctl", "-n", "hw.memsize"}, "", nil, 5*time.Second)
		if !res.Ran || res.Timeout || res.RC != 0 {
			return "8g"
		}
		n, err := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
		if err != nil {
			return "8g"
		}
		hostMemBytes = n
	} else {
		n, ok := sysconfPhysMem()
		if !ok {
			return "8g"
		}
		hostMemBytes = n
	}
	const gib = 1024 * 1024 * 1024
	defaultMem := hostMemBytes / 2
	if 4*gib > defaultMem {
		defaultMem = 4 * gib
	}
	return strconv.FormatInt(defaultMem/gib, 10) + "g"
}

// gpuArgs builds the GPU passthrough args: memlock ulimit, then
// vendor-specific device + env flags.
func (o *Options) gpuArgs(cfg *jsonx.OrderedMap, rt string, gpuEnabled bool, gpuVendor string) []string {
	if !gpuEnabled {
		return nil
	}
	out := o.pr(o.Stdout)
	var args []string

	// memlock ulimit (clamp to the host hard cap, or -1:-1 when unlimited).
	if hard, unlimited := hostHardMemlock(); unlimited {
		args = append(args, "--ulimit", "memlock=-1:-1")
	} else {
		args = append(args, "--ulimit", "memlock="+strconv.FormatInt(hard, 10)+":"+strconv.FormatInt(hard, 10))
	}

	gpuSec := cfgMap(cfg, "gpu")
	if gpuVendor == "nvidia" {
		gpuDevices := mapStrOr(gpuSec, "devices", "all")
		gpuCaps := mapStrOr(gpuSec, "capabilities", "compute,utility")
		if gpuDevices == "all" {
			args = append(args, "--device", "nvidia.com/gpu=all")
		} else {
			for _, idx := range strings.Split(gpuDevices, ",") {
				args = append(args, "--device", "nvidia.com/gpu="+strings.TrimSpace(idx))
			}
		}
		args = append(args,
			"-e", "NVIDIA_VISIBLE_DEVICES="+gpuDevices,
			"-e", "NVIDIA_DRIVER_CAPABILITIES="+gpuCaps)
		out.print("[dim]GPU passthrough: devices=" + gpuDevices + ", capabilities=" + gpuCaps + "[/dim]")
		return args
	}
	if gpuVendor == "amd" {
		gpuDevices := mapStrOr(gpuSec, "devices", "all")
		gpuMode := mapStrOr(gpuSec, "mode", "devices")
		if gpuMode == "cdi" {
			if gpuDevices == "all" {
				args = append(args, "--device", "amd.com/gpu=all")
			} else {
				for _, idx := range strings.Split(gpuDevices, ",") {
					args = append(args, "--device", "amd.com/gpu="+strings.TrimSpace(idx))
				}
			}
		} else {
			if o.PathExists("/dev/kfd") {
				args = append(args, "--device", "/dev/kfd")
			}
			if gpuDevices == "all" {
				args = append(args, "--device", "/dev/dri")
			} else {
				for _, idx := range strings.Split(gpuDevices, ",") {
					if n, err := strconv.Atoi(strings.TrimSpace(idx)); err == nil {
						node := "/dev/dri/renderD" + strconv.Itoa(128+n)
						if o.PathExists(node) {
							args = append(args, "--device", node)
						}
					}
				}
			}
		}
		if rt == "podman" {
			args = append(args, "--group-add", "keep-groups")
		}
		if gpuDevices != "all" {
			args = append(args,
				"-e", "ROCR_VISIBLE_DEVICES="+gpuDevices,
				"-e", "HIP_VISIBLE_DEVICES="+gpuDevices)
		}
		if gfx := mapStr(gpuSec, "hsa_override_gfx_version"); gfx != "" {
			args = append(args, "-e", "HSA_OVERRIDE_GFX_VERSION="+gfx)
		}
		if mapTrue(gpuSec, "seccomp_unconfined") {
			args = append(args, "--security-opt", "seccomp=unconfined")
		}
		if mapTrue(gpuSec, "vaapi") {
			args = append(args, "-e", "LIBVA_DRIVERS_PATH=/lib/dri:/usr/lib/dri")
		}
		vaapiSuffix := ""
		if mapTrue(gpuSec, "vaapi") {
			vaapiSuffix = ", vaapi"
		}
		out.print("[dim]ROCm passthrough (mode=" + gpuMode + "): devices=" + gpuDevices + vaapiSuffix + "[/dim]")
		return args
	}
	return args
}
