package config

import (
	"fmt"
	"net/url"
	"slices"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func validateProviders(config *jsonx.OrderedMap, workspace string, errs, warns *[]string) {
	// ABOVE the presence gate, not below it. The scope check reads the workspace FILE and
	// the gate below asks the MERGED map — normally the same answer, since a workspace
	// `providers` key survives the merge, but "normally" is not a security boundary: any
	// caller handing this a map that was filtered, snapshotted or hand-built would switch
	// the refusal off without switching the feature off. Measured while writing the test
	// that pins it, with an empty merged map.
	validateProviderCredentialScope(workspace, errs)
	validateProviderEntries(config, errs, warns)
}

// ValidateProviderSection is ValidateConfig's provider and profile section over one config
// with no workspace in it: the `providers` entries, the retired `agent_profiles` and
// `use_profiles` keys, the `profile` selection, and the user file's `profiles` and `adapters`, in ValidateConfig's
// order and words. `yolo host` composes from user scope alone (UserScopeConfig) and runs no
// ValidateConfig, so it composed a provider written with removed keys and sent claude to its
// first-party endpoint with a model named `m1`, where every jail launch and `yolo check`
// refuse the same user file (docs/plans/notch-convergence.md item 13, row A8). The
// workspace-scope refusals are not here: the host reads no workspace config, so a workspace
// file cannot change what it composes.
func ValidateProviderSection(config *jsonx.OrderedMap) (errors []string, warnings []string) {
	errs, warns := &[]string{}, &[]string{}
	validateProviderEntries(config, errs, warns)
	validateAgentProfilesRetired(config, errs, warns)
	validateUseProfilesRetired(config, errs, warns)
	validateProfile(config, errs)
	validateUserScopeProfiles(errs)
	validateUserScopeAdapters(errs)
	return *errs, *warns
}

// validateProviderEntries is validateProviders' shape half: every `providers.<name>` entry.
func validateProviderEntries(config *jsonx.OrderedMap, errs, warns *[]string) {
	sharedNames := validateModelFacts(config, errs)
	v, present := config.Get(providersKey)
	if !present || v == nil {
		return
	}
	providers, ok := asMap(v)
	if !ok {
		add(errs, "config.providers: expected an object")
		return
	}
	for _, name := range providers.Keys() {
		cfgV, _ := providers.Get(name)
		path := "config.providers." + name
		if cfgV == nil {
			continue // null disables or drops
		}
		cfg, ok := asMap(cfgV)
		if !ok {
			add(errs, path+": expected an object or null")
			continue
		}
		cfg = splitReadableModels(name, cfg, errs)
		reportUnknownKeys(cfg, knownProviderKeys, path, errs)
		if base, has := cfg.Get("base_url"); has && base != nil {
			// REMOVED (protocol-resolution.md), and this is the entry's ONLY message
			// about that key: the checks below would each ask the user to fix the shape of
			// something they must delete.
			validateProviderShorthandRetired(path, errs, warns)
			continue
		}
		endpoints, hasEndpoints := cfg.Get("endpoints")
		if hasEndpoints && endpoints != nil {
			validateProviderEndpoints(endpoints, path, errs)
		}
		if w, ok := cfg.Get("wire_api"); ok && w != nil {
			validateWireAPI(w, path+".wire_api", errs)
		}
		if r, ok := cfg.Get("region"); ok && r != nil {
			validateProviderRegion(r, path+".region", errs)
		}
		// What service the provider is (OQ-BR2, docs/design/providers-and-profiles-redesign.md):
		// the shape rule is packdecl's, so a user's own provider and a pack's cannot accept
		// different spellings of one field of one composed table. The vocabulary is open, and
		// an unknown value is inert, which is why nothing here checks it against a list.
		if pl, ok := cfg.Get("platform"); ok && pl != nil {
			if s, isString := asStr(pl); !isString {
				add(errs, path+".platform: expected a string")
			} else if prob := packdecl.PlatformProblem(path+".platform", s); prob != "" {
				add(errs, prob)
			}
		}
		if a, ok := cfg.Get("api_key_env_name"); ok && a != nil {
			// One variable name, or a list of them (OQ-CN1,
			// docs/reference/providers.md). The rule is packdecl's, so a pack
			// manifest and this entry cannot accept different values for one field of one
			// composed table. A lone string keeps its old wording, which tests and users
			// already know.
			names, ok := packdecl.EnvNamesFromValue(a)
			switch {
			case !ok:
				add(errs, fmt.Sprintf("%s.api_key_env_name: expected an env var name or a "+
					"non-empty list of them, got %s", path, pyReprValue(a)))
			case isStr(a):
				if !envVarNameRe.MatchString(names[0]) {
					add(errs, fmt.Sprintf("%s.api_key_env_name: invalid env var name %s (must match [A-Za-z_][A-Za-z0-9_]*)",
						path, pyReprValue(a)))
				}
			default:
				for _, p := range names.Problems(path) {
					add(errs, p)
				}
			}
		}
		if m, ok := cfg.Get("models"); ok && m != nil {
			models, ok := asMap(m)
			if !ok {
				add(errs, path+".models: expected an object")
			} else {
				for _, alias := range models.Keys() {
					val, _ := models.Get(alias)
					if val == nil {
						continue // null deletes the alias, the merge-patch convention
					}
					if isStr(val) {
						continue // the shorthand: alias -> wire id
					}
					entry, ok := asMap(val)
					if !ok {
						add(errs, path+".models."+alias+": expected a model id string or an object")
						continue
					}
					validateModelEntry(entry, path+".models."+alias, sharedNames, errs)
				}
			}
		}
		if o, ok := cfg.Get("options"); ok && o != nil {
			validateProviderOptions(o, path+".options", errs)
		}
		if c, ok := cfg.Get("capabilities"); ok && c != nil {
			validateStringList(c, path+".capabilities", errs)
		}
	}
}

// validateProviderRegion checks one `providers.<name>.region` value: a string shaped like one DNS
// label (packdecl.RegionProblem). Both passes of validateProviders call it, the workspace file's
// and the merged map's, so a workspace region is reported by whichever sees it first and not
// twice: the refusal is one line about one field.
func validateProviderRegion(v any, path string, errs *[]string) {
	s, ok := asStr(v)
	msg := path + ": expected a string"
	if ok {
		msg = packdecl.RegionProblem(path, s)
	}
	if msg == "" || slices.Contains(*errs, msg) {
		return
	}
	add(errs, msg)
}

// validateModelEntry checks one OBJECT-form `models.<alias>` entry: the closed field set,
// the required wire `id`, and each field's JSON type. A string value is the shorthand and
// never reaches here.
//
// `id` is required rather than inferred from the alias because the merge REPLACES: the
// moment a user's value is an object, a pack's id string under the same alias is gone, so
// an inferred `id = alias` would silently rewrite the wire id (cerebras' `default` ->
// qwen-3.8-27b) the first time someone added one fact. What a fact MEANS is still the
// consuming derive's business; core checks only that it is shaped like one. ⚠ That is NOT
// the stance docs/reference/providers.md §"Profiles and options" takes for a provider's flat
// `options` map, where core validates no value at all: a model FACT is shape-checked right
// here — `input` against a closed vocabulary, `cost` rates non-negative,
// `context_window`/`max_tokens` positive — and only its interpretation is left alone. The
// two surfaces differ on purpose (see knownModelKeys in config.go).
//
// A fact written as null is a TOMBSTONE (docs/design/model-lists-and-pickers.md ML-D10), accepted
// at every fact and carried to the derives as a removal (packload.liftModelFacts). `extends`
// names the top-level `model_facts` entry this model's routes share (OQ-ML5); sharedNames is
// that map's names, and a name it does not hold is refused, since the row would silently lose
// every fact it meant to share.
func validateModelEntry(entry *jsonx.OrderedMap, path string, sharedNames map[string]bool, errs *[]string) {
	keys := append([]string(nil), entry.Keys()...)
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := knownModelKeys[key]; ok {
			continue
		}
		if hint, ok := unknownModelKeyHints[key]; ok {
			add(errs, path+"."+key+": unknown key — "+hint)
			continue
		}
		add(errs, path+"."+key+": unknown key")
	}
	id, hasID := entry.Get("id")
	switch {
	case !hasID || id == nil:
		add(errs, path+".id: required — the wire model id, which is usually not the alias")
	default:
		if s, ok := asStr(id); !ok || s == "" {
			add(errs, path+".id: expected a non-empty string")
		}
	}
	if v, ok := entry.Get("extends"); ok {
		switch s, isString := asStr(v); {
		case !isString || s == "":
			add(errs, path+".extends: expected the name of a `model_facts` entry, the facts this "+
				"model's routes share")
		case !sharedNames[s]:
			add(errs, fmt.Sprintf("%s.extends: names %q, which config.model_facts does not declare — "+
				"declare it there, or remove `extends`", path, s))
		}
	}
	validateModelFactValues(entry, path, errs)
}

