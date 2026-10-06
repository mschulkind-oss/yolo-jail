package run

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostcas"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// MISE_STORE_VOLUME is the named volume backing the jail-land mise store on
// macOS (podman + Apple Container), mounted at /mise. Versioned name (bump the
// suffix to force a fresh store).
const miseStoreVolume = "yolo-mise-data-v2"

// assembleInput carries everything the ordered-argv assembler needs that isn't
// on Options. It is populated by the fresh-launch path before assembly; grouping
// it keeps assembleRunCmd a pure function of its inputs ().
type assembleInput struct {
	cfg   *jsonx.OrderedMap
	rt    string
	cname string
	// packs are this run's loaded packs (embedded official + configured). Their
	// DECLARATIONS drive the mounts below — writable dirs, mount targets, host-file
	// grants — which is what lets core stay ignorant of what an "agent" is.
	packs []*packload.Pack
	// jailDaemons is this launch's composed YOLO_JAIL_DAEMONS payload: every active
	// loophole's jail_daemon plus every selected service's, one sorted list
	// (jailDaemonsFor). Assembly only SERIALIZES it.
	//
	// It is INPUT for the reason storePackages and cacheRelocations are: the answer
	// belongs to the launch rather than to the argv, and one other consumer needs it
	// — the macos-user arm, which has no argv at all and must decline each entry by
	// name (docs/reference/macos-user-nix-and-features.md). Composing it here as
	// well would give one launch two payloads that agree only by both call sites
	// passing the same arguments.
	//
	// A zero value is "no jail daemons", which is what every hand-built
	// assembleInput in a test gets for free; a test that wants the env var composes
	// it the way Run does.
	jailDaemons []loopholes.JailDaemonSpec
	// imageRef is the ref of the image AutoLoadImage actually made ready this
	// launch — content-addressed (`localhost/yolo-jail:<sha16>`) on the normal
	// path, the legacy :latest tag on a degraded fallback with no store path.
	//
	// It is INPUT, not something assembly derives. Before C2 both this position
	// in the argv and runNormal's host-service insert point called a
	// jailImageRef(rt) helper that returned a constant, and they agreed only
	// because the constant was the same in both. With one image per config there
	// is no constant left to agree on, so the ref is threaded from the one place
	// that knows it and read twice from this single field. See jailImage() for
	// what an unset field does, and why it does not fall back.
	imageRef string
	// jailPrefix is the HOST side of /opt/yolo-jail — the two directories the
	// launch bind-mounts in so the jail has a yolo at all. It is INPUT for the
	// same reason imageRef is: resolving it can run a nix build (jailprefix.go),
	// which argv assembly must stay free of, and the container command names
	// JailEntrypointPath inside it. An empty binDir emits a mount with an empty
	// source; every construction that leaves it empty is a test.
	jailPrefix jailPrefix
	agentsPath string // AGENTS_DIR/<cname> (briefings + skills staging)
	// scratchID is this launch's half of its scratch volumes' names (newScratchLaunchID,
	// ScratchMountArgs). INPUT because the teardown must remove exactly the volumes the
	// argv mounted: runContainer mints it once and hands the same value to both.
	scratchID string
	// packStaging is this launch's own pack tree (packtree.go: one per launch under
	// AGENTS_DIR/<cname>/pack-trees, never edited afterwards) — the staged packs the
	// entrypoint renders from, so it sees the same declarations the host read. Delivered :ro
	// at /ctx/packs on podman and as a per-launch COPY under ws_state on Apple Container,
	// which ignored :ro below acROBindsFloor; either way the jail is TOLD where by YOLO_PACK_ROOT.
	packStaging string
	// capturesDir is the machine-wide install-capture store (paths.CapturesDir), bound
	// :ro so an in-jail launcher can materialize an entry instead of downloading it.
	// EMPTY means "do not mount it" — the capture jail's own launch, which must not be
	// able to resolve against the store it is filling (Options.CapturesDir).
	capturesDir string
	// forkDeliveries is the host's decision per forked program for this launch
	// (Options.forkDeliveriesFor, forkbuild.go): the store key its jail materializes, or why there
	// is none. Emitted beside the store mount as entrypoint.ForkBuildsEnv; nil emits nothing.
	forkDeliveries map[string]entrypoint.ForkDelivery
	// patchedTrees is the host's decision per patched extension for this launch
	// (Options.patchedTreesWire, patchedtrees.go), emitted as YOLO_PATCHED_TREES; treeDirs is the
	// per-launch copy the `files` emitter mounts for each one delivered, by extension key.
	patchedTrees map[string]entrypoint.TreeDelivery
	treeDirs     map[string]string
	// homeSkeleton is this launch's per-jail home skeleton (buildHomeSkeleton), the
	// directory podmanBaseMounts binds read-only at /home/agent. It is INPUT, built by the
	// run pipeline on the fresh-launch path, because building it creates directories and
	// argv assembly must stay free of that. Unused on Apple Container, which binds wsState
	// whole at /home/agent instead.
	homeSkeleton string
	wsState      string // <workspace>/.yolo/home
	// durableDir is the in-jail path this fresh launch's durable dir was made at
	// (ensureDurableDir), exported as $YOLO_DURABLE_DIR; "" exports nothing. INPUT because
	// making it creates a directory, which argv assembly must stay free of.
	durableDir string
	miseStore  string // _jail_mise_store_dir(), or a sealed build's own (seal.go)
	// cacheDir is the host side of the jail's ~/.cache: the machine's shared cache
	// (paths.GlobalCache), or a sealed build's own (seal.go). "" reads as the machine's, which is
	// what every hand-built assembleInput in a test gets for free.
	cacheDir string
	// sealed is THE SEAL (Options.Sealed, seal.go), carried here so argv assembly withholds every
	// crossing site it emits. False for every launch but a fork build.
	sealed bool
	// sealedTree is, under the seal, the patched extension the build jail builds
	// (Options.SealedTree), emitted as entrypoint.TreeBuildEnv.
	sealedTree   string
	hostTZ       string // "" => no TZ
	yoloVersion  string // _git_describe_version() or "unknown"
	mountTargets map[string]struct{}
	// storePruneOK is true when the host CLI proved no other jail is live and
	// grants the in-jail store prune (`-e YOLO_STORE_PRUNE_OK=1`). Set by the
	// lifecycle phase; false leaves the env unset.
	storePruneOK bool
	// storePackages is C4/C5's settled decision for this launch — whether packages come
	// from the mounted nix store instead of the image, and which profiles. Decided BEFORE
	// the image build (it changes what is built) and carried here so the argv states the
	// same answer the build acted on; assembly only emits the -e pair. A zero value is
	// the baked default, which is what every non-podman, non-Linux and non-opt-in launch
	// gets — and what every hand-built assembleInput in a test gets for free.
	storePackages storePackagesPlan
	// cacheRelocations are the user-scope cache subdir → host dir relocations,
	// already loaded, validated and provisioned by the run pipeline (assembly
	// only emits the -v pairs, and must stay free of the fs access + the
	// user-config read that producing them requires).
	cacheRelocations []config.CacheRelocation
	// hostCASAlias is L9's settled decision for this launch — which recognised
	// content-addressed host caches this jail shares instead of pooling a second
	// copy of (docs/design/disk-levers-and-backfill.md OQ-BF10). One entry per
	// recognised store, aliased or not, so the disclosure can explain a decline;
	// assembly emits a -v only for the aliased ones.
	//
	// Decided and PROVISIONED by the run pipeline before assembly, the same split
	// cacheRelocations keeps: producing it needs a filesystem probe and a MkdirAll,
	// and argv assembly must stay free of both. A nil slice is the status quo —
	// what every hand-built assembleInput in a test gets for free, which is why no
	// frozen argv in this package had to move for this feature.
	hostCASAlias []hostcas.Disposition
	// writableHomeDirs are extra home-relative paths (config writable_home_dirs)
	// mounted read-write off <wsState>/writable-home, letting an agent extension
	// that hardcodes a $HOME path (e.g. ~/.pi-lens) write through the :ro base.
	// Already derived + validated by the run pipeline; prepareWsState created
	// each backing dir, so assembly only emits the -v pairs.
	writableHomeDirs []string
	// hostFiles are the resolved user `host_files` entries (config.LoadHostFiles),
	// already scope-filtered and validated by the run pipeline — assembly only
	// emits the -v pairs + the YOLO_HOST_FILES env, and must stay free of the
	// user-config read and the host stat that producing them requires (the same
	// split as cacheRelocations). prepareHostFiles provisioned each destination's
	// writable staging before assembly.
	hostFiles []config.HostFileEntry

	// userEnv is the env_sources the run pipeline hydrated — the same resolution that
	// wrote yolo-user-env.sh, handed to assembly rather than read a second time. It is
	// the secret channel inside the composed channel (an env derive's credential
	// placeholder resolves through it, and so does the credential pre-flight); assembly
	// reads it only through envChannel, and only when the pipeline did not hand a channel
	// over — every construction that does not come from the run pipeline is a test. Nil
	// there means nothing was hydrated, and the lookup then falls back to the CLI's own
	// environment.
	userEnv *jsonx.OrderedMap

	// acCtxMaterialized is set by EITHER host-file emitter when it copies a grant into
	// the Apple Container ctx dir. The YOLO_CTX_ROOT that tells the entrypoint where to
	// look is then emitted ONCE, below — two emitters each appending their own would put
	// the same -e on the argv twice, which is at best noise on a frozen argv and at worst
	// a backend that rejects a duplicate flag.
	acCtxMaterialized bool

	// hostLayersDelivered is where hostFileArgs records the /ctx destination of every
	// pack-declared host file it actually put in the jail, for the report the entrypoint's
	// fail-closed host-layer read witnesses against (packload.HostLayerReport). Written by
	// the emitter and read once, by hostLayerEnv below: the launcher is the only half that
	// knows the difference between "the user has no such file" and "it did not arrive".
	hostLayersDelivered []string

	// hostLayersRendered is the LABEL half of that report: which of those destinations hold
	// bytes yolo itself rendered into this home rather than the user's own
	// ([OQ-CR6], docs/reference/config-target-resolution.md; entrypoint.HostLayerRender states
	// what the jail does with one). A subset of hostLayersDelivered, written by the same
	// emitter, because what a file IS travels with the delivery that carries it — the jail
	// cannot derive it, `host_management` being deliberately un-inherited.
	hostLayersRendered []string

	// channel is the profile/provider environment the run pipeline composed above the
	// backend dispatch (profilechannel.go). The env block below EMITS it; it must not
	// re-derive any part of it, or the argv and whatever the macos-user arm delivered
	// from the same launch could disagree. Nil only on a hand-built assembleInput that
	// never came from the run pipeline — every one is a test — which envChannel then
	// composes from these same fields through the one composer.
	channel *packChannel
}

