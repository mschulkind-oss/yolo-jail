package run

// seal.go is THE SEAL (docs/design/forked-programs-as-packs.md FP-D9): the launch shape a fork's
// BUILD runs in. A fork's build is arbitrary code from a repository a pack named, so the build
// jail is handed no credential and nothing that writes outside its own workspace and home. The
// word and the next one are this file's own:
//
//   - THE SEAL (coined by the forked-programs plan): Options.Sealed, and every decision below.
//   - A CROSSING SITE (coined there too): any place in this pipeline that hands a jail something
//     of the host's — an env pair, a bind, a started host service.
//
// # The crossing sites, and what each withholds under the seal
//
// Each is withheld WHERE THE LAUNCH HANDS IT OVER, never by narrowing the loaded config: much of
// the run pipeline reads the user config file directly rather than the loaded value, so a
// narrowed copy would leave those readers crossing anyway.
//
//	the composed channel (Run)             env_sources, pack env, provider credentials and
//	                                       profiles: sealedChannel, delivered as empty files
//	the jail-daemon payload (Run)          none — no loophole or pack service runs in the jail
//	the broker singleton (runContainer)    not ensured
//	host loopholes and services (the       none disclosed and none planned (plannedLoopholeNames),
//	keeper's, keeper.go)                   the plan sealed, and the keeper starts none and
//	                                       registers no credential view; it still holds the
//	                                       container, its records and its teardown (FP-D15)
//	cache_relocations, the host-CAS alias  none: ~/.cache is the build's own
//	host_files                             none
//	machine-scope pack dirs                neither made nor bound, nor claude's secure storage
//	~/.cache and /mise                     private directories of the build's workspace
//	host port forwards, published ports    none
//	the host's network (resolveNetMode,    the runtime's own bridge, whatever `network.mode`
//	assembleRunCmd)                        says, and no host-loopback forwarding (FP-D13); a
//	                                       nested launch is still forced onto its launcher's
//	                                       namespace, which is itself a jail's. On macos-user,
//	                                       which shares the host's stack, the sealed Seatbelt
//	                                       profile denies the loopback instead (FP-D24)
//	`mounts`, pack `mount`, reads-host     none, the surfaces' host layers included
//	the host briefing prepend              none
//	the host's global gitignore            not bound, nor named by the composed git config
//	                                       (gitIdentityMountArgs); the identity stays
//	the nix daemon socket                  not bound; the store stays mounted read-only when
//	                                       store-delivered packages need it
//	devices, GPU, KVM                      none
//	the host nvim config                   not bound
//	the inherited user config              not bound
//	the workspace's assembled config copy  an empty object (assembledConfigFor, FP-D20):
//	(writeLaunchConfigArtifacts)           the merged config holds inline env_sources, and
//	                                       the build's workspace is bound read-write
//	the MCP and LSP tables and the MCP     empty (agentServerTables, FP-D20): a server's
//	presets (commonEnvBlock), and the LSP  literal env or args is a credential, and a build
//	plugin from lsp_servers                runs no agent; so the jail's bootstrap installs
//	(refreshJailBriefings)                 no preset's npm package either
//	env_sources' MISE_DISABLE_TOOLS        not hydrated
//	the jail's briefing                    describes only what crosses (sealedBriefingInput,
//	(refreshJailBriefings)                 FP-D23): no loophole, context mount, port, host
//	                                       nix daemon or forwarded host loopback, and no
//	                                       machine-wide store; and no `agents_md_extra`,
//	                                       which is the user's own text
//	the whole macos-user arm (Run)         left above its first crossing site
//	                                       (runSealedMacosUser): no context mount, relocation,
//	                                       host service, keeper, doorway, credential view, host
//	                                       bytes or herdr pane; the backend's own seal is the
//	                                       sealed capture profile, which also denies the nix
//	                                       daemon's socket (FP-D24)
//
// WHAT THE LAUNCH PRINTS FOLLOWS WHAT CROSSES (FP-D21). The read disclosure (notePackHostAccess)
// keeps only the claims about what the build itself fetches or runs (sealKeepsClaim), and says in
// one counted line which declared env vars, host reads and loophole crossings it withheld
// (sealedWithheldLine): a disclosure of a read that does not happen is worse than silence (DP-B2).
// The host-execution disclosure is not printed at all, since nothing runs on the host.
//
// The exec disclosure's reader, hostServiceNames, stays seal-blind on purpose (keeper.go): on
// macos-user the disclosure and the spawn are one call (startLoopholesDisclosed), which a sealed
// launch never reaches.
//
// What stays is TOOLCHAIN, not credential (FP-D9): the image, `packages`, `mise_tools` and a
// base's `node_floor` (installed into the private /mise, at the cost of that download once per
// build), the git identity (a name and an address), and the network — the runtime's bridge, never
// the host's — which a build needs for its dependencies and which the launch discloses.
//
// The selection is narrowed as well (Options.OnlyPacks): every other selected pack's loopholes and
// machine-scope directories are channels the build does not need.

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sealedStoreLeaves are the private ~/.cache and /mise of a sealed build, under its workspace's
// state dir: the build's own, so nothing it writes there reaches any other jail.
const (
	sealedCacheLeaf = "build-cache"
	sealedMiseLeaf  = "build-mise"
)

