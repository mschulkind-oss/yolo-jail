package loopholes

import "github.com/mschulkind-oss/yolo-jail/internal/config"

// Resolver implements config.LoopholeResolver, backing _validate_config's
// _known_loopholes() with real file-backed discovery (bundled + user dir,
// include_disabled=True). config.ValidateConfig consults only Name +
// HasHostDaemon per loophole.
//
// This is the integration seam config declared as a stage-14 placeholder: the
// config package owns the interface, this package supplies the implementation.
type Resolver struct {
	// Root overrides the user loopholes dir (mirrors discover_loopholes' root
	// argument). Empty => UserLoopholesDir().
	Root string
	// IncludeBundled toggles the bundled dir (Python default: true).
	IncludeBundled bool
}

// NewResolver returns a Resolver matching _known_loopholes()'s call:
// discover_loopholes(include_disabled=True) with bundled included and the
// default user dir.
func NewResolver() *Resolver {
	return &Resolver{IncludeBundled: true}
}

// Known mirrors _known_loopholes: {name: info} for every file-backed loophole
// discovered with include_disabled=True. Discovery never errors (per-manifest
// and per-dir failures are swallowed), so ok is always true — matching the
// "empty on a truly-empty machine" branch of the OSError-degrades contract.
func (r *Resolver) Known() (map[string]config.LoopholeInfo, bool) {
	loaded := Discover(DiscoverOptions{
		Root:            r.Root,
		RootSet:         r.Root != "",
		IncludeDisabled: true,
		IncludeBundled:  r.IncludeBundled,
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
