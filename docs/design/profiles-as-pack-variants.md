---
title: "A profile is a pack's own variant, not a cross-pack fragment"
date: 2026-08-29
status: in-review
tags: [packs, config, profiles, providers, prism, architecture, secrets]
summary: "Counter-design to pack-profiles.md. Most of what that doc proposes is already shipped: providers are already a typed extension point consumed by three packs' derives, and profile-conditional pack behavior already works in Lua. The real gap is one channel (process env, profile-gated) and one hardcode (assemble.go:722). So this design adds no new merge engine and no cross-pack fragment kind — it generalizes the shipped `autonomy` contribution from a closed two-valued selector to an open named one, keeps `providers` as a config key with a stricter schema, and fixes the delivery channel so the Claude/Bedrock case works at the host notch too."
---

# A profile is a pack's own variant, not a cross-pack fragment

**Status:** DESIGN, 2026-08-29. Nothing built. This is a counter-proposal to
[`pack-profiles.md`](pack-profiles.md), written after measuring what that doc's §2 diagnoses
against the code as it stands today. All file:line claims below were verified on 2026-08-29.

**The short version.** `pack-profiles.md` is right that `agent_profiles` is an inversion and that
[`assemble.go:722-754`](../../internal/cli/run/assemble.go#L722-L754) must die. It is wrong about
how much is missing. Providers are **already** a typed extension point, already exposed to Lua as
`ctx.providers`, and already consumed by `packs/pi`, `packs/codex` and `packs/opencode` with no
adapter packs. Profile-conditional pack behavior **already works** — `packs/claude/derive.lua:5`
branches on `ctx.agent_profiles.claude == "bedrock"` today. What does not exist is a way for a pack
to contribute **process environment** conditionally on a profile, which is the entire reason the
Bedrock case ended up hardcoded in Go. So: no `kind: "provider"`, no `kind: "pack-fragment"`, no
new merge engine. Instead — **`kind: "profile"`, a named variant of the pack's OWN declarations,
which is the shipped `autonomy` contribution with an open selector instead of a two-valued one.**

**The most important sections are [§2](#2-what-already-exists--measured-not-assumed) (what is
already shipped, which is most of the argument), [§5](#5-the-delivery-channel-rule--and-why-it-kills-the-worked-example)
(the host-notch constraint that decides the Claude/Bedrock case), and
[§9](#9-where-this-differs-from-pack-profilesmd-and-why) (the diff against `pack-profiles.md`).**

**Reads with:** [`pack-profiles.md`](pack-profiles.md) (the design this answers — its §4
credential architecture is adopted as recommendation plus mechanism), [`pack-code-separation.md`](pack-code-separation.md) (core knows no agents),
[`extension-point-principle.md`](extension-point-principle.md) (the framework author designs the
extension point), [`stringly-typed-references-principle.md`](stringly-typed-references-principle.md)
(unmatched references fail closed), [`pack-config-collaboration.md`](pack-config-collaboration.md)
(the shipped `config-overlay` kind and the layer fold), [`host-agent-environment.md`](host-agent-environment.md)
(§3.1: native config-file injection is the preferred host delivery path), and
[`pack-system.md`](pack-system.md) (the kind registry and the footprint model).

**Terms**, defined once and used with one meaning throughout — coinages say so, borrowed terms
link their owner:

- **profile** *(coined here — the word arrived with `pack-profiles.md`; the mechanism renames
  the shipped `agent_profiles` config key)* — a named variant of **one pack's own**
  contributions (config patches, launch flags, env, provider requirements), selected at launch
  by name. Not a cross-pack fragment (contributing to another pack's surface is
  `config-overlay`, [§7](#7-if-cross-pack-delivery-is-ever-needed-it-is-config-overlay-with-a-profile-field))
  and not the autonomy posture (that variant is selected by the confinement notch, not the
  user — OQ-1).
- **selector** *(coined here)* — whatever picks the active variant: for `autonomy` the notch
  (two values); for a profile the user's `-p` / `pack_profiles` choice (an open name set).
- **provider** — one entry of the shipped `providers` config key: a named endpoint, model
  aliases, and the *name* of the env var holding its credential. A machine-local fact, not a
  pack artifact, and never the credential itself.
- **notch** — one value of the confinement dial — `jail`, `guest`, or `host`
  ([`yolo-as-environment-manager.md`](yolo-as-environment-manager.md) §4.0). "The host notch"
  is the unconfined end: `yolo host apply` and `yolo host --`.
- **the Prism** — the engine that composes every agent's config surfaces at boot and check,
  running each pack's `derive.lua` (its per-pack Lua script) against live config
  ([`packs-and-the-prism.md`](packs-and-the-prism.md)).
- **surface** — one generated config file a pack composes, with its codec and layer stack
  ([`pack-system.md`](pack-system.md); the agent is not the surface — `claude/settings` is).
- **pack universe** — every pack this machine can resolve, selected or not; contrasted with
  the **active** list, which is what one launch selected.

---

## 1. Principles and verdict up front

1. **P1 — Core knows packs, not agents.** Unchanged from `pack-profiles.md` P1, and the only
   principle the two designs share without qualification.
2. **P2 — A profile selects among a pack's OWN declarations.** A pack declares named variants of
   what it already contributes: config-surface patches, launch flags, env vars, requirements. The
   launch selects one by name. Nothing targets another pack. This is `kind: "autonomy"`
   ([`contributes.go:116-123`](../../internal/packdecl/contributes.go#L116-L123)) with an open
   selector, and it inherits that kind's whole safety story for free.
3. **P3 — A provider is a fact about the user's machine, not a shipped artifact.** It stays a
   top-level config key (`providers`), because that is where a URL, a region and a credential
   pointer actually come from. It is already an extension point strangers write, so per
   [`extension-point-principle.md`](extension-point-principle.md) the typed schema stays and gets
   *stricter* — the opposite of `pack-profiles.md` §2.3.
4. **P4 — One representation per concept, with no escape hatch.** If an endpoint can be expressed
   two ways — once typed, once smuggled through an untyped dictionary — the typed one is
   decorative. The untyped path must be unrepresentable, not discouraged.
5. **P5 — Every new kind states its combine rule and its claim before it exists.** "Claim" is a
   term of the footprint model ([`pack-system.md` §3](pack-system.md)), not prose: the one-line
   statement of what a contribution of that kind **takes on the environment** — a name on `PATH`,
   an owned path, a host-home read — carried as the `Claims` field of
   [`Footprint`](../../internal/packdecl/kinds.go#L170-L191) and printed as the Claims column of
   the footprint table; the combine rule says how two claims on one target resolve.
   [`kinds.go:196-267`](../../internal/packdecl/kinds.go#L196-L267) makes this structural: a kind
   with no `Footprint{Combine, Claims}` cannot be registered, so `yolo pack footprint` and the
   collision table cannot see it. A design that adds kinds without answering this is not
   implementable as written.
6. **P6 — Route by payload type: the two channels are partial along different axes** *(restated
   2026-08-30, superseding this bullet's original preference order — see §5).* `kind: "env"` is
   still *refused* by `yolo host apply` ([`fieldset.go:99-103`](../../internal/render/fieldset.go#L99-L103))
   — a limit of that COMMAND, not of the notch, since `host apply` never runs a process. Host env
   IS deliverable now that [`hostwrap`](../../internal/hostwrap/hostwrap.go) generates a
   per-program PATH wrapper — but behind the opt-in `host_wrappers` key
   ([`host-agent-environment.md` §5.1](host-agent-environment.md#51-where-launch-wrappers-live--and-the-path-claim-they-cost)),
   so it needs yolo in the launch path *and* the opt-in, while a config-file patch works from any
   invocation (IDE, cron, absolute path) with neither. So **configuration** patches the agent's
   own config surface; **secrets and unsets** go through process env. Channels, not preferences.
7. **P7 — Git-tracked packs never contain secrets; the schema makes compliance the default.**
   A secret is a value whose disclosure would force rotation. Kept from `pack-profiles.md` P4 as
   *recommendation plus mechanism*, not enforcement: the mechanism is the schema —
   credential-pointer fields (`api_key_env`) hold env-var names by contract
   ([`validate.go:922-927`](../../internal/config/validate.go#L922-L927)), credential values
   travel `env_sources`, and §4.3 refuses userinfo in `base_url`. A pack is a *distribution
   artifact* — fetched from a git remote, approved at a commit — which is why the recommendation
   matters; yolo's part is that its schema never offers a sanctioned slot for a credential
   value, so what it validates cannot carry one. What this design does **not** add is a secrets
   scanner (§6).

**Verdict.** Ship a rename, one new contribution kind, one new field on `providers`, and three
validation gates. Do not ship a cross-pack fragment merge engine — and if one is ever needed, it is
`config-overlay` with a `profile` field, not a new kind ([§7](#7-if-cross-pack-delivery-is-ever-needed-it-is-config-overlay-with-a-profile-field)).

---

## 2. What already exists — measured, not assumed

This is the section that makes this design smaller than `pack-profiles.md`. That doc's §2 diagnoses
three defects and implies everything else is unbuilt. Three of the four mechanisms it proposes are
shipped.

### 2.1 Providers are already a typed extension point with three native consumers

`providers` is a top-level config key, validated in Go
([`validate.go:885-944`](../../internal/config/validate.go#L885-L944)) against a closed key set
([`config.go:88`](../../internal/config/config.go#L88)), exposed to the Prism as a live derive source
([`sources.go:19`](../../internal/agentcfg/manifest/sources.go#L19),
[`derive.go:180`](../../internal/agentcfg/luahook/derive.go#L180)), and consumed today by three packs
that translate it into three different on-disk dialects with zero core involvement:

| Pack | Derive | Reads | Writes |
| :--- | :--- | :--- | :--- |
| `pi` | [`packs/pi/derive.lua`](../../packs/pi/derive.lua) | `ctx.providers` | `~/.pi/agent/models.json` (`baseUrl`/`api`/`apiKeyEnv`) |
| `codex` | [`packs/codex/derive.lua`](../../packs/codex/derive.lua) | `ctx.providers` | `~/.codex/config.toml` (`model_providers`) |
| `opencode` | [`packs/opencode/derive.lua`](../../packs/opencode/derive.lua) | `ctx.providers` | `opencode.json` (`provider`, `{env:VAR}` interpolation) |

`pack-profiles.md` §8.2 presents all three of these derives as proposed new work. They exist, and
the shipped versions are close to line-for-line what that doc sketches. Its "Layer 1" is not a
design; it is a description of 2026.

> [!IMPORTANT]
> This is the load-bearing observation of the whole counter-design. The universal-provider layer
> `pack-profiles.md` argues for is **already built and already working**. What that doc adds on top
> of it — moving providers into pack manifests as `kind: "provider"` — is not filling a gap, it is
> relocating a working mechanism, and [§4](#4-providers-stay-a-config-key--and-the-schema-gets-stricter-not-deleted)
> argues the relocation is the wrong direction.

### 2.2 Profile-conditional pack behavior already works — in Lua, inside the pack

[`packs/claude/derive.lua:5-6`](../../packs/claude/derive.lua#L5-L6):

```lua
local claudeProfile = (ctx.agent_profiles and ctx.agent_profiles.claude) or "default"
local isBedrock = (claudeProfile == "bedrock")
```

A pack reads the active profile and changes what it renders. No fragment, no target, no adapter
pack, no core involvement. The mechanism `pack-profiles.md` §7 calls "pathway 2: built-in agent
modes" is the one that is real, and it is the one the shipped code uses.

### 2.3 The actual gap: one channel and one hardcode

A derive writes **config files**. It cannot set a **process environment variable**. Claude Code's
Bedrock mode needs `CLAUDE_CODE_USE_BEDROCK`, `AWS_REGION`, and `ANTHROPIC_DEFAULT_*_MODEL` in the
environment — and `kind: "env"` is *static only*, literal strings with no selector
([`kinds.go:96-99`](../../internal/packdecl/kinds.go#L96-L99),
[`packload.go:241-251`](../../internal/packload/packload.go#L241-L251)).

There was no profile-gated env channel, so the Bedrock case went into Go:

```go
// assemble.go:722 — the whole reason pack-profiles.md exists
if prof, ok := effectiveProfiles.Get("claude"); ok && prof == "bedrock" {
    env = append(env, "-e", "CLAUDE_CODE_USE_BEDROCK=1")
    // …region, Opus/Haiku/Sonnet model IDs…
}
```

**That is the gap. One missing selector on one existing kind.** Everything else in
`pack-profiles.md` §3's dual-layer architecture is scaffolding around it — and
[§5](#5-the-delivery-channel-rule--and-why-it-kills-the-worked-example) argues that even this case
should not be solved with env.

### 2.4 Two inversions the diagnosis missed

`pack-profiles.md` §2.1–2.2 names `agent_profiles` and `assemble.go`. Core names an agent in two
more places, and a full removal has to take them:

- **`--claude-auth` / `--auth`** ([`runcmd.go:99`](../../internal/cli/runcmd.go#L99),
  [`runcmd.go:166-173`](../../internal/cli/runcmd.go#L166-L173)) is an agent name in the CLI
  surface, feeding `o.ClaudeAuth` → `out.Set("claude", …)`
  ([`assemble.go:776-777`](../../internal/cli/run/assemble.go#L776-L777)). A design that deletes
  `agent_profiles` and leaves `--claude-auth` has moved the inversion, not removed it.
- **The full `agent_profiles` footprint is eight sites, not two**: `config.go:67`,
  `validate.go:902-922`, `inherit.go:132`, `sources.go:20-21`, `derive.go:180`,
  `entrypoint/providers.go:27-41` (`LoadAgentProfiles`), the `YOLO_AGENT_PROFILES` env channel at
  `assemble.go:718`, and `effectiveAgentProfiles` at `assemble.go:763-789`. `pack-profiles.md` §9 claims the design "deletes all hardcoded `claude`
  Bedrock checks from `assemble.go` and removes `knownProviderKeys`" — that is under half of it.

### 2.5 The stringly-typed hole that is live today

[`stringly-typed-references-principle.md`](stringly-typed-references-principle.md) lists
`pack_profiles.<pack_name>` as "Required / Validation error if pack is not in configured universe."
**It is not.** `validateAgentProfiles` ([`validate.go:902-922`](../../internal/config/validate.go#L902-L922))
checks only that the value is a string; the *key* is never checked against anything. So today:

```jsonc
{ "agent_profiles": { "cloude": "bedrock" } }   // accepted silently, does nothing
```

And the CLI makes it worse: `-p <name> -- <cmd>` keys the profile by the **binary basename**, not
the pack slug ([`assemble.go:779-787`](../../internal/cli/run/assemble.go#L779-L787)). Every shipped
agent pack happens to have `bin == slug` (verified across `packs/*/pack.json`, 2026-08-29), so the
conflation is latent rather than live — but a third-party pack named `aider-pack` shipping
`bin: "aider"` silently sets a key nothing reads.

**This is the one thing in the whole design space that is both broken today and cheap to fix**, and
neither doc has to build a fragment engine to fix it.

---

## 3. The design: a profile is a named variant of a pack's own declarations

### 3.1 The shape

`kind: "profile"` is `kind: "autonomy"` with an open selector. Compare — the shipped autonomy
contribution from [`packs/claude/pack.json`](../../packs/claude/pack.json), trimmed:

```jsonc
{ "kind": "autonomy",
  "autonomous": { "config": [ { "agent": "claude", "name": "settings",
                                "managed": { "permissions": { "defaultMode": "acceptEdits" } } } ],
                  "launch":  [ { "bin": "claude", "flags": ["--dangerously-skip-permissions"] } ] },
  "guarded":    { "config": [ … ] } }
```

The proposed profile contribution, the same body shape — *posture* being `autonomy`'s word for
one of its two permission variants — named instead of positional:

```jsonc
{ "kind": "profile",
  "name": "bedrock",
  "config": [ { "agent": "claude", "name": "settings",
                "managed": { "env": { "CLAUDE_CODE_USE_BEDROCK": "1",
                                      "AWS_REGION": "us-east-1" } } } ],
  "env":    { },
  "launch": [ ],
  "requires_provider": "bedrock" }
```

Field semantics:

| Field | Type | Required | Meaning |
| :--- | :--- | :--- | :--- |
| `name` | `string` | **Yes** | The profile name this variant answers to. Unique within the pack. |
| `config` | surface-patch list | No | Patches the **managed** layer of a surface **this same pack owns**, identified by `agent`/`name` — byte-identical to `AutonomyPosture.Config` ([`contributes.go:347-356`](../../internal/packdecl/contributes.go#L347-L356)). |
| `launch` | `[{bin, flags}]` | No | Flags merged into the binary's launch flags, same shape as `AutonomyLaunch`. |
| `env` | `map[string]string` | No | Static process env, jail-notch only ([§5](#5-the-delivery-channel-rule--and-why-it-kills-the-worked-example) says when this is the wrong field). |
| `requires_provider` | `string` | No | A closed-namespace reference into `providers`. Unresolvable → fatal preflight ([§6.2](#62-an-activated-profile-with-no-credential-is-a-preflight-failure)). |

### 3.2 Why the pack's own declarations, and not a target

`autonomy`'s doc comment states the reason better than I can
([`contributes.go:344-346`](../../internal/packdecl/contributes.go#L344-L346)): its config patches
fold into *"the managed layer of a surface the SAME pack owns"* — the **managed layer** being the
top of a surface's layer stack, the part yolo itself writes rather than folds from user input
(full stack in [§7](#7-if-cross-pack-delivery-is-ever-needed-it-is-config-overlay-with-a-profile-field))
— **"it is not a second config
writer, it is a notch-gated patch."** That restriction is what lets `autonomy` skip a cross-pack
collision rule entirely: two packs cannot collide on a surface only one of them owns.

`pack-fragment` gives that up by design. It targets another pack, so it *is* a second config writer,
and it therefore owes a per-key collision rule and a provenance label — neither of which
`pack-profiles.md` specifies. The shipped mechanism that already pays that price correctly is
`config-overlay` ([§7](#7-if-cross-pack-delivery-is-ever-needed-it-is-config-overlay-with-a-profile-field)).

There is a second, structural reason. In the skills subsystem the **consumer** declares the
destination (`into: ".claude/skills"`); `pack-profiles.md` §6 claims profiles mirror that. They
mirror it for providers and **invert it for fragments**, where the contributor names the target. The
inverted direction scales as *N* provider packs × *M* agent quirks: every provider pack must learn
every agent's private env-var spellings. Keeping profiles inside the pack keeps the skills direction
everywhere.

### 3.3 The selector, and where it comes from

Rename, don't redesign:

| Today | Proposed | Why |
| :--- | :--- | :--- |
| `agent_profiles` (config key) | `pack_profiles` | The inversion is the *word*, not the shape. |
| `YOLO_AGENT_PROFILES` | `YOLO_PACK_PROFILES` | Same. |
| `ctx.agent_profiles` (Lua) | `ctx.pack_profiles` | Keeps `packs/claude/derive.lua` working with a one-word edit. |
| `--claude-auth` / `--auth` | *(deleted; `--pack-profile claude=bedrock`)* | §2.4. |
| `-p <name> -- <bin>`, keyed by bin basename | `-p <name> -- <bin>`, resolved bin → **pack slug**, fatal if no pack owns that bin | §2.5. |

Resolution order is the shipped one, extended by nothing: workspace config < user config < CLI. The
selected profile name for pack *P* is then a plain string that (a) gates *P*'s `kind: "profile"`
contributions and (b) reaches *P*'s derive as `ctx.pack_profiles[P]`, exactly as it does today.

### 3.4 The combine rule and the footprint — stated, because P5 requires it

```
KindProfile: { Combine: CombineExclusive, Claims: "a named variant of the pack's own
               surfaces, launch flags and env, selected at launch" }
```

**`CombineExclusive`, keyed by `(pack, profile name)`.** Two `profile` contributions in one pack with
the same `name` is a load error, the same way a second `autonomy` is
([`contributes.go:371-381`](../../internal/packdecl/contributes.go#L371-L381)). Across packs there
is nothing to combine — profile `bedrock` in `packs/claude` and profile `bedrock` in `packs/pi` are
unrelated declarations that happen to share a selector value, and neither can touch the other's
surfaces.

Within a launch, a selected profile's patches fold into the **managed** layer of the named surface,
after `autonomy`'s. Two patches from the same pack on the same key — its autonomy posture and its
profile — are one pack's own business and resolve later-wins, in declaration order. That is the
existing managed-layer contract ([`compose.go:355-380`](../../internal/agentcfg/compose.go#L355-L380)),
not a new one.

---

## 4. Providers stay a config key — and the schema gets stricter, not deleted

### 4.1 Why not a contribution kind

`pack-profiles.md` §5.1 moves providers into pack manifests. My read is that this gets the ownership
backwards. A provider is a **machine-local fact**: my endpoint, my region, my model aliases, the
name of the variable my key lives in. Under `kind: "provider"`, adding a personal DeepSeek endpoint
means authoring a pack; today it means four lines in `~/.config/yolo-jail/config.jsonc`. That is a
real ergonomic regression, and `pack-profiles.md` never says whether `config.providers` survives
alongside its new kind — which leaves the combine question ([P5](#1-principles-and-verdict-up-front))
unanswered in the one place it is most likely to bite: a pack-shipped `bedrock` and a user-configured
`bedrock`.

The narrow thing a pack *does* legitimately want is to say **"I need a provider named X"** —
`requires_provider` in §3.1 — which is an assertion, not a definition, and behaves like
`kind: "requires"` ([`kinds.go:40-58`](../../internal/packdecl/kinds.go#L40-L58)): many packs may
assert one provider, nobody owns it, and a missing one is a named preflight failure.

### 4.2 The escape hatch that must not exist

This is the sharpest objection to `pack-profiles.md`, and it is P4 in one example. That doc's §5.2
worked manifest contributes a fragment whose body is:

```jsonc
"config": { "provider": { "base_url": "…", "wire_api": "openai-completions",
                          "api_key_env": "AWS_BEDROCK_API_KEY", "models": { … } } }
```

That **is** a `ProviderSpec`, written in the untyped layer. Every guarantee its §5.1 buys — the URL
check, the `wire_api` enum, the `api_key_env` regex, the §4.3 credential refusal — is bypassed by
writing the same object one nesting level over. The type discipline is voluntary.

> [!WARNING]
> The tell is that `packs/aws-bedrock`, the only complete manifest in `pack-profiles.md`, contains
> **no `kind: "provider"` entry at all** — two fragments, zero providers. The flagship example of
> the dual-layer architecture never uses the prescribed layer. That is not a drafting slip; it is
> what happens when the untyped layer can express everything the typed one can.

Under this design the hatch is closed by construction: there is one place a provider can be written
(the `providers` config key), and a pack can only *name* one.

### 4.3 What to add to the schema

Extend [`validate.go:885-944`](../../internal/config/validate.go#L885-L944) rather than replacing it:

- **`wire_api` becomes a closed enum** (`openai-completions`, `openai-chat`, `anthropic`,
  `responses`), which is [Rule 4](stringly-typed-references-principle.md) applied to a fixed
  syntactic slot. It is a free-form string today.
- **`base_url` must parse as `http`/`https` and must carry no userinfo.** `https://user:tok@host/v1`
  is a credential in a git-tracked file, and no current check catches it; this rule is the check.
- **`models` gets a documented alias vocabulary** (`default`, `fast`, `coder`, …), because
  `assemble.go:731-750` reads `default`/`haiku`/`sonnet` — Claude-specific alias names in core, a
  fourth inversion nobody has named.

> [!NOTE]
> `pack-profiles.md` §4.3 proposes the `api_key_env` credential check as new. It ships today:
> [`validate.go:922-927`](../../internal/config/validate.go#L922-L927) already rejects anything that
> is not `[A-Za-z_][A-Za-z0-9_]*`, which already refuses `sk-9f82…` (the hyphen fails the class).
> What is genuinely missing is the *error message* that names the remedy.

---

## 5. The delivery-channel rule — and why it kills the worked example

This is the finding that changed my mind about the Claude/Bedrock case, and neither doc's §9 parity
claim survives it.

**`kind: "env"` is refused at the host notch.** Not unimplemented — *refused*, with a written
reason ([`fieldset.go:99-103`](../../internal/render/fieldset.go#L99-L103)):

> *"env vars apply to a process yolo starts, and `yolo host apply` only configures your tools — it
> never runs them. Setting them for your whole session would mean editing your shell rc, a much
> larger claim than a pack's env contribution asks for. `yolo host -- <program>` delivers them at
> launch instead, to that process only."*

`pack-profiles.md` §9 claims alignment with [`happy-path-principle.md`](happy-path-principle.md) —
*"One unified merge pipeline across the entire matrix: Linux containers, macOS Apple Container,
`macos-user`, and Host Render Target (`yolo host apply`)"*. Its worked example is **pure `config.env`**.
So the flagship case does not work on the notch the doc claims parity for — and the host notch is
precisely where the design's stated downstream motivation lives
([`host-agent-environment.md` §2.2](host-agent-environment.md#22-real-world-case-study-obviating-bashrc-wrapper-functions):
obviating the `.bashrc` `claude()` wrapper).

**The rule this implies — corrected 2026-08-30.** The first version of this section stated a
*preference order* (config first, env as last resort). That is wrong for half the payload, and the
half it is wrong for is the half that carries credentials:

> [!WARNING]
> **A config file ROUTES a credential; it cannot DELIVER one.** `api_key_env` / `apiKeyEnv` /
> `{env:VAR}` all write the **name** of a variable the agent then reads from **its own process
> environment** — verified against the three shipped derives in
> [`host-agent-environment.md` §4](host-agent-environment.md#4-per-agent-host-capabilities-matrix).
> So there is no "fallback" here: any bring-your-own-key provider is unusable on the host without a process-env
> channel, for every agent. A preference order cannot express that, because the two channels are not
> ranked — they carry different things.

> **P6 restated (corrected).** Split by **payload type**, not by preference and not per agent.
> **Configuration** — endpoints, model aliases, `wire_api`, permissions, and the `api_key_env`
> *name* — patches the config surface, which is the only channel that survives an invocation yolo is
> not part of (IDE, cron, absolute path). **Environment** — secrets, process flags, and **unsets** —
> goes through the process env, which requires yolo in the launch path
> (`yolo host -- <agent>`, or its `PATH` wrapper). Both channels always apply; a pack that needs
> neither declares neither. Neither channel is universal, and they are partial along *different*
> axes, which is why the answer is both rather than a winner.

And for the Claude/Bedrock **flags** specifically, the config file can carry them. Claude Code's `settings.json` has an
`env` block — `packs/claude/derive.lua` already writes into it
(`env = { ENABLE_LSP_TOOL = … }`). So:

```mermaid
flowchart LR
    SEL["pack_profiles.claude = 'bedrock'"] --> PROF["packs/claude<br/>kind: profile, name: bedrock"]
    PROF -->|"CONFIG: flags, model IDs, region"| SURF["claude/settings<br/>~/.claude/settings.json"]
    SURF --> JAIL["jail launch ✓"]
    SURF --> HOST["yolo host apply ✓ (any invocation)"]
    PROF -->|"ENV: AWS creds, unset AWS_PROFILE"| ENVCH["process env"]
    ENVCH --> JAIL2["jail launch ✓"]
    ENVCH --> HOST2["yolo host -- claude ✓<br/>(or its PATH wrapper)"]
```

The **non-secret** half of the motivating example dissolves into a managed-layer patch on a surface
`packs/claude` already owns — no fragment, no adapter pack, no new merge engine, and it works from
any invocation at both notches. The **secret** half (AWS credentials, and §2.2's `unset
AWS_PROFILE`, which no config surface can express at all) goes through the process env and needs
`yolo host` on the host side.

> [!IMPORTANT]
> **The argument against the cross-pack fragment is unchanged and does not depend on the
> correction.** `pack-profiles.md` invents a fragment mechanism to deliver, via process env, a
> payload the target pack could have written into its own file — and process env is precisely the
> channel that needs yolo in the launch path, so its flagship example is *also* the case that does
> not reach `yolo host apply`. What the correction changes is only that process env is a required
> second channel rather than a last resort; it is still the wrong channel for `CLAUDE_CODE_USE_BEDROCK`,
> and a fragment is still the wrong shape for either.

---

## 6. Credentials — `pack-profiles.md` §4's architecture, minus the scanner

`pack-profiles.md` §4 is the best-argued part of that doc and this design adopts its
architecture: configuration and credentials are decoupled, packs carry `api_key_env` *names*
only (a name-syntax contract, enforced by one regex at
[`validate.go:922-927`](../../internal/config/validate.go#L922-L927)), and credential values
travel the `env_sources` channel — untracked host files, hydrated at launch. As
*recommendation plus mechanism*, that is the whole of it.

**Deliberately not built: a secrets scanner.** A content scan over manifest strings was
proposed in an earlier draft of this section and refused in review (2026-08-30) — it is a
product category of its own (gitleaks, trufflehog, GitHub secret scanning) with goals
orthogonal to yolo's. The in-scope remainder is the structural pieces above and the one gate
below; a user who wants that tripwire runs a scanner in CI over the same git-tracked files.
What adopting §4 leaves open is one asymmetry and one gate:

### 6.1 `env_sources` fails open while the config layer fails closed

The census in [`stringly-typed-references-principle.md`](stringly-typed-references-principle.md)
records `env_sources` as *"Permissive on missing — silent skip with trace log"*, which
[`envsources.go:172-176`](../../internal/config/envsources.go#L172-L176) confirms (a warn, then
continue). That asymmetry is deliberate and right for host portability. But it means the *secret*
channel is the permissive one while the *configuration* channel is fail-closed — so a profile that
resolves perfectly, with an `api_key_env` naming a variable that was never hydrated, produces
exactly the *"mysterious auth failures"* §2 of that principle calls the debugging nightmare.

### 6.2 An activated profile with no credential is a preflight failure

The fix is narrow and it is the highest-value gate in either design:

> **When a profile is ACTIVE and its resolved provider declares `api_key_env: "X"`, and `X` is
> unset in the assembled launch environment, refuse the launch** — naming the variable, the
> provider, the profile, and the `env_sources` entries that were consulted.

Scoped to *active* profiles, so a configured-but-unselected provider with no key on this machine
stays inert, which is the ordinary case for a shared workspace config.

---

## 7. If cross-pack delivery is ever needed, it is `config-overlay` with a `profile` field

I am not arguing cross-pack contribution is never legitimate. I am arguing it already has a kind.
[`KindConfigOverlay`](../../internal/packdecl/kinds.go#L78-L81) is *"a contribution to a config
surface OWNED by another pack"*, `CombineOverlay`, folded after the owner in the shipped stack
(`defaults < host < workspace < config-overlay:<pack> < overlay < computed < transform < managed`,
[`compose.go:355-380`](../../internal/agentcfg/compose.go#L355-L380)) with a **required per-key
provenance label**, `config-overlay:<pack>` ([`compose.go:176`](../../internal/agentcfg/compose.go#L176)),
so an override is legible in `yolo config diff` rather than silent
([`pack-config-collaboration.md`](pack-config-collaboration.md) §8).

So the additive move, if a second consumer ever demands it, is one field:

```jsonc
{ "kind": "config-overlay", "profile": "bedrock", "surface": "claude/settings", "config": { … } }
```

That inherits collision detection, provenance, footprint reporting, and layer placement. Compare
what `pack-fragment` would have to re-invent, all of it unspecified in `pack-profiles.md`:

| Question | `config-overlay` + `profile` | `pack-fragment` as specified |
| :--- | :--- | :--- |
| Two contributors set the same key | `CombineOverlay`, later-wins, **provenance names the winner** | unspecified — silent last-wins |
| Which file does it land in | `surface: "agent/name"` — exact | `target: "claude"` — a pack owns several surfaces; undefined |
| Which layer | `config-overlay`, below `computed`/`managed` | unspecified, and it decides whether an in-jail edit survives |
| Visible in `yolo pack footprint` | yes | no kind → no footprint → invisible |
| Host notch | honored | `config.env` refused ([§5](#5-the-delivery-channel-rule--and-why-it-kills-the-worked-example)) |

**`profile` is a modifier, not a kind.** The same field would apply to `kind: "env"` and
`kind: "launch"` for the jail-only cases. That is the whole of the "Layer 2" story.

---

## 8. Fail-closed, but on the right set

`pack-profiles.md` §8.1 checks `frag.Target` against the **active packs**. Two consequences:

1. `target: "cloude"` with `optional: true` is **silently skipped** — the precise typo the
   [stringly-typed principle](stringly-typed-references-principle.md) exists to catch, waved through
   by the field that was supposed to make opportunism safe.
2. A generic provider pack targeting four agents must mark all four `optional`, after which
   *nothing* about it is verified.

Two different questions are being conflated, and the principle's own Rule 4 separates them:

| Question | Checked against | Severity |
| :--- | :--- | :--- |
| *Does this string name a real pack?* | the resolvable pack **universe** | **fatal, always** — `optional` does not excuse a typo |
| *Is that pack selected this launch?* | the **active** pack list | fatal when required; clean skip when `optional` |

Under this design the same split applies to `pack_profiles.<pack>` (§2.5) and to
`requires_provider` (§3.1): the name must resolve in the universe, always; selection governs only
whether the contribution renders.

---

## 9. Where this differs from `pack-profiles.md`, and why

| # | `pack-profiles.md` | This design | Why |
| :--- | :--- | :--- | :--- |
| D1 | Two new kinds: `provider` + `pack-fragment` | One new kind: `profile` | Three of four proposed mechanisms are shipped (§2.1–2.2). The gap is one selector on one kind (§2.3). |
| D2 | Providers move into pack manifests | Providers stay a config key, schema tightened | A provider is a machine-local fact; a pack per endpoint is a regression, and the two sources' combine rule is unanswered (§4.1). |
| D3 | §2.3: delete core's provider schema; §5.1: add a stricter one | Keep and tighten it, and retract §2.3 | The doc argues both sides; providers are an extension point strangers write, so P2 says the schema stays (§4.3). |
| D4 | Fragments carry inline provider dicts | A pack may only *name* a provider | The untyped path makes the typed one decorative — and the doc's own flagship manifest uses only the untyped path (§4.2). |
| D5 | Cross-pack `target: "<pack>"` | The pack's own declarations only | `autonomy`'s restriction to its own surfaces is what lets it skip a collision rule; fragments give that up and don't replace it (§3.2). |
| D6 | Silent on combine + footprint | `CombineExclusive` by `(pack, name)`, `Claims` stated | [`kinds.go:196`](../../internal/packdecl/kinds.go#L196) makes a footprint structural — a kind without one cannot be registered (P5). |
| D7 | Modeled on the skills broker (§6) | Modeled on `autonomy` | Skills invert the direction for fragments (consumer-declares vs contributor-declares); `autonomy` is literally a two-valued profile (§3.1). |
| D8 | Bedrock delivered as process `env` | Delivered as a managed patch to `claude/settings` | `env` is *refused* at the host notch, which is where the stated motivation lives (§5). |
| D9 | `optional` gates the typo check | Universe-existence always fatal; selection gates rendering | An `optional` typo is silently skipped today under that design (§8). |
| D10 | Secrets: harden `api_key_env` | Schema stays name-only; userinfo refused in `base_url`; active-profile credential preflight (§6.2); **no secrets scanner** | Content scanning is a product category of its own, refused in review — the structural pieces are the in-scope remainder (§6). |
| D11 | Removal scope: 2 sites | 8 sites, plus `--claude-auth` | §2.4. |
| D12 | New 5-step profile merge pipeline (§8) | No new pipeline | The shipped layer fold already composes these inputs; a second stack with no stated relationship to the first is where "which layer wins" goes to die (§7). |

**What I take from `pack-profiles.md` unchanged:** the diagnosis of `agent_profiles` as an inversion
(§2.1), the `assemble.go` verdict (§2.2), the secrets architecture (§4), and the fail-closed
instinct — applied to a different set (§8).

---

## 10. Non-goals

- **Not a provider registry, marketplace, or auto-discovery.** `providers` stays a hand-written map.
- **Not multi-profile composition.** One profile per pack per launch. Stacking (`-p bedrock,fast`)
  is deliberately unbuilt — it needs a precedence rule between two managed patches on one key, and
  no use case has asked for it yet.
- **Not profile-conditional `mount`, `reads-host`, `state`, or `loophole` claims.** Those are
  boundary-crossing claims reviewed at pack-approval time; making them conditional on a launch flag
  would mean an approved pack can claim something the reviewer never saw. Profiles stay inside the
  claims a pack already made.
- **Not a way to make one pack reconfigure another.** That is `config-overlay`, and §7 keeps it
  where it is.
- **Not a migration of `mcp_servers` / `lsp_servers`.** They share the derive-source plumbing but
  have no profile story here.

---

## 11. Risks

| # | Risk | Mitigation |
| :--- | :--- | :--- |
| R1 | The `pack_profiles` rename breaks existing configs and `packs/claude/derive.lua`. | Refuse `agent_profiles` by name with a message that gives the replacement — the pattern `journal`/`host_processes` already set ([AGENTS.md](../../AGENTS.md)). The derive is a one-word edit. |
| R2 | Refusing the cross-pack fragment means a third-party provider genuinely cannot adapt an agent pack it does not control. | §7 is the designed answer, one field on a shipped kind. Ship it the moment a real second consumer appears; the namespace is settled now, which is what [`extension-point-principle.md`](extension-point-principle.md) Rule 6 asks for. |
| R3 | Moving Bedrock env into `claude/settings` depends on Claude Code honoring `settings.json`'s `env` block for these specific variables. | **Unverified — this is the design's load-bearing empirical assumption.** OQ-4. If it does not hold, the fallback is profile-gated `kind: "env"`, jail-only, and §5's host-notch parity is lost for this one case. |
| R4 | `CombineExclusive` by `(pack, name)` forbids a pack splitting one profile across several contributions. | Deliberate — it is the same one-declaration rule `autonomy` has, and it keeps "what does profile X do" answerable by reading one object. |
| R5 | The active-profile credential preflight (§6.2) turns a working-but-degraded launch into a refusal. | Scoped to *active* profiles only, and the message names the variable, the provider and the `env_sources` files consulted. Same disposition as the reachability witness in [AGENTS.md](../../AGENTS.md). |

---

## 12. What I would build, in order

1. **The rename and the two validation gates.** `agent_profiles` → `pack_profiles` everywhere
   (8 sites, §2.4), with the old key refused by name; `pack_profiles` keys validated against the
   resolved pack universe; `-p … -- <bin>` resolving bin → slug and failing when no pack owns the
   bin. This is independently valuable and fixes a live silent-typo hole (§2.5) with no new kind.
2. **`kind: "profile"`**, its footprint entry, and its managed-layer fold — modeled on `autonomy`,
   which means the loader, the render path and the host notch already know the posture shape.
3. **Move Bedrock into `packs/claude`** as a profile patching `claude/settings`, and delete
   [`assemble.go:722-754`](../../internal/cli/run/assemble.go#L722-L754) and `--claude-auth`.
   Verify against R3 *before* deleting the Go path.

   ⚠ **`yolo host` is a prerequisite for the HOST half of this step, not a follow-on** (amended
   2026-08-30). The flags move to `claude/settings`, but the AWS credentials and the
   `unset AWS_PROFILE` cannot — so until the process-env channel exists on the host, a `bedrock`
   profile is jail-only and §2.2's `.bashrc` wrapper cannot actually be deleted. See
   [`host-agent-environment.md` §5](host-agent-environment.md#5-the-recommended-host-environment-architecture).
4. **Tighten the provider schema** (§4.3) and add `requires_provider`.
5. **The credential preflight gate** (§6.2).
6. **`profile` on `config-overlay` / `env` / `launch`** — only when a second real consumer exists.

Steps 1–3 delete more code than they add. Step 6 is the only speculative one, and it is one field.

---

## 13. Alternatives considered

**A. Adopt `pack-profiles.md` as written.** Rejected. Its two new kinds have no combine rule (P5),
its typed layer is bypassable by its own worked example (§4.2), and its flagship case does not reach
the host notch it claims parity for (§5).

**B. Keep providers in config but add `kind: "pack-fragment"` for the cross-pack case only.**
Rejected for v1 — this is `config-overlay` with extra steps (§7). Revisit as step 6, as a field.

**C. Solve Bedrock with profile-gated `kind: "env"` and stop there.** Tempting: it is the minimum
edit that deletes the Go hardcode, and it is the R3 fallback. Rejected as the *primary* path because
it bakes the host-notch blind spot in permanently, for the one case whose stated motivation is a
host-side `.bashrc` wrapper.

**D. Let each pack solve profiles in its own `derive.lua` and add nothing.** This is the status quo,
and it genuinely covers `packs/claude`'s MCP-suppression case today (§2.2). Rejected because a
derive cannot set process env or launch flags, so the residue that forced `assemble.go:722` would
survive — and because "every pack invents its own profile convention in Lua" is the stringly-typed
chaos `pack-profiles.md` §3 correctly warns about, just relocated.

**E. A first-class `profiles.<name>.<pack>` config block that can patch any pack's surfaces.**
Rejected: it makes the *user config* a cross-pack config writer, which is a strictly larger version
of the D5 problem, with no manifest to review.

---

## 14. Open Questions

1. 💬 **OQ-1: Does `kind: "profile"` subsume `kind: "autonomy"`?** Autonomy is a profile with a
   closed two-value selector chosen by the confinement notch rather than by the user. Once `profile`
   exists, `autonomy` could become two reserved profile names selected by policy instead of by
   `-p`. **This decides whether the design adds a kind or replaces one**, and therefore whether
   step 2 of §12 is additive or a migration.

   _Leaning:_ Keep them separate. The selectors have different **authorities** — the notch chooses
   autonomy, the user chooses a profile — and per [`gate-placement-principle.md`](gate-placement-principle.md)
   that is exactly where two mechanisms should stay two. Merging them would let `-p guarded` override
   a notch policy, which is a security regression, not a simplification.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-2: Does `providers` stay flat, or gain a per-profile shape?** Today `providers.<name>` is
   flat and every provider is visible to every pack's derive. A profile currently selects *pack
   behavior*, not *which providers exist*. If two profiles want the same provider name with
   different regions, the flat map cannot express it.

   _Leaning:_ Flat, and let the profile name the provider it wants (`requires_provider`). Two
   regions are two providers (`bedrock-use1`, `bedrock-euw1`), which is greppable and keeps the
   derive contract unchanged. Revisit only if a real config needs the same name twice.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-3: Is a profile name a closed set per pack, or free-form?** `-p glm` against a pack that
   declares no `glm` profile: fatal, or inert? Under §8's split this is the "does the string name a
   real thing" question, and the answer should be fatal — but a profile also reaches the derive as a
   plain string, so a pack can meaningfully honor a name it never declared (which is exactly what
   `packs/claude/derive.lua` does today with `"bedrock"`).

   _Leaning:_ **Fatal if no selected pack declares OR reads the name.** A derive that reads a
   profile name should have to declare it — add `"profiles": ["bedrock", "default"]` as a
   pack-level advertisement, so the namespace is closed and `-p` can be checked and tab-completed.
   This is the one place I would add a field `pack-profiles.md` does not have.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 🔒 **OQ-4: Does Claude Code honor `settings.json`'s `env` block for `CLAUDE_CODE_USE_BEDROCK`
   and `ANTHROPIC_DEFAULT_*_MODEL`?** **§5 is load-bearing on this and it is unverified.** If yes,
   the Bedrock profile is a managed patch and works at both notches, and `assemble.go:722-754` can
   be deleted outright. If no, the fallback is profile-gated `kind: "env"` (alternative C) and the
   host-notch parity claim in §5 must be retracted for this case.

   _Leaning:_ Likely yes — `packs/claude/derive.lua` already writes `env.ENABLE_LSP_TOOL` into that
   surface and it works, so the block is honored for at least one variable. But "honored for a
   yolo-set flag" is not "honored for auth-mode selection read at startup", and the difference is
   measurable in about ten minutes. **Blocked on that measurement, not on a ruling** — and no code
   should be deleted before it.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 🤷 **OQ-5: `-p` with no `--` command.** `yolo -p dev` with three agent packs selected: apply
   `dev` to every pack that declares it, or refuse as ambiguous? `pack-profiles.md` OQ-2 leans
   "globally"; that is a real behavioral choice, not a spelling one.

   _Leaning:_ Apply to every pack that declares the name, and **print which packs it landed on**.
   Silent multi-pack activation is the "silent skip" failure with the sign flipped. If it lands on
   zero packs, that is OQ-3's fatal case.

   **Answer:**
   > _(empty — fill in when decided)_
