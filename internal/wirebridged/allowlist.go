package wirebridged

// allowlist.go is Part 5's model allowlist (docs/design/wire-bridge-gateway.md §6, OQ-WG3): on
// the bridge path the bridge refuses a model outside the provider's list, the one hard refusal
// an agent whose picker is soft (pi's enabledModels) can have.
//
// THE LIST IS NOT A SECOND LIST (OQ-WG3, ruled 2026-09-25: "If we put it in the model picker,
// it's allowed"). It is the provider's composed list after a `models` `only`
// (model-lists-and-pickers.md OQ-BR12), which the launch marks `models_only` on the composed
// entry, the same list every picker renders. A list no `only` narrowed refuses nothing: whether it
// should on a gateway that serves more is OQ-MM3's, open.
//
// THE SWITCH is the profile's `enforce_models`, on unless the profile says false (MM-D5, MM-D13),
// the one switch that governs every refusal yolo installs. Off, the list only shapes the menus.
//
// PER ROUTE (WG-I40). A via route is one agent's, so its own profile's switch decides. The adapter
// route is shared by every agent that reaches the provider there (claude and copilot both speak
// anthropic), and the bridge cannot tell their requests apart, so it refuses only when every such
// agent's profile has the switch on and none is exempt: an off switch any of them has must stay
// reachable ("No refusal ships before its off switch does").
//
// EXEMPT AGENTS (WG-I41). Before the default-on refusal applies to an agent, its background
// traffic has to be on the list (§14.3): claude's is, because yolo pins every tier to it (MM-D2);
// codex's review and memories models and copilot's background ids are not. An agent whose pack
// says so (`unlisted_background_models`) is admitted every model, and an off-list one is logged,
// never refused. The daemon reads that declaration from the jail's staged pack tree (WG-I42),
// since the tables it boots from carry no pack facts; a tree it cannot read refuses nothing.
//
// THE REFUSAL (WG-I43) is a 400 invalid_request_error in the route's protocol, Anthropic's on the
// adapter route and OpenAI's on a via route, naming the model, the provider, the list and the
// switch. A request body that names `model` twice is refused too: the bridge reads one and the
// provider might read the other.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// modelAllowlist is one route's refusal: the provider's narrowed list and whom it binds.
type modelAllowlist struct {
	provider string
	// profiles are the profiles whose switch put the list in force, for the refusal's remedy.
	profiles []string
	ids      map[string]bool
	order    []string
	// exempt names the agents the list admits every model for (WG-I41), "" when none: the
	// route then logs an off-list model instead of refusing it.
	exempt string
}

// narrowedList reads a composed provider entry's list when a `models` `only` narrowed it
// (packload's `models_only` mark), keyed by wire id with Claude's [1m] client suffix trimmed,
// as the Messages pass-through keys it (WG-I34). ok is false for a list no `only` narrowed.
func narrowedList(entry *jsonx.OrderedMap) (ids map[string]bool, order []string, ok bool) {
	if entry == nil {
		return nil, nil, false
	}
	if v, _ := entry.Get(packload.ModelsOnlyKey); v != true {
		return nil, nil, false
	}
	ids = map[string]bool{}
	if mv, _ := entry.Get("models"); mv != nil {
		if models, _ := mv.(*jsonx.OrderedMap); models != nil {
			for _, alias := range models.Keys() {
				raw, _ := models.Get(alias)
				s, _ := raw.(string)
				if id := strings.TrimSuffix(s, oneMillionSuffix); id != "" && !ids[id] {
					ids[id] = true
					order = append(order, id)
				}
			}
		}
	}
	sort.Strings(order)
	return ids, order, true
}

// checks answers one request: whether it may go upstream, and the refusal's message when not.
// body is the request body as sent; a body naming no model (a GET, /models) is not the list's.
func (a *modelAllowlist) checks(agentOrRoute string, body []byte) (ok bool, msg string) {
	if a == nil {
		return true, ""
	}
	model, dup, named := requestModel(body)
	if !named {
		return true, ""
	}
	listedID := a.ids[strings.TrimSuffix(model, oneMillionSuffix)]
	if a.exempt != "" {
		// Admitted whatever it names, and said when that is off the list: the evidence a later
		// build needs to put the exempt agent's background ids on it.
		if dup || !listedID {
			logf("%s: admitted model %q, which is not on provider %s's list, because %s's pack declares "+
				"unlisted_background_models (wire-bridge-gateway.md WG-I41)", agentOrRoute, model, a.provider, a.exempt)
		}
		return true, ""
	}
	if dup {
		logf("%s: refused a request naming \"model\" twice, while provider %s's list is enforced", agentOrRoute, a.provider)
		return false, fmt.Sprintf("wire-bridge: the request names \"model\" more than once, so the bridge "+
			"cannot tell which one provider %s would run, and provider %s's list is enforced", a.provider, a.provider)
	}
	if listedID {
		return true, ""
	}
	listed := "none: the list is empty"
	if len(a.order) > 0 {
		listed = strings.Join(a.order, ", ")
	}
	logf("%s: refused model %q, which is not on provider %s's list (wire-bridge-gateway.md Part 5)",
		agentOrRoute, model, a.provider)
	return false, fmt.Sprintf("wire-bridge: model %q is not on provider %s's list, and profile %s enforces "+
		"it (enforce_models is on by default). Allowed: %s. Pick one of those, or set \"enforce_models\": false "+
		"on the profile so the list only shapes the model menus", model, a.provider,
		strings.Join(quoteAll(a.profiles), ", "), listed)
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// requestModel reads a JSON body's top-level "model": its value, whether the key appeared more
// than once (dup), and whether it appeared at all (named). A body that is not a JSON object names
// no model here; the upstream refuses it as it always has.
func requestModel(body []byte) (model string, dup, named bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", false, false
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return model, dup, named
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return model, dup, named
		}
		if key != "model" {
			continue
		}
		if named {
			return model, true, true
		}
		named = true
		_ = json.Unmarshal(v, &model)
	}
	return model, dup, named
}

