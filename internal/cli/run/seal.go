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
//	host loopholes and services            startLoopholesDisclosed is never reached
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
