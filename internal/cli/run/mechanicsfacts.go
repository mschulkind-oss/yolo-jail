package run

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/changetiming"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// mechanicsFactSheet adapts decisions already made by the launch pipeline. In particular,
// mounts and host files come from their resolved/staged lists and package delivery comes from
// planStorePackages; the renderer is not allowed to reconstruct any of those decisions.
func (o *Options) mechanicsFactSheet(cname string, cfg *jsonx.OrderedMap, rt string, staged stagedPacks,
	store storePackagesPlan, hostFiles []config.HostFileEntry, sourceInputs ...persistenceSources) *jailcontent.FactSheet {
	backend := changetiming.Backend(rt)
	packageDelivery := changetiming.BakedImage
	if store.Active {
		packageDelivery = changetiming.StoreDelivered
	}
	facts := &jailcontent.FactSheet{
		HostWorkspace:      o.Workspace,
		WorkspacePath:      "/workspace",
		Confinement:        string(config.ResolveConfinement(cfg)),
		UserConfigPath:     paths.UserConfigPath(),
		PackageDelivery:    string(packageDelivery),
		Packs:              mechanicsPackNames(staged.packs),
		ScopeRestrictions:  mechanicsScopeRestrictions(),
		AppliedNetworkMode: appliedNetMode(rt, o.resolveNetMode(cfg), mechanicsInContainer(o)),
		NixDelivery:        nixDelivery(rt, mechanicsHostNixMounted(o, rt)),
	}
	if rt == "macos-user" { // parity: HonoredBy — the workspace is already at its native host path, not mounted at /workspace
		facts.WorkspacePath = o.Workspace
		if _, ok := macosuser.CooperativeCPUs(config.Resources(cfg)); ok {
			facts.CooperativeCPUDefaults = append([]string(nil), macosuser.CooperativeCPUVars...)
		}
	}
	localHostConfig, localConfigName := config.ResolveWorkspaceConfigPath(o.Workspace, config.WorkspaceLocalConfigName)
	facts.LocalConfigPath = filepath.Join(facts.WorkspacePath, localConfigName)
	if _, err := os.Stat(localHostConfig); err == nil {
		facts.LocalConfigPresent = true
		facts.Paths = append(facts.Paths, jailcontent.PathFact{Path: facts.LocalConfigPath, HostSource: localHostConfig,
			Mode: "workspace config", Lifetime: "workspace", Delivery: "live workspace filesystem", Origin: "workspace-local config"})
	}
	if rt == "macos-user" { // parity: HonoredBy — native session paths need no /workspace mount prefix
		facts.WorkspacePath = o.Workspace
		facts.SkillsDelivery = "live filesystem"
		facts.NativeAccountMode = o.MacosUserMode.String()
		facts.PackageDelivery = "native host filesystem"
		packageDelivery = changetiming.StoreDelivered
	} else if rt == "container" { // parity: HonoredBy — Apple Container receives the per-launch skills copy rather than a live bind
		facts.SkillsDelivery = "read-only copy at next launch"
	} else {
		facts.SkillsDelivery = "live read-only bind"
	}
	ctx := changetiming.Context{
		Backend: backend, PackageDelivery: packageDelivery,
		LiveMounts: rt == "podman" && !o.IsMacOS,                       // parity: HonoredBy — only Linux podman has live mount changes; Apple Container's VM mounts are fixed for the launch
		LiveScope:  rt == "podman" && !o.IsMacOS || rt == "macos-user", // parity: HonoredBy — podman applies live scope on Linux and macos-user reads native scope paths directly
		Rebuild:    packageDelivery == changetiming.BakedImage,
	}
	hostConfig, configName := config.ResolveWorkspaceConfigPath(o.Workspace, config.WorkspaceConfigName)
	facts.ConfigPath = filepath.Join(facts.WorkspacePath, configName)
	if _, configErr := os.Stat(hostConfig); configErr == nil {
		facts.WorkspaceConfigPresent = true
		facts.Paths = append(facts.Paths, jailcontent.PathFact{Path: facts.ConfigPath, HostSource: hostConfig,
			Mode: "workspace config", Lifetime: "workspace", Delivery: "live workspace filesystem", Origin: "workspace config"})
	}
	facts.RestartCommand = stopCommand(rt, cname)

	// Context path rows are populated from the final BriefingInput by refreshJailBriefings,
	// which uses the actual container or native linker and carries its refusal/siting result.

	home := jailHome
	if rt == "macos-user" { // parity: HonoredBy — the native session's HOME is the actual host home, not the container jailHome
		home = macosuser.LaunchHomes(o.MacosUserMode, o.Workspace, o.macosUserHostHome()).Home
	}
	for _, rel := range cfgStrList(cfg, "workspace_readonly") {
		hostPath := resolvePath(filepath.Join(o.Workspace, rel))
		if !isUnderOrEqual(hostPath, resolvePath(o.Workspace)) || !fileExists(hostPath) {
			continue
		}
		reason := o.roBindsUnsupported(rt)
		facts.WorkspaceReadonly = append(facts.WorkspaceReadonly, jailcontent.WorkspaceReadonlyFact{
			Path: filepath.Join(facts.WorkspacePath, rel), Enforced: reason == "", Reason: reason,
		})
	}
	for _, entry := range hostFiles {
		mode := entry.Mode
		if mode == "" {
			mode = "composed file"
		}
		origin := "inline or generated host_files entry"
		if entry.SourceBearing() {
			origin = "host_files source from " + entry.Scope + " config"
		}
		facts.Paths = append(facts.Paths, jailcontent.PathFact{Path: filepath.ToSlash(filepath.Join(home, entry.Path)),
			HostSource: entry.Source, OriginalSource: entry.Source, Mode: mode, Lifetime: "host file source lifetime",
			Delivery: hostFileDelivery(rt, entry), Origin: origin})
	}
	workspaceHost := o.Workspace
	if rt == "macos-user" { // parity: HonoredBy — native HOME, machine and shared tiers are host paths exposed to the sandbox session
		homes := macosuser.LaunchHomes(o.MacosUserMode, o.Workspace, o.macosUserHostHome())
		facts.Paths = append(facts.Paths, jailcontent.PathFact{Path: facts.WorkspacePath, HostSource: workspaceHost,
			Mode: "read-write", Lifetime: "host filesystem", Delivery: "native session", Origin: "workspace"})
		for name, path := range map[string]string{"native HOME": homes.Home, "native machine tier": homes.Machine, "native shared tier": homes.Shared} {
			if path != "" {
				facts.Paths = append(facts.Paths, jailcontent.PathFact{Path: path, HostSource: path,
					Mode: "host path subject to macos-user sandbox permissions", Lifetime: "host filesystem",
					Delivery: "native session", Origin: name})
			}
		}
		facts.NativeMiseData = macosuser.SandboxMiseData(homes.Machine)
		facts.Paths = append(facts.Paths, jailcontent.PathFact{Path: facts.NativeMiseData, HostSource: facts.NativeMiseData,
			Mode: "native mise data root", Lifetime: "host filesystem", Delivery: "native filesystem", Origin: "MISE_DATA_DIR"})
		if o.MacosUserMode != macosuser.ModeSelf {
			facts.NativeAccountUser = macosuser.SandboxUser
			facts.HomeRestPolicy = "the dedicated account HOME is writable; other host paths follow the dedicated-account Seatbelt profile"
		} else {
			facts.HomeRestPolicy = "the workspace-owned HOME is writable; other host paths follow the self-mode Seatbelt profile"
		}
		if homes.Real != "" {
			facts.Paths = append(facts.Paths, jailcontent.PathFact{Path: homes.Real, HostSource: homes.Real,
				Mode: "real host home; sandbox permissions apply", Lifetime: "host filesystem", Delivery: "native session", Origin: "real host home"})
		}
	} else {
		facts.Paths = append(facts.Paths, jailcontent.PathFact{Path: "/workspace", HostSource: workspaceHost,
			Mode: "read-write", Lifetime: "per workspace", Delivery: "bind mount"})
	}

	localSources := persistenceSources{}
	if len(sourceInputs) > 0 {
		localSources = sourceInputs[0]
	}
	facts.Persistence = persistenceMapForSources(rt, cfg, staged.packs, o.Workspace, o.Sealed, localSources, cname)
	if facts.Persistence != nil {
		facts.HomeRestPolicy = "read-only outside listed writable mounts"
		if facts.Persistence.HomeIsDurable(jailHome) {
			facts.HomeRestPolicy = "writable and per-workspace"
		}
		for _, p := range facts.Persistence.Paths {
			facts.Paths = append(facts.Paths, jailcontent.PathFact{Path: p.Path,
				HostSource: facts.Persistence.HostSources[p.Path], Mode: "read-write",
				Lifetime: p.Class.String(), Delivery: pathDelivery(p.Path, facts.Persistence.HostSources[p.Path])})
		}
	}
	for _, key := range config.TopLevelConfigKeys() {
		keyContext := ctx
		keyContext.UserScopeOnly = config.WorkspaceUserScopeOnlyKey(key)
		keyContext.Rebuild = false
		switch key {
		case "packages":
			keyContext.Rebuild = packageDelivery == changetiming.BakedImage
		case "gpu":
			keyContext.Rebuild = packageDelivery == changetiming.BakedImage && gpuAddsImagePackages(cfg)
		}
		row, err := changetiming.Classify(key, keyContext)
		if err == nil {
			facts.Timing = append(facts.Timing, row)
		}
	}
	facts.Switches = mechanicsSwitchFacts(facts.Briefing.Loopholes)
	return facts
}