// envChannel returns this input's composed channel, composing it from the input's own
// fields when the pipeline did not hand one over. There is one composer, so the fallback
// cannot produce a different channel — it exists so a test that assembles an argv by hand
// still exercises the same fold, not as a second production path.
func (in *assembleInput) envChannel(o *Options) *packChannel {
	if in.channel != nil {
		return in.channel
	}
	c, err := o.composePackChannel(in.cfg, in.packs, in.userEnv)
	if err != nil {
		// Unreachable from the run pipeline: Run composes above the backend dispatch,
		// refuses there, and hands the channel in. A nil in.channel is a hand-built
		// input — every one a test — and assembling an argv from a table the launch
		// refused would be exactly the ambiguity this error exists to prevent, so fail
		// loudly rather than quietly compose one.
		panic("envChannel: the provider composition refused: " + err.Error())
	}
	return c
}

// storePruneEnv returns the `-e YOLO_STORE_PRUNE_OK=1` pair when granted, else
// nil.
// that set storePruneOK live in the lifecycle phase).
func (in *assembleInput) storePruneEnv() []string {
	if in.storePruneOK {
		return []string{"-e", "YOLO_STORE_PRUNE_OK=1"}
	}
	return nil
}

// assembleRunCmd builds the ordered container argv: flags-before-image, the -e
// env block, the mount order, network, devices, GPU/KVM, resources, loopholes,
// then the image + the absolute path of the mounted yolo-entrypoint
// (JailEntrypointPath).
// It is a pure function of (o, in) EXCEPT for the ws_state dir/file touches and
// venv-shadow backing mkdirs performed inline while building the argv — those
// side effects are preserved (they are part of the launch, not the argv), so
// callers pass a prepared ws_state — and EXCEPT for the host probes it runs
// through the o.Exec / o.LookPath / o.PathExists seams: git identity, the GPU
// availability probe, and (since the host-loopback fix) `podman info` plus
// `<rootless-network-stack> --help`. Those are why every caller in the test suite
// goes through goldenOptions: a test that leaves the seams at their fillDefaults
// values shells out to the host and gets an argv that depends on which machine ran
// it — which is how TestAssembleRunCmdPodmanLinuxGolden once started failing on the
// macOS runner.
// The final internal command and the host-service -e insertion are handled by the
// lifecycle phase.
// The argv this returns ends at the image ref + JailEntrypointPath; the
// final_internal_cmd is appended after inserting host-service env at
// index(image); see runContainer for that tail.
func (o *Options) assembleRunCmd(in *assembleInput) []string {
	cfg := in.cfg
	rt := in.rt
	// STDERR, like every other launch notice (warnIfNoPacks states the rule):
	// stdout belongs to the jailed command, and a notice printed there is
	// swallowed by the command's redirect or corrupts what the user pipes. This
	// printer emits ONLY notices — the seven warnings below, nothing else — so
	// the stream is the one decision. It was stdout until 2026-09-06, and on
	// GitHub's podman-4.9.3 runners the host-loopback note fired on every
	// bridge launch and prepended itself to every integration test's asserted
	// command output (TestAssembleNoticesGoToStderr is the regression, and the
	// reason it is a unit test: a nested jail forces --net=host, so the
	// in-jail suite can never see the branch fire).
	out := o.pr(o.Stderr)

	// --- Network mode + ports ---
	//
	// Through resolveNetMode rather than an inline copy of it: the loophole runtime
	// resolves the mode the same way to decide what each host daemon PUBLISHES
	// (advertiseHostFor), and the two now feed one shared predicate
	// (sharesLauncherNetns) whose whole value is that they cannot disagree.
	// Its source names the key or the flag in the warnings below, which must send the user to
	// whichever one chose the mode.
	netMode, netModeSource := o.resolveNetModeSource(cfg)

	// --- nested-container detection ---
	// o.inContainer() rather than a second copy of the same two probes, for the
	// reason given at netMode above: this answer and the loophole runtime's have to
	// be one answer, not two that happen to match today.
	inContainer := o.inContainer()

	// THE MODE THIS LAUNCH RUNS UNDER, computed HERE — above the port gate — rather
	// than beside the `--net=` selector it also spells (see the network-mode-flag
	// section below, which is its other reader).
	//
	// The port keys used to answer to the CONFIGURED mode while the selector answered
	// to the applied one, and a nested launch is exactly where those two differ:
	// podman-in-podman is forced onto the launcher's namespace, so `network.mode:
	// "bridge"` (the default) emitted --net=host AND every -p, and the publish path
	// then appends the DNAT sysctl the host namespace refuses outright:
	//
	//	Error: ... sysctl net.ipv4.conf.all.route_localnet=1 can't be set since
	//	Network Namespace set to host
	//
	// — a `podman create` failure, so a nested jail declaring ANY port could not start
	// at all. That is this repo's own dev loop (AGENTS.md, "Nested-jail verification").
	applied := appliedNetMode(rt, netMode, inContainer)

	var publishArgs []string
	var forwardHostPorts []any
	if netSec := cfgMap(cfg, "network"); netSec != nil {
		declaredPorts := asAnyList(mapGet(netSec, "ports"))
		declaredForwards := asAnyList(mapGet(netSec, "forward_host_ports"))
		if applied == "bridge" {
			for _, p := range declaredPorts {
				publishArgs = append(publishArgs, "-p", pyStrCoerce(p))
			}
			forwardHostPorts = declaredForwards
		} else if netMode == "bridge" && (len(declaredPorts) > 0 || len(declaredForwards) > 0) {
			// ONLY THE FORCED DROP WARNS, and only when there is something to drop. An
			// explicit `network.mode: "host"` drops the same two keys and always has —
			// that is the user's own declaration, and on Apple Container (where the mode
			// is not honored at all) the branch further down is where it gets its line.
			// What has no declaration anywhere is NESTING: the config says bridge, the
			// launch applies host, and the ports vanish for a reason nothing the user
			// wrote can account for. Conditional on the declaration for the reason
			// backend-parity.md OQ-BP-3 gives: a launch line that fires only when the user
			// declared the thing is the kind a reader does not learn to skip.
			var dropped []string
			if len(declaredPorts) > 0 {
				dropped = append(dropped, "network.ports")
			}
			if len(declaredForwards) > 0 {
				dropped = append(dropped, "network.forward_host_ports")
			}
			out.print("[yellow]Warning: nested launch — podman-in-podman is forced onto this " +
				"container's network namespace (--net=host; netavark cannot create one without " +
				"NET_ADMIN), so " + strings.Join(dropped, " and ") + " " +
				"are NOT applied[/yellow] — the jail shares this process's network stack, so a " +
				"port it binds is already reachable on the same localhost, with no forwarding hop " +
				"to publish. Launch from the host for published ports.")
		}
	}
	// A user provider URL at localhost names the launcher host, not this jail's
	// private loopback. The channel has already extracted those ports from user
	// config (never pack endpoints) and the usual forward_host_ports machinery
	// carries them without making a runtime gateway name part of that config.
	if applied == "bridge" {
		forwardHostPorts = mergeHostForwards(forwardHostPorts, in.envChannel(o).localProviderForwards)
	}
	// THE SEAL (seal.go): a fork build publishes no port and reaches no host service through a
	// forward. Withheld after the computation above, so a nested launch's warning about the
	// ports it would have dropped anyway still reads the same.
	if in.sealed {
		publishArgs, forwardHostPorts = nil, nil
	}

	normalizedBlocked := config.NormalizeBlockedToolsWith(cfgMap(cfg, "security"), packload.BlockedTools(in.packs))
	blockedConfigJSON := jsonDumps(normalizedBlocked)

	// --- Extra mounts (config.mounts → -v host:container[:ro]) ---
	//
	// configCtxMounts is the one reading of the key (ctxmounts.go): read-only elements from
	// the merged config, read-write ones from the user scope alone, each skip said here. A
	// read-write element gets no mode suffix and its disclosure line, EVERY launch — printed
	// where the bind is emitted, so no path emits the one without the other
	// (docs/design/context-mounts.md §2.4).
	var mountArgs []string
	if !in.sealed { // a fork build gets no `mounts` element (seal.go)
		rootful := o.rootfulPodmanHost(rt)
		for _, m := range o.configCtxMounts(rt, cfg, out.print) {
			mountArgs = append(mountArgs, "-v", m.bindArg())
			if m.rw {
				out.print(rwMountDisclosure(m, rootful))
			}
		}
	}

	// --- run_flags ---
	runFlags := []string{"--rm", "-i", "--init", "--read-only", "--name", in.cname}
	if rt != "container" {
		// insert("--cgroupns=private", 3)
		runFlags = insertAt(runFlags, 3, "--cgroupns=private")
	}
	if rt == "podman" && o.IsLinux {
		runFlags = append(runFlags, "--read-only-tmpfs=false")
	}
	if rt == "podman" {
		runFlags = append(runFlags, "--pull=never")
		runFlags = append(runFlags, "--log-driver", "none")
		runFlags = append(runFlags, "--security-opt", "unmask=/proc/sys")
		// THE CLIENT FORWARDS NO SIGNAL INTO THE MAIN PROCESS. pid 1 is a hold, and the
		// launcher's own signal arm is what ends the jail on a hangup or an interrupt; a
		// client that also forwarded one would be a second way for a stray signal to end
		// every session (docs/design/jail-lifetime-last-session-wins.md JL-D15, §3.1).
		// podman only: whether Apple Container's client can be told the same is unmeasured.
		runFlags = append(runFlags, "--sig-proxy=false")
	}
	// NO DETACH SEQUENCE on the main process's client, as on every session's exec
	// (runtime.DetachKeysArgs, JL-D27). This client holds no terminal, so nothing can type the
	// sequence at it today; it carries the flag anyway because the rule is every run and exec,
	// and the keeper's client that replaces it must not be the one exception.
	runFlags = append(runFlags, runtime.DetachKeysArgs(rt)...)
	// NO -t, ON ANY TERMINAL. The main process is a hold that reads nothing and whose output
	// the launcher relays line by line (jailmain.go's relay); the session that owns the
	// terminal is the first session's exec, which takes -t there (firstSessionExecCmd).

	// --- base run_cmd (mounts) ---
	var runCmd []string
	if rt == "container" {
		runCmd = appleContainerBaseMounts(rt, runFlags, o.Workspace, in, out)
		// THE INSTALL PREFIX, on this backend too. It is emitted here rather than
		// once below the branch because Apple Container's base mounts are a
		// different function, and the ONE mount whose absence means "no pid1" must
		// not be the one that depends on which arm added it. Two directory mounts
		// — the backend copies files rather than binding them
		// (helpers.go:acMaterialize) only for single FILES, which these are not.
		runCmd = append(runCmd, jailPrefixMountArgs(in.jailPrefix)...)
	} else {
		runCmd = podmanBaseMounts(rt, runFlags, o.Workspace, in, o.IsMacOS)
		runCmd = append(runCmd, jailPrefixMountArgs(in.jailPrefix)...)
		// Ephemeral scratch dirs.
		runCmd = append(runCmd, ScratchMountArgs(cfgStr(cfg, "ephemeral_storage"), in.cname, in.scratchID)...)
		// PACK-DECLARED writable dirs, backed per-workspace. Core does not know these
		// belong to an "agent" — a pack asked for a writable dir and got one.
		for _, dir := range packload.WritableDirs(in.packs) {
			runCmd = append(runCmd, "-v",
				filepath.Join(in.wsState, strings.TrimPrefix(dir, "."))+":/home/agent/"+dir)
		}
		// B5: machine-wide (cross-jail) dirs, from the registry rather than a
		// hardcoded per-agent branch. These come from GlobalHome, NOT ws_state, so a
		// credential survives across workspaces — see packload.SharedDirs for why that
		// tier exists and why widening it is a real decision. None under the seal: a
		// fork build is handed no pack's machine-scope directory (seal.go).
		if !in.sealed {
			for _, dir := range packload.SharedDirs(in.packs) {
				runCmd = append(runCmd, "-v",
					filepath.Join(paths.GlobalHome(), dir)+":/home/agent/"+dir)
			}
		}
	}

	// --- Common env block (frozen order) ---
	runCmd = append(runCmd, o.commonEnvBlock(in, blockedConfigJSON, netMode)...)
	// THE JAIL'S --with-credentials GRANT (jailgrant.go, ES-D37): on podman a `:ro` bind of the
	// per-launch grant file the launch staged outside the workspace (stageJailGrant), which every
	// boot and session reads into its environment; on Apple Container nothing here, the copy being
	// in the home it binds. NEVER a granted name or value as `-e`: podman resolves an `-e` into the
	// container's configuration, its inspect output and its database, which outlive the jail.
	runCmd = append(runCmd, o.jailGrantBindArgs(rt)...)
	// THE CONTEXT DIR (docs/design/context-mounts.md CX-D4), on both container backends and on
	// EVERY launch: /ctx is where every context mount lands here, and the directory exists
	// whether or not one was declared. macos-user exports its own value (its staged tree),
	// so pack text and agents spell a context path `$YOLO_CONTEXT_DIR/<rel>` everywhere.
	// Not YOLO_CTX_ROOT, which is the entrypoint's composition input and means ~/.yolo-ctx
	// on Apple Container.
	runCmd = append(runCmd, "-e", paths.ContextDirEnv+"="+paths.ContainerContextDir)
	// THE DURABLE DIR (durabledir.go), on both container backends, and ONLY when this launch
	// made it: a variable naming a directory that is not there would send every agent and
	// tool to nothing. A container env var, so every exec an attach starts inherits it.
	if in.durableDir != "" {
		runCmd = append(runCmd, "-e", durable.EnvVar+"="+in.durableDir)
	}
	// CLAUDE'S CREDENTIAL STORE, on both container backends (CL-D22, claudesecurestorage.go): the
	// machine-scope directory the shared dirs above bind, so Claude opens the real file there.
	// macos-user renders the same value into its launch env (run.go).
	if !in.sealed { // it names a machine-scope directory, which a sealed build is not given
		runCmd = append(runCmd, o.claudeSecureStorageEnvArgs(rt, cfg, in.packs)...)
	}

	// --- yolo-user-env.sh and the per-agent env files (written by deliverChannel) ---
	// Both are written by the lifecycle phase before this assembly and by every attach.
	// podman gets them as binds: the shared file single, the per-agent directory whole —
	// what the credential gate scopes to ONE agent, each file sourced by that agent's
	// launcher (agentenvfiles.go, OQ-CN6) — so an attach's rewrite shows in the running
	// jail, and `:ro`, because nothing in the jail has a reason to write what the launcher
	// delivers. Apple Container gets NO bind for either: it binds wsState over the home and
	// ignores `:ro` (roBindsUnsupported), so deliverChannel writes both straight to their
	// in-home paths beneath wsState, on this launch and on every attach. Copying them here
	// instead, as this block used to, reached the jail only on a fresh launch — an attach
	// rewrote files that backend never reads.
	userEnvFile := filepath.Join(in.wsState, "yolo-user-env.sh")
	agentEnvDir := filepath.Join(in.wsState, agentEnvStateDir)
	if rt != "container" { // parity: HonoredBy — Apple Container binds wsState at /home/agent, and deliverChannel writes both files at their in-home paths beneath it on every entry
		runCmd = append(runCmd,
			"-v", userEnvFile+":/home/agent/.config/yolo-user-env.sh",
			"-v", agentEnvDir+":/home/agent/"+entrypoint.AgentEnvDirRel+":ro")
	}

	// --- container cwd ---
	// The in-jail CLI no longer needs a source bind: the image bakes the flake
	// bundle + real-file binaries at /opt/yolo-jail (installPrefix in flake.nix),
	// so the shared resolver (internal/reporoot) finds the repo exe-relative,
	// identically inside and outside the jail. Self-hosting is no exception since
	// the cwd-walk was removed: an in-jail agent that wants the live /workspace
	// checkout instead sets YOLO_REPO_ROOT itself. Nothing to bind, and no
	// YOLO_REPO_ROOT for the launcher to guess at.
	runCmd = append(runCmd, "--workdir", "/workspace")

	// --- GPU availability probe (gates the uidmap/runc branch below) ---
	gpuRequested := false
	gpuVendor := "nvidia"
	gpuUnavailableReason := ""
	gpuEnabled := false
	// A sealed build requests no GPU (seal.go), so neither the passthrough nor the userns branch
	// NVIDIA's needs is chosen for it.
	if gpuSec := cfgMap(cfg, "gpu"); gpuSec != nil && !in.sealed {
		gpuRequested = mapBoolOr(gpuSec, "enabled", false)
		gpuVendor = mapStrOr(gpuSec, "vendor", "nvidia")
	}
	if gpuRequested {
		var okGPU bool
		switch {
		case gpuVendor == "amd":
			okGPU, gpuUnavailableReason = o.rocmHostAvailable(rt)
		case inContainer:
			// NESTED NVIDIA IS DECLINED HERE RATHER THAN HALF-EMITTED BELOW. The
			// nesting branch of podmanNestingArgs is chosen FIRST — it has to be, a
			// doubly-nested user namespace cannot mount /proc — so the NVIDIA branch's
			// `--runtime runc` never reaches the argv. That flag is not a nicety: it is
			// the documented workaround for crun's CDI handling (podman#27483), and
			// without it a jail carrying `--device nvidia.com/gpu=…` dies as
			// `conmon bytes "": readObjectStart`. The CDI flags gpuArgs emits further
			// down were the only half of the feature a nested launch ever got.
			//
			// The user guide already said nested-with-GPU is unsupported and "not
			// currently prevented"; this is the prevention, taking the warn-and-skip
			// path every other unavailable-GPU verdict takes rather than a refusal —
			// a GPU that cannot be passed through has never been worth a jail.
			//
			// AMD is deliberately above this: its device-node path needs neither the
			// runc workaround nor the identity maps, so nesting drops nothing it
			// depends on, and declining it would be a claim nobody has measured.
			gpuUnavailableReason = "this launcher is already inside a jail and nested " +
				"podman-in-podman cannot carry NVIDIA passthrough"
		default:
			okGPU, gpuUnavailableReason = o.gpuHostAvailable(rt)
		}
		gpuEnabled = okGPU
	}

	// --- Podman nesting / GPU userns / device+cap block ---
	if rt == "podman" {
		runCmd = append(runCmd, o.podmanNestingArgs(inContainer, gpuEnabled, gpuVendor)...)
	}

	// --- host nix daemon + store ---
	if o.hostNixMounted(rt) && in.sealed {
		// THE SEAL (seal.go): no daemon socket, which would let a fork's build ask the host's
		// nix daemon to build and register anything it likes. The store stays, read-only, for
		// the store-delivered packages a launch may take its toolchain from.
		runCmd = append(runCmd, "-v", hostNixStore+":"+hostNixStore+":ro")
	} else if o.hostNixMounted(rt) {
		runCmd = append(runCmd,
			"-v", hostNixSocket+":"+hostNixSocket,
			"-v", hostNixStore+":"+hostNixStore+":ro",
			"-e", "NIX_REMOTE=daemon")
	} else if msg := o.nixDelegationSkipNotice(rt); msg != "" {
		out.print(msg)
	}

	// --- network mode flag ---
	//
	// The default `bridge` mode still emits no --net flag: it means "let podman
	// decide", and it stays that way. What it no longer means is "say nothing at
	// all about networking" — hostLoopbackFactsFor asks podman which rootless
	// stack it will use and, on the stacks yolo can positively identify, adds the
	// option that forwards the host's LOOPBACK into the jail. Without it, pasta
	// (podman's default since 5.0) forwards host.containers.internal to the
	// host's global address and every loopback-TLS service is unreachable from
	// every jail — see hostloopback.go and
	// docs/reference/loopback-tls-reachability.md §6. Every failure path there emits
	// nothing, so the worst case is exactly the behaviour above.
	//
	// Both emitting branches below spell the selector as `--net=` + appliedNetMode, so
	// the argv IS the predicate's answer rather than a second computation of it. That is
	// what the briefing reads (backend-parity.md §6): a jail forced onto host networking
	// by nesting used to be told it was bridged, because the forcing lived here as an
	// inline `rt == "podman" && inContainer` and nothing else could see it.
	//
	// `applied` is computed at the top of this function, beside the port gate that is
	// its other reader — the same answer, once, for both.
	var hostLoopback hostLoopbackPlan
	if rt == "container" {
		// Apple Container handles networking internally, so an explicit
		// `network.mode: "host"` does nothing here and used to say nothing either. No
		// --net is emitted (this branch), so there is no host networking. The agent was
		// also told "localhost resolves directly to the host" on top of that, by a
		// briefing composed from the config rather than from what was applied — it now
		// reads appliedNetMode, which answers "bridge" here for the reason this branch
		// emits no selector.
		//
		// The port keys follow that same answer now (the gate at the top of this
		// function reads `applied`, not the config), so an explicit host mode no longer
		// silently drops them on this backend: what AC applies is its own bridged
		// networking, and -p works there whatever the unhonored key says.
		//
		// Only an EXPLICIT host is warned: bridge is genuinely honored on this backend
		// (-p is emitted ungated, forward_host_ports goes through --publish-socket, and
		// AC gives each container its own vmnet netns), so warning on the default would
		// be noise on every launch.
		if netMode == "host" {
			asked, remedy := `network.mode "host"`, "Remove the key"
			if netModeSource == netModeFromFlag {
				asked, remedy = "--network host", "Drop the flag"
			}
			out.print("[yellow]Warning: " + asked + " is NOT honored on Apple Container[/yellow] — " +
				"the backend manages networking itself, so the jail does not share your host's " +
				"network stack (published ports still apply: this backend runs bridged either " +
				"way). " + remedy + ", or use YOLO_RUNTIME=podman for host networking.")
		}
	} else if rt == "podman" && inContainer {
		// Podman-in-podman: netavark cannot create a netns without NET_ADMIN, so the
		// applied mode is host whatever the config asked for. This is also the one mode
		// in which the reachability bug CANNOT reproduce (the jail shares the launcher's
		// stack, so the two loopbacks are one), which is why a nested jail is no evidence
		// about the branch below — §7 of the design doc, and the carve-out in AGENTS.md.
		runCmd = append(runCmd, "--net="+applied)
	} else if in.sealed {
		// THE SEAL (seal.go, FP-D13): a fork's build gets the runtime's own bridge and NOT the
		// host's loopback. Forwarding it is what makes every loopback-TLS service reachable from a
		// jail — and every other service the host binds to 127.0.0.1 with them, which is the reach
		// FP-D11 withholds a port forward for. A build starts no service of yolo's to reach, so
		// nothing is asked of the host, and the jail is told `unknown`: yolo did not look.
		if applied != "bridge" {
			runCmd = append(runCmd, "--net="+applied)
		}
	} else {
		if applied != "bridge" {
			runCmd = append(runCmd, "--net="+applied)
		}
		// Spanned because it EXECS `podman info`, and argv assembly measured
		// 2.080s on a real host with no way to see which of its exec probes that
		// was (2026-09-08). Assembly is otherwise pure string work.
		hlsp := o.Perf.Span("assemble.host_loopback_probe")
		facts := o.hostLoopbackFactsFor(rt, netMode)
		facts.modeFromFlag = netModeSource == netModeFromFlag
		hostLoopback = decideHostLoopback(facts)
		hlsp.End()
		runCmd = append(runCmd, hostLoopback.args...)
		if hostLoopback.warning != "" {
			out.print(hostLoopback.warning)
		}
	}

	// What the jail is TOLD about all of that, rendered once for every shape a launch
	// can take — including the two branches above that never reach the decision at
	// all. The in-jail witness cannot recover any of it for itself: an unreachable
	// service looks identical whether yolo could not ask this host to forward
	// loopback (a known limitation), asked and was ignored (a fault), never reached a
	// conclusion, or had nothing to ask for because the jail shares this process's
	// namespace. Only some of those may ever fail a launch — OQ-R2 as scoped by
	// OQ-R3 and OQ-R5 — and none of that survives into the container by itself.
	//
	// THE SHARED CASE IS DECIDED HERE, NOT IN hostloopback.go, because the shapes
	// that have it never reach that file and must not: podman-in-podman is the
	// branch above (podman refuses a container carrying two network selectors, and
	// this is also the repo's own dev loop), and `network.mode: host` returns from
	// the decision before it looks at anything. A disposition computed inside a
	// function that did not run is a value that never arrives — measured exactly
	// that way in this repo on 2026-08-18, where the variable stayed absent in the
	// jail the patch was written for.
	//
	// It reads the SAME predicate the loophole runtime uses to choose each daemon's
	// advertised address, so the severity the witness applies and the address it
	// dials cannot disagree. It cannot mask a `requested` either: that value is only
	// ever produced on the default bridge mode, which is disjoint from every shape
	// sharesLauncherNetns accepts.
	disposition := hostLoopback.disposition
	if sharesLauncherNetns(rt, netMode, inContainer) {
		disposition = paths.HostLoopbackShared
	}
	runCmd = append(runCmd, jailLoopbackEnvArgs(disposition)...)

	// The in-jail witness's escape hatch, forwarded from the host environment where
	// the user types it. Outside the branch above on purpose: the witness runs
	// under every runtime and every network mode, so the way past it must too.
	runCmd = append(runCmd, o.reachabilityOptOutArgs()...)

	// The entrypoint's hold-on-refusal opt-in, forwarded from the host for the same
	// reason and on the same terms — plus the exec line only this side can compose.
	// It is NOT a second spelling of the hatch above: that one suppresses a refusal,
	// this one keeps it and only delays the teardown that erases the evidence
	// (holdonrefusal.go, internal/entrypoint/hold.go).
	runCmd = append(runCmd, o.holdOnRefusalArgs(rt, in.cname)...)

	// The readiness act's hatch and its off-switch, forwarded from the host for the same
	// reason: the stage that reads them runs in the jail (programreadiness.go).
	runCmd = append(runCmd, o.programReadinessArgs()...)

	// --- git identity + global gitignore (host-composed, :ro-mounted) ---
	runCmd = append(runCmd, o.gitIdentityMountArgs(rt, in.wsState, in.mountTargets)...)

	// --- publish + extra mounts ---
	runCmd = append(runCmd, publishArgs...)
	runCmd = append(runCmd, mountArgs...)

	// --- published-port DNAT sysctl + env ---
	// `applied == "bridge"` is spelled again here rather than left implied by a
	// non-empty publishArgs, because THIS is the flag the kernel refuses: podman
	// rejects the sysctl outright under host networking ("can't be set since Network
	// Namespace set to host") and the container never gets created. The publish gate
	// above already withholds every -p in that case, so the condition is redundant
	// today — deliberately, so that relaxing either half alone cannot resurrect the
	// pairing that failed the launch.
	if len(publishArgs) > 0 && rt == "podman" && applied == "bridge" {
		runCmd = append(runCmd, "--sysctl", "net.ipv4.conf.all.route_localnet=1")
		var publishedPorts []string
		if netSec := cfgMap(cfg, "network"); netSec != nil {
			for _, p := range asAnyList(mapGet(netSec, "ports")) {
				spec := pyStrCoerce(p)
				proto := "tcp"
				if i := strings.LastIndex(spec, "/"); i >= 0 {
					proto = spec[i+1:]
					spec = spec[:i]
				}
				parts := strings.Split(spec, ":")
				containerPort := parts[len(parts)-1]
				publishedPorts = append(publishedPorts, containerPort+"/"+proto)
			}
		}
		if len(publishedPorts) > 0 {
			runCmd = append(runCmd, "-e", "YOLO_PUBLISHED_PORTS="+jsonDumpsStrings(publishedPorts))
		}
	}

	// --- host port forwarding flags (the socat lifecycle is separate) ---
	runCmd = append(runCmd, o.forwardHostPortsArgs(rt, in.cname, forwardHostPorts)...)

	// THE SEAL (seal.go) withholds the next four groups: a fork build reaches no host service, and
	// is handed no host device — a device node is a write channel outside its workspace and home.
	if !in.sealed {
		// --- host services sockets dir + broker endpoint env ---
		runCmd = append(runCmd, o.hostServicesMountArgs(rt, in.cname, cfg)...)

		// --- device passthrough ---
		runCmd = append(runCmd, o.deviceArgs(cfg)...)

		// --- GPU warn + memlock + vendor-specific flags ---
		if gpuRequested && !gpuEnabled {
			out.print("[yellow]Warning: GPU requested but " + gpuUnavailableReason + " — " +
				"starting without GPU passthrough[/yellow]")
		}
		runCmd = append(runCmd, o.gpuArgs(cfg, rt, gpuEnabled, gpuVendor)...)

		// --- KVM ---
		runCmd = append(runCmd, o.kvmArgs(cfg, rt, slices.Contains(runCmd, "keep-groups"))...)
	}

	// --- resources ---
	runCmd = append(runCmd, o.resourceArgs(cfg, rt)...)
	// The disk I/O priority is not a limit and takes no runtime flag: the entrypoint
	// applies it from this variable (docs/design/io-priority.md §5.1). The decision is the
	// one the briefing reads (appliedIOPriority).
	runCmd = append(runCmd, ioPriorityEnvArgs(appliedIOPriority(rt, o.IsMacOS, cfgMap(cfg, "resources")))...)

	// --- host nvim config ---
	// Read once at boot (entrypoint copies /ctx/host-nvim-config into the jail's
	// ~/.config/nvim) — but the mount stays for the whole session, so on a backend that
	// ignores :ro (below acROBindsFloor) it is a live write channel into the user's real editor config. Refuse
	// rather than downgrade, and say so: the visible symptom is nvim coming up
	// unconfigured, which is otherwise an odd thing to have to explain to yourself.
	hostNvim := filepath.Join(homeDir(), ".config", "nvim")
	if isDir(hostNvim) && !in.sealed { // a fork build is handed no host config (seal.go)
		if reason := o.roBindsUnsupported(rt); reason != "" {
			out.print("[yellow]Skipping host nvim config (~/.config/nvim): " + reason + "[/yellow]")
		} else {
			runCmd = append(runCmd, "-v", hostNvim+":"+paths.ContextHostNvimDir+":ro")
		}
	}

	// --- shadow .overmind.sock ---
	// `<workspace>/.overmind.sock` is a host socket this jail cannot use (OVERMIND_SOCKET
	// points at /tmp/overmind.sock instead). Binding /dev/null over it makes the path read as
	// an EMPTY file rather than expose a socket the jail has no route to.
	//
	// ⚠ A SHADOW IS NOT A WRITE, and only the bind can tell them apart. `/workspace` is bound
	// READ-WRITE — that is the product — so `/workspace/.overmind.sock` IS the user's file on
	// the host. Emptying it from the entrypoint would not shadow it, it would TRUNCATE it.
	// Do not "simplify" this into a boot-time write; shadowbinds_test.go states the same.
	//
	// ⚠ A SECOND SHADOW WAS REMOVED HERE ON 2026-09-22, AND THE REMOVAL STATES A POSITION:
	// yolo does NOT shadow workspace MCP config, and does not want to. An agent finding the
	// workspace's `.vscode/mcp.json` is DESIRED — it is the repo's or the user's own config,
	// and keeping config from an agent is not a goal here. Do not re-add this as an
	// "isolation" measure; there is no such goal to serve. (It would not have served one
	// anyway — copilot loads three repo-root MCP files and Claude loads a fourth, so one
	// blanked file was never a boundary — which is the other reason to refuse that framing.)
	//
	// The costs that DID justify the removal are mechanical:
	//
	//   - A DEVICE NODE BREAKS GIT. The /dev/null bind makes the destination a character
	//     device (1:3), which git can neither hash nor `git add`; on a TRACKED file — a repo's
	//     committed `.vscode/mcp.json` — that is a permanently dirty, uncommittable path.
	//   - IT FIRED ON FILE EXISTENCE, NOT ON A READER, so every jail that had the file paid
	//     that cost whether or not anything in it read the file.
	//   - DO NOT "FIX" A RE-ADD BY BINDING AN EMPTY REGULAR FILE. That is fail-OPEN in the one
	//     way that matters: git would stage the empty blob, so an in-jail `git commit -a` would
	//     record an EMPTIED config. The device node fails closed, and that is the property to
	//     keep if this bind ever comes back.
	//
	// `.overmind.sock` shares none of that: it is a socket rather than a tracked file, and no
	// git operation needs it. Its shadow is about a ROUTE — the jail has no way to the host's
	// overmind daemon — not about keeping workspace content from an agent.
	var shadowed []string
	if fileExists(filepath.Join(o.Workspace, ".overmind.sock")) {
		shadowed = append(shadowed, ".overmind.sock")
	}
	if len(shadowed) > 0 {
		if rt == "container" { // parity: Warned — the /dev/null bind ARRIVES on AC but reads ENXIO, so the shadow cannot do its one job; no materialize escape exists, because a shadow is defined by NOT writing the host file
			// It was UNGATED until 2026-09-14, which made it the last emitter in this file
			// to have missed the rule five other sites each discovered by being broken
			// (acbindsources_test.go).
			//
			// ⚠ THE REASON MOVED THE SAME DAY, and the first one was wrong. This said
			// "Apple Container drops a bind whose host side is not a directory" — the
			// apple/container#1089 belief, cited here and in six other places and never
			// measured. MEASURED 2026-09-14 (macOS 26.5 arm64, `container` 1.1.0): the
			// /dev/null shadow ARRIVES, as a character special file. What it does not do is
			// WORK — virtiofs synthesises the node with the wrong major:minor (0:3002
			// against the container's own 1:3), so a read fails
			// `No such device or address` instead of returning empty.
			//
			// The skip is unchanged and the failure it avoids is now worse than the one
			// first described: not "the agent reads the real file" but "the agent gets an
			// I/O error from a path it expected to open". A shadow exists to read as EMPTY,
			// and ENXIO is not empty.
			out.print("[yellow]Not shadowing " + strings.Join(shadowed, ", ") +
				": on Apple Container a /dev/null bind arrives with the wrong device node, " +
				"so reading it fails instead of returning empty. The agent will see the " +
				"workspace's real path. Use `YOLO_RUNTIME=podman` to shadow it.[/yellow]")
		} else {
			for _, rel := range shadowed {
				runCmd = append(runCmd, "-v", "/dev/null:/workspace/"+rel+":ro")
			}
		}
	}

	// --- workspace-readonly overlays ---
	runCmd = append(runCmd, o.workspaceReadonlyMountArgs(cfg, rt)...)

	// --- per-side venv shadows ---
	runCmd = append(runCmd, o.venvShadowMountArgs(cfg, in.wsState)...)

	// --- user config mount (nested jails) ---
	// Not under the seal: the inherited copy of the user config is host config a fork build has
	// no use for, and it carries inline env_sources (seal.go).
	if !in.sealed {
		runCmd = append(runCmd, o.userConfigMountArgs(rt, in.wsState)...)
	}

	// --- MISE_DISABLE_TOOLS env ---
	// A sealed build hydrates no env_sources to read it from: hydrating one runs the commands and
	// reads the files it names on the host (seal.go).
	userEnv := jsonx.NewOrderedMap()
	if !in.sealed {
		userEnv = config.ResolveEnvSources(o.Workspace, cfg, nil)
	}
	// The merged mise_tools go in beside the user's list because the jail withholds yolo's
	// pnpm launcher for a declared mise pnpm, and mise must then be free to deliver it
	// (MergeMiseDisabledTools; misepnpm_test.go drives both halves).
	miseDisabled := config.MergeMiseDisabledTools(mapGet(userEnv, "MISE_DISABLE_TOOLS"),
		config.MergeMiseTools(cfg))
	runCmd = append(runCmd, "-e", "MISE_DISABLE_TOOLS="+miseDisabled)

	// --- store-prune gate (host-only) --- handled by the lifecycle phase
	// (needs live-container enumeration); the -e is inserted there. Placeholder
	// here keeps argv order: it is appended before skills.
	runCmd = append(runCmd, in.storePruneEnv()...)

	// --- store-delivered packages (C4/C5) ---
	// Emitted right after the prune gate and before the skills mounts, so the argv reads
	// in the order the pipeline decided things. Nothing here re-derives the plan: it was
	// settled before the image build, because the image build acts on it.
	runCmd = append(runCmd, in.storePackages.env()...)

	// --- skills mounts (selected agents with a skills dir) ---
	// PACK-DECLARED skills mounts. The SOURCE is the per-pack staging dir, not the
	// pack's own tree, because PrepareSkills merges three sources into it (built-ins <
	// pack skills < the user's own host skills) and that merge has to land somewhere.
	// Core reads the destination off the pack's declaration and mounts it; it does not
	// know the content is "an agent's skills".
	//
	// DEDUP BY DESTINATION, for the same reason briefings do below: `skills` is
	// CombineMerge — several packs into one dir IS the feature — and PrepareSkills has
	// already merged EVERY pack's skills into each staging dir (built-ins < all packs <
	// the user's own). So a second mount at one destination carries the same merged
	// content, and podman rejects it with "duplicate mount destination", failing the boot.
	//
	// This was pack-system.md §14's known sharp edge, worked around by telling authors not
	// to declare a `skills` contribution whose `into` duplicates another pack's. That
	// advice was unfollowable in the configuration it most matters for: an agent pack
	// naming ~/.claude/skills plus a user pack sharing a skills corpus is the whole point
	// of the kind. Fixed rather than documented (plan OQ-C).
	seenSkillDest := map[string]bool{}
	for _, target := range packSkillTargets(in.packs) {
		if seenSkillDest[target.Dest] {
			continue
		}
		seenSkillDest[target.Dest] = true
		runCmd = append(runCmd, "-v",
			filepath.Join(in.agentsPath, target.Staging)+":/home/agent/"+target.Dest+":ro")
	}

	// --- PACK MANIFESTS, read-only at /ctx/packs ---
	// The entrypoint renders each pack's declared SURFACES in-jail, so it needs the
	// same declarations the host just read. Mounting the staged tree is how they cross,
	// rather than an env var carrying serialized JSON: the tree is already staged (the
	// exec-bit and symlink-escape refusals in packstage have run on it), and a pack's
	// `files` and derive.lua have to exist at a path in-jail.
	//
	// :ro, and that is load-bearing rather than tidiness — a pack manifest is an INPUT
	// to composition, and an agent that could rewrite one in-jail could grant its own
	// pack a host file on the next boot.
	//
	// ⚠ THE `:ro` HALF DOES NOT SURVIVE ON APPLE CONTAINER, and the arm below says what
	// replaces it. Below acROBindsFloor that backend accepts `-v src:dest:ro` and IGNORES the suffix
	// (roBindsUnsupported), so there is no read-only bind to fall back to — a bind would
	// hand the jail WRITE access to the launcher's own pack tree, which is the tree the HOST
	// reads again on every attach to this jail (runningJailPackView), which is the escalation
	// above happening rather than being prevented. So the tree is COPIED into ws_state
	// instead: the copy is made from the host tree at the fresh launch and host-side
	// composition never reads it, so what an attach composes is decided from bytes the jail
	// cannot reach. What is
	// genuinely lost is within-session integrity of the jail's own copy — an agent can
	// rewrite the manifests it renders its OWN surfaces from, which is a subset of what it
	// can already do by writing its own home.
	//
	// A COPY USED TO LOSE THE ATTACH REFRESH, which a bind carried: an attach re-staged the
	// tree a podman jail binds. Nothing refreshes it now on either backend (OQ-PK2 (c),
	// packtree.go): a running jail keeps the pack tree it booted with, so this arm running
	// only on a fresh launch is the same behavior podman has. The host keeps the tree the copy
	// was made from until the container is known gone, since an attach reads it.
	if in.packStaging != "" {
		if rt == "container" { // parity: HonoredBy — the jail still gets the pack tree, as a per-launch ws_state COPY rather than a :ro bind AC would ignore
			if err := acMaterializeTree(in.packStaging, acPackRootRel, in.wsState); err != nil {
				// NO SILENT DROP. Saying nothing here is the whole of issue #44: the jail
				// comes up with guardrails, briefings and a managed settings.json, looks
				// provisioned, and has no agent in it. An unset YOLO_PACK_ROOT is the
				// honest state (LoadJailPacks reads it as "no packs" rather than as a
				// broken mount), so the line has to carry the diagnosis.
				out.print("[yellow]Warning: could not stage the pack trees for Apple " +
					"Container (" + err.Error() + ") — NO pack will render in this jail: " +
					"no pack-declared program will install and no pack launch flag will " +
					"apply, though shims and briefings still will, so the jail will LOOK " +
					"provisioned.[/yellow] The source is " + in.packStaging + " and the " +
					"destination is " + filepath.Join(in.wsState, acPackRootRel) +
					"; a source that has gone missing means a concurrent launch's " +
					"housekeeping reaped it (re-run), and a destination that cannot be " +
					"written usually means a full or read-only disk.")
			} else {
				runCmd = append(runCmd, "-e", "YOLO_PACK_ROOT="+acPackRootInJail)
			}
		} else {
			runCmd = append(runCmd, "-v", in.packStaging+":"+packCtxDir+":ro",
				"-e", "YOLO_PACK_ROOT="+packCtxDir)
		}
	}

	// --- INSTALL CAPTURES, read-only at /ctx/captures ---
	// The machine's capture store, so a native launcher can materialize an already-captured
	// install instead of downloading it (captures.go says why it is a mount and why :ro).
	runCmd = append(runCmd, o.capturesArgs(rt, in.capturesDir)...)
	// --- FORK BUILDS: which store entry each forked program materializes (forkbuild.go) ---
	// Beside the store it reads from. The fork lock is in the user config directory, which no jail
	// can read, so the host decides the entry and hands the jail its key, or why it has none (FP-D8).
	if wire := entrypoint.ForkBuildsWire(in.forkDeliveries); wire != "" {
		runCmd = append(runCmd, "-e", entrypoint.ForkBuildsEnv+"="+wire)
	}
	// --- PATCHED EXTENSIONS: what each one was handed, and whether its agent's launchers stop ---
	// (patchedtrees.go, docs/design/patched-extensions.md PPX-D8, PPX-D18).
	if wire := entrypoint.PatchedTreesWire(in.patchedTrees); wire != "" {
		runCmd = append(runCmd, "-e", entrypoint.PatchedTreesEnv+"="+wire)
	}
	// And in a patched extension's own sealed build jail, which extension it builds (PPX-D30).
	if in.sealed && in.sealedTree != "" {
		runCmd = append(runCmd, "-e", entrypoint.TreeBuildEnv+"="+in.sealedTree)
	}

	// --- host files (pack-declared, origin-gated) ---
	// --- pack `mount` contributions: host-home dir/file :ro under /ctx ---
	// Neither under the seal: a fork build reads no file of the host's home, a surface's host
	// layer included (seal.go).
	if !in.sealed {
		runCmd = append(runCmd, o.hostFileArgs(in)...)
		runCmd = append(runCmd, o.hostMountArgs(in)...)
	}

	// --- pack `env` contributions: static jail env vars ---
	// NOT on the argv. The pack env fold crosses in yolo-user-env.sh's channel
	// section with everything else the launch composed per-entry (writeUserEnvFile's
	// doc): an `-e` here would freeze this launch's fold into the container's
	// environment, where a later exec inherits it as stale provider state — the
	// frozen-copy defect per-entry delivery exists to remove. The macos-user arm
	// still delivers the fold to its own plan env (macosuser/runplan.go), which has
	// no attach and no frozen copy to leak. The merge itself is unchanged and still
	// composed once, in the channel: the CLI-keyed profile table folded in
	// (EnvVarsFor, not a static-only fold) so a selected variant later-wins over the
	// pack's own static value (OQ-8), and a null in it removes the key — the file is
	// rewritten whole by every entry, so "unset" is simply an absent line.

	// --- user host_files: :ro source mounts, writable destinations, wire env ---
	// Order within the group is fixed: the destination's writable subtree must be
	// declared alongside the other home binds (podman sorts by destination depth,
	// so adjacency is cosmetic, but a deterministic argv is not), then the :ro
	// source inputs under /ctx, then the resolved-entry env the entrypoint decodes.
	runCmd = append(runCmd, o.hostFileWritableDirArgs(in)...)
	runCmd = append(runCmd, o.hostUserFileArgs(in)...)
	// ONE YOLO_CTX_ROOT for both host-file emitters above (see acCtxMaterialized).
	if in.acCtxMaterialized {
		runCmd = append(runCmd, "-e", "YOLO_CTX_ROOT=/home/agent/"+acCtxDirRel)
	}
	runCmd = append(runCmd, o.hostFilesEnv(in)...)
	// What the launcher delivered, for the jail's fail-closed host-layer read. AFTER
	// hostFileArgs, which is what fills it in.
	runCmd = append(runCmd, o.hostLayerEnv(in)...)

	// --- PACK-DECLARED briefings ---
	// Same Apple-Container single-file-mount limitation as yolo-user-env.sh: AC
	// materializes the staged briefing into ws_state. Skipping the container branch
	// silently dropped every briefing on that backend.
	//
	// The destination list and the staging filename both come from briefingdest.go, which is
	// the ONE place either is computed — refreshJailBriefings writes through the same two
	// functions. That coupling used to be a comment saying the names must match, and a
	// mismatch is silent: a missing bind source for a FILE is not an error the way a missing
	// dir is, so the jail simply comes up with a blank briefing (docs/reference/agent-briefings.md#ba-r2).
	//
	// The DEDUP-BY-DESTINATION that used to sit here is in briefingDestinations now, for the
	// same reason: `briefing` is CombineConcat — several packs contributing prose at one path
	// is designed behavior, and the composed staging file already holds all of it — but podman
	// rejects a duplicate mount destination and kills the boot, so exactly one bind per path
	// may be emitted. Keeping that rule beside the write half is what makes the file that is
	// composed and the file that is mounted the same file by construction.
	for _, d := range briefingDestinations(in.packs) {
		staged := filepath.Join(in.agentsPath, briefingStagingName(d.Into))
		if rt == "container" {
			acMaterialize(staged, d.Into, in.wsState)
		} else {
			runCmd = append(runCmd, "-v", staged+":/home/agent/"+d.Into+":ro")
		}
	}

	// --- PACK-DECLARED `files` trees ---
	// An opaque tree the pack owns, bound :ro at its home-relative `into`. Emitted
	// beside skills and briefing because it is the third staged-tree kind — the one
	// that shipped inert (plan N1). See packfiles.go for the AC single-file split and
	// why the source is the STAGED tree.
	runCmd = append(runCmd, o.packFilesMountArgs(in)...)

	// --- TERM, the color environment (COLORTERM, NO_COLOR) + timing ---
	if term := o.Getenv("TERM"); term != "" {
		runCmd = append(runCmd, "-e", "TERM="+term)
	}
	if ct := o.Getenv("COLORTERM"); ct != "" {
		runCmd = append(runCmd, "-e", "COLORTERM="+ct)
	}
	runCmd = append(runCmd, o.noColorEnvArgs()...)
	if o.timingReporting() {
		// Renamed from YOLO_PROFILE (design D13). It was named for the flag that used
		// to own this meaning (--profile, before docs/reference/providers.md OQ-PT5
		// renamed it --timing), and it stayed put on the belief that a rename was a
		// host->jail contract change no step here owned. That premise was false: the
		// launcher emits this pair AND generates the bash that the block belongs to
		// (command.go's buildSessionCmd), so both halves are host-side and move
		// in one commit. The old spelling was also a strict prefix of YOLO_PROFILES —
		// the resolved auth-profile table, which IS read in the jail — so one grep
		// conflated two unrelated mechanisms. NOT named YOLO_TIMING: that is paths.
		// TimingEnv, a host-process opt-in deliberately never forwarded (D5), and
		// reusing it here would make forwarding look intended.
		//
		// The REPORTING gate (D12), not the recording one: this pair is what makes the
		// in-container half PRINT its `=== YOLO Jail Profile ===` block, and the jail
		// records into ~/.yolo-perf.log either way. So a persistent opt-in
		// (`perf_logging: true`, an exported YOLO_TIMING/YOLO_VERBOSE) leaves it off and
		// only an explicit --timing/--verbose puts it on the argv.
		runCmd = append(runCmd, "-e", "YOLO_JAIL_TIMING=1")
	}

	// --- host-side loopholes runtime args (--add-host, CA mounts, env) ---
	// The SAME call emits the one YOLO_JAIL_DAEMONS payload — the loopholes' own
	// jail daemons and the pack service contributions' together (packservices.go;
	// wire-bridge.md §2.1) — so a service daemon can never land on a second -e of
	// the same name and lose to the runtime's duplicate resolution. The payload is
	// INPUT here (in.jailDaemons), composed above the backend dispatch. The same call
	// first writes every declared mount sentinel (prepareMountSentinels), the inert marker
	// a credential service's nonempty state_files list names so its cache never crosses.
	//
	// NONE UNDER THE SEAL (seal.go): a fork build runs no loophole — no CA it trusts, no
	// --add-host, no jail daemon, no endpoint — so none of this is emitted, the witness's
	// registration below included.
	if !in.sealed {
		runCmd = append(runCmd, o.loopholesRuntimeArgs(cfg, rt, in.jailDaemons)...)

		// --- jail-facing service endpoint env (the witness's registration) ---
		// Beside the composition above on purpose: a service whose daemon joins
		// YOLO_JAIL_DAEMONS here is the same service whose endpoint file the env
		// var below advertises to the in-jail reachability witness — emitted only
		// when the launch has decided the daemon will actually serve (§5's
		// WARNING), so an idle bridge never registers.
		runCmd = append(runCmd, serviceEndpointEnvArgs(in, o)...)
	}

	// --- the host path map (docs/design/in-jail-nix-roots.md §4) ---
	// LAST BEFORE THE IMAGE, because it is read off every mount above: which host directory
	// each one is, so an in-jail `yolo` can register its GC roots under the host's spelling
	// (translatedroots.go). Only with the host nix daemon mounted, the one reader.
	runCmd = append(runCmd, o.hostPathMapEnvArgs(rt, in.sealed, runCmd)...)

	// --- image + entrypoint ---
	//
	// The entrypoint is named by ABSOLUTE PATH into the mounted install prefix,
	// not by the bare "yolo-entrypoint" this line carried while the binary was
	// baked. The image's Config.Entrypoint is null and Cmd is ["/bin/bash"], so
	// what follows the image ref IS the container command: a bare name resolves
	// on the image's own PATH (/bin:/usr/bin) and would reach the binary through
	// /bin/yolo-entrypoint — which is now a symlink INTO the mount. Naming the
	// path directly means a launch that failed to mount the prefix dies saying
	// which file is missing instead of "failed to exec pid1".
	runCmd = append(runCmd, in.jailImage(), JailEntrypointPath)
	return runCmd
}

