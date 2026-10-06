package config

// capabilities.go is THE CAPABILITY CENSUS behind `required_capabilities`
// (docs/design/agent-auth-modes.md §6.2, OQ-CAP2): which named jobs a launch can count as
// done, so that a config declaring it needs one that nothing does is refused before any backend
// starts. THREE CALLERS answer from this one census: a jail launch's gate
// (`run.refuseUnmetCapabilities`), the same gate at the host notch, which `yolo host -- <cmd>`
// asks over the user scope before anything else it does (cli's refuseHostUnmetCapabilities), and
// `yolo check`'s prediction of the jail gate. Both gates print the one refusal
// UnmetCapabilityRefusal words, so the two notches say the same thing about the same gap.
//
// FOUR SOURCES, and they are the whole census:
//
//   - the baseline every agent meets (CapabilityBaseline), which nothing has to declare;
//   - `providers.<name>.capabilities` in the USER'S OWN config (see the over-permission below);
//   - an `mcp_servers.<name>` entry whose `provides` names the capability (§6.2's collision rule
//     already refuses two servers claiming one name). A null-removed server provides nothing:
//     the jail deletes it rather than running it;
//   - the ACTIVE SOURCE of every agent a selected pack installs — §6.2's "the active agent" —
//     resolved by the rule capability-driven MCP delivery resolves it by (luahook's
//     sourceCapabilities). For each profile in the agent's active set, the `capabilities` of the
//     provider that profile selects, read from the COMPOSED providers table
//     (packload.ComposeProviders: a pack's provider under the user's override, so a null removes
//     it and a list replaces the pack's). For an agent no profile points at a provider, its
//     BUILT-IN login's: the `capabilities` on its pack's `program`, found by bin ownership
//     through packload.NativeCapabilities (§6.1 clause 1).
//
// `mcp_presets` contributes nothing on purpose: a preset is a baked command list
// (internal/entrypoint/mcp.go), and neither shipped preset declares a `provides`.
//
// A PACK'S PROVIDER COUNTS ONLY WHERE A PROFILE MAKES IT AN AGENT'S SOURCE, and an agent's
// built-in login only where no profile replaces it. A provider rides into a launch whether or
// not anything runs on it: openai-auth's `openai-codex`, which declares web_search, joins every
// launch selecting claude, codex or pi through their `needs`. Counting the composed table whole
// let `pi` alone pass `web_search`, and claude on its `bedrock` profile too — both sources §6.2
// names as lacking search, and both refused by this gate before packs counted at all.
//
// ANY AGENT'S ACTIVE SOURCE SATISFIES THE LAUNCH, and so does any entry of an active set, not
// only the primary a session starts on. The gate judges the launch, not the session a user
// starts in it — a set's agent switches between its entries without a relaunch — and erring
// toward launching is the right direction for a fatal refusal: the gap the gate closes is the
// config where nothing could do the job (setup-support-gaps.md G9).
//
// ONE DELIBERATE OVER-PERMISSION, the one the gate shipped with on 2026-09-17: a provider the
// USER declares capabilities for counts whether or not a profile selects it. It predates the
// pack half, and it is what the shipped config reference told a user to write for a capability
// the gate could not see ("writing the same name under providers.<name>.capabilities states it
// in the place this gate reads"), so narrowing it would refuse configs that followed that
// advice. Narrowing it is a change of its own, not part of counting the packs.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// CapabilityBaseline is the capability set every agent yolo can launch meets without anything
// declaring it: editing files and running commands are the floor of "a coding agent", so
// agent-auth-modes.md §6.2 makes them the default requirement. Named rather than left implicit
// because `"required_capabilities": ["code_editing"]` is the example the shipped config
// reference carries, and a gate that refused the floor would refuse the documented spelling.
var CapabilityBaseline = []string{"code_editing", "command_execution"}

