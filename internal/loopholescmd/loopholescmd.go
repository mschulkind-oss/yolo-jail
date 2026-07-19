// Package loopholescmd implements the `yolo loopholes {list,status,enable,
// disable}` command group. It inspects and toggles host-side loopholes. The
// discovery/doctor/set-enabled engines are in internal/loopholes; this is the
// thin command body behind injectable seams.
package loopholescmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Deps are the injectable seams: Out/Err writers, the workspace cwd, and the
// in-jail flag (YOLO_VERSION set). LoadUserConfig / LoadWorkspaceConfig return
// the merged config maps (nil on any error, mirroring the Python try/except).
type Deps struct {
	Out, Err            io.Writer
	Cwd                 string
	InJail              bool
	LoadUserConfig      func() *jsonx.OrderedMap
	LoadWorkspaceConfig func(cwd string) *jsonx.OrderedMap
}

// RealDeps returns Deps backed by the real filesystem/config loaders.
func RealDeps() Deps {
	cwd, _ := os.Getwd()
	return Deps{
		Out:    os.Stdout,
		Err:    os.Stderr,
		Cwd:    cwd,
		InJail: os.Getenv("YOLO_VERSION") != "",
		LoadUserConfig: func() *jsonx.OrderedMap {
			m, err := config.LoadJSONCFile(paths.UserConfigPath(), "user config", false, nil)
			if err != nil {
				return nil
			}
			return m
		},
		LoadWorkspaceConfig: func(cwd string) *jsonx.OrderedMap {
			m, err := config.LoadWorkspaceConfig(cwd, false, nil)
			if err != nil {
				return nil
			}
			return m
		},
	}
}

// loopholesWithConfig discovers loopholes including host_services synthesized
// from the merged user+workspace config `loopholes:` block. Mirrors
// _loopholes_with_config: user then workspace, later wins on key collision.
func loopholesWithConfig(deps Deps, includeDisabled bool) []*loopholes.Loophole {
	merged := jsonx.NewOrderedMap()
	for _, cfg := range []*jsonx.OrderedMap{deps.LoadUserConfig(), deps.LoadWorkspaceConfig(deps.Cwd)} {
		if cfg == nil {
			continue
		}
		if v, ok := cfg.Get("loopholes"); ok {
			if lh, ok := v.(*jsonx.OrderedMap); ok {
				for _, k := range lh.Keys() {
					val, _ := lh.Get(k)
					merged.Set(k, val)
				}
			}
		}
	}
	// include_bundled defaults to true in Python's discover_loopholes; the Go
	// DiscoverOptions zero value is false, so set it explicitly (matches
	// loopholes.NewResolver, which does the same).
	return loopholes.Discover(loopholes.DiscoverOptions{
		IncludeDisabled: includeDisabled,
		IncludeBundled:  true,
		LoopholesConfig: merged,
	})
}

// List runs `yolo loopholes list`. Mirrors loopholes_list byte-for-byte.
func List(deps Deps) int {
	all := loopholesWithConfig(deps, true)
	if len(all) == 0 {
		fmt.Fprintln(deps.Out, "No loopholes installed.")
		fmt.Fprintf(deps.Out, "  • bundled: %s\n", loopholes.BundledLoopholesDir())
		fmt.Fprintf(deps.Out, "  • user: %s\n", loopholes.UserLoopholesDir())
		fmt.Fprintln(deps.Out, "  • workspace: yolo-jail.jsonc loopholes: block")
		return 0
	}
	for _, lh := range all {
		var label string
		if !lh.Enabled {
			label = "disabled"
		} else if reason, ok := lh.InactiveReason(); ok {
			label = "inactive (" + reason + ")"
		} else {
			label = "active"
		}
		var extra string
		if lh.Transport == "tls-intercept" && len(lh.Intercepts) > 0 {
			hosts := make([]string, len(lh.Intercepts))
			for i, ic := range lh.Intercepts {
				hosts[i] = ic.Host
			}
			extra = "intercepts=[" + strings.Join(hosts, ", ") + "]"
		} else {
			extra = "transport=" + lh.Transport
		}
		tags := lh.Source + "/" + lh.Transport + "/" + lh.Lifecycle
		fmt.Fprintf(deps.Out, "  %-36s  %s  (%s)  %s\n", label, lh.Name, tags, extra)
		if lh.Description != "" {
			fmt.Fprintf(deps.Out, "      %s\n", lh.Description)
		}
	}
	return 0
}

// Status runs `yolo loopholes status` (each loophole's doctor_cmd). Mirrors
// loopholes_status, including the in-jail short-circuit.
func Status(deps Deps) int {
	if deps.InJail {
		fmt.Fprintln(deps.Out, "Inside jail — doctor checks are host-side.  From the host: yolo loopholes status")
		return 0
	}
	all := loopholesWithConfig(deps, true)
	if len(all) == 0 {
		fmt.Fprintln(deps.Out, "No loopholes installed.")
		return 0
	}
	for _, r := range loopholes.RunDoctorChecks(all, 10*time.Second) {
		var prefix string
		switch {
		case !r.Loophole.Enabled:
			prefix = "disabled"
		case !r.Loophole.RequirementsMet():
			prefix = "inactive"
		case r.RC != nil && *r.RC == 0:
			prefix = "ok"
		case r.RC == nil:
			prefix = "no-check"
		default:
			prefix = "fail"
		}
		fmt.Fprintf(deps.Out, "  [%s] %s  rc=%s\n", prefix, r.Loophole.Name, rcStr(r.RC))
		if r.Output != "" {
			for _, line := range strings.Split(r.Output, "\n") {
				fmt.Fprintf(deps.Out, "      %s\n", line)
			}
		}
	}
	return 0
}

// SetEnabled runs `yolo loopholes enable|disable <name>`. Mirrors
// loopholes_enable / loopholes_disable: only user-installed loopholes are
// toggleable (a missing user manifest → the exact stderr message + exit 1).
func SetEnabled(deps Deps, name string, enabled bool) int {
	path := filepath.Join(loopholes.UserLoopholesDir(), name)
	if fi, err := os.Stat(filepath.Join(path, "manifest.jsonc")); err != nil || fi.IsDir() {
		fmt.Fprintf(deps.Err,
			"No user-installed loophole at %s.\n"+
				"For bundled or workspace-inline loopholes, edit the workspace "+
				"yolo-jail.jsonc (loopholes.<name>.enabled).\n", path)
		return 1
	}
	if err := loopholes.SetEnabled(path, enabled); err != nil {
		fmt.Fprintf(deps.Err, "%v\n", err)
		return 1
	}
	word := "enabled"
	if !enabled {
		word = "disabled"
	}
	fmt.Fprintf(deps.Out, "%s %s\n", word, name)
	return 0
}

// rcStr renders an *int rc the way Python's f"rc={r.returncode}" does: the int,
// or "None" when nil.
func rcStr(rc *int) string {
	if rc == nil {
		return "None"
	}
	return fmt.Sprintf("%d", *rc)
}