// sealedChannel is the channel a sealed launch delivers: no env_sources, no pack env, no provider,
// no profile and no caller token. Its credential scope is the gate's answer to an empty input, so
// every reader of the channel reads a well-formed empty one rather than a nil.
func sealedChannel() *packChannel {
	served := packload.NothingServed()
	scope, _ := packload.ScopeCredentials(packload.ScopeInput{Served: &served})
	return &packChannel{
		profiles:  jsonx.NewOrderedMap(),
		providers: jsonx.NewOrderedMap(),
		scope:     scope,
		userEnv:   jsonx.NewOrderedMap(),
		served:    served,
	}
}

// launchChannel is the channel this launch composes: the composed one, or under the seal the
// empty one, composed from nothing — no env_sources is even hydrated, since hydrating one runs
// the commands and reads the files it names on the host.
func (o *Options) launchChannel(cfg *jsonx.OrderedMap, packs []*packload.Pack) (*packChannel, error) {
	if o.Sealed {
		return sealedChannel(), nil
	}
	return o.composePackChannel(cfg, packs, nil)
}

// sealedStores makes and returns a sealed build's private ~/.cache and /mise sources, under the
// workspace's state dir. Made before the argv names them, because podman refuses a container
// whose bind source is missing.
func sealedStores(workspace string) (cacheDir, miseDir string, err error) {
	state := paths.WorkspaceStateDir(workspace)
	cacheDir = filepath.Join(state, sealedCacheLeaf)
	miseDir = filepath.Join(state, sealedMiseLeaf)
	for _, d := range []string{cacheDir, miseDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", "", err
		}
	}
	return cacheDir, miseDir, nil
}

// selectionNarrowed reports whether this launch carries a NARROWED SELECTION (Options.OnlyPacks): a
// fork's or a patched extension's build jail, whose packs are a subset of the user's chosen by the
// build act, not a selection anybody typed.
//
// THE SEAL MUST NOT TRIP OVER ITS OWN NARROWING (docs/design/patched-extensions.md PPX-D39). Some
// launch gates ask whether a name one pack writes is provided by ANOTHER selected pack, and the
// narrowing is what drops that other pack, so asked of a build jail they refuse a config the user's
// own launches accept. The first launch with patched extensions met one: pack matt's briefing named
// `agents: ["pi"]`, the seal narrowed the build to matt, and every build jail refused before its
// build line ran. Each gate below is skipped under a narrowed selection, because what it protects
// is an agent this jail runs, and a build jail runs a build line and no agent; the user's own
// launches still run every one of them over the whole selection:
//
//	an `agents` selector naming an agent no selected pack provides   AgentAudienceProblems
//	an addressed contribution reaching no destination (reported)     reportUnmatchedAudiences
//	a `supersedes` claim no selected pack's loophole serves          refuseUnmatchedSupersessions
//	a via profile whose service serves no route for it               checkViaRoutes
//	a required capability no selected pack satisfies                 refuseUnmetCapabilities
//
// The one gate of that shape a skip cannot answer is a fork whose base is not selected
// (packload.ApplyForks), which the jail's own loader repeats over the staged tree: there the build
// act names the base in the seal instead, and every base down that chain (packload.Fork.PackBases,
// cli's sealPacks). Every other pre-flight asks about two packs claiming one thing, which a subset
// can only make rarer, and stays.
func (o *Options) selectionNarrowed() bool { return o.OnlyPacks != nil }