// composedProviders is the providers table this launch carries: the user's `providers`
// config entries with every selected pack's `kind: "provider"` service facts composed
// UNDER them, per field (packload.ComposeProviders). It is the ONLY place that composition
// happens — the result crosses to the jail whole and the derives read it verbatim — so a
// user override of one model alias cannot cost the pack's endpoints, and nothing
// downstream re-derives a second copy of the merge.
//
// The error is a launch refusal, not a degraded table: composition can manufacture the
// base_url+endpoints pair every consumer would resolve differently, and handing the launch
// a table like that is the defect the refusal exists to prevent.
//
// served is what this launch's notch serves (servedDaemons): the composition leaves out every
// adapter address nothing here serves, and the second result names those adaptations for the
// credential gate, so a pairing only one of them resolves refuses saying why
// (packload.ComposeProvidersAt, the one composition every notch calls).
func composedProviders(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	served packload.ServedDaemons) (*jsonx.OrderedMap, []packload.Adaptation, error) {
	// The user's adapter address overrides, read from the USER FILE DIRECTLY rather than
	// from cfg — which is what makes workspace scope inexpressible for a key that decides
	// where inference goes (config/adapters.go, the rule `packs` and `profiles` follow).
	// A read problem is a warning and a skip there, so a launch degrades to the addresses
	// the packs declared rather than refusing over a key it could not parse.
	addresses, _ := config.LoadAdapterAddresses(nil)
	return packload.ComposeProvidersAt(cfgMap(cfg, "providers"), packs, addresses, served)
}