// validateModelFacts checks the top-level `model_facts` map (docs/design/model-lists-and-pickers.md
// OQ-ML5): each entry the facts a model's routes share, in the object-form model entry's
// vocabulary minus what only a route can say (`id`, `vendor`, `wire_id`, `extends`),
// a null fact being a tombstone (ML-D10). It returns the declared names, for `extends`.
func validateModelFacts(config *jsonx.OrderedMap, errs *[]string) map[string]bool {
	names := map[string]bool{}
	v, present := config.Get(modelFactsKey)
	if !present || v == nil {
		return names
	}
	facts, ok := asMap(v)
	if !ok {
		add(errs, "config."+modelFactsKey+": expected an object")
		return names
	}
	for _, name := range facts.Keys() {
		path := "config." + modelFactsKey + "." + name
		ev, _ := facts.Get(name)
		if ev == nil {
			continue // null deletes the entry, the merge-patch convention
		}
		entry, ok := asMap(ev)
		if !ok {
			add(errs, path+": expected an object")
			continue
		}
		names[name] = true
		routeOnly := false
		for _, key := range packdecl.ModelFactsRouteOnlyKeys {
			if _, has := entry.Get(key); has {
				routeOnly = true
				add(errs, fmt.Sprintf("%s.%s: only a route says %s; a model_facts entry says only what every "+
					"route of the model shares, so write it on the row that `extends` this entry", path, key, key))
			}
		}
		if routeOnly {
			continue
		}
		// The readable spelling (MF-D2) is checked by the decoder a row and `every_model` take;
		// the legacy one by its own shape check, which accepts the same values for the keys the
		// two spellings share.
		if packdecl.IsReadableModelFacts(entry) {
			if _, err := packdecl.DecodeModelFacts(name, entry); err != nil {
				add(errs, "config."+err.Error())
			}
			continue
		}
		reportUnknownKeys(entry, knownSharedModelKeys, path, errs)
		validateModelFactValues(entry, path, errs)
	}
	return names
}

