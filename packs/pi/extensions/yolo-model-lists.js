import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

// THE NARROWED LISTS ARE DATA. yolo renders every provider list a `models` contribution narrowed
// with an `only` into this file at every boot: the `pi/model-lists` surface packs/pi/pack.json
// declares, written by `yolo.derive("pi", "model-lists")` in packs/pi/derive.lua, keyed by pi's
// provider id (docs/design/model-lists-and-pickers.md MM-D6). The file sits beside extensions/,
// outside pi's extension discovery and outside this file's own read-only delivery, as the
// openai-codex list's does (yolo-openai-auth.js, ML-D3).
//
// WHY A REGISTRATION. pi builds a provider's list as its own catalog, then models.json rows,
// which add or replace by id and never remove, then an extension's registerProvider with
// `models`, which replaces the provider's whole list (pi 0.99.1 core/provider-composer.js;
// MEASURED on 0.87.1 and 0.99.1 with a library load). So only a registration can make a narrowed
// list pi's exact menu for a provider pi ships. It passes `models` alone, which keeps pi's own
// address, wire and credential for the provider (MEASURED for this form).
//
// NOT A REFUSAL. `pi --model <provider>/<unlisted id>` still runs, with pi's own warning. The
// form that refuses it, a streamSimple wrapper, is not built until its credential path is
// measured (MM-D6).
const LISTS_FILE = join(homedir(), ".pi", "agent", "yolo-model-lists.json");

// The output cap pi's models.json loader gives a model that states none (modelFromJson).
const DEFAULT_MAX_TOKENS = 16384;

// readModelLists returns { <pi provider id>: [entry, ...] }, {} when the file is missing, is not
// JSON, or holds no providers. {} is never an error: it registers nothing, and pi keeps every
// catalog as it is.
function readModelLists() {
	let parsed;
	try {
		parsed = JSON.parse(readFileSync(LISTS_FILE, "utf8"));
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
		if (models.length > 0) out[id] = models;
	}
	return out;
}

// builtinLookup returns a lookup into pi's OWN catalog, the source of every pi-dialect fact yolo
// does not declare (the consumer translates, docs/reference/providers.md OQ-CS4). The specifier
// resolves through the alias pi's extension loader installs for its own packages; anything
// unavailable degrades to "unknown", never to a failed load.
async function builtinLookup() {
	try {
		const { getBuiltinModel } = await import("@earendil-works/pi-ai/providers/all");
		if (typeof getBuiltinModel !== "function") return () => undefined;
		return (provider, id) => {
			try {
				return getBuiltinModel(provider, id);
			} catch {
				return undefined;
			}
		};
	} catch {
		return () => undefined;
	}
}

// definition merges one rendered entry over pi's catalog entry for its id (or its `base`, for a
// variant). The catalog's address fields are dropped so the registration never repoints the
// provider. An id pi's catalog lacks gets the defaults pi's own models.json loader applies,
// because registerProvider applies none of its own; what yolo declares always wins.
function definition(provider, entry, lookup) {
	const { base, ...declared } = entry;
	const builtin = lookup(provider, base ?? entry.id);
	if (builtin) {
		const { api: _api, provider: _provider, baseUrl: _baseUrl, headers: _headers, ...facts } = builtin;
		return { ...facts, ...declared, name: declared.name ?? builtin.name };
	}
	return {
		name: entry.id,
		reasoning: false,
		input: ["text"],
		cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
		contextWindow: 128000,
		maxTokens: DEFAULT_MAX_TOKENS,
		...declared,
	};
}

// pi awaits an extension's factory (core/extensions/loader.js), so the catalog import finishes
// before any registration is read.
export default async function registerYoloModelLists(pi) {
	const lists = readModelLists();
	const providers = Object.keys(lists);
	if (providers.length === 0) return;
	const lookup = await builtinLookup();
	for (const provider of providers) {
		pi.registerProvider(provider, {
			models: lists[provider].map((entry) => definition(provider, entry, lookup)),
		});
	}
}
