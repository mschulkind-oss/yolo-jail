package run

// packservices.go composes the launch's SERVICE contributions
// (packdecl.KindService, docs/design/wire-bridge.md §2.1) into the container
// argv. In this build exactly one thing is composed: a service's jail_daemon
// joins the YOLO_JAIL_DAEMONS payload through internal/loopholes'
// RuntimeArgsForWithJailDaemons — the loophole JailDaemon wire shape verbatim
// ({name, cmd, restart}), because the env var is ONE frozen contract with ONE
// writer (the in-jail reader is the supervisor's ParseEnv, and the source-skew
// gate cannot see an env contract).
//
// host_daemon is DELIBERATELY NOT composed anywhere in this build: no
// host-daemon path exists for services yet (wire-bridge.md §2.1 rules one kind
// carries both halves; executing the host half is the follow-up that re-forms
// the loophole packs around services). The manifest validates the declaration
// so it is stateable today; this file is where its composition will land, and
// this comment is the standing record that ignoring it here is a decision, not
// an oversight. The same is true of a service's `platforms`, `serves` and
// `settings` — declared, carried, unread — with no consumer in the tree yet.

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridged"
)

// serviceJailDaemons returns the YOLO_JAIL_DAEMONS payload entries for every
// selected pack's service contribution that declares a jail_daemon, sorted by
// service name. Sorted, not declaration-ordered, because the entries ride one
// JSON list beside the loopholes' own and the env var is read in-jail verbatim:
// a deterministic argv is the same rule the pack env block above it follows.
//
// The restart policy is emitted ALWAYS, defaulting to "on-failure" when the
// manifest said nothing — the same default the supervisor's ParseEnv applies
// when the key is absent, spelled out so the payload's shape is exactly the
// loophole half's (internal/loopholes' runtimeArgsFor sets the key the same
// way) and a diff of the two halves' entries shows no structural difference.
func serviceJailDaemons(packs []*packload.Pack) []any {
	type named struct {
		name string
		spec *jsonx.OrderedMap
	}
	var entries []named
	for _, p := range packs {
		if p.Decl == nil {
			continue
		}
		for _, s := range p.Decl.Services() {
			if s.JailDaemon == nil || len(s.JailDaemon.Cmd) == 0 {
				continue
			}
			spec := jsonx.NewOrderedMap()
			spec.Set("name", s.Name)
			cmd := make([]any, len(s.JailDaemon.Cmd))
			for i, c := range s.JailDaemon.Cmd {
				cmd[i] = c
			}
			spec.Set("cmd", cmd)
			restart := s.JailDaemon.Restart
			if restart == "" {
				restart = "on-failure"
			}
			spec.Set("restart", restart)
			entries = append(entries, named{name: s.Name, spec: spec})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	if len(entries) == 0 {
		return nil
	}
	out := make([]any, len(entries))
	for i, e := range entries {
		out[i] = e.spec
	}
	return out
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
	var bridge *packdecl.ServiceContribution
	for _, p := range in.packs {
		if p.Decl == nil {
			continue
		}
		for _, s := range p.Decl.Services() {
			if s.Name != wirebridged.ServiceName {
				continue
			}
			// The name is sole-owned across packs (a second contributor is a
			// launch refusal, not a fold), so the first hit is THE service.
			found := s
			bridge = &found
			break
		}
		if bridge != nil {
			break
		}
	}
	if bridge == nil || bridge.Endpoint == "" {
		return nil
	}
	channel := in.envChannel(o)
	if !wirebridged.WillServe(channel.providers, useProfilesTable(channel.profiles),
		channel.resolvedProfiles) {
		return nil
	}
	return []string{"-e", hostServiceEnvVar(bridge.Name) + "=" +
		paths.JailHostServicesDir + "/" + bridge.Endpoint}
}

// useProfilesTable lowers the composed use-profiles table to the plain
// agent→profile map WillServe reads. A non-string value lowers to "": the same
// "no profile active here" answer the in-jail loader gives a malformed entry,
// so the two ends lower a corrupt table the same way.
func useProfilesTable(m *jsonx.OrderedMap) map[string]string {
	out := map[string]string{}
	if m == nil {
		return out
	}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		s, _ := v.(string)
		out[k] = s
	}
	return out
}