// validateModelFactValues is the per-fact shape check one model entry and one `model_facts`
// entry share: a null skips, being a tombstone (ML-D10).
func validateModelFactValues(entry *jsonx.OrderedMap, path string, errs *[]string) {
	for _, key := range []string{"name", "description"} {
		if v, ok := entry.Get(key); ok && v != nil {
			if s, isString := asStr(v); !isString || s == "" {
				add(errs, path+"."+key+": expected a non-empty string")
			}
		}
	}
	if v, ok := entry.Get("vendor"); ok && v != nil {
		if s, isString := asStr(v); !isString || !packdecl.ValidModelVendor(s) {
			add(errs, path+".vendor: expected the model's maker as one lowercase token, "+
				`such as "anthropic" or "openai"`)
		}
	}
	if v, ok := entry.Get("reasoning"); ok && v != nil {
		if !isBool(v) {
			add(errs, path+".reasoning: expected a boolean")
		}
	}
	if v, ok := entry.Get("input"); ok && v != nil {
		validateModelInput(v, path+".input", errs)
	}
	if v, ok := entry.Get("cost"); ok && v != nil {
		validateModelCost(v, path+".cost", errs)
	}
	for _, key := range []string{"context_window", "max_tokens"} {
		if v, ok := entry.Get(key); ok && v != nil {
			if !positiveNumber(v) {
				add(errs, path+"."+key+": expected a positive number")
			}
		}
	}
	// `base` names the pi CATALOG model this row inherits facts from (docs/reference/
	// providers.md §"Per-model OpenRouter routing"): a non-empty wire id, checked here for
	// the same reason `id` is — a variant id the catalog lacks would otherwise lose every
	// catalog fact silently, and pi's menu would show a bare default row.
	// `wire_id` is the id the row sends where `id` is a client spelling (knownModelKeys). Only a
	// route's entry reaches here with it: `model_facts` refuses it as an unknown key, a wire id
	// being a route's and never a fact every route of a model shares.
	for _, key := range []string{"base", "wire_id"} {
		if v, ok := entry.Get(key); ok && v != nil {
			if s, isString := asStr(v); !isString || s == "" {
				add(errs, path+"."+key+": expected a non-empty model id string")
			}
		}
	}
	// `openrouter_routing` is the one OBJECT-valued model fact, because it is sent VERBATIM as
	// OpenRouter's request `provider` field (packs/pi/home/.pi/agent/extensions/yolo-model-lists.js). Core
	// checks the object shape only; pi's own openRouterRouting schema is the content's
	// authority, and transcribing it here would be a second copy to keep true. A `model_facts`
	// entry carries it too, with the same check and the same message.
	if v, ok := entry.Get("openrouter_routing"); ok && v != nil {
		if _, isMap := asMap(v); !isMap {
			add(errs, path+".openrouter_routing: expected an object (sent verbatim as OpenRouter's request `provider` field)")
		}
	}
}

