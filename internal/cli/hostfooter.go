package cli

import (
	"errors"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/footer"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostFooterTables is the host notch's half of the agent footer's billing route
// (docs/design/agent-footer.md OQ-FT6): the three wire tables, composed from the user config
// for an agent whose env carries none, so a host Claude that `yolo host --` did not start still
// names the profile the config selects. footer.WithHostTables asks for it only outside a jail
// and only when the agent's own env carries no selection ("env first").
//
// THE RESOLUTION IS `yolo host env`'s WITH NO `-p`, piece by piece: the user-scope config's
// `profile` (effectiveHostProfiles, with no `-p`, since a status-line command has
// none), the user's `profiles` (config.LoadProfiles), the provider table
// (composedHostProviders) and the profile resolution over both (packload.ResolveProfiles).
// So it names what a host launch with no `-p` composes. A one-launch `yolo host -p X --
// <agent>` never reaches it: that launch exports the three tables it composed
// (composeHostVarsWith's step 3b, FT-D2), so the renderer reads them from the env first and
// this is not asked.
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
// THE FILTERS ARE THE EXCEPTION TO THAT NARROWING: an entry with `only`/`exclude` is staged
// into a temp copy (removed before the read returns), because its filters decide which manifest
// the launch reads at all. An unfiltered pack is read in place, so only a filtered entry pays a
// copy per refresh.
//
// It never writes to stderr: every loader is handed a nil (silent) warn, and a table that
// cannot be composed is left empty, which the renderer reads as absent.
//
// A SELECTION THE HOST LAUNCH REFUSES IS LEFT OUT (docs/design/agent-footer.md FT-D1). The
// footer names what the host composed, and for an agent whose selected profile the host's
// protocol gate refuses it composes nothing: `yolo host -- claude` refuses claude's codex
// profile (ES-D18, ES-D25), so a claude running on this host with that selection and no tables
// in its env was started outside `yolo host`, which leaves it on its login. (One started by
// `yolo host -p <other>` carries that launch's tables, so this table is not asked for it.) The
// footer used to say `codex (bridge) · host` there, a bridge the host never runs. Only the pairing gate is asked (packload.PairingRefusal,
// with the host's unservable adaptations), the one refusal that turns on the selection itself;
// a selection whose table could not be composed at all is left as it was.
func hostFooterTables() footer.Tables {
	cfg := config.UserScopeConfigOrEmpty()
	var t footer.Tables
	if config.ConfigProfileSelection(cfg).IsZero() {
		return t // no selection: the footer names the login, and nothing else is needed
	}
	// The packs before the table, because the key's "*" (or its string form) reaches the CLIs
	// the selected packs install, as it does at every notch.
	packs := footerHostPacks()
	use := effectiveHostProfiles(cfg, packs, "", "")
	if use.Len() == 0 {
		return t
	}
	t.UseProfiles = footerJSON(use)
	providers, unservable, err := composedHostProviders(cfg, packs, nil)
	if err != nil {
		return t
	}
	// A SELECTION A HOST LAUNCH SERVES WITH A LAUNCH-OWNED SERVICE IS KEPT (HS-D10): since the
	// host starts a pack service's host half for the command it runs (launchservice), claude's
	// codex profile is one `yolo host -- claude` composes, so the footer names it as it does in
	// a jail. The table is recomposed with those services at their DECLARED addresses: the
	// footer has no launch, so no picked port, and it reads only which route the agent takes.
	if services := footerLaunchServices(cfg, packs, providers, use, unservable); len(services) > 0 {
		if providers, unservable, err = composedHostProviders(cfg, packs, services); err != nil {
			return t
		}
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
	composed := jsonx.NewOrderedMap()
	// A value is an agent's ACTIVE SET (docs/design/active-provider-sets.md), a name or a list;
	// the footer names its first entry (packload.ProfileTable), so that is the pairing asked.
	sets := packload.ProfileSets(use)
	for _, agent := range use.Keys() {
		v, _ := use.Get(agent)
		profile := ""
		if set := sets[agent]; len(set) > 0 {
			profile = set[0]
		}
		if packload.PairingRefusal(packs, providers, resolved, agent, profile, unservable) != nil {
			continue
		}
		composed.Set(agent, v)
	}
	t.UseProfiles = ""
	if composed.Len() > 0 {
		t.UseProfiles = footerJSON(composed)
	}
	// Inert via, as composeHostVars makes it (WG-I12): the host notch serves no via route, so
	// its table carries no via address, whatever the pack set holds.
	inert, _ := packload.ViaServedAt(resolved, packs, packload.NothingServed())
	t.Profiles = footerJSON(packload.ProfilesWireTable(inert))
	return t
}

// footerLaunchServices is the services a host launch would start for use's selections, each as a
// plan with no port or token: the admitted host halves (launchservice.Admit) of the adaptations
// the pairing gate refuses only because nothing serves them yet.
func footerLaunchServices(cfg *jsonx.OrderedMap, packs []*packload.Pack, providers *jsonx.OrderedMap,
	use *jsonx.OrderedMap, unservable []packload.Adaptation) []*launchservice.Plan {
	userProfiles, err := config.LoadProfiles(nil)
	if err != nil {
		return nil
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		return nil
	}
	var out []*launchservice.Plan
	seen := map[string]bool{}
	for _, agent := range use.Keys() {
		v, _ := use.Get(agent)
		profile, _ := v.(string)
		var unserved *packload.UnservedAdapterError
		if !errors.As(packload.PairingRefusal(packs, providers, resolved, agent, profile, unservable), &unserved) ||
			!unserved.Selected || unserved.ProviderPack != "" || seen[unserved.Adaptation.Service] {
			continue
		}
		d, err := launchservice.Admit(packs, unserved.Adaptation.Service)
		if err != nil {
			continue
		}
		seen[d.Service] = true
		out = append(out, &launchservice.Plan{Declared: d})
	}
	return out
}

// footerHostPacks is the selected pack set, resolved offline as a host launch resolves it
// once that launch's pack refresh step has run (an embedded pack by name, a git pack from the
// pack store, a local one from its path), minus the fetched-tree staging hostFooterTables
// explains, and WRITING NOTHING to the pack store. It NEVER FETCHES: a refresh per
// status-line redraw would put the network on the footer's path, so a git pack no launch has
// fetched yet is simply unresolved here. A fetched pack whose tree for the pinned commit is
// not already checked out is skipped rather than checked out (packsrc.Store.ResolveExisting).
// A pack that does not resolve, or whose manifest has problems, contributes nothing (the host
// launch refuses over it), so a profile only it declares reads as its bare name: an
// under-claim, never a wrong provider. The one lasting write a refresh can still cause
// is packload.Embedded's tree, made once per build by the first host `yolo` of that build,
// whatever the command; a filtered entry's temp copy is removed before this returns.
func footerHostPacks() []*packload.Pack {
	// The one selection function (selectHostPacks, notch-convergence item 6), closure included,
	// so the footer describes the pack set a host launch composes. Its resolver is the one
	// resolver (config.ResolvePack) in DECLARATION mode and writing nothing to the store: the
	// declaration the host launch composes is the one the entry's filters leave, read in place
	// when nothing filters it (a copy only for a filtered entry), and none from a pack with
	// manifest problems or one packstage refuses, which the host launch refuses over (NC-D5). A
	// profile only such a pack declares then reads as its bare name here: the footer is a status
	// line with nowhere to put the refusal.
	return selectHostPacks(func(e config.PackEntry) (*packload.Pack, error) {
		res, err := config.ResolvePack(e, hostPackResolveSpec(true))
		if err != nil {
			return nil, err
		}
		return resolvedOrProblems(e, res)
	}, config.UserScopeSelection()).packs
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
