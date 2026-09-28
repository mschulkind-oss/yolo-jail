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
// `yolo host apply` runs no derive for content, so on the host the file holds no list, this
// extension registers no models, and pi keeps its own openai-codex catalog (ML-D8, OQ-ML3).
const CODEX_LIST_FILE = join(homedir(), ".pi", "agent", "yolo-openai-codex-models.json");

// readCodexModelList returns the rendered entries, or [] when the file is missing, is not
// JSON, or holds no `models` array. [] is never an error: it registers no models of our
// own, and pi then keeps its built-in openai-codex catalog, so login never depends on it.
function readCodexModelList() {
	let parsed;
	try {
		parsed = JSON.parse(readFileSync(CODEX_LIST_FILE, "utf8"));
	} catch {
		return [];
	}
	const models = parsed?.models;
	if (!Array.isArray(models)) return [];
	return models.filter((entry) => typeof entry?.id === "string" && entry.id.length > 0);
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

// degradedWarning says which registered models fell back to the defaults above, or returns
// undefined when pi's catalog described every one. The fallback registers a model pi cannot
// think with, send images to or give its real output cap, and pi is not version-pinned, so a
// renamed catalog module would otherwise cost every model that silently.
function degradedWarning(list, catalog) {
	const what = `registered as text-only with no thinking levels and a ${DEFAULT_MAX_TOKENS}-token output cap`;
	if (catalog.failure) {
		return `yolo: pi's own openai-codex catalog did not load (${catalog.failure}), so the ${list.length} ChatGPT subscription models yolo lists are ${what}.`;
	}
	const missing = [...new Set(list.map((entry) => entry.base ?? entry.id))].filter((id) => !catalog.lookup(id));
	if (missing.length === 0) return undefined;
	return `yolo: pi's openai-codex catalog has no ${missing.join(", ")}, so ${missing.length === 1 ? "that model is" : "those models are"} ${what}.`;
}

// pi awaits an extension's factory (core/extensions/loader.js), so the catalog import
// finishes before the registration is read.
export default async function registerYoloOpenAIAuth(pi) {
	const list = readCodexModelList();
	const catalog = list.length > 0 ? await codexCatalog() : { lookup: () => undefined };
	const lookup = catalog.lookup;
	pi.registerProvider("openai-codex", {
		baseUrl: "https://chatgpt.com/backend-api",
		api: "openai-codex-responses",
		oauth: {
			name: "OpenAI Codex (yolo shared login)",
			isSubscription: true,
			login: (_callbacks) => brokerLogin(),
			refreshToken: (_credentials, signal) => brokerToken(signal),
			getApiKey: (credentials) => credentials.access,
		},
		...(list.length > 0 ? { models: list.map((entry) => codexModelDefinition(entry, lookup)) } : {}),
	});

	// ONCE PER LOAD, where the user can see it: pi's own notification when there is a UI, else
	// stderr, which is what pi's extension runner does with its own diagnostics. session_start
	// fires again on /new and on a resume, and the degradation is the same each time.
	const warning = list.length > 0 ? degradedWarning(list, catalog) : undefined;
	if (warning) {
		let told = false;
		pi.on?.("session_start", (_event, ctx) => {
			if (told) return;
			told = true;
			if (ctx?.hasUI) ctx.ui.notify(warning, "warning");
			else console.warn(warning);
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