// validateModelInput checks the modality list: a non-empty array of "text"/"image". The
// vocabulary is closed because pi's schema is (`Type.Array(Type.Union([text, image]))`),
// and an accepted modality nothing consumes would be the same silent drop one level down.
func validateModelInput(v any, path string, errs *[]string) {
	list, ok := asList(v)
	if !ok {
		add(errs, path+": expected an array of \"text\" and/or \"image\"")
		return
	}
	if len(list) == 0 {
		add(errs, path+": expected at least one of \"text\", \"image\"")
		return
	}
	for _, item := range list {
		if s, ok := asStr(item); !ok || (s != "text" && s != "image") {
			add(errs, path+": expected \"text\" or \"image\", got "+pyReprValue(item))
		}
	}
}

// validateModelCost checks the cost object: the closed rate set, every rate present and
// non-negative. All four are required because pi's ModelCostSchema requires all four and
// discards the WHOLE models.json on one missing — a partial object here would compose a
// file pi refuses, which is worse than making the author state a zero.
func validateModelCost(v any, path string, errs *[]string) {
	cost, ok := asMap(v)
	if !ok {
		add(errs, path+": expected an object")
		return
	}
	reportUnknownKeys(cost, knownModelCostKeys, path, errs)
	for _, key := range []string{"input", "output", "cache_read", "cache_write"} {
		val, present := cost.Get(key)
		if !present || val == nil {
			add(errs, path+"."+key+": required (the agent cost schema needs all four rates)")
			continue
		}
		if !nonNegativeNumber(val) {
			add(errs, path+"."+key+": expected a non-negative number")
		}
	}
}

// positiveNumber and nonNegativeNumber accept a decoded JSON integer (jsonInt) or float and
// deliberately not a bool — jsonx decodes true/false to a Go bool, so a predicate that
// accepted one would pass a value the reader drops, the silent drop routeWarnThreshold's
// own comment names.
func positiveNumber(v any) bool {
	if n, ok := jsonx.AsInt(v); ok {
		return n > 0
	}
	if f, ok := v.(float64); ok {
		return f > 0
	}
	return false
}

func nonNegativeNumber(v any) bool {
	if n, ok := jsonx.AsInt(v); ok {
		return n >= 0
	}
	if f, ok := v.(float64); ok {
		return f >= 0
	}
	return false
}

