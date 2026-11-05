// Package changetiming is the shared, explicit timing contract used by the
// config-apply planner and generated jail guidance. Runtime-only observations
// narrow its expected outcomes through Context; rendering never probes them.
package changetiming

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// Backend names the execution notch whose timing is being described.
type Backend string

const (
	Podman         Backend = "podman"
	AppleContainer Backend = "container"
	MacOSUser      Backend = "macos-user"
	Host           Backend = "host"
)

// Outcome is a lifecycle class. The values are human-readable because they
// are also rendered in the mechanics skill.
type Outcome string

const (
	AppliedNow        Outcome = "applied now"
	NextSession       Outcome = "next session"
	AtNextRestart     Outcome = "at next restart"
	AtNextLaunch      Outcome = "at next launch"
	RestartAndRebuild Outcome = "restart and rebuild"
	UserScopeOnly     Outcome = "user scope only"
	HostSwitch        Outcome = "host switch"
	NotApplicable     Outcome = "not applicable"
)

// PackageDelivery records the launch's already-decided package mechanism.
type PackageDelivery string

const (
	BakedImage     PackageDelivery = "baked image"
	StoreDelivered PackageDelivery = "store-delivered"
)

// Context carries applied decisions and the positive capabilities a runtime
// adapter has proved. A caller may narrow these expectations after a failed
// per-run probe; the renderer supplies no probes of its own.
type Context struct {
	Backend         Backend
	PackageDelivery PackageDelivery
	LiveMounts      bool
	LiveScope       bool
	UserScopeOnly   bool
	HostSwitch      bool
	Rebuild         bool
	// Grant says the change is a grant-declaring loophole's grant, and how that loophole crosses
	// (docs/reference/loophole-grants.md §4.4); NoGrant for anything else.
	Grant GrantCrossing
	// LiveGrants: every running daemon of that loophole was found, by its grant file, and takes
	// a rewritten one at its next request.
	LiveGrants bool
}

// GrantCrossing is how a grant-declaring loophole's grant reaches the host (§4.4): checked per
// request by a daemon reading its grant file (mediated), or container argv decided when the jail
// starts (unmediated: binds, devices).
type GrantCrossing int

const (
	NoGrant GrantCrossing = iota
	MediatedGrant
	UnmediatedGrant
)

// Row is one explicit key's expected timing for a backend.
type Row struct {
	Key     string
	Backend Backend
	Outcome Outcome
	Reason  string
}

// schemaRows is intentionally explicit: Classify rejects keys absent here,
// and TestEveryConfigKeyHasAnExplicitTimingRow ties this census to config's
// accepted live schema. Adding a config key therefore requires a ruling here.
var schemaRows = map[string]Outcome{
	"packages": AtNextRestart,
	"mounts":   AtNextRestart, "workspace_readonly": AtNextRestart, "per_side_paths": AtNextRestart,
	"network": AtNextRestart, "security": AtNextRestart, "mise_tools": AtNextRestart,
	"lsp_servers": AtNextRestart, "mcp_servers": AtNextRestart, "devices": AtNextRestart,
	"gpu": AtNextRestart, "resources": AtNextRestart, "env_sources": AtNextRestart,
	"loopholes": AtNextRestart, "brokered": AtNextRestart, "kvm": AtNextRestart,
	"prune": AtNextRestart, "programs": AtNextRestart, "ephemeral_storage": AtNextRestart,
	"include_if_found": AtNextRestart, "agents_md_extra": AtNextRestart,
	"briefing_provenance": AtNextRestart, "cache_relocations": AtNextRestart,
	"writable_home_dirs": AtNextRestart, "host_files": AtNextRestart,
	"host_wrappers": AtNextRestart, "share_host_fonts": AtNextRestart,
	"local_clones": AtNextRestart, "host_apply_on_launch": AtNextRestart,
	"host_management": AtNextRestart, "host_import_exclusions": AtNextRestart,
	"agent_updates": AtNextRestart, "host_floor": AtNextRestart, "host_path": AtNextRestart,
	"provisioners": AtNextRestart, "packs": AtNextRestart, "perf_logging": AtNextRestart,
	"macos_user_shared_tmp": AtNextRestart,
	"on_request":            AtNextRestart, "update_check": AtNextRestart, "promotion_target": AtNextRestart,
	"shipped_skill_prefix": AtNextRestart, "providers": AtNextRestart, "model_facts": AtNextRestart,
	"profiles": AtNextRestart, "profile": AtNextRestart, "required_capabilities": AtNextRestart,
	"adapters": AtNextRestart, "resource_groups": AtNextRestart,
	// Named setups: the setup is chosen when a launch starts, so a running jail keeps its own.
	"setup": AtNextRestart, "setups": AtNextRestart,
	// `requests` is a project's ask (readable config), not one of TopLevelConfigKeys. A grant it
	// carries for a mediated loophole is live (Context.Grant); every other ask waits.
	"requests": AtNextRestart,
}

