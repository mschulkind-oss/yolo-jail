package check

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cgd"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// checkRocmEnumeration ports _check_rocm_enumeration: enumerate AMD GPUs via
// rocminfo for the report. Informational only — NEVER a fail.
func (o *Options) checkRocmEnumeration(r *reporter) {
	rocminfoNote := "Optional: rocminfo is only used to list GPU models in this " +
		"check — passthrough itself needs only the device nodes below.  " +
		"To enable enumeration install the rocminfo package (Arch: " +
		"pacman -S rocminfo; other distros: " +
		"https://rocm.docs.amd.com/projects/install-on-linux/)."
	_, hasRocminfo := o.LookPath("rocminfo")
	if !hasRocminfo {
		present := ""
		if _, ok := o.LookPath("rocm-smi"); ok {
			present = " (rocm-smi/amd-smi present)"
		} else if _, ok := o.LookPath("amd-smi"); ok {
			present = " (rocm-smi/amd-smi present)"
		}
		r.warn("rocminfo not found"+present+" — GPU model enumeration skipped", rocminfoNote)
		return
	}
	res := o.Exec([]string{"rocminfo"}, "", nil, 10*time.Second)
	if !res.Ran || res.Timeout {
		msg := "exec failed"
		if res.Timeout {
			msg = "timeout"
		}
		r.warn("rocminfo execution failed — enumeration skipped", msg)
		return
	}
	if res.RC != 0 || strings.TrimSpace(res.Stdout) == "" {
		r.warn("rocminfo ran but reported no GPUs",
			"Check amdgpu driver installation (the device-node checks "+
				"below are the functional gate)")
		return
	}
	foundGPU := false
	pendingName := ""
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.Contains(line, "Marketing Name:") {
			pendingName = strings.TrimSpace(strings.SplitN(line, "Marketing Name:", 2)[1])
		} else if strings.Contains(line, "Device Type:") {
			devType := strings.TrimSpace(strings.SplitN(line, "Device Type:", 2)[1])
			if devType == "GPU" && pendingName != "" {
				r.ok("GPU detected: " + pendingName)
				foundGPU = true
			}
			pendingName = ""
		}
	}
	if !foundGPU {
		r.warn("rocminfo ran but enumerated no GPU agent",
			"Check the amdgpu driver and that the GPU is ROCm-supported")
	}
}

// checkPodmanMachineResources ports _check_podman_machine_resources: surface the
// Podman Machine VM memory and warn if it's below the floor or below the
// workspace's resources.memory request. Best-effort; never gating.
func (o *Options) checkPodmanMachineResources(r *reporter, config *jsonx.OrderedMap) {
	name, memMB, ok := o.podmanMachineMemory()
	if !ok {
		return
	}

	// Compare against the workspace's requested resources.memory if set.
	workspaceFloorMB := int64(-1)
	requestedStr := ""
	if config != nil {
		if resV, _ := config.Get("resources"); resV != nil {
			if resources, ok := resV.(*jsonx.OrderedMap); ok {
				if reqV, _ := resources.Get("memory"); reqV != nil {
					if s, ok := reqV.(string); ok {
						requestedStr = s
						if parsed, ok := cgd.ParseMemoryValue(s); ok {
							workspaceFloorMB = parsed / (1024 * 1024)
						}
					}
				}
			}
		}
	}

	fix := runtime.PodmanMachineResizeHint()

	switch {
	case memMB < runtime.PodmanMachineMemoryFloorMB:
		r.warn(fmt.Sprintf("Podman Machine '%s' memory: %d MB (below %d MB recommended floor)",
			name, memMB, runtime.PodmanMachineMemoryFloorMB),
			fmt.Sprintf("Agent installs (claude, copilot) and `mise install` can OOM at "+
				"this size — claude's first-run native install has been observed "+
				"to take SIGKILL at 2 GB.  %s", fix))
	case workspaceFloorMB >= 0 && int64(memMB) < workspaceFloorMB:
		r.warn(fmt.Sprintf("Podman Machine '%s' memory: %d MB (workspace requests resources.memory=%s)",
			name, memMB, requestedStr),
			fmt.Sprintf("The jail's memory limit is enforced inside the VM, so the VM "+
				"itself needs at least that much.  %s", fix))
	default:
		r.ok(fmt.Sprintf("Podman Machine '%s' memory: %d MB", name, memMB))
	}
}
