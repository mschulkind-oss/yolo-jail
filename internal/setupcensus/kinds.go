package setupcensus

import "github.com/mschulkind-oss/yolo-jail/internal/packdecl"

// kinds.go is the census over the pack contribution kinds (packdecl.KnownKinds()). Every reason
// names the code path it was checked against on 2026-10-05, in internal/entrypoint unless
// another package is named.
//
// "Both boots" means the container entrypoint and the macos-user darwin bootstrap, which run
// ONE boot step table (bootsteps.go): a step either boot skips is declared there with its
// reason (notContainer, notDarwin), so a kind rendered by a step neither skips is rendered on
// every setup by construction. configure_pack_surfaces is that step for most kinds: one loop
// over the declarations (surfaceloop.go), with no switch on a tool name or a setup.

// renderedEverywhere is a kind the shared surface loop renders the same way on all four setups.
func renderedEverywhere(how string) Entry {
	return everywhere(honored(how + " on both boots (bootsteps.go), with no switch on the setup"))
}

var kinds = map[packdecl.Kind]Entry{
	packdecl.KindConfig: renderedEverywhere("configure_pack_surfaces renders the surface " +
		"(surfaceloop.go)"),
	packdecl.KindConfigOverlay: renderedEverywhere("configure_pack_surfaces folds the overlay " +
		"into its surface (surfaceloop.go)"),
	packdecl.KindConfigList: renderedEverywhere("configure_pack_surfaces appends the list into " +
		"its surface (surfaceloop.go)"),
	packdecl.KindAutonomy: renderedEverywhere("the jail notch's posture folds into the pack's " +
		"surfaces and launch flags (configure_pack_surfaces, deliver_launch_flags)"),
	packdecl.KindHook: renderedEverywhere("RunPackHooks runs each named hook inside " +
		"configure_pack_surfaces, and run_pack_boot_checks each boot_check in a container jail " +
		"(on macos-user it records the skip, the bootstrap running no pack script)"),
	packdecl.KindModels: renderedEverywhere("each consumer's model list is rendered by " +
		"configure_pack_surfaces from the provider's one list (surfaceloop.go)"),
	packdecl.KindBlockedTool: renderedEverywhere("GenerateShims writes the refusal into the " +
		"block dir when the replacement is on the agent's PATH (shims.go)"),
	packdecl.KindIntercept: renderedEverywhere("GenerateIntercepts, called from GenerateShims, " +
		"writes the forwarder into the block dir (interceptshims.go)"),
	packdecl.KindMCPAuth: {
		PodmanLinux:    dropped("declaration parsed and retained by the host; broker consumer is not implemented, so auth-store behavior is absent and no launch notice exists; this is an interim foundation, not a ruled permanent silence"),
		PodmanMac:      dropped("declaration parsed and retained by the host; broker consumer is not implemented, so auth-store behavior is absent and no launch notice exists; this is an interim foundation, not a ruled permanent silence"),
		AppleContainer: dropped("declaration parsed and retained by the host; broker consumer is not implemented, so auth-store behavior is absent and no launch notice exists; this is an interim foundation, not a ruled permanent silence"),
		MacosUser:      dropped("declaration parsed and retained by the host; broker consumer is not implemented, so auth-store behavior is absent and no launch notice exists; this is an interim foundation, not a ruled permanent silence"),
		Guide:          []string{"`mcp-auth` — an MCP sign-in store declaration"},
	},
	packdecl.KindMCP: {
		PodmanLinux: honored("jailMCPServers (internal/cli/run assemble.go) composes each " + "selected pack's `mcp` entries under the config's mcp_servers into YOLO_MCP_SERVERS, " +
			"which the boot renders per agent"),
		PodmanMac:      honored("the same composition (assemble.go)"),
		AppleContainer: honored("the same composition (assemble.go)"),
		MacosUser: honoredBy("BuildRunPlanWithDaemons (internal/macosuser runplan.go) composes " +
			"them for the sandbox account's home through MCPServersAt (mcp.go)"),
	},
	packdecl.KindProvider: everywhere(honored("packload.ComposeProvidersAt composes it into " +
		"the channel above the dispatch (composedProviders, internal/cli/run assemble.go)")),
	packdecl.KindProfile: {
		PodmanLinux: honored("launchChannel → composePackChannel (internal/cli/run seal.go, profilechannel.go) resolves `-p` " +
			"over it above the dispatch"),
		PodmanMac: honored("the same resolution (profilechannel.go)"),
		AppleContainer: honored("the same resolution (profilechannel.go); the channel files are " +
			"rewritten on every entry, rendered through that launch's copy of each pack"),
		MacosUser: honored("the same resolution (profilechannel.go); its variables reach the " +
			"program the invocation starts through the session env file"),
		Guide: []string{"`profile` — a named `-p` selection"},
	},
	packdecl.KindAdapter: {
		PodmanLinux: honored("packload.ComposeProvidersAt selects the declared conversion and " +
			"composes the address that serves it (composedProviders)"),
		PodmanMac:      honored("the same composition (composedProviders)"),
		AppleContainer: honored("the same composition (composedProviders)"),
		MacosUser: honoredBy("a launch-owned service's conversions are composed at the ports " +
			"this launch picked (composedProvidersFor, internal/cli/run macosuserservices.go)"),
	},
	packdecl.KindProgram: {
		PodmanLinux: honored("GenerateAgentLaunchers writes a lazy launcher per bin " +
			"(bootsteps.go), seeded from the install-capture store where one exists"),
		PodmanMac: honored("the same launchers (GenerateAgentLaunchers)"),
		AppleContainer: honored("the same launchers; below Apple Container 1.1.0 the capture " +
			"store is not mounted (captures.go's roBindsUnsupported gate), so a first use runs " +
			"the vendor installer"),
		MacosUser: honored("the darwin bootstrap runs the same generate_agent_launchers step; " +
			"an installer program's capture and a fork's darwin build are staged into the sandbox's copy of " +
			"the capture store, and with no capture a first use runs the vendor installer"),
		// A patched fork (`program` with `patches`) has the parent's disposition everywhere: on
		// macos-user the fork's advance builds it for darwin as the sandbox account in the sealed
		// macos-user act (FP-D24, PF-D97), and the launch stages it into the sandbox's capture store.
		Guide: []string{"`program` with `patches`"},
	},
	packdecl.KindSkills: {
		PodmanLinux: honored("assembleRunCmd's skills loop (internal/cli/run assemble.go) binds " +
			"each staged tree read-only at its destination (packSkillTargets, " +
			"internal/cli/run prepare.go)"),
		PodmanMac: honored("the same read-only binds (packSkillTargets)"),
		AppleContainer: honored("the same read-only binds, with no branch for this backend " +
			"(packSkillTargets); below Apple Container 1.1.0 the suffix is ignored and the " +
			"tree is writable, with no line"),
		MacosUser: honoredBy("copied into the sandbox home by install_home_overlay " +
			"(buildMacosHomeOverlay) and write-protected by the Seatbelt profile " +
			"(macosuser homereadonly.go)"),
	},
	packdecl.KindBriefing: {
		PodmanLinux: honored("assembleRunCmd binds the composed briefing read-only at each " +
			"destination briefingDestinations names (internal/cli/run briefingdest.go)"),
		PodmanMac: honored("the same binds (briefingDestinations)"),
		AppleContainer: honoredBy("a per-launch copy under the wsState bind (acMaterialize), " +
			"which a re-entry does not refresh (attachskewbriefing.go)"),
		MacosUser: honoredBy("copied into the sandbox home by install_home_overlay " +
			"(buildMacosHomeOverlay) and write-protected by the profile (macosuser " +
			"homereadonly.go)"),
	},
	packdecl.KindFiles: {
		PodmanLinux: honored("packFilesMountArgs binds each tree, or single file, read-only " +
			"into the home (internal/cli/run packfiles.go)"),
		PodmanMac: honored("the same binds (packFilesMountArgs)"),
		AppleContainer: honored("a tree is the same read-only bind (packFilesMountArgs); a " +
			"single-file target is copied into wsState instead (acMaterialize), where the agent " +
			"can write it"),
		MacosUser: honoredBy("copied by install_home_overlay (buildMacosHomeOverlay) and " +
			"write-protected by the profile (macosuser homereadonly.go)"),
		// A patched pi extension (`files` with `source` and `patches`) has the parent's disposition
		// everywhere: on macos-user its advance builds it for darwin as the sandbox account in the
		// sealed macos-user act (pi-extensions.md §11, PF-D97), and the launch copies its per-launch
		// copy into the sandbox home through the same overlay (buildMacosHomeOverlayWith).
		Guide: []string{"`files` — a tree in the agent's home", "`files` with `source` and `patches`"},
	},
	packdecl.KindState: {
		PodmanLinux: honored("assembleRunCmd binds per-workspace dirs from wsState and " +
			"machine-wide ones from GlobalHome (packload.WritableDirs, packload.SharedDirs; " +
			"internal/cli/run assemble.go)"),
		PodmanMac: honored("the same binds (assembleRunCmd)"),
		AppleContainer: honoredBy("the wsState bind at /home/agent holds the per-workspace tier, " +
			"and machine-wide dirs bind from GlobalHome with a copy-if-missing rescue " +
			"(appleContainerBaseMounts; backend-parity.md §5 #1, #2)"),
		MacosUser: honoredBy("InstallDarwinHomeLayout symlinks each scope:workspace dir into " +
			"<workspace>/.yolo/home; scope:machine dirs stay in the sandbox account home"),
	},
	packdecl.KindReadsHost: {
		PodmanLinux: honored("each grant is a read-only /ctx bind the entrypoint renders from " +
			"(internal/cli/run packhostgrants.go)"),
		PodmanMac: honored("the same binds (packhostgrants.go)"),
		AppleContainer: honoredBy("materialized into ws_state and named by YOLO_CTX_ROOT " +
			"(acMaterialize; backend-parity.md §5 #3)"),
		MacosUser: honoredBy("copied into a host-owned read-only tree under /Users/Shared/yolo-staged " +
			"(internal/cli/run macosctxtree.go, macosuser.StageCtxCommands)"),
	},
	packdecl.KindMount: {
		PodmanLinux: honored("packCtxMounts binds the host dir read-only at /ctx/<into> " +
			"(internal/cli/run ctxmounts.go)"),
		PodmanMac: honored("the same bind (packCtxMounts); the source must be in the VM's " +
			"share set"),
		AppleContainer: honored("the same bind from Apple Container 1.1.0; below it, or with an " +
			"unreadable version, roBindsUnsupported skips it with a line"),
		MacosUser: refused("planMacosUserCtxMounts refuses the launch for a selected pack's " +
			"mount whose folder exists: its source is in a home (~/<from>), and this setup " +
			"delivers only folders outside every home (DP-D15); a missing folder is skipped " +
			"with a warning"),
		Guide: []string{"`mount` — host dir read-only"},
	},
	packdecl.KindEnv: {
		PodmanLinux: honored("packload.EnvVarsFor folds the static vars into the channel the " +
			"jail's env file carries (internal/cli/run profilechannel.go)"),
		PodmanMac: honored("the same channel (profilechannel.go)"),
		AppleContainer: honored("the same channel, rendered through the launch's copy of the " +
			"pack, so a pack edit waits for a restart"),
		MacosUser: honoredBy("carried in the launch env (macosuser buildPlan layers PackEnv); a " +
			"served_by variable is set at the port this launch picked for its helper"),
		Guide: []string{"`env` — static vars"},
	},
	packdecl.KindService: {
		PodmanLinux: honored("the boot's supervisor runs its jail daemon " +
			"(start_jail_daemon_supervisor, bootsteps.go) and the witness probes its endpoint"),
		PodmanMac:      honored("the same in-jail daemon (start_jail_daemon_supervisor)"),
		AppleContainer: honored("the same in-jail daemon (start_jail_daemon_supervisor)"),
		MacosUser: honoredBy("the guest declines the jail daemon and the launch starts the " +
			"service's host half outside the sandbox on a port it picked " +
			"(startMacosUserServices, internal/cli/run macosuserservices.go)"),
		Guide: []string{"`service` — an in-jail daemon"},
	},
	packdecl.KindLoophole: {
		PodmanLinux: honored("startLoopholesDisclosed (internal/cli/run packloopholes.go) " +
			"starts its host daemon and the jail learns the endpoint"),
		PodmanMac: honored("the same host daemon, reached from the VM; the jail-to-Mac hop is " +
			"dialed by the macOS nightly (guide's machop)"),
		AppleContainer: warned("loopholeAllow (loopholesruntime.go) starts only the OpenAI " +
			"service, plus the Claude broker for an opted-in credential view (off by default), and " +
			"notePackLoopholesInert names every other one at launch"),
		MacosUser: honoredBy("every admitted host daemon starts with an ACL-granted endpoint " +
			"(macosuser.EndpointGrantCommands) and the jail halves run in the sandbox; the ones " +
			"it cannot run are declined by name (noteMacosUserJailDaemonDeclines)"),
		Guide: []string{"`loophole` — a host service"},
	},
}
