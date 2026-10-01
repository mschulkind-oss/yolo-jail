// opencode on yolo's shared ChatGPT subscription login (the `openai-codex` provider), through
// opencode's OWN client: its built-in `openai` provider and its Responses SDK. yolo re-points
// nothing (docs/design/pi-codex-provider-shadowing.md OQ-2): the opencode derive writes no
// `npm` and no `baseURL` for `openai`. What this server plugin replaces is the one part of
// opencode's built-in ChatGPT support that would otherwise own the credential, its request
// `fetch`, because that fetch refreshes the token itself against a hard-coded
// https://auth.openai.com and stores the refresh token it gets back. yolo's machine-wide
// broker is the only refresh owner (docs/reference/agent-credentials.md OQ-OA1), so this plugin
// asks the broker for the current access token instead, through
// `yolo internal openai-auth-client token`, as pi's extension does (packs/pi/extensions/
// yolo-openai-auth.js). opencode never holds the canonical refresh token (OQ-OA2).
//
// EVERY FACT BELOW WAS READ from opencode 1.18.34 (the installed binary's embedded source,
// and upstream anomalyco/opencode at v1.18.34), never by running it:
//
//   - The built-in ChatGPT support is CodexAuthPlugin (src/plugin/openai/codex.ts). Its auth
//     loader runs only while a credential of type `oauth` is stored under `openai` in
//     $XDG_DATA_HOME/opencode/auth.json (else ~/.local/share/opencode/auth.json), and returns
//     `{ apiKey, fetch }`. That fetch drops any Authorization header, sets the OAuth bearer and
//     ChatGPT-Account-Id, and sends a /v1/responses or /chat/completions request to
//     https://chatgpt.com/backend-api/codex/responses.
//   - Every plugin's auth loader for a provider runs, internal plugins first, and opencode
//     merges their results with remeda's mergeDeep, the later one winning (provider.ts). A
//     plugin in ~/.config/opencode/plugins/ loads after every internal one, so the `fetch`
//     below replaces the built-in one. Nothing else of the built-in changes: its model filter,
//     its `instructions` shaping and its request headers key on the stored `oauth` credential,
//     which the jail's launcher writes (yolo internal openai-auth-client token --opencode-auth).
//   - The login methods a provider shows in /connect are the LAST plugin's for it (opencode's
//     `auth login` takes findLast, ProviderAuth's state fromEntries). So this plugin registers
//     only on yolo's route (onYoloRoute), and then offers yolo's shared login beside the API-key
//     method opencode's own list carries.
//
// THE MODULE CONTRACT: a file plugin default-exports `{ id, server }`; opencode refuses a path
// plugin with no id. Nothing else may be exported.
import { execFile } from "node:child_process";
import { readFileSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

// opencode's provider id for OpenAI, the built-in provider the subscription rides.
const PROVIDER = "openai";

// The refresh value of every credential yolo writes for opencode: a generation marker, never a
// credential (internal/openauthclient's opencode view, the same marker pi's view carries).
const MARKER_PREFIX = "yolo-broker:";

// Where opencode's built-in plugin sends a Responses request on the subscription.
const CODEX_API_ENDPOINT = "https://chatgpt.com/backend-api/codex/responses";

// The value opencode's own plugin hands the SDK in place of an API key on the subscription
// (src/auth/index.ts OAUTH_DUMMY_KEY). The fetch below replaces the header it would make.
const OAUTH_DUMMY_KEY = "opencode-oauth-dummy-key";

// The header opencode's WebSocket transport adds to mark a title request, which its own plugin
// strips before an HTTP request leaves (OpenAIWebSocketPool.withoutInternalHeaders).
const INTERNAL_TITLE_HEADER = "x-opencode-title";

// A token this close to its expiry is asked for again: the broker refreshes inside the same
// five-minute window (docs/reference/agent-credentials.md, "One writer"), so it already holds a
// newer one.
const REFRESH_MARGIN_MS = 5 * 60 * 1000;

// The two routes `yolo internal openai-auth-client` has to the machine's OpenAI login: a jail's
// endpoint file, and the host broker's private socket, which only `yolo host --` sets
// (internal/openauthclient's EndpointEnv and HostSocketEnv).
const BROKER_ENDPOINT_ENV = "YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT";
const HOST_SOCKET_ENV = "YOLO_OPENAI_AUTH_HOST_SOCKET";

// authFile is the file opencode's Auth store reads: xdg-basedir's data dir, then opencode/.
function authFile() {
	const data = process.env.XDG_DATA_HOME || join(homedir(), ".local", "share");
	return join(data, "opencode", "auth.json");
}

// storedCredential is the `openai` entry opencode would read, or undefined. OPENCODE_AUTH_CONTENT
// replaces the file for every read when it is set, so it is read the same way here.
function storedCredential() {
	try {
		const raw = process.env.OPENCODE_AUTH_CONTENT || readFileSync(authFile(), "utf8");
		const entry = JSON.parse(raw)?.[PROVIDER];
		return entry && typeof entry === "object" ? entry : undefined;
	} catch {
		return undefined;
	}
}

// isYoloCredential says whether a stored credential is the view yolo wrote: an oauth entry
// whose refresh value is the broker's generation marker. A credential the user's own login
// stored is never one, and this plugin leaves it to opencode.
function isYoloCredential(entry) {
	return entry?.type === "oauth" && typeof entry.refresh === "string" && entry.refresh.startsWith(MARKER_PREFIX);
}

// onYoloRoute says whether this opencode runs on yolo's shared login: the stored credential is
// yolo's (a jail's launcher wrote it), or `yolo host --` handed it the broker's host socket,
// which it does only for a launch whose opencode selects openai-codex (the pack's prelaunch).
// Anywhere else the plugin registers nothing, and opencode's own OpenAI login is untouched.
function onYoloRoute() {
	return isYoloCredential(storedCredential()) || Boolean(process.env[HOST_SOCKET_ENV]);
}

// inJail says whether this opencode runs inside a yolo jail, by the two witnesses pi's
// extension asks: YOLO_VERSION, and ~/.yolo/bin, which every jail home carries and a real home
// never does (paths.WorkspaceScopeBreach).
function inJail() {
	if (process.env.YOLO_VERSION) return true;
	try {
		return statSync(join(homedir(), ".yolo", "bin")).isDirectory();
	} catch {
		return false;
	}
}

// brokerFailure is the error a failed client call reports, rewriting only the case the client
// cannot word for a person: no route at all, outside a jail, which is opencode started on the
// host without `yolo host`.
function brokerFailure(detail) {
	if (!process.env[BROKER_ENDPOINT_ENV] && !process.env[HOST_SOCKET_ENV] && !inJail()) {
		return new Error(
			"OpenAI credential service: opencode was not started through `yolo host`, so it has no " +
				`route to yolo's shared OpenAI login. Launch it with \`yolo host -- opencode\` (${detail}).`,
		);
	}
	return new Error(`OpenAI credential service: ${detail}`);
}

// brokerCommand runs one client verb and decodes its JSON answer. onStderr, when given, sees
// the client's stderr as it arrives: the login verb prints its authorization URL there.
function brokerCommand(action, onStderr) {
	return new Promise((resolve, reject) => {
		const child = execFile(
			"yolo",
			["internal", "openai-auth-client", action],
			{ encoding: "utf8" },
			(error, stdout, stderr) => {
				if (error) {
					reject(brokerFailure(String(stderr).trim() || error.message));
					return;
				}
				try {
					resolve(JSON.parse(stdout));
				} catch {
					reject(new Error("OpenAI credential service returned malformed JSON"));
				}
			},
		);
		if (onStderr) child.stderr?.on("data", (chunk) => onStderr(String(chunk)));
	});
}

// brokerToken is the broker's current access view, checked the way pi's extension checks it.
async function brokerToken() {
	const view = await brokerCommand("token");
	if (
		typeof view?.access_token !== "string" ||
		view.access_token.length === 0 ||
		typeof view.expires_at !== "number" ||
		!Number.isFinite(view.expires_at) ||
		!Number.isSafeInteger(view.generation) ||
		view.generation <= 0
	) {
		throw new Error("OpenAI credential service returned an invalid token view");
	}
	return {
		access: view.access_token,
		expires: view.expires_at,
		generation: view.generation,
		accountId: typeof view.account_id === "string" && view.account_id.length > 0 ? view.account_id : undefined,
	};
}

// storedView is the credential opencode stores after the login method below: the access view
// and the generation marker, the shape the jail's launcher writes.
function storedView(token) {
	return {
		type: "success",
		refresh: `${MARKER_PREFIX}${token.generation}`,
		access: token.access,
		expires: token.expires,
		...(token.accountId ? { accountId: token.accountId } : {}),
	};
}

// residency is the compute-residency claim of an access token, which opencode's own plugin sends
// as x-openai-internal-codex-residency, or undefined.
function residency(access) {
	try {
		const claims = JSON.parse(Buffer.from(access.split(".")[1], "base64url").toString());
		const value = claims?.["https://api.openai.com/auth"]?.chatgpt_compute_residency ?? claims?.chatgpt_compute_residency;
		return value && value !== "no_constraint" ? value : undefined;
	} catch {
		return undefined;
	}
}

// requestHeaders copies a request's headers without its Authorization, which this fetch sets.
function requestHeaders(init) {
	const headers = new Headers();
	const source = init?.headers;
	if (source instanceof Headers) {
		source.forEach((value, key) => headers.set(key, value));
	} else if (Array.isArray(source)) {
		for (const [key, value] of source) if (value !== undefined) headers.set(key, String(value));
	} else if (source) {
		for (const [key, value] of Object.entries(source)) if (value !== undefined) headers.set(key, String(value));
	}
	headers.delete("authorization");
	headers.delete(INTERNAL_TITLE_HEADER);
	return headers;
}

// brokerFetch is the `fetch` opencode's `openai` SDK runs on the subscription: the access token
// the broker holds, asked for again only near its expiry and never refreshed here.
function brokerFetch() {
	let token;
	let pending;
	async function current() {
		if (token && token.expires - REFRESH_MARGIN_MS > Date.now()) return token;
		pending ??= brokerToken().finally(() => {
			pending = undefined;
		});
		token = await pending;
		return token;
	}
	return async (input, init) => {
		const { access, accountId } = await current();
		const headers = requestHeaders(init);
		headers.set("authorization", `Bearer ${access}`);
		if (accountId) headers.set("ChatGPT-Account-Id", accountId);
		const parsed = input instanceof URL ? input : new URL(typeof input === "string" ? input : input.url);
		const rewrite = parsed.pathname.includes("/v1/responses") || parsed.pathname.includes("/chat/completions");
		if (rewrite) {
			const where = residency(access);
			if (where) headers.set("x-openai-internal-codex-residency", where);
		}
		return fetch(rewrite ? new URL(CODEX_API_ENDPOINT) : parsed, { ...init, body: init?.body, headers });
	};
}

// loginMethod is yolo's shared login in opencode's /connect: the broker's own browser login,
// whose URL the client prints, then the view opencode stores. A machine already logged in needs
// no browser, and the method only stores the view.
const loginMethod = {
	type: "oauth",
	label: "ChatGPT Plus/Pro (yolo shared login)",
	async authorize() {
		const status = await brokerCommand("status");
		if (status?.logged_in === true && status?.login_required !== true) {
			return {
				url: "",
				instructions: "yolo's shared OpenAI login is already signed in on this machine.",
				method: "auto",
				callback: async () => storedView(await brokerToken()),
			};
		}
		let seen;
		const url = new Promise((resolve) => {
			seen = resolve;
		});
		const done = brokerCommand("login", (text) => {
			const match = text.match(/https?:\/\/\S+/);
			if (match) seen(match[0]);
		});
		// A login that ends without printing a URL still answers, so the dialog never waits on it.
		done.then(
			() => seen(""),
			() => seen(""),
		);
		return {
			url: await url,
			instructions: "Sign in in your browser. yolo keeps this one login for every agent on this machine.",
			method: "auto",
			callback: async () => {
				try {
					await done;
					return storedView(await brokerToken());
				} catch {
					return { type: "failed" };
				}
			},
		};
	},
};

export default {
	id: "yolo-openai-auth",
	async server() {
		if (!onYoloRoute()) return {};
		return {
			auth: {
				provider: PROVIDER,
				// Only on yolo's credential: opencode's own login keeps opencode's own fetch.
				async loader(getAuth) {
					if (!isYoloCredential(await getAuth())) return {};
					return { apiKey: OAUTH_DUMMY_KEY, fetch: brokerFetch() };
				},
				methods: [loginMethod, { type: "api", label: "Manually enter API Key" }],
			},
		};
	},
};
