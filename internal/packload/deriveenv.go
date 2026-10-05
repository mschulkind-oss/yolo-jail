package packload

// The env-derive runner: docs/reference/providers.md

// deriveenv.go is the ENV-EMITTING DERIVE (docs/reference/providers.md and
// §9 OQ-CS8; docs/reference/providers.md OQ-PT9): the ONE runner both notches compose an
// agent's provider environment through. The binding lives in the agent's OWN pack, as a
// yolo.env(agent, fn) registration in its derive.lua — the producer reads the composed
// providers table and returns the variables the agent's process needs — so no manifest
// vocabulary declares the delivery, and a second implementation of the composition has
// nowhere to live: the jail notch's env block and the host notch's composition both
// reduce through AgentEnv, the way both already reduce the pack env fold through
// packload.EnvVarsFor.
//
// Host-side only, on purpose. An IN-JAIL env derive has no consumer: this runner's
// output crosses per-entry through the yolo-user-env.sh channel section on the
// container backends and the macos-user notch fixes its plan env before bootstrap —
// so the entrypoint never runs this and the yolo.env registration a pack's
// derive.lua carries is inert there.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// DeriveScript reads a pack's derive.lua (at its tree root), or "" when absent. The
// script registers per-surface producers via yolo.derive(agent, surface, fn) and
// environment producers via yolo.env(agent, fn); a surface with no registered derive
// gets no dynamic layer, and an agent whose pack registered no yolo.env composes no
// provider environment.
func DeriveScript(p *Pack) string {
	if p.Root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(p.Root, "derive.lua"))
	if err != nil {
		return ""
	}
	return string(data)
}

// DerivedSurfaces reports which of a pack's surfaces its derive.lua registers a producer
// for — the pack's own answer to "which of my surfaces get a computed layer", read by
// running the script's registrations (luahook.DeriveRegistrations).
//
// A pack with no derive.lua registers nothing and gets (nil, nil): every one of its
// surfaces composes from its static layers alone.
//
// This is the DECLARATION the host-side `computed` column is derived from
// (docs/design/host-render-target.md §3.4). There is no field in pack.json to read
// instead, and that is the finding rather than a gap: a computed layer is produced by a
// `yolo.derive(agent, surface, fn)` call, so the registration IS the declaration, and any
// manifest flag beside it would be a second statement of one fact — exactly the drift the
// hand-maintained CLI map demonstrated: it was missing claude/config, pi/settings and
// pi/models, three surfaces the shipped packs register a producer for.
//
// The identities are NOT checked against the pack's own surfaces. A registration naming a
// surface the pack does not declare is inert at the boot render (nothing invokes it) and
// is reported as such by the fold's own note mechanism; filtering here would make this
// disagree with what the render does.
func DerivedSurfaces(p *Pack) ([]manifest.SurfaceKey, error) {
	script := DeriveScript(p)
	if script == "" {
		return nil, nil
	}
	regs, err := (luahook.GopherLuaVM{}).DeriveRegistrations(script)
	if err != nil {
		return nil, fmt.Errorf("pack %s: reading derive.lua registrations: %w", p.Name, err)
	}
	out := make([]manifest.SurfaceKey, 0, len(regs))
	for _, r := range regs {
		out = append(out, manifest.SurfaceKey{Agent: r.Agent, Name: r.Surface})
	}
	return out, nil
}