// AllowUnmetCapabilitiesEnv is the escape hatch out of the capability gate: set, a launch whose
// requirement nothing declares continues, saying so. It is for a capability the environment
// really has that no declaration states — an agent whose pack does not declare what its login
// does, or a tool the agent reaches some way yolo does not deliver — never for a census defect.
const AllowUnmetCapabilitiesEnv = "YOLO_ALLOW_UNMET_CAPABILITIES"

// CapabilityLaunch is what the census reads of a launch beyond its config: the pack selection,
// and the profiles each agent in it runs on. The launch hands its own (its narrowed entries, its
// `-p`); `yolo check` hands ConfigCapabilityLaunch, the user scope's selection under the config's
// `profile` key.
type CapabilityLaunch struct {
	// Packs resolves the selection, reporting complete=false when some of it could not be read.
	Packs func() (packs []*packload.Pack, complete bool)
	// ProfileSets is CLI name → active set over packs, in packload.ProfileSets' shape. nil, or
	// an agent the answer omits, selects no profile: that agent runs on its built-in login.
	ProfileSets func(packs []*packload.Pack) map[string][]string
}

// ConfigCapabilityLaunch is the launch a config describes with no flag above it: the user scope's
// pack selection (UserScopeSelectedPacks, the one validation reserves names for), under cfg's
// `profile` key (ConfigProfileSets). `yolo check` hands it to the census, since a `-p` is an
// argument to a launch that has not happened.
func ConfigCapabilityLaunch(cfg *jsonx.OrderedMap) *CapabilityLaunch {
	return &CapabilityLaunch{
		Packs: UserScopeSelectedPacks,
		ProfileSets: func(packs []*packload.Pack) map[string][]string {
			return packload.ProfileSets(ConfigProfileSets(cfg, packs))
		},
	}
}

// errSelectionIncomplete is UnmetCapabilities' answer when a name is unmet and some selected
// pack could not be read: that pack may be the satisfier, so the census proves nothing.
var errSelectionIncomplete = errors.New("a selected pack could not be read, so whether it " +
	"satisfies them is unknown")

