package checkcmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// gpuConfig extracts config["gpu"] as a map (or empty), plus enabled/vendor.
func gpuConfig(merged *jsonx.OrderedMap) (enabled bool, vendor string, gpu *jsonx.OrderedMap, mode string) {
	gpu = jsonx.NewOrderedMap()
	if v, _ := merged.Get("gpu"); v != nil {
		if m, ok := v.(*jsonx.OrderedMap); ok {
			gpu = m
		}
	}
	if ev, _ := gpu.Get("enabled"); ev != nil {
		b, _ := ev.(bool)
		enabled = b
	}
	vendor = "nvidia"
	if vv, _ := gpu.Get("vendor"); vv != nil {
		if s, ok := vv.(string); ok {
			vendor = s
		}
	}
	if mv, _ := gpu.Get("mode"); mv != nil {
		mode = asString(mv)
	}
	return
}

// sectionGPUNvidia runs the GPU (NVIDIA) block.
func (o *Options) sectionGPUNvidia(r *reporter, merged *jsonx.OrderedMap) {
	enabled, vendor, _, _ := gpuConfig(merged)
	if !enabled || vendor == "amd" {
		return
	}
	r.section("GPU (NVIDIA)")
	if o.IsMacOS {
		r.warn("GPU passthrough is not supported on macOS",
			"NVIDIA GPU passthrough requires Linux with NVIDIA drivers")
		r.blank()
		return
	}
	if _, ok := o.LookPath("nvidia-smi"); ok {
		res := o.Exec([]string{"nvidia-smi", "--query-gpu=name,driver_version", "--format=csv,noheader"}, "", nil, 10*time.Second)
		if res.Ran && !res.Timeout && res.RC == 0 && strings.TrimSpace(res.Stdout) != "" {
			for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
				r.ok("GPU detected: " + strings.TrimSpace(line))
			}
		} else if !res.Ran || res.Timeout {
			r.fail("nvidia-smi execution failed", "probe failed")
		} else {
			r.fail("nvidia-smi found but no GPUs detected", "Check NVIDIA driver installation")
		}
	} else {
		r.fail("nvidia-smi not found",
			"Install NVIDIA drivers: https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/install-nvidia-driver.html")
	}

	if _, ok := o.LookPath("nvidia-ctk"); ok {
		r.ok("nvidia-ctk found (NVIDIA Container Toolkit)")
	} else {
		r.fail("nvidia-ctk not found",
			"Install: https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html")
	}

	effectiveRuntime, _ := o.runtimeForCheck(merged)
	if effectiveRuntime == "podman" {
		if _, ok := o.LookPath("runc"); ok {
			r.ok("runc found (required for Podman GPU passthrough)")
		} else {
			r.fail("runc not found",
				"GPU passthrough requires runc (CDI fails with crun). "+
					"Install runc: https://github.com/opencontainers/runc/releases")
		}
		cdiFound := ""
		for _, p := range []string{"/etc/cdi/nvidia.yaml", "/var/run/cdi/nvidia.yaml"} {
			if o.PathExists(p) {
				cdiFound = p
				break
			}
		}
		if cdiFound != "" {
			r.ok("CDI spec found for Podman GPU support")
			cdiText := readFileString(cdiFound)
			smiRes := o.Exec([]string{"nvidia-smi", "--query-gpu=driver_version", "--format=csv,noheader"}, "", nil, 10*time.Second)
			if smiRes.Ran && !smiRes.Timeout && smiRes.RC == 0 {
				smiDriver := strings.TrimSpace(firstLine(strings.TrimSpace(smiRes.Stdout)))
				if smiDriver != "" && strings.Contains(cdiText, smiDriver) {
					r.ok("CDI spec matches driver " + smiDriver)
				} else if smiDriver != "" {
					r.warn("CDI spec may be stale (driver is "+smiDriver+")",
						"Regenerate: sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml")
				}
			}
		} else {
			r.fail("No CDI spec found for Podman",
				"Generate with: sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml")
		}
	}
	r.blank()
}

