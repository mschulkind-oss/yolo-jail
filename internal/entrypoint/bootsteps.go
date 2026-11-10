package entrypoint

// bootsteps.go is THE boot step table: every step either boot runs — the container
// entrypoint (Main) and the macos-user bootstrap (RunDarwinBootstrap) — in the one order
// both run them, with each step a boot does NOT run declared as an exclusion that says why.
//
// There used to be two lists, Main's sequence of calls and RunDarwinBootstrap's, and a step
// added to one had to be remembered in the other. The omissions were silent: nothing in
// either list said that the orphan catalog and the program reconcile were missing from the
// darwin one, and the reason each file gave for it ("macos-user stages no pack tree") had
// stopped being true when that backend began staging one (docs/plans/notch-convergence.md,
// row D10). An omission is now a line with a reason on it, and TestEveryBootStepRunsOrSaysWhy
// refuses a step that runs nowhere, or is excluded without one.
//
// WHAT STAYS OUT OF THE TABLE is what is not a step of the boot's content: Main's I/O
// priority re-exec, its boot log, the refusal gate and the exec that hands over control, all
// of which only a container boot has, and which bracket the table rather than sit in it.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bootTarget names which boot a step table run is: the container entrypoint or the
// macos-user bootstrap.
type bootTarget int

const (
	bootContainer bootTarget = iota
	bootDarwin
)

func (t bootTarget) String() string {
	if t == bootDarwin {
		return "macos-user"
	}
	return "container"
}

// bootStep is one step of the boot. Exactly one of gen and run is set: gen is a generator,
// run through genStep under the step's label (a failure is fatal to the boot, A12); run is
// anything else — an informational report, a side effect with its own error handling, a
// runtime setup step.
type bootStep struct {
	// name is the step's identity, and the genStep label unless label is set.
	name string
	// label overrides the genStep label, where a step's failure line predates the table and
	// is kept verbatim.
	label string
	gen   func(e *Env) error
	run   func(b *bootRun)
	// perf is the container perf-log label marked after the step, when it is not name;
	// noMark marks nothing (the next step's mark covers this one).
	perf   string
	noMark bool
	// notContainer and notDarwin are the declared exclusions: non-empty means that boot does
	// not run the step, and the text is why.
	notContainer string
	notDarwin    string
	// notSessionPass, non-empty, says why a SESSION'S PASS does not run the step: an exec into a
	// container jail whose main process is a hold, the first session included, runs the table
	// again for itself, and a step that belongs to the jail's own boot alone says so here. The
	// text is why. The main process's boot, a jail with no hold, and the macos-user bootstrap
	// (which has no attach) run it.
	notSessionPass string
}

// excludedFrom returns why t does not run the step, or "" when it does.
func (s bootStep) excludedFrom(t bootTarget) string {
	if t == bootDarwin {
		return s.notDarwin
	}
	return s.notContainer
}

// bootRun is one run of the table: the Env, which boot it is, and what the steps share.
type bootRun struct {
	e      *Env
	target bootTarget
	// sessionPass is whether this run is a session's pass rather than the jail's own boot: an
	// exec into a container whose main process is a hold (Main passes gate != nil). The steps
	// that declare notSessionPass are skipped on it.
	sessionPass bool
	// darwin carries the macos-user bootstrap's own inputs.
	darwin DarwinBootstrapOptions
	// perf is the boot's step marks: the container's ~/.yolo-perf.log, and the macos-user
	// bootstrap's boot-perf.log for its launcher (writeBootPerf); nil marks nothing.
	perf *perfLog

	packsLoaded bool
	packs       []*packload.Pack
	packErr     error

	// sealed is whether the launcher made this boot a sealed build jail's (sealedbuild.go),
	// read by runSteps before any step runs: hydrate_user_env, the first, folds a channel a
	// selected pack and the user's env_sources write into the same Vars, and only the launcher
	// may say a jail is a sealed build.
	sealed bool

	// egress is the outbound-network probe start_egress_probe launched in the background, which
	// report_egress collects (egress.go); nil when the start step did not run.
	egress *egressProbe
}

