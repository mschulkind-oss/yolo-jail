import { execFile } from "node:child_process";

const BROKER_MARKER = "yolo-broker";

function brokerCommand(action, signal, showStderr = false) {
	return new Promise((resolve, reject) => {
		const child = execFile(
			"yolo",
			["internal", "openai-auth-client", action],
			{ encoding: "utf8", signal },
			(error, stdout, stderr) => {
				if (error) {
					const detail = stderr.trim();
					reject(new Error(detail ? `OpenAI credential service: ${detail}` : `OpenAI credential service: ${error.message}`));
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
		!Number.isFinite(view.expires_at)
	) {
		throw new Error("OpenAI credential service returned an invalid token view");
	}
	return {
		refresh: BROKER_MARKER,
		access: view.access_token,
		expires: view.expires_at,
		...(typeof view.account_id === "string" && view.account_id.length > 0
			? { accountId: view.account_id }
			: {}),
	};
}

export default function registerYoloOpenAIAuth(pi) {
	pi.registerProvider("openai-codex", {
		oauth: {
			name: "OpenAI Codex (yolo shared login)",
			isSubscription: true,
			login: async (_callbacks) => {
				await brokerCommand("login", undefined, true);
				return brokerToken();
			},
			refreshToken: (_credentials, signal) => brokerToken(signal),
			getApiKey: (credentials) => credentials.access,
		},
	});
}
