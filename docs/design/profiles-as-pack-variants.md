---
title: "A profile is a pack's own variant, not a cross-pack fragment"
date: 2026-08-29
status: accepted
tags: [packs, config, profiles, providers, prism, architecture, secrets]
summary: "Counter-design to pack-profiles.md. Most of what that doc proposes is already shipped: providers are already a typed extension point consumed by three packs' derives, and profile-conditional pack behavior already works in Lua. The real gap is one channel (process env, profile-gated) and one hardcode (now `internal/agentenv`, originally assemble.go:722). So this design adds no new merge engine and no cross-pack fragment kind — it generalizes the shipped `autonomy` contribution from a closed two-valued selector to an open named one, keeps `providers` as a config key with a stricter schema, and fixes the delivery channel so the Claude/Bedrock case works at the host notch too."
---

# A profile is a pack's own variant, not a cross-pack fragment

**Status:** DECIDED, 2026-09-01 — every open question settled (ledger, §14); implementation
underway against §12's build order.

> [!NOTE]
> **Follow-up, 2026-09-01:** the implementation shipped through `980aed71`, including §12 step 6's
> `config-overlay` `profile` gate. A review of it re-measured this doc's claims clean — the
> credential boundary, the three-level skew handling and the backend-parity repairs all hold — and
> found six defects at the edges, five of them needing a ruling. They live in
> [`provider-table-fidelity.md`](provider-table-fidelity.md) (`OQ-PT1`…`OQ-PT5`), not here; §6.2's
> preflight and §4.1's `wire_api` enum are the two sections that row reports against.

Originally DESIGN, 2026-08-29, nothing built. This is a counter-proposal to
[`pack-profiles.md`](pack-profiles.md), written after measuring what that doc's §2 diagnoses
against the code as it stands today. File:line claims were verified on 2026-08-29 and
**re-anchored 2026-09-01** after `784dd209` extracted the profile env out of `assemble.go` —
the extraction is itself the ninth removal site (§2.4).

