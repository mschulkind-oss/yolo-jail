---
title: "Why host Pi cannot select OpenAI Codex — and how to seed its auth"
date: 2026-09-30
status: in-review
stage: DESIGN
next: "Rule OQ-1: How the host notch provides Pi with its OAuth credential in auth.json"
tags: [pi, host, openai-auth, credentials, model-picker]
summary: "Pi's runtime gates model selection on credentials in ~/.pi/agent/auth.json. While yolo seeds auth.json inside a jail, yolo host withholds host-file writes under NC-D37, leaving openai-codex unconfigured at the host notch despite the broker socket and extension hooks being active."
---

# Why host Pi cannot select OpenAI Codex — and how to seed its auth

**Status:** DESIGN, 2026-09-30. Nothing built. Evidence verified in tree and live on host.

> **In short.** Pi's runtime gates model availability on credentials present in
> `~/.pi/agent/auth.json`. Inside a jail `yolo` seeds `auth.json` before launch, but
> `yolo host` withholds host file writes under NC-D37, leaving `openai-codex`
> unconfigured and invisible in the model picker despite the running auth broker.

**Why it matters.** Running `yolo host -- pi` under the default `codex` profile boots Pi
into a fallback model (`local/qwen3.8-27b`) and filters out all ChatGPT subscription
models from the `/model` selector, stating *"Only showing models from configured
providers. Use /login to add providers."*

**The shape.** Either re-enable atomic merging into host `~/.pi/agent/auth.json` during host
prelaunch (revising NC-D37), or have Pi's delivered `yolo-openai-auth.js` extension seed
the entry on initialization from `YOLO_OPENAI_AUTH_HOST_SOCKET`.

**Cost.** None for jail launches. On the host, mutates `~/.pi/agent/auth.json` specifically
under the `openai-codex` key while preserving all other provider credentials.

