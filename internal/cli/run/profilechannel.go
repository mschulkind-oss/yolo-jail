package run

// profilechannel.go is the profile/provider channel ONE launch composes — the B-0 move
// applied to the environment half of the channel, exactly as stageRunPacks applied it to
// the pack trees and the launch-flag injection applied it to the argv (both documented at
// their call sites in run.go).
//
// The defect this removes: every piece of the channel was composed inside the container
// arm — the profile table and the pack env fold in assembleRunCmd, the composed provider
// table and the provider env vars in commonEnvBlock — and the macos-user branch returns
// before reaching any of it. That backend therefore parsed and VALIDATED `-p zai`
// (checkProfileTargets sits in stagePacks, above the dispatch) and then delivered nothing:
// no variant env, no provider env, no YOLO_PROVIDERS/YOLO_USE_PROFILES for its
// bootstrap, no credential pre-flight. `yolo -p zai -- claude` on macos-user composed
// nothing and said so to nobody — the same signature of silence B-0 found for the pack
// trees, one layer down.
//
// The composition is therefore hoisted ABOVE the backend dispatch, and each arm consumes
// the result: the container arm writes it into yolo-user-env.sh's channel section and
// each agent's own env file (deliverChannel — per-entry delivery, on a fresh launch and an
// attach alike), the macos-user arm layers it into its plan env and relays the two wire
// tables to its bootstrap. What reaches WHICH agent is the credential gate's answer
// (packload.ScopeCredentials, composed here once; docs/reference/providers.md).
// One composition means the
// two backends cannot answer differently about what a profile delivers — which is the same
// property packload.ProfileTable's launch-flag injection already claims for the two
// spellings of one launch.

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// packChannel is everything a launch composes from its selected profiles and providers,
// reduced to the form each consumer reads. Composed once, by composePackChannel, above
// the backend dispatch; never re-derived downstream.
type packChannel struct {
	// profiles is the CLI-keyed effective profile table (effectiveUseProfiles): the
	// merge the env block emits as YOLO_USE_PROFILES, the launch-flag injection reads,
	// and the profile disclosure line describes.
	profiles *jsonx.OrderedMap
	// bareNote is OQ-AP3's one launch line for a BARE profile list this launch narrowed (the
	// config key's or a -p's, config.ProfileFold.BareListNote): which agents take its first entry
	// alone. "" when none did. The profile disclosure prints it (noteUseProfiles).
	bareNote string
	// deselect answers, for an agent the selection reaches nothing for, where its selection
	// came from (the config `profile` key, with its file and line, or this launch's -p) and the
	// spelling that selects none for it there (config.ProfileDeselection). The profile
	// disclosure's warning prints it (noteUseProfiles); nil names the -p form alone.
	deselect func(agent, profile string) string
	// workerNotes is the line, one per worker, for every macos-user PURE WORKER with no jail
	// daemon that this launch does not start (planMacosUserWorkers; host-notch-services.md
	// HS-D29), recorded when the composition planned the workers and printed where the arm opens
	// its doorways (planMacosUserDoorways). nil on every other runtime.
	workerNotes []string
	// viaNotes is the line, one per agent, for every profiled agent whose via or carrier names a
	// pack service that this macos-user launch does not plan because the via re-points nothing of
	// the agent (planMacosUserViaServices, packload.ViaRouted.NoEffect), printed beside workerNotes.
	// nil on every other runtime.
	viaNotes []string
	// providers is the composed provider table (composedProviders): user `providers`
	// entries over every selected pack's `kind: "provider"` service facts. Emitted as
	// YOLO_PROVIDERS and read by the env derive below.
	providers *jsonx.OrderedMap
	// scope is THE CREDENTIAL GATE's answer for this launch (packload.ScopeCredentials,
	// docs/reference/providers.md OQ-CN2): the env_sources and pack env every
	// process may see, and per profiled agent what only that agent receives — its
	// provider's claimed credentials, the gated env its own selection satisfies, and its
	// pack's env derive's output (the shape vars, composed through the gate's lookup).
	// Every vehicle reads it and none re-derives it: the container arm writes the shared
	// half into yolo-user-env.sh and each agent's half into its own env file
	// (agentenvfiles.go), and the macos-user arm layers the shared half plus the launched
	// agent's (launchEnv).
	scope *packload.CredentialScope
	// userEnv is the hydrated env_sources BEFORE the gate — the secret channel the gate
	// scopes — with each removal (an inline null no later entry cancelled) carried as a nil
	// value after the assignments (config.HydrateEnvSources). Hydrated once, here, because two
	// hydrations would read every dotenv file twice and warn twice, and the paths that compose
	// the channel again (an attach's rekey, a pack-skew attach) hand this map back, removals
	// included. No writer delivers it whole any more: the shared file carries the shared
	// composition (scope.SharedEnv), and each agent's file what its own composition adds.
	userEnv *jsonx.OrderedMap
	// resolvedProfiles is every profile name this launch could activate, resolved to its
	// provider and option map (packload.ResolveProfiles). Emitted as YOLO_PROFILES and
	// handed to the env-derive runner, so both consumers of a profile's body — the jail's
	// derives and the host notch's env composition — read ONE resolution.
	resolvedProfiles map[string]packload.ResolvedProfile
	// localProviderForwards are implicit host-loopback forwards requested by user
	// provider URLs. They originate only in user config, never in a pack endpoint;
	// see localProviderForwards for why that boundary matters.
	localProviderForwards []any
	// localProviderForwardSources is the same set WITH provider attribution, for the
	// launch disclosure (OQ-PC2). Same walker, two projections — see providerlocal.go.
	localProviderForwardSources []providerForward
	// callerTokens are this entry's pack-service caller tokens, keyed by the variable carrying
	// each (callertokens.go, WB-D18): minted for a fresh launch, the running jail's own on an
	// attach. The writer puts them in the shared channel section, because the daemon and every
	// bridged client read them from the environment, and the credential gate composes them into
	// the derives that point a client at the service. nil when no selected service runs.
	callerTokens map[string]string
	// scopedTokenVars are the callerTokens variables whose tokens are SCOPED
	// (packload.ScopedCallerTokenDaemons, providers.md OQ-CN7 (c)): a selected
	// pack names the daemon's token through `{caller_token}`, so the token is exported only in
	// the agent files that pointer reaches, and the shared file carries it as a non-exported
	// record (entrypoint.ScopedCallerTokenRecord) for the daemon and the next attach to read.
	scopedTokenVars map[string]bool
	// carriedTokens are the running jail's caller tokens an attach adopted that this entry's own
	// composition needs none of — a profile-served daemon the jail's boot started and this entry
	// does not select. Recorded, never exported, so a later entry that selects it again adopts
	// the token that daemon still demands (rekeyChannelForAttach). nil on a fresh launch.
	carriedTokens map[string]string
	// bootEnv is the container's frozen environment, when the delivery knows it (an attach
	// reads it off the running container): one of the places a value in an agent's incoming
	// environment came from yolo rather than from the user (inheritedValues, OQ-CN8). nil on a
	// fresh launch, whose argv carries none of the channel's names.
	bootEnv map[string]string
	// unservedVias are the profiles whose via this notch does not serve (packload.ViaServedAt),
	// sorted: their agents keep their own clients, and the launch names them (noteUnserved).
	unservedVias []string
	// served is what this launch's notch serves (servedDaemons), for the checks that read
	// the channel after it is composed (checkEnvOverrides).
	served packload.ServedDaemons
	// packs is the selected pack set the channel was composed from, for a reader that asks a
	// pack fact of the composed tables: which service a profile's via names (viaURLsThrough).
	packs []*packload.Pack
	// servedAddresses is the declared-to-served address map this entry was composed with
	// (servedaddresses.go): the ports a fresh launch picked on a shared network namespace, or
	// the running jail's on an attach. The writer records it in the channel section, which is
	// where the next attach reads it back. nil when nothing moved.
	servedAddresses map[string]string
}

