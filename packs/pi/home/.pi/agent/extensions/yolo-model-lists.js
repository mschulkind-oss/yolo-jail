import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

// THE NARROWED LISTS ARE DATA. yolo renders every provider list a `models` contribution narrowed
// with an `only` into this file at every boot: the `pi/model-lists` surface packs/pi/pack.json
// declares, written by `yolo.derive("pi", "model-lists")` in packs/pi/derive.lua, keyed by pi's
// provider id (docs/design/model-lists-and-pickers.md MM-D6). The file sits beside extensions/,
// outside pi's extension discovery and outside this file's own read-only delivery, as the
// openai-codex list's does (yolo-openai-auth.js, ML-D3). A list a routing declaration carries is
// here too, and so are the rows of pi's own Bedrock client, marked `keep_catalog`: those complete
// pi's catalog for the provider rather than narrow it (withCatalog), because a models.json row
// replaces its catalog entry whole and only this extension can read the catalog to merge it
// (docs/design/model-lists-and-pickers.md OQ-ML4, ML-D12).
//
// WHY A REGISTRATION. pi builds a provider's list as its own catalog, then models.json rows,
// which add or replace by id and never remove, then an extension's registerProvider with
// `models`, which replaces the provider's whole list (pi 0.99.1 core/provider-composer.js;
// MEASURED on 0.87.1 and 0.99.1 with a library load). So only a registration can make a narrowed
// list pi's exact menu for a provider pi ships.
//
// TWO FORMS, chosen by the provider's `enforce` flag, which is the profile's enforce_models
// switch (MM-D5), on unless the profile says false, for a list an `only` narrowed, and false for
// every other list (OQ-MM3: a list no `only` narrowed refuses nothing):
//   - off: `models` alone, which keeps pi's own address, wire and credential for the provider
//     (MEASURED). The menu is exact, and `pi --model <provider>/<unlisted id>` still runs, with
//     pi's own warning.
//   - on: `models`, `api` and a `streamSimple` wrapper that REFUSES a model outside the list and
//     hands a listed one to the stream pi would have used without it. pi runs an extension's
//     streamSimple for every model of the registration's `api` (composeModelProvider's
//     streamWith), after it has resolved the credential into `options` (ModelRuntime's
//     prepareRequest), so the delegate below receives pi's own credential and passes it on
//     unchanged: MEASURED 2026-09-30 on pi 0.99.1's shipped bundle against mock endpoints, for a
//     Bedrock bearer token, Bedrock SigV4 keys and a models.json row's key (MM-D21). pi 0.99.1
//     refuses a streamSimple without `api` (validateExtensionProvider), hence the `api`.
//
// pi reads its state under PI_CODING_AGENT_DIR when that is set (a per-session home,
// docs/design/per-session-profiles.md SP-D9), so the list file is read there too, resolved the
// way pi resolves the variable: `~` and `~/...` against HOME, ~/.pi/agent when it is unset. The
// idiom is subagentConfigPath's in yolo-durable-worktrees.js.
function listsFile(env = process.env) {
	const configured = env.PI_CODING_AGENT_DIR;
	const home = env.HOME || env.USERPROFILE || homedir();
	let agentDir;
	if (configured === "~") {
		agentDir = home;
	} else if (configured?.startsWith("~/") || configured?.startsWith("~\\")) {
		agentDir = join(home, configured.slice(2));
	} else if (configured) {
		agentDir = configured;
	} else {
		agentDir = join(home, ".pi", "agent");
	}
	return join(agentDir, "yolo-model-lists.json");
}

// THE LAUNCH'S OWN LISTS, READ FIRST. `yolo host -p <profile> -- pi` composes this file's content
// for its own -p and hands it in this variable, never writing the file, which `yolo host apply`
// renders for the configured profile alone (packs/pi/pack.json's `launch_selection`;
// docs/design/model-lists-and-pickers.md MM-D30). Set, it is what pi registers; unset, the file is.
const LISTS_ENV = "YOLO_PI_MODEL_LISTS";

// The output cap pi's models.json loader gives a model that states none (modelFromJson).
const DEFAULT_MAX_TOKENS = 16384;