// CapabilitySatisfiers maps every capability a source of this launch provides to the phrase
// naming that source. packs is the launch's selection and sets its CLI name → active set; see
// the file doc for the sources and why a pack's counts only where it is active.
//
// The error is a pack half the census could not read: a provider composition that failed (an
// address conflict the launch refuses itself, below this census), or profiles that would not
// load or resolve (which the launch's channel composition refuses). The map still holds every
// source the failure did not touch: the baseline, the user's declarations, the MCP servers, and
// the built-in login of every agent no profile points elsewhere.
func CapabilitySatisfiers(cfg *jsonx.OrderedMap, packs []*packload.Pack, sets map[string][]string) (map[string]string, error) {
	out := map[string]string{}
	add := func(capName, source string) {
		if _, seen := out[capName]; !seen && capName != "" {
			out[capName] = source
		}
	}
	for _, name := range CapabilityBaseline {
		add(name, "the baseline every agent meets")
	}

	user := capabilityMap(cfg, "providers")
	if user != nil {
		for _, name := range user.Keys() {
			pm := capabilityMap(user, name)
			if pm == nil {
				continue // null drops the provider; it declares nothing
			}
			for _, capName := range capabilityStrings(pm, "capabilities") {
				add(capName, "provider '"+name+"'")
			}
		}
	}

	if servers := capabilityMap(cfg, "mcp_servers"); servers != nil {
		for _, name := range servers.Keys() {
			// A null-removed server is one this jail will not run: the null crosses in
			// YOLO_MCP_SERVERS and the entrypoint's LoadMCPServers deletes the entry. So the
			// merged VALUE is read rather than the key set, or a workspace `"tavily": null`
			// would keep satisfying `web_search` off the user-level entry it just deleted.
			sm := capabilityMap(servers, name)
			if sm == nil {
				continue
			}
			if provides, ok := sm.Get("provides"); ok {
				if s, ok := provides.(string); ok {
					add(s, "mcp_servers."+name)
				}
			}
		}
	}

	if len(packs) == 0 {
		return out, nil
	}

	// THE ACTIVE SOURCE of each agent the selection installs. An agent with no profile runs on
	// its built-in login; every other agent runs on the providers its active set selects, read
	// from the table the launch composes, so the pack's provider default arrives under the
	// user's override exactly as the derives see it.
	var problem error
	providers, err := packload.ComposeProviders(user, packs)
	if err != nil {
		problem = fmt.Errorf("the provider table did not compose: %w", err)
	}
	var resolved map[string]packload.ResolvedProfile
	resolveOnce := func() bool {
		if resolved != nil || problem != nil {
			return problem == nil
		}
		declared, err := LoadProfiles(func(string) {})
		if err != nil {
			problem = fmt.Errorf("the profile declarations did not load: %w", err)
			return false
		}
		if resolved, err = packload.ResolveProfiles(packs, declared, providers); err != nil {
			problem = fmt.Errorf("the profiles did not resolve: %w", err)
			return false
		}
		return true
	}
	seen := map[string]bool{}
	for _, p := range packs {
		for _, bin := range p.InstallBins() {
			if seen[bin] {
				continue
			}
			seen[bin] = true
			native := func() {
				for _, capName := range packload.NativeCapabilities(packs, bin) {
					add(capName, "agent '"+bin+"' (its built-in login)")
				}
			}
			set := sets[bin]
			if len(set) == 0 {
				native()
				continue
			}
			if !resolveOnce() {
				continue // which source this agent runs on is unknown; problem says why
			}
			for _, profile := range set {
				// A profile that resolves to no provider leaves the agent on its own login,
				// as packload.ProviderFor answers for every derive; one naming a provider the
				// table does not hold provides nothing (sourceCapabilities' rule).
				provider := packload.ProviderFor(resolved, profile)
				if provider == "" {
					native()
					continue
				}
				for _, capName := range capabilityStrings(capabilityMap(providers, provider), "capabilities") {
					add(capName, "provider '"+provider+"' (agent '"+bin+"', profile '"+profile+"')")
				}
			}
		}
	}
	return out, problem
}

// UnmetCapabilities returns cfg's `required_capabilities` entries nothing satisfies, in
// declaration order and de-duplicated, so a refusal names them the way the config does.
//
// launch is consulted ONLY when the config's own declarations leave a name unmet, so a launch
// that requires nothing, or only what it declares itself, resolves no pack for this census. That
// is equivalent rather than an approximation: the pack half only adds to what the config's own
// declarations provide. A nil launch counts no pack.
//
// A non-nil error means the census could not prove the names it returns unmet: some selected
// pack could not be read, the provider table did not compose, or the profiles did not resolve.
// Each is a fault the launch reports itself — pack staging refuses a pack it cannot stage, and
// the channel composition refuses the table and the profiles — so a caller that refused here
// would name the second fault first. The names are returned beside the error for that caller to
// say what it could not check.
func UnmetCapabilities(cfg *jsonx.OrderedMap, launch *CapabilityLaunch) ([]string, error) {
	have, _ := CapabilitySatisfiers(cfg, nil, nil) // no pack, so nothing that can fail
	missing := unmetGiven(cfg, have)
	if len(missing) == 0 || launch == nil || launch.Packs == nil {
		return missing, nil
	}
	packs, complete := launch.Packs()
	var sets map[string][]string
	if launch.ProfileSets != nil {
		sets = launch.ProfileSets(packs)
	}
	have, err := CapabilitySatisfiers(cfg, packs, sets)
	missing = unmetGiven(cfg, have)
	if len(missing) == 0 {
		return nil, nil
	}
	if err != nil {
		return missing, err
	}
	if !complete {
		return missing, errSelectionIncomplete
	}
	return missing, nil
}