func mechanicsInContainer(o *Options) bool {
	return !o.IsMacOS && o.PathExists != nil && paths.InsideContainer(o.PathExists)
}

func mechanicsHostNixMounted(o *Options, rt string) bool {
	if o.PathExists == nil || o.Getenv == nil {
		return false
	}
	return o.hostNixMounted(rt)
}

func mechanicsScopeRestrictions() []jailcontent.ScopeRestrictionFact {
	rules := config.WorkspaceScopeRestrictions()
	liveKeys := make(map[string]bool)
	for _, key := range config.TopLevelConfigKeys() {
		liveKeys[key] = true
	}
	out := make([]jailcontent.ScopeRestrictionFact, 0, len(rules))
	for _, rule := range rules {
		path := strings.TrimPrefix(rule.Path, "config.")
		key := path
		if dot := strings.IndexAny(key, ".["); dot >= 0 {
			key = key[:dot]
		}
		if !liveKeys[key] {
			continue
		}
		out = append(out, jailcontent.ScopeRestrictionFact{Path: rule.Path, When: rule.When})
	}
	return out
}

func nixDelivery(rt string, hostMounted bool) string {
	if rt == "macos-user" { // parity: HonoredBy — the native session uses the host Nix filesystem directly
		return "native host filesystem"
	}
	if hostMounted {
		return "host Nix daemon and store"
	}
	return "not mounted from the host"
}

