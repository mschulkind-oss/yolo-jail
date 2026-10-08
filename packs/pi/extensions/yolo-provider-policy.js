import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

// THE ACTIVE SET'S PROVIDER POLICY (docs/design/simultaneous-auth-and-pack-isolation.md §3). With
// an active profile set, pi may call only the set's providers, even where pi holds a saved login
// for another. yolo hands the set's pi provider IDs in this variable (packs/pi/derive.lua's
// yolo.env, for a jail boot and for every `yolo host -- pi` launch alike); with no profile it is
// absent and this extension does nothing, so pi keeps its native behavior.
//
// The document: {"schemaVersion":1,"mode":"allowlist","allowedProviderIds":[...],"profiles":[...]}.
// A document this file cannot read is INVALID, never absent: every provider is blocked, because
// the launch asked for a restriction and an unreadable one must not run unrestricted.
const POLICY_ENV = "YOLO_PI_PROVIDER_POLICY";

// The name every blocking provider's key method carries. pi's composer keeps the base provider's
// key-method name when models.json overlays a row on it (provider-composer.js, composeApiKeyAuth),
// so the name also says, after composition, that the provider pi dispatches to is still this one.
const BLOCK_NAME = "blocked by yolo's profile set";

// The api of a virtual model (pi core/virtual-models.js, VIRTUAL_MODEL_API). A virtual model routes
// each request to a physical model; the request is checked at that physical model's provider.
const VIRTUAL_API = "pi-virtual";

// readPolicy returns undefined when the launch set no policy, else { allowed, profiles, invalid }.
export function readPolicy(env = process.env) {
	const raw = env[POLICY_ENV];
	if (raw === undefined) return undefined;
	let doc;
	try {
		doc = JSON.parse(raw);
	} catch {
		doc = undefined;
	}
	const ids = doc?.allowedProviderIds;
	const valid =
		doc?.schemaVersion === 1 &&
		doc?.mode === "allowlist" &&
		Array.isArray(ids) &&
		ids.length > 0 &&
		ids.every((id) => typeof id === "string" && id.length > 0);
	const profiles = Array.isArray(doc?.profiles) ? doc.profiles.filter((p) => typeof p === "string" && p) : [];
	return valid ? { allowed: new Set(ids), profiles, invalid: false } : { allowed: new Set(), profiles, invalid: true };
}

// denial is the message a refused call ends with: which provider, which set, what it allows, and
// the way out. It names no credential and no request detail, and avoids every word pi's retry
// classifier reads as transient, so pi does not retry it.
export function denial(policy, provider) {
	if (policy.invalid) {
		return (
			`yolo: this launch's profile-set policy (${POLICY_ENV}) is unreadable, so pi may call no provider, ` +
			`"${provider}" included. Relaunch pi through yolo (\`yolo host -- pi\`, or a jail).`
		);
	}
	const allowed = [...policy.allowed].sort().join(", ");
	const set = policy.profiles.length > 0 ? `profile set (${policy.profiles.join(", ")})` : "profile set";
	return (
		`yolo: provider "${provider}" is outside this launch's ${set}, which allows only ${allowed}. ` +
		`Pick a model of those providers with /model, or relaunch with a set that includes "${provider}" ` +
		`(\`yolo host -p pi=<profile>[,<profile>...] -- pi\`, or the workspace's "profile").`
	);
}

// blockingProvider is the provider pi dispatches to for a provider outside the set. It is
// registered as a NATIVE provider (pi's one-argument registerProvider), which takes the place of
// pi's built-in one (ModelRuntime.composeProvider: an extension's native provider, else the
// built-in). Every request pi makes goes through ModelRuntime.prepareRequest, which resolves this
// provider's auth before it calls any provider method, as do pi's own pre-flights
// (AgentSession._getRequiredRequestAuth, ModelRegistry.getApiKeyAndHeaders): so the denial is
// thrown from AUTH, before any request is built and before pi's own credential code for the
// provider runs. pi wraps it as "API key auth failed for provider <id>: <denial>" (pi-ai
// ModelsError keeps the cause in the message).
//
//   - The key method reports the provider configured, so pi gets as far as asking for the auth
//     and the user reads the denial, not pi's "No API key found ... /login".
//   - filterModels returns no model, so /model and the available list hide the provider. The
//     catalog stays, so a resumed or `--model` selection still names a model and meets the
//     denial.
//   - The OAuth method exists so a saved OAuth login also counts configured and also meets the
//     denial (pi-ai's resolver hands a stored OAuth credential only to an OAuth method). Its
//     refresh throws too: a stored token near expiry takes pi's credential lock, and the refresh
//     refuses inside it, so nothing is written and nothing is sent.
//   - Logins refuse, so /login cannot store a credential for the provider this launch.
//   - No refreshModels: a dynamic catalog is not fetched with the provider's credential.
export function blockingProvider(id, base, message) {
	const refuse = () => {
		throw new Error(message);
	};
	const refuseAsync = async () => refuse();
	const configured = async () => ({ type: "api_key", source: BLOCK_NAME });
	const provider = {
		id,
		name: base?.name ?? id,
		...(base?.baseUrl !== undefined ? { baseUrl: base.baseUrl } : {}),
		...(base?.headers !== undefined ? { headers: base.headers } : {}),
		auth: {
			apiKey: { name: BLOCK_NAME, login: refuseAsync, check: configured, resolve: refuseAsync },
			oauth: { name: BLOCK_NAME, login: refuseAsync, refresh: refuseAsync, toAuth: refuseAsync },
		},
		getModels: () => safeModels(() => base?.getModels?.()),
		getAllModels: () => safeModels(() => base?.getAllModels?.() ?? base?.getModels?.()),
		filterModels: () => [],
		filterAllModels: () => [],
		stream: refuse,
		streamSimple: refuse,
		fetchDeferred: refuse,
		cancelDeferred: refuseAsync,
		generateImages: refuseAsync,
		classify: refuseAsync,
	};
	return provider;
}

