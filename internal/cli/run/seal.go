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
//	                                       namespace, which is itself a jail's
//	`mounts`, pack `mount`, reads-host     none, the surfaces' host layers included
//	the host briefing prepend              none
//	the host's global gitignore            not bound, nor named by the composed git config
//	                                       (gitIdentityMountArgs); the identity stays
//	the nix daemon socket                  not bound; the store stays mounted read-only when
//	                                       store-delivered packages need it
//	devices, GPU, KVM                      none
//	the host nvim config                   not bound
//	the inherited user config              not bound
//	env_sources' MISE_DISABLE_TOOLS        not hydrated
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

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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

// SealedBuildSharesLauncherNetwork reports whether a sealed build jail this process launches on rt
// ("" for the runtime the build's own launch resolves, podman on Linux) shares this process's network
// namespace instead of getting the runtime's bridge. The seal asks for the bridge (FP-D13), but a
// podman launched from inside a container is forced onto --net=host whatever it asks
// (assembleRunCmd), so a build launched from inside a jail runs on that jail's network. A build's
// start line reads this, so what it discloses is what the build jail's launch applies.
func SealedBuildSharesLauncherNetwork(rt string) bool {
	return sealedBuildSharesNetns(rt, paths.IsMacOS, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
}

// sealedBuildSharesNetns is SealedBuildSharesLauncherNetwork with the platform and the path probe the
// assembler's own inContainer reads, so a test asks both the same question.
func sealedBuildSharesNetns(rt string, isMacOS bool, exists func(string) bool) bool {
	if rt == "" {
		rt = "podman"
	}
	o := Options{IsMacOS: isMacOS, PathExists: exists}
	return sharesLauncherNetns(rt, "bridge", o.inContainer())
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
// act names the base in the seal instead (packload.Fork.PackBases, cli's sealPacks). Every other
// pre-flight asks about two packs claiming one thing, which a subset can only make rarer, and stays.
func (o *Options) selectionNarrowed() bool { return o.OnlyPacks != nil }

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
