---
title: "Provider switching — graduated to the providers reference"
date: 2026-09-04
status: accepted
tags: [design, providers, profiles, selection, deselection, graduated]
summary: "A stub. The deselection rule (clear the model id yolo wrote, keep the one you wrote), the host-layer override, codex's openai-codex selection and the OQ-PSW2, OQ-PSW4 and OQ-SW1 rulings now live in docs/reference/providers.md. This filename survives only while inbound references held by other work are repointed."
---

# Provider switching — graduated to the providers reference

**Status:** GRADUATED, 2026-09-26 → [`../reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote`](../reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote).
Nothing is owed here.

> [!IMPORTANT]
> **The design is BUILT, and this file no longer describes it.** The as-built account is in
> [`../reference/providers.md`](../reference/providers.md). It covers the deselection clear, the
> host-layer override, codex's `openai-codex` selection, and one gap the build left: the clear does
> not hold on an adopting boot. The rulings keep their ids in that doc's
> [*Why it's this way*](../reference/providers.md#why-its-this-way) appendix.

**Why it still exists.** Some files that link here are held by in-flight work and could not be
repointed in the graduation. Point each at the anchor named below, then **delete this file**.

## Where each id and section went

- <a id="decision-ledger"></a>**The decision ledger**, which every [`OQ-PSW2`](../reference/providers.md#oq-psw2)
  citation linked → the reference's [*Why it's this way*](../reference/providers.md#why-its-this-way)
  appendix. Point each such citation at [`#oq-psw2`](../reference/providers.md#oq-psw2).
- <a id="OQ-PSW4"></a>[**OQ-PSW4**](../reference/providers.md#oq-psw4) (the clear is silent on the
  terminal and recorded in `boot.log`) → `providers.md#oq-psw4`.
- <a id="OQ-SW1"></a>[**OQ-SW1**](../reference/providers.md#oq-sw1) (a selection outranks a
  host-layer value) → `providers.md#oq-sw1`.
- <a id="OQ-PSW5"></a>[**OQ-PSW5**](provider-credential-scope.md#where-the-rest-went) (which
  credentials a profile lets reach an agent) never moved to the reference: it was split into
  [`provider-credential-scope.md`](provider-credential-scope.md) on 2026-09-22, and that doc's own
  questions carry it.
- <a id="41-native-first-party-provider-resolution-openai-codex-for-codex"></a>**Section 4.1** of the
  old body (codex's `openai-codex` selection, which Lua and Go comments cite by that number) →
  [`providers.md#selecting-openai-codex-for-codex`](../reference/providers.md#selecting-openai-codex-for-codex).
- **The rejected "refuse a launch on a model id outside the provider's `models`" alternative** →
  [`providers.md#no-launch-time-model-id-refusal`](../reference/providers.md#no-launch-time-model-id-refusal).
