---
title: "Pi host launch selection: decoupling or pairing --provider and --model"
date: 2026-10-06
status: in-review
stage: DESIGN
next: "Rule OQ-1 on how packdecl.LaunchSelection or packs/pi handles flag pairing"
tags: [pi, host, launch-selection, model-picker, cli-flags]
summary: "yolo host -p <profile> hands pi its selection via CLI flags declared in packs/pi/pack.json under MM-D30. For a built-in provider with no explicit model, defaultProvider is emitted as --provider and enabledModels as --models while defaultModel is omitted. Pi's CLI parser treats --provider solely as a qualifier for --model and errors with '--provider requires --model'. This document designs the fix."
---

# Pi host launch selection: decoupling or pairing --provider and --model

**Status:** 2026-10-06. Drafted following live host failure on `yolo host -p deepseek -- pi`.
[OQ-1](#OQ-1) is open for ruling on the flag-coupling mechanism.

> **In short.** At `yolo host`, `launch_selection` passes `defaultProvider` as `--provider`
> and `enabledModels` as `--models`. In Pi's CLI, `--provider` is not an independent flag;
> it only exists to scope `--model <pattern>`. Passing `--provider` without `--model`
> fails immediately at Pi's argument parser. Either `packdecl.LaunchSelection` must support
> coupled/conditional flags, `packs/pi` must emit `--models` alone when no model is chosen,
> or `derive.lua` must always provide a default model pattern.

**Why it matters.** Running `yolo host -p deepseek -- pi` (or `-p` for any of Pi's built-in
providers whose profile does not explicitly declare a `model`) fails before Pi starts. The user
sees:
```text
Error: --provider requires --model (for example: --provider deepseek --model <pattern>)
```

---

## 1. The Diagnosis

### 1.1 The Runtime Asymmetry: `settings.json` vs. Pi CLI

In a jail container, `yolo` provisions `~/.pi/agent/settings.json` directly from the settings
derive ([`packs/pi/derive.lua`](file:///workspace/packs/pi/derive.lua)). The derive returns:

```json
{
  "defaultProvider": "deepseek",
  "enabledModels": ["deepseek/*"]
}
```

Pi's configuration schema accepts `defaultProvider` without `defaultModel`. When `defaultModel`
is missing, Pi's runtime `findInitialModel` selects the first model within the active
`enabledModels` scope.

At the host notch ([MM-D30](file:///workspace/docs/design/model-lists-and-pickers.md#MM-D30)),
`yolo host` explicitly protects the user's home configuration and avoids modifying
`~/.pi/agent/settings.json`. Instead, `HostLaunchSelection` translates the in-memory
selection into CLI arguments defined in [`packs/pi/pack.json`](file:///workspace/packs/pi/pack.json):

```json
"launch_selection": {
  "flags": [
    { "key": "defaultProvider", "argv": ["--provider", "{value}"] },
    { "key": "defaultModel",    "argv": ["--model",    "{value}"] },
    { "key": "enabledModels",   "argv": ["--models",   "{value}"] }
  ]
}
```

### 1.2 The Bug in Flag Translation

`LaunchSelection.Argv` iterates over declared `flags` independently:
- `defaultProvider = "deepseek"` &rarr; emits `--provider deepseek`
- `defaultModel = nil` (not in selection) &rarr; omitted
- `enabledModels = ["deepseek/*"]` &rarr; emits `--models 'deepseek/*'`

The resulting command line is:
```sh
pi --provider deepseek --models 'deepseek/*'
```

Pi's CLI argument validator (`packages/coding-agent/src/main.ts`) contains an explicit guard:
```javascript
if (parsed.provider && !parsed.model) {
  diagnostics.push({
    type: "error",
    message: `--provider requires --model (for example: --provider ${parsed.provider} --model <pattern>)`,
  });
}
```

Pi does not support setting a default provider on the CLI without specifying `--model`.

---

## 2. Architecture & Design Options

There are three architectural layers where this impedance mismatch can be resolved:

### Option A: Pack declaration grammar — conditional/coupled flags
Extend `packdecl.LaunchSelectionFlag` to support conditional presence:
- A flag can specify `requires: ["defaultModel"]`, so `--provider` is only emitted when
  `defaultModel` is also present.
- Or a compound flag: when both `defaultProvider` and `defaultModel` are present, emit
  `--provider {defaultProvider} --model {defaultModel}`; when only `defaultModel` is present,
  emit `--model {defaultModel}`.

**Cost:** Expands core `packdecl` schema and validation logic for one agent's CLI quirk.

### Option B: Pi pack configuration — rely on `--models` when `--model` is absent
In Pi's CLI, passing `--models '<provider>/*'` scopes the session to that provider and causes Pi
to automatically select the provider's first model if `--model` is omitted.
In [`packs/pi/pack.json`](file:///workspace/packs/pi/pack.json), remove `defaultProvider` from
`launch_selection.flags`:
```json
"launch_selection": {
  "flags": [
    { "key": "defaultModel",  "argv": ["--model",  "{value}"] },
    { "key": "enabledModels", "argv": ["--models", "{value}"] }
  ]
}
```
If a model is selected, `--model` is passed. If only a provider is selected, `--models '<provider>/*'`
is passed.

**Cost:** Zero core changes. If a user sets a provider that has no models or wildcard scope, Pi
relies on the scope.

### Option C: Derive layer — synthesize a default model or wildcard
In [`packs/pi/derive.lua`](file:///workspace/packs/pi/derive.lua), when a built-in provider has
no explicit profile model, synthesize `defaultModel = "*"` (or resolve the first known model ID
for that provider if available in Pi's catalog).

**Cost:** Pi CLI expects `--model` to resolve to a concrete pattern. Passing `*` might match models
from other providers if `--provider` is not passed or if multiple providers match.

---

## 3. Open Questions

### 💬 OQ-1: How should `packs/pi` handle the `--provider` / `--model` dependency?

- **Option A (Grammar):** Add `requires` or flag grouping to `packdecl.LaunchSelection`.
- **Option B (Pack manifest):** Omit `--provider` from `launch_selection.flags`. Rely on
  `--model <id>` when `defaultModel` is present, and `--models <scope>` for provider scoping.
- **Option C (Derive):** Ensure `packs/pi/derive.lua` always yields a non-nil `defaultModel`
  for built-in providers.

**Leaning:** Option B. In Pi's CLI, `--models "<provider>/*"` already accomplishes provider
selection when launching without a specific model, and `--model <id>` (or `<provider>/<id>`)
handles specific model selection.

---

## 4. Decision Ledger

| ID | Summary | Ruling | Date |
|:---|:---|:---|:---|
| OQ-1 | Handling Pi's CLI flag requirement between `--provider` and `--model` | Open | 2026-10-06 |