var nextSessionKeys = map[string]bool{
	"env_sources": true, "providers": true, "profiles": true, "profile": true,
	"model_facts": true, "adapters": true,
}

// hostApplyConsumedKeys is narrower than host field applicability: HostFields
// describes every host verb, while this list is the set whose rendered output
// yolo host apply writes. HostFields remains the authority that each is honored.
var hostApplyConsumedKeys = map[string]bool{
	"packs": true, "providers": true, "model_facts": true, "profiles": true,
	"profile": true, "adapters": true, "mcp_servers": true, "lsp_servers": true,
	"agents_md_extra": true, "briefing_provenance": true,
}

// Classify returns the expected timing for key. User-scope, host-switch and
// rebuild predicates are inputs supplied by the existing validator/launch
// deciders; this package does not recreate those policy predicates.
func Classify(key string, ctx Context) (Row, error) {
	if _, ok := schemaRows[key]; !ok {
		return Row{}, fmt.Errorf("no change-timing row for config key %q", key)
	}
	if ctx.Backend != Podman && ctx.Backend != AppleContainer && ctx.Backend != MacOSUser && ctx.Backend != Host {
		return Row{}, fmt.Errorf("unknown change-timing backend %q", ctx.Backend)
	}
	row := Row{Key: key, Backend: ctx.Backend, Outcome: schemaRows[key]}
	if ctx.UserScopeOnly {
		row.Outcome = UserScopeOnly
		row.Reason = "the workspace config cannot carry this setting"
		return row, nil
	}
	if ctx.Backend == Host {
		// env_sources is honored by host verbs, but host apply cannot rewrite the
		// environment of the process already running it.
		if key == "env_sources" {
			row.Outcome = NextSession
			return row, nil
		}
		disposition, _ := render.HostFields().ConfigKey(key)
		if hostApplyConsumedKeys[key] && disposition == render.KeyHonored {
			row.Outcome = AppliedNow
		} else {
			row.Outcome = NotApplicable
		}
		return row, nil
	}
	if key == "loopholes" && ctx.HostSwitch {
		row.Outcome = HostSwitch
		row.Reason = "this loophole is enabled by its host-side switch"
		return row, nil
	}
	if ctx.Grant != NoGrant {
		return grantRow(row, ctx), nil
	}
	if ctx.Backend == MacOSUser {
		if key == "brokered" && ctx.LiveScope {
			row.Outcome = AppliedNow
		} else {
			row.Outcome = NextSession
		}
		return row, nil
	}
	switch key {
	case "mounts":
		if ctx.Backend == AppleContainer {
			row.Reason = "Apple Container shares are fixed when its VM boots"
		} else if ctx.LiveMounts {
			row.Outcome = AppliedNow
		} else {
			row.Reason = "live mount delivery was not proved for this launch"
		}
	case "brokered":
		if ctx.Backend == AppleContainer {
			row.Reason = "no broker runs in this jail"
		} else if ctx.LiveScope {
			row.Outcome = AppliedNow
		} else {
			row.Reason = "live broker scope was not proved for this launch"
		}
	case "agents_md_extra", "briefing_provenance":
		if ctx.Backend == AppleContainer {
			row.Reason = "Apple Container copies its briefing once, when the jail starts"
		} else {
			row.Outcome = NextSession
		}
	case "packages", "gpu":
		if ctx.Rebuild && ctx.PackageDelivery == BakedImage {
			row.Outcome = RestartAndRebuild
			row.Reason = "the baked image inputs change"
		}
	default:
		if nextSessionKeys[key] {
			row.Outcome = NextSession
		}
	}
	return row, nil
}

// grantRow is a grant change's timing (docs/reference/loophole-grants.md §4.6, LG-D7): a
// mediated loophole's running daemon reads its rewritten grant file at its next request, as a
// broker takes a scope at its next call; an unmediated loophole's crossing is container argv,
// so its grant is recorded and lands at the next restart. A native session is a fresh launch,
// so a grant it cannot take live reaches the next one.
func grantRow(row Row, ctx Context) Row {
	switch {
	case ctx.Grant == MediatedGrant && ctx.LiveGrants:
		row.Outcome = AppliedNow
	case ctx.Backend == MacOSUser:
		row.Outcome = NextSession
	case ctx.Grant == UnmediatedGrant:
		row.Outcome = AtNextRestart
		row.Reason = "this loophole's access is bound into the jail when it starts"
	default:
		row.Outcome = AtNextRestart
		row.Reason = "no running daemon of this loophole was found"
	}
	return row
}

// HostCommand makes the human handoff sentence from its real command and
// workspace. Callers pass the host-side path; it is never inferred here.
func HostCommand(command, workspace string, endsSessions bool) string {
	command = strings.TrimSpace(command)
	workspace = strings.TrimSpace(workspace)
	if endsSessions {
		return fmt.Sprintf("Please run `%s` in `%s` on the host; this ends the jail's sessions.", command, workspace)
	}
	return fmt.Sprintf("Please run `%s` in `%s` on the host.", command, workspace)
}
