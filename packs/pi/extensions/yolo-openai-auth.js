import { execFile } from "node:child_process";

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

export default function registerYoloOpenAIAuth(pi) {
	pi.registerProvider("openai-codex", {
		oauth: {
			name: "OpenAI Codex (yolo shared login)",
			isSubscription: true,
			login: (_callbacks) => brokerLogin(),
			refreshToken: (_credentials, signal) => brokerToken(signal),
			getApiKey: (credentials) => credentials.access,
		},
	});
}
