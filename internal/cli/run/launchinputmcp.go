package run

import (
	"reflect"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// frozenLaunchMCP is the launch's approved composition, carried through the keeper
// plan rather than reconstructed from mutable host configuration in the daemon.
type frozenLaunchMCP struct {
	EffectiveJSON string `json:"effective,omitempty"`
	UserOnlyJSON  string `json:"user_only,omitempty"`
	Proven        bool   `json:"proven,omitempty"`
	// Unproven is WHY provenance could not be proved: one sentence naming the pack and the
	// check, then the next step. Empty when proven or when this launch needed no provenance. It
	// rides the keeper plan, because the keeper's process prints the notice.
	Unproven  string                   `json:"unproven,omitempty"`
	UserPacks []string                 `json:"user_packs,omitempty"`
	SignIn    *mcpsignin.Launch        `json:"sign_in,omitempty"`
	Advice    *mcpsignin.AdviceContext `json:"advice,omitempty"`
	// SharedCredentialDir is LaunchInputContext.SharedCredentialDir, decided at launch.
	SharedCredentialDir string `json:"shared_credential_dir,omitempty"`
	// SharedCredentialHome is LaunchInputContext.SharedCredentialHome: "" for paths.GlobalHome.
	SharedCredentialHome string `json:"shared_credential_home,omitempty"`
}

// launchMCPSelection holds the user layer as it stood before this launch staged
// packs. A later user edit cannot promote a workspace/CLI-selected pack to user scope.
type launchMCPSelection struct {
	user  *jsonx.OrderedMap
	packs []*packload.Pack
	valid bool
	// why is the reason valid is false (for the notice); "" when no snapshot was needed.
	why string
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
	// Where the launcher keeps a sign_in = "once" server itself (mcpauthboundary.go), it needs
	// the same proven composition a broker's share is built from.
	if !active && hasOnceServer(cfg) {
		active = true
	}
	if !active {
		return launchMCPSelection{}
	}
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil {
		return launchMCPSelection{why: "your user config could not be read (" + err.Error() + "); fix it as that message says, check it with `yolo config check`, then retry the launch"}
	}
	packs, complete := config.UserScopeSelectedPacks()
	if !complete {
		why := config.UserScopeSelectionGap()
		if why == "" {
			why = "your user pack selection did not resolve completely; list it with `yolo pack ls`, then retry the launch"
		}
		return launchMCPSelection{why: why}
	}
	return launchMCPSelection{user: user, packs: packs, valid: true}
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
		if pack == nil {
			return frozenLaunchMCP{Unproven: "a selected user pack could not be read; list your packs with `yolo pack ls`, then retry the launch"}
		}
		got := byName[pack.Name]
		if got == nil {
			return frozenLaunchMCP{Unproven: "user pack " + pack.Name + " is not in this launch's pack tree, so its sign-ins cannot be tied to your user config; " +
				"retry the launch, and if it recurs run `yolo pack ls` to see whether " + pack.Name + " is selected"}
		}
		if differs := declarationDifference(pack, got); differs != "" {
			return frozenLaunchMCP{Unproven: "user pack " + pack.Name + " reads differently in your user config than in this launch's pack tree (" + differs +
				"), so its sign-ins cannot be tied to your user config; retry the launch, and if it recurs run `yolo pack ls` and compare " + pack.Name}
		}
		verified = append(verified, got)
	}
	effective := jailMCPServers(cfg, staged)
	userOnly := packload.ComposeMCPServers(cfgMap(user, "mcp_servers"), verified, jailHome)
	names := make([]string, 0, len(verified))
	for _, pack := range verified {
		names = append(names, pack.Name)
	}
	return frozenLaunchMCP{EffectiveJSON: jsonx.MustDumpsCompact(effective), UserOnlyJSON: jsonx.MustDumpsCompact(userOnly), Proven: true, UserPacks: names, Advice: MCPAdviceContext(user, verified, names)}
}