// allowlistPlan is every route's allowlist for one boot, computed from the tables the plan was
// read from and the pack tree.
type allowlistPlan struct {
	adapter *modelAllowlist
	via     map[string]*modelAllowlist
}

// allowlistsFor computes the plan's allowlists (WG-I40): a via route's from its agent's own
// profile, the adapter route's from every agent that reaches its provider there. The pack tree is
// read only when some route's provider list is narrowed, and a tree that cannot be read refuses
// nothing, which the log says.
func allowlistsFor(p plan, providers *jsonx.OrderedMap, useProfiles map[string]string,
	resolved map[string]packload.ResolvedProfile, e *entrypoint.Env) allowlistPlan {
	var out allowlistPlan
	narrowed := func(provider string) bool {
		_, _, ok := narrowedList(providerEntry(providers, provider))
		return ok
	}
	need := p.adapter != nil && narrowed(p.adapter.ProviderName)
	for _, r := range p.via.Routes {
		need = need || narrowed(r.ProviderName)
	}
	if !need {
		return out
	}
	// THE JAIL'S STAGED PACK TREE (WG-I42): the one copy of the selected packs' facts in the jail,
	// which the boot rendered every surface from.
	packs, err := entrypoint.LoadJailPacks(e)
	if err != nil || len(packs) == 0 {
		why := "no pack tree was delivered"
		if err != nil {
			why = err.Error()
		}
		logf("a provider's model list is narrowed, but the bridge cannot read which agents send models "+
			"off it (%s), so it refuses no model on any route (wire-bridge-gateway.md WG-I42)", why)
		return out
	}
	exempt := func(agent string) bool {
		for _, pk := range packs {
			if pk != nil && pk.Decl != nil && pk.Decl.SendsUnlistedModels(agent) {
				return true
			}
		}
		return false
	}
	if p.adapter != nil && narrowed(p.adapter.ProviderName) {
		out.adapter = adapterAllowlist(*p.adapter, providers, useProfiles, resolved, packs, exempt)
	}
	for _, r := range p.via.Routes {
		if !narrowed(r.ProviderName) {
			continue
		}
		profile := useProfiles[r.Agent]
		if !packload.ModelsEnforced(resolved[profile]) {
			continue
		}
		ids, order, _ := narrowedList(providerEntry(providers, r.ProviderName))
		a := &modelAllowlist{provider: r.ProviderName, profiles: []string{profile}, ids: ids, order: order}
		if exempt(r.Agent) {
			a.exempt = r.Agent
		}
		if out.via == nil {
			out.via = map[string]*modelAllowlist{}
		}
		out.via[r.Agent] = a
	}
	return out
}

// adapterAllowlist is the adapter route's allowlist, or nil when any agent sharing the route has
// the switch off (WG-I40). The sharers are the agents whose active profile selects the route's
// provider and that reach it at its anthropic endpoint, which a via agent does not (it rides its
// via route) and nobody reaches through an address composed for a via on a profile without one.
func adapterAllowlist(rt route, providers *jsonx.OrderedMap, useProfiles map[string]string,
	resolved map[string]packload.ResolvedProfile, packs []*packload.Pack, exempt func(string) bool) *modelAllowlist {
	entry := providerEntry(providers, rt.ProviderName)
	ids, order, _ := narrowedList(entry)
	a := &modelAllowlist{provider: rt.ProviderName, ids: ids, order: order}
	var exempts []string
	agents := make([]string, 0, len(useProfiles))
	for agent := range useProfiles {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	for _, agent := range agents {
		profile := useProfiles[agent]
		r := resolved[profile]
		if packload.ProviderFor(resolved, profile) != rt.ProviderName {
			continue
		}
		if _, protocol, viaAgent := preferredViaWire(packs, agent); viaAgent || protocol == "" {
			continue
		}
		if forViaEndpoint(entry, "anthropic") && r.Via != ServiceName {
			continue
		}
		if !packload.ModelsEnforced(r) {
			logf("provider %s's list is narrowed, and profile %q (active for %s) turns enforce_models off, so the "+
				"adapter route, which %s shares, refuses no model", rt.ProviderName, profile, agent, agent)
			return nil
		}
		a.profiles = appendUnique(a.profiles, profile)
		if exempt(agent) {
			exempts = append(exempts, agent)
		}
	}
	if len(a.profiles) == 0 {
		return nil
	}
	a.exempt = strings.Join(exempts, ", ")
	return a
}

func appendUnique(ss []string, s string) []string {
	for _, have := range ss {
		if have == s {
			return ss
		}
	}
	return append(ss, s)
}

// providerEntry is the composed table's entry for name, nil when absent or malformed.
func providerEntry(providers *jsonx.OrderedMap, name string) *jsonx.OrderedMap {
	if providers == nil {
		return nil
	}
	v, _ := providers.Get(name)
	entry, _ := v.(*jsonx.OrderedMap)
	return entry
}

// allowlistNote is the serve line's phrase for a route's allowlist, "" for none.
func allowlistNote(a *modelAllowlist) string {
	if a == nil {
		return ""
	}
	if a.exempt != "" {
		return fmt.Sprintf("; provider %s's list is narrowed, and %s sends models off it, so an off-list model "+
			"is logged, not refused", a.provider, a.exempt)
	}
	return fmt.Sprintf("; a model off provider %s's list (%d models) is refused", a.provider, len(a.order))
}