function safeModels(read) {
	try {
		const models = read();
		return Array.isArray(models) ? [...models] : [];
	} catch {
		return [];
	}
}

// isBlocking says whether pi's provider for an id is still this extension's (composed or not).
function isBlocking(provider) {
	return provider?.auth?.apiKey?.name === BLOCK_NAME;
}

// builtinProviders returns pi's own providers by id, or an empty map when pi exports none. The
// specifier resolves through the alias pi's extension loader installs for its own packages.
async function builtinProviders() {
	try {
		const all = await import("@earendil-works/pi-ai/providers/all");
		const list = typeof all.builtinProviders === "function" ? all.builtinProviders() : [];
		return new Map(list.filter((p) => typeof p?.id === "string").map((p) => [p.id, p]));
	} catch {
		return new Map();
	}
}

// modelsJsonProviderIds returns the provider IDs pi's models.json names: a row there defines a
// provider pi has no built-in for (yolo's own catalogue of a configured provider, say), which
// needs its block before the first request as much as a built-in does.
function modelsJsonProviderIds() {
	const dir = process.env.PI_CODING_AGENT_DIR || join(homedir(), ".pi", "agent");
	try {
		const providers = JSON.parse(readFileSync(join(dir, "models.json"), "utf8"))?.providers;
		return providers && typeof providers === "object" ? Object.keys(providers) : [];
	} catch {
		return [];
	}
}

// lacksNativeProviders says whether this pi is KNOWN to be unable to take a provider object: its
// package root imports and its ModelRuntime has no registerNativeProvider, which pi 0.81.0 added
// (extensions/yolo-openai-auth.js, piTakesNativeProviders, measured there). Such a pi queues the
// object as a name and drops it, so the block never applies. A root that does not import says
// nothing either way.
async function lacksNativeProviders() {
	let root;
	try {
		root = await import("@earendil-works/pi-coding-agent");
	} catch {
		return false;
	}
	return typeof root?.ModelRuntime?.prototype?.registerNativeProvider !== "function";
}

// pi awaits an extension's factory (core/extensions/loader.js), so the registrations below are
// queued before pi applies any, and pi applies native ones after every ProviderConfig
// registration, which a native registration replaces.
//
// THEN AGAIN ON EVERY session_start, model_select, input, before_agent_start and turn_start: a
// provider another extension registered (or registered again) after this one, or one only the
// live registry knows, gets its block there, before the prompt or turn that would use it. A virtual model is never blocked by its own
// provider ID: its request is checked at the physical model it routes to.
export default async function registerYoloProviderPolicy(pi) {
	const policy = readPolicy();
	if (!policy) return;
	const blocked = (id) => typeof id === "string" && !policy.allowed.has(id);
	const unenforceable = await lacksNativeProviders();
	const builtins = await builtinProviders();
	for (const id of new Set([...builtins.keys(), ...modelsJsonProviderIds()])) {
		if (blocked(id)) pi.registerProvider(blockingProvider(id, builtins.get(id), denial(policy, id)));
	}

	const reassert = (ctx) => {
		const registry = ctx?.modelRegistry;
		if (!registry) return;
		const ids = new Set();
		try {
			for (const model of registry.getAll?.() ?? []) ids.add(model?.provider);
			for (const id of registry.getRegisteredProviderIds?.() ?? []) ids.add(id);
		} catch {
			return;
		}
		for (const id of ids) {
			if (!blocked(id)) continue;
			const current = registry.getProvider?.(id);
			if (isBlocking(current)) continue;
			try {
				registry.registerProvider(blockingProvider(id, current ?? builtins.get(id), denial(policy, id)));
			} catch (error) {
				warn(ctx, `yolo: could not block provider "${id}" outside the profile set: ${error?.message ?? error}`);
			}
		}
	};

	let told = false;
	pi.on?.("session_start", (_event, ctx) => {
		reassert(ctx);
		if (told) return;
		told = true;
		if (unenforceable) {
			warn(
				ctx,
				"yolo: this pi is too old to take yolo's profile-set provider block, so providers outside the " +
					"set are NOT refused. Update pi (`pi update`) to enforce it.",
			);
		}
		if (policy.invalid) warn(ctx, denial(policy, "every provider"));
	});
	pi.on?.("model_select", (event, ctx) => {
		reassert(ctx);
		const model = event?.model;
		if (model && model.api !== VIRTUAL_API && blocked(model.provider)) {
			warn(ctx, denial(policy, model.provider));
		}
	});
	for (const event of ["input", "before_agent_start", "turn_start"]) {
		pi.on?.(event, (_event, ctx) => {
			reassert(ctx);
		});
	}
}

function warn(ctx, message) {
	if (ctx?.hasUI) ctx.ui.notify(message, "warning");
	else console.warn(message);
}
