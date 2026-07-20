package check

import (
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// sectionMacOSPlatform runs the macOS Platform block (only runs on macOS).
// config is loaded lazily for the podman-machine-resources sub-check via
// load_config(strict=False) inside _check_podman_machine_resources.
func (o *Options) sectionMacOSPlatform(r *reporter, _ *jsonx.OrderedMap) {
	r.section("macOS Platform")
	r.ok("Architecture: " + o.Machine)

	if _, ok := o.LookPath("podman"); ok {
		res := o.Exec([]string{"podman", "machine", "info"}, "", nil, 5*time.Second)
		if res.Ran && !res.Timeout && res.RC == 0 {
			r.ok("Podman Machine: available")
			o.checkPodmanMachineResources(r, o.loadWorkspaceConfigLoose())
		} else if !res.Ran || res.Timeout {
			r.warn("podman: probe failed", "")
		} else {
			r.warn("Podman Machine: not configured", "")
		}
	}

	_, hasContainer := o.LookPath("container")
	if hasContainer {
		res := o.Exec([]string{"container", "system", "status"}, "", nil, 5*time.Second)
		if res.Ran && !res.Timeout && res.RC == 0 {
			r.ok("Apple Container CLI: available")
			if strings.Contains(strings.ToLower(res.Stdout), "running") {
				r.ok("Apple Container system: running")
			} else {
				r.warn("Apple Container system not running", "Start with: container system start")
			}
		} else if !res.Ran || res.Timeout {
			r.warn("Apple Container CLI: probe failed", "")
		} else {
			r.warn("Apple Container: installed but not started", "Start with: container system start")
		}
	}

	if hasContainer {
		if _, ok := o.LookPath("skopeo"); ok {
			r.ok("skopeo: available (OCI image conversion, no daemon needed)")
		} else if _, ok := o.LookPath("podman"); ok {
			r.ok("OCI conversion: via podman (skopeo recommended: brew install skopeo)")
		} else {
			r.warn("No OCI conversion tool for Apple Container",
				"Install skopeo (recommended): brew install skopeo")
		}
	}

	// Nix store volume check.
	if o.PathExists("/nix") {
		res := o.Exec([]string{"mount"}, "", nil, 5*time.Second)
		if res.Ran && !res.Timeout {
			var nixLines []string
			for _, line := range strings.Split(res.Stdout, "\n") {
				if strings.Contains(line, " /nix ") || strings.Contains(line, " on /nix") {
					nixLines = append(nixLines, line)
				}
			}
			if len(nixLines) > 0 {
				if strings.Contains(strings.ToLower(nixLines[0]), "apfs") {
					r.ok("Nix store: mounted (APFS volume)")
				} else {
					r.ok("Nix store: mounted")
				}
			} else {
				r.warn("Nix store: /nix exists but mount not detected",
					"Check /etc/synthetic.conf and Disk Utility")
			}
		} else {
			r.ok("Nix store: /nix exists")
		}
	} else {
		r.fail("Nix store: /nix not found", "Reinstall Nix or check /etc/synthetic.conf")
	}
	r.blank()
}

// call inside _check_podman_machine_resources. Any error → empty map.
func (o *Options) loadWorkspaceConfigLoose() *jsonx.OrderedMap {
	cfg := loadConfigLoose(o.Workspace)
	if cfg == nil {
		return jsonx.NewOrderedMap()
	}
	return cfg
}
