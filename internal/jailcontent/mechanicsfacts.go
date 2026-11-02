package jailcontent

import "github.com/mschulkind-oss/yolo-jail/internal/changetiming"

// FactSheet is the applied launch state shared by generated built-in content.
// Its values are assembled by the launch/apply deciders; renderers must not
// read files, inspect the environment or re-probe runtime capabilities.
type FactSheet struct {
	Briefing               BriefingInput
	Host                   bool
	HostWorkspace          string
	WorkspacePath          string
	Confinement            string
	UserConfigPath         string
	ConfigPath             string
	WorkspaceConfigPresent bool
	LocalConfigPath        string
	LocalConfigPresent     bool
	NativeAccountMode      string
	NativeAccountUser      string
	NativeMiseData         string
	WorkspaceReadonly      []WorkspaceReadonlyFact
	WorkspaceProjectDirs   []string
	ScopeRestrictions      []ScopeRestrictionFact
	AppliedNetworkMode     string
	NixDelivery            string
	HostLoopback           string
	HomeRestPolicy         string
	Persistence            *PersistenceMap
	Switches               []SwitchFact
	AppliedAgentsMDExtra   string
	AppliedWorkspaceSkills *WorkspaceSkills
	RestartCommand         string
	LogPath                string
	PackageDelivery        string
	SkillsDelivery         string
	Paths                  []PathFact
	Timing                 []changetiming.Row
	Packs                  []string
	CooperativeCPUDefaults []string
}

// PathFact records a delivered jail path and its host source, mode, lifetime
// and delivery mechanism as decided by the launch.
type PathFact struct {
	Path           string
	HostSource     string
	OriginalSource string
	BackingSource  string
	Mode           string
	Lifetime       string
	Delivery       string
	Origin         string
}

// WorkspaceReadonlyFact describes one configured read-only workspace path and whether
// the current backend enforces that mode.
type WorkspaceReadonlyFact struct {
	Path     string
	Enforced bool
	Reason   string
}

// ScopeRestrictionFact is one config validator's workspace-scope restriction rule.
type ScopeRestrictionFact struct {
	Path string
	When string
}

// SwitchFact is a loophole a `loopholes.<name>.enabled` key decides for the selected launch.
// There is no command for it: `yolo loopholes enable|disable` are retired
// (docs/design/loophole-access-approval.md LG-D8), so the sheet names the pack and grant verbs.
type SwitchFact struct {
	Name      string
	Available bool
}