// freezeLaunchMCP freezes the launch's composition, and the Claude store it bridges under the
// host directory sharedHome: "" for a container jail, whose store is under paths.GlobalHome, or a
// macos-user session's shared home (macosuser.LaunchHomes) for one.
func (o *Options) freezeLaunchMCP(rt string, cfg *jsonx.OrderedMap, staged []*packload.Pack, before launchMCPSelection, sharedHome string) {
	o.freezeLaunchMCPComposition(cfg, staged, before)
	if o.launchInputMCP.Proven {
		home := sharedHome
		if home == "" {
			home = paths.GlobalHome()
		}
		// The machine-wide Claude store this launch bridges (CL-D22), on the host: the one place
		// a one-sign-in server's sign-in is adopted from and written back to (MA-D17).
		o.launchInputMCP.SharedCredentialDir = o.claudeSecureStorageDir(rt, cfg, staged, home)
		if home != paths.GlobalHome() {
			o.launchInputMCP.SharedCredentialHome = home
		}
	}
}

func (o *Options) freezeLaunchMCPComposition(cfg *jsonx.OrderedMap, staged []*packload.Pack, before launchMCPSelection) {
	if !before.valid {
		o.launchInputMCP = frozenLaunchMCP{Unproven: before.why}
		return
	}
	// A changed user layer between the original config load, staging and the
	// approval gate cannot retrospectively turn a CLI-only pack into user scope.
	const moved = "your config changed while this launch was starting, so which sign-ins are yours could not be proved; retry the launch"
	current, err := config.LoadConfig(o.Workspace, true, func(string) {})
	if err != nil || !reflect.DeepEqual(jsonx.Plain(current), jsonx.Plain(cfg)) {
		o.launchInputMCP = frozenLaunchMCP{Unproven: moved}
		return
	}
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil || !reflect.DeepEqual(jsonx.Plain(user), jsonx.Plain(before.user)) {
		o.launchInputMCP = frozenLaunchMCP{Unproven: moved}
		return
	}
	selected, complete := config.UserScopeSelectedPacks()
	if !complete || !sameSelectedMCPPacks(before.packs, selected) {
		o.launchInputMCP = frozenLaunchMCP{Unproven: "your user pack selection changed while this launch was starting, so which sign-ins are yours could not be proved; retry the launch"}
		return
	}
	o.launchInputMCP = frozenMCPForLaunch(cfg, before.user, before.packs, staged)
}

// No generic input is active on ordinary launches: leave their keeper plans unchanged.
func (o *Options) frozenLaunchMCPForKeeper() *frozenLaunchMCP {
	if !o.launchInputMCP.Proven {
		// Only the reason crosses: the keeper's process prints the notice (mcpauthnotice.go).
		if o.launchInputMCP.Unproven == "" {
			return nil
		}
		return &frozenLaunchMCP{Unproven: o.launchInputMCP.Unproven}
	}
	frozen := o.launchInputMCP
	return &frozen
}

// declarationDifference names what differs between a pack as the user-scope read resolved it and
// as the launch staged it: its namespace, or the top-level declaration fields that are not equal.
// "" means the two are the same pack.
func declarationDifference(user, staged *packload.Pack) string {
	var differs []string
	if user.Namespace != staged.Namespace {
		differs = append(differs, "namespace")
	}
	ud, sd := relocatable(user.Decl), relocatable(staged.Decl)
	if !reflect.DeepEqual(ud, sd) {
		if ud == nil || sd == nil {
			differs = append(differs, "declaration present in only one")
		} else {
			a, b := reflect.ValueOf(ud).Elem(), reflect.ValueOf(sd).Elem()
			for i := 0; i < a.NumField(); i++ {
				if !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
					differs = append(differs, "declaration field "+a.Type().Field(i).Name)
				}
			}
		}
	}
	if len(differs) == 0 {
		return ""
	}
	return strings.Join(differs, ", ")
}

// relocatable is decl with the three values that only say WHERE a fork pack lives cleared: the fork
// rewrite (packload.ApplyForks) stamps a fork's base with the fork pack's root directory, which is
// the user's source directory in the user-scope read and a path inside this launch's staged tree
// in the launch, so a base a fork rewrote never compared equal to itself. No MCP declaration
// reads them; every other field still has to be identical.
func relocatable(decl *packdecl.Manifest) *packdecl.Manifest {
	if decl == nil {
		return nil
	}
	out := *decl
	out.Contributes = slices.Clone(decl.Contributes)
	for i := range out.Contributes {
		out.Contributes[i].ForkRoot = ""
		out.Contributes[i].ForkPatchBranchDir = ""
		out.Contributes[i].ForkPatchBranchRepoIdentity = ""
	}
	return &out
}
