---
title: "An active Pi profile must constrain supported model calls, not saved logins"
date: 2026-10-06
status: in-review
stage: DESIGN
next: "Rule OQ-PAS2: enforce the active set inside yolo alone, or with a Pi runtime change"
tags: [pi, profiles, credentials, openai-auth, packs]
summary: "Corrects the diagnosis of simultaneous Pi provider use: rendered selection, pack closure, broker preparation, and credentials already stored by Pi are separate authorities."
---

# An active Pi profile must constrain supported model calls, not saved logins

**Status:** 2026-10-08. Owner policy is settled ([OQ-PAS1](#decision-ledger)); enforcement is unbuilt.
Where it is enforced is open again: the maintainer wants it contained in yolo ([OQ-PAS2](#OQ-PAS2)).
The [startup flag repair](pi-launch-selection-flags.md) remains separate and does not restrict calls.
Installed Pi 1.0.4 source was rechecked without reading auth/settings files or invoking a model.

> **In short.** An active profile set must deny normal supported Pi calls to other providers even
> with saved credentials. With no active profile, preserve Pi's native behavior and login files.

**Why it matters.** Picker selection and absence of a new broker grant cannot deny an already-saved login.

**The shape.** Yolo supplies launch-scoped provider IDs; Pi checks the final dispatch provider before
request authentication, independently of model menus and credential storage.

**Cost.** As written, a Pi runtime/API change: existing extension notification hooks cannot enforce
this rule. [OQ-PAS2](#OQ-PAS2) asks whether a yolo-only route is preferable.

**Start at [§3](#3-accepted-provider-use-policy)** — the supported-call boundary and denial behavior.

**Needs your ruling:** [OQ-PAS2](#OQ-PAS2).

**Reads with:** [implementation handoff](simultaneous-auth-and-pack-isolation-plan.md),
[`active-provider-sets.md`](active-provider-sets.md) (active-set semantics),
[`providers.md`](../reference/providers.md) (credential delivery),
[`pi-host-openai-auth.md`](pi-host-openai-auth.md) (stored-login behavior).

---

The earlier diagnosis and proposed guarantees were rejected by a read-only source audit. The prior
installed Pi source was version 1.0.4, build `7db4cad252707bfe04180f0068579ba855aa1d148be55345446d1fc671264b43`;
its fork commit/dirty state are unknown. The incident machine's build and authentication state were
not inspected. Neither the original audit nor this design attributes an auth path to that incident.

---

## 1. Four different authorities

### 1.1 Rendered selection is presentation and model policy, not provider denial

Pi's `enabledModels` scopes its initial model view; it is not an access-control list. Pi can show models outside that view, and switching to one checks whether it can authenticate rather than whether yolo selected its provider. The Pi pack's OpenAI extension also registers `openai-codex` when its rendered model data is absent or empty; an empty model list does not disable Pi's built-in provider.

Yolo may render profile-specific default models, lists, and model-level enforcement. Those are not a rule that forbids every call to a provider absent from the active profile. In particular, a provider/model shortlist must not be described as a provider ACL.

### 1.2 Pack closure describes selected contributions, not per-launch authorization

The workspace's configured packs are selected and their declared dependencies are closed. The Pi pack itself unconditionally needs `bedrock` and `openai-auth` ([`packs/pi/pack.json`](../../packs/pi/pack.json)); selecting Pi therefore includes those dependencies without Claude. Claude can add dependencies of its own, but removing Claude does not remove Pi's `openai-auth` or Bedrock needs. Pi does not declare `wire-bridge` as a need.

The presence of `openai-auth` in the selected pack closure means its declared contributions participate. It does not by itself establish that this Pi launch received a fresh broker-backed login or made a broker request.

### 1.3 Broker preparation is launch-scoped; a service route is a separate doorway

At the host notch, Pi's derive declares the OpenAI auth prelaunch only when `openai-codex` is the selected provider or an entry in Pi's active set ([`packs/pi/derive.lua`](../../packs/pi/derive.lua)). Host preparation follows those composed declarations rather than treating every Pi launch as a broker grant ([`internal/cli/host.go`](../../internal/cli/host.go), [`internal/openaiauthhost/host.go`](../../internal/openaiauthhost/host.go)). A host launch on DeepSeek alone therefore does not declare that Pi prelaunch.

The broker service and the jail-facing endpoint are distinct from the host prelaunch: selected and enabled loophole contributions can make a route available independently of whether a particular Pi profile declares a new login preparation. The OpenAI broker is a machine-wide refresh owner ([its manifest](../../packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc)); route availability alone does not prove that Pi used it or that a new credential was issued.

### 1.4 Pi's saved native login can work without a new broker grant

The installed Pi 1.0.4 source creates persistent credential storage and loads built-in providers. Its credential resolver checks stored credentials before ambient authentication; a usable stored OAuth token can authenticate a request without first asking yolo's broker. Yolo's OpenAI extension also returns a stored access token directly and calls the broker for login or refresh, not for every request ([`packs/pi/extensions/yolo-openai-auth.js`](../../packs/pi/extensions/yolo-openai-auth.js)).

Accordingly, a native Pi login or a broker-seeded credential already in Pi's auth store may remain usable when a later launch does not declare a broker prelaunch. If that stored token needs refresh, its registered refresh path may still involve the broker. Neither possibility was inspected on the incident machine, and neither is evidence of the incident's actual authentication state.

Pi retains built-in providers independently of yolo's provider override. In the inspected Pi source, unregistering an extension provider removes the override and recomposes the provider; it does not remove Pi's built-in definition. Therefore, omitting or removing yolo's registration is not a reliable way to disable native authentication.

## 2. What this design does not claim

- Removing Claude is not a fix for Pi's OpenAI authority: Pi itself needs `openai-auth`.
- Joining `openai-auth` does not prove host Pi received a new broker login view. The host prelaunch is gated by Pi's active provider set.
- A profile's picker selection, rendered model list, or model-level enforcement is not a general provider access-control boundary.
- Masking `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT` or `YOLO_OPENAI_AUTH_HOST_SOCKET` can affect yolo's broker client route. It does not erase saved credentials, disable Pi's native provider, or confine arbitrary same-UID code. Clearing one route alone also does not establish the other is absent.
- No claim here establishes which path the other machine's Pi used. This is a source/design audit, not an inspection of the incident's environment, Pi settings, auth files, credential store, process state, or network traffic.
- No saved login should be deleted or rewritten to implement a launch policy. Arbitrary host code and same-UID jail processes are outside a Pi profile-selection guarantee.

## 3. Accepted provider-use policy

The policy boundary is **normal Pi model calls made through Pi's supported provider runtime**.
An active Pi profile set must restrict those calls to its selected providers even when Pi has
saved native or broker-seeded credentials. With no active profile, preserve Pi's native behavior.
Do not delete, rewrite, or revoke saved login files to enforce the launch policy.

This is not filesystem confinement against a user who can read the same auth store, nor a claim
that a jail process cannot inspect another readable file. Runtime enforcement and verification
are still owed; rendered selection and broker-route masking alone do not satisfy this policy.
This closes [OQ-PAS1](#decision-ledger), not the implementation.

### 3.1 The request boundary

- **With an active set:** allow only its normalized Pi provider IDs. Duplicate profiles on one
  provider deduplicate; existing per-provider model policy remains additional, not a substitute.
- **Without a profile:** no yolo provider restriction. Do not infer one from selected pack closure,
  saved credentials, rendered catalog rows or a previous invocation's persistent selection.
- **At dispatch:** check the actual physical provider after virtual routing on every request, retry
  and continuation. A previously selected/resumed out-of-set model cannot bypass this check.
- **Supported calls:** Pi's normal runtime dispatch, including assistant/compaction/summary/cache-warm
  calls and extension/SDK/codemode chat, image and classifier calls through its model runtime. Deferred
  fetch/cancel of a model request uses the same provider boundary. A router's classifier is itself a
  model call; allowing the final chat route does not exempt its auxiliary provider.
- **Before request authentication:** deny without invoking that request's credential resolver, token
  refresh, credential command or provider network transport. Native startup catalog/auth availability
  discovery and explicit login/logout are not request dispatch; do not claim this policy suppresses all
  auth activity or network traffic in the process.
- **Failure:** invalid/empty active-set policy is an error, not no-profile fallback. Missing enforcement
  capability under an active-set launch refuses the managed launch and names the compatible Pi build.
  Policy-hook errors deny; denials are non-retryable with the provider and the next profile-selection
  step, never tokens/headers. Do not silently switch to another provider or grant.

The policy belongs to the process invocation. Host `-p` overrides must not write persistent Pi
settings; ordinary child Pi/SDK runtimes inherit and validate the same launch policy. Reload/session
replacement keeps it. Explicitly disabling extensions must not silently disable the request boundary.
A snapshot of one launch does not retroactively change another process already running.

### 3.2 What does not enforce the rule

The inspected supported `before_provider_request` event carries a payload, not a dispatch identity
or deny result. `ExtensionRunner.emitBeforeProviderRequest` catches handler errors and continues.
Throwing from that handler is therefore **not enforcement**. `before_agent_start`/`model_select`
likewise cannot cover nested runtime calls, warming or later routed requests. Provider unregister
restores built-ins; `streamSimple` overrides only registered providers/APIs. None is a universal gate.

The grounded dispatch seam is Pi's common `ModelRuntime.prepareRequest`, **before its `getAuth`**.
Its supported `ModelRegistry` facade delegates model calls there; no existing public policy-registration
API was found in the inspected declarations. But this gate alone is not a zero-auth-work guarantee:
`AgentSession._getRequiredRequestAuth` and `_getSummarizationRequestAuth` resolve runtime `getAuth`
earlier. Guard those preflight resolutions before credential lookup/refresh too; summarization's
ordinary-auth fallback must propagate policy denial. Add dedicated fail-closed supported source
seams, not monkeypatches or changed notification semantics. Source map and repo ownership are in
the [plan](simultaneous-auth-and-pack-isolation-plan.md).

### 3.3 Explicit exclusions

No saved login is deleted, rewritten, revoked, or widened to enforce selection. Ordinary allowed
Pi authentication keeps its native behavior; the policy does not install an alternative credential
store (unless [OQ-PAS2](#OQ-PAS2) chooses the yolo-only route, which leaves saved files untouched
but hands Pi a per-launch view). This is not a same-UID filesystem/network boundary. Deliberate standalone `pi-ai` calls,
arbitrary HTTP clients, trusted code bypassing the managed runtime, or a manually unconfigured
process are outside the supported-call guarantee. Menus may aid discovery but their contents are
not acceptance evidence. No incident-machine repair or live provider/account proof is claimed.

## Open questions

1. 💬 **OQ-PAS2: Enforce the active set inside yolo alone, or by changing Pi?** <a id="OQ-PAS2"></a>

   With the `codex` profile active and a saved Anthropic login, you pick a Claude model in Pi.

   - **A — Yolo only.** A per-launch view of Pi's login store holds only the active set's
     providers; other providers' key variables are dropped. Pi is unchanged. **You'd see:** Pi's
     own "No API key found" or "run /login". **Cost:** an auth failure, not a denial; `/login`,
     Bedrock/Vertex ambient credentials and keys in `models.json` get around it.
   - **B — Patch Pi** (as written). **You'd see:** a yolo denial naming the profile. **Cost:** a
     Pi patch series to carry; a stock npm Pi must refuse every profile launch.

   <!-- vantage: question id=OQ-PAS2 leaning="None yet: A keeps Pi unmodified but only narrows access; B enforces the ruled policy but ties profiles to yolo's Pi build." -->

   _Leaning:_ none yet. A keeps Pi unmodified but only narrows access; B enforces the ruled
   policy but ties profiles to yolo's Pi build. B is already built, so either is cheap.

   **Answer:**

   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-PAS1 | Owner: yes, an active profile set constrains normal supported Pi calls despite saved credentials; no-profile launches preserve native behavior and saved login files remain untouched. Vantage comment `e5b4ea51`, round 0 | 2026-10-07 | [§3](#3-accepted-provider-use-policy) | — |

## 4. Evidence checked

The repository claims above were checked read-only at base `d5bc7a4188badeb56e1a2cb5591916bf69249723` against the Pi pack's declared needs, the Pi derive's prelaunch predicate, host prelaunch composition, Pi's extension registration and broker client, and the broker loophole manifest. The installed Pi source read was limited to provider composition, registration/unregistration, and credential resolution. No credential values, Pi settings, auth files, keychain, broker state, or incident-machine data were read; no agent, provider API, or model was invoked.

The installed source confirms only what that installed 1.0.4 build supports: persistent credential storage, built-in providers surviving removal of an extension override, and stored credentials being considered before ambient auth. The installed fork commit and dirty state are unknown. This cannot establish the other machine's installed build or the incident's actual auth route.

On 2026-10-07, the installed public extension docs/declarations, request-event runner, SDK wiring,
model-registry facade, agent-session auth preflights and model-runtime dispatch were rechecked. Package metadata names the source
repository [earendil-works/pi](https://github.com/earendil-works/pi). The configured `pi-fork` source
pack independently declares that upstream's `main` plus a format-patch series; it is not a distinct
GitHub fork. Its first patch's base is recorded in the [plan](simultaneous-auth-and-pack-isolation-plan.md),
not assumed to identify installed bytes. A scout must pin/replay the declared source and series in a
writable candidate before assigning the API writer. No installed Pi file was edited; no auth/settings
file, account endpoint or live model was used.