// servedDaemons is the "served at this notch" set for a launch whose composed jail-daemon
// payload is specs (jailDaemonsFor): the daemons this launch SERVES
// (loopholes.ServedJailDaemonNames, the one split `yolo check` predicts with too) — on a
// container runtime every one, in the jail, and on macos-user the ones its Seatbelt guest runs
// since OQ-DP8/OQ-DP9 plus the doorways the launch opens outside it (macosuserdoorways.go,
// host-notch-services.md HS-D15), the rest declined by name (noteMacosUserJailDaemonDeclines)
// (docs/plans/notch-convergence.md §4 item 2) — and the bound loopholes its argv binds
// (loopholes.JailBoundNames, none on macos-user).
//
// It also carries WHERE each serves (servedaddresses.go): every daemon's served listen address,
// read off the payload, and the declared-to-served map for the pack services' adapter and via
// addresses, so the provider table, the via base and the pack env pointers compose the same
// ports the payload hands the daemons.
func (o *Options) servedDaemons(specs []loopholes.JailDaemonSpec) packload.ServedDaemons {
	names, listen := loopholes.ServedJailDaemonNames(o.runtime, specs)
	// The BOUND LOOPHOLES the argv binds are served by name too (jailDaemonsFor recorded them
	// beside this payload): their pointers name a path in the jail that exists exactly when the
	// bind does (docs/design/loophole-packaging.md LP-D1).
	names = append(append([]string(nil), names...), o.jailBound...)
	served := packload.ServedInJail(names).WithListen(listen).WithRebind(o.movedServedAddresses())
	if o.runtime == "macos-user" { // parity: Warned — the Seatbelt sandbox binds nothing (loopholes.JailBoundNames is nil there), so a pointer at what a loophole binds is withheld and the launch names it (packload.UnservedEnvLines)
		served = served.MountsNothing()
	}
	// A macos-user launch also serves the launch-owned services it planned (macosuserservices.go):
	// a pack service's host half at the ports it picked, since its guest declines the service's
	// jail daemon (loopholes.JailDaemonsRunIn).
	if o.runtime == "macos-user" && len(o.launchServices) > 0 { // parity: NotApplicable — the macos-user arm's own launch-owned services; a container runs the service's jail daemon
		return served.Plus(launchservice.Served(o.launchServices))
	}
	return served
}

