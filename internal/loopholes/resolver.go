package loopholes

import "github.com/mschulkind-oss/yolo-jail/internal/config"

// Resolver implements config.LoopholeResolver, backing _validate_config's
// _known_loopholes() with real file-backed discovery (bundled + the recorded pack
// modules, include_disabled=True). config.ValidateConfig consults only Name +
// HasHostDaemon per loophole.
// This is the integration seam config declared as a stage-14 placeholder: the
// config package owns the interface, this package supplies the implementation.
type Resolver struct {
	// IncludeBundled toggles the bundled dir (default: true).
	IncludeBundled bool
}

// NewResolver returns a Resolver matching _known_loopholes()'s call:
// discover_loopholes(include_disabled=True) with bundled included.
func NewResolver() *Resolver {
	return &Resolver{IncludeBundled: true}
}

// discovered with include_disabled=True. Discovery never errors (per-manifest
// and per-dir failures are swallowed), so ok is always true — matching the
// "empty on a truly-empty machine" branch of the OSError-degrades contract.
//
// THE INVARIANT STILL HOLDS after the pack source landed, and it was checked rather than
// assumed: nothing added here returns an error. Fatality for a pack loophole's NAME lives
// in the launch pre-flight (run.PackLoopholeNameConflicts), which is the only place it
// can: Discover's signature has no error channel and seven call sites rely on that
// (docs/design/loophole-packaging.md §3.1, "fatality cannot be implemented inside
// Discover").
//
// It sees the pack modules this process recorded, which is what makes
// docs/design/loophole-packaging.md §5.2's prerequisite hold: a `loopholes.<name>.enabled`
// entry for a PACK-shipped loophole now resolves to a real LoopholeInfo, so it takes the
// OVERRIDE path instead of the unknown-name fallback that warned "no loophole named 'x'
// is installed on this machine" at every single launch — the same sentence a user gets
// when a pack genuinely failed to stage. RESOLVED BY JOINING THE CONVERGED SET (the first
// of §5.2's two options), because the alternative — recording a pack loophole's state
// somewhere else — would give the same name two homes.
func (r *Resolver) Known() (map[string]config.LoopholeInfo, bool) {
	loaded := Discover(DiscoverOptions{
		IncludeDisabled: true,
		IncludeBundled:  r.IncludeBundled,
		PackModules:     PackModules(),
	})
	out := make(map[string]config.LoopholeInfo, len(loaded))
	for _, lp := range loaded {
		out[lp.Name] = config.LoopholeInfo{
			Name:          lp.Name,
			HasHostDaemon: lp.HostDaemon != nil,
		}
	}
	return out, true
}

// static assertion that Resolver satisfies the config interface.
var _ config.LoopholeResolver = (*Resolver)(nil)