// readModelLists returns { <pi provider id>: { models, enforce, api } }, {} when the launch's
// variable and the file are missing, are not JSON, or hold no providers. {} is never an error: it
// registers nothing, and pi keeps every catalog as it is. The variable, when set, is read instead of
// the file, never beside it: it is the whole list this launch composed.
function readModelLists() {
	let parsed;
	try {
		const fromLaunch = process.env[LISTS_ENV];
		parsed = JSON.parse(fromLaunch ? fromLaunch : readFileSync(listsFile(), "utf8"));
	} catch {
		return {};
	}
	const providers = parsed?.providers;
	if (!providers || typeof providers !== "object" || Array.isArray(providers)) return {};
	const out = {};
	for (const [id, list] of Object.entries(providers)) {
		const models = Array.isArray(list?.models)
			? list.models.filter((entry) => typeof entry?.id === "string" && entry.id.length > 0)
			: [];
		if (models.length === 0) continue;
		out[id] = {
			models,
			enforce: list.enforce === true,
			keepCatalog: list.keep_catalog === true,
			api: typeof list.api === "string" && list.api.length > 0 ? list.api : undefined,
		};
	}
	return out;
}

// piAI returns what this file reads from pi's own pi-ai: its catalog lookup, its built-in
// providers and its api registry. The specifiers resolve through the modules pi's extension
// loader provides for its own packages, an alias in the npm build and a virtual module in the
// bundled one (core/extensions/loader.js, virtual-modules.js); anything unavailable degrades to
// "unknown", never to a failed load.
async function piAI() {
	const out = { lookup: () => undefined, catalog: () => [], builtin: () => undefined, apiProvider: () => undefined };
	try {
		const all = await import("@earendil-works/pi-ai/providers/all");
		if (typeof all.getBuiltinModel === "function") {
			out.lookup = (provider, id) => {
				try {
					return all.getBuiltinModel(provider, id);
				} catch {
					return undefined;
				}
			};
		}
		if (typeof all.getBuiltinModels === "function") {
			out.catalog = (provider) => {
				try {
					return all.getBuiltinModels(provider) ?? [];
				} catch {
					return [];
				}
			};
		}
		if (typeof all.builtinProviders === "function") {
			let providers;
			out.builtin = (provider) => {
				try {
					providers ??= all.builtinProviders();
					return providers.find((p) => p?.id === provider);
				} catch {
					return undefined;
				}
			};
		}
	} catch {
		// no catalog: every fact below falls back to pi's models.json defaults
	}
	try {
		const compat = await import("@earendil-works/pi-ai/compat");
		if (typeof compat.getApiProvider === "function") out.apiProvider = (api) => compat.getApiProvider(api);
	} catch {
		// no registry: a provider with no built-in cannot be enforced, and says so
	}
	return out;
}

// definition merges one rendered entry over pi's catalog entry for its id (or its `base`, for a
// variant). The catalog's address fields are dropped so the registration never repoints the
// provider. An id pi's catalog lacks gets the defaults pi's own models.json loader applies,
// because registerProvider applies none of its own; what yolo declares always wins.
//
// `openrouter_routing`, when declared, is lowered into pi's `compat.openRouterRouting`, which pi
// sends verbatim as the request's `provider` field (openai-completions' buildParams:
// `model.compat?.openRouterRouting&&(params.provider=model.compat.openRouterRouting)`). It is
// MERGED over the catalog entry's own compat rather than replacing it, and is removed from the
// model's top-level fields so no stray `openrouter_routing` reaches pi.
//
// A FACT THE ROW NAMES IN `tombstones` IS REMOVED (docs/design/model-lists-and-pickers.md
// ML-D10): the row wrote it as null, meaning the model has no such fact, so the catalog's value
// does not reach the registration. A field pi's models.json loader requires takes the value that
// loader gives a row that states none (the same defaults an id the catalog lacks gets), and any
// other field is left out.
const LOADER_DEFAULTS = {
	reasoning: false,
	input: ["text"],
	cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
	contextWindow: 128000,
	maxTokens: DEFAULT_MAX_TOKENS,
};

// catalogFacts is a catalog entry with its address fields dropped, so a registration built from
// it never repoints the provider. The entry's own `headers` are KEPT: they are not an address but
// part of what the model needs sent, and pi sends a registered model's headers with its requests
// (pi 1.0.1 provider-composer.js rawModelHeaders). Every github-copilot catalog model carries the
// IDE headers Copilot refuses a request without (Editor-Version, Copilot-Integration-Id), and the
// provider has none, so dropping them failed every turn of a curated Copilot list with a 400.
function catalogFacts(builtin) {
	const { api: _api, provider: _provider, baseUrl: _baseUrl, ...facts } = builtin;
	return facts;
}