// commonEnvBlock builds the big -e env block. Frozen contract (order and
// content must not drift — yolo-entrypoint reads these exact vars).
func (o *Options) commonEnvBlock(in *assembleInput, blockedConfigJSON, netMode string) []string {
	cfg := in.cfg
	// None under the seal (seal.go): a server's literal env is a credential, and a build runs no
	// agent to start one.
	lspServers, mcpServers, mcpPresets := agentServerTables(cfg, in.sealed, in.packs)
	env := []string{
		"-e", "JAIL_HOME=/home/agent",
		"-e", "NPM_CONFIG_PREFIX=/home/agent/.npm-global",
		"-e", "NPM_CONFIG_CACHE=/home/agent/.cache/npm",
		// Env hygiene, as the host floor's npm has it (hostfloor.Floor.npmEnv): no "new
		// version of npm available" box and no funding line on an install that succeeded,
		// two notices an agent reads as output (TestAssembleSilencesNpmNotices).
		"-e", "NPM_CONFIG_UPDATE_NOTIFIER=false",
		"-e", "NPM_CONFIG_FUND=false",
		"-e", "GOPATH=/home/agent/go",
		"-e", "MISE_DATA_DIR=/mise",
		"-e", "MISE_CACHE_DIR=/tmp/mise-cache",
		"-e", "MISE_PYTHON_PRECOMPILED_FLAVOR=install_only",
		"-e", "MISE_PYTHON_GITHUB_ATTESTATIONS=false",
		"-e", "MISE_TRUSTED_CONFIG_PATHS=/workspace",
		"-e", "MISE_ENV=jail",
		"-e", "RUSTUP_HOME=/mise/rustup",
		"-e", "CARGO_HOME=/mise/cargo",
		"-e", "MISE_YES=1",
		"-e", "COPILOT_ALLOW_ALL=true",
		"-e", "IS_SANDBOX=1",
		// Retained deliberately (not redundant cleanup): this mirrors the value
		// baked into the OCI image's config.Env (flake.nix), but re-asserting it
		// on -e makes the launch env self-describing and independent of whichever
		// image tag podman resolves — a `yolo run` that (mis)loads an image
		// without the baked env still gets a correct LD_LIBRARY_PATH. It is the
		// dlopen-by-soname discovery path for nix-built processes (which never
		// traverse /lib64 and so are unreachable by nix-ld); nix-ld handles the
		// FHS-binary case. See docs/reference/mise-node-dynamic-linking.md, "The
		// three library paths", item 3.
		"-e", "LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/" + storage.LinuxMultilib(),
		"-e", "HOME=/home/agent",
		"-e", "EDITOR=cat",
		"-e", "VISUAL=nvim",
		// No PI_TELEMETRY=0 here any more: it is pi's variable, so the pi pack's `env` sets
		// it, and core names no agent (docs/design/agent-directory-map.md Appendix B).
		"-e", "PAGER=cat",
		"-e", "GIT_PAGER=cat",
		"-e", "YOLO_BLOCK_CONFIG=" + blockedConfigJSON,
	}
	if in.hostTZ != "" {
		env = append(env, "-e", "TZ="+in.hostTZ)
	}
	// The channel (providers, profile tables, pack env fold, shape vars) is composed
	// ONCE above the backend dispatch and does NOT pass through this block: its
	// container-backend crossing is yolo-user-env.sh's channel section plus each agent's
	// own env file (deliverChannel at the run.go call sites — the credential gate's
	// per-agent half), and the macos-user arm delivers the same channel to its own plan
	// env and bootstrap.
	env = append(env,
		"-e", "YOLO_HOST_DIR="+o.Workspace,
		"-e", "YOLO_VERSION="+in.yoloVersion,
		"-e", "OVERMIND_SOCKET=/tmp/overmind.sock",
		"-e", "YOLO_MISE_TOOLS="+jsonDumps(config.MergeMiseTools(cfg)),
		"-e", "YOLO_LSP_SERVERS="+jsonDumpsOrEmptyObj(lspServers),
		// THE COMPOSED TABLE (packload.ComposeMCPServers, through agentServerTables): each selected
		// pack's `mcp` entries joined to the jail's home, your mcp_servers merged over them. Composed
		// here, on the host, for this backend's home (docs/design/mcp-presets-removal.md OQ-MP4); the
		// jail reads it as it always read your table, over its presets. None under the seal.
		"-e", "YOLO_MCP_SERVERS="+jsonDumpsOrEmptyObj(mcpServers),
		"-e", "YOLO_MCP_PRESETS="+jsonDumpsOrEmptyList(mcpPresets),
		// The `agent_updates` policy, read from USER scope directly rather than from the
		// merged config: /workspace is bind-mounted rw, so a workspace value would let an
		// agent freeze its own updates (config.AgentUpdatesWire). Emitted on EVERY launch,
		// empty when the key is absent — the jail defaults OPEN either way, so an older
		// host that emits nothing and this one emitting "" read identically.
		"-e", entrypoint.AgentUpdatesEnv+"="+config.AgentUpdatesWire(),
		// THE CONTRACT TAGS (entrypoint.ContractTagsEnv, contracttags.go): what this jail's
		// binaries can receive from a later attach — among them `agent-env-files`, which
		// replaced the per-agent env marker (entrypoint.AgentEnvFilesEnv) this line used to
		// freeze. FROZEN into the container on purpose, since that is the one record of this
		// launch a later attach can inspect: an attach from a newer yolo compares the tags
		// it needs against these, and a missing one never rides along silently.
		"-e", entrypoint.ContractTagsEnv+"="+launchContractTagsValue(),
		// THE MAIN PROCESS IS A HOLD (entrypoint/jailmain.go): the container's pid 1 boots and
		// holds, and every session enters by exec. Frozen into the container so that every
		// session's entrypoint inherits it and waits for the boot and for provisioning, and so
		// that the host can read it off an inspect (jailSessionCount).
		"-e", entrypoint.JailMainEnv+"="+entrypoint.JailMainHold,
		// The three provider/profile wire tables are NOT here: they cross in
		// yolo-user-env.sh's channel section (writeUserEnvFile's doc) with the shared
		// pack env fold, the per-agent values in each agent's own env file
		// (deliverChannel), so the container's frozen environment holds no
		// provider state for a later exec to inherit — per-entry delivery. The channel
		// below is still composed once, above the backend dispatch, and is what the
		// file section and the macos-user plan env both consume.
		//
		// YOLO_REQUIRED_CAPABILITIES IS NOT HERE ANY MORE (2026-09-17), and the reason
		// is the gate that replaced it. The variable carried `required_capabilities`
		// into the jail "so a jail can read what was asked for" and no reader ever
		// appeared — the key was validated, exported, and consumed by nothing on any
		// backend (setup-support-gaps.md G9; declaration-parity.md DP-B30, which also
		// records that macos-user never set it at all, because this writer sits below
		// that arm's return). OQ-CAP2's fatal refusal now answers the requirement on the
		// HOST, above the backend dispatch (preflight.go's refuseUnmetCapabilities), so
		// by the time a container starts the declaration has already been judged and an
		// unread copy of it on the argv would be a promise nothing keeps. What a nested
		// launch needs still crosses: `required_capabilities` is in BOTH inherit scopes
		// (config/inherit.go), so an inner jail inherits the declaration in its config
		// and re-runs the same gate over it.
		"-e", "YOLO_RUNTIME=podman",
	)
	// programs.autoprune (OQ-PD4's third clause, program-delivery.md §10 step four) —
	// EMITTED ONLY WHEN ON, which is the opposite of YOLO_HOST_LOOPBACK's always-emit rule
	// and for a reason that inverts it. There, an absent variable had to be
	// distinguishable from a decision, because "the launcher never asked" and "the launcher
	// asked and it failed" call for different severities. Here every way the variable can
	// be absent — an older launcher, a backend that emits no env, a config that never
	// mentions it — means the same thing, and it is the ruled default: OFF. A `=0` would
	// add a spelling of "off" without adding an answer.
	//
	// The value is read from the USER config directly rather than off the merged cfg above:
	// a key that authorises deleting binaries must not be settable by a repo-committed,
	// agent-editable workspace config (config.ProgramsAutoprune, and validatePrograms
	// reports a workspace-scoped one as an error rather than ignoring it).
	if config.ProgramsAutoprune(nil) {
		env = append(env, "-e", "YOLO_PROGRAMS_AUTOPRUNE=1")
	}
	// The profile-derived provider environment — the env shape a provider declares for
	// the protocol an agent speaks (providers.md#pv-oq-14, since superseded by #oq-cs8), which for bedrock is AWS_REGION and the
	// model ids — is composed by internal/agentenv, which is ALSO what
	// `yolo host -- <agent>` applies on the host. One implementation, so the two notches
	// cannot drift — that is the jail/host parity claim in host-agent-environment.md §2.2,
	// and it is not a claim two copies of this block could keep. This used to be thirty
	// lines of nested type assertions inline here, covered by no test at all.
	//
	// NOT on the argv either, for the per-entry reason the tables above cite: the shape
	// vars (ANTHROPIC_BASE_URL and its kin, credentials included) cross in the
	// yolo-user-env.sh channel section, which every shell sources and every boot
	// hydrates — and which a credential should prefer to a `ps`-visible argv line in any
	// case. The macos-user arm still delivers the same list to its plan env, claude's
	// CLAUDE_CODE_USE_BEDROCK among them since OQ-BR8 moved it into claude's env derive; a
	// pack's gated env rides the pack env fold, through the same selection, into the env
	// file of each agent whose selection it gates.
	//
	// No YOLO_REPO_ROOT: the in-jail CLI resolves its repo root the same way the
	// host does — exe-relative to the baked /opt/yolo-jail bundle, or the
	// live-mounted /workspace checkout when self-hosting (internal/reporoot).
	// There is no jail-special env override any more.
	_ = netMode
	return env
}