**Start at [§1](#1-the-diagnosis-why-host-pi-ignores-openai-codex)** — the runtime check.
The rest falls out of it.

**Needs your ruling:** [OQ-1](#oq-1), [OQ-2](#oq-2).

**Reads with:** [`notch-convergence.md`](../plans/notch-convergence.md) (NC-D37's host prelaunch rule),
[`pi-codex-provider-shadowing.md`](pi-codex-provider-shadowing.md) (Pi provider catalog layering),
[`model-lists-and-pickers.md`](model-lists-and-pickers.md) (ML-D3, MM-D23).

---

## 1. The Diagnosis: Why host Pi ignores OpenAI Codex

Pi's model discovery and selection logic in `@earendil-works/pi-coding-agent` (measured on
v0.87.1 and v0.99.2) separates provider registration from provider authentication status:

1. **The Extension Layer:** [`packs/pi/extensions/yolo-openai-auth.js`](../../packs/pi/extensions/yolo-openai-auth.js)
   registers `openai-codex` via `pi.registerProvider("openai-codex", { ... })`. It supplies
   the model list from `~/.pi/agent/yolo-openai-codex-models.json` and an `oauth` definition
   containing `login`, `refreshToken`, and `getApiKey` connected to the yolo auth broker.
2. **The Authentication Check:** When Pi computes available models (`ModelRuntime.updateModelSnapshot`
   and `ModelRuntime.runAvailabilityRefresh`), it queries `checkProviderAuth` for each provider:
   ```javascript
   checkProviderAuth(provider, credential, signal) {
       if (credential?.type === "oauth") {
           return provider.auth.oauth ? { source: "OAuth", type: "oauth" } : undefined;
       }
       const apiKey = provider.auth.apiKey;
       if (!apiKey) return undefined;
       ...
   }
   ```
   For any OAuth provider, Pi looks **exclusively** at `credential`, which is read directly from
   `~/.pi/agent/auth.json`. Pi never calls `oauth.login()`, `oauth.refreshToken()`, or `oauth.getApiKey()`
   to probe whether the provider is active or reachable.
3. **The Divergence Between Notches:**
   - **In-Jail:** The launch script ([`internal/entrypoint/shims.go`](../../internal/entrypoint/shims.go) `agentAuthPrelaunchShellFn`)
     executes `yolo internal openai-auth-client token --pi-auth=$HOME/.pi/agent/auth.json` before
     starting Pi. This populates `auth.json` with the broker's current token view.
   - **On Host:** [`internal/openaiauthhost/host.go`](../../internal/openaiauthhost/host.go) (`prepare`)
     follows [NC-D37](../plans/notch-convergence.md#NC-D37), which ruled:
     *"the pi view as the host credential socket, since a jail's pi auth file is the user's own file at the host"*.
     `yolo host` exports `YOLO_OPENAI_AUTH_HOST_SOCKET` but writes nothing to `auth.json`.
4. **The Failure Mode:**
   - On a host where `~/.pi/agent/auth.json` lacks an `openai-codex` entry, Pi marks `openai-codex`
     as unconfigured (`{ configured: false }`).
   - Pi's initial model selector (`findInitialModel`) finds `hasConfiguredAuth("openai-codex") == false`,
     so it rejects the configured default (`openai-codex/gpt-6.1-sol`).
   - Pi falls back to the first available model with valid auth. In `models.json`, `local` declares
     a static `"apiKey": "local"`, which passes auth checks. Pi selects `local/qwen3.8-27b`.
   - When the user opens the model selector, Pi filters out all `openai-codex` models (*"Only showing
     models from configured providers. Use /login to add providers."*), leaving only `local/qwen3.8-27b`.

Running `/login` in Pi interactively on the host was verified live: selecting `OpenAI Codex (yolo shared login)` logged in instantly without prompting for credentials (drawing the token from the host broker socket), saved the credential to `~/.pi/agent/auth.json`, and immediately made `openai-codex` models available in Pi. Manually executing:
```bash
yolo internal openai-auth-client token --pi-auth ~/.pi/agent/auth.json
```
achieves the exact same result because `openauthclient.WritePiAuth` populates `auth.json`.

---

## 2. Secondary Symptom: Catalog Refresh Failures under Credential Scoping

A secondary symptom visible in the same session is:
```text
Could not refresh 3 model catalogs (cerebras, deepseek, openrouter); showing cached models.
```

1. **How it happens:**
   [`packs/pi/derive.lua`](../../packs/pi/derive.lua#L587-L620) renders rows in `~/.pi/agent/models.json`
   for all configured providers, setting `"apiKey": "${CEREBRAS_API_KEY}"`, etc.
2. **Credential Scoping:**
   When running under the `codex` profile, `yolo host` enforces credential scoping ([`provider-credential-scope.md`](provider-credential-scope.md))
   and withholds unselected keys (`CEREBRAS_API_KEY`, `DEEPSEEK_API_KEY`, `OPENROUTER_API_KEY`).
3. **Automatic Refresh:**
   Whenever Pi opens its interactive model selector, `ModelSelectorComponent` calls `refreshModelCatalogs`
   over the network for every entry in `models.json`. Because the environment variables are empty,
   those network calls fail with 401 or invalid auth errors.
4. **Impact:**
   This does not cause the `openai-codex` absence, but it adds noise and false failure warnings on startup.

---

## 3. Potential Solutions

### For the `auth.json` Seeding

1. **Host Prelaunch Seeding (Amend NC-D37):**
   `openaiauthhost.Prepare` at the host notch can call `openauthclient.WritePiAuth` targeting
   `~/.pi/agent/auth.json`.
   - *Pros:* Matches what the jail does; `WritePiAuth` already implements proper file locking (`proper-lockfile`
     compatibility in [`internal/openauthclient/pi.go`](../../internal/openauthclient/pi.go)) and merges into
     existing JSON rather than overwriting other providers.
   - *Cons:* Directly mutates a host file in the user's home directory.

2. **Extension-Side Initialization Seeding:**
   [`yolo-openai-auth.js`](../../packs/pi/extensions/yolo-openai-auth.js) can inspect `~/.pi/agent/auth.json`
   on load. If running on the host (`HOST_SOCKET_ENV` set) and `auth.json` lacks `openai-codex`, it can call
   `brokerToken()` and merge the entry using Node's filesystem APIs.
   - *Pros:* Confined to the Pi pack's own extension lifecycle.
   - *Cons:* Re-implements lockfile handling or risks racing Pi's native `AuthStorage`.

3. **Temporary Managed Home or Auth Path Override:**
   Point Pi at a managed auth file or directory when running via `yolo host -- pi`.
   - *Pros:* Fully isolates host user configuration.
   - *Cons:* Pi does not have a native `--auth-path` CLI flag without wrappers, and isolating home splits
     session history and skills.

### For Catalog Refresh Noise

1. **Filter `models.json` at Host Notch by Active Profile:**
   Only write providers into `models.json` if their credentials are provided by the active profile or
   if their API key is statically configured (e.g. `local`).
2. **Accept Warning as Harmless:**
   Pi gracefully degrades to cached models when a remote catalog refresh fails.

---

## Open Questions

1. 💬 **OQ-1: How host Pi receives its initial `openai-codex` credential.**
   Pi requires an entry in `~/.pi/agent/auth.json` to recognize `openai-codex` as configured, but NC-D37
   prohibited host-file writes during `yolo host` prelaunch. What should bridge this gap?

   - **A — Host prelaunch merges `auth.json` (amend NC-D37).** `openaiauthhost.Prepare` invokes
     `openauthclient.WritePiAuth(filepath.Join(paths.Home(), ".pi/agent/auth.json"), response)`.
     `WritePiAuth` safely takes Pi's `auth.json.lock` and preserves other providers.
   - **B — `yolo-openai-auth.js` seeds `auth.json` on extension load.** The JavaScript extension
     checks if `auth.json` has `openai-codex`; if missing, it calls `brokerToken()` and writes it.
   - **C — Require explicit `/login` once on the host.** Document that users must run `/login` in Pi
     once on the host, which calls `yolo-openai-auth.js`'s `brokerLogin()` without a browser prompt.

   <!-- vantage: oq id=OQ-1 leaning="A — Host prelaunch merges auth.json (amend NC-D37). WritePiAuth was specifically designed to safely merge credentials under Pi's lock rule without clobbering other providers." -->

   _Leaning:_ A — Host prelaunch merges `auth.json` (amend NC-D37). `WritePiAuth` was specifically
   built to safely merge the broker token under Pi's own lockfile semantics without clobbering existing
   host credentials.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-2: Handling catalog refresh failures for unscoped providers.**
   Under credential scoping, `models.json` entries for unselected providers cause Pi's model picker
   to emit background refresh warnings. Should yolo suppress unselected providers in `models.json`?

   - **A — Only render providers with available credentials in `models.json`.** At the host notch
     (and in-jail), omit providers from `models.json` if their keys are withheld by credential scoping.
   - **B — Leave `models.json` intact.** The failure is non-fatal and Pi displays cached models;
     preserving the entries allows mid-session profile or key changes.

   <!-- vantage: oq id=OQ-2 leaning="A — Only render providers with available credentials in models.json, suppressing unnecessary network failures for withheld credentials." -->

   _Leaning:_ A — Only render providers with available credentials in `models.json`, avoiding noisy
   catalog refresh errors.

   **Answer:**
   > _(empty — fill in when decided)_