// validateProviderCredentialScope refuses, at workspace scope, every provider field that
// decides where a credential goes (docs/plans/notch-convergence.md OQ-NC6, ruled 2026-09-28,
// the field list NC-D63). It began as OQ-LM3's address rule (docs/reference/providers.md#oq-lm3):
// `endpoints.<protocol>.base_url` in a workspace `yolo-jail.jsonc` is an error naming the user
// config. OQ-NC6 applied the same reason, that the blast radius is total, to the rest of the
// routing:
//
//   - `api_key_env_name` names the variable the provider CLAIMS (packload's credentialClaims),
//     and the credential gate hands a claimed value only to the agents on that provider, which
//     send it upstream. A workspace value re-points the claim — `"GH_TOKEN"` makes the user's
//     GitHub token zai's, sent to z.ai — and a null unclaims the provider's own key, which then
//     reaches every process in the launch.
//   - `endpoints`, in ANY form. A URL decides where the key goes; so does a protocol key with no
//     URL, which an agent pairs with and then sends the key to its own default service (claude's
//     derive treats an entry naming no anthropic address as a first-party launch), and so does a
//     null removing an endpoint or the map. `wire_api` rides inside the same object and goes with
//     it: allowing a dialect while refusing its neighbours would make the rule depend on which
//     protocols a pack ships, which validation does not resolve.
//   - A null provider, or a null `providers`: removing a provider removes its claims, and the
//     unclaimed values reach every process.
//   - `platform` (OQ-BR2), which an `env` contribution's platform gate keys on, so it decides
//     which agents receive a pack's credential pointer (aws-auth's, for "aws-bedrock"): a
//     workspace value relabelling a provider an agent already selects would hand that agent
//     the pointer.
//
// `models`, `options`, `region` and `capabilities` still merge, deliberately: they steer a
// request that still goes to the provider the credential was issued for, and a workspace that
// pins a model alias for its own repo is what the key is ordinarily for. ⚠ `region` holds that
// only because of its SHAPE: the agents build their service's host name from it, so a value that
// is not one DNS label is refused here too (validateProviderRegion), and the one a workspace may
// write can then name only a region of the domain the agent appends. NC-D63 judged the field to
// route no credential before that was read (docs/plans/notch-convergence.md).
//
// It is the same boundary `profiles`/`profile` draw one step further in (profiles.go's
// userScopeOnlyMessage, OQ-CS5): a profile decides WHICH declared provider an agent talks to,
// and these fields decide where that provider is and what it is handed. The workspace file
// travels with the repo and is writable by the agent running inside the jail.
//
// It reads the WORKSPACE config directly rather than the merged map, for validatePacks'
// reason: in the merged map an entry from either scope looks identical, and only the
// workspace one is wrong.
func validateProviderCredentialScope(workspace string, errs *[]string) {
	wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil || wsCfg == nil {
		return
	}
	validateProviderCredentialScopeConfig(wsCfg, errs)
}

func validateProviderCredentialScopeConfig(wsCfg *jsonx.OrderedMap, errs *[]string) {
	v, present := wsCfg.Get(providersKey)
	if !present {
		return
	}
	if v == nil {
		add(errs, providerCredentialScopeMessage("config."+providersKey, "as null it removes "+
			"every provider your user config declares, and with them their claims on their "+
			"credential variables, whose values then reach every process in the launch."))
		return
	}
	providers, ok := asMap(v)
	if !ok {
		return // shape problems are the merged pass's to report, once
	}
	for _, name := range providers.Keys() {
		entryV, _ := providers.Get(name)
		path := "config." + providersKey + "." + name
		if entryV == nil {
			add(errs, providerCredentialScopeMessage(path, "as null it removes the provider, "+
				"and with it its claim on its credential variables, whose values then reach "+
				"every process in the launch."))
			continue
		}
		entry, ok := asMap(entryV)
		if !ok {
			continue
		}
		// `platform` decides who receives a credential too: a provider's platform is what an
		// `env` contribution's platform gate keys on, so aws-auth's credentials pointer reaches
		// every agent whose selected provider says it is Bedrock (OQ-BR8). A workspace value
		// could relabel a provider an agent already selects and hand that agent the pointer.
		if _, has := entry.Get("platform"); has {
			add(errs, providerCredentialScopeMessage(path+".platform", "it says what service "+
				"this provider is, and a pack's credential pointer for a platform (aws-auth's, for "+
				"aws-bedrock) reaches every agent whose selected provider declares it; a workspace "+
				"value could relabel a provider so that its agents receive a credential they were "+
				"never given, or, as null, withhold one."))
		}
		// `region` MERGES from a workspace, but only as a region: every agent that reads it builds
		// its service's host name from it (packdecl.RegionProblem), so a repo file writing
		// "attacker.example/#" there would choose the host every prompt and the provider's
		// credential go to. Checked here, on the workspace file itself, and not only by the merged
		// pass below, for this function's reason: the merged map is not the security boundary.
		if r, has := entry.Get("region"); has && r != nil {
			validateProviderRegion(r, path+".region", errs)
		}
		if _, has := entry.Get("api_key_env_name"); has {
			add(errs, providerCredentialScopeMessage(path+".api_key_env_name", "it names the "+
				"variable this provider claims, whose value only the agents on this provider "+
				"receive and send upstream; a workspace value could claim another credential "+
				"(GH_TOKEN, say) for this provider, or, as null, unclaim the provider's own key "+
				"so that it reaches every process."))
		}
		// The bare `base_url` SHORTHAND is deliberately not checked here: it is REMOVED
		// (validateProviderShorthandRetired), and telling someone their deleted key is in the
		// wrong scope is two contradictory instructions about one line — the same call
		// validateHostProcessesRetired makes about its own type checks.
		if endpointsV, has := entry.Get("endpoints"); has {
			validateEndpointsScope(path+".endpoints", endpointsV, errs)
		}
	}
}

