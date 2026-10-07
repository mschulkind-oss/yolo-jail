---
title: "Pi profile selection is not an authentication boundary"
date: 2026-10-06
status: in-review
stage: DESIGN
next: "Rule OQ-PAS1 on whether an active Pi profile restricts calls made with saved credentials"
tags: [pi, profiles, credentials, openai-auth, packs]
summary: "Corrects the diagnosis of simultaneous Pi provider use: rendered selection, pack closure, broker preparation, and credentials already stored by Pi are separate authorities."
---

# Pi profile selection is not an authentication boundary

**Status:** 2026-10-07. Provider visibility and use outside the active profile remain unfixed.
The [provider-only startup flag repair](pi-launch-selection-flags.md) is a separate change:
it does not restrict provider visibility or calls. [OQ-PAS1](#OQ-PAS1) still needs the owner's
policy ruling, including no-profile behavior; saved logins remain untouched.

The earlier diagnosis and its proposed guarantees were rejected by a read-only source audit. The installed Pi source inspected was version 1.0.4, build `7db4cad252707bfe04180f0068579ba855aa1d148be55345446d1fc671264b43`; its fork commit and dirty state are unknown. The incident occurred on another machine, whose Pi build and actual authentication state were not inspected. This document does not attribute an authentication path to that incident.

> **In short.** A Pi profile controls what yolo renders and which credentials yolo newly delivers; it is not a runtime deny rule for credentials Pi already has. Pack closure, broker preparation, and Pi's saved native login are separate facts.

**Why it matters.** A model shown outside the selected profile does not prove a broker grant was made, and the absence of a new grant does not prove Pi cannot authenticate with a saved credential.

**The shape.** Keep four authorities distinct: rendered model selection, selected-pack dependency closure, launch-scoped broker preparation, and Pi's persistent native credentials.

**Cost.** This design leaves provider-use policy open; it does not remove logins, change pack manifests, or claim confinement of same-user processes.

**Start at [§1](#1-four-different-authorities)** — the distinction that replaces the original diagnosis.

**Needs your ruling:** [OQ-PAS1](#OQ-PAS1).

**Reads with:** [`active-provider-sets.md`](active-provider-sets.md) (what an active Pi profile set means), [`providers.md`](../reference/providers.md) (profile credential delivery), and [`pi-host-openai-auth.md`](pi-host-openai-auth.md) (host-side Pi broker preparation and stored-login behavior).

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

## 3. Scope and outstanding policy

The policy boundary under discussion is **normal Pi model calls made through Pi's supported provider runtime**. It is not filesystem confinement against a user who can read the same auth store, nor a claim that a jail process cannot inspect another readable file. Login files remain untouched under either answer.

1. 💬 **OQ-PAS1: Should an active Pi profile set restrict every normal model call to its selected providers, even when Pi has saved native credentials?**

   This would make provider selection an actual runtime policy for Pi rather than only a rendered selection and yolo credential-delivery decision. It must not delete, rewrite, or revoke Pi's saved native or broker-seeded credentials.

   - **Yes:** calls through Pi's supported runtime are limited to the active provider set, despite saved credentials.
   - **No:** profiles continue to control rendered selection and yolo's new credential delivery, while a saved credential may keep another native provider usable.

   With no active profile, the policy also needs a defined behavior rather than an accidental fallback.

   <!-- vantage: question id=OQ-PAS1 leaning="Yes — make an active profile set constrain normal Pi calls, while leaving saved login files untouched; with no profile, preserve Pi's native behavior." -->

   _Leaning:_ Yes: an active profile set should constrain normal Pi model calls even with saved native credentials; no profile should preserve native behavior. Leave login files untouched.

   **Answer:**

   > _(Awaiting the owner's ruling.)_

## 4. Evidence checked

The repository claims above were checked read-only at base `d5bc7a4188badeb56e1a2cb5591916bf69249723` against the Pi pack's declared needs, the Pi derive's prelaunch predicate, host prelaunch composition, Pi's extension registration and broker client, and the broker loophole manifest. The installed Pi source read was limited to provider composition, registration/unregistration, and credential resolution. No credential values, Pi settings, auth files, keychain, broker state, or incident-machine data were read; no agent, provider API, or model was invoked.

The installed source confirms only what that installed 1.0.4 build supports: persistent credential storage, built-in providers surviving removal of an extension override, and stored credentials being considered before ambient auth. The installed fork commit and dirty state are unknown. This cannot establish the other machine's installed build or the incident's actual auth route.
