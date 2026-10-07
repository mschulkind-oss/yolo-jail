---
title: "Pi's provider flag travels only with an explicit model"
date: 2026-10-06
status: in-review
stage: BUILT
next: "Complete combined landing and nested-jail checks, then graduate this built design to the relevant reference"
tags: [pi, host, launch-selection, cli-flags]
summary: "A provider-only Pi profile must not become an orphan --provider argument. The launch-selection declaration expresses this generic flag-presence dependency without inventing a model or changing Pi's native settings."
---

# Pi's provider flag travels only with an explicit model

**Status:** 2026-10-07. Focused source tests and independent review are complete. **UNMEASURED:** Combined landing checks and nested-jail verification remain; no Pi process or model request was made.

> **In short.** A host launch hands Pi `--provider` only when the composed selection also has `defaultModel`. A provider-only selection keeps its existing `--models` scope, leaving Pi to resolve its own startup model.

**Why it matters.** Pi treats `--provider` as a qualifier for `--model`, not as a standalone provider default. Handing a provider-only profile as `--provider deepseek --models deepseek/*` is rejected during session setup.

**The shape.** `LaunchSelectionFlag.requires` names other selection keys whose presence is required before that flag is emitted. The Pi pack declares `defaultProvider` requires `defaultModel`; all unrelated selection flags keep their existing transport.

**Cost.** Provider-only handoff no longer independently sets Pi's provider. No synthetic `--model` is added, and the native settings file is not edited by a host launch.

**Start at [the flag-presence rule](#1-flag-presence-and-pi).**

**Needs your ruling:** None. This closes only the transport question; it does not decide that a profile locks resumed or saved sessions to its provider.

**Reads with:** [`model-lists-and-pickers.md`](model-lists-and-pickers.md) (the host handoff contract).

---

## 1. Flag presence and Pi

`yolo host -p` composes a pack's selection and, only when it differs from the configured profile's selection, hands it through the pack's declared `launch_selection` form. For the flags form, a declaration may give a flag a `requires` list. The flag is emitted only when its own selection key and every required selection key are present in the handoff (including declared defaults); other flags are evaluated independently. An absent prerequisite suppresses that flag, not the rest of the selection.

Pi declares:

- `defaultProvider` as `--provider {value}`, requiring `defaultModel`;
- `defaultModel` as `--model {value}`;
- `enabledModels` as `--models {value}`.

Therefore a provider-only selection such as `defaultProvider=deepseek` with `enabledModels=["deepseek/*"]` hands only `--models deepseek/*`. When `defaultModel` is explicit, Pi still receives the provider-qualified pair `--provider <provider> --model <id>`. Keeping the provider on that explicit path matters when providers expose the same model ID: the list scope does not qualify Pi's explicit `--model` lookup. The other declared list flags and variables are unchanged.

This is a transport repair, not a provider lock or authorization boundary. It does not choose a model, turn `--model "*"` into a wildcard, change the native `settings.json`, or promise that Pi will replace a saved in-scope selection or a resumed session. `--models` is a scope; Pi's native startup/session behavior remains in control. If a composed selection has no explicit model and no usable scope, yolo does not fabricate one; the provider flag is omitted and Pi retains its own configuration behavior.

With no differing profile selection, with a profile that composes no selection, or when a declared subcommand is first in the user's argv, the existing host launch-selection rules still hand no injected Pi flags. Explicit user arguments remain after the injected words and retain existing precedence.

## 2. Evidence and limits

The production path is `internal/cli/host.go` → `hostmodelmenu.go` → `entrypoint.HostLaunchSelection` → `LaunchSelection.Argv`, with flag grammar and validation in `packdecl`. The Pi consumer is declared in `packs/pi/pack.json`; the derive continues to supply the existing provider, model (when explicit), and scope selection in `packs/pi/derive.lua`.

The source audit read the installed `@earendil-works/pi-coding-agent` **1.0.4** source and bundle, whose runtime build reported `forkCommit: null`. It found Pi's provider/model coupling check during session setup and its explicit-model resolver does not use the `--models` scope to disambiguate overlapping IDs. This evidence is version-bounded: it does not identify another machine's fork revision and cannot establish that runtime's behavior. The implementation makes no API or inference requests.

Regression coverage exercises declaration decoding/validation and cloning, argv for a provider-only selection, explicit overlapping IDs and a missing prerequisite while retaining `--models`, and the production `yolo host` exec caller with a provider-only profile. It also verifies the native settings file remains unchanged.