// assembledConfigFor is the merged config this launch writes to its workspace's delivery copy
// (config.WriteAssembledConfig): cfg, or under the seal an empty object. The copy sits in the
// workspace the jail binds read-write, and the merged config carries every inline env_sources
// value and every user-scope key, so a sealed build is told it was launched from nothing — which
// is what crossed — rather than handed the user's config to read. Its in-jail readers (`yolo
// internal config-dump` and the in-jail config verbs) then report the empty config.
func (o *Options) assembledConfigFor(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	if o.Sealed {
		return jsonx.NewOrderedMap()
	}
	return cfg
}

// agentServerTables is the `lsp_servers` and `mcp_servers` tables and the `mcp_presets` list
// this launch hands its jail: cfg's, or under the seal none. An MCP server's `env` and an LSP
// server's `args` are literal strings the user writes, an API key among them, and a build runs
// no agent to start a server for. An empty preset list is also what keeps the jail's bootstrap
// from installing a preset's npm package (entrypoint.BootstrapScript).
func agentServerTables(cfg *jsonx.OrderedMap, sealed bool, packs []*packload.Pack) (lsp, mcp *jsonx.OrderedMap, presets []any) {
	if sealed {
		return nil, nil, nil
	}
	return cfgMap(cfg, "lsp_servers"), jailMCPServers(cfg, packs), cfgList(cfg, "mcp_presets")
}

// sealedBriefingInput is in with every description of a crossing the seal withholds taken out
// (FP-D23): the loopholes, the context mounts (each naming its host path), both port lists and the
// host nix daemon. It is marked Sealed, so the network line says yolo asked for no forwarding of
// the host's loopback (FP-D13) instead of that host services are forwarded in. The briefing is
// staged where the build reads it, so a description of a connection the build does not have is
// both a leak of the user's config and an untrue disclosure (DP-B2). The storage-classes section
// follows the seal through its own input (persistenceMapFor), and the caller drops
// `agents_md_extra`.
func sealedBriefingInput(in jailcontent.BriefingInput) jailcontent.BriefingInput {
	in.Sealed = true
	in.ContextMounts, in.PublishPorts, in.ForwardHostPorts, in.Loopholes = nil, nil, nil, nil
	in.HostNix = false
	return in
}

// sealKeepsClaim reports whether the read disclosure of a sealed launch keeps claim c: a claim
// about what the build itself fetches or runs, which is a `program` (a fork's source build, an
// installer the jail could run) or a patched extension's tree. Everything else the read
// disclosure carries is a pack env var, a host read or a loophole's crossing, each of which the
// seal withholds (FP-D9).
func sealKeepsClaim(c packload.Claim) bool {
	return c.Kind == packdecl.KindProgram || c.IsPatchedExtension()
}

// sealWithholdsClaim is sealKeepsClaim's complement, the filter for sealedWithheldLine.
func sealWithholdsClaim(c packload.Claim) bool { return !sealKeepsClaim(c) }

