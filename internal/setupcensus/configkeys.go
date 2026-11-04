package setupcensus

// configkeys.go is the census over the LIVE top-level config keys
// (config.TopLevelConfigKeys(); retired spellings are refused by validation before any setup is
// reached, so they have no cells). Every reason names the code path it was checked against on
// 2026-10-05, in internal/cli/run unless another package is named. Re-check one before relying
// on it: a reader that moves is exactly what turns a cell false without touching this file.
//
// "Above the dispatch" means a part of run.Run that every setup passes through before the
// macos-user arm returns and the container arm begins: config load and validation
// (loadAndValidateConfig), resolveSetup (setupgate.go), stageRunPacks and launchChannel. A key read only there is the same on every setup by construction.

// hostSideKey is a key no jail reads: a host command acts on it, so every setup is the same.
func hostSideKey(reader string) Entry {
	return everywhere(honored("read host-side by "+reader+"; no jail reads it, so the "+
		"setup is not the axis"), "`host_management`, `host_wrappers`")
}

var configKeys = map[string]Entry{
	// ---- Choosing the setup ------------------------------------------------------------------
	// Named setups (docs/design/named-setups.md): user scope only, read before any backend runs.
	"setup": everywhere(honored("validateSetups (setups.go) checks it at every launch and `yolo check`, "+
		"refusing it in a project file and refusing a host setup; config.SelectSetup reads it from the "+
		"user config alone, after --setup and YOLO_SETUP and before the OS default"), "`setup`"),
	"setups": everywhere(honored("validateSetups (setups.go) decodes each table through setup.Decode "+
		"at every launch and `yolo check`, refusing it in a project file and refusing a built-in's "+
		"name; config.UserSetups reads it from the user config alone"), "`setups`"),

	// ---- Workspace, mounts and host files ----------------------------------------------
	"mounts": {
		PodmanLinux: honored("configCtxMounts (ctxmounts.go) binds each entry read-only under " +
			"/ctx; a read-write entry from the user config (config.LoadRWMounts) or an approved " +
			"project request the config-change gate approved (config.ApproveWorkspaceRead) is named on " +
			"every launch"),
		PodmanMac: honored("the same binds, made inside the Podman Machine VM; a source outside " +
			"the machine's share set refuses the launch before the container starts " +
			"(machineshares.go)"),
		AppleContainer: honored("the same binds; below Apple Container 1.1.0, or when `container " +
			"--version` cannot be read, roBindsUnsupported (backendcaps.go) skips each read-only " +
			"entry with a line rather than bind it writable"),
		MacosUser: honoredBy("planMacosUserCtxMounts and macosCtxLinks (ctxmounts.go, " +
			"macosctxtree.go): a root-owned link in $YOLO_CONTEXT_DIR plus Seatbelt rules, no " +
			"bind; an entry under a home folder refuses the launch, naming it (DP-D15)"),
		// The "optional" row shares these dispositions: an optional entry a setup cannot deliver
		// is skipped with one line (config.OptionalMountSkipLine, context-mounts.md#optional-mounts).
		Guide: []string{"`mounts` — host dirs read-only", "`mounts` read-write form",
			"`mounts` entry with `\"optional\": true`"},
		Aspects: map[string]Entry{
			// An entry whose folder is inside a user's home (`~/notes`), the guide's own example.
			"under_a_home": {
				PodmanLinux: honored("configCtxMounts (ctxmounts.go) binds it like any other entry"),
				PodmanMac: honored("the same bind; the VM shares /Users by default, and a source " +
					"outside its share set refuses the launch (machineshares.go)"),
				AppleContainer: honored("the same bind, read-only from Apple Container 1.1.0 " +
					"(roBindsUnsupported), like any other entry"),
				MacosUser: refused("planMacosUserCtxMounts (macosctxtree.go) refuses the launch, " +
					"naming each such entry, through SiteContextLinks (macosuser ctxlinks.go): the " +
					"sandbox account cannot reach into a real home, so this setup delivers only " +
					"folders outside every home (DP-D15)"),
				Guide: []string{"`mounts` — host dirs read-only", "`mounts` read-write form"},
			},
		},
	},
	"workspace_readonly": {
		PodmanLinux: honored("workspaceReadonlyMountArgs (mounts.go) binds each declared path " +
			"read-only over the workspace bind"),
		PodmanMac: honored("workspaceReadonlyMountArgs (mounts.go), the same binds"),
		AppleContainer: honored("the same binds from Apple Container 1.1.0; below it " +
			"workspaceReadonlyMountArgs prints 'workspace_readonly is NOT enforced on this " +
			"runtime' naming every entry (roBindsUnsupported)"),
		MacosUser: honoredBy("macosuser.SeatbeltProfile's readonlyRels deny writes to each path " +
			"(seatbelt.go); a policy rule, not a mount"),
		// The lock on the workspace's own config file, which setting the key also performs, has
		// the parent's disposition on every setup: workspaceReadonlyMountArgs (mounts.go) adds the
		// file to the read-only binds whenever an entry is set, and on macos-user
		// workspaceReadonlyRels (macosuser workspacereadonly.go) adds it to the write denies. Below
		// Apple Container 1.1.0 the bind is emitted and its suffix ignored, so the lock is lost
		// with the entries' own warning naming only the entries.
		Guide: []string{"`workspace_readonly` — lock workspace sub-paths",
			"… and the `yolo-jail.jsonc` lock it also performs"},
	},
	"per_side_paths": {
		PodmanLinux: honored("venvShadowMountArgs (mounts.go) binds a private wsState dir over " +
			"each path, the default .venv and node_modules included"),
		PodmanMac: honored("venvShadowMountArgs (mounts.go), the same binds inside the VM"),
		AppleContainer: honored("venvShadowMountArgs (mounts.go) emits the same binds for this " +
			"backend; unverified on hardware, since no apple-container.yml test mounts one"),
		MacosUser: warnedSaying("printPerSideDisclosure (macosuser orchestrator.go) prints the "+
			"notice naming every declared entry and each default the workspace uses "+
			"(perSideSharedPaths), then uv's redirect and the container step", Notice{
			Says: "per-side paths are SHARED with the host on macos-user",
			Then: "A container gives the host and the jail their own copy of each by mounting " +
				"over it, and this backend has no mount namespace, so the sandbox installs into " +
				"the same directories your host tools read.",
			By: "macosuser.printPerSideDisclosure",
		}),
		Guide: []string{"`per_side_paths`"},
	},
	"writable_home_dirs": {
		PodmanLinux: honored("podmanBaseMounts (assemble_parts.go) nests a read-write bind per " +
			"path inside the read-only home skeleton, whose mountpoint buildHomeSkeleton makes"),
		PodmanMac: honored("podmanBaseMounts (assemble_parts.go), the same binds"),
		AppleContainer: honoredBy("appleContainerBaseMounts (assemble_parts.go) binds this " +
			"workspace's whole wsState read-write at /home/agent, so every home path is writable"),
		MacosUser: honoredBy("the sandbox home is the sandbox account's own and writable " +
			"(macosuser.SandboxHome); unverified whether the declared dir is pre-created there"),
		Guide: []string{"`writable_home_dirs`"},
	},
	"ephemeral_storage": {
		PodmanLinux: honored("ScratchMountArgs (runmount.go) backs /tmp, /var/tmp and " +
			"/var/lib/containers with named volumes or tmpfs, as the key says"),
		PodmanMac: honored("ScratchMountArgs (runmount.go); the volumes sit on the VM's disk"),
		AppleContainer: dropped("appleContainerBaseMounts mounts tmpfs scratch whatever the key " +
			"says and prints nothing (run.go's scratch-volume branch is podman-only); ruled " +
			"silent in backend-parity.md §5.1"),
		MacosUser: notApplicable("no container scratch to back: the sandbox writes the Mac's own " +
			"/tmp and /var/folders (bootsteps.go's scratch_permissions is notDarwin); a tmpfs " +
			"request, which asks for RAM-backed scratch, is warned by noteMacosUserPlatformGaps " +
			"(loopholeinert.go)"),
		Guide: []string{"`ephemeral_storage`"},
	},
	"cache_relocations": {
		PodmanLinux: honored("podmanBaseMounts (assemble_parts.go) nests a read-write bind per " +
			"relocated segment inside ~/.cache; a provisioning failure refuses the launch"),
		PodmanMac: honored("podmanBaseMounts emits the same binds, and a source outside the " +
			"VM's share set refuses (machineshares.go); unverified on hardware, never measured " +
			"on a Mac"),
		AppleContainer: warnedSaying("appleContainerBaseMounts (assemble_parts.go) mounts no "+
			"relocation and prints the notice once for the set", Notice{
			Says: "cache_relocations are not implemented on Apple Container",
			Then: "The cache stays on its original filesystem; launch with `--setup podman-self` for " +
				"cache relocation.",
			By: "run.appleContainerBaseMounts",
		}),
		MacosUser: honoredBy("the bootstrap lays each relocation as a link at ~/.cache/<subdir> " +
			"that the profile opens (macosuser ctxlinks.go), read from the user config alone, and " +
			"printCacheRelocations (orchestrator.go) names each link and its target; unverified " +
			"on a Mac"),
		Guide: []string{"`cache_relocations`"},
	},
	"host_files": {
		PodmanLinux: honored("hostfiles.go stages each entry, a file source binds read-only, and " +
			"the boot's configure_host_files step writes the destination (bootsteps.go)"),
		PodmanMac: honored("the same staging and binds; a source must be in the VM's share set " +
			"(machineshares.go)"),
		AppleContainer: honoredBy("a file source is copied into wsState (acMaterialize, " +
			"helpers.go), where the agent can write it, and a directory source binds read-only " +
			"from 1.1.0 (roBindsUnsupported); the entrypoint's write (entrypoint hostfiles.go) " +
			"lands in wsState"),
		MacosUser: honoredBy("a file source is copied into a host-owned read-only tree (macosctxtree.go, " +
			"macosuser.StageCtxCommands) that YOLO_CTX_ROOT names, and the darwin bootstrap runs " +
			"the same configure_host_files step"),
		Guide: []string{
			"`host_files` modes",
			"`host_files` mode `capture`",
			"`host_files` source is a **file**",
			"`host_files` destination",
			"`host_files` entries with a `source:`",
		},
		Aspects: map[string]Entry{
			// A destination at the home root (`~/.npmrc`), outside every directory a pack or the
			// home layout makes per-workspace.
			"home_root_destination": {
				PodmanLinux: honored("buildHomeSkeleton (homeskeleton.go) links it into this " +
					"workspace's writable ~/.config overlay (config.HostFileEntry.SymlinkTarget), so " +
					"each workspace writes its own"),
				PodmanMac: honored("the same skeleton link (buildHomeSkeleton)"),
				AppleContainer: honoredBy("appleContainerBaseMounts (assemble_parts.go) binds this " +
					"workspace's own wsState at /home/agent, so a home-root file is per-workspace " +
					"with no link"),
				MacosUser: dropped("DeriveDarwinHomeLayout (entrypoint darwinhomelayout.go) links " +
					"only ~/.config, the installed-program surfaces and pack-declared dirs into the " +
					"workspace sidecar, and nothing redirects a host_files destination, so the " +
					"bootstrap's configure_host_files writes a home-root file into the sandbox " +
					"account home every workspace shares: one workspace's launch overwrites " +
					"another's, and nothing says so. Not a ruled decline — recorded as found"),
				Guide: []string{"`host_files` destination"},
			},
			"directory_source": {
				PodmanLinux: honored("hostfiles.go binds the directory read-only"),
				PodmanMac:   honored("the same bind, from the VM's share set (machineshares.go)"),
				AppleContainer: honored("bound read-only from Apple Container 1.1.0; below it, " +
					"or with an unreadable version, roBindsUnsupported skips it with a " +
					"'Skipping host_files directory' line"),
				MacosUser: honoredBy("copyCtxTreeConfined (macosctxtree.go) copies the directory into " +
					"the root-owned context tree, confined to its source and size-capped, with each " +
					"file's mode recorded beside it (context-mounts.md CX-D25, CX-D26); a copy over " +
					"the cap refuses the launch, naming the entry"),
				Guide: []string{"`host_files` source is a **directory**"},
			},
		},
	},
	"host_import_exclusions": everywhere(honoredBy("LoadHostImportExclusions (config hostimportexclusions.go) reads only user scope and carries its policy through HostLayerWire (entrypoint hostlayerlabel.go); renderPlannedSurface (entrypoint packsurfaces.go) applies FilterHostImportExclusions (agentcfg hostimportexclusions.go) only to delivered user host bytes"),
		"`host_import_exclusions` — filter imported user host config"),
	"host_management":      hostSideKey("`yolo host apply` (render's hostOwnership)"),
	"host_wrappers":        hostSideKey("`yolo host apply`, which writes the wrappers"),
	"host_apply_on_launch": hostSideKey("a wrapped `yolo host -- <agent>` (hostapplygate.go)"),
	"host_floor":           hostSideKey("the host floor (config.HostFloorWire)"),
	"host_path":            hostSideKey("`yolo host`'s tool lookup (internal/hostpath)"),
	"provisioners":         hostSideKey("`yolo check-deps`, `yolo host apply` and the host floor (config.ProvisionerOrder)"),
	"promotion_target":     hostSideKey("`yolo config promote`"),
	"shipped_skill_prefix": everywhere(honored("read from user scope by the host when it stages "+
		"skills (config.ShippedSkillPrefix, in hostskills.PackLayer and jailcontent's built-in "+
		"suite), above the backend's delivery of them"), "`shipped_skill_prefix`"),
	"programs": {
		PodmanLinux: honored("assembleRunCmd passes YOLO_PROGRAMS_AUTOPRUNE=1 when the user " +
			"config asks (config.ProgramsAutoprune), and the boot's catalog step removes orphans " +
			"(entrypoint orphanremove.go)"),
		PodmanMac:      honored("the same env and boot step (assemble.go, orphanremove.go)"),
		AppleContainer: honored("the same env and boot step (assemble.go, orphanremove.go)"),
		MacosUser: honored("the darwin bootstrap runs the same catalog_installed_orphans step " +
			"(bootsteps.go), and BuildRunPlanWithDaemons (macosuser runplan.go) relays the user " +
			"config's autoprune to the bootstrap alone (notch-convergence.md NC-D71); unmeasured " +
			"on a Mac"),
		Guide: []string{"`programs: { autoprune: true }`"},
		Aspects: map[string]Entry{
			// programs.overrides (docs/design/pack-buildable-programs.md §5): a program a pack builds
			// from its own tree, built from a host checkout the user is developing instead.
			"overrides": {
				PodmanLinux: honored("packTreePins (packtreepins.go) resolves each pack-tree program " +
					"through ResolvePackTreeProgram (programoverrides.go), which puts the checkout in place " +
					"of the staged `from`; the fork route's slot builds it in the sealed build jail keyed " +
					"on the checkout's tree digest, and noteCheckoutDeliveries names it on every fresh " +
					"launch; an override of any other program refuses the launch " +
					"(refuseNonPackTreeOverrides)"),
				PodmanMac: honored("the same resolution and build, the build jail running inside the " +
					"Podman Machine VM from the host's staged copy of the checkout"),
				AppleContainer: honored("the same resolution and build, through the same build slot; " +
					"unmeasured on a Mac"),
				MacosUser: honoredBy("the same resolution, the build the darwin sealed act " +
					"(forkbuild.go's macos-user arm, which copies the checkout as it copies a pack's tree); " +
					"unmeasured on a Mac"),
				Guide: []string{"`programs: { overrides: … }` — build a program from your own checkout"},
			},
		},
	},

	// ---- Resources, devices and networking ---------------------------------------------
	"resources": {
		PodmanLinux: honored("appliedResourceLimits (backendcaps.go) → resourceArgs " +
			"(assemble_parts.go) passes --memory, --cpus and --pids-limit"),
		PodmanMac: honored("the same flags (appliedResourceLimits), enforced inside the VM and " +
			"clipped to its size"),
		AppleContainer: honored("appliedResourceLimits' container branch (backendcaps.go) passes " +
			"--memory and --cpus, with host-derived defaults when unset; its aspects part ways"),
		MacosUser: honoredBy("printResourceDispositions (macosuser orchestrator.go) says how each " +
			"is honored: cpus by the cooperative parallelism defaults (CooperativeCPUs), memory by " +
			"the sampled session guard (SessionGuardFor), neither kernel-enforced"),
		Guide: []string{"`resources.memory`", "`resources.cpus`"},
		Aspects: map[string]Entry{
			"pids_limit": {
				PodmanLinux: honored("appliedResourceLimits (backendcaps.go) always passes " +
					"--pids-limit, 32768 unless set"),
				PodmanMac: honored("the same flag (appliedResourceLimits)"),
				AppleContainer: dropped("appliedResourceLimits passes no pids limit for this " +
					"backend and nothing says so; ruled silent in backend-parity.md §5.1"),
				MacosUser: warnedSaying("printResourceDispositions (macosuser orchestrator.go) prints "+
					"the notice naming it, with any other key no mechanism here acts on "+
					"(unenforcedResourceKeys)", Notice{
					Says: "resources are NOT enforced on macos-user",
					Then: "macOS has no cgroups and there is no VM to size, so these are read and " +
						"ignored; the agent runs with your user's own limits.",
					By: "macosuser.printResourceDispositions",
				}),
				Guide: []string{"`resources.pids_limit`"},
			},
			"io": {
				PodmanLinux: honored("appliedIOPriority (backendcaps.go) passes the class to " +
					"the entrypoint, and noteIOPriority (iopriority.go) names a disk that " +
					"ignores it"),
				PodmanMac: warned("noteIOPriority (iopriority.go) says the VirtioFS crossing " +
					"carries no priority, and appliedIOPriority passes none"),
				AppleContainer: warned("noteIOPriority (iopriority.go), the same VirtioFS line"),
				MacosUser: honoredBy("applyDiskIOPolicy (macosuser orchestrator.go) sets the session's macOS " +
					"disk priority; a set that fails is said at launch"),
				Guide: []string{"`resources.io`"},
			},
		},
	},
	"devices": {
		PodmanLinux: honored("deviceArgs (assemble_parts.go) passes raw " +
			"paths, usb: and cgroup_rule entries"),
		PodmanMac: warned("deviceArgs (assemble_parts.go) prints 'device passthrough … not " +
			"supported on macOS — skipping' per entry, by host OS, and passes nothing"),
		AppleContainer: warned("deviceArgs' same per-entry line (assemble_parts.go); measured " +
			"2026-09-16 that `container run` 1.1.0 has no --device at all"),
		MacosUser: warnedSaying("noteMacosUserPlatformGaps (loopholeinert.go) discloses each raw "+
			"/dev path the profile allows device control on (macosuser.DeviceIoctlPaths), warns "+
			"per raw entry it refuses, and prints the notice naming the usb and cgroup_rule "+
			"entries, which nothing here reads", Notice{
			Says: "`devices` USB and cgroup entries are not read on macos-user",
			Then: "A USB entry attaches a device to a CONTAINER, and a cgroup rule lets a " +
				"container's device cgroup open the device numbers it matches; this backend " +
				"starts no container, so neither does anything here. A USB device is reached " +
				"through macOS itself, which yolo neither attaches nor restricts. To drive a " +
				"serial adapter from the sandbox, list its /dev/cu.* node instead.",
			By: "run.noteMacosUserPlatformGaps",
		}),
		Guide: []string{"`devices`"},
	},
	"gpu": {
		PodmanLinux: honored("gpuArgs (helpers.go), which assembleRunCmd calls, passes the CDI " +
			"device or the AMD device nodes and the vendor's driver env"),
		PodmanMac: warned("gpuHostAvailable and rocmHostAvailable (hostprobes.go) answer no on " +
			"macOS, and assembleRunCmd prints 'GPU requested but … — starting without GPU " +
			"passthrough'"),
		AppleContainer: warned("the same probes answer no for this runtime (hostprobes.go), and " +
			"assembleRunCmd prints the same 'GPU requested but' line"),
		MacosUser: warnedSaying("noteMacosUserPlatformGaps (loopholeinert.go) prints the "+
			"notice when gpu.enabled is set", Notice{
			Says: "`gpu.enabled` is not read on macos-user",
			Then: "GPU passthrough is a CDI device plus NVIDIA/ROCm environment on a container, " +
				"and this backend starts none. yolo passes nothing through and gates nothing; " +
				"whatever the sandboxed process can reach through macOS, it reaches.",
			By: "run.noteMacosUserPlatformGaps",
		}),
		Guide: []string{"`gpu` — `vendor: nvidia`", "`gpu` — `vendor: amd`"},
	},
	"kvm": {
		PodmanLinux: honored("kvmArgs (assemble_parts.go) passes /dev/kvm and keep-groups"),
		PodmanMac: warned("kvmArgs (assemble_parts.go) prints 'kvm passthrough is not " +
			"supported on this runtime' on macOS"),
		AppleContainer: warned("kvmArgs (assemble_parts.go), the same line"),
		MacosUser: warnedSaying("noteMacosUserPlatformGaps (loopholeinert.go) prints the "+
			"notice when kvm is set", Notice{
			Says: "`kvm` is not read on macos-user",
			Then: "it asks for /dev/kvm inside a container, and there is neither a container " +
				"nor a /dev/kvm on macOS.",
			By: "run.noteMacosUserPlatformGaps",
		}),
		Guide: []string{"`kvm`"},
	},
	// The parent is the default bridge mode; the explicit host mode and the two port keys
	// part ways from it.
	"network": {
		PodmanLinux: honored("no --net selector, so podman's own namespace; on a rootless host " +
			"decideHostLoopback (hostloopback.go) adds the loopback forwarding option"),
		PodmanMac: honored("the same, a namespace inside the Podman Machine VM"),
		AppleContainer: honoredBy("Apple Container gives each container its own vmnet network " +
			"and takes no selector (appliedNetMode answers bridge for it, backendcaps.go)"),
		MacosUser: refused("NetworkRefusal (networkrefusal.go) refuses a written or typed bridge " +
			"before the launch builds anything, naming --setup podman-self and the unset (SU-R5); " +
			"an unwritten mode is the Mac's own network, which the briefing states"),
		Guide: []string{"`network.mode: \"bridge\"`"},
		Aspects: map[string]Entry{
			"mode_host": {
				PodmanLinux: honored("assembleRunCmd emits --net=host, and the port keys drop " +
					"with it as the user's own declaration"),
				PodmanMac: dropped("--net=host is applied to the VM's namespace, not the Mac's, " +
					"and nothing says so (guide's hostmac); not a ruled decline"),
				AppleContainer: refused("NetworkRefusal (networkrefusal.go) refuses a written or " +
					"typed host before the launch builds anything, naming --setup podman-self and " +
					"the unset (SU-R5)"),
				MacosUser: honoredBy("the sandbox already shares the launcher's stack " +
					"(sharesLauncherNetns), which is what host mode asks for"),
				Guide: []string{"`network.mode: \"host\"`"},
			},
			"ports": {
				PodmanLinux: honored("assembleRunCmd passes -p per entry under the bridge mode, " +
					"with the route_localnet DNAT fixup"),
				PodmanMac: honored("the same -p; measured to answer the Mac for a 0.0.0.0 " +
					"listener (guide's vm-limits)"),
				AppleContainer: honored("assembleRunCmd passes -p ungated; measured 2026-10-03 " +
					"(apple-container.yml run 37133569003, backend-parity.md §5.4 #10) answering " +
					"the Mac on 127.0.0.1 in both modes. Earlier runs saw no data, which §5.4 " +
					"traces to macOS Local Network privacy for Apple's ad-hoc-signed helpers, and " +
					"no launch line says so on a Mac where that recurs"),
				MacosUser: honoredBy("the sandbox shares the launcher's stack, so a port it binds is " +
					"already on the Mac's interfaces, and planMacosUserPortRelays " +
					"(macosuserportrelay.go) relays a remap over TCP; noteMacosUserPortKeys " +
					"(loopholeinert.go) warns that listing a port confines nothing"),
				Guide: []string{"`network.ports`"},
			},
			"forward_host_ports": {
				PodmanLinux: honored("forwardHostPortsArgs (assemble_parts.go) and the host-side socat " +
					"lifecycle (hostports.go); needs socat on the host"),
				PodmanMac: honored("the same socat hop, reached through the VM; unverified on " +
					"hardware, no macOS run dials one"),
				AppleContainer: refused("appleContainerForwardRefusal (hostports.go) refuses the " +
					"launch before anything starts, naming the key, each entry and `--setup podman-self`: " +
					"the backend carries no container-to-Mac traffic and its --publish-socket forwards " +
					"the other way, so there is no forward to deliver. Until 2026-10-05 the argv " +
					"carried --publish-socket and `container run` 1.1.0 rejected it, naming a socket " +
					"(measured 2026-09-16, guide's acfwd), which no disposition described"),
				MacosUser: honoredBy("a same-port entry already holds on the shared stack, and " +
					"planMacosUserPortRelays (macosuserportrelay.go) relays a remap over the Mac's " +
					"loopback; noteMacosUserPortKeys (loopholeinert.go) discloses both, and warns for " +
					"a remap it does not relay"),
				Guide: []string{"`network.forward_host_ports`"},
			},
		},
	},

	// ---- Packages, tools and agent configuration ---------------------------------------
	"packages": {
		PodmanLinux: honored("the image build hands the list to the flake as YOLO_EXTRA_PACKAGES " +
			"(internal/image stockimage.go), or the store farm under YOLO_STORE_PACKAGES=1 " +
			"(storepackages.go)"),
		PodmanMac: honored("the same Linux image build; a binary-cache miss starts the Linux " +
			"builder container (internal/containerbuilder)"),
		AppleContainer: honored("the same Linux image build through the same builder " +
			"(internal/containerbuilder)"),
		MacosUser: honoredBy("darwinpkg.Materialize builds a native darwin profile over the " +
			"floor before the sandbox starts (macosuser.RunMacosUser); no image"),
		Guide: []string{"`packages` (nix packages on PATH)", "`packages` per-entry `platforms`"},
		Aspects: map[string]Entry{
			// An entry that cannot build: the outcome is the launch's, on every setup.
			"unbuildable_entry": {
				PodmanLinux: refused("a failed image build stops the launch with nix's error; " +
					"no stale-image fallback without YOLO_ALLOW_STALE_IMAGE (internal/image)"),
				PodmanMac: refused("the same fatal build, through the Linux builder " +
					"(internal/image)"),
				AppleContainer: refused("the same fatal build, through the Linux builder " +
					"(internal/image)"),
				MacosUser: refused("macosuser.RunMacosUser aborts when darwinpkg's build fails, " +
					"naming the real target"),
				Guide: []string{"`packages` entry that cannot build"},
			},
		},
	},
	"mise_tools": {
		PodmanLinux: honored("the boot's generate_mise_config step composes mise's config " +
			"(bootsteps.go); the tool store is a host bind at /mise (podmanBaseMounts)"),
		PodmanMac: honored("the same step; the store is a podman volume inside the VM " +
			"(podmanBaseMounts' isMacOS branch)"),
		AppleContainer: honored("the same step; the store is a volume inside Apple Container's VM " +
			"(appleContainerBaseMounts)"),
		MacosUser: honoredBy("the floor puts mise on the sandbox PATH and the confined " +
			"provisioning stage runs `mise install` (macosuser provision.go); one store for " +
			"the whole machine"),
		Guide: []string{"`mise_tools`"},
	},
	"mcp_servers": {
		PodmanLinux: honored("LoadMCPServers (entrypoint mcp.go) composes them per agent at " +
			"boot from the launch's payload (assembleRunCmd), and loadMCPTables (entrypoint " +
			"mcp.go) evaluates each requires_env gate per agent against that agent's own env " +
			"file, naming every server it skips"),
		PodmanMac:      honored("the same payload and generator (assemble.go, entrypoint mcp.go)"),
		AppleContainer: honored("the same payload and generator (assemble.go, entrypoint mcp.go)"),
		MacosUser: honored("BuildRunPlan (macosuser runplan.go) hands the same section to the " +
			"darwin bootstrap, which runs the same generator and the same per-agent gate: " +
			"configure_pack_surfaces is not notDarwin, and the gate reads the env files " +
			"writeMacosUserAgentEnvFiles (agentenvfiles.go) writes into the sidecar the home " +
			"layout links ~/.config to; a `command` must exist on the Mac"),
		Guide: []string{"`mcp_servers`", "`mcp_servers.requires_env`"},
	},
	"lsp_servers": everywhere(honored("rendered into each agent's config by the boot's surface "+
		"loop from the launch's payload (assembleRunCmd; BuildRunPlan on macos-user); no setup "+
		"installs a server, so the `command` must already be on PATH (OQ-LSP1)"), "`lsp_servers`"),
	"security": {
		PodmanLinux: honored("GenerateShims (entrypoint shims.go) writes each blocked tool's " +
			"refusal at the head of PATH from the launch's payload"),
		PodmanMac:      honored("the same GenerateShims step (entrypoint shims.go)"),
		AppleContainer: honored("the same GenerateShims step (entrypoint shims.go)"),
		MacosUser: honored("BuildRunPlan (macosuser runplan.go) hands the blocked tools to the " +
			"darwin bootstrap, which runs the same generate_shims step; the block measures the " +
			"sandbox PATH, so it replaces the Mac's BSD tool"),
		Guide: []string{"`security.blocked_tools`"},
	},
	"packs": {
		PodmanLinux: honored("stageRunPacks stages the selected packs into this launch's own tree " +
			"(packtree.go), bound read-only"),
		PodmanMac: honored("the same staged tree (packtree.go); the workspace must be in the " +
			"VM's share set"),
		AppleContainer: honoredBy("the tree is a per-launch copy under ws_state rather than a " +
			"read-only bind (assembleRunCmd's container branch), so a pack edit waits for a " +
			"restart"),
		MacosUser: honoredBy("the staged tree is named by YOLO_PACK_ROOT and rendered by the " +
			"darwin bootstrap; skills and briefings arrive by copy (buildMacosHomeOverlay), and " +
			"what cannot run is declined by name (noteMacosUserJailDaemonDeclines)"),
		Guide: []string{"`packs`"},
	},
	"providers": everywhere(honored("composedProviders (assemble.go) composes the table the "+
		"channel carries, above the dispatch; on macos-user composedProvidersFor composes a "+
		"launch-owned service's conversions at the ports that launch picked "+
		"(macosuserservices.go)"), "`providers`"),
	"profiles": everywhere(honored("launchChannel → composePackChannel (seal.go, profilechannel.go) resolves the selection "+
		"over them above the dispatch"), "`profiles`"),
	"profile": everywhere(honored("launchChannel → composePackChannel (seal.go, profilechannel.go) composes the selection "+
		"above the dispatch; a `-p` the launch cannot honor refuses on every setup "+
		"(printProviderRefusal)"), "`profile` †"),
	"agent_updates": {
		PodmanLinux: honored("assembleRunCmd passes config.AgentUpdatesWire as " +
			"entrypoint.AgentUpdatesEnv on every launch, which the launchers read"),
		PodmanMac:      honored("the same env (assemble.go)"),
		AppleContainer: honored("the same env (assemble.go)"),
		MacosUser: honored("BuildRunPlan (macosuser runplan.go) sets the same AgentUpdatesEnv " +
			"for the darwin bootstrap's launchers"),
		// Two rows: the key, and the background update its "next-launch" value asks for, which
		// the page footnotes on its own and which works wherever the key does.
		Guide: []string{"`agent_updates` †", "`agent_updates` set to `\"next-launch\"`"},
	},
	"env_sources": {
		PodmanLinux: honored("composePackChannel hydrates the files through the credential gate into " +
			"the channel (profilechannel.go), whose env file is a live bind"),
		PodmanMac: honored("the same channel and bind (profilechannel.go)"),
		AppleContainer: honoredBy("deliverChannel rewrites the env files under the wsState " +
			"bind on every entry instead of binding them live (agentenvfiles.go)"),
		MacosUser: honoredBy("the channel crosses in the session env file " +
			"(writeMacosUserAgentEnvFiles; macosuser buildPlan layers PackEnv), read fresh at " +
			"each launch"),
		Guide: []string{"`env_sources`"},
	},
	"loopholes": {
		PodmanLinux: honored("startLoopholesDisclosed (packloopholes.go) starts each " +
			"enabled host daemon, and the jail learns its endpoint"),
		PodmanMac: honored("the same host daemons, reached from the VM at " +
			"host.containers.internal; the Linux-only ones are named inert " +
			"(notePackLoopholesInert)"),
		AppleContainer: warned("loopholeAllow (loopholesruntime.go) starts only the OpenAI " +
			"service, plus the Claude broker when the launch opts in to the credential view " +
			"(CL-D11, off by default: claudeview.DefaultOn), and notePackLoopholesInert (loopholeinert.go) names every other enabled " +
			"loophole at launch"),
		MacosUser: honoredBy("startLoopholesDisclosed starts every admitted host daemon and " +
			"macosuser.EndpointGrantCommands ACL-grants each endpoint; the jail halves run in " +
			"the sandbox, and the ones it cannot run are declined by name " +
			"(noteMacosUserJailDaemonDeclines)"),
		Guide: []string{"`loopholes`"},
	},
	"brokered": {
		PodmanLinux: honored("writeScopeFiles (brokeredscope.go) writes the widened scope for " +
			"the GitHub broker this launch starts (config.BrokeredWidening)"),
		PodmanMac: honored("the same scope file, written host-side (brokeredscope.go)"),
		AppleContainer: warned("loopholeAllow (loopholesruntime.go) does not start the GitHub " +
			"broker here, so nothing reads the widening; notePackLoopholesInert names the " +
			"loophole, not this key"),
		MacosUser: honored("the macos-user arm's startLoopholesDisclosed reaches " +
			"startLoopholesMatching (loopholesruntime.go), which writes the same scope file " +
			"(writeScopeFiles); loopholeAllow admits every loophole on this backend"),
		Guide: []string{"`requests.github.repos`"},
	},
	"on_request": {
		PodmanLinux: honored("the GitHub broker's host daemon (ghbroker.Main) reads it from the user " +
			"config (config.OnRequest) and runs it per request its own lifetime files (brokerhook.Runner)"),
		PodmanMac: honored("the same host daemon, on the Mac (ghbroker.Main)"),
		AppleContainer: warned("loopholeAllow (loopholesruntime.go) does not start the GitHub " +
			"broker here, so nothing runs the hook; notePackLoopholesInert names the loophole"),
		MacosUser: honored("the same host daemon, started by startLoopholesMatching " +
			"(loopholesruntime.go), runs it as the invoking user"),
		Guide: []string{"`on_request`"},
	},
	"required_capabilities": everywhere(honored("refuseUnmetCapabilities runs inside "+
		"loadAndValidateConfig, above the dispatch, so a capability nothing satisfies refuses "+
		"the same way on every setup (preflight.go)"), "`required_capabilities`"),
	"model_facts": everywhere(honored("composedProviders (assemble.go), its macos-user variant "+
		"composedProvidersFor and the host's composedHostProviders pass it to the provider "+
		"composition (packload.WithModelFacts) above the dispatch"), "`model_facts`"),
	"adapters": everywhere(honored("composedProviders reads config.LoadAdapterAddresses from "+
		"the user file above the dispatch (assemble.go); on macos-user a launch-owned "+
		"service's conversions take the launch's own ports instead of an override "+
		"(composedProvidersFor, OQ-HS4)"), "`adapters`"),
	"agents_md_extra": {
		PodmanLinux: honored("prepare.go folds it into the briefing the host composes, bound " +
			"read-only into the jail"),
		PodmanMac: honored("the same briefing and bind (prepare.go)"),
		AppleContainer: honoredBy("the briefing is a per-launch copy under the wsState bind " +
			"(acMaterialize), which a re-entry does not refresh (attachskewbriefing.go)"),
		MacosUser: honoredBy("the briefing is copied into the sandbox home by the bootstrap's " +
			"install_home_overlay (buildMacosHomeOverlay) and write-protected by the profile"),
		Guide: []string{"`agents_md_extra`"},
	},
	"briefing_provenance": everywhere(honored("prepare.go reads config.BriefingProvenance when "+
		"it composes the briefing, above the backend's delivery of it"), "`agents_md_extra`"),
	"include_if_found": everywhere(honored("resolved by the config loader (config load.go) "+
		"into the user scope every launch reads, before the setup is chosen"),
		"`include_if_found`"),
	"prune": everywhere(honored("`yolo check`'s disk section warns at this free-disk "+
		"threshold on the host (check sections_misc.go); no jail reads it"), "`include_if_found`"),
	"perf_logging": everywhere(honored("fillDefaults reads config.PerfLoggingEnabled once "+
		"(runcmd.go) and Run's initPerf starts the collector before the setup is chosen"),
		"`include_if_found`"),
	"local_clones": everywhere(honored("the host's pack check resolves a patch branch URL against a "+
		"registered clone (packsrc localclones.go, gathered by config.LocalClones from the user config); "+
		"no jail reads it"), "`local_clones`"),
	"update_check": everywhere(honored("gates this machine's own update check "+
		"(config.UpdateCheckEnabled); no jail reads it"), "`include_if_found`"),
	"macos_user_shared_tmp": {
		PodmanLinux:    notApplicable("ignored: not this backend; only the macos-user backend reads it"),
		PodmanMac:      notApplicable("ignored: not this backend; only the macos-user backend reads it"),
		AppleContainer: notApplicable("ignored: not this backend; only the macos-user backend reads it"),
		MacosUser: honored("macosUserRun (cli/commands.go) reads it from the user config (config.MacosUserSharedTmp) " +
			"and hands it to the backend (macosuser.Options.SharedTmp), whose run-as-you profile then grants " +
			"the shared /tmp (TmpShared)"),
		Guide: []string{"`macos_user_shared_tmp`"},
	},
	"share_host_fonts": {
		PodmanLinux: honored("assembleRunCmd binds ~/.local/share/fonts (or $XDG_DATA_HOME/fonts) " +
			"and ~/.fonts read-only under the jail's ~/.local/share/fonts, each with its disclosure " +
			"line, and skips a source resolving outside your home or into a credential folder " +
			"(run hostfonts.go), read from the user config alone (config.ShareHostFonts)"),
		PodmanMac: honored("~/Library/Fonts is bound the same way (hostfonts.go); a folder the Podman " +
			"Machine does not share is skipped with a line rather than refusing the launch; " +
			"unverified on a Mac"),
		AppleContainer: honored("~/Library/Fonts is bound the same way (hostfonts.go), refused with a " +
			"line below the read-only bind floor (roBindsUnsupported); unverified on a Mac"),
		MacosUser: honoredBy("the separate-account mode reads a yolo-owned copy of ~/Library/Fonts " +
			"that its fonts.conf and profile name (macosuser stageFontCopy, fontcopy.go), never the " +
			"folder in your home; the run-as-you mode reads ~/Library/Fonts always, so the key " +
			"changes nothing there; unverified on a Mac"),
		Guide: []string{"`share_host_fonts`"},
	},
}
