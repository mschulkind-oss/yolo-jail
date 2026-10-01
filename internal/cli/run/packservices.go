package run

// packservices.go composes the launch's SERVICE contributions
// (packdecl.KindService, docs/reference/wire-bridge.md §2.1). In this build
// exactly one thing is composed: a service's jail_daemon joins the
// YOLO_JAIL_DAEMONS payload through internal/loopholes' one composer — the
// loophole JailDaemon shape verbatim ({name, cmd, restart}), because the env var
// is ONE frozen contract with ONE writer (the in-jail reader is the supervisor's
// ParseEnv, and the source-skew gate cannot see an env contract).
//
// The composition is LAUNCH-LEVEL, not argv-level: jailDaemonsFor below is
// called above the backend dispatch, so a backend with no container argv can
// still see what this launch declared (and say it will not run it).
//
// host_daemon is NOT composed here: a container launch runs a service's jail daemon, and the
// host half runs only at a notch with no jail supervisor, as a launch-owned child of the one
// launch whose agent's pairing needs it (internal/launchservice; macosuserservices.go for this
// package's macos-user arm, internal/cli's host launch for `yolo host --`;
// docs/design/host-notch-services.md). A service's `platforms`, `serves` and `settings` are
// still declared, carried and unread, with no consumer in the tree yet.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridged"
)

