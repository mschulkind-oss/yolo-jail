---
title: "Simultaneous Auth and Cross-Agent Pack Dependency Drag"
date: 2026-10-06
status: in-review
stage: DESIGN
next: "Rule OQ-1 on command-scoped pack pruning and OQ-2 on broker/extension credential gating"
tags: [packs, host, jail, credentials, openai-auth, pi, claude, needs, provider-scope]
summary: "When launching a specific agent CLI (e.g. yolo host -p deepseek -- pi or yolo -p deepseek -- pi), yolo resolves the entire workspace pack set including unrelated agents (claude), dragging in claude's transitive needs (openai-auth, bedrock, wire-bridge, aws-auth). Furthermore, broker-backed auth (openai-codex) bypasses env-var credential scoping, registering Codex alongside DeepSeek in pi and defeating profile isolation. This document designs command-scoped pack selection and broker/extension credential gating."
---

# Simultaneous Auth and Cross-Agent Pack Dependency Drag

**Status:** 2026-10-06. Drafted following live observation of `yolo host -p deepseek -- pi`
dragging in `claude`'s dependencies (`bedrock`, `openai-auth`, `wire-bridge`, `aws-auth`) and
leaving `openai-codex` active alongside `deepseek`.
[OQ-1](#OQ-1) and [OQ-2](#OQ-2) are open for ruling.

> **In short.** A workspace configuring multiple agent packs (e.g. `packs: ["claude", "pi"]`)
> currently evaluates `packs` as a flat union for every launch. When executing `pi`, `claude`
> is resolved, pulling in `claude`'s unconditional `needs`: `openai-auth`, `bedrock`,
> `wire-bridge`, and `aws-auth`. Because `openai-auth` delivers the `openai-codex` provider via
> a background daemon rather than an env-var API key, yolo's credential scope does not withhold
> it. Pi's extension registers Codex, and Pi's soft shortlist exposes both DeepSeek and Codex
> simultaneously. The fix requires: (1) pruning the host pack set to the executed command's
> own dependency tree, and (2) gating broker doorways and extension provider registrations on
> the active provider profile.

**Why it matters.** Running `yolo host -p deepseek -- pi` explicitly requested the `deepseek`
profile for `pi`. The user expects:
1. Only the packs and dependencies relevant to `pi` and `deepseek` to be engaged.
2. Only `deepseek` credentials and models to be reachable by the agent process.
Instead, the launch logs four unneeded joined packs, opens routes to OpenAI auth brokers, and
leaves Pi with both DeepSeek and ChatGPT subscription models active.

---

## 1. The Diagnosis: Two Independent Leakage Paths

The simultaneous auth bug is the compounding result of two architectural gaps:
pack selection granularity (what is loaded) and credential gate coverage (what is withheld).

### 1.1 Door 1: Workspace-Wide Pack Closure on Single-Command Exec

In [`internal/cli/host.go`](file:///workspace/internal/cli/host.go#L3476-L3478), `loadedHostPacks`:
```go
func loadedHostPacks(cfg *jsonx.OrderedMap, agent, typed string) hostPackSet {
    return selectHostPacks(resolveConfiguredPack, hostLaunchSelection(cfg, agent, typed))
}
```
calls [`selectHostPacks`](file:///workspace/internal/cli/hostselection.go#L53-L59), which reads
[`config.LoadPackEntries()`](file:///workspace/internal/config/packselection.go).

`LoadPackEntries()` loads every entry in the workspace's `packs` list. If the workspace config
declares:
```jsonc
"packs": ["claude", "pi"]
```
both packs are handed to `config.SelectPacks`.

`SelectPacks` then resolves the transitive dependency closure (`packload.Selection.Close`) over
all configured entries:
- `packs/claude` declares unconditional `needs: ["bedrock", "openai-auth", "wire-bridge"]`.
- `packs/bedrock` declares `needs: ["aws-auth"]`.

Even though the invocation was `yolo host -- pi` (or `yolo host -p deepseek -- pi`), `yolo`
evaluates the closure over `claude`. This causes the launch to announce and join:
```text
yolo host: + bedrock (needed by claude)
yolo host: + openai-auth (needed by claude)
yolo host: + wire-bridge (needed by claude)
yolo host: + aws-auth (needed by bedrock)
```

At the host notch, `pi` is running, but the launch composition includes `openai-auth`,
`wire-bridge`, and `bedrock`.

### 1.2 Door 2: Daemon/Broker Auth Bypasses Env-Var Credential Scoping

yolo's credential scope ([`docs/reference/providers.md` §2.4](file:///workspace/docs/reference/providers.md#the-credential-gate),
[`OQ-CN1`](file:///workspace/docs/reference/providers.md#oq-cn1) through [`OQ-CN9`](file:///workspace/docs/reference/providers.md#oq-cn9))
gates environment variable credentials per launch:
```text
yolo host: Credential scope: a provider's credential reaches only the agents whose profile selects it.
  DEEPSEEK_API_KEY (provider deepseek): pi only
  OPENROUTER_API_KEY (provider openrouter): withheld from every process...
  CEREBRAS_API_KEY (provider cerebras): withheld from every process...
```

However, **`openai-codex` does not authenticate via an environment variable API key**. It
authenticates through the `openai-auth-broker` service/doorway and Pi's
[`packs/pi/extensions/yolo-openai-auth.js`](file:///workspace/packs/pi/extensions/yolo-openai-auth.js).

When `openai-auth` is pulled into the pack set:
1. `openai-auth` contributes the `openai-codex` provider definition to `ctx.providers`.
2. `packs/pi/derive.lua` composes `codex-models` (`~/.pi/agent/yolo-openai-codex-models.json` or
   `YOLO_PI_OPENAI_CODEX_MODELS`) because `ctx.providers["openai-codex"]` is present.
3. `yolo-openai-auth.js` executes inside Pi at startup. It reads the model list and unconditionally
   registers the provider via `pi.registerProvider("openai-codex", ...)`.
4. In a jail container, the `openai-auth-broker` daemon is running. On host, if the host broker
   socket is active, Pi talks to it.
5. In Pi, `enabledModels: ["deepseek/*"]` is only an initial filter on the picker. Pressing `Tab`
   in `/model` switches to `all` models, where `openai-codex` is registered and fully functional.

Thus, the credential gate successfully withholds `OPENROUTER_API_KEY`, but fails to withhold
`openai-codex`, because `openai-codex` is delivered through a daemon doorway and a pack extension.

---

## 2. Invariants

1. **Command Pack Purity:** Running `yolo host -- <agent>` or `yolo host -p <prof> -- <agent>`
   must only load the pack delivering `<agent>` and any non-program utility/guardrail packs.
   Packs delivering *other* agent CLIs (`kind: "program"` where `bin != agent`) and their
   exclusive `needs` closures must not be loaded.
2. **Profile-Bound Service Doorways:** A background daemon or broker doorway (such as
   `openai-auth-broker` or `wire-bridge`) must only be exposed or connected if the active
   profile or active provider set for the running agent selects it.
3. **No Unselected Provider Registration:** A pack extension (like `yolo-openai-auth.js` or
   `yolo-model-lists.js`) must not register a provider or models in the agent's runtime if that
   provider is withheld from the launch.

---

## 3. Architecture & Alternatives

### 3.1 Pack Selection Scoping at Host (`loadedHostPacks`)

When `yolo host -- <cmd0>` runs, `cmd0` identifies the target program.

Instead of passing all workspace entries to `SelectPacks`:
- **Filter entries before closure:**
  Identify which pack delivers `cmd0` (`targetPack`).
  Partition configured packs into:
  - Agent packs (`kind: "program"` where `bin != cmd0`): **dropped**.
  - Target pack (`kind: "program"` where `bin == cmd0`): **kept**.
  - Non-program packs (guardrails, shared files, loopholes without programs): **kept**.
- **Close only over kept packs:**
  Run `SelectPacks` over the filtered set.
  When running `pi`, `claude` is dropped before `needs` resolution. `claude`'s `needs`
  (`openai-auth`, `wire-bridge`, `bedrock`, `aws-auth`) are never evaluated or added.

### 3.2 Gating Extension Provider Registrations

In `packs/pi/extensions/yolo-openai-auth.js`:
- Currently, `readCodexModelList()` reads the model list and calls `pi.registerProvider("openai-codex", ...)`.
- It should check whether `openai-codex` is an active provider for this launch.
- If the launch was `-p deepseek`, `yolo` should not emit `YOLO_PI_OPENAI_CODEX_MODELS`, or should
  set an explicit disablement marker (`YOLO_PI_OPENAI_CODEX_MODELS=""` or `disabled: true`).
- When disabled, `yolo-openai-auth.js` skips calling `pi.registerProvider`. Pi's runtime never
  sees `openai-codex`, even if the user hits `Tab`.

### 3.3 What happens in container jails?

In a jail, a single container is booted for the workspace, holding all selected packs.
However, per-command executions inside the jail (`yolo -p deepseek -- pi`):
- Write per-command environment files (`~/.config/yolo-agent-env/pi.sh`).
- If `pi` is started under `-p deepseek`, the per-agent environment should mask the broker
  endpoint variable (`YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT=""`) so `pi` cannot reach the
  broker, and `codex-models` derive should omit models for unselected profiles.

---

## 4. Open Questions

### 💬 OQ-1: How should `yolo host` prune packs for a specific agent command?

- **Option A (Filter before closure):** In `loadedHostPacks`, when `agent` is non-empty,
  filter the configured pack entries to remove any `kind: "program"` pack whose `bin != agent`
  before running `SelectPacks`.
- **Option B (Conditional needs on when_bins):** Change `packs/claude`'s `needs` to be conditional:
  `when_bins: ["claude"]`. If `claude` is not selected by the command, the need does not fire.

**Leaning:** Option A. Option B requires modifying every pack author's `needs` declarations,
whereas Option A enforces the architectural principle that launching one agent at the host
should never load another agent's pack.

### 💬 OQ-2: How should loophole/broker credentials be withheld from non-selecting profiles?

- **Option A (Withhold registration data):** When a profile does not select `openai-codex`,
  the derive layer emits an empty/disabled payload for `pi/codex-models`, and the extension
  skips `registerProvider`.
- **Option B (Unset broker endpoint variables in per-agent env):** In the per-agent environment
  script, unset `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT` and `YOLO_OPENAI_AUTH_HOST_SOCKET`
  unless the agent's profile selects `openai-codex`.

**Leaning:** Both (A and B). Option A prevents the models from cluttering Pi's UI/Tab view;
Option B enforces the security boundary preventing token requests.

---

## 5. Decision Ledger

| ID | Summary | Ruling | Date |
|:---|:---|:---|:---|
| OQ-1 | Pack pruning scope for single-agent host launches | Open | 2026-10-06 |
| OQ-2 | Withholding broker-backed auth from unselected agent profiles | Open | 2026-10-06 |
