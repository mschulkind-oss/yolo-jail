package runcmd

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agentsmd"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Broker singleton constants (mirror loopholes_runtime.py). Kept local — the
// broker lifecycle lives in the run wiring.
const (
	brokerSingletonSocket = "/tmp/yolo-claude-oauth-broker.sock"
	brokerLoopholeName    = "claude-oauth-broker"
)

// sha1Hex8 returns the first 8 hex chars of sha1(s) — the per-jail hash keying
// the sockets dir + relay pid/lock files.
func sha1Hex8(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// hostServiceSocketsDir ports _host_service_sockets_dir: /tmp/yolo-host-services-<8hex>
// keyed by sha1(cname)[:8]. macOS resolves /tmp → /private/tmp.
func hostServiceSocketsDir(cname string, isMacOS bool) string {
	base := "/tmp"
	if isMacOS {
		if r, err := filepath.EvalSymlinks(base); err == nil {
			base = r
		}
	}
	return filepath.Join(base, "yolo-host-services-"+sha1Hex8(cname))
}

var nonAlnumRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

// hostServiceEnvVar ports _host_service_env_var: YOLO_SERVICE_<SANITIZED>_SOCKET.
func hostServiceEnvVar(serviceName string) string {
	s := nonAlnumRe.ReplaceAllString(serviceName, "_")
	s = strings.Trim(s, "_")
	return "YOLO_SERVICE_" + strings.ToUpper(s) + "_SOCKET"
}

// workspaceIsYoloSourceTree delegates to the ported agentsmd helper.
func workspaceIsYoloSourceTree(workspace string) bool {
	return agentsmd.WorkspaceIsYoloSourceTree(workspace)
}

// acMaterialize ports _ac_materialize_under_ws_state: copy src into
// ws_state/target_rel for Apple Container (single-file mounts trip
// apple/container#1089). is_dir=false here (all callers pass files).
func acMaterialize(src, targetRel, wsState string) {
	dst := filepath.Join(wsState, targetRel)
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	_ = copyFile2(src, dst)
}

// numCPU mirrors multiprocessing.cpu_count().
func numCPU() int { return runtime.NumCPU() }

// appleContainerDefaultMemory runs the AC default-memory block (2616-2634):
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

// gpuArgs runs the GPU passthrough emission (2452-2574): memlock ulimit, then
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