// serviceJailDaemons returns the YOLO_JAIL_DAEMONS entries for every selected
// pack's service contribution that declares a jail_daemon, sorted by service
// name. Sorted, not declaration-ordered, because the entries ride one JSON list
// beside the loopholes' own and the env var is read in-jail verbatim: a
// deterministic argv is the same rule the pack env block above it follows.
//
// The restart policy is set ALWAYS, defaulting to "on-failure" when the manifest
// said nothing — the same default the supervisor's ParseEnv applies when the key
// is absent, and the same one internal/loopholedecl applies to a LOOPHOLE's
// jail_daemon at load, so the two halves of the payload carry the same field set
// with no structural difference.
//
// It returns loopholes.JailDaemonSpec, not the wire shape. The JSON is built in
// exactly one place (loopholes.JailDaemonPayload); this file used to build its
// own objects beside that one, with a comment in each asking the other to stay
// identical.
//
// ONE DAEMON PER SERVICE NAME, the declaration packload.HeldServices says holds it: the LATER
// pack's in the pack order, the one rule for a duplicated sole-owned claim (notch-convergence
// NC-D59). serviceEndpointEnvArgs reads the same holder (ServiceNamed), so the daemon that runs
// is the one whose endpoint file the jail is pointed at. Every declarer used to reach the
// payload, two daemons racing for one name's endpoint file. When the holder declares no
// jail_daemon, the name runs none: the earlier pack's daemon is not a fallback. What was set
// aside is disclosed by noteShadowedServices.
func serviceJailDaemons(packs []*packload.Pack) []loopholes.JailDaemonSpec {
	var entries []loopholes.JailDaemonSpec
	held, _ := packload.HeldServices(packs)
	for _, h := range held {
		s := h.Service
		if s.JailDaemon == nil || len(s.JailDaemon.Cmd) == 0 {
			continue
		}
		restart := s.JailDaemon.Restart
		if restart == "" {
			restart = "on-failure"
		}
		// CallerToken ALWAYS: every address a service serves names the service's
		// caller token as its credential (packload's serviceCredentialEnv), so the
		// daemon behind it demands one (wire-bridge.md WB-D18).
		entries = append(entries, loopholes.JailDaemonSpec{
			Name: s.Name, Cmd: s.JailDaemon.Cmd, Restart: restart, CallerToken: true,
			Service: true,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

// jailDaemonsFor composes THIS LAUNCH'S jail-daemon payload: every active
// loophole's own jail_daemon plus every selected pack service's, through the one
// composer in internal/loopholes.
//
// CALLED ABOVE THE BACKEND DISPATCH (run.Run), because the payload is a fact
// about the launch and not about the argv. It used to be composed inside
// loopholesRuntimeArgs — that is, inside container-argv assembly — and emitted
// only as `-e YOLO_JAIL_DAEMONS=`, so on macos-user it was never composed at
// all: two daemons selected by a bare `"packs": ["claude"]`, neither started,
// nothing said (docs/reference/macos-user-nix-and-features.md). Hoisting it
// gives the native arm something to decline BY NAME and leaves the container
// arm reading the same value rather than a second composition of it.
//
// It touches no filesystem (the mounts do; this reads declarations), so it is
// safe this early — in particular it does not depend on
// prepareOpenAIAuthMountSentinel, which runs later and exists for the bind
// sources.
func (o *Options) jailDaemonsFor(cfg *jsonx.OrderedMap, rt string,
	packs []*packload.Pack) []loopholes.JailDaemonSpec {
	// The credential view drops the terminator from the payload, as it drops the rest of the
	// interception from the argv (loopholesRuntimeArgs): one decision, both halves.
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes")).WithCredentialView(o.claudeCredentialView(rt, cfg))
	// The service declarations the payload below sets aside, recorded for noteShadowedServices
	// the way withoutUnselectedProfileDaemons records what it leaves out: every call composes
	// the same packs, so the record is the same whichever call wrote it last.
	_, o.shadowedServices = packload.HeldServices(packs)
	specs := o.withoutUnselectedProfileDaemons(cfg, packs,
		set.JailDaemons(set.Enabled(), rt, serviceJailDaemons(packs)))
	// A DOORWAY'S HOST ARGV RUNS ONLY FROM A PACK YOLO SHIPS (macosuserdoorways.go): one this
	// launch will not admit is cleared here, so every reader below (the served set, the settle,
	// the split) sees a daemon that runs where it would have without one.
	specs = o.admitDoorways(packs, specs)
	// WHERE EACH DAEMON LISTENS (servedaddresses.go): its declared address, or on a jail that
	// shares this process's network namespace a port picked for this launch, settled once so
	// the payload and every client composition read one answer. Only the daemons this launch
	// SERVES (loopholes.ServedJailDaemons: the ones its jail runs, and on macos-user the doorways
	// it opens outside): a daemon macos-user declines binds nothing, and a pack service's host
	// half picks its own ports there (internal/launchservice).
	o.settleServedAddresses(cfg, rt, loopholes.ServedJailDaemons(rt, specs), packs)
	return o.withServedListen(specs)
}

// serviceEndpointEnvArgs emits the reachability witness's registration for a
// JAIL-FACING service this launch has decided it will serve — today exactly
// one: `-e YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT=/run/yolo-services/wire-bridge.endpoint`
// for the wire-bridge (wire-bridge.md §5's WARNING). It is the in-jail
// counterpart of hostServicesMountArgs' broker emission: that one advertises a
// HOST-side daemon the lifecycle spawned before the argv was frozen, this one a
// jail-side daemon whose file appears once supervise has booted it and the bind
// has succeeded — which is precisely the appearance the witness waits for and,
// on an escalating host-loopback disposition, refuses the launch without.
//
// BOTH gates must hold, and neither implies the other:
//
//   - a selected pack contributes the wire-bridge SERVICE (with an endpoint to
//     publish — a service that publishes none has no file to witness). This is
//     usually the needs closure's doing: cerebras's `needs` joins the pack
//     whenever claude is selected, so the ordinary bridged launch lists only
//     claude and cerebras in `packs`. A launch without the bridge emits
//     nothing, whatever the provider table says.
//   - wirebridged.WillServe says the daemon will actually SERVE. The daemon is
//     selection-lazy (§3.4): staged in every launch that selects claude, it
//     idles healthy when no claude profile routes at a bridged provider, and an
//     idle daemon publishes nothing — so emitting the variable there would make
//     every idle bridge a fatal "unpublished service", the exact contradiction
//     the design rules out.
//
// WillServe runs over THIS launch's composed channel — the same providers,
// use-profiles and resolved-profiles objects the env block below serializes
// onto the argv — and the daemon re-answers it in-jail from what that block
// crossed. One decision function, two call sites, same inputs: the env var,
// the endpoint file and the witness probe cannot disagree without the code
// having been forked first.
//
// The VALUE is the manifest's declared endpoint file name under the services
// dir — read off the contribution, never reconstructed from the daemon's
// constant. The manifest is the host-side declaration and the daemon is the
// publisher; if the two ever name different files, the variable points at a
// file nothing writes and the witness says so loudly, which is the failure
// mode a silent reconstruction would hide.
func serviceEndpointEnvArgs(in *assembleInput, o *Options) []string {
	// The name is sole-owned across packs; a second declarer is not refused at launch, so it
	// is held by the LATER one in the pack order, the one rule for a duplicated sole-owned
	// claim (packload.ServiceNamed, notch-convergence NC-D59). This took the first hit.
	bridge, ok := packload.ServiceNamed(in.packs, wirebridged.ServiceName)
	if !ok || bridge.Endpoint == "" {
		return nil
	}
	channel := in.envChannel(o)
	// The use-profiles table goes in as composed, never lowered here: WillServe lowers it with
	// the daemon's own lowering, which reads an active set's list as its first entry, so the
	// two ends cannot read one selection differently.
	if !wirebridged.WillServe(channel.providers, channel.profiles, channel.resolvedProfiles) {
		return nil
	}
	return []string{
		"-e", hostServiceEnvVar(bridge.Name) + "=" +
			paths.JailHostServicesDir + "/" + bridge.Endpoint,
		"-e", paths.JailDaemonReadyNamesEnv + "=" + bridge.Name,
	}
}

// withoutUnselectedProfileDaemons drops from specs every PROFILE-SERVED daemon (a jail daemon
// whose only clients are the agents a gated pointer reaches; packload's profileserved.go coins
// the term) that no agent's selection this launch delivers a gate to — aws-auth's credential
// adapter when no agent's selected provider is Bedrock (docs/reference/providers.md
// OQ-CN7 (b), ruled 2026-09-28; keyed on the provider's platform since OQ-BR8). Enabling the
// loophole does not start it: selecting a provider it serves does. The selection is the one the
// credential gate reads, answered per agent by the gate's own gateFiresFor over a selection built
// the way the gate builds its own (daemonSelection), so the daemon starts exactly when the gate
// delivers its pointer to some agent, and a daemon left out gets no caller token and serves no
// address (launchCallerTokens, servedDaemons read this payload).
//
// What was left out is recorded for noteUnstartedProfileDaemons, which says so: never silently.
func (o *Options) withoutUnselectedProfileDaemons(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	specs []loopholes.JailDaemonSpec) []loopholes.JailDaemonSpec {
	sel, resolved, providers := o.daemonSelection(cfg, packs)
	unselected := packload.UnselectedProfileServedDaemons(packs, sel)
	o.unstartedDaemons = nil
	o.unstartedDaemonProfiles = nil
	if len(unselected) == 0 {
		return specs
	}
	drop := map[string]packload.ProfileServedDaemon{}
	for _, d := range unselected {
		drop[d.Name] = d
	}
	out := specs[:0:0]
	for _, s := range specs {
		if d, ok := drop[s.Name]; ok {
			o.unstartedDaemons = append(o.unstartedDaemons, d)
			// The `-p` names that WOULD start it: its name gates' profiles, and every declared
			// profile over a provider of one of its platforms.
			names := append([]string(nil), d.Profiles...)
			for _, platform := range d.Platforms {
				names = append(names, packload.ProfilesOnPlatform(resolved, providers, platform)...)
			}
			if o.unstartedDaemonProfiles == nil {
				o.unstartedDaemonProfiles = map[string][]string{}
			}
			o.unstartedDaemonProfiles[d.Name] = names
			continue
		}
		out = append(out, s)
	}
	return out
}

// daemonSelection is the gate's view of the effective selection for the jail-daemon payload,
// which is composed BEFORE the channel, because the payload decides what the channel serves. A
// `platform` gate needs each agent's provider and that provider's platform, so this composes the
// provider table and resolves the profiles here, as composePackChannel does, but without the
// served addresses: a provider's platform does not depend on where any daemon listens. A table
// that does not compose, or profiles that do not resolve, fall back to the profile names alone;
// the channel composition then refuses that launch, saying why.
func (o *Options) daemonSelection(cfg *jsonx.OrderedMap, packs []*packload.Pack) (
	packload.GateSelection, map[string]packload.ResolvedProfile, *jsonx.OrderedMap) {
	effective := o.effectiveUseProfiles(cfg, packs)
	profiles := packload.ProfileTable(effective)
	providers, err := packload.ComposeProviders(cfgMap(cfg, "providers"), packs)
	if err != nil {
		return packload.ProfilesOnly(profiles), nil, nil
	}
	userProfiles, err := config.LoadProfiles(func(string) {})
	if err != nil {
		return packload.ProfilesOnly(profiles), nil, providers
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		return packload.ProfilesOnly(profiles), nil, providers
	}
	// Over each agent's whole active set (docs/design/active-provider-sets.md AP-P1), as the
	// credential gate reads it, so a daemon a later entry's platform serves starts too.
	return packload.SelectionOfSets(packload.ProfileSets(effective), resolved, providers), resolved, providers
}

// noteUnstartedProfileDaemons is the disclosure for withoutUnselectedProfileDaemons: one line per
// profile-served daemon this launch's payload left out, naming what would start it: for a
// platform gate, the platform and a declared profile over it; for a name gate, the profile. A
// disclosure, so no quiet switch (docs/reference/report-tiers.md, OQ-RO3). Silent when the
// payload left nothing out.
func (o *Options) noteUnstartedProfileDaemons() {
	for _, d := range o.unstartedDaemons {
		var why []string
		if len(d.Platforms) > 0 {
			quoted := make([]string, len(d.Platforms))
			for i, p := range d.Platforms {
				quoted[i] = strconv.Quote(p)
			}
			why = append(why, "no agent's selected provider is on platform "+
				strings.Join(quoted, " or "))
		}
		if len(d.Profiles) > 0 {
			quoted := make([]string, len(d.Profiles))
			for i, p := range d.Profiles {
				quoted[i] = strconv.Quote(p)
			}
			why = append(why, "no agent's selected profile is "+strings.Join(quoted, " or "))
		}
		remedy := "select a provider it serves"
		if names := o.unstartedDaemonProfiles[d.Name]; len(names) > 0 {
			remedy = "select one (`-p <agent>=" + names[0] + "`)"
		}
		o.pr(o.Stderr).print("[dim]Not started: the " + d.Name + " jail daemon, because " +
			strings.Join(why, ", and ") + ", which it serves; " + remedy + " to start it " +
			"(provider-credential-scope.md OQ-CN7).[/dim]")
	}
}

// noteShadowedServices is the disclosure for the one-daemon-per-name rule serviceJailDaemons
// follows: one line per service declaration this launch set aside because a later pack
// declares the same service name, naming both packs. A disclosure, so no quiet switch
// (docs/reference/report-tiers.md, OQ-RO3), and yellow because the pack the user may have
// selected for its service is not the one that runs. Silent when no name is declared twice.
func (o *Options) noteShadowedServices() {
	for _, s := range o.shadowedServices {
		o.pr(o.Stderr).print(fmt.Sprintf("[yellow]Service %q: pack %s's declaration is shadowed "+
			"by pack %s's, the later in the pack order, so this launch uses only pack %s's "+
			"(its jail daemon and its endpoint). Rename one service to keep both; "+
			"`yolo pack footprint` reports the pair (notch-convergence NC-D59).[/yellow]",
			s.Name, s.Pack, s.HeldBy, s.HeldBy))
	}
}