// validateEndpointsScope is validateProviderCredentialScope for one workspace `endpoints`
// value: every path under it is named, at the deepest level it was written, so a config fixed
// one error at a time does not re-refuse on the next launch. A URL keeps OQ-LM3's own message.
func validateEndpointsScope(path string, v any, errs *[]string) {
	const why = "the endpoints map is where this provider's credential is sent: a URL, a " +
		"protocol an agent pairs with (one with no URL leaves the agent sending this " +
		"provider's key to its own default service), a dialect, or an endpoint removed."
	endpoints, ok := asMap(v)
	if !ok || endpoints.Len() == 0 {
		add(errs, providerCredentialScopeMessage(path, why))
		return
	}
	for _, proto := range endpoints.Keys() {
		epV, _ := endpoints.Get(proto)
		ep, ok := asMap(epV)
		if !ok || ep.Len() == 0 {
			add(errs, providerCredentialScopeMessage(path+"."+proto, why))
			continue
		}
		for _, key := range ep.Keys() {
			if key == "base_url" {
				add(errs, providerAddressScopeMessage(path+"."+proto+".base_url"))
				continue
			}
			add(errs, providerCredentialScopeMessage(path+"."+proto+"."+key, why))
		}
	}
}

// validateProviderShorthandRetired reports the DELETED single-protocol `base_url`
// shorthand on a provider entry (protocol-resolution.md).
//
// It is the THIRD member of the retired-declaration set — `journal` and `host_processes`
// above are the top-level keys, `packdecl.retiredKinds` is the contribution kinds — and it
// lands the same way for a reason of its own: THE SAME VALUE MEANT TWO DIFFERENT THINGS.
// pi's derive read a bare `base_url` as its OPENAI url; claude's read the same field as its
// ANTHROPIC base URL (added 2026-09-16). One line of user config, two contradictory
// readings, decided by whichever agent happened to consume it — and the headline use case
// was the worst of them, because llama.cpp, ollama and vLLM all speak OpenAI, so `claude`
// plus a bare URL pointed ANTHROPIC_BASE_URL at an OpenAI server and failed at the first
// request.
//
// A REFUSAL, NOT A WARNING, AND NOT SILENCE — the three-way choice its two siblings
// describe, with the sharpest version of their argument: this key decides WHERE AN AGENT'S
// INFERENCE GOES. A config that still writes it and gets nothing has been silently
// redirected back to a first-party API, which is the one direction where silence reads as
// "it worked".
//
// IT FINISHES OQ-PT2 RATHER THAN REVERSING IT. That ruling already refused `base_url`
// beside `endpoints`, because the shorthand is the ambiguous spelling the moment more than
// one protocol exists; §5 observes that more than one protocol always exists now, so there
// is no case left where the short form is unambiguous.
//
// TYPE AND SCOPE CHECKS WENT WITH THE KEY. The URL-shape check, the base_url/endpoints
// conflict and the user-scope refusal all used to fire on this field; telling someone their
// removed key is malformed, or in the wrong file, is two contradictory instructions about
// one line.
//
// ERROR ON THE HOST, WARNING INSIDE A JAIL, for validateJournalRetired's reason: in-jail
// the config is the HOST-GENERATED snapshot, so erroring there refuses every nested launch
// over a key the in-jail user cannot fix at its source.
func validateProviderShorthandRetired(path string, errs, warns *[]string) {
	msg := path + ".base_url: REMOVED — say which protocol this URL speaks:\n" +
		"      endpoints.anthropic.base_url   the Anthropic Messages wire\n" +
		"      endpoints.openai.base_url      the OpenAI-client wire\n" +
		"    The bare field meant different protocols to different agents — pi read it as " +
		"openai and claude as anthropic — so one line pointed two agents at two different " +
		"services, and a local llama.cpp/ollama/vLLM endpoint (all OpenAI-speaking) sent " +
		"claude to an address it could not talk to. Naming the protocol also makes the " +
		"launch able to ROUTE it: an openai endpoint reaches an anthropic-speaking agent " +
		"through whichever selected pack declares that adaptation, and a pairing nothing " +
		"can serve is refused by name instead of failing at the first request."
	if inJail() {
		add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
			"so remove the key from the HOST config.)")
		return
	}
	add(errs, msg)
}

