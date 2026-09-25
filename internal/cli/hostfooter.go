package cli

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/footer"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostFooterTables is the host notch's half of the agent footer's billing route
// (docs/design/agent-footer.md OQ-FT6): the three wire tables no host launch exports,
// composed from the user config so a host Claude's footer names the profile the config
// selects. footer.WithHostTables asks for it only outside a jail and only when the agent's
// own env carries no selection ("env first").
//
// THE RESOLUTION IS `yolo host env`'s WITH NO `-p`, piece by piece: the user-scope config's
// `use_profiles` (effectiveHostProfiles, with no `-p`, since a status-line command has
// none), the user's `profiles` (config.LoadProfiles), the provider table
// (composedHostProviders) and the profile resolution over both (packload.ResolveProfiles).
// So it names what a host launch with no `-p` composes. A one-launch `yolo host -p X --
// <agent>` is invisible to it: that launch exports no YOLO_* table (composeHostVars), so the
// footer still names the config's selection, or the login when the config selects nothing
// (docs/design/agent-footer.md §4).
//
// THE ONE PLACE IT PARTS FROM THAT PATH is loading the packs, and it is a deliberate
// narrowing: footerHostPacks reads each selected pack's declarations where the store already
// holds them (packsrc.Store.ResolveExisting), where loadedHostPacks stages every fetched pack
// into a temp dir first and a launch checks a missing tree out. That staging is the
// no-escape check for CONTENT a host apply delivers into the real home; the footer delivers
// nothing and reads only profile and provider declarations, and it runs on every status-line
// refresh, so a copy of each fetched pack per refresh would be pure cost, and a checkout per
// refresh a write into the pack store that two refreshing sessions would race on.
//
// It never writes to stderr: every loader is handed a nil (silent) warn, and a table that
// cannot be composed is left empty, which the renderer reads as absent.
func hostFooterTables() footer.Tables {
	cfg := config.UserScopeConfigOrEmpty()
	var t footer.Tables
	if use := effectiveHostProfiles(cfg, "", ""); use.Len() > 0 {
		t.UseProfiles = footerJSON(use)
	}
	if t.UseProfiles == "" {
		return t // no selection: the footer names the login, and nothing else is needed
	}
	packs := footerHostPacks()
	providers, err := composedHostProviders(cfg, packs)
	if err != nil {
		return t
	}
	t.Providers = footerJSON(providers)
	userProfiles, err := config.LoadProfiles(nil)
	if err != nil {
		return t
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		return t
	}
	t.Profiles = footerJSON(packload.ProfilesWireTable(resolved))
	return t
}

// footerHostPacks is the selected pack set, resolved offline as a host launch resolves it
// once that launch's pack refresh step has run (an embedded pack by name, a git pack from the
// pack store, a local one from its path), minus the fetched-tree staging hostFooterTables
// explains, and WRITING NOTHING to the pack store. It NEVER FETCHES: a refresh per
// status-line redraw would put the network on the footer's path, so a git pack no launch has
// fetched yet is simply unresolved here. A fetched pack whose tree for the pinned commit is
// not already checked out is skipped rather than checked out (packsrc.Store.ResolveExisting).
// A pack that does not resolve contributes nothing, as it contributes nothing to a host
// launch's env, so a profile only it declares reads as its bare name: an under-claim, never a
// wrong provider. The one write a refresh can still cause is packload.Embedded's tree, made
// once per build by the first host `yolo` of that build, whatever the command.
func footerHostPacks() []*packload.Pack {
	entries, err := config.LoadPacks(nil)
	if err != nil {
		return nil
	}
	var packs []*packload.Pack
	for _, e := range entries {
		if e.Embedded() {
			for _, p := range packload.Embedded() {
				if p.Name == e.Name {
					packs = append(packs, p)
				}
			}
			continue
		}
		addr, err := packsrc.Parse(e.Source)
		if err != nil {
			continue
		}
		res, err := (&packsrc.Store{Dir: paths.PacksDir()}).ResolveExisting(addr, e.Slug())
		if err != nil {
			continue
		}
		if p, _ := packload.LoadDir(res.Root, e.Name); p != nil {
			packs = append(packs, p)
		}
	}
	return packs
}

// footerJSON is a table's wire text, or "" when it will not encode.
func footerJSON(m *jsonx.OrderedMap) string {
	if m == nil {
		return ""
	}
	text, err := jsonx.DumpsCompact(m)
	if err != nil {
		return ""
	}
	return text
}