function definition(provider, entry, lookup) {
	const { base, openrouter_routing, tombstones, ...declared } = entry;
	const withRouting = (model) => {
		if (openrouter_routing === null || typeof openrouter_routing !== "object" || Array.isArray(openrouter_routing)) {
			return model;
		}
		return { ...model, compat: { ...(model.compat ?? {}), openRouterRouting: openrouter_routing } };
	};
	const withoutTombstoned = (model) => {
		if (!Array.isArray(tombstones)) return model;
		const out = { ...model };
		for (const field of tombstones) {
			if (typeof field !== "string") continue;
			if (field === "name") out.name = entry.id;
			else if (field in LOADER_DEFAULTS) out[field] = structuredClone(LOADER_DEFAULTS[field]);
			else delete out[field];
		}
		return out;
	};
	// `cost` merges RATE BY RATE: a row may state only some rates (the derive emits only the
	// declared ones), and each rate it leaves out keeps the catalog's, or the loader's 0.
	const withCost = (under) =>
		declared.cost && typeof declared.cost === "object" ? { cost: { ...(under ?? {}), ...declared.cost } } : {};
	const builtin = lookup(provider, base ?? entry.id);
	if (builtin) {
		const facts = catalogFacts(builtin);
		return withRouting(withoutTombstoned({ ...facts, ...declared, ...withCost(facts.cost), name: declared.name ?? builtin.name }));
	}
	return withRouting(withoutTombstoned({
		name: entry.id,
		...structuredClone(LOADER_DEFAULTS),
		...declared,
		...withCost(LOADER_DEFAULTS.cost),
	}));
}

// withCatalog is the list a `keep_catalog` registration names: pi's own catalog for the
// provider, each entry a row states replaced in place by that row merged over it, then every row
// the catalog lacks. It is the list pi itself builds from its catalog and models.json rows (which
// add or replace by id), except that a row's merge is field by field (OQ-ML4), so the menu is
// not narrowed and a row keeps every catalog fact it does not state.
function withCatalog(provider, rows, pi) {
	const byId = new Map(rows.map((m) => [m.id, m]));
	const out = [];
	for (const builtin of pi.catalog(provider)) {
		if (typeof builtin?.id !== "string") continue;
		if (byId.has(builtin.id)) {
			out.push(byId.get(builtin.id));
			byId.delete(builtin.id);
		} else {
			out.push(catalogFacts(builtin));
		}
	}
	return [...out, ...byId.values()];
}

// listApi is the ONE api every model of the list runs on, the api the refusing registration
// names, or undefined when there is not exactly one. The derive states it for a provider it
// writes a models.json row for (that row's `api`); otherwise it is pi's own catalog's, read per
// listed id, and for an id the catalog lacks the api pi's catalog gives the whole provider. A
// registration names one `api` and pi hands the wrapper only models of that api, so a list whose
// models run on two apis could refuse only half of what `--model` can reach (pi's custom-model
// fallback copies a listed model, of either api), and is registered without the refusal instead.
function listApi(provider, list, pi) {
	if (list.api) return list.api;
	const catalogApis = new Set(pi.catalog(provider).map((m) => m?.api).filter((api) => typeof api === "string"));
	const apis = new Set();
	for (const entry of list.models) {
		const api = pi.lookup(provider, entry.base ?? entry.id)?.api;
		if (typeof api === "string") apis.add(api);
		else if (catalogApis.size === 1) apis.add([...catalogApis][0]);
		else return undefined;
	}
	return apis.size === 1 ? [...apis][0] : undefined;
}

// delegateFor is the stream pi runs a model on when no extension wraps it, rebuilt from what pi
// exports: the built-in provider of that id when it serves the model's api, else pi's api
// registry (pi 0.99.1 composeModelProvider's streamWith, which does exactly this for a
// registration without a streamSimple). It never touches the credential: pi resolved it into
// `options` before it called the wrapper. undefined when neither can be found.
function delegateFor(provider, api, pi) {
	const base = pi.builtin(provider);
	const baseServes = base && typeof base.streamSimple === "function" &&
		(base.getModels?.() ?? []).some((m) => m?.api === api);
	if (baseServes) return (model, context, options) => base.streamSimple(model, context, options);
	const registered = pi.apiProvider(api);
	if (registered && typeof registered.streamSimple === "function") {
		return (model, context, options) => registered.streamSimple(model, context, options);
	}
	return undefined;
}