**The short version.** `pack-profiles.md` is right that `agent_profiles` is an inversion and that
the Bedrock hardcode (originally `assemble.go:722`, now
[`internal/agentenv`](../../internal/agentenv/agentenv.go#L61-L94)) must die. It is wrong about
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
[`pack-system.md`](pack-system.md) (the kind registry and the footprint model),
[`zai-plumbing.md`](zai-plumbing.md) (the first real consumer — worked examples for both provider
routes, and the endpoint-by-protocol resolution).

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
   credential-pointer fields (`api_key_env_name`) hold env-var names by contract
   ([`validate.go:922-927`](../../internal/config/validate.go#L922-L927)), credential values
   travel `env_sources`, and §4.3 refuses userinfo in `base_url`. A pack is a *distribution
   artifact* — fetched from a git remote, approved at a commit — which is why the recommendation
   matters; yolo's part is that its schema never offers a sanctioned slot for a credential
   value, so what it validates cannot carry one. What this design does **not** add is a secrets
   scanner (§6).

**Verdict.** Ship a rename, one new contribution kind, one new field on `providers`, and three
validation gates. Do not ship a cross-pack fragment merge engine — and if one is ever needed, it is
`config-overlay` with a `profile` field, not a new kind ([§7](#7-if-cross-pack-delivery-is-ever-needed-it-is-config-overlay-with-a-profile-field)).
**The thread to carry through §3–§5:** no objection in this doc is to cross-pack contribution
*existing* — it ships today as `config-overlay`. Every objection is to a second mechanism for it
that skips the questions `config-overlay` already answers.

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

A derive writes **config files**. It cannot set a **process environment variable** — an
architectural boundary, not a missing capability: derives run at *compose* time (boot and
check), and process env is a *launch*-time concern. Claude Code's
Bedrock mode needs `CLAUDE_CODE_USE_BEDROCK`, `AWS_REGION`, and `ANTHROPIC_DEFAULT_*_MODEL` in the
environment — and `kind: "env"` is *static only*, literal strings with no selector
([`kinds.go:96-99`](../../internal/packdecl/kinds.go#L96-L99),
[`packload.go:241-251`](../../internal/packload/packload.go#L241-L251)).

There was no profile-gated env channel, so the Bedrock case went into Go — originally at
`assemble.go:722`, and since extracted wholesale into core's own package *(re-anchored 2026-09-01;
the extraction is itself the ninth site, §2.4)*:

```go
// internal/agentenv/agentenv.go:66 — the whole reason pack-profiles.md exists,
// moved out of assemble.go by 784dd209 but still core, still hardcoded
if agent == "claude" && profile == "bedrock" {
    return bedrockVars(region, models)   // CLAUDE_CODE_USE_BEDROCK, AWS_REGION, ANTHROPIC_DEFAULT_*_MODEL
}
```

**That is the gap — and it is narrower than "no env channel."** The channel itself exists at both
notches now: the host launch wrapper set ([`hostwrap`](../../internal/hostwrap/hostwrap.go)) can
put every installed program's invocation through `yolo host --`, where environment composition happens
at LAUNCH time from live config — one implementation, decided in one place — once the user opts in
(`host_wrappers: true` and the wrap dir on `PATH`; P6 states the qualifier). What the channel
cannot do is *vary by profile*, because `kind: "env"` is static. So the gap is the selector, and
this design builds it as the new kind's own `env` field (§3.1) rather than a modifier on `env` —
and
[§5](#5-the-delivery-channel-rule--and-why-it-kills-the-worked-example) still routes the
non-secret half of this case to the config file, because invocations that bypass yolo (a bare
`claude` from a shell without the wrap dir on `PATH`, cron, an IDE-configured absolute path) are
exactly the ones a config surface survives.

### 2.4 Two inversions the diagnosis missed

`pack-profiles.md` §2.1–2.2 names `agent_profiles` and `assemble.go`. Core names an agent in two
more places, and a full removal has to take them:

- **`--claude-auth` / `--auth`** ([`runcmd.go:99`](../../internal/cli/runcmd.go#L99),
  [`runcmd.go:166-173`](../../internal/cli/runcmd.go#L166-L173)) is an agent name in the CLI
  surface, feeding `o.ClaudeAuth` → `out.Set("claude", …)`
  ([`assemble.go:764-766`](../../internal/cli/run/assemble.go#L764-L766)). So is
  **`--agent-profile <agent>=<provider>`** ([`runcmd.go:52`](../../internal/cli/runcmd.go#L52),
  [`runcmd.go:175-194`](../../internal/cli/runcmd.go#L175-L194)), a second flag whose fate the
  rename has to decide *(found in the 2026-09-01 audit; the site census below predates it)*. A
  design that deletes `agent_profiles` and leaves either flag has moved the inversion, not
  removed it.
- **The full `agent_profiles` footprint is ten sites, not two** *(re-anchored 2026-09-01 after
  `784dd209` extracted the profile env into `internal/agentenv`)*: `config.go:67`,
  `validateAgentProfiles` at [`validate.go:947-967`](../../internal/config/validate.go#L947-L967),
  `inherit.go:132`, `sources.go:20-21`, `derive.go:180`,
  `entrypoint/providers.go:27-41` (`LoadAgentProfiles`), the `YOLO_AGENT_PROFILES` env channel at
  [`assemble.go:720`](../../internal/cli/run/assemble.go#L720), `effectiveAgentProfiles` at
  [`assemble.go:751-777`](../../internal/cli/run/assemble.go#L751-L777), the bin-keyed `-p` read
  at [`assemble.go:767-775`](../../internal/cli/run/assemble.go#L767-L775), and **the hardcode's
  new home, [`internal/agentenv`](../../internal/agentenv/agentenv.go#L61-L94)** — the extraction
  that made this list true. `pack-profiles.md` §9 claims the design "deletes all hardcoded `claude`
  Bedrock checks from `assemble.go` and removes `knownProviderKeys`" — that is under half of it.

### 2.5 The stringly-typed hole that is live today

[`stringly-typed-references-principle.md`](stringly-typed-references-principle.md) originally
listed `pack_profiles.<pack_name>` as "Required / Validation error if pack is not in configured
universe." **That census row was wrong when this doc measured it** — `validateAgentProfiles`
([`validate.go:947-967`](../../internal/config/validate.go#L947-L967)) checks only that the value
is a string; the *key* is never checked against anything — and the principle doc has since been
amended to agree (its row now reads *"**Unchecked.** … `{"cloude": "bedrock"}` returns
[PASS]"* with the fatal-against-the-universe disposition). The bug it records is still live in
the code, so today:

```jsonc
{ "agent_profiles": { "cloude": "bedrock" } }   // accepted silently, does nothing
```

And the CLI keys the profile by the **binary basename**
([`assemble.go:767-775`](../../internal/cli/run/assemble.go#L767-L775)). That keying is right and
this design keeps it *(amended 2026-08-30, review)*: the CLI-name namespace is already exclusively
owned — `program` and `launch` are `CombineExclusive` by `bin`, so two packs claiming one CLI name
is a load error today, and `bin` is an explicitly declared bare program name in every install
contribution. A CLI name therefore resolves to at most one pack, and "the agents" are simply the
union of the bins the selected packs install. What is broken is only that an *unknown* key is
accepted silently; the fix is fatal-on-unknown against that namespace
([§8](#8-fail-closed-but-on-the-right-set)), not re-keying by pack slug.

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

**`autonomy` and `profile` stay two kinds** *(OQ-1, settled 2026-09-01 — ledger)*. The posture
body is shared because the SHAPE generalizes, but the selectors have different authorities: the
confinement notch chooses autonomy — unreachable from config or CLI by construction, fail-closed
(`Target.Profile()` derives it only from the notch) — while the user chooses a profile through
channels a workspace config can write. Merging them would put a notch-owned decision behind a
user-owned selector.

> [!WARNING]
> The dangerous direction is `-p autonomous` **at the host notch**, not `-p guarded`: a real host
> gaining the agent's permission bypass re-creates the §4.2 leak verbatim. Prompts-appearing-in-a-
> jail is the side the code explicitly calls the non-regression
> ([`confinement.go:149-153`](../../internal/render/confinement.go#L149-L153)). An earlier draft
> of this doc named the wrong direction; the correction is load-bearing for anyone tempted to
> re-litigate the split.

Field semantics:

| Field | Type | Required | Meaning |
| :--- | :--- | :--- | :--- |
| `name` | `string` | **Yes** | The profile name this variant answers to. Unique within the pack. |
| `config` | surface-patch list | No | Patches the **managed** layer of a surface **this same pack owns**, identified by `agent`/`name` — byte-identical to `AutonomyPosture.Config` ([`contributes.go:347-356`](../../internal/packdecl/contributes.go#L347-L356)). |
| `launch` | `[{bin, flags}]` | No | Flags merged into the binary's launch flags, same shape as `AutonomyLaunch`. |
| `env` | `map[string]string` (value may be `null`) | No | Static process env delivered wherever yolo launches the process — both notches, refused only by the `host apply` verb (P6). A `null` value UNSETS the variable, the merge-patch convention `providers` already uses (`validate.go` treats null as disable). [§5](#5-the-delivery-channel-rule--and-why-it-kills-the-worked-example) says when the config surface is the better channel. |
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

`pack-fragment`, read fairly, is not a competing author but a *function over a pack* — it runs at
a defined point and transforms what the pack produced, the same posture as the shipped
`transform` layer, composing rather than competing. Fair; the objection narrows but stands,
because a function over another pack's output still owes four answers: **which file** it lands in
(`target: "claude"` names a pack, and a pack owns several surfaces), **when** it runs (which layer
of the stack — this decides whether an in-jail edit survives), **what two contributors on one key
do**, and **whether the result is legible after the fact** (provenance). `pack-profiles.md`
specifies none of the four. The shipped stack already contains the slots that answer them —
`config-overlay` for declared cross-pack contributions with per-key provenance, `transform` for
functions — which is the thread of this whole design: cross-pack contribution is legitimate and
already has mechanisms; a new kind that answers none of the four questions is a second door into
the same room. [§7](#7-if-cross-pack-delivery-is-ever-needed-it-is-config-overlay-with-a-profile-field)
is the one additive field the real cases need.

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
| `-p <name> -- <bin>`, keyed by bin basename | same keying, now checked: fatal if no pack owns that bin; `-p <name>` **without** a command sets the name for EVERY selected pack (OQ-5's ruling — Decision Ledger) | §2.5 — the CLI-name namespace is exclusive by construction. |

Resolution order is the shipped one, extended by nothing: workspace config < user config < CLI. The
selected profile name for CLI *c* is then a plain string that (a) gates the `kind: "profile"`
contributions of the pack that owns *c* and (b) reaches that pack's derive as
`ctx.pack_profiles[c]` — the same CLI-keyed table `ctx.agent_profiles` is today. Every derive
receives the **whole** table (one table for all packs,
[`derive.go:160-180`](../../internal/agentcfg/luahook/derive.go#L160-L180)), so a pack owning no
bin — a provider pack — still reads any CLI's selected name. **The launch line prints what the
name landed on: DECLARED** (which selected packs ship a `kind: "profile"` with that name) **and
RECEIVED** (every selected pack, since all derives get the table). *"Honored"* is deliberately
not claimed — what a derive does with the string is unobservable, and a transparency print that
overclaims is the silent-skip failure wearing a badge.

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
not a new one. The same later-wins rule governs the profile's `env` and `launch` against the
pack's **own static** contributions of those kinds *(added 2026-09-01; the rule was unstated)*:
the profile is the more specific intent, declared after the baseline, and it wins — a pack whose
static `kind: "env"` sets a key its profile also sets is not a load error, it is a variant
overriding its own default.

---

## 4. Providers stay a config key — and the schema gets stricter, not deleted

### 4.1 The contribution kind — ruled in 2026-09-01 *(reversing this section's original verdict)*

`pack-profiles.md` §5.1 moves providers into pack manifests; this section originally argued that
was backwards ("a provider is a machine-local fact"). **The maintainer reversed it, and the
reversal is right** (OQ-12): the claim was half-true. The **credential pointer is machine-local**
(`api_key_env_name` names a variable only this machine hydrates), but the **endpoints are facts
about the service** — z.ai's URLs and wire protocols are the same for every user, which is exactly
what a shareable pack exists to carry. So `kind: "provider"` ships the service facts; user config
keeps the key pointer and any overrides; the composed map (pack defaults < user overrides) feeds
the unchanged `ctx.providers` table and the three derives. A personal endpoint is still four lines
of user config — an override of nothing — so the ergonomic argument survives as "overrides, not
authoring." P5's answer for the kind: sole-owned by provider NAME across packs (two packs shipping
`zai` collide), `Claims: "a named provider's endpoints, wire protocols and model aliases, with the
credential supplied by user config"`.

The narrow thing a pack *does* legitimately want is to say **"I need a provider named X"** —
`requires_provider` in §3.1 — which is an assertion, not a definition (the three things the word
"provider" can mean, separated and worked end-to-end:
[`zai-plumbing.md`](zai-plumbing.md) §1) — and behaves like
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
check, the `wire_api` enum, the `api_key_env_name` regex, the §4.3 credential refusal — is bypassed by
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
- **`api_key_env` is renamed `api_key_env_name`** *(OQ-6, ruled 2026-09-01)* — the value is the
  NAME of the env var, and the spelling now says so before the regex has to. The old key is
  refused by name with the replacement in the message, the `agent_profiles` pattern.
- **`models` gets a documented alias vocabulary** (`default`, `fast`, `coder`, …), because
  [`agentenv.go:84-88`](../../internal/agentenv/agentenv.go#L84-L88) reads
  `default`/`haiku`/`sonnet` — Claude-specific alias names in core, a
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

**`yolo host apply` refuses `kind: "env"` — the COMMAND, not the notch** *(opening amended
2026-08-31 for the wrapper era)*. Not unimplemented — *refused*, with a written
reason ([`fieldset.go:99-103`](../../internal/render/fieldset.go#L99-L103)):

> *"env vars apply to a process yolo starts, and `yolo host apply` only configures your tools — it
> never runs them. Setting them for your whole session would mean editing your shell rc, a much
> larger claim than a pack's env contribution asks for. `yolo host -- <program>` delivers them at
> launch instead, to that process only."*

The refusal is scoped to `apply` because `apply` never runs a process. The host *notch* can deliver
env, and since [`hostwrap`](../../internal/hostwrap/hostwrap.go) landed it does: every installed
program's invocation can pass through `yolo host --`, where environment composition happens at
launch from live config. So the accurate statement is narrower than the one this section originally
opened with: **process env is deliverable at both notches, but only through a channel with yolo (or
its wrapper) in the launch path** — `host apply` alone, a bare invocation from a shell without the
wrap dir on `PATH`, cron, and an IDE-configured absolute path all miss it.

`pack-profiles.md` §9 claims alignment with [`happy-path-principle.md`](happy-path-principle.md) —
*"One unified merge pipeline across the entire matrix: Linux containers, macOS Apple Container,
`macos-user`, and Host Render Target (`yolo host apply`)"*. Its worked example is **pure `config.env`**.
So the flagship case does not reach the apply verb the doc claims parity for — and the host notch,
where the design's stated downstream motivation lives, is served by the launch verb and wrapper
they did not build
([`host-agent-environment.md` §2.2](host-agent-environment.md#22-real-world-case-study-obviating-bashrc-wrapper-functions):
obviating the `.bashrc` `claude()` wrapper).

**The rule this implies — corrected 2026-08-30.** The first version of this section stated a
*preference order* (config first, env as last resort). That is wrong for half the payload, and the
half it is wrong for is the half that carries credentials:

> [!WARNING]
> **A config file ROUTES a credential; it cannot DELIVER one.** `api_key_env_name` / `apiKeyEnv` /
> `{env:VAR}` all write the **name** of a variable the agent then reads from **its own process
> environment** — verified against the three shipped derives in
> [`host-agent-environment.md` §4](host-agent-environment.md#4-per-agent-host-capabilities-matrix).
> So there is no "fallback" here: any bring-your-own-key provider is unusable on the host without a process-env
> channel, for every agent. A preference order cannot express that, because the two channels are not
> ranked — they carry different things.

> **P6 restated (corrected).** Split by **payload type**, not by preference and not per agent.
> **Configuration** — endpoints, model aliases, `wire_api`, permissions, and the `api_key_env_name`
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
any invocation at both notches. The **secret** half (AWS credentials, and
[`host-agent-environment.md`](host-agent-environment.md) §2.2's `unset
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
architecture: configuration and credentials are decoupled, packs carry `api_key_env_name` *names*
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
records `env_sources` as *warn + skip* (its row has since absorbed this section's gate as the
disposition), which
[`envsources.go:172-176`](../../internal/config/envsources.go#L172-L176) confirms (a warn, then
continue). That asymmetry is deliberate and right for host portability. But it means the *secret*
channel is the permissive one while the *configuration* channel is fail-closed — so a profile that
resolves perfectly, with an `api_key_env_name` naming a variable that was never hydrated, produces
exactly the *"mysterious auth failures"* §2 of that principle calls the debugging nightmare.

### 6.2 An activated profile with no credential is a preflight failure

The fix is narrow and it is the highest-value gate in either design:

> **When a SELECTED PACK requires a provider and either half is missing, refuse the launch** —
> *(rescoped 2026-09-01, OQ-13: selection, not profile activation — the earlier active-profile
> scoping is withdrawn)* the composed provider map lacks the named provider, or the provider's
> `api_key_env_name` variable is unset in the assembled launch environment. The refusal names the
> variable, the provider, the requiring pack, and the `env_sources` entries consulted. Escape
> hatch: `YOLO_ALLOW_MISSING_PROVIDERS=1`, forwarded from the host env — the reachability
> witness's `YOLO_ALLOW_UNREACHABLE_SERVICES` pattern. Selecting a provider pack is the intent;
> intent without its credential is a launch that would fail at first request anyway.

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

**Is `config-overlay` already what `pack-fragment` wanted? Yes — with one field missing, and the
env half answered elsewhere.** Decompose what a fragment was *for* — a provider pack adapting an
agent pack it does not control — into the payloads it actually delivers, and every row already
has a home:

| The fragment delivers | Where that lives today | What is missing |
| :--- | :--- | :--- |
| Settings in the agent's own config file | `config-overlay` — exactly this: a declared contribution to another pack's surface, later-wins, per-key provenance | the `profile` field, below |
| Env vars in the process (`AWS_REGION`, `AWS_PROFILE`) | the provider pack's **own** `kind: "env"` — env is ambient to the launch, so `target: "claude"` on an env payload was incoherent: there is no per-agent env to aim at. One writer per key ([`kinds.go:96-99`](../../internal/packdecl/kinds.go#L96-L99)) | the selector — this design's `kind: "profile"`, or the `profile` modifier on `env` |
| Launch flags for the agent binary | nowhere, cross-pack — `launch` is sole-owned by bin ([`kinds.go:100`](../../internal/packdecl/kinds.go#L100)) | nothing — `PackFragmentSpec` ([§5.2](pack-profiles.md)) has no launch field, so this is off the wish-list; and owner-only argv is a trust property (`--dangerously-skip-permissions` is a flag) |
| An inline provider definition (`base_url`, models, keys) | not a fragment job at all — `providers` is a config key; a pack *names* one (`requires_provider`, §4.1) | nothing — §4.2 |

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

### 7.1 Feature-complete against the fragment wish-list

`PackFragmentSpec` ([`pack-profiles.md` §5.2](pack-profiles.md)) is four fields. Audited:

| Fragment field | `config-overlay` + `profile` | Verdict |
| :--- | :--- | :--- |
| `target: "<pack>"` | `surface: "agent/name"` | shipped, finer-grained — a pack owns several surfaces, and the fragment left which-file undefined |
| `optional` (fatal when `false` and target unselected) | **no field — selection is the optionality**: owner unselected → clean skip; surface names nothing real → fatal | [§8](#8-fail-closed-but-on-the-right-set)'s split, applied; strictly simpler, and it deletes the field whose misuse (an `optional` typo waved through) §8 exists to catch |
| `profile` | the one additive field, above | this design |
| `config` (JSON Merge Patch) | `CombineOverlay` — merge-patch **plus** per-key provenance | shipped, stronger |

Nothing on the wish-list is missing. The one deliberate divergence is `optional`: a provider
pack selected without its target skips cleanly rather than failing, because the user's selection
*is* the requirement — a pack cannot demand the selection of another pack. A provider whose
credential never arrives is still caught, loudly, by the [§6.2](#62-an-activated-profile-with-no-credential-is-a-preflight-failure)
preflight rather than by a required-overlay error.

### 7.2 The name

Both names were on the table and neither is loved: `config-overlay` (shipped) and
`pack-fragment` (proposed). Candidates weighed:

- **`patch`** — the body literally *is* a JSON Merge Patch (`PackFragmentSpec`'s own field table
  says so), and [RFC 7386](https://www.rfc-editor.org/rfc/rfc7386) is the stable external
  reference a defined term wants. Rejected: "patch" reads ephemeral — a diff applied once —
  while this is a standing declaration re-composed at every boot and check.
- **`amendment`** — the truest fit for the *relationship*: sponsored, ordered, recorded,
  additive to another's document. Rejected: coined where a plain word already works, and it
  names the relationship but not the landing zone, which is the part an author must not
  misunderstand.
- **`adapter`** — names the purpose (adapt X to Y). Rejected: adapters translate, and the
  derives are the translators; this kind only delivers.
- plain **`overlay`** — rejected: the layer family already spends the word.

**Recommendation: keep `config-overlay`.** "Overlay" does consistent, load-bearing work in a
family — the compose stack carries `config-overlay` (contributions from other packs) beside
`capture-overlay` (in-jail edits) ([`compose.go:48-62`](../../internal/agentcfg/compose.go#L48-L62),
[`pack-config-collaboration.md`](pack-config-collaboration.md)) — and the provenance label
`config-overlay:<pack>` is shipped and user-visible in every `yolo config diff`. The name states
exactly what the mechanism guarantees: *where* the contribution lands, as a layer over the
owner's config. `pack-fragment` was the genuinely bad name — a fragment *of what*; it names
incompleteness instead of a relationship. If the maintainer overrules anyway, the cost is at its
lifetime minimum now — one migration, old kind refused by name with the replacement named,
exactly the `agent_profiles` → `pack_profiles` mechanism [§3.3](#33-the-selector-and-where-it-comes-from)
uses — but every alternative trades a shipped, coherent, provenance-visible name for a shorter
one.

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

Under this design the same split applies to `pack_profiles.<cli>` (§2.5) and to
`requires_provider` (§3.1): the name must resolve in the universe — for a profile, the
CLI names owned by resolvable packs — always; selection governs only
whether the contribution renders.

---

## 9. Where this differs from `pack-profiles.md`, and why

| # | `pack-profiles.md` | This design | Why |
| :--- | :--- | :--- | :--- |
| D1 | Two new kinds: `provider` + `pack-fragment` | One new kind: `profile` | Three of four proposed mechanisms are shipped (§2.1–2.2). The gap is one selector on one kind (§2.3). |
| D2 | Providers move into pack manifests | Providers stay a config key, schema tightened | A provider is a machine-local fact; a pack per endpoint is a regression, and the two sources' combine rule is unanswered (§4.1). |
| D3 | §2.3: delete core's provider schema; §5.1: add a stricter one | Keep and tighten it, and retract §2.3 | The doc argues both sides; providers are an extension point strangers write, so P3 says the schema stays (§4.3). |
| D4 | Fragments carry inline provider dicts | A pack may only *name* a provider | The untyped path makes the typed one decorative — and the doc's own flagship manifest uses only the untyped path (§4.2). |
| D5 | Cross-pack `target: "<pack>"` | The pack's own declarations only | `autonomy`'s restriction to its own surfaces is what lets it skip a collision rule; fragments give that up and don't replace it (§3.2). |
| D6 | Silent on combine + footprint | `CombineExclusive` by `(pack, name)`, `Claims` stated | [`kinds.go:196`](../../internal/packdecl/kinds.go#L196) makes a footprint structural — a kind without one cannot be registered (P5). |
| D7 | Modeled on the skills broker (§6) | Modeled on `autonomy` | Skills invert the direction for fragments (consumer-declares vs contributor-declares); `autonomy` is literally a two-valued profile (§3.1). |
| D8 | Bedrock delivered as process `env` | Delivered as a managed patch to `claude/settings` | `env` is refused by the *apply verb*, and the flagship case's motivation lives at the host notch apply cannot reach (§5, amended). |
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
| R3 | Moving Bedrock env into `claude/settings` depends on Claude Code honoring `settings.json`'s `env` block for these specific variables. | **Retired by measurement 2026-08-31 (OQ-4)** — the settings `env` block IS honored, applied before the first API call; witness `ANTHROPIC_BASE_URL`. The Bedrock-mode var rides the same mechanism; a re-test with AWS creds is cheap insurance before deleting the Go path. |
| R4 | `CombineExclusive` by `(pack, name)` forbids a pack splitting one profile across several contributions. | Deliberate — it is the same one-declaration rule `autonomy` has, and it keeps "what does profile X do" answerable by reading one object. |
| R5 | The active-profile credential preflight (§6.2) turns a working-but-degraded launch into a refusal. | Scoped to *active* profiles only, and the message names the variable, the provider and the `env_sources` files consulted. Same disposition as the reachability witness in [AGENTS.md](../../AGENTS.md). |

---

## 12. What I would build, in order

1. **The rename and the two validation gates.** `agent_profiles` → `pack_profiles` everywhere
   (8 sites, §2.4), with the old key refused by name; `pack_profiles` keys validated against the
   CLI-name namespace — bins owned by resolvable packs — with unknown names fatal (§2.5, §8);
   `-p … -- <bin>` failing when no pack owns that bin. This is independently valuable and fixes a
   live silent-typo hole (§2.5) with no new kind.
2. **`kind: "profile"`**, its footprint entry, and its managed-layer fold — modeled on `autonomy`,
   which means the loader, the render path and the host notch already know the posture shape.
3. **Move Bedrock into `packs/claude`** as a profile patching `claude/settings`, and delete
   the Bedrock hardcode (now
   [`internal/agentenv:61-94`](../../internal/agentenv/agentenv.go#L61-L94), originally
   `assemble.go:722-754`) and `--claude-auth`.
   R3 is measured and retired (OQ-4, 2026-08-31); a cheap AWS-creds re-test of
   `CLAUDE_CODE_USE_BEDROCK` itself is the remaining insurance *before* deleting the Go path.

   ⚠ **`yolo host` is a prerequisite for the HOST half of this step, not a follow-on** (amended
   2026-08-30). The flags move to `claude/settings`, but the AWS credentials and the
   `unset AWS_PROFILE` cannot — so until the process-env channel exists on the host, a `bedrock`
   profile is jail-only and [`host-agent-environment.md`](host-agent-environment.md) §2.2's
   `.bashrc` wrapper cannot actually be deleted. See
   [`host-agent-environment.md` §5](host-agent-environment.md#5-the-recommended-host-environment-architecture).
4. **Tighten the provider schema** (§4.3) and add `requires_provider`.
5. **The credential preflight gate** (§6.2).
6. **`profile` on `config-overlay`** — SHIPPED 2026-09-01 (commits 568d5a3a + 980aed71), `packs/zai` the first consumer; the env/launch spellings stay unshipped (subsumed by the profile kind's own fields).

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
derive cannot set process env or launch flags, so the residue that forced the agentenv hardcode would
survive — and because "every pack invents its own profile convention in Lua" is the stringly-typed
chaos `pack-profiles.md` §3 correctly warns about, just relocated.

**E. A first-class `profiles.<name>.<pack>` config block that can patch any pack's surfaces.**
Rejected: it makes the *user config* a cross-pack config writer, which is a strictly larger version
of the D5 problem, with no manifest to review.

---

## 14. Decision Ledger

Every question this design asked is settled — none remain open. Rulings are woven into the body
sections named below; the ledger is the greppable index. IDs are stable — sibling docs and code
comments cite them.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-1 | **Keep `autonomy` and `profile` as two kinds.** Corpus-forced three ways: environment-manager OQ-11 (resolved 2026-08-01, shipped) placed the confinement-conditional keys in the autonomy kind "and nowhere else" so unconditional config cannot contain a bypass; OQ-5 made profile values free-form and derive-reaching, leaving no expressible way to reserve two names; and the selector asymmetry IS the shipped §4.2 fix — autonomy keys off the constructor-only, fail-closed notch (`Target.Profile()`), while profile names arrive through the workspace-over-user merge an agent can edit (gate-placement Test 1). | 2026-09-01 | §3.1 |
| OQ-2 | **`providers` stays flat.** A profile selects a provider by name (`requires_provider`); it never reshapes the map. Where one key must produce many endpoint shapes, the shape axis lives INSIDE the entry (`endpoints`, per the zai OQ-Z2 ruling) — which supersedes this doc's earlier "two regions are two providers" whenever the variants share one credential. | 2026-09-01 | §4.1 |
| OQ-3 | **Profile names are free-form and global; values are never name-checked.** Entailed by OQ-5's ruling. Config KEYS stay checked against the CLI-name namespace (§2.5, §8); typo defense is the launch line's DECLARED/RECEIVED print, not a gate. | 2026-09-01 | §3.3 |
| OQ-4 | **Claude Code honors `settings.json`'s `env` block before the first API call — YES, measured.** Controlled listener experiment, claude 2.1.252, scratch `CLAUDE_CONFIG_DIR`, inherited `ANTHROPIC_*` scrubbed: settings-only `ANTHROPIC_BASE_URL` produced traffic identical to the process-env control. Witness var is `ANTHROPIC_BASE_URL`; `CLAUDE_CODE_USE_BEDROCK` rides the same mechanism (a cheap AWS-creds re-test is the insurance before deleting the Go path). R3 retired. | 2026-08-31 | §5, §12.3 |
| OQ-5 | **`-p <name>` is global, declared or not** — the active profile name reaches every selected pack; consistency is the point. *(Ruled by the maintainer; supersedes the declare-only leaning and withdrew OQ-3's fatality.)* | 2026-08-31 | §3.3 |
| OQ-7 | **The profile `env` field may carry `null` to UNSET a variable** — the merge-patch convention `providers` already uses; `unset` was named as a payload in §5 with no field able to express it. | 2026-09-01 | §3.1 |
| OQ-8 | **A profile's `env`/`launch` later-wins over the pack's own static contributions** — the variant overrides its own default; not a load error. | 2026-09-01 | §3.4 |
| OQ-9 | **A missing provider is the same refusal as an unhydrated key** — `requires_provider` naming nothing in `providers` is fatal with the §6.2 message shape; escape hatch `YOLO_ALLOW_MISSING_PROVIDERS=1`. | 2026-09-01 | §6.2 |
| OQ-10 | **The launch line prints DECLARED and RECEIVED, never "honored"** — what a derive does with the string is unobservable; a print that overclaims is the silent-skip failure wearing a badge. | 2026-09-01 | §3.3 |
| OQ-11 | **The removal census is ten sites plus two flags** — the 2026-08-29 list of eight predates `internal/agentenv` (the extraction itself) and missed `--agent-profile` beside `--claude-auth`. | 2026-09-01 | §2.4 |
| OQ-6 | **Rename `api_key_env` → `api_key_env_name`** — the value's type is the last word read; no convention required. *(Ruled by the maintainer.)* One refuse-by-name migration: `knownProviderKeys`, the regex error message, three derives, docs. | 2026-09-01 | §4.3 |
| OQ-12 | **D2 REVERSED — `kind: "provider"` exists.** A pack ships a provider's SERVICE facts (endpoints by protocol, wire_api, model aliases); the user's config carries only the credential pointer (`api_key_env_name`) and overrides. The composed provider map is pack-defaults < user-overrides, feeding the unchanged `ctx.providers` table. What survives P3 is the narrower truth: the *credential* is machine-local; the *endpoints* are facts about the service, the same for everyone — exactly what a shareable pack carries. *(Ruled by the maintainer, 2026-09-01.)* | 2026-09-01 | §4.1 |
| OQ-13 | **Preflight scope: SELECTED PACK, not active profile.** A selected pack whose required provider (or its key) is missing refuses the launch, unconditionally — "configured-but-unselected stays inert" is withdrawn; selecting a provider pack IS the intent. *(Ruled by the maintainer, 2026-09-01; supersedes §6.2's scoping.)* | 2026-09-01 | §6.2 |
| OQ-14 | **No agent is special-cased — claude included.** The provider declares its per-protocol ENV SHAPE (anthropic → `ANTHROPIC_BASE_URL` from the endpoint, `ANTHROPIC_AUTH_TOKEN` from the hydrated key); when a profile naming that provider is active for an agent, launch-time env composition applies it. The launcher mapping a hydrated credential into the process env is the §5 payload split's own channel — NOT the config-file interpolation the 2026-08-03 `${VAR}` ruling removed. No user-written token alias, no per-agent overlay bridge. *(Ruled by the maintainer, 2026-09-01.)* | 2026-09-01 | §5 |
| OQ-15 | **`env_shape` is user-overridable** — it is a service fact like endpoints and models, and the override story OQ-12 promised ("user config carries the credential pointer and overrides") has a hole without it: the validator refused the key while ComposeProviders merged it. `knownProviderKeys` gains `env_shape`, validated with the same closed placeholder set the pack path enforces (`{endpoint}`/`{key}`/`{region}`/`{model:<alias>}`) — users override the shape, they cannot smuggle templates. *(Found by the implementation review as a validator/merger vocabulary disagreement; ruled during fix-forward 2026-09-01.)* | 2026-09-01 | §4.1 |
| OQ-17 | **The host-notch overlay gate reads USER-SCOPE config only.** A gated `config-overlay` rewrites real-home keys (`ANTHROPIC_BASE_URL`), and a workspace `yolo-jail.jsonc` is agent-editable — gate-placement Test 1's own worked example — so `yolo host apply` / `config diff` gate on the user config's `pack_profiles` alone (the same source the host env composition already reads). The JAIL notch uses the full effective table (workspace < user < CLI): inside a jail the blast radius is the disposable home. *(Decided during step 6's implementation, 2026-09-01.)* | 2026-09-01 | §7 |
| OQ-16 | **SHIPPED (was: the config-file half deferred where the profile does not own the surface).** Step 6 landed 2026-09-01: `config-overlay` + `profile` gates a cross-pack contribution on the active profile (clean skip when inactive, provenance and footprint inherited, the field refused on other kinds for now), and `packs/zai` ships the first consumer — `ANTHROPIC_BASE_URL` into `claude/settings`' env block, surviving any invocation. The original deferral cost (bare-`claude` coverage for zai) is retired. | 2026-09-01 | §7, §12.6 | packs/claude's bedrock profile patches its own `claude/settings` env block (OQ-4's measured landing), so the non-secret Bedrock half survives bare invocations. A provider pack reaching an agent surface it does not own — zai→claude/settings — needs `config-overlay` + the `profile` field (§12 step 6, deliberately deferred), so in v1 `env_shape` launch composition is that case's only channel and a bare `claude` outside yolo's launch path gets nothing from packs/zai. The documented cost of deferral, not a retraction of §5's split. *(Ruled during fix-forward 2026-09-01 after the review caught the docs promising a landing no code shipped.)* | 2026-09-01 | §5, §12.6 |

*(OQ-7…OQ-11 were found unset by the 2026-09-01 completeness audit — decisions a builder would
otherwise have made silently — and settled from the corpus in the sections named.)*

