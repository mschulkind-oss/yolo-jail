package run

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// shouldMountHostNix ports _should_mount_host_nix: decide whether run() should
// bind-mount the host's Nix daemon socket + store. Linux: mount when both paths
// exist and the runtime supports it. macOS: skip by default (the runtime VM
// doesn't share /nix), opt back in via a truthy YOLO_NIX_HOST_DAEMON. Apple
// Container can't share Unix sockets via -v regardless.
func shouldMountHostNix(rt string, nixSocketExists, nixStoreExists, isMacOS bool, optInEnv string) bool {
	if !(nixSocketExists && nixStoreExists) {
		return false
	}
	if rt == "container" {
		return false
	}
	if !isMacOS {
		return true
	}
	switch strings.ToLower(optInEnv) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// gpuHostAvailable ports _gpu_host_available: probe whether NVIDIA GPU
// passthrough will work. Returns (ok, reason); reason is a one-line phrase when
// not ok.
func (o *Options) gpuHostAvailable(rt string) (bool, string) {
	if o.IsMacOS || rt == "container" {
		return false, "runtime does not support NVIDIA passthrough"
	}
	if rt != "podman" {
		return false, "unsupported runtime: " + rt
	}
	smi, ok := o.LookPath("nvidia-smi")
	if !ok {
		return false, "nvidia-smi not found on host"
	}
	res := o.Exec([]string{smi, "-L"}, "", nil, 5*time.Second)
	if !res.Ran {
		return false, "nvidia-smi failed to run"
	}
	if res.Timeout || res.RC != 0 {
		return false, "nvidia-smi reported no GPUs"
	}
	if !o.PathExists("/etc/cdi/nvidia.yaml") && !o.PathExists("/var/run/cdi/nvidia.yaml") {
		return false, "no CDI spec at /etc/cdi/nvidia.yaml"
	}
	return true, ""
}

// rocmHostAvailable ports _rocm_host_available: probe whether AMD ROCm
// passthrough will work (amdgpu module + /dev/kfd + a render node; functional
// rocminfo when present).
func (o *Options) rocmHostAvailable(rt string) (bool, string) {
	if o.IsMacOS || rt == "container" {
		return false, "runtime does not support ROCm/AMD passthrough"
	}
	if rt != "podman" {
		return false, "unsupported runtime: " + rt
	}
	if !o.PathExists("/sys/module/amdgpu") {
		return false, "amdgpu kernel module not loaded"
	}
	if !o.PathExists("/dev/kfd") {
		return false, "no /dev/kfd on host"
	}
	if !hasRenderNode("/dev/dri") {
		return false, "no /dev/dri render node on host"
	}
	if rocminfo, ok := o.LookPath("rocminfo"); ok {
		res := o.Exec([]string{rocminfo}, "", nil, 5*time.Second)
		if !res.Ran {
			return false, "rocminfo failed to run"
		}
		if res.Timeout || res.RC != 0 {
			return false, "rocminfo reported no GPUs"
		}
	}
	return true, ""
}

// hasRenderNode reports whether driDir has any renderD* node (glob renderD*).
func hasRenderNode(driDir string) bool {
	matches, err := filepath.Glob(filepath.Join(driDir, "renderD*"))
	return err == nil && len(matches) > 0
}

var _ = os.Stat