// refusal is the message a refused model ends its turn with: what was refused, why, what is
// allowed, and the two ways out, worded as the wire bridge words its own refusal
// (internal/wirebridged's allowlist).
// ⚠ DUPLICATED VERBATIM in yolo-openai-auth.js: pi loads every .js file in its extensions directory
// as an extension and reports one that exports no factory as a load error, so a shared
// module would be one more file delivered elsewhere for one sentence.
// internal/entrypoint's TestPisTwoRefusalsAreWordedAlike fails when the copies differ.
function refusal(provider, id, listed) {
	return (
		`yolo: model "${provider}/${id}" is not on yolo's model list for provider ${provider}, and the ` +
		`profile enforces it (enforce_models is on by default). Allowed: ${listed.join(", ")}. Pick one ` +
		`of those, or set "enforce_models": false on the profile so the list only shapes pi's menu.`
	);
}

// ANOTHER EXTENSION'S REGISTRATION OF THE SAME PROVIDER IS KEPT (docs/design/model-lists-and-pickers.md
// ML-D15). pi composes a re-registration of a provider by laying each key it defines over the
// previous registration's, so a second `models` replaces the first whole (pi 1.0.1
// ModelRuntime.registerProvider, "Re-registration merges defined values over the previous
// registration"), and an extension cannot read another's registration through its API: before
// pi binds them, registrations are queued write-only (core/extensions/loader.js). So a pack
// extension that registered, say, amazon-bedrock's long-context variant rows lost them to yolo's
// list, which pi applies later. The one place both are visible is pi's model runtime as it
// applies a registration, where `getRegisteredProviderConfig` returns the one before it; yolo
// wraps `registerProvider` there once per process, and the wrapper acts only on a config carrying
// MERGE, yolo's own: it hands MERGE what was registered before, and registers what MERGE returns.
// For a list nothing enforces, that is the other extension's rows as it registered them, then
// each of yolo's declared rows whose id they lack, and every other key the other extension set is
// kept by pi's own key merge. A list the profile enforces still replaces the provider's models
// (it is the list the profile asked pi to refuse outside, MM-D21), and says so once. Where pi
// does not export ModelRuntime with both methods, the registration is yolo's list as before.
const MERGE = Symbol.for("yolo-jail.pi.model-lists.merge");

// THEIRS marks a models array yolo registered with what another extension had registered before
// it (undefined for none), so a later registration of yolo's merges with that and never with
// yolo's own earlier rows.
const THEIRS = Symbol.for("yolo-jail.pi.model-lists.theirs");

function markTheirs(models, theirs) {
	Object.defineProperty(models, THEIRS, { value: theirs });
	return models;
}

// foreignModels is the models another extension registered for the provider before yolo, read
// off the registration pi holds, or undefined when there is none.
function foreignModels(prior) {
	const models = prior?.models;
	if (!Array.isArray(models)) return undefined;
	const theirs = THEIRS in models ? models[THEIRS] : models;
	return Array.isArray(theirs) && theirs.length > 0 ? theirs : undefined;
}

// installMerge wraps pi's ModelRuntime.registerProvider so a config carrying MERGE is resolved
// against the registration before it; true when the wrapper is in place.
async function installMerge() {
	let proto;
	try {
		proto = (await import("@earendil-works/pi-coding-agent")).ModelRuntime?.prototype;
	} catch {
		return false;
	}
	if (!proto || typeof proto.registerProvider !== "function" || typeof proto.getRegisteredProviderConfig !== "function") {
		return false;
	}
	if (proto[MERGE]) return true;
	const original = proto.registerProvider;
	Object.defineProperty(proto, "registerProvider", {
		configurable: true,
		writable: true,
		value: function registerProvider(providerId, config, ...rest) {
			const merge = config?.[MERGE];
			if (typeof merge === "function") {
				let prior;
				try {
					prior = this.getRegisteredProviderConfig(providerId);
				} catch {
					prior = undefined;
				}
				config = merge(prior);
			}
			return original.call(this, providerId, config, ...rest);
		},
	});
	Object.defineProperty(proto, MERGE, { value: true });
	return true;
}