// jailPacks loads the staged packs once per run, on first use. The macos-user bootstrap
// first asks at darwin_home_layout (the pack-declared tier lists ARE the layout, and it runs
// above every generator), the container boot at configure_pack_surfaces, which is where each
// loaded them when the two boots were two lists.
func (b *bootRun) jailPacks() ([]*packload.Pack, error) {
	if !b.packsLoaded {
		b.packs, b.packErr = LoadJailPacks(b.e)
		b.packsLoaded = true
	}
	return b.packs, b.packErr
}

// runBootSteps runs every step of the table that b's boot runs, in table order, marking the
// perf log after each on the container boot.
func runBootSteps(b *bootRun) { runSteps(b, bootSteps()) }

// runSteps is runBootSteps over a given table, split out so the runner's own rules can be
// driven over a synthetic table.
func runSteps(b *bootRun, steps []bootStep) {
	b.sealed = b.e.launchedSealed(b.target) || (b.target == bootDarwin && b.darwin.Sealed)
	for _, s := range steps {
		if s.excludedFrom(b.target) != "" || (b.target == bootContainer && b.sessionPass && s.notSessionPass != "") {
			continue
		}
		switch {
		case s.gen != nil:
			label := s.label
			if label == "" {
				label = s.name
			}
			gen := s.gen
			genStep(b.e, label, func() error { return gen(b.e) })
		case s.run != nil:
			s.run(b)
		}
		if b.perf != nil && !s.noMark {
			mark := s.perf
			if mark == "" {
				mark = s.name
			}
			b.perf.mark(mark)
		}
	}
}