// sealedWithheldLine is the one line a sealed launch prints for the read-disclosure claims the
// seal withheld, withheld being the lines disclosedClaimsWhere rendered for sealWithholdsClaim:
// how many pack env vars, host reads (a reads-host, mount or host-briefing claim) and loophole
// crossings (a loophole's CA, intercept or bind that runs nothing on the host), and which packs
// declared them. "" when nothing was withheld.
func sealedWithheldLine(withheld []disclosureLine) string {
	if len(withheld) == 0 {
		return ""
	}
	var env, reads, loopholeCrossings int
	var packs []string
	for _, l := range withheld {
		switch l.kind {
		case packdecl.KindEnv:
			env++
		case packdecl.KindLoophole:
			loopholeCrossings++
		default:
			reads++
		}
		if !slices.Contains(packs, l.pack) {
			packs = append(packs, l.pack)
		}
	}
	var parts []string
	for _, n := range []struct {
		count        int
		one, several string
	}{
		{env, "pack env var", "pack env vars"},
		{reads, "host read", "host reads"},
		{loopholeCrossings, "loophole crossing", "loophole crossings"},
	} {
		switch {
		case n.count == 1:
			parts = append(parts, "1 "+n.one)
		case n.count > 1:
			parts = append(parts, strconv.Itoa(n.count)+" "+n.several)
		}
	}
	what := parts[0]
	if len(parts) > 1 {
		what = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	verb := "are"
	if len(withheld) == 1 {
		verb = "is"
	}
	return "Sealed build: " + what + " declared by " + strings.Join(packs, ", ") + " " + verb +
		" withheld (FP-D9: a build jail gets no credential and no host file)"
}

// narrowedPackEntries is entries narrowed to the names in Options.OnlyPacks, or entries unchanged
// when OnlyPacks is nil. The conventional local pack is dropped unless named, like any other.
func (o *Options) narrowedPackEntries(entries []config.PackEntry) []config.PackEntry {
	if o.OnlyPacks == nil {
		return entries
	}
	keep := map[string]bool{}
	for _, n := range o.OnlyPacks {
		keep[n] = true
	}
	var out []config.PackEntry
	for _, e := range entries {
		if keep[e.Name] {
			out = append(out, e)
		}
	}
	return out
}

// runSealedMacosUser is a SEALED launch on macos-user: a fork's build (forked-programs-as-packs.md
// FP-D24), which this backend runs in its capture act under the sealed Seatbelt profile
// (macosuser.RunForkBuildAct, through the build act's MacosUserRun). It hands the backend nothing of
// the host's, and does so by RETURNING ABOVE every crossing site of the macos-user arm rather than by
// a check at each one, since that arm has a dozen and a build needs none of them:
//
//	the crossing site, on the arm it leaves        under the seal
//	`mounts`, pack `mount`, cache_relocations      neither planned nor handed
//	host loopholes and services, the workspace's   none started or joined, and no session recorded:
//	keeper, the OpenAI service's fail-closed       the build's key is its own staging workspace's
//	check
//	the credential view, doorways, launch-owned    none
//	services, port relays
//	host_files, reads-host grants, the skills and  not composed: the backend gets an empty overlay
//	briefing overlay, the capture store's entries  and an empty host context
//	the jail-daemon payload                        none (Run withholds it above the dispatch)
//	the herdr pane                                 not registered
//	auto-capture, the reclaim offer, housekeeping, none runs
//	the launch's config artifacts, the durable dir
//
// It keeps the config-change approval (which the build act grants up front, as a container build's
// is), the pack tree the build's bootstrap renders from, the blocked tools, the arm's signal handling,
// the launch's record line and the sealed channel's launch env: the wire tables, empty.
func (o *Options) runSealedMacosUser(cfg *jsonx.OrderedMap, rt, repoRoot string, staged stagedPacks,
	args []string, channel *packChannel) int {
	o.releaseArrivalLock()
	wsCfg, _ := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {})
	if !o.DryRun && !o.checkConfigChanges(wsCfg, cfg, rt) {
		return 1
	}
	arm, disarm := o.armMacosUser()
	defer disarm()
	if status, ending := arm.Ending(); ending {
		return status
	}
	launched := ""
	if len(args) > 0 {
		launched = filepath.Base(args[0])
	}
	// The launch's fate is known, as a container build's is once its jail starts (launchrecord.go).
	if !o.DryRun {
		o.recordLaunchOutcome(launchStarted, -1)
	}
	return o.MacosUserRun(cfg, o.Workspace, config.SelectedAgents(cfg), args, repoRoot, staged.root,
		macosuser.HomeOverlay{}, macosuser.HostContext{}, o.DryRun, channel.launchEnv(launched),
		packload.BlockedTools(staged.packs), macosuser.JailDaemons{})
}
