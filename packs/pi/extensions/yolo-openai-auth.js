import { execFile } from "node:child_process";
import { readFileSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

// The two routes `yolo internal openai-auth-client` has to the machine's OpenAI login: a jail's
// endpoint file, and the host broker's private socket, which only `yolo host --` sets
// (internal/openauthclient's EndpointEnv and HostSocketEnv). With neither, the client can only
// fail, naming the jail's variable.
const BROKER_ENDPOINT_ENV = "YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT";
const HOST_SOCKET_ENV = "YOLO_OPENAI_AUTH_HOST_SOCKET";

// inJail says whether this pi runs inside a yolo jail. It must never answer "no" in one, since
// a "no" is what turns the failure into advice to launch through `yolo host`, so it asks two
// witnesses and either suffices: YOLO_VERSION, which every jail launcher sets and which yolo's
// own code reads for the same question (internal/banner's jailEnv), and ~/.yolo/bin, the
// generated script dir every jail home carries on every backend and a real home never does (a
// home may not hold a `.yolo`, paths.WorkspaceScopeBreach). The second is for an environment an
// agent or wrapper scrubbed before starting pi. A host where both are false gets the advice; a
// host where one is true only keeps the client's own message, which is the safe direction.
function inJail() {
	if (process.env.YOLO_VERSION) return true;
	try {
		return statSync(join(homedir(), ".yolo", "bin")).isDirectory();
	} catch {
		return false;
	}
}

// brokerFailure is the error a failed client call reports. The one case it rewrites is the
// one the client cannot word for a person: no route at all, outside a jail. That is pi started
// directly or from an IDE on the host, and the fix is the launch, not the variable
// (docs/design/host-computed-layer.md §8.1 item 3). The client's own words stay in parentheses.
function brokerFailure(detail) {
	if (!process.env[BROKER_ENDPOINT_ENV] && !process.env[HOST_SOCKET_ENV] && !inJail()) {
		return new Error(
			"OpenAI credential service: pi was not started through `yolo host`, so it has no " +
				`route to yolo's shared OpenAI login. Launch it with \`yolo host -- pi\` (${detail}).`,
		);
	}
	return new Error(`OpenAI credential service: ${detail}`);
}

function brokerCommand(action, signal, showStderr = false) {
	return new Promise((resolve, reject) => {
		const child = execFile(
			"yolo",
			["internal", "openai-auth-client", action],
			{ encoding: "utf8", signal },
			(error, stdout, stderr) => {
				if (error) {
					reject(brokerFailure(stderr.trim() || error.message));
					return;
				}

				let view;
				try {
					view = JSON.parse(stdout);
				} catch {
					reject(new Error("OpenAI credential service returned malformed JSON"));
					return;
				}
				resolve(view);
			},
		);
		if (showStderr) child.stderr?.pipe(process.stderr);
	});
}

async function brokerToken(signal) {
	const view = await brokerCommand("token", signal);
	if (
		typeof view.access_token !== "string" ||
		view.access_token.length === 0 ||
		typeof view.expires_at !== "number" ||
		!Number.isFinite(view.expires_at) ||
		typeof view.generation !== "number" ||
		!Number.isSafeInteger(view.generation) ||
		view.generation <= 0
	) {
		throw new Error("OpenAI credential service returned an invalid token view");
	}
	return {
		refresh: `yolo-broker:${view.generation}`,
		access: view.access_token,
		expires: view.expires_at,
		...(typeof view.account_id === "string" && view.account_id.length > 0
			? { accountId: view.account_id }
			: {}),
	};
}

async function brokerLogin(signal) {
	const status = await brokerCommand("status", signal);
	if (status?.logged_in !== true || status?.login_required === true) {
		await brokerCommand("login", signal, true);
	}
	return brokerToken(signal);
}

// THE MODEL LIST IS DATA, NOT A COPY. yolo renders the one openai-codex declaration
// (packs/openai-auth/pack.json) into this file at every jail boot: the `pi/codex-models`
// surface packs/pi/pack.json declares, written by `yolo.derive("pi", "codex-models")` in
// packs/pi/derive.lua. claude's picker renders the same list, so the two cannot drift
// (docs/design/model-lists-and-pickers.md ML-D1, ML-D3). The file sits beside extensions/,
// outside pi's extension discovery and outside this file's own read-only delivery.
// `yolo host apply` renders the same file into a real home from the provider table it composes
// there (docs/reference/host-agent-environment.md OQ-HC1, superseding ML-D8), so host pi registers
// the same list.
const CODEX_LIST_FILE = join(homedir(), ".pi", "agent", "yolo-openai-codex-models.json");

// THE LAUNCH'S OWN LIST, READ FIRST. `yolo host -p <profile> -- pi` composes this file's content
// for its own -p and hands it in this variable, never writing the file, which `yolo host apply`
// renders for the configured profile alone (packs/pi/pack.json's `launch_selection`;
// docs/design/model-lists-and-pickers.md MM-D30). Set, it is what pi registers; unset, the file is.
const CODEX_LIST_ENV = "YOLO_PI_OPENAI_CODEX_MODELS";

// The pi api every openai-codex model runs on, which this registration names.
const CODEX_API = "openai-codex-responses";

// readCodexModelList returns the rendered entries and the file's `enforce` flag, or no entries
// when the launch's variable and the file are missing, are not JSON, or hold no `models` array. No
// entries is never an error: it registers no models of our own, and pi then keeps its built-in
// openai-codex catalog, so login never depends on the file. `enforce` is the switch of the profile
// that governs the list (enforce_models, on unless the profile says false;
// docs/design/model-lists-and-pickers.md MM-D5), which the derive writes beside the list; anything
// but `true` refuses nothing. The variable, when set, is read instead of the file.
function readCodexModelList() {
	let parsed;
	try {
		const fromLaunch = process.env[CODEX_LIST_ENV];
		parsed = JSON.parse(fromLaunch ? fromLaunch : readFileSync(CODEX_LIST_FILE, "utf8"));
	} catch {
		return { list: [], enforce: false };
	}
	const models = parsed?.models;
	if (!Array.isArray(models)) return { list: [], enforce: false };
	return {
		list: models.filter((entry) => typeof entry?.id === "string" && entry.id.length > 0),
		enforce: parsed.enforce === true,
	};
}

// The subscription's address, and the names pi shows for the provider and its login. pi composes
// a registration's display name as its own `name`, else models.json's, else its built-in
// provider's (provider-composer.js, composeModelProvider), and pi 0.99.0 renamed that built-in
// "OpenAI Codex (legacy)". So a registration naming nothing showed yolo's subscription login under
// a label for a provider pi no longer recommends (docs/design/model-lists-and-pickers.md §14.2).
const CODEX_BASE_URL = "https://chatgpt.com/backend-api";
const CODEX_NAME = "OpenAI Codex";
const CODEX_LOGIN_NAME = "OpenAI Codex (yolo shared login)";

// builtinCodexProvider returns pi's own openai-codex provider (a complete pi-ai Provider:
// address, catalog, streams and its own oauth) when pi exports one that serves this
// registration's api, else undefined. The specifier resolves through the alias pi's extension
// loader installs for its own packages.
async function builtinCodexProvider() {
	try {
		const all = await import("@earendil-works/pi-ai/providers/all");
		const base =
			typeof all.builtinProviders === "function"
				? all.builtinProviders().find((provider) => provider?.id === "openai-codex")
				: undefined;
		if (
			base &&
			typeof base.streamSimple === "function" &&
			(base.getModels?.() ?? []).some((model) => model?.api === CODEX_API)
		) {
			return base;
		}
	} catch {
		// no built-in provider: the caller degrades
	}
	return undefined;
}

// codexDelegate is the stream pi runs an openai-codex model on when no extension wraps it,
// rebuilt from what pi exports: its built-in openai-codex provider when that serves this
// registration's api, else pi's api registry for the api (pi 0.99.1 composeModelProvider's
// streamWith, which does exactly this for a registration without a streamSimple). It never
// touches the credential: pi resolves the subscription login into `options` before it calls the
// wrapper below (MEASURED 2026-09-30 on pi 0.99.1, docs/design/model-lists-and-pickers.md
// MM-D23). undefined when neither can be found.
async function codexDelegate() {
	const base = await builtinCodexProvider();
	if (base) return (model, context, options) => base.streamSimple(model, context, options);
	try {
		const compat = await import("@earendil-works/pi-ai/compat");
		const registered = typeof compat.getApiProvider === "function" ? compat.getApiProvider(CODEX_API) : undefined;
		if (registered && typeof registered.streamSimple === "function") {
			return (model, context, options) => registered.streamSimple(model, context, options);
		}
	} catch {
		// no registry either: the list is registered as the exact menu alone, and says so
	}
	return undefined;
}

// refusal is the message a refused model ends its turn with: what was refused, why, what is
// allowed, and the two ways out, worded as the wire bridge words its own refusal
// (internal/wirebridged's allowlist).
// ⚠ DUPLICATED VERBATIM in yolo-model-lists.js: pi loads every .js file in its extensions directory
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

// codexCatalog returns a lookup into pi's OWN openai-codex catalog, the source of every
// pi-dialect fact yolo does not declare (cost tiers, thinking levels, compat, image limits):
// the consumer translates, yolo does not re-copy pi-ai (docs/reference/providers.md OQ-CS4).
// The specifier resolves through the alias pi's extension loader installs for its own
// packages. Anything unavailable degrades to "unknown", never to a failed load, and
// `failure` says why the catalog could not be reached so the degradation is reported.
async function codexCatalog() {
	let getBuiltinModel;
	try {
		({ getBuiltinModel } = await import("@earendil-works/pi-ai/providers/all"));
	} catch (error) {
		return { lookup: () => undefined, failure: error?.message || String(error) };
	}
	if (typeof getBuiltinModel !== "function") {
		return { lookup: () => undefined, failure: "the module exports no getBuiltinModel" };
	}
	return {
		lookup: (id) => {
			try {
				return getBuiltinModel("openai-codex", id);
			} catch {
				return undefined;
			}
		},
	};
}

// codexModelDefinition merges one rendered entry over pi's catalog entry for its base id.
// The catalog's address fields are dropped so the registration below stays the one place
// that says where requests go. An id pi's catalog lacks gets the defaults pi's own
// models.json loader applies (core/provider-composer.js, modelFromJson), because
// registerProvider applies none of its own.
function codexModelDefinition(entry, lookup) {
	const builtin = lookup(entry.base ?? entry.id);
	if (builtin) {
		const { api: _api, provider: _provider, baseUrl: _baseUrl, headers: _headers, ...facts } = builtin;
		return {
			...facts,
			id: entry.id,
			name: entry.name ?? builtin.name,
			contextWindow: entry.contextWindow ?? builtin.contextWindow,
		};
	}
	return {
		id: entry.id,
		name: entry.name ?? entry.id,
		reasoning: false,
		input: ["text"],
		cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
		contextWindow: entry.contextWindow ?? 128000,
		maxTokens: DEFAULT_MAX_TOKENS,
	};
}

// The output cap pi's models.json loader gives a model that states none.
const DEFAULT_MAX_TOKENS = 16384;

// piVersion returns the running pi's version, or undefined when it cannot be read. pi's
// extension API passes no version, but the package root is one of the specifiers pi's loader
// resolves for extensions (an alias to its own index.js, or a virtual module in a bundled
// binary; core/extensions/loader.js and virtual-modules.js), and that root exports VERSION.
// A package.json path would not survive the bundled binary, which has no files to read.
// "0.0.0" is what pi reports when it could not read its own package.json (config.js), so it
// counts as unreadable rather than as a version.
async function piVersion() {
	const VERSION = (await piPackage())?.VERSION;
	return typeof VERSION === "string" && VERSION.length > 0 && VERSION !== "0.0.0" ? VERSION : undefined;
}

// piPackage returns the running pi's package root, the module piVersion describes, or undefined
// when it cannot be imported.
async function piPackage() {
	try {
		return await import("@earendil-works/pi-coding-agent");
	} catch {
		return undefined;
	}
}

// piTakesNativeProviders says whether this pi's extension loader can take a provider object, the
// host route's registration. pi 0.81.0 added that, routing the one-argument registerProvider to
// its model runtime's registerNativeProvider; pi 0.80.10 already exports the built-in provider,
// but its registerProvider queues the object as a name and fails applying it, which loses the
// registration and throws nothing at the call (MEASURED 2026-10-04,
// docs/design/pi-host-openai-auth.md PH-D4). A try/catch around the call cannot see that, so the
// method is asked for here instead, on the ModelRuntime pi's root exports.
async function piTakesNativeProviders() {
	return typeof (await piPackage())?.ModelRuntime?.prototype?.registerNativeProvider === "function";
}

// degradedWarning says which registered models fell back to the defaults above, or returns
// undefined when pi's catalog described every one. The fallback registers a model pi cannot
// think with, send images to or give its real output cap, and pi is not version-pinned, so a
// renamed catalog module would otherwise cost every model that silently. A catalog that
// loaded but lacks an id most likely belongs to a pi older than the model (a host pi is the
// user's own install, updated on the user's schedule), so that warning names the running pi
// and the remedy, reading the version only then. A catalog that did not load is a different
// fault, which an update is not known to fix, so its message makes no such claim.
async function degradedWarning(list, catalog) {
	const what = `registered as text-only with no thinking levels and a ${DEFAULT_MAX_TOKENS}-token output cap`;
	if (catalog.failure) {
		return `yolo: pi's own openai-codex catalog did not load (${catalog.failure}), so the ${list.length} ChatGPT subscription models yolo lists are ${what}.`;
	}
	const missing = [...new Set(list.map((entry) => entry.base ?? entry.id))].filter((id) => !catalog.lookup(id));
	if (missing.length === 0) return undefined;
	const these = missing.length === 1 ? "this model" : "these models";
	const version = await piVersion();
	const cause = version
		? `Your pi (${version}) predates ${these}; \`pi update\` fixes it.`
		: `Your pi may predate ${these}; \`pi update\` fixes it.`;
	return `yolo: pi's openai-codex catalog has no ${missing.join(", ")}, so ${missing.length === 1 ? "that model is" : "those models are"} ${what}. ${cause}`;
}

// The source pi records for the host route's answer, and the name of the key method that gives
// it. pi lists that method in /login with no setup of its own, as "configured outside pi".
const HOST_LOGIN_SOURCE = "yolo shared login";
const HOST_LOGIN_METHOD = "yolo shared login (through `yolo host`)";

// The window pi refreshes a stored OAuth login in (pi-ai auth/resolve.js,
// DEFAULT_OAUTH_MINIMUM_VALIDITY_MS): the host route reuses the broker's view until then, as pi
// reuses a stored one, rather than asking the client on every request.
const TOKEN_REUSE_MS = 5 * 60 * 1000;

// THE HOST ROUTE (docs/design/pi-host-openai-auth.md OQ-1, D2). pi lists a provider's models only
// when it counts the provider configured, and for openai-codex it counted that only for a login
// stored in ~/.pi/agent/auth.json. A jail stores one before pi starts; `yolo host -- pi` writes
// nothing into that file, which is the user's own (docs/plans/notch-convergence.md NC-D37), and
// hands pi the host broker's socket instead. So host pi started on a fallback model with no
// ChatGPT subscription model in /model.
//
// This is pi's own openai-codex provider with yolo's login on it, registered as a NATIVE provider
// (pi's one-argument registerProvider). Its key method answers "oauth" while the socket is set,
// which pi counts as configured with nothing stored, and resolves to the broker's access token, so
// the footer's subscription mark stays. A login stored in auth.json still wins over the key method,
// as pi resolves a stored credential first (pi-ai auth/resolve.js), so /login, a jail's view and a
// login of the user's own act as before. The method has no `login`: there is no key to enter.
//
// A native registration replaces the extension layer a ProviderConfig would compose, so the model
// list, the name and the refusal ride on the provider itself. models are the list's definitions,
// completed with the address fields pi's composer would add; guard throws the refusal for a model
// outside the list, before pi's own stream runs it.
function nativeCodexProvider(base, models, guard) {
	let held;
	const accessToken = async (signal) => {
		if (!held || held.expires - Date.now() <= TOKEN_REUSE_MS) held = await brokerToken(signal);
		return held.access;
	};
	return {
		...base,
		name: CODEX_NAME,
		baseUrl: CODEX_BASE_URL,
		auth: {
			oauth: {
				name: CODEX_LOGIN_NAME,
				isSubscription: true,
				login: async (interaction) => ({ ...(await brokerLogin(interaction?.signal)), type: "oauth" }),
				refresh: async (_credential, signal) => ({ ...(await brokerToken(signal)), type: "oauth" }),
				toAuth: async (credential) => ({ apiKey: credential.access }),
			},
			apiKey: {
				name: HOST_LOGIN_METHOD,
				check: async () => (process.env[HOST_SOCKET_ENV] ? { type: "oauth", source: HOST_LOGIN_SOURCE } : undefined),
				resolve: async (input) =>
					process.env[HOST_SOCKET_ENV]
						? { auth: { apiKey: await accessToken(input?.signal) }, source: HOST_LOGIN_SOURCE }
						: undefined,
			},
		},
		...(models
			? {
					getModels: () => [...models],
					getAllModels: () => [...models],
					// The list is the exact menu, so no catalog refresh replaces it.
					refreshModels: undefined,
				}
			: {}),
		...(guard
			? {
					...(typeof base.stream === "function"
						? {
								stream: (model, context, options) => {
									guard(model);
									return base.stream(model, context, options);
								},
							}
						: {}),
					streamSimple: (model, context, options) => {
						guard(model);
						return base.streamSimple(model, context, options);
					},
				}
			: {}),
	};
}

// nativeModel completes one list definition as the Model a native provider returns: the fields
// pi's composer adds to an extension's definition (provider-composer.js,
// extensionModelFromDefinition).
function nativeModel(definition) {
	return { ...definition, api: CODEX_API, provider: "openai-codex", baseUrl: CODEX_BASE_URL, headers: undefined };
}

// pi awaits an extension's factory (core/extensions/loader.js), so the catalog import
// finishes before the registration is read.
//
// TWO REGISTRATIONS, ONE PER ROUTE. Where `yolo host` set the broker's socket, pi's loader takes a
// provider object and pi exports its built-in openai-codex provider, the host route above
// (nativeCodexProvider). Everywhere else — a jail, which stores the login before pi starts, a pi
// started directly, and a pi too old for either — the ProviderConfig below, which composes over
// pi's built-in provider and counts as configured only for a stored login. Keeping the jail on the
// ProviderConfig keeps its registration exactly what it was, with pi's remote catalog refresh and
// no key method in /login.
//
// THE REFUSAL (docs/design/model-lists-and-pickers.md MM-D6, MM-D23). With a list and its
// `enforce` on, the registration also carries a `streamSimple` that throws yolo's refusal for a
// model outside the list and hands a listed one to the stream pi would have used without it. pi
// runs an extension's streamSimple for every model of the registration's `api`, and only after it
// has resolved the credential into `options`: for this provider the subscription login pi keeps
// as an oauth credential, refreshed through `refreshToken` below when it is near expiry and turned
// into the bearer token by `getApiKey` (pi 0.99.1 ModelRuntime.prepareRequest, pi-ai
// auth/resolve.js). So the login needs no handling here, and the refusal covers pi's `--model`
// fallback, which copies a listed model and so runs on the same api. Without a list there is
// nothing to refuse against, and pi's own catalog stays in place. A delegate that cannot be found
// leaves the exact menu alone, and pi says once that it cannot refuse. On the host route the
// refusal guards the provider's own two streams, which pi calls after the same resolution, and
// pi's built-in provider is the delegate.
export default async function registerYoloOpenAIAuth(pi) {
	const { list, enforce } = readCodexModelList();
	const catalog = list.length > 0 ? await codexCatalog() : { lookup: () => undefined };
	const lookup = catalog.lookup;
	const refusing = list.length > 0 && enforce;
	const listed = list.map((entry) => entry.id);
	const allowed = new Set(listed);
	const definitions = list.length > 0 ? list.map((entry) => codexModelDefinition(entry, lookup)) : undefined;
	const refuse = (model) => {
		if (!allowed.has(model?.id)) throw new Error(refusal("openai-codex", model?.id, listed));
	};

	const base =
		process.env[HOST_SOCKET_ENV] && (await piTakesNativeProviders()) ? await builtinCodexProvider() : undefined;
	const native = base !== undefined;
	if (native) {
		pi.registerProvider(nativeCodexProvider(base, definitions?.map(nativeModel), refusing ? refuse : undefined));
	}
	const delegate = !native && refusing ? await codexDelegate() : undefined;
	if (!native) {
		pi.registerProvider("openai-codex", {
			name: CODEX_NAME,
			baseUrl: CODEX_BASE_URL,
			api: CODEX_API,
			oauth: {
				name: CODEX_LOGIN_NAME,
				isSubscription: true,
				login: (_callbacks) => brokerLogin(),
				refreshToken: (_credentials, signal) => brokerToken(signal),
				getApiKey: (credentials) => credentials.access,
			},
			...(definitions ? { models: definitions } : {}),
			...(delegate
				? {
						streamSimple: (model, context, options) => {
							refuse(model);
							return delegate(model, context, options);
						},
					}
				: {}),
		});
	}

	// ONCE PER LOAD, where the user can see it: pi's own notification when there is a UI, else
	// stderr, which is what pi's extension runner does with its own diagnostics. session_start
	// fires again on /new and on a resume, and the degradation is the same each time.
	const warnings = [];
	const degraded = list.length > 0 ? await degradedWarning(list, catalog) : undefined;
	if (degraded) warnings.push(degraded);
	if (refusing && !native && !delegate) {
		warnings.push(
			"yolo: pi shows exactly yolo's model list for openai-codex, but cannot refuse a model outside it " +
				"there (pi's own stream for it was not found), so `pi --model` can still run an unlisted one.",
		);
	}
	if (warnings.length > 0) {
		let told = false;
		pi.on?.("session_start", (_event, ctx) => {
			if (told) return;
			told = true;
			for (const warning of warnings) {
				if (ctx?.hasUI) ctx.ui.notify(warning, "warning");
				else console.warn(warning);
			}
		});
	}

	pi.on?.("before_provider_request", (event) => {
		if (
			event?.payload &&
			typeof event.payload.model === "string" &&
			event.payload.model.endsWith("[1m]")
		) {
			return {
				...event.payload,
				model: event.payload.model.slice(0, -4),
			};
		}
	});
}