// bootSteps is the table. A function rather than a package var, because several steps close
// over package vars tests redirect.
func bootSteps() []bootStep {
	return []bootStep{
		{
			// Hydrate env_sources values before any configure_* so MCP env ${VAR}
			// interpolation sees them. bash sources the same file again at shell time.
			// The jail's --with-credentials grant after it, from its own per-launch file
			// (hydrateEnvFromGrantFile), which no entry rewrites.
			name: "hydrate_user_env",
			run: func(b *bootRun) {
				hydrateEnvFromUserEnvFile(b.e)
				hydrateEnvFromGrantFile(b.e)
			},
			notDarwin: "this backend writes no ~/.config/yolo-user-env.sh: its composed " +
				"environment rides the root-owned session env file, which hydrate_session_env reads",
		},
		{
			// The macos-user twin of hydrate_user_env, in the same slot for the same reason: the
			// requires_env gate in every configure_* step below asks the environment the agent
			// will have, and on this backend that is the session env file the launch installed
			// before this bootstrap ran. Read into e.Vars only, never the process environment
			// (hydrateEnvFromSessionEnvFile says why).
			name:         "hydrate_session_env",
			run:          func(b *bootRun) { hydrateEnvFromSessionEnvFile(b.e) },
			notContainer: "the container boot reads its composed environment from ~/.config/yolo-user-env.sh (hydrate_user_env, above)",
		},
		{
			// THE OUTBOUND-NETWORK PROBE STARTS HERE and is collected at report_egress, at the
			// bottom (egress.go), so its few seconds of worst case overlap the boot's own work
			// instead of adding to it. After the env hydration on purpose: HTTPS_PROXY and
			// NO_PROXY may come from env_sources, which those steps fold into e.Vars. It reads
			// the packs (their declared hosts are its targets), which only moves their one load
			// earlier; a load failure is still reported at configure_pack_surfaces.
			name: "start_egress_probe",
			run:  startEgressProbeStep,
			notSessionPass: "the finding is the jail's, said once by its own boot on the launch " +
				"terminal; a session's pass joins a jail whose launch already reported it",
		},
		{
			// Populate /run/localtime + /run/timezone from $TZ before anything else.
			name:      "configure_timezone",
			run:       func(b *bootRun) { configureTimezone(b.e) },
			notDarwin: "the sandbox user runs on the Mac's own clock and zoneinfo; there is no /run to populate",
		},
		{
			// Ensure scratch directories (/tmp and /var/tmp) are mode 1777.
			name:      "scratch_permissions",
			run:       func(b *bootRun) { configureScratchPermissions(b.e) },
			notDarwin: "the sandbox writes the Mac's own /tmp and /var/folders, which are not yolo's to chmod",
		},
		{
			// THE HOME LAYOUT, ABOVE EVERY GENERATOR — the workspace tier macos-user otherwise
			// has no way to express (docs/reference/macos-user-home-tiers.md, "The layout: what is a symlink, what is a mirror").
			// Above generate_shims and not merely before the pack hooks, because ~/.yolo/bin is
			// itself one of the links and GenerateShims writes THROUGH it: a shim generated into
			// the account home before the link was laid would be a blocker in the wrong tier,
			// and a link laid over the directory it had just created would refuse (the layout
			// never removes a real directory — OQ-HT2). The packs load here, because the two
			// pack-declared tier lists ARE the layout; a failure to load them is still reported
			// at load_packs, in configure_pack_surfaces, so the boot log reads in its old order.
			name: "darwin_home_layout",
			run: func(b *bootRun) {
				packs, _ := b.jailPacks()
				genStep(b.e, "darwin_home_layout", func() error { return InstallDarwinHomeLayout(b.e, packs) })
			},
			notContainer: "the container's home tiers are the launcher's bind mounts",
		},
		{
			// C4/C5's jail half: link the store-delivered package profiles into the
			// /run/yolo/packages farm. FIRST AMONG THE GENERATORS, and both steps below depend
			// on that: generate_ld_cache scans the farm's lib dir, so a cache built before the
			// farm exists omits every store-delivered library (flake.nix states the same
			// ordering for its own user-package loop); and generate_agent_launchers asks
			// imageProbePath whether a name is already provided, which answers by stat'ing the
			// file, so a launcher generated before the farm exists would shadow a tool the
			// workspace declared by name — defect 11.1 coming back through the door C4 opens.
			name: "generate_store_packages",
			gen:  GenerateStorePackages,
			notDarwin: "store-delivered packages are a podman-on-Linux launch mode (YOLO_STORE_PACKAGES); " +
				"macos-user takes `packages:` from its own native nix build",
		},
		{
			// The selected Mesa outputs carry lib/ and share/ trees but do not install
			// them at the NixOS paths graphics loaders expect. Materialize that view only
			// after store profiles exist, and only for a GPU-enabled container boot.
			name:           "configure_graphics_drivers",
			gen:            GenerateGraphicsDrivers,
			notDarwin:      "macos-user runs no container GPU exposure boot step",
			notSessionPass: "the launch boot owns /run graphics trees and provenance; attached sessions reuse them",
		},
		{
			// Put the image's glibc-free LD_LIBRARY_PATH farm (ImageLDLib: the C++ runtime,
			// zlib and the `packages:` libs) on LD_LIBRARY_PATH, so a nix-built process such as
			// the image's python3 finds them by bare soname without /lib (and its glibc) on the
			// search path. After generate_store_packages so that the baked farm is searched
			// first — R2's "baked wins".
			name:      "export_image_ld_lib",
			run:       exportImageLDLibStep,
			notDarwin: "macos-user bakes no image farm, and dyld does not read LD_LIBRARY_PATH",
		},
		{
			// Keep the podman settings a nested podman needs in force over a user
			// containers.conf (podmanconf.go), in a session's pass too: an exec's env is the
			// container's, not the boot's.
			name:      "export_podman_required_conf",
			run:       exportPodmanRequiredConfStep,
			noMark:    true,
			notDarwin: "macos-user runs no image, so there is no baked podman requirement to keep in force",
		},
		{
			// Populate /run/ld.so.cache from the /lib farm, plus both store-package lib dirs above.
			name:      "generate_ld_cache",
			run:       func(b *bootRun) { generateLdCache(b.e, StorePackagesLib(), StorePackagesFHSLib()) },
			notDarwin: "macOS has no ld.so.cache; dyld resolves libraries itself",
		},
		{name: "generate_shims", gen: GenerateShims},
		{name: "generate_agent_launchers", gen: GenerateAgentLaunchers},
		{name: "generate_package_manager_launchers", gen: GeneratePackageManagerLaunchers},
		{
			// LAST of the three launch-dir steps, and it must stay last: it fills the gap the
			// two above leave, which is only a gap once they have both run (launchwrapper.go).
			// On macos-user it is also what delivers a pack's launch flags to the prompt at
			// all: the account's login shell is zsh, which reads none of the bash rc files the
			// aliases are written into (DP-B43). The launch dir is second on
			// macosuser.SandboxPath and is re-prepended by WriteLoginRC, so a launcher is on the
			// path a typed name takes there, and the alias never was.
			name: "deliver_launch_flags",
			gen:  DeliverLaunchFlags,
		},
		{
			// A `program` need (no recipe) asserts presence and generates nothing, so it is not
			// a generator: an absent needed binary is a WARNING naming the bin, not a boot
			// failure (see AssertRequiredBins). After the launchers, so a recipe another selected
			// pack carries is already represented on the PATH. It matters MORE on macos-user than in
			// a container: that backend bakes no image at all, so a required tool comes from
			// the user's own machine or not at all.
			name: "assert_required_bins",
			run:  func(b *bootRun) { AssertRequiredBins(b.e) },
		},
		{
			// The orphan catalog is informational too, and for the same reason the need probe is:
			// nothing is half-written — a package is installed that this launch's declarations
			// do not account for. OQ-PD4 ruled that dropping a pack does not delete its
			// program, so this NAMES orphans and removes nothing unless the user turned on
			// `programs.autoprune` (program-delivery.md §10 step four). Here, before any
			// provisioning, on purpose: what is on disk now is what the LAST launch installed,
			// which is the only state in which "undeclared" means anything.
			//
			// ON BOTH BOOTS. macos-user was excluded until every place the one-line summary
			// sends its reader worked there (notch-convergence.md, NC-D26): the bootstrap now
			// keeps the container's boot.log (attachDarwinBootLog), the session names the staged
			// pack tree and the workspace, so `yolo programs ls`/`remove` read this jail from
			// inside the sandbox (entrypoint.JailEnvFromOS), and the launch relays
			// `programs.autoprune` (macosuser.BuildRunPlanWithDaemons).
			//
			// CONFINED ON macos-user, the one per-boot difference in this step: that bootstrap
			// runs outside Seatbelt, and every directory the catalog reads and autoprune unlinks
			// is reached through the agent-writable workspace sidecar, so it reads and unlinks
			// only beneath roots opened on those directories (catalogConfinedOrphans). A
			// container boot sees only what the agent sees and keeps the plain filesystem.
			name: "catalog_installed_orphans",
			run: func(b *bootRun) {
				if b.target == bootDarwin {
					catalogConfinedOrphans(b.e)
					return
				}
				CatalogInstalledOrphans(b.e)
			},
		},
		{
			// The catalog's other half, beside it for the same reasons and with the same
			// non-generator status: it compares what the receipts say this home GOT against
			// what is on disk, offline, and reports (program-delivery.md §10 step two, A4 as
			// ruled in §5.4 — "reconcile reports; it does not install"). A drifted version is
			// not a broken generator, so a fatal here would mean a jail whose vendor CLI
			// self-updated refuses to START, and OQ-PD7 rules that this reports first and gates
			// only if the reports ever justify one. AFTER the catalog, deliberately: "what has
			// no owner at all" is the coarser finding and comes first.
			name: "reconcile_installed_programs",
			run:  func(b *bootRun) { ReconcileInstalledPrograms(b.e) },
		},
		{
			// THE DURABLE DIR'S LAUNCH LINE (durablereport.go, OQ-DS2): informational, like the
			// two reports above, and on BOTH boots, since both export the directory. Beside them
			// because all three answer "what has accumulated that nothing will remove for you".
			name: "report_durable_dir",
			run:  func(b *bootRun) { ReportDurableDir(b.e) },
			notSessionPass: "the line is the launch's, said once by the jail's own boot on the launch " +
				"terminal (DS-D11): an attach shares the jail whose launch already said it, and the " +
				"size walk was every pass's largest step",
		},
		{
			// Build the combined CA bundle BEFORE bashrc and before any child spawn, so the env
			// vars exported here propagate to every child the entrypoint spawns. AFTER
			// hydrate_user_env, whose file it reads for the user's own CA variables: the
			// host's CAs join the bundle, and a bundle the user named wins for all five
			// (catrust.go, PS-D16).
			name: "generate_ca_bundle",
			run: func(b *bootRun) {
				e := b.e
				if err := applyCATrust(e); err != nil {
					e.warn("Warning: generate_ca_bundle: " + err.Error() + ". The jail's TLS " +
						"clients use the image's public roots; a restart composes the bundle again.")
				}
			},
			notDarwin: "the macos-user host launcher builds the bundle instead, from the tool " +
				"profile's public roots, the CAs this Mac's System and login keychains trust " +
				"for TLS, and the in-date certificates of the bundle the user's SSL_CERT_FILE " +
				"names, and names it in the session env file, which is the agent's environment " +
				"there (macosuser.ComposeCATrust)",
		},
		{name: "generate_bashrc", gen: GenerateBashrc},
		{
			name: "generate_bootstrap_script",
			gen:  GenerateBootstrapScript,
			notDarwin: "macos-user writes its provisioning stage's script instead " +
				"(generate_darwin_bootstrap_script, below), which a Seatbelt-confined stage runs",
		},
		{
			name: "generate_venv_precreate_script",
			gen:  GenerateVenvPrecreateScript,
			notDarwin: "the macos-user provisioning stage pre-creates no venv; a workspace that " +
				"configures `_.python.venv` gets none there (darwinstage.go records it)",
		},
		{
			// The mark for this step is taken after mise_uninstall_retired, its deferred side
			// effect, under this step's label.
			name:   "generate_mise_config",
			gen:    ConfigureMisePrism,
			noMark: true,
		},
		{
			// Deferred side effect of generate_mise_config: mise uninstall of retired tools.
			name: "mise_uninstall_retired",
			run:  func(b *bootRun) { miseUninstallRetired(b.e) },
			perf: "generate_mise_config",
			notDarwin: "not ported: a one-shot cleanup of installs the container boot once made " +
				"through mise (packload.RetiredMiseTools)",
		},
		{
			// Copy host nvim config into the writable .config/ overlay.
			name:      "nvim_config",
			run:       func(b *bootRun) { copyHostNvimConfig(b.e) },
			notDarwin: "it copies from the /ctx/host-nvim-config mount, which only a container launch provides",
		},
		{
			name: "generate_mcp_wrappers",
			gen:  GenerateMCPWrappers,
			notDarwin: "the wrapper bodies are Linux-absolute (`exec /bin/node`, /etc/fonts) and this " +
				"backend provisions neither; a pack's MCP server runs through its own program here",
		},
		{
			// Skills are mounted :ro by the launcher — no entrypoint action. A perf-log mark only.
			name:      "skills_skipped",
			run:       func(*bootRun) {},
			notDarwin: "skills reach the sandbox home in install_home_overlay, below",
		},
		{
			name: "configure_git",
			run:  func(b *bootRun) { configureGit(b.e) },
			notContainer: "git identity is host-composed and mounted read-only by the launcher (gitIdentityMountArgs), " +
				"and the jail's uid 0 owns the workspace there, so git needs no safe.directory entry",
		},
		{
			// EACH SESSION'S OWN CONFIG DIRECTORY (sessionhome.go, docs/design/per-session-
			// profiles.md): on a session's pass with a valid YOLO_SESSION_ID, every selected pack's
			// `session_homes` declaration gets a session home, the render below writes the
			// declared directory's surfaces there instead of into the shared copy (SP-D2), and the
			// declared variable is exported for the session's shell (SP-D4). Directly above
			// configure_pack_surfaces, which renders into what this decides. Not marked
			// notSessionPass's opposite in the table, since the table has no such flag: the step
			// itself does nothing on the jail's own boot. A home that cannot be made fails the
			// step, and the boot refuses the session (SP-D6).
			name: "prepare_session_homes",
			run:  prepareSessionHomesStep,
		},
		{
			// Render every PACK-DECLARED surface. One loop over declarations — no switch on
			// tool names, because core does not know any (see packsurfaces.go). A pack that
			// parsed on the host and not here means the mounted tree disagrees with what was
			// staged: fatal (A12), because rendering a subset would yield a jail whose config
			// is quietly incomplete.
			//
			// Neither in a sealed build jail (sealedbuild.go, PPX-D41), which runs a build line
			// and no agent: its narrowed selection can leave a pack's surface for another pack's
			// agent, or a hook's link, under a home directory only a dropped pack makes writable.
			name: "configure_pack_surfaces",
			run: func(b *bootRun) {
				packs, err := b.jailPacks()
				if err != nil {
					genStep(b.e, "load_packs", func() error { return err })
				}
				if b.sealed {
					b.e.note(sealedBuildSkipNote)
					return
				}
				// CAPTURE FROM SESSION HOMES (sessioncapture.go, OQ-SP2): a container pass
				// captures every other session home's rendered files into the shared overlay
				// before it renders, under the in-jail render lock, and the jail's own boot then
				// removes the homes the previous container left.
				if b.target == bootContainer || b.e.nativeSessionID != "" {
					capture := beginSessionCapture(b.e, packs, b.sessionPass)
					if capture.failed {
						capture.end(b.e)
						return
					}
					ConfigurePackSurfaces(b.e, packs)
					capture.end(b.e)
				} else {
					ConfigurePackSurfaces(b.e, packs)
				}
				RunPackHooks(b.e, packs)
			},
		},
		{
			// Stage the user's host_files entries (YOLO_HOST_FILES) through the same
			// composition engine, after the pack surfaces so a user entry never races a builtin
			// (the config layer already forbids one at a builtin path). On macos-user the
			// launcher COPIES each source-bearing entry's bytes into a root-owned tree named by
			// YOLO_CTX_ROOT, which hostUserPath resolves through (DP-L1), so this reads the same
			// wire and the same directory it does under a container.
			name: "configure_host_files",
			gen:  ConfigureHostFiles,
		},
		{
			// CONTENT — skills, pack briefings and pack `files` trees — copied over the home
			// from the staged overlay: the macos-user answer to the container's mounts. LAST
			// among the writers on purpose — the per-agent surface writers above create the
			// agent home dirs this copies into (~/.claude and kin), so running it earlier would
			// either race them or have to re-create them itself.
			name: "install_home_overlay",
			run: func(b *bootRun) {
				packs, _ := b.jailPacks()
				genStep(b.e, "install_home_overlay", func() error { return InstallHomeOverlay(b.e, packs) })
			},
			notContainer: "skills, briefings and pack files are bind-mounted read-only by the launcher",
		},
		{
			// The agent's ~/ctx link to $YOLO_CONTEXT_DIR (OQ-MM6), on both boots: the link is
			// a name for what the launch already mounts or stages, and it goes after the home
			// overlay so a pack file that happens to be named ctx is seen first and left alone.
			name: "link_context_dir",
			gen:  LinkContextDir,
		},
		{
			// EACH SELECTED PACK'S boot_check (packbootchecks.go, mcp-presets-removal.md
			// OQ-MP9): the pack's own script, run once, its output printed as a warning. After
			// install_home_overlay, which is what puts the script in a macos-user home. A `run`,
			// never a generator: a check that fails or hangs costs a line, never the boot.
			name: "run_pack_boot_checks",
			run: func(b *bootRun) {
				if b.sealed {
					return
				}
				packs, err := b.jailPacks()
				if err != nil {
					return // configure_pack_surfaces has already failed the boot on it
				}
				RunPackBootChecks(b.e, b.target, packs)
			},
			notSessionPass: "the jail's own boot ran each check once; a session's pass would print " +
				"the same warning again on every attach",
		},
		{
			// THE PROVISIONING STAGE'S SCRIPT ("The stage" of macos-user-provisioning.md).
			// Written LAST among the generators that produce content, because it is the only one
			// nothing here consumes: the stage is a separate, Seatbelt-confined process the
			// launcher runs after this bootstrap returns (macosuser.ProvisionArgv). Its
			// interpolations read the pack set and the MCP/LSP config, which every step above
			// has already had its turn with.
			name:         "generate_darwin_bootstrap_script",
			label:        "generate_bootstrap_script",
			gen:          GenerateDarwinBootstrapScript,
			notContainer: "the container boot writes ~/.yolo-bootstrap.sh (generate_bootstrap_script, above)",
		},
		{
			// The in-sandbox yolo-log wrapper is retired: yolo-log is the macos-log loophole's
			// client now, staged into the guest prefix, and a leftover wrapper would shadow it.
			name: "retire_yolo_log",
			run: func(b *bootRun) {
				genStep(b.e, "retire_yolo_log", func() error { return RetireYoloLog(b.e) })
			},
			notContainer: "no container boot ever wrote the wrapper; the container's own clients are baked",
		},
		{
			name:         "write_login_rc",
			gen:          WriteLoginRC,
			notContainer: "it undoes macOS path_helper, which a container has none of: the container boot sets PATH once, in execBash and .bashrc",
		},
		{
			name:      "cgroup_delegation",
			run:       func(b *bootRun) { setupCgroupDelegation(b.e.Stderr) }, // a boot diagnostic: it belongs in the log
			notDarwin: "macOS has no cgroups, and the sandbox is not a container",
		},
		{
			// No generate step for yolo-cglimit / yolo-journalctl any more: the image bakes
			// both (flake.nix shippedBinaries). All that is left is unlinking the scripts an
			// older entrypoint wrote into ~/.local/bin, which PRECEDES /bin on PATH and would
			// otherwise shadow the binaries forever.
			name: "cleanup_stale_wrappers",
			gen:  RemoveStaleGeneratedClients,
			notDarwin: "not run here: its names include `yolo`, and nothing establishes that a " +
				"regular file of that name in the sandbox's ~/.local/bin is a retired generated client",
		},
		{
			name:      "published_port_localnet",
			run:       func(b *bootRun) { setupPublishedPortLocalnet(b.e) },
			notDarwin: "no published ports: the sandbox is on the Mac's own network",
		},
		{
			name:      "port_forwarding",
			run:       func(b *bootRun) { startContainerPortForwarding(b.e) },
			notDarwin: "no container network to forward into: the sandbox is on the Mac's own network",
		},
		{
			// Record, in boot.log only, which pack services this launch armed with a caller
			// token: the daemons below demand it of every request (wire-bridge.md WB-D18).
			name:      "note_service_caller_auth",
			run:       func(b *bootRun) { noteServiceCallerAuth(b.e) },
			noMark:    true,
			notDarwin: "the macos-user launch hands its guest's supervisor its caller tokens in a root-owned env file of its own (macosuser.SandboxDaemonEnvFile), not through this bootstrap",
		},
		{
			// Publish each caller token the boot was handed as an in-jail 0600 file, for a
			// client that reads its credential from one (paths.JailCallerTokenDir), before the
			// supervisor starts the daemons demanding it. A SCOPED token is handed to no boot
			// (providers.md OQ-CN7 (c)), so aws-auth's gets no file.
			name:      "write_caller_token_files",
			run:       func(b *bootRun) { writeCallerTokenFiles(b.e) },
			noMark:    true,
			notDarwin: "the bootstrap is handed no caller token on macos-user: the guest's supervisor reads its tokens from its own root-owned env file, and the agent from its session env file",
		},
		{
			// Start the jail-daemon supervisor (child of PID 1; kernel-reaped on exit). A
			// `run`, not a generator: its failures are SERVICE refusals, which the reachability
			// hatch reaches when a required service did not start (requiredservice.go, OQ-R8).
			name:      "start_jail_daemon_supervisor",
			run:       startJailDaemons,
			perf:      "jail_daemon_supervisor",
			notDarwin: "the macos-user launch starts the guest's supervisor itself, confined by its Seatbelt profile, after this bootstrap exits (macosuser.JailDaemonArgv)",
		},
		{
			// The nix root watcher (nixrootwatcher.go): keeps, for the host, the roots the
			// jail's nix asks for. Best-effort; it never fails the boot.
			name:   "start_nix_root_watcher",
			run:    func(b *bootRun) { startNixRootWatcher(b.e) },
			noMark: true,
			notSessionPass: "the jail's own boot started its one watcher; a session's would find " +
				"the watcher's lock taken and exit, and race the session's exec into a program " +
				"that does not reap it",
			notDarwin: "macos-user shares the host's filesystem, so a root nix makes there is already one the host honors; no watcher is needed (in-jail-nix-roots.md, supported producers)",
		},
		{
			// Collect the outbound-network probe start_egress_probe launched (egress.go). A
			// WARNING, never a refusal: it records nothing on the refusal gate. Here, just above
			// the service witness, so a boot's two network findings read together; under a
			// progress line, silent when quick, which it nearly always is by now.
			name: "report_egress",
			run: func(b *bootRun) {
				line := b.e.progress("Checking that the jail can reach the internet")
				reportEgressStep(b)
				line.Done("")
			},
			notSessionPass: "the finding is the jail's, said once by its own boot on the launch " +
				"terminal; a session's pass joins a jail whose launch already reported it",
		},
		{
			// The in-jail reachability witness runs LAST, and both halves of that are
			// deliberate. Its finding is then the closest thing to the agent's first prompt
			// instead of being buried under pack rendering; and it sits immediately above
			// genFailuresError in Main, the boot's "refuse before handing over control" gate,
			// which is what OQ-R2's fatal plugs into — a service this jail cannot use REFUSES
			// the launch there, having first let every generator above run so one boot reports
			// every problem. See reachability.go. A healthy probe answers in milliseconds, but a
			// blackholed service costs a dial of up to 30 s plus retries, so it runs under a
			// progress line — line-oriented (boot.log is half of e.Stderr) and silent when quick.
			name: "probe_service_reachability",
			run: func(b *bootRun) {
				reach := b.e.progress("Checking that the jail can reach its host services")
				ProbeServiceReachability(b.e)
				reach.Done("")
			},
			notDarwin: "this bootstrap runs outside the session's Seatbelt profile, so a probe " +
				"here could pass where the agent's own client is refused; the macos-user launch " +
				"runs the witness as a confined stage of its own after its jail daemons start " +
				"(macosuser.ProbeServicesArgv)",
		},
	}
}
