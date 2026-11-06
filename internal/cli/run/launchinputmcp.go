package run

import (
	"reflect"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// frozenLaunchMCP is the launch's approved composition, carried through the keeper
// plan rather than reconstructed from mutable host configuration in the daemon.
type frozenLaunchMCP struct {
	EffectiveJSON string `json:"effective,omitempty"`
	UserOnlyJSON  string `json:"user_only,omitempty"`
	Proven        bool   `json:"proven,omitempty"`
	// SharedCredentialDir is LaunchInputContext.SharedCredentialDir, decided at launch.
	SharedCredentialDir string `json:"shared_credential_dir,omitempty"`
}

// launchMCPSelection holds the user layer as it stood before this launch staged
// packs. A later user edit cannot promote a workspace/CLI-selected pack to user scope.
type launchMCPSelection struct {
	user  *jsonx.OrderedMap
	packs []*packload.Pack
	valid bool
}

func (o *Options) snapshotLaunchMCPSelection(cfg *jsonx.OrderedMap, rt string) launchMCPSelection {
	registry := o.launchInputProducers
	if registry == nil {
		registry = launchInputProducers()
	}
	if len(registry) == 0 || o.Sealed {
		return launchMCPSelection{}
	}
	// Staging registered this launch's selected modules. Do not read user scope
	// again for a launch with no selected, admitted generic-input host daemon.
	allow := o.loopholeAllow(rt, cfg)
	active := false
	for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(cfg)).Enabled() {
		if lp.HostDaemon != nil && registry[lp.HostDaemon.LaunchInput].Produce != nil && allow(lp.Name) {
			active = true
			break
		}
	}
	if !active {
		return launchMCPSelection{}
	}
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil {
		return launchMCPSelection{}
	}
	packs, complete := config.UserScopeSelectedPacks()
	return launchMCPSelection{user: user, packs: packs, valid: complete}
}

func sameSelectedMCPPacks(before, after []*packload.Pack) bool {
	if len(before) != len(after) {
		return false
	}
	for i := range before {
		if before[i] == nil || after[i] == nil || before[i].Name != after[i].Name ||
			before[i].Namespace != after[i].Namespace || !reflect.DeepEqual(before[i].Decl, after[i].Decl) {
			return false
		}
	}
	return true
}

func frozenMCPForLaunch(cfg, user *jsonx.OrderedMap, selected, staged []*packload.Pack) frozenLaunchMCP {
	if user == nil {
		return frozenLaunchMCP{}
	}
	// Match the user selection against the selected immutable tree. A missing pack
	// or changed declaration makes provenance unavailable, never user by default.
	byName := make(map[string]*packload.Pack, len(staged))
	for _, pack := range staged {
		if pack != nil {
			byName[pack.Name] = pack
		}
	}
	verified := make([]*packload.Pack, 0, len(selected))
	for _, pack := range selected {
		if pack == nil || byName[pack.Name] == nil || !reflect.DeepEqual(pack.Decl, byName[pack.Name].Decl) ||
			pack.Namespace != byName[pack.Name].Namespace {
			return frozenLaunchMCP{}
		}
		verified = append(verified, byName[pack.Name])
	}
	effective := jailMCPServers(cfg, staged)
	userOnly := packload.ComposeMCPServers(cfgMap(user, "mcp_servers"), verified, jailHome)
	return frozenLaunchMCP{EffectiveJSON: jsonx.MustDumpsCompact(effective), UserOnlyJSON: jsonx.MustDumpsCompact(userOnly), Proven: true}
}

func (o *Options) freezeLaunchMCP(rt string, cfg *jsonx.OrderedMap, staged []*packload.Pack, before launchMCPSelection) {
	o.freezeLaunchMCPComposition(cfg, staged, before)
	if o.launchInputMCP.Proven {
		// The machine-wide Claude store this launch bridges (CL-D22), on the host: the one place
		// a one-sign-in server's sign-in is adopted from and written back to (MA-D17).
		o.launchInputMCP.SharedCredentialDir = o.claudeSecureStorageDir(rt, cfg, staged, paths.GlobalHome())
	}
}

func (o *Options) freezeLaunchMCPComposition(cfg *jsonx.OrderedMap, staged []*packload.Pack, before launchMCPSelection) {
	if !before.valid {
		return
	}
	// A changed user layer between the original config load, staging and the
	// approval gate cannot retrospectively turn a CLI-only pack into user scope.
	current, err := config.LoadConfig(o.Workspace, true, func(string) {})
	if err != nil || !reflect.DeepEqual(jsonx.Plain(current), jsonx.Plain(cfg)) {
		return
	}
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil || !reflect.DeepEqual(jsonx.Plain(user), jsonx.Plain(before.user)) {
		return
	}
	selected, complete := config.UserScopeSelectedPacks()
	if !complete || !sameSelectedMCPPacks(before.packs, selected) {
		return
	}
	o.launchInputMCP = frozenMCPForLaunch(cfg, before.user, before.packs, staged)
}

// No generic input is active on ordinary launches: leave their keeper plans unchanged.
func (o *Options) frozenLaunchMCPForKeeper() *frozenLaunchMCP {
	if !o.launchInputMCP.Proven {
		return nil
	}
	frozen := o.launchInputMCP
	return &frozen
}