// AgentEnv composes one agent's provider environment: it runs the yolo.env producer the
// agent's own pack registered, over the composed providers table, and returns what the
// producer emitted as agentenv.Vars, sorted by key — a map has no order, and an
// environment must not reshuffle between launches.
//
// providers is the launch's composed table (ComposeProviders) — the SAME table the
// config derives read as YOLO_PROVIDERS — but the copy handed to the producer is
// HYDRATED: each entry that names an api_key_env_name the lookup can find carries
// api_key = that value. The credential crosses into the derive invocation ONLY; the
// table itself stays secret-free (docs/reference/providers.md, D8) and is never
// mutated. A lookup that finds nothing composes no api_key at all — an empty credential
// is the pre-flight's refusal to make, not a token to hand an agent. useProfiles is the
// effective profile table in ProfileTable's shape, exposed as ctx.use_profiles; the
// resolved profile table (WithResolvedProfiles) is BOTH remaining halves of the ctx: the
// option map of THIS agent's active profile (ctx.profile, §5.2) and the provider that
// profile selects (ctx.selected_provider, through ProviderFor — the one resolution rule
// the surface path answers through too).
//
// Beside the producer's output it composes the ROLE ENVIRONMENT (ModelRoleVars, OQ-XM4):
// YOLO_MODEL_<ROLE> for each conventional tier alias the agent's selected provider names, which
// core composes for every agent whether or not its pack registers a producer, and which a
// variable of the same name the producer sets overrides.
//
// The producer is discovered by bin ownership: the one selected pack that installs the
// agent's CLI. Nothing composes when the inputs are inert — no profile at this agent's
// CLI name, or no pack installing the bin, returns (nil, nil), the identity, and so does a
// pack whose derive.lua registers no yolo.env for the agent on a launch whose providers name
// no tier alias. A selected provider the table does not
// hold is NOT inert: the protocol gate refuses it, naming why (MissingProviderError), since
// a profile composed into nothing is P1's silent no-op. A Lua error, or a producer that
// sets a variable to something other than a string or ctx.tombstone, is a real error:
// this composition IS the delivery, so a broken producer refuses the launch rather than
// composing half an environment.
func AgentEnv(packs []*Pack, providers *jsonx.OrderedMap, useProfiles map[string]string,
	agent, profile string, lookup func(string) (string, bool), opts ...AgentEnvOption) ([]agentenv.Var, error) {
	cfg := agentEnvOpts{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if agent == "" || profile == "" {
		return nil, nil
	}
	owner := binOwner(packs, agent)
	if owner == nil {
		return nil, nil
	}
	selected := ProviderFor(cfg.resolved, profile)
	// THE PROTOCOL GATE (protocol-resolution.md), above the derive and not inside it.
	// A derive composes VARIABLES; whether this agent can be pointed at this provider at
	// all is a question about two DECLARATIONS, and core answers it — which is the line
	// OQ-CS8 draws and the reason nothing below learns a protocol name.
	//
	// Here rather than in the run pre-flight for the reason protocolresolution.go states:
	// the gate needs a SELECTION, and this is the runner both notches reduce through with
	// the resolved selection in hand. Above DeriveScript on purpose — a pairing nothing can
	// serve is broken whether or not the pack ships a producer, and a silent pass for a
	// pack with no derive.lua would make the gate depend on a file's existence.
	if err := refuseUnspeakableProvider(packs, owner, agent, profile, selected, providers, cfg.unserved); err != nil {
		return nil, err
	}
	// EVERY LATER ENTRY OF THE ACTIVE SET PAIRS TOO (docs/design/active-provider-sets.md AP-P1):
	// each is a provider the agent's config points it at, so each is asked the gate's question.
	// A pairing only an unserved adaptation would resolve is refused as a plain error naming the
	// entry, never as *UnservedAdapterError: a notch plans at most one launch-owned service, for
	// the primary's pairing (AP-D7, host-notch-services.md §4.2), so a later entry that needs
	// one is not a pairing any service planned here would carry.
	if err := refuseUnspeakableSetEntries(packs, owner, agent, profile, cfg, providers); err != nil {
		return nil, err
	}
	table := hydrateProviders(providers, lookup)
	// THE ROLE ENVIRONMENT (modelroles.go, OQ-XM4) comes first and the derive's output second,
	// so a variable the agent's own pack sets under the same name, a tombstone included, wins:
	// the pack is the more specific statement about its own agent's process. Composed before
	// the derive-script check, because the variables are core's and an agent whose pack ships
	// no yolo.env producer is still an agent whose children want its provider's tiers.
	composed := map[string]any{}
	for _, v := range ModelRoleVars(table, selected) {
		if v.Unset {
			composed[v.Key] = nil
		} else {
			composed[v.Key] = v.Value
		}
	}
	if script := DeriveScript(owner); script != "" {
		out, err := deriveAgentEnv(script, owner, packs, providers, table, useProfiles, agent,
			profile, selected, cfg)
		if err != nil {
			return nil, err
		}
		for k, v := range out {
			composed[k] = v
		}
	}
	// THE AGENT FILES LEAVE THE ENVIRONMENT HERE (agentfiles.go, MM-D33): collected where this
	// notch writes them, withheld everywhere else, and never a shape var.
	if err := takeAgentFiles(composed, owner, agent, cfg.files); err != nil {
		return nil, err
	}
	return envVarsOf(composed, owner, agent)
}

// deriveAgentEnv runs the yolo.env producer of owner's derive.lua for agent, over table (the
// hydrated providers view, hydrateProviders) and the selection AgentEnv resolved.
func deriveAgentEnv(script string, owner *Pack, packs []*Pack, providers *jsonx.OrderedMap,
	table map[string]any, useProfiles map[string]string, agent, profile, selected string,
	cfg agentEnvOpts) (map[string]any, error) {
	out, err := (luahook.GopherLuaVM{}).Derive(script, &luahook.DeriveCtx{
		Agent:            agent,
		Env:              true,
		ProfileName:      profile,
		SelectedProvider: selected,
		Profile:          cfg.profileOptions(profile),
		ActiveSet:        ActiveSetFor(cfg.setOr(profile), cfg.resolved),
		ViaURL:           ViaURLFor(cfg.resolved[profile], agent),
		ViaAPIKeyEnvName: ViaAPIKeyEnvNameFor(packs, cfg.resolved[profile], agent),
		// The profile's model-list enforcement switch (MM-D5), the same answer the surface
		// path hands its derives (entrypoint.surfaceSelectionFor).
		ModelsNotEnforced: !ModelsEnforced(cfg.resolved[profile]),
		// Whether this notch writes the agent files the pack declares (WithAgentFiles, MM-D33).
		AgentFiles: cfg.files != nil,
		// The built-in source's capabilities, resolved the same way the surface path
		// resolves them (surfaceSelectionFor) — `owner` is by construction the pack bin
		// ownership would find. It changes nothing HERE, because this ctx carries no
		// mcp_servers table for capability-driven delivery to filter; it is set so the two
		// derive paths cannot grow different answers to "what is the active source", which
		// is the rule SelectedProvider and Profile above already follow.
		NativeCapabilities: owner.Decl.NativeCapabilities(agent),
		Tables: map[string]map[string]any{
			manifest.SourceProviders:   table,
			manifest.SourceUseProfiles: plainProfiles(useProfiles),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pack %s: %s's env derive: %w", owner.Name, agent, err)
	}
	return out, nil
}

// envVarsOf lowers a composed name → value map (a string, or nil for a removal) into
// agentenv.Vars, sorted by key. owner and agent name the producer in the one error.
func envVarsOf(out map[string]any, owner *Pack, agent string) ([]agentenv.Var, error) {
	if len(out) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(out))
	for name := range out {
		names = append(names, name)
	}
	sort.Strings(names)
	vars := make([]agentenv.Var, 0, len(names))
	for _, name := range names {
		// The empty spellings compose nothing, the rule the old placeholder vocabulary
		// enforced: an empty value is an absent input (an empty endpoint is a request
		// to the wrong host; an empty token is a credential that gets SENT), and an
		// empty key names no variable at all.
		if name == "" {
			continue
		}
		switch v := out[name].(type) {
		case nil: // the tombstone: an explicit removal, not a set
			vars = append(vars, agentenv.Var{Key: name, Unset: true})
		case string:
			if v == "" {
				continue
			}
			vars = append(vars, agentenv.Var{Key: name, Value: v})
		default:
			return nil, fmt.Errorf("pack %s: %s's env derive set %s to a %T, want a string "+
				"(or ctx.tombstone to remove the variable)", owner.Name, agent, name, out[name])
		}
	}
	return vars, nil
}

// agentEnvOpts carries the optional inputs AgentEnv grew after its first callers were
// written. It is an OPTION and not a parameter for one reason: the seven call sites the
// signature already has pass nothing, and the resolved-profile table is only available
// to a caller that composed it — demanding it positionally would have made every one of
// them hand over a nil it does not have.
type agentEnvOpts struct {
	resolved map[string]ResolvedProfile
	// unserved is WithUnservedAdaptations' list.
	unserved []Adaptation
	// set is WithActiveSet's list.
	set []string
	// files is WithAgentFiles' destination; nil at a notch that writes no agent files.
	files *[]AgentFile
}

// WithActiveSet hands the runner the agent's whole active set (docs/design/active-provider-sets.md
// §4.3), the primary first: the derive reads it as ctx.active_set, and every later entry is
// asked the protocol gate's question. A set whose first entry is not the profile AgentEnv was
// called for, or none, reads as the one profile, which is every caller composing no set.
func WithActiveSet(set []string) AgentEnvOption {
	return func(o *agentEnvOpts) { o.set = set }
}

// setOr is the active set for a run on profile: WithActiveSet's list when its primary is
// profile, else profile alone.
func (o agentEnvOpts) setOr(profile string) []string {
	if len(o.set) > 0 && o.set[0] == profile {
		return o.set
	}
	if profile == "" {
		return nil
	}
	return []string{profile}
}

// refuseUnspeakableSetEntries asks the protocol gate about every entry of the active set after
// the primary, which AgentEnv's own call already asked. A refusal names the entry's position.
func refuseUnspeakableSetEntries(packs []*Pack, owner *Pack, agent, profile string,
	cfg agentEnvOpts, providers *jsonx.OrderedMap) error {
	set := cfg.setOr(profile)
	for i, name := range set {
		if i == 0 {
			continue
		}
		err := refuseUnspeakableProvider(packs, owner, agent, name, ProviderFor(cfg.resolved, name),
			providers, cfg.unserved)
		if err == nil {
			continue
		}
		return fmt.Errorf("profile %q, entry %d of %s's profiles (%s): %s", name, i+1, agent,
			strings.Join(set, ", "), strings.TrimSuffix(err.Error(), "\n"))
	}
	return nil
}

// WithUnservedAdaptations hands the protocol gate the conversions this notch can never serve
// (UnservedAdaptationsAt: what WithServed left out, and at a notch running no jail daemon the unselected shipped
// packs' of the same kind). A pairing only one of them would resolve then refuses as
// *UnservedAdapterError, saying why. Without the list it would refuse as a pairing nothing
// declares an adapter for, or as outcome 3 naming a pack whose selection resolves nothing here.
// It changes no pairing's outcome: the table decides that, and the table already lacks these
// addresses.
func WithUnservedAdaptations(unserved []Adaptation) AgentEnvOption {
	return func(o *agentEnvOpts) { o.unserved = unserved }
}

// AgentEnvOption mutates the optional inputs.
type AgentEnvOption func(*agentEnvOpts)

// WithResolvedProfiles hands the runner the launch's resolved profile table
// (ResolveProfiles). It is the runner's WHOLE input on the selection: ctx.profile reads
// its option map for the profile active at THIS agent, and ctx.selected_provider reads
// the provider that entry names (ProviderFor) — the table user declarations are part of,
// which is why nothing here re-derives a selection off the pack manifests. Absent or not
// supplied, no selection resolves: the derive sees an empty ctx.profile and an empty
// provider name, and composes nothing — the same world as no profile being active, which
// is what keeps a caller that does not know the table from inventing a second, worse
// answer for it.
func WithResolvedProfiles(resolved map[string]ResolvedProfile) AgentEnvOption {
	return func(o *agentEnvOpts) { o.resolved = resolved }
}

// profileOptions returns the option map of the named profile, never nil — a Lua table
// field cannot be nil, and an empty map is the honest "nothing resolved".
func (o agentEnvOpts) profileOptions(name string) map[string]string {
	if r, ok := o.resolved[name]; ok && r.Options != nil {
		return r.Options
	}
	return map[string]string{}
}

// hydrateProviders deep-copies the composed providers table into the plain value model
// the derive ctx exposes, resolving each entry's credential into the copy: an entry that
// points at one api_key_env_name the lookup finds carries api_key = that value, and one
// that does not carries no api_key at all. The copy is the whole point — the credential
// crosses into the derive invocation only (the table the launch relays stays
// secret-free, D8), and the input table is never mutated.
//
// The table is ProvidersForDerive's view, so every entry's api_key_env_name is the ONE
// variable it points at or absent (OQ-CN1: a multi-route provider points at none). The
// lookup is the CREDENTIAL GATE's (CredentialScope.LookupFor) on every launch path: it
// answers nothing for a variable another provider claims, so an agent's derive sees the
// api_key of the provider its own profile selects and of no other (OQ-CN2's
// rendered-config half — the environment half is the same gate's delivery).
func hydrateProviders(providers *jsonx.OrderedMap, lookup func(string) (string, bool)) map[string]any {
	root, _ := plainValue(ProvidersForDerive(providers)).(map[string]any)
	if root == nil {
		root = map[string]any{}
	}
	if lookup == nil {
		return root
	}
	for _, v := range root {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		hydrateCredential(entry, lookup)
		// AN ENDPOINT MAY NAME ITS OWN CREDENTIAL, and it is resolved the same way into the
		// endpoint's own `api_key`. The adapter pass is the writer today: an address a pack
		// service serves takes that service's caller token rather than the provider's key
		// (serviceCredentialEnv). A derive sending an agent to that endpoint sends its
		// api_key, so the credential travels with the address it belongs to.
		endpoints, _ := entry["endpoints"].(map[string]any)
		for _, ev := range endpoints {
			if ep, isMap := ev.(map[string]any); isMap {
				hydrateCredential(ep, lookup)
			}
		}
	}
	return root
}

// hydrateCredential resolves one table's api_key_env_name, when it names a single variable
// the lookup finds, into api_key on that same table.
func hydrateCredential(entry map[string]any, lookup func(string) (string, bool)) {
	keyName, _ := entry["api_key_env_name"].(string)
	if keyName == "" {
		return
	}
	if val, ok := lookup(keyName); ok && val != "" {
		entry["api_key"] = val
	}
}

// plainValue lowers one composed value into the plain model a derive ctx table holds:
// objects become map[string]any (recursively), a null drops, everything else passes
// through. A nil *jsonx.OrderedMap is a nil input, not a receiver to call methods on.
func plainValue(v any) any {
	m, ok := v.(*jsonx.OrderedMap)
	if !ok || m == nil {
		return v
	}
	out := make(map[string]any, m.Len())
	for _, k := range m.Keys() {
		sub, _ := m.Get(k)
		if sub == nil {
			continue
		}
		out[k] = plainValue(sub)
	}
	return out
}

// plainProfiles lowers the effective profile table into the same plain model, so the
// producer reads ctx.use_profiles.claude exactly as the in-jail derives do.
func plainProfiles(t map[string]string) map[string]any {
	out := make(map[string]any, len(t))
	for k, v := range t {
		out[k] = v
	}
	return out
}