// pi awaits an extension's factory (core/extensions/loader.js), so the catalog import finishes
// before any registration is read.
export default async function registerYoloModelLists(pi) {
	const lists = readModelLists();
	const providers = Object.keys(lists);
	if (providers.length === 0) return;
	const lib = await piAI();
	const merging = await installMerge();
	const unenforced = [];
	const replaced = [];
	for (const provider of providers) {
		const list = lists[provider];
		const declared = list.models.map((entry) => definition(provider, entry, lib.lookup));
		let models = declared;
		if (list.keepCatalog) {
			// With no catalog to complete, a registration would only replace pi's list with yolo's
			// rows; the models.json rows pi already loaded say the same without narrowing anything.
			if (lib.catalog(provider).length === 0) continue;
			models = withCatalog(provider, models, lib);
		}
		// A list yolo cannot refuse outside keeps another extension's rows and adds only its declared
		// rows whose ids they lack (ML-D15): replacing them would take that extension's models away
		// and buy no refusal in return.
		const keeping = () => ({
			models: markTheirs(models, undefined),
			[MERGE]: (prior) => {
				const theirs = foreignModels(prior);
				if (!theirs) return { models: markTheirs([...models], undefined) };
				const ids = new Set(theirs.map((m) => m?.id));
				// pi validates a registration on its own, so the other extension's provider-level
				// api and baseUrl ride along for its rows that rely on them (unchanged values).
				return {
					...(prior.api ? { api: prior.api } : {}),
					...(prior.baseUrl ? { baseUrl: prior.baseUrl } : {}),
					models: markTheirs([...theirs, ...declared.filter((m) => !ids.has(m.id))], theirs),
				};
			},
		});
		if (!list.enforce) {
			pi.registerProvider(provider, keeping());
			continue;
		}
		// An enforced list pi can refuse outside replaces another extension's models, and is told
		// about (ML-D15).
		const replacing = (config) => ({
			...config,
			models: markTheirs(config.models, undefined),
			[MERGE]: (prior) => {
				if (foreignModels(prior) && !replaced.includes(provider)) replaced.push(provider);
				return { ...config, models: markTheirs([...config.models], undefined) };
			},
		});
		const api = listApi(provider, list, lib);
		const delegate = api && delegateFor(provider, api, lib);
		if (!delegate) {
			pi.registerProvider(provider, keeping());
			unenforced.push(provider);
			continue;
		}
		const listed = list.models.map((entry) => entry.id);
		const allowed = new Set(listed);
		pi.registerProvider(provider, replacing({
			api,
			models,
			streamSimple: (model, context, options) => {
				if (!allowed.has(model?.id)) throw new Error(refusal(provider, model?.id, listed));
				return delegate(model, context, options);
			},
		}));
	}

	// ONCE PER LOAD, where the user can see it, as yolo-openai-auth.js reports its degradation: a
	// list the profile enforces that pi could not be made to refuse is a softer limit than the one
	// the profile asked for, and an enforced list that replaced another extension's models took
	// something away; each says so. `replaced` fills when pi applies the registrations, before it
	// starts a session.
	if (unenforced.length > 0 || (merging && providers.some((p) => lists[p].enforce))) {
		let told = false;
		pi.on?.("session_start", (_event, ctx) => {
			if (told) return;
			const warnings = [];
			if (unenforced.length > 0) {
				warnings.push(
					`yolo: the profile enforces yolo's model list for ${unenforced.join(", ")}, but pi cannot refuse a model ` +
					`outside it there (the list's models do not share one pi api, or pi's own stream for it was not ` +
					`found), so \`pi --model\` can still run an unlisted one, and another pi extension's models there are kept.`,
				);
			}
			if (replaced.length > 0) {
				warnings.push(
					`yolo: the profile enforces yolo's model list for ${replaced.join(", ")}, so it replaced the models ` +
					`another pi extension registered there. Set "enforce_models": false on the profile to keep that ` +
					`extension's models beside yolo's.`,
				);
			}
			if (warnings.length === 0) return;
			told = true;
			for (const warning of warnings) {
				if (ctx?.hasUI) ctx.ui.notify(warning, "warning");
				else console.warn(warning);
			}
		});
	}
}