// unmetGiven is the requirement list minus what have holds.
func unmetGiven(cfg *jsonx.OrderedMap, have map[string]string) []string {
	seen := map[string]bool{}
	var missing []string
	for _, name := range capabilityStrings(cfg, "required_capabilities") {
		if name == "" || have[name] != "" || seen[name] {
			continue
		}
		seen[name] = true
		missing = append(missing, name)
	}
	return missing
}

// capabilityMap is m[key] as an object, nil for an absent, null or non-object value.
func capabilityMap(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if m == nil {
		return nil
	}
	v, _ := m.Get(key)
	om, _ := asMap(v)
	return om
}

// capabilityStrings is m[key]'s string members, nil for anything but a list; the validator
// reports a malformed list, and the census reads what it can.
func capabilityStrings(m *jsonx.OrderedMap, key string) []string {
	if m == nil {
		return nil
	}
	v, _ := m.Get(key)
	raw, ok := asList(v)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := asStr(e); ok {
			out = append(out, s)
		}
	}
	return out
}

// RequiredCapabilitiesProblems is the validator's verdict on cfg's `required_capabilities` alone:
// the messages ValidateConfig would add for the key (a value that is not a list, an entry that is
// not a string), nil when its shape is fine or it is absent. For a caller that judges the key
// without validating the whole config: `yolo host --`, whose composition reads the user scope
// directly and never runs ValidateConfig, would otherwise read `"required_capabilities":
// "web_search"` as requiring nothing (capabilityStrings reads a non-list as empty) and launch.
func RequiredCapabilitiesProblems(cfg *jsonx.OrderedMap) []string {
	var errs []string
	if cfg != nil {
		validateRequiredCapabilities(cfg, &errs)
	}
	return errs
}

// UnmetCapabilityRefusal is the capability gate's whole message, the same at EVERY notch, in the
// shape packload.ProviderCredentialRefusal gives the credential pre-flight: plain lines, the
// verdict first, which each notch prints in its own way (the jail's launcher with its markup,
// `yolo host` behind its prefix). missing and err are UnmetCapabilities' answer; held is whether
// AllowUnmetCapabilitiesEnv is set; where locates the key in the files that wrote it
// (Sources.Locations), nil for nowhere. refuse is whether the launch stops.
//
// A census that could not look warns and continues (UnmetCapabilities says why), and the hatch is
// consulted only where it suppresses something, so a launch with no gap never announces it
// (providerpreflight.go's rule); when it does suppress, the notice says what: nothing was
// repaired. Only a refusal is more than one line, and its last line is the next step, naming the
// hatch (docs/reference/happy-path-principle.md).
func UnmetCapabilityRefusal(missing []string, err error, held bool, where []string) (lines []string, refuse bool) {
	if len(missing) == 0 {
		return nil, false
	}
	named := "'" + strings.Join(missing, "', '") + "'"
	if err != nil {
		return []string{"Warning: cannot tell whether anything satisfies required capability " +
			named + ": " + err.Error() + ". Continuing: this launch reports that problem itself " +
			"further on."}, false
	}
	if held {
		return []string{"Warning: " + AllowUnmetCapabilitiesEnv + " is set — CONTINUING with " +
			"required capability " + named + " that nothing in this launch satisfies. Nothing " +
			"was repaired: whatever needed the capability still has to do without it."}, false
	}
	lines = []string{"Refusing to launch: config.required_capabilities declares " + named +
		", and nothing this config or its selected packs declare satisfies it."}
	if len(where) > 0 {
		lines = append(lines, "  config.required_capabilities is written at "+
			strings.Join(where, " and at ")+".")
	}
	lines = append(lines,
		"  A capability is satisfied by a declaration: `providers.<name>.capabilities` "+
			"naming it (the agent has it natively there), an `mcp_servers.<name>` entry with "+
			"\"provides\": \"<capability>\", or a selected agent whose pack declares it for the "+
			"source the agent runs on: its built-in login, or the provider its profile selects.",
		"Declare the satisfier, drop the name from required_capabilities, or launch anyway "+
			"with "+AllowUnmetCapabilitiesEnv+"=1.")
	return lines, true
}