// composePackChannel composes the channel from the config and the STAGED pack set.
//
// userEnv may be nil, which means "hydrate env_sources here", through
// config.HydrateEnvSources: the assignments and the removals of one pass, the removals carried
// as nil values so a later composition from the same map keeps them. The paths that compose
// again pass the channel's own map back rather than resolving env_sources a second time; a
// hand-built assembleInput (every one is a test) passes whatever it has, or nil.
func (o *Options) composePackChannel(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	userEnv *jsonx.OrderedMap) (*packChannel, error) {
	if userEnv == nil {
		userEnv = config.HydrateEnvSources(o.Workspace, cfg, func(msg string) {
			o.pr(o.Stdout).print(msg)
		})
	}
	// The fold effectiveUseProfiles returns the table of, kept whole for what a BARE list did in
	// it (OQ-AP3): the launch line naming the agents that ignore part of it.
	fold := o.profileFold(cfg, packs)
	profiles := fold.Table
	// The user's profile DECLARATIONS, at user scope — never read off `cfg`, which is the
	// merged map and would let a workspace spelling through (config.LoadProfiles reads
	// the user file directly; OQ-CS5). Malformed entries arrive as warnings, which this
	// is the right place to surface: the fatal form of the same problem already refused
	// the launch in ValidateConfig.
	userProfiles, err := config.LoadProfiles(func(msg string) {
		o.pr(o.Stdout).print(msg)
	})
	if err != nil {
		return nil, err
	}
	// THE EIGHTH bespoke pre-flight (the numbering is packs.go's and
	// providerpreflight.go's; checkProfileTargets was the fifth), and the one OQ-CS6
	// buys: declaration is MANDATORY, so a selected name nothing declares refuses here
	// rather than silently doing nothing. Before the resolution below, because an
	// undeclared name has no resolution to argue about.
	if err := o.checkProfileDeclarations(fold, userProfiles, packs); err != nil {
		return nil, err
	}
	// The provider table composes BEFORE the profiles resolve, because the resolution
	// reads the declared options off it (packload.providerOptions) and the census must
	// measure the surface this launch actually carries — not the one a manifest walk
	// would have described. Same object for both, so the env derive below and the
	// resolution cannot disagree about what a provider declares.
	//
	// THE PAYLOAD FIRST: the jail daemons this launch declares (jailDaemonsFor, the same
	// composer the container argv serializes and the macos-user arm declines, on the runtime
	// Run resolved) decide both the caller tokens and what is SERVED AT THIS NOTCH
	// (servedDaemons), and the provider table composes only the addresses served here.
	specs := o.jailDaemonsFor(cfg, o.runtime, packs)
	// THE VIA TRIGGER (macosuserservices.go, host-notch-services.md HS-D30): on macos-user a via or a
	// carrier routes an agent through a pack service only once the launch runs its host half, and
	// it refuses nothing while the service is unserved, so the launch asks first and plans the
	// service when serving it would route some profiled agent through it.
	var viaNotes []string
	if o.runtime == "macos-user" { // parity: HonoredBy — a container runs the via's service as a jail daemon; macos-user its host half (macosuserservices.go)
		notes, err := o.planMacosUserViaServices(cfg, packs, profiles, userProfiles, o.servedDaemons(specs))
		if err != nil {
			return nil, err
		}
		viaNotes = notes
	}
	// ONE RETRY PER LAUNCH-OWNED SERVICE (macosuserservices.go): on macos-user a pairing through a
	// pack service's adaptation refuses at the gate below until this launch plans that service's
	// host half, and then composes again against it. Bounded by the services the packs declare.
	for tries := 0; ; tries++ {
		c, err := o.composePackChannelWith(cfg, packs, userEnv, profiles, userProfiles, specs)
		// THE PURE WORKERS this macos-user launch starts outside the sandbox (planMacosUserWorkers,
		// host-notch-services.md HS-D29), planned off the selection this composition resolved and
		// composed in once more, so a pack env pointer at one is served and handed its caller
		// token, as a container composes a pointer at the worker's jail daemon. A worker serves no
		// adaptation, so the second composition resolves the same selection.
		if err == nil && o.runtime == "macos-user" { // parity: HonoredBy — a container runs a worker's jail daemon in the jail; macos-user runs a host-only worker's host half (macosuserservices.go)
			added, notes := o.planMacosUserWorkers(packs, c)
			if added {
				c, err = o.composePackChannelWith(cfg, packs, userEnv, profiles, userProfiles, specs)
			}
			if c != nil {
				c.workerNotes = notes
			}
		}
		if err == nil || o.runtime != "macos-user" || tries > len(packs) { // parity: HonoredBy — a container runs the service's jail daemon; macos-user the host half of one serving an adaptation (macosuserservices.go), and the guest the rest's jail daemons (JD-9)
			if c != nil {
				c.bareNote = fold.BareListNote(fold.BareFrom == profileFoldFromKey)
				// The two sources profileFold folded, for the disclosure's "reaches nothing"
				// warning to name the one that reached an agent and how to undo it there.
				key, flags := config.ConfigProfileSelection(cfg), o.ProfileFlags()
				c.deselect = func(agent, _ string) string {
					return config.ProfileDeselection(key, flags, agent)
				}
				c.viaNotes = viaNotes
			}
			// THE VIA GATE OVER THE VIAS THIS LAUNCH SERVES (checkViaRoutes' rule, WG-I13 to WG-I15): on
			// macos-user that pre-flight runs before the channel plans a launch-owned service, so it
			// sees every via cleared and asks nothing; the channel asks it again here once a planned
			// service serves them (HS-D30), as a container launch's pre-flight does of its jail daemon.
			if err == nil && o.runtime == "macos-user" && len(o.launchServices) > 0 { // parity: HonoredBy — a container's checkViaRoutes sees its jail daemon served; macos-user's served set grows only here
				if gerr := o.checkServedViaRoutes(packs, c); gerr != nil {
					return nil, gerr
				}
			}
			return c, err
		}
		added, perr := o.planMacosUserService(err, packs)
		if perr != nil {
			return nil, perr
		}
		if !added {
			return nil, err
		}
	}
}

