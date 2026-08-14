package loopholes

// The `yolo loopholes {list,status,enable,disable}` command group. It inspects
// and toggles host-side loopholes. The discovery/doctor/set-enabled engines are
// alongside in this package; this is the thin command body behind injectable
// seams.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Deps are the injectable seams: Out/Err writers, the workspace cwd, and the
// in-jail flag (YOLO_VERSION set). LoadUserConfig / LoadWorkspaceConfig return
// the merged config maps (nil on any error).
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
// from the merged user+workspace config `loopholes:` block: user then
// workspace, later wins on key collision.
//
// Entries are VALIDATED before they are honored (loophole-packaging.md item
// 1b): these commands used to read the config with no schema pass at all, so a
// workspace entry `yolo check` rejects — e.g. a doctor_cmd with no command,
// host execution from two read-only-looking commands — was still listed, and
// Status executed it. An entry that fails validation is dropped with a printed
// reason and never reaches RunDoctorChecks. The scope rules follow the launch
// path's asymmetry: a workspace-scope violation refuses the entry on the host
// and is only a warning in-jail, where the entry stays honored.
// It returns a Set rather than a slice, so the doctor path downstream gets the ORIGIN
// GATE with the records (census site 5, docs/design/loophole-packaging.md §5.1): `status`
// executes each loophole's doctor_cmd, and this command has no pack resolution of its own
// — it reads what the process recorded through NewHostSet. On a `yolo loopholes` process
// nothing records anything, so a pack loophole is absent rather than executed, which is
// the fail-safe direction and is stated on packModules.
func loopholesWithConfig(deps Deps, includeDisabled bool) Set {
	// The same file-backed set config.ValidateConfig resolves names against.
	known, _ := NewResolver().Known()

	scopes := []struct {
		cfg           *jsonx.OrderedMap
		fromWorkspace bool
		src           string
	}{
		{deps.LoadUserConfig(), false, paths.UserConfigPath()},
		// deps.LoadWorkspaceConfig collapses yolo-jail.jsonc and
		// yolo-jail.local.jsonc, so the merged block cannot say which file an
		// entry came from and the refusal used to blame the tracked file for a
		// violation living in the local one. config.WorkspaceLoopholeOrigins
		// re-reads the two files (the same re-read the launch validator does) and
		// names the actual origin per entry; the collapsed map still supplies the
		// VALUES, so the injected-config seam these commands are tested through
		// keeps working — with no real files it simply finds no origins and falls
		// back to the tracked name.
		{deps.LoadWorkspaceConfig(deps.Cwd), true, filepath.Join(deps.Cwd, config.WorkspaceConfigName)},
	}
	wsOrigins := config.WorkspaceLoopholeOrigins(deps.Cwd)
	userInline := map[string]bool{}
	merged := jsonx.NewOrderedMap()
	for _, sc := range scopes {
		if sc.cfg == nil {
			continue
		}
		v, ok := sc.cfg.Get("loopholes")
		if !ok {
			continue
		}
		lh, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		for _, k := range lh.Keys() {
			val, _ := lh.Get(k)
			var info *config.LoopholeInfo
			if ki, isKnown := known[k]; isKnown {
				kiCopy := ki
				info = &kiCopy
			}
			src := sc.src
			if sc.fromWorkspace {
				if files := wsOrigins[k]; len(files) > 0 {
					// "A or B" when both files contributed: both are
					// agent-editable, and which key came from which is the
					// per-file question only the launch validator answers.
					src = strings.Join(files, " or ")
				}
			}
			problems := config.LoopholeEntryErrors(k, val, info, userInline[k],
				sc.fromWorkspace, deps.InJail, src, deps.Cwd)
			if len(problems) > 0 {
				fmt.Fprintf(deps.Err, "Ignoring loopholes.%s (from %s):\n", k, src)
				for _, p := range problems {
					fmt.Fprintf(deps.Err, "  • %s\n", p)
				}
				continue
			}
			if !sc.fromWorkspace {
				if m, isMap := val.(*jsonx.OrderedMap); isMap {
					if _, hasCmd := m.Get("command"); hasCmd {
						userInline[k] = true
					}
				}
			}
			merged.Set(k, val)
		}
	}
	// NewHostSet, not a hand-built DiscoverOptions: it is the one constructor that
	// composes bundled + pack + user + config, so this command cannot come to disagree
	// with the launch path about what this machine has. It always builds the
	// include-disabled superset; includeDisabled selects the VIEW below.
	set := NewHostSet(merged)
	if includeDisabled {
		return set
	}
	return SetOf(set.Enabled()).withGate(set)
}