// effectiveUseProfiles folds the launch's two profile sources, the config's `profile` key
// and then the -p flags, through the one fold both spellings share (config.ProfileTableFor,
// PP-D10). So every `-p` form beats every key form for each CLI it reaches — the precedence
// the persistent table always had under the flag — and within each source a named CLI keeps
// its own entry while the source's default (a bare -p, the key's string or list form or its
// "*") reaches every other CLI this pack set installs. Every key in the result is a CLI name —
// the bin a pack installs — which is what makes the table readable as "the profile each CLI
// runs" and what lets `yolo check` and the launch pre-flight validate it against one
// namespace.
//
// A default reaches the CLIs each selected pack installs, not the pack slug — the table's
// keys are CLI names everywhere else, and a derive reads its own bin. A pack that installs
// nothing gets no key (no CLI to select for) but is still a receiver of the table, which is
// what the launch line says. It never keys on the command after `--` (the 2026-09-03 ruling):
// a short option whose meaning depends on a token further down the argv is the confusion the
// ruling removed — name the CLI explicitly with -p <cli>=<name> when the distinction matters.
//
// A value is an agent's ACTIVE SET (docs/design/active-provider-sets.md): a string for a set
// of one, an array for more. A pair or a named key entry replaces that CLI's whole set, never
// appending (AP-D4), and a BARE list (the key's string or list form, "*", or a -p naming no
// agent) goes whole to every CLI whose pack declares provider_sets and its first entry to every
// other (OQ-AP3), which the channel's bareNote says, over the same fold (profileFold).
//
// A method on Options taking the config and the pack set, rather than a method on
// assembleInput, because it has TWO consumers that must agree byte for byte: the env
// block below (the jail's copy of the table) and the launch's profile disclosure line,
// which describes the same table to the human. One merge, so neither can drift.
func (o *Options) effectiveUseProfiles(cfg *jsonx.OrderedMap, packs []*packload.Pack) *jsonx.OrderedMap {
	return o.profileFold(cfg, packs).Table
}