// providerAddressScopeMessage is the ONE refusal both address spellings give, so the
// shorthand and the per-protocol form cannot word the same rule differently — the reason
// packdecl.ProviderAddressConflictMessage is one constant and not two literals.
func providerAddressScopeMessage(path string) string {
	return path + ": user-scope only — move it to " + paths.UserConfigDisplayPath() +
		". A workspace config travels with the repo and is agent-editable, and this is the " +
		"field that decides WHERE an agent's inference goes: every prompt, every file the " +
		"agent has read, and every credential hydrated for this provider are sent to this " +
		"address. " + providerMergesSentence
}

// providerCredentialScopeMessage is the workspace-scope refusal of a provider field that
// decides where a credential goes (OQ-NC6): the field's path, the file to move it to, and why
// this one routes a credential.
func providerCredentialScopeMessage(path, why string) string {
	return path + ": user-scope only — move it to " + paths.UserConfigDisplayPath() +
		". A workspace config travels with the repo and is agent-editable, and this field " +
		"decides where a provider's credential goes: " + why + " " + providerMergesSentence
}

// providerMergesSentence is what a workspace may still write in a provider entry, the same
// sentence at the end of every refusal above.
const providerMergesSentence = "Only models, options, region and capabilities merge from a " +
	"workspace config."

// providerURLProblem returns what is wrong with a provider base_url, or "" when it is a
// usable address: it must parse as an http or https URL and carry no userinfo.
//
// The userinfo half is the credential rule (docs/reference/providers.md#principles):
// `https://user:tok@host/v1` is a credential in a git-tracked config file, and this rule
// is the check. A base_url routes an ADDRESS; the credential travels by NAME through
// api_key_env_name and is hydrated from env_sources. The scheme half is the same rule
// pointed the other way — `file://` or a bare host is a fact about the local machine, not
// about a service. packdecl's validateProviderEndpoints is this rule for the manifest
// layer, where the file ships to strangers; here it stays on the machine, so only the
// wording differs.
func providerURLProblem(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return err.Error()
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Sprintf("must be an http or https URL (%s)", u)
	}
	if parsed.User != nil {
		return fmt.Sprintf("must not carry userinfo — %q is a credential in a git-tracked "+
			"file; name an env var in api_key_env_name and let env_sources hydrate it", u)
	}
	return ""
}