// composePackChannelWith is one composition of the channel over the payload specs and whatever
// launch-owned services this launch has planned so far (servedDaemons adds them).
func (o *Options) composePackChannelWith(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	userEnv, profiles *jsonx.OrderedMap, userProfiles map[string]packload.UserProfile,
	specs []loopholes.JailDaemonSpec) (*packChannel, error) {
	served := o.servedDaemons(specs)
	providers, unservedAdaptations, err := o.composedProvidersFor(cfg, packs, served)
	if err != nil {
		return nil, err
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		return nil, err
	}
	// THE ACTIVE SETS' OWN RULES (docs/design/active-provider-sets.md AP-D3, OQ-AP2, AP-D9),
	// once every name resolves and before anything is composed from them: a name listed twice,
	// a list at an agent whose pack does not declare provider_sets, two entries on one provider,
	// a via entry anywhere but first. The host notch refuses the same table in the same words.
	if problems := packload.ProfileSetProblems(packs, providers, packload.ProfileSets(profiles), resolved); len(problems) > 0 {
		return nil, fmt.Errorf("packs: %s", strings.Join(problems, "\npacks: "))
	}
	// A via this notch does not serve is cleared, so its agent keeps its own client, and named
	// (noteUnserved): the host's rule, now every notch's (ViaServedAt).
	resolved, unservedVias := packload.ViaServedAt(resolved, packs, served)
	// The caller tokens of the jail daemons the payload names (callertokens.go), so a token
	// exists exactly for a daemon the launch declared.
	callerTokens, err := o.launchCallerTokens(specs)
	if err != nil {
		return nil, err
	}
	// A launch-owned service's token beside the jail daemons' (macosuserservices.go).
	for k, v := range launchservice.CallerTokens(o.launchServices) {
		if callerTokens == nil {
			callerTokens = map[string]string{}
		}
		callerTokens[k] = v
	}
	var scopedTokenVars map[string]bool
	for daemon := range packload.ScopedCallerTokenDaemons(packs) {
		if v := paths.ServiceCallerTokenEnv(daemon); v != "" {
			if scopedTokenVars == nil {
				scopedTokenVars = map[string]bool{}
			}
			scopedTokenVars[v] = true
		}
	}
	c := &packChannel{
		packs:                       packs,
		scopedTokenVars:             scopedTokenVars,
		unservedVias:                unservedVias,
		served:                      served,
		servedAddresses:             o.movedServedAddresses(),
		callerTokens:                callerTokens,
		profiles:                    profiles,
		providers:                   providers,
		userEnv:                     userEnv,
		resolvedProfiles:            resolved,
		localProviderForwards:       localProviderForwards(cfgMap(cfg, "providers")),
		localProviderForwardSources: localProviderForwardSources(cfgMap(cfg, "providers")),
	}
	// THE CREDENTIAL GATE, ONCE (OQ-CN2): the one decision of what reaches which agent,
	// which every vehicle reads. It also runs each profiled agent's env derive (the
	// env-derive runner, OQ-CS8), with that agent's selected provider's credential — and
	// no other provider's — hydrated into the derive's copy of the table. A credential
	// resolves through what this launch carries: the hydrated env_sources, then the
	// environment yolo was launched from, so the relay does not claim a credential the
	// launch would not have carried.
	//
	// The REMOVALS cross too: a null ranks with env_sources in the one ordered composition every
	// vehicle serializes (packload's envcompose.go, the null rank OQ-NC12's decision records), so
	// it takes out a pack env value of its name in a jail as it does at the host, and never a
	// shape var.
	assignments, removals := config.SplitHydratedEnvSources(userEnv)
	scope, err := packload.ScopeCredentials(packload.ScopeInput{
		Packs:     packs,
		Providers: providers,
		Profiles:  packload.ProfileTable(profiles),
		// Each agent's whole active set (§4.5): every entry's claimed key reaches that agent.
		Sets:              packload.ProfileSets(profiles),
		Resolved:          resolved,
		EnvSources:        assignments,
		EnvSourceRemovals: removals,
		// THE SERVICE CALLER TOKENS, answering first in every agent's lookup (WB-D18): an
		// address a pack service serves names its token as its credential, and the derive of
		// any agent sent there must read the value that service demands.
		CallerTokens: callerTokens,
		// What this notch cannot serve (served above): nil on a container launch, and on
		// macos-user every service adaptation, so a pairing only one resolves refuses naming
		// why, exactly as the host's does (ES-D18, generalized by notch convergence item 2).
		UnservedAdaptations: unservedAdaptations,
		Served:              &served,
		// THE AGENT FILES (docs/design/model-lists-and-pickers.md MM-D33): every backend of this
		// notch writes them, beside each agent's env file (writeAgentEnvFiles), so each env derive
		// is told it may compose one.
		AgentFiles: true,
		Fallback: func(name string) (string, bool) {
			v := o.Getenv(name)
			return v, v != ""
		},
		// THE REGION FILE (docs/design/bedrock-plumbing.md BR-DIR1), read on the machine this
		// launcher runs on, from the environment it was launched from: an agent on a provider
		// reached through a region that receives none is given the region its credential's
		// profile names there, in its own env file. Nothing is Inherited: no backend forwards the
		// launching shell into the jail (BR-D2). So a region or profile variable left in that
		// shell is Stranded: the user's choice, which the fill does not replace with the file's
		// answer for another profile, and the refusal names it. The loophole settings are the
		// ones the launch writes each loophole's settings file from.
		RegionFiles: &packload.RegionFileSource{Getenv: o.Getenv, Setting: packload.LoopholeSettingIn(cfg),
			Stranded: func(name string) bool { return o.Getenv(name) != "" }},
	})
	if err != nil {
		return nil, err
	}
	c.scope = scope
	return c, nil
}