// profileFold is the fold behind effectiveUseProfiles: the config `profile` key, then the -p
// flags, over the receivers packs make (config.ReceiversOf).
func (o *Options) profileFold(cfg *jsonx.OrderedMap, packs []*packload.Pack) config.ProfileFold {
	return config.FoldProfiles(config.ReceiversOf(packs), config.ConfigProfileSelection(cfg),
		o.ProfileFlags())
}

// profileFoldFromKey is profileFold's selection index for the config key: the key is handed
// first, the flags second.
const profileFoldFromKey = 0

// ProfileFlags is this launch's -p/--profile selection in the shape the config `profile` key
// lowers to (config.ProfileSelection): ProfileName is its default and UseProfiles its named
// entries, each the comma-joined list the grammar checked (AP-D11), split back by
// packload.SplitProfileList. The one bridge from the flag fields to the fold, so the flag cannot
// be read any other way than the key is.
func (o *Options) ProfileFlags() config.ProfileSelection {
	sel := config.ProfileSelection{Default: packload.SplitProfileList(o.ProfileName)}
	if o.UseProfiles != nil {
		sel.Named = make(map[string][]string, len(o.UseProfiles))
		for cli, list := range o.UseProfiles {
			sel.Named[cli] = packload.SplitProfileList(list)
		}
	}
	return sel
}