// sectionGPUAmd runs the GPU (AMD/ROCm) block.
func (o *Options) sectionGPUAmd(r *reporter, merged *jsonx.OrderedMap) {
	enabled, vendor, _, mode := gpuConfig(merged)
	if !enabled || vendor != "amd" {
		return
	}
	r.section("GPU (AMD/ROCm)")
	if o.IsMacOS {
		r.warn("ROCm passthrough is not supported on macOS",
			"AMD ROCm GPU passthrough requires Linux with the amdgpu driver")
		r.blank()
		return
	}
	inJail := o.inJail()

	o.checkRocmEnumeration(r)

	if inJail {
		r.ok("Inside jail — amdgpu module check skipped (managed by host)")
	} else if o.PathExists("/sys/module/amdgpu") {
		r.ok("amdgpu kernel module loaded")
	} else {
		r.fail("amdgpu kernel module not loaded",
			"Install amdgpu-dkms and reboot, or `modprobe amdgpu`")
	}

	if inJail {
		r.ok("Inside jail — device-node checks skipped (managed by host)")
	} else {
		o.checkDeviceNode(r, "/dev/kfd")
		renderNodes := o.globRenderNodes()
		if len(renderNodes) > 0 {
			for _, node := range renderNodes {
				o.checkDeviceNode(r, node)
			}
		} else {
			r.fail("no /dev/dri render node present",
				"ROCm needs at least one /dev/dri/renderD* node")
		}
	}

	effectiveRuntime, _ := o.runtimeForCheck(merged)
	if effectiveRuntime == "podman" {
		r.ok("Podman will preserve render/video group via --group-add keep-groups")
	} else if effectiveRuntime == "container" {
		r.warn("Apple Container does not support device passthrough",
			"ROCm passthrough will be ignored on the 'container' runtime")
	}

	if mode == "cdi" {
		cdiFound := ""
		for _, p := range []string{"/etc/cdi/amd.json", "/var/run/cdi/amd.json"} {
			if o.PathExists(p) {
				cdiFound = p
				break
			}
		}
		if cdiFound != "" {
			r.ok("AMD CDI spec found: " + cdiFound)
		} else {
			r.fail("No AMD CDI spec found (mode: cdi)",
				"Generate with: sudo amd-ctk cdi generate "+
					"--output=/etc/cdi/amd.json")
		}
	}
	r.blank()
}

// checkDeviceNode runs the nested _check_node closure (ROCm device nodes).
func (o *Options) checkDeviceNode(r *reporter, node string) {
	if !o.PathExists(node) {
		r.fail(node+" not present", "")
		return
	}
	r.ok("Device node: " + node)
	if o.AccessRW(node) {
		r.ok(node + " is readable and writable by the current user")
		return
	}
	gid, groupName, ok := o.NodeGID(node)
	if !ok {
		r.fail("Could not stat "+node+": stat error", "")
		return
	}
	if o.InUserGroups(gid) {
		r.warn(fmt.Sprintf("User is in group '%s' but %s is not accessible from this process", groupName, node),
			fmt.Sprintf("Log out and back in (or `newgrp %s`) so the new group takes effect", groupName))
	} else {
		r.fail(fmt.Sprintf("%s not accessible; user missing group '%s'", node, groupName),
			fmt.Sprintf("sudo usermod -aG %s $USER && log out / log back in", groupName))
	}
}

// globRenderNodes returns sorted /dev/dri/renderD* nodes.
func (o *Options) globRenderNodes() []string {
	matches, _ := filepath.Glob("/dev/dri/renderD*")
	sort.Strings(matches)
	return matches
}

// sectionKVM runs the KVM Virtualization block.
func (o *Options) sectionKVM(r *reporter, merged *jsonx.OrderedMap) {
	kvmV, _ := merged.Get("kvm")
	if b, ok := kvmV.(bool); !ok || !b {
		return
	}
	r.section("KVM Virtualization")
	if o.inJail() {
		r.ok("Inside jail — kvm checks skipped (managed by host)")
		r.blank()
		return
	}
	if o.IsMacOS {
		r.warn("kvm passthrough is not supported on macOS",
			"Apple hosts use the VZ framework; drop the `kvm` key on mac")
		r.blank()
		return
	}
	const kvmPath = "/dev/kvm"
	if !o.PathExists(kvmPath) {
		r.fail("/dev/kvm not present",
			"Enable virtualization in firmware and `modprobe kvm_intel` "+
				"or `modprobe kvm_amd`")
		r.blank()
		return
	}
	r.ok("Device node: " + kvmPath)
	if o.AccessRW(kvmPath) {
		r.ok("/dev/kvm is readable and writable by the current user")
	} else {
		gid, groupName, ok := o.NodeGID(kvmPath)
		if !ok {
			r.fail("Could not stat /dev/kvm: stat error", "")
		} else if o.InUserGroups(gid) {
			r.warn(fmt.Sprintf("User is in group '%s' but /dev/kvm is not accessible from this process", groupName),
				"Log out and back in (or `newgrp kvm`) so the new group takes effect")
		} else {
			r.fail(fmt.Sprintf("/dev/kvm not accessible; user missing group '%s'", groupName),
				fmt.Sprintf("sudo usermod -aG %s $USER && log out / log back in", groupName))
		}
	}
	effectiveRuntimeKVM, _ := o.runtimeForCheck(merged)
	if effectiveRuntimeKVM == "podman" {
		r.ok("Podman will preserve kvm group via --group-add keep-groups")
	} else if effectiveRuntimeKVM == "container" {
		r.warn("Apple Container does not support device passthrough",
			"kvm: true will be ignored on the 'container' runtime")
	}
	r.blank()
}

func readFileString(p string) string {
	data, err := readFileBytes(p)
	if err != nil {
		return ""
	}
	return string(data)
}