// List runs `yolo loopholes list`.
func List(deps Deps) int {
	all := loopholesWithConfig(deps, true).All()
	if len(all) == 0 {
		fmt.Fprintln(deps.Out, "No loopholes installed.")
		fmt.Fprintf(deps.Out, "  • bundled: %s\n", BundledLoopholesDir())
		fmt.Fprintf(deps.Out, "  • user: %s\n", UserLoopholesDir())
		fmt.Fprintf(deps.Out, "  • config: loopholes: block in %s "+
			"(install-shaped keys are user-scope only; a workspace "+
			"yolo-jail.jsonc may set enabled/jail_env)\n", paths.UserConfigPath())
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
		// Interception is a property of the intercept list, not of the transport
		// string — see RuntimeArgsFor. The `transport=` fallback still prints for
		// every non-intercepting loophole, which is what makes the active transport
		// visible without asking (loophole-transport.md OQ-T2).
		var extra string
		if len(lh.Intercepts) > 0 {
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

// Status runs `yolo loopholes status` (each loophole's doctor_cmd), including
// the in-jail short-circuit.
func Status(deps Deps) int {
	if deps.InJail {
		fmt.Fprintln(deps.Out, "Inside jail — doctor checks are host-side.  From the host: yolo loopholes status")
		return 0
	}
	set := loopholesWithConfig(deps, true)
	all := set.All()
	if len(all) == 0 {
		fmt.Fprintln(deps.Out, "No loopholes installed.")
		return 0
	}
	// THE SET's doctor runner, not the package-level one. `status` runs each loophole's
	// doctor_cmd — host code — and this command is one users treat as read-only preflight,
	// so a pack-shipped record runs only when the origin gate was evaluated AND passed
	// (docs/design/loophole-packaging.md §5.1). A withheld one is REPORTED, with the
	// reason, rather than skipped: a skip is indistinguishable from `no-check`, which
	// would read as "this loophole declares no self-check" — the wrong story entirely.
	for _, r := range set.RunDoctorChecks(all, 10*time.Second) {
		var prefix string
		switch {
		case !r.Loophole.Enabled:
			prefix = "disabled"
		case !set.MayRunHostCode(r.Loophole):
			prefix = "unapproved"
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

// CmdSetEnabled runs `yolo loopholes enable|disable <name>`. Only
// user-installed loopholes are toggleable (a missing user manifest → the exact
// stderr message + exit 1).
//
// The fallback instruction points at the USER config (loophole-packaging.md
// §5.2): it used to direct people at the workspace yolo-jail.jsonc — the
// weaker, agent-editable scope, and the one whose install-shaped keys are now
// refused. `enabled` is honored from either scope, but the command should
// never steer a human toward the file this design distrusts.
func CmdSetEnabled(deps Deps, name string, enabled bool) int {
	path := filepath.Join(UserLoopholesDir(), name)
	if fi, err := os.Stat(filepath.Join(path, "manifest.jsonc")); err != nil || fi.IsDir() {
		fmt.Fprintf(deps.Err,
			"No user-installed loophole at %s.\n"+
				"For bundled or config-inline loopholes, set "+
				"loopholes.%s.enabled in the user config (%s).\n",
			path, name, paths.UserConfigPath())
		return 1
	}
	if err := SetEnabled(path, enabled); err != nil {
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

// rcStr renders an *int rc as the int, or "None" when nil.
func rcStr(rc *int) string {
	if rc == nil {
		return "None"
	}
	return fmt.Sprintf("%d", *rc)
}