// SetProfileFlags stores sel as this launch's -p selection, each list comma-joined
// (ProfileFlags' inverse).
func (o *Options) SetProfileFlags(sel config.ProfileSelection) {
	o.ProfileName = strings.Join(sel.Default, ",")
	o.UseProfiles = nil
	if sel.Named != nil {
		o.UseProfiles = make(map[string]string, len(sel.Named))
		for cli, set := range sel.Named {
			o.UseProfiles[cli] = strings.Join(set, ",")
		}
	}
}

// unsetImageRef is what the argv gets when nobody threaded a ref in. It is
// deliberately NOT a working name.
//
// The obvious alternative — fall back to the legacy :latest constant — is the
// exact defect C2 exists to remove: assembly would quietly name a different
// image than the one the load pipeline prepared, and the launch would look fine
// while running whatever :latest happens to point at. This value cannot resolve,
// so a launch that reaches it fails immediately with a runtime error naming the
// placeholder, one line from its cause. In-package tests that do not care about
// the image tail also land here, which is why it reads as an obvious sentinel
// rather than as a plausible ref.
const unsetImageRef = "yolo-jail:<image-ref-not-threaded>"

// jailImage returns the image ref this launch must run. There is no per-runtime
// branch left: image.JailImageRef already spells the ref the way the runtime
// wants it (Apple Container drops the localhost/ prefix), and it did so at the
// point where the store path was known.
func (in *assembleInput) jailImage() string {
	if in.imageRef == "" {
		return unsetImageRef
	}
	return in.imageRef
}

// jsonDumps renders v as compact JSON.
func jsonDumps(v any) string {
	s, _ := jsonx.DumpsCompact(v)
	return s
}

// jailMCPServers is the mcp_servers table a container jail renders: the selected packs' `mcp`
// entries joined to the jail's home, under the config's own `mcp_servers`.
func jailMCPServers(cfg *jsonx.OrderedMap, packs []*packload.Pack) *jsonx.OrderedMap {
	return packload.ComposeMCPServers(cfgMap(cfg, "mcp_servers"), packs, jailHome)
}

func jsonDumpsOrEmptyObj(m *jsonx.OrderedMap) string {
	if m == nil {
		return "{}"
	}
	return jsonDumps(m)
}

func jsonDumpsOrEmptyList(l []any) string {
	if l == nil {
		return "[]"
	}
	return jsonDumps(l)
}

func jsonDumpsStrings(ss []string) string {
	arr := make([]any, len(ss))
	for i, s := range ss {
		arr[i] = s
	}
	return jsonDumps(arr)
}

// asAnyList coerces a decoded value to []any (nil when absent/non-list).
func asAnyList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// pyStrCoerce renders a config port entry (int/str) as a string. Ints render
// without ".0"; strings verbatim.
func pyStrCoerce(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	}
	if lit, ok := jsonx.AsIntLiteral(v); ok {
		return lit
	}
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	s, _ := jsonx.DumpsCompact(v)
	return s
}

func resolveExpand(p string) string {
	return resolvePath(expandUser(p))
}

// insertAt inserts v at index i.
func insertAt(s []string, i int, v string) []string {
	out := make([]string, 0, len(s)+1)
	out = append(out, s[:i]...)
	out = append(out, v)
	out = append(out, s[i:]...)
	return out
}