// validateProviderEndpoints checks one provider's per-protocol endpoint map: every
// protocol names an endpoint object, whose base_url obeys providerURLProblem and whose
// wire_api is in the closed vocabulary.
//
// The PROTOCOL names stay open. Which protocols any agent speaks is decided by the agent
// packs' own derives in their dialects, so
// a protocol nobody speaks yet is inert rather than invalid — closing the names here would
// make the config schema the one place a new protocol has to be registered, ahead of
// anything that could consume it. The VALUES are schema, and an unknown key inside an
// endpoint is refused for the same reason one at the provider level is: accepted one level
// down, it would be inert where the user meant it to work.
func validateProviderEndpoints(v any, path string, errs *[]string) {
	endpoints, ok := asMap(v)
	if !ok {
		add(errs, path+".endpoints: expected an object")
		return
	}
	for _, proto := range endpoints.Keys() {
		epPath := path + ".endpoints." + proto
		epV, _ := endpoints.Get(proto)
		if epV == nil {
			continue // null disables the protocol's endpoint
		}
		ep, ok := asMap(epV)
		if !ok {
			add(errs, epPath+": expected an object or null")
			continue
		}
		reportUnknownKeys(ep, knownEndpointKeys, epPath, errs)
		if u, ok := ep.Get("base_url"); ok && u != nil {
			if s, isStrOk := asStr(u); !isStrOk {
				add(errs, epPath+".base_url: expected a string")
			} else if problem := providerURLProblem(s); problem != "" {
				add(errs, epPath+".base_url: "+problem)
			}
		}
		if w, ok := ep.Get("wire_api"); ok && w != nil {
			validateWireAPI(w, epPath+".wire_api", errs)
		}
	}
}

// validateWireAPI checks the wire protocol a provider speaks. A closed vocabulary rather
// than a free string because the value is the CANONICAL name the derives translate from
// (docs/reference/providers.md — canonical vocabulary, OQ-PT1): a name outside the set translates to
// nothing, so it would reach every agent as no protocol at all — silently, from a jail
// that booted green. Rule 4, applied to a fixed slot.
//
// The set is not restated here. packdecl's KnownWireAPIs is the one vocabulary for both
// spellings of a provider — the entry a user writes and the entry a pack ships compose
// into the same table — so this pass asks for it rather than keeping a second copy that
// could drift away from the one the manifest layer enforces
// (packdecl.validateProviderEndpoints). That is the field's own contract:
// ProviderEndpoint.WireAPI's "the enum that tightens one tightens both".
func validateWireAPI(w any, path string, errs *[]string) {
	apis := packdecl.KnownWireAPIs()
	if !inStrList(apis, w) {
		add(errs, fmt.Sprintf("%s: expected one of %s (got %s)",
			path, pyListRepr(apis), pyReprValue(w)))
	}
}

// validateProviderOptions checks a provider's flat `options` map. `path` is the map's own
// full path (`config.providers.<name>.options`), so the diagnostic names the key the user
// wrote rather than a suffix this function assumes.
//
// Two checks, neither of which invents a vocabulary:
//
//   - the NAME must be non-empty. packdecl's validateContributions refuses the same
//     emptiness in a manifest, for the same reason — the name is the key every profile
//     for this provider is measured against, so an empty one declares a key no profile
//     can spell and the downstream refusal would quote it.
//   - the VALUE must be a string or a null, decided by packdecl.OptionDefaultFromValue
//     rather than by a local predicate. That is the field's own contract, the same one
//     validateWireAPI one function up records for wire_api: a pack's manifest and a
//     user's config entry compose into the SAME table entry, so what may sit in the map
//     cannot be two predicates without the two spellings drifting into accepting
//     different files. The message spells out what the null means, because it is the one
//     null in this config that is not the delete (docs/reference/providers.md §"How the
//     table composes", the options-map carve-out) — a reader meeting it in an error would
//     otherwise assume the merge-patch convention this key deliberately departs from.
//
// What is deliberately NOT checked: whether the value MEANS anything. Core learns no
// option names and validates no values — what `model` or `thinking` does is the agent
// pack's derive's business (docs/reference/providers.md §"Profiles and options": the
// option NAMES are the provider's declared set and no value validation happens in core),
// so an option no derive consumes composes to nothing rather than to an error.
func validateProviderOptions(v any, path string, errs *[]string) {
	opts, ok := asMap(v)
	if !ok {
		add(errs, path+": expected an object")
		return
	}
	for _, name := range opts.Keys() {
		val, _ := opts.Get(name)
		if name == "" {
			add(errs, path+": an option name cannot be empty")
			continue
		}
		if _, legal := packdecl.OptionDefaultFromValue(val); !legal {
			add(errs, path+"."+name+": expected a string or null (null declares the "+
				"option settable with no default — it is not the delete it is elsewhere in "+
				"this config)")
		}
	}
}