func pathDelivery(path, source string) string {
	if source == "tmpfs" {
		return "tmpfs"
	}
	if _, _, _, ok := prune.ParseScratchVolumeName(source); ok {
		return "named per-launch volume"
	}
	if path == "/mise" {
		return "tool disk or store bind"
	}
	return "writable bind"
}

func hostFileDelivery(rt string, entry config.HostFileEntry) string {
	if !entry.SourceBearing() {
		return "host-file layer renders into home"
	}
	if rt == "macos-user" { // parity: HonoredBy — host-file sources are copied into the native context tree for the session
		return "copied into native context tree at launch"
	}
	if rt == "container" && !entry.IsDir { // parity: HonoredBy — Apple Container materializes source files in context staging because it does not bind them live
		return "copied into context staging at launch"
	}
	if rt == "container" { // parity: HonoredBy — Apple Container can bind host directories read-only, matching the other backend's context staging grant
		return "read-only host directory bind"
	}
	return "read-only host grant in context staging"
}

func mechanicsSwitchFacts(loops []jailcontent.Loophole) []jailcontent.SwitchFact {
	out := make([]jailcontent.SwitchFact, 0, len(loops))
	for _, loop := range loops {
		out = append(out, jailcontent.SwitchFact{Name: loop.Name, Available: loop.SwitchAvailable})
	}
	return out
}

func hostLoopbackFromArgv(argv []string) string {
	prefix := paths.HostLoopbackEnvVar + "="
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-e" && strings.HasPrefix(argv[i+1], prefix) {
			return strings.TrimPrefix(argv[i+1], prefix)
		}
	}
	return ""
}

func hostNixMountedFromArgv(argv []string) bool {
	socket, store := false, false
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		mount := argv[i+1]
		socket = socket || strings.HasPrefix(mount, hostNixSocket+":")
		store = store || strings.HasPrefix(mount, hostNixStore+":")
	}
	return socket && store
}

func appliedWorkspaceMechanics(facts *jailcontent.FactSheet, extra string, skills *jailcontent.WorkspaceSkills) (string, *jailcontent.WorkspaceSkills) {
	if facts == nil {
		return extra, skills
	}
	if facts.Briefing.Mechanism != "" {
		return facts.AppliedAgentsMDExtra, facts.AppliedWorkspaceSkills
	}
	facts.AppliedAgentsMDExtra = extra
	facts.AppliedWorkspaceSkills = skills
	return extra, skills
}

// gpuAddsImagePackages follows config.EffectivePackages, the launch's package decider, and
// is true only when AMD VA-API actually adds an image package not already declared.
func gpuAddsImagePackages(cfg *jsonx.OrderedMap) bool {
	declared := map[string]bool{}
	for _, value := range declaredNixPackages(cfg) {
		if name, ok := value.(string); ok {
			declared[name] = true
		}
	}
	for _, value := range config.EffectivePackages(cfg, containerJailPlatform()) {
		if name, ok := value.(string); ok && (name == "mesa" || name == "libva-utils") && !declared[name] {
			return true
		}
	}
	return false
}

func declaredNixPackages(cfg *jsonx.OrderedMap) []any {
	if install := cfgMap(cfg, "install"); install != nil {
		if value, present := install.Get("nix"); present {
			packages, _ := value.([]any)
			return packages
		}
	}
	return cfgList(cfg, "packages")
}

func mechanicsPackNames(packs []*packload.Pack) []string {
	out := make([]string, 0, len(packs))
	for _, pack := range packs {
		if pack != nil {
			out = append(out, pack.Name)
		}
	}
	return out
}