// checkProfileDeclarations refuses a SELECTED profile name nothing declares — the flag
// and config spellings alike: a `profile` value and a `-p <name>` are both in the
// table this reads (effectiveUseProfiles merged them). It sits beside
// checkProfileTargets, its CLI-name twin on the same table, and shares that check's
// reason for being FATAL: a silently inert selector is indistinguishable from a working
// one (OQ-CS6 — the reversal of the old free-form-names ruling).
//
// The declared set is packload.DeclaredProfileNames — every kind:profile the staged
// packs ship plus every user `profiles` entry — because that is exactly the union the
// design makes a profile name answer to; a second list here would be a second idea of
// "declared".
func (o *Options) checkProfileDeclarations(fold config.ProfileFold,
	userProfiles map[string]packload.UserProfile, packs []*packload.Pack) error {
	profiles := fold.Table
	if profiles.Len() == 0 {
		return nil
	}
	declared := packload.DeclaredProfileNames(packs, userProfiles)
	isDeclared := map[string]bool{}
	for _, name := range declared {
		isDeclared[name] = true
	}
	var problems []string
	reported := map[string]bool{}
	sets := packload.ProfileSets(profiles)
	for _, agent := range profiles.Keys() {
		// EVERY ENTRY of the agent's active set (docs/design/active-provider-sets.md AP-D3): one
		// undeclared entry refuses the launch, and yolo never runs the declared rest.
		for _, name := range sets[agent] {
			if name == "" || isDeclared[name] {
				continue
			}
			reported[name] = true
			problems = append(problems, fmt.Sprintf("profile %q selected for %s: %s",
				name, agent, packload.UndeclaredProfileMessage(name, declared)))
		}
	}
	// AND EVERY ENTRY OF A BARE LIST, whichever agents took it (OQ-AP3 read with AP-D3), the
	// config key's or a -p's. An agent whose pack declares no provider_sets took its first entry
	// alone (config.FoldProfiles), so the rest are in no agent's set above, and a typo there
	// would pass whenever no set-capable agent is selected and refuse whenever one is. Asked
	// only when the list reached some agent, as a bare name always was: the fold records a list
	// no CLI received as none.
	if bare := fold.BareList; len(bare) > 1 {
		spelled := "the bare -p list " + strings.Join(bare, ",")
		if fold.BareFrom == profileFoldFromKey {
			spelled = "the profile key's list " + strings.Join(bare, ",")
		}
		for i, name := range bare {
			if isDeclared[name] || reported[name] {
				continue
			}
			reported[name] = true
			problems = append(problems, fmt.Sprintf("profile %q (entry %d of %s): %s",
				name, i+1, spelled, packload.UndeclaredProfileMessage(name, declared)))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("packs: %s", strings.Join(problems, "\npacks: "))
}

// deliverySource answers whether this launch delivers a variable, non-empty, and the PHRASE
// naming which source answered:
//
//  1. the one ordered composition (packload's envcompose.go) of some process of the launch —
//     the shared one, else the first agent's (sorted) that delivers the name
//     (CredentialScope.Delivered) — which names the source that WINS there: a shape var over
//     env_sources over the pack env fold, an env_sources null removing the fold's value. It
//     answers FromProfileEnv, FromEnvSources or FromPackEnv;
//  2. argvPairs — the `-e K=V` pairs of the assembled container argv, which carry every
//     pack-shipped loophole's jail_env, a channel the composition does not hold. Nil on the
//     macos-user arm, which assembles no argv;
//  3. the environment yolo itself was launched from, which reaches the jail by no channel
//     and which the one reader, jailOriginLookup, drops.
//
// An EMPTY value is unset at every step: the env-derive runner drops an empty value
// rather than composing an empty token, and an empty credential is the failure the
// pre-flight exists to name, not an escape from it.
//
// The credential pre-flight does not read this: it asks each AGENT what reaches it
// (checkProviderCredentials), since a launch-wide answer counted the launching shell for an
// agent nothing relays it to.
//
// The phrase exists because a refusal has to name where each side was DECLARED
// (docs/reference/protocol-resolution.md#the-four-outcomes): "something overrides that
// pack's variable" sends a reader hunting through four files, and "env_sources, and a
// selected pack's env contribution" does not. It never carries the VALUE — these are
// typically credentials.
//
// It cannot name WHICH pack set a var: the launch delivers the fold rather than any one
// contribution, so "a selected pack's" is the honest precision available here; the
// `yolo pack footprint` verb is where a reader learns which one.
//
// THE PHRASES THEMSELVES ARE packload's (packload.From*), not this file's, because the
// `yolo check` prediction reports the same refusal from the same composition
// (internal/cli/check/envoverrides.go). Spelled at both callers they would drift, and a
// prediction that worded one problem differently from the launch is the defect that file
// exists to avoid rather than to introduce.
//
// ⚠ THE LAST SOURCE IS NOT A DELIVERY INTO THE JAIL: nothing forwards the launch
// environment into the jail under its own name. So the env-override pre-flight reads this
// through jailOriginLookup (envoverrides.go), which drops that answer.
//
// "DELIVERED" MEANS TO SOME PROCESS OF THE LAUNCH, since the credential gate (OQ-CN2):
// the shared set or any one agent's. A hydrated credential the gate withholds from every
// agent is NOT delivered, so it answers nothing here — an override between two variables
// nobody receives overrides nothing — and the credential pre-flight never asks about one,
// having been narrowed to the selected providers (OQ-CN3).
func (c *packChannel) deliverySource(o *Options, argvPairs map[string]string,
	name string) (value, origin string, ok bool) {
	if e, found := c.scope.Delivered(name); found {
		return e.Value, e.Origin, true
	}
	if v, found := argvPairs[name]; found && v != "" {
		return v, packload.FromContainerArgv, true
	}
	if v := o.Getenv(name); v != "" {
		return v, packload.FromLaunchEnv, true
	}
	return "", "", false
}

// launchEnv flattens the channel into the launch environment of ONE PROGRAM: the form the
// macos-user arm layers into its plan env, for the program its invocation starts. agent is
// that program's name (the basename of its argv[0]); a shell, or any name no profile
// selects, receives the shared composition only.
//
// PER LAUNCH for the session env, and the macos-user arm says so
// (noteMacosUserCredentialScope): this backend runs one command per invocation under one
// session env file. The launched agent's own values reach every process of its session — as
// any agent's reach its children on every backend. Another agent started inside the session
// gets its own values from its per-agent env file, which the arm writes with the container
// vehicle's writer (writeMacosUserAgentEnvFiles, providers.md OQ-CN9); before
// OQ-CN9 it received none of them.
//
// THE ORDER IS THE ONE COMPOSITION's (packload's envcompose.go, OQ-NC12's option A), the
// order the host exec and the container's files serialize too: the agent's shape vars over
// the env_sources it receives over the pack env fold, then the three wire tables. So a profile's
// ANTHROPIC_BASE_URL now beats a dotenv entry of the same name on this backend, where
// env_sources used to be layered LAST, and the launched agent and a login shell that starts it
// read one winner per name. A user who wants a value to beat the profile types it on the
// command line, which the per-agent file keeps (OQ-CN8).
//
// A removal (an env_sources null, a shape tombstone) is the name left out: `env -i K=V…` starts
// from nothing, so there is nothing to remove.
func (c *packChannel) launchEnv(agent string) *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	for _, e := range c.scope.EnvFor(agent).Entries() {
		if e.Unset || e.Key == "" {
			continue
		}
		env.Set(e.Key, e.Value)
	}
	wire := c.wireTableValues()
	for _, k := range entrypoint.WireTables() {
		env.Set(k, wire[k])
	}
	return env
}

// wireTableValues is this channel's three wire tables, serialized, keyed by the names in
// entrypoint.WireTables — the ONE list both writers (launchEnv and writeUserEnvFile) range
// over, so a table cannot reach one vehicle and not the other. An empty table is written as
// `{}` rather than omitted: the readers treat the two alike, and the container and macos-user
// boots then see the same input shape.
func (c *packChannel) wireTableValues() map[string]string {
	return map[string]string{
		entrypoint.ProvidersWireEnv:   jsonDumpsOrEmptyObj(c.providers),
		entrypoint.ProfilesWireEnv:    jsonDumpsOrEmptyObj(packload.ProfilesWireTable(c.resolvedProfiles)),
		entrypoint.UseProfilesWireEnv: jsonDumpsOrEmptyObj(c.profiles),
	}
}
